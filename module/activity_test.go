package module

import (
	"testing"
	"wnw/niri"
	"wnw/procs"
)

func windowFixture(id uint64, pid *int32) *niri.Window {
	return &niri.Window{Id: id, Pid: pid}
}

func pidOf(v int32) *int32 { return &v }

func TestLevelsByWindowFollowsThePidEachWindowIsMeasuredBy(t *testing.T) {
	// Windows 1 and 2 are two windows of one application and neither announced a
	// shell, so both are measured by the application's pid: they report the same
	// work, which is all a shared pid can say. Windows 3 and 4 belong to that
	// same application, but each of them announced a shell of its own, so each
	// is measured on its own. Window 5 is measured by a process that exited
	// while the sample was on its way, and window 6 has no pid at all: neither
	// gets a level.
	pidOfWindow := map[uint64]int{
		1: 100,
		2: 100,
		3: 300,
		4: 400,
		5: 500,
	}
	levels := map[int]procs.Level{
		100: procs.Heavy,
		300: procs.Light,
		400: procs.Idle,
	}
	windows := []*niri.Window{
		windowFixture(1, pidOf(100)),
		windowFixture(2, pidOf(100)),
		windowFixture(3, pidOf(100)),
		windowFixture(4, pidOf(100)),
		windowFixture(5, pidOf(500)),
		windowFixture(6, nil),
	}

	want := map[uint64]procs.Level{
		1: procs.Heavy,
		2: procs.Heavy,
		3: procs.Light,
		4: procs.Idle,
	}
	got := levelsByWindow(windows, pidOfWindow, levels)
	if len(got) != len(want) {
		t.Fatalf("levelsByWindow = %v, want %v", got, want)
	}
	for id, level := range want {
		if got[id] != level {
			t.Errorf("window %d is %v, want %v", id, got[id], level)
		}
	}
}
