package module

import (
	"maps"
	"slices"
	"strconv"
	"time"
	"wnw/log"
	"wnw/niri"
	"wnw/procs"

	"github.com/gotk3/gotk3/glib"
	"github.com/gotk3/gotk3/gtk"
)

// activityInterval is how often the process trees behind the tiles are
// sampled. One pass over /proc costs about 5 ms for 400 processes, nearly all
// of it opening /proc/<pid>/stat once per process, so 1 Hz keeps the module
// near its baseline CPU cost and still shows a window that starts working
// within a second or two.
//
// A second bar costs a second scan: the tracker belongs to the instance. See
// docs/waybar.md.
const activityInterval = time.Second

// activityClasses are the classes a tile can carry, from least to most active.
// Idle is the absence of both, so it is not in the list.
var activityClasses = []procs.Level{procs.Warm, procs.Busy}

// startActivity runs the sampler. It lives off the GTK thread, which may not
// touch a widget, so every tick hands its result to the main loop instead.
func (i *Instance) startActivity() {
	done := make(chan struct{})
	i.activityDone = done
	i.activityWg.Add(1)

	go func() {
		defer i.activityWg.Done()

		ticker := time.NewTicker(activityInterval)
		defer ticker.Stop()
		for {
			select {
			case <-done:
				return
			case <-ticker.C:
				i.sampleActivity()
			}
		}
	}()
}

// stopActivity waits for the sampler to return. Deinit calls it before taking
// the instance lock, because the sampler takes that lock itself.
func (i *Instance) stopActivity() {
	if i.activityDone == nil {
		return
	}
	close(i.activityDone)
	i.activityDone = nil
	i.activityWg.Wait()
}

// sampleActivity measures the processes behind the windows on this bar's
// monitor and queues the result for the main loop.
//
// It runs concurrently with a redraw, so it reads the instance fields under
// the instance lock and lets the niri state take its own lock afterwards:
// holding one while taking the other would invert the order the state
// callbacks use and deadlock the bar.
func (i *Instance) sampleActivity() {
	i.mu.RLock()
	ready := i.ready
	monitor := i.monitor
	textMode := i.config.Mode == TextMode
	i.mu.RUnlock()
	if !ready || textMode {
		return
	}

	tiled, floating := i.niriState.Windows(monitor)

	windows := slices.Concat(tiled, floating)
	levels := i.tracker.Update(processes(windows))
	byWindow := levelsByWindow(windows, levels)

	// Walk the bar only when something changed. A level that is already on a
	// tile costs nothing to re-apply, but walking hands GTK a fresh set of
	// widget wrappers to finalize on every tick, and a level set is stable for
	// minutes at a time in an idle session. A rebuild in the meantime has
	// applied the levels itself, so skipping the walk cannot leave a tile
	// stale.
	i.mu.Lock()
	changed := !maps.Equal(i.levels, byWindow)
	i.levels = byWindow
	i.mu.Unlock()

	log.Tracef("activity: %d windows, %v", len(byWindow), levels)

	if !changed {
		return
	}
	glib.IdleAdd(i.applyActivity)
}

// processes returns the pid of every window, each once: the tracker keys its
// scores by pid, so a process that owns several windows must not be sampled
// twice. Windows whose process id niri does not know are skipped.
func processes(windows []*niri.Window) []int {
	pids := make([]int, 0, len(windows))
	seen := make(map[int]bool, len(windows))
	for _, window := range windows {
		if window.Pid == nil || seen[int(*window.Pid)] {
			continue
		}
		seen[int(*window.Pid)] = true
		pids = append(pids, int(*window.Pid))
	}
	return pids
}

// levelsByWindow spreads the level of a process over the windows it owns. A
// process can own several windows (two windows of one terminal, every window
// of a browser), and they all report the activity of one tree: the pid in the
// niri reply is as fine-grained as this gets.
func levelsByWindow(windows []*niri.Window, levels map[int]procs.Level) map[uint64]procs.Level {
	byWindow := make(map[uint64]procs.Level, len(windows))
	for _, window := range windows {
		if window.Pid == nil {
			continue
		}
		level, ok := levels[int(*window.Pid)]
		if !ok {
			continue // the process is gone
		}
		byWindow[window.Id] = level
	}
	return byWindow
}

// applyActivity is the main loop half of sampleActivity.
func (i *Instance) applyActivity() {
	i.mu.Lock()
	defer i.mu.Unlock()

	// A tick can be queued while waybar is tearing the module down; the
	// widgets may already be gone by the time it runs.
	if !i.ready {
		return
	}
	i.applyActivityLocked()
}

// applyActivityLocked refreshes the activity classes of the tiles already in
// the bar. It never creates or destroys a widget: rebuilding would throw away
// the hover state of the tile under the cursor, which is the flicker that
// title-only niri events no longer cause either.
func (i *Instance) applyActivityLocked() {
	if len(i.levels) == 0 {
		return
	}
	walkTiles(containerOf(i.box.ToWidget()), i.levels)
}

// walkTiles visits every widget the module put in the bar and sets the level
// of the tiles among them. GTK hands out containers as plain widgets, so each
// one is reinterpreted as the container it really is.
func walkTiles(parent *gtk.Container, levels map[uint64]procs.Level) {
	parent.GetChildren().Foreach(func(child any) {
		widget := child.(*gtk.Widget)
		if setTileLevel(widget, levels) {
			return
		}
		walkTiles(containerOf(widget), levels)
	})
}

// containerOf reinterprets a widget as the container it really is. The
// glib.Object is shared with that widget and never wrapped a second time,
// because a second wrapper would carry a second finalizer for the same pointer.
func containerOf(widget *gtk.Widget) *gtk.Container {
	return &gtk.Container{Widget: *widget}
}

// setTileLevel puts the level of a tile's window on the tile, and reports
// whether the widget was a tile at all. A tile is recognised by its name: the
// module names every tile after its window id, so that walking the bar needs
// no Go references to the widgets, which would have to be dropped and
// re-taken on every rebuild.
func setTileLevel(tile *gtk.Widget, levels map[uint64]procs.Level) bool {
	name, err := tile.GetName()
	if err != nil {
		return false
	}
	windowId, err := strconv.ParseUint(name, 10, 64)
	if err != nil {
		return false
	}

	style, err := tile.GetStyleContext()
	if err != nil {
		return true
	}

	// Touch a class only when it has to change: a style context that is told
	// about a class it already has still queues a revalidation, and every
	// level change repaints the tile during the stylesheet's transition.
	level := levels[windowId]
	for _, class := range activityClasses {
		wanted := class == level
		has := style.HasClass(class.String())
		if wanted == has {
			continue
		}
		if wanted {
			style.AddClass(class.String())
		} else {
			style.RemoveClass(class.String())
		}
	}
	return true
}
