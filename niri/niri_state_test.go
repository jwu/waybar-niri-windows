package niri

import "testing"

func strptr(s string) *string { return &s }

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

func TestTitleOnlyChangeDoesNotNeedRedraw(t *testing.T) {
	s := NewNiriState()
	s.Update(&WindowOpenedOrChanged{Window: *windowFixture()})

	changed := windowFixture()
	changed.Title = strptr("⠙ π - jwu")
	s.Update(&WindowOpenedOrChanged{Window: *changed})

	if s.needsRedraw {
		t.Fatal("title-only change must not request a redraw (it causes hover flicker)")
	}
	if got := *s.windows[42].Title; got != "⠙ π - jwu" {
		t.Fatalf("title not updated in place: got %q", got)
	}
}

func TestRedrawTriggers(t *testing.T) {
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
			s.Update(&WindowOpenedOrChanged{Window: *changed})

			if !s.needsRedraw {
				t.Fatal("change must request a redraw")
			}
		})
	}
}

func TestNewWindowNeedsRedraw(t *testing.T) {
	s := NewNiriState()
	s.Update(&WindowOpenedOrChanged{Window: *windowFixture()})

	if !s.needsRedraw {
		t.Fatal("a brand new window must request a redraw")
	}
}

func TestFocusChangeNeedsRedraw(t *testing.T) {
	s := NewNiriState()
	s.Update(&WindowOpenedOrChanged{Window: *windowFixture()})

	second := windowFixture()
	second.Id = 43
	second.IsFocused = true
	s.Update(&WindowOpenedOrChanged{Window: *second})

	if !s.needsRedraw {
		t.Fatal("focus change must request a redraw")
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

	moved := windowFixture()
	moved.Layout.PosInScrollingLayout = &Vec2[uint32]{X: 2, Y: 1}
	s.Update(&WindowOpenedOrChanged{Window: *moved})
	if calls != 1 {
		t.Fatalf("relevant change notified modules %d time(s), want 1", calls)
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
