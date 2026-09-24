package niri

import (
	"sync"
	"testing"
	"time"
)

func strptr(s string) *string { return &s }

func u64(v uint64) *uint64 { return &v }

func windowFixture() *Window {
	workspaceId := uint64(1)
	return &Window{
		Id:          42,
		Title:       strptr("⠋ π - jwu"),
		AppId:       strptr("com.mitchellh.ghostty"),
		WorkspaceId: &workspaceId,
		Layout: WindowLayout{
			PosInScrollingLayout:   &Vec2[uint32]{X: 1, Y: 1},
			TileSize:               Vec2[float64]{X: 700, Y: 1200},
			TilePosInWorkspaceView: &Vec2[float64]{X: 0, Y: 0},
		},
	}
}

func TestTitleOnlyChangeDoesNotNeedARedraw(t *testing.T) {
	s := NewNiriState()
	s.Update(&WindowOpenedOrChanged{Window: *windowFixture()})
	layout, focus := s.Versions()

	changed := windowFixture()
	changed.Title = strptr("⠙ π - jwu")
	s.Update(&WindowOpenedOrChanged{Window: *changed})

	if gotLayout, gotFocus := s.Versions(); gotLayout != layout || gotFocus != focus {
		t.Fatal("title-only change must not request a redraw (it causes hover flicker)")
	}
	if got := *s.windows[42].Title; got != "⠙ π - jwu" {
		t.Fatalf("title not updated in place: got %q", got)
	}
}

func TestLayoutChangeNeedsARebuild(t *testing.T) {
	cases := map[string]func(w *Window){
		"layout moved":  func(w *Window) { w.Layout.PosInScrollingLayout = &Vec2[uint32]{X: 2, Y: 1} },
		"tile resized":  func(w *Window) { w.Layout.TileSize = Vec2[float64]{X: 900, Y: 1200} },
		"floating":      func(w *Window) { w.IsFloating = true },
		"urgent":        func(w *Window) { w.IsUrgent = true },
		"workspace":     func(w *Window) { *w.WorkspaceId = 7 },
		"app id change": func(w *Window) { w.AppId = strptr("other") },
	}

	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			s := NewNiriState()
			s.Update(&WindowOpenedOrChanged{Window: *windowFixture()})

			changed := windowFixture()
			mutate(changed)
			layout, _ := s.Versions()
			s.Update(&WindowOpenedOrChanged{Window: *changed})

			if got, _ := s.Versions(); got == layout {
				t.Fatal("change must request a rebuild of the tiles")
			}
		})
	}
}

func TestNewWindowNeedsARebuild(t *testing.T) {
	s := NewNiriState()
	s.Update(&WindowOpenedOrChanged{Window: *windowFixture()})

	if layout, _ := s.Versions(); layout == 0 {
		t.Fatal("a brand new window must request a rebuild")
	}
}

// The marker has to move when the focus does, but the tiles must not be
// rebuilt for it: a rebuild destroys and recreates every tile, and the
// stylesheet's 75 ms background transition then fades each of them from the
// plain .tile colour, which is the flash on every focus switch.
func TestFocusChangeMovesTheMarkerWithoutARebuild(t *testing.T) {
	s := NewNiriState()
	s.Update(&WindowOpenedOrChanged{Window: *windowFixture()})

	second := windowFixture()
	second.Id = 43
	s.Update(&WindowOpenedOrChanged{Window: *second})
	built, _ := s.Versions()

	s.Update(&WindowFocusChanged{Id: u64(43)})

	layout, focus := s.Versions()
	if layout != built {
		t.Fatal("focus change must not request a rebuild of the tiles")
	}
	if focus == built {
		t.Fatal("focus change must move the focus marker")
	}
	if !s.windows[43].IsFocused || s.windows[42].IsFocused {
		t.Fatal("focus flags not updated")
	}
}

// The actual anti-flicker guarantee: modules are only notified when the event
// changed something they draw.
func TestCallbacksOnlyFireForDrawingRelevantEvents(t *testing.T) {
	s := NewNiriState()
	s.Update(&WindowOpenedOrChanged{Window: *windowFixture()})

	var calls int
	s.OnUpdate(1, func(*State) { calls++ })

	title := windowFixture()
	title.Title = strptr("⠙ π - jwu")
	s.Update(&WindowOpenedOrChanged{Window: *title})
	if calls != 0 {
		t.Fatalf("title-only change notified modules %d time(s)", calls)
	}

	s.Update(&KeyboardLayoutsChanged{})
	if calls != 0 {
		t.Fatalf("unrelated event notified modules %d time(s)", calls)
	}

	// A focus change does not rebuild the tiles, but the modules still have to
	// hear about it: that is what moves the marker.
	s.Update(&WindowFocusChanged{Id: u64(42)})
	if calls != 1 {
		t.Fatalf("focus change notified modules %d time(s), want 1", calls)
	}

	moved := windowFixture()
	moved.Layout.PosInScrollingLayout = &Vec2[uint32]{X: 2, Y: 1}
	s.Update(&WindowOpenedOrChanged{Window: *moved})
	if calls != 2 {
		t.Fatalf("relevant change notified modules %d time(s), want 2", calls)
	}
}

// Tile tooltips capture the *Window pointer, so in-place updates must keep it.
func TestWindowPointerStaysLive(t *testing.T) {
	s := NewNiriState()
	s.Update(&WindowOpenedOrChanged{Window: *windowFixture()})
	before := s.windows[42]

	changed := windowFixture()
	changed.Title = strptr("new title")
	s.Update(&WindowOpenedOrChanged{Window: *changed})

	if s.windows[42] != before {
		t.Fatal("window pointer replaced; tooltips would show a stale title")
	}
	if got := *before.Title; got != "new title" {
		t.Fatalf("captured pointer not updated: got %q", got)
	}
}

// Update used to run the registered callbacks while still holding s.mu for
// reading. A callback that takes a lock of its own then deadlocks against a
// concurrent RemoveOnUpdate issued while that same lock is held, which is
// exactly what the waybar module does in Notify and Deinit.
func TestUpdateDoesNotHoldLockDuringCallbacks(t *testing.T) {
	state := NewNiriState()

	// stands in for module.Instance and its mutex
	var instance sync.RWMutex
	state.OnUpdate(1, func(*State) {
		instance.RLock()
		defer instance.RUnlock()
	})

	done := make(chan struct{})
	go func() {
		defer close(done)
		for range 1000 {
			// Must be an event that needs a redraw, otherwise the notification
			// gate below skips the callbacks and nothing is exercised.
			go state.Update(&WindowClosed{Id: 1})

			instance.Lock()
			state.RemoveOnUpdate(1)
			instance.Unlock()

			state.OnUpdate(1, func(*State) {
				instance.RLock()
				defer instance.RUnlock()
			})
		}
	}()

	select {
	case <-done:
	case <-time.After(30 * time.Second):
		t.Fatal("deadlock: Update callbacks ran while the state lock was held")
	}
}
