package niri

import "testing"

func tiledFixture(id, workspace uint64, column, row uint32) *Window {
	ws := workspace
	return &Window{
		Id:          id,
		WorkspaceId: &ws,
		Layout: WindowLayout{
			PosInScrollingLayout: &Vec2[uint32]{X: column, Y: row},
		},
	}
}

func floatingFixture(id, workspace uint64, x, y float64) *Window {
	ws := workspace
	return &Window{
		Id:          id,
		WorkspaceId: &ws,
		IsFloating:  true,
		Layout: WindowLayout{
			TilePosInWorkspaceView: &Vec2[float64]{X: x, Y: y},
		},
	}
}

func workspaceFixture(id uint64, index uint8, output *string) *Workspace {
	return &Workspace{Id: id, Index: index, Output: output}
}

func TestGroupWindowsOrdersAndSplits(t *testing.T) {
	dp := "DP-1"
	hdmi := "HDMI-A-1"

	workspaces := []*Workspace{
		workspaceFixture(10, 2, &dp),
		workspaceFixture(11, 1, &dp),
		workspaceFixture(12, 1, &hdmi),
		workspaceFixture(13, 1, nil),
	}
	windows := []*Window{
		tiledFixture(101, 11, 2, 1),
		tiledFixture(102, 11, 1, 2),
		tiledFixture(103, 11, 1, 1),
		floatingFixture(104, 11, 30, 10),
		tiledFixture(105, 12, 1, 1),
		tiledFixture(106, 99, 1, 1), // unknown workspace
		{Id: 107},                   // no workspace
	}

	views := GroupWindows(workspaces, windows)

	wantWorkspaces := []uint64{11, 10, 12, 13}
	if len(views) != len(wantWorkspaces) {
		t.Fatalf("got %d views, want %d", len(views), len(wantWorkspaces))
	}
	for i, want := range wantWorkspaces {
		if views[i].Workspace.Id != want {
			t.Errorf("view %d is workspace %d, want %d", i, views[i].Workspace.Id, want)
		}
	}

	wantTiled := []uint64{103, 102, 101}
	gotTiled := ids(views[0].Tiled)
	if !equalIDs(gotTiled, wantTiled) {
		t.Errorf("tiled order = %v, want %v", gotTiled, wantTiled)
	}

	gotFloating := ids(views[0].Floating)
	if !equalIDs(gotFloating, []uint64{104}) {
		t.Errorf("floating = %v, want [104]", gotFloating)
	}

	if len(views[3].Tiled) != 0 || len(views[3].Floating) != 0 {
		t.Errorf("workspace without output should be empty: %+v", views[3])
	}
}

func ids(windows []*Window) []uint64 {
	out := make([]uint64, len(windows))
	for i, win := range windows {
		out[i] = win.Id
	}
	return out
}

func equalIDs(a, b []uint64) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func TestOutputSizeFallsBackToCurrentMode(t *testing.T) {
	index := uint8(1)
	out := Output{
		Modes: []OutputMode{
			{Width: 2560, Height: 1440},
			{Width: 1440, Height: 2560},
		},
		CurrentMode: &index,
	}
	if w, h := out.Size(); w != 1440 || h != 2560 {
		t.Fatalf("Size() = %dx%d, want 1440x2560", w, h)
	}

	out.Logical = &LogicalOutput{Width: 1920, Height: 1080}
	if w, h := out.Size(); w != 1920 || h != 1080 {
		t.Fatalf("Size() = %dx%d, want logical 1920x1080", w, h)
	}
}
