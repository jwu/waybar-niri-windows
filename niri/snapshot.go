package niri

import (
	"cmp"
	"slices"
)

// WorkspaceView is a workspace together with the windows on it, split into the
// tiling layout and floating windows.
//
// This is the neutral shape shared by the live State (event stream) and the
// one-shot IPC snapshot (QuerySnapshot), so the overview renderer does not care
// where the data came from.
type WorkspaceView struct {
	Workspace *Workspace
	// Tiled windows, sorted by column then row.
	Tiled []*Window
	// Floating windows, sorted left-to-right then top-to-bottom.
	Floating []*Window
}

// GroupWindows groups windows by the workspace they are on. Workspaces are
// sorted by output then index; windows keep their niri layout order.
//
// Windows without a workspace (unmapped) are dropped.
func GroupWindows(workspaces []*Workspace, windows []*Window) []WorkspaceView {
	views := make([]WorkspaceView, len(workspaces))
	index := make(map[uint64]int, len(workspaces))
	for i, ws := range workspaces {
		views[i].Workspace = ws
		index[ws.Id] = i
	}

	for _, win := range windows {
		if win.WorkspaceId == nil {
			continue
		}
		i, ok := index[*win.WorkspaceId]
		if !ok {
			continue
		}
		if win.IsFloating {
			views[i].Floating = append(views[i].Floating, win)
		} else {
			views[i].Tiled = append(views[i].Tiled, win)
		}
	}

	slices.SortFunc(views, func(a, b WorkspaceView) int {
		if c := compareOutput(a.Workspace, b.Workspace); c != 0 {
			return c
		}
		return cmp.Compare(a.Workspace.Index, b.Workspace.Index)
	})
	for i := range views {
		slices.SortFunc(views[i].Tiled, compareTiled)
		slices.SortFunc(views[i].Floating, compareFloating)
	}
	return views
}

// Snapshot returns every workspace with its windows, without blocking on the
// event stream. Used by the live overview to do an initial full draw.
func (s *State) Snapshot() []WorkspaceView {
	s.mu.RLock()
	defer s.mu.RUnlock()

	workspaces := make([]*Workspace, 0, len(s.workspaces))
	for _, ws := range s.workspaces {
		workspaces = append(workspaces, ws)
	}
	windows := make([]*Window, 0, len(s.windows))
	for _, win := range s.windows {
		windows = append(windows, win)
	}
	return GroupWindows(workspaces, windows)
}

func compareOutput(a, b *Workspace) int {
	// Workspaces without an output sort last.
	if a.Output == nil || b.Output == nil {
		if a.Output == nil && b.Output == nil {
			return 0
		}
		if a.Output == nil {
			return 1
		}
		return -1
	}
	return cmp.Compare(*a.Output, *b.Output)
}

func compareTiled(a, b *Window) int {
	ax, ay := scrollingPos(a)
	bx, by := scrollingPos(b)
	if c := cmp.Compare(ax, bx); c != 0 {
		return c
	}
	return cmp.Compare(ay, by)
}

func scrollingPos(w *Window) (x, y uint32) {
	if w.Layout.PosInScrollingLayout == nil {
		return 0, 0
	}
	return w.Layout.PosInScrollingLayout.X, w.Layout.PosInScrollingLayout.Y
}

func compareFloating(a, b *Window) int {
	ax, ay := tilePos(a)
	bx, by := tilePos(b)
	if c := cmp.Compare(ax, bx); c != 0 {
		return c
	}
	return cmp.Compare(ay, by)
}

func tilePos(w *Window) (x, y float64) {
	if w.Layout.TilePosInWorkspaceView == nil {
		return 0, 0
	}
	return w.Layout.TilePosInWorkspaceView.X, w.Layout.TilePosInWorkspaceView.Y
}
