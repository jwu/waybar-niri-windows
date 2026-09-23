package module

import (
	"slices"
	"testing"
	"wnw/niri"
	"wnw/procs"
)

func windowFixture(id uint64, pid *int32) *niri.Window {
	return &niri.Window{Id: id, Pid: pid}
}

func pidOf(v int32) *int32 { return &v }

func TestProcessesSamplesEachProcessOnce(t *testing.T) {
	// Two windows of one terminal share a process, so the tracker must be
	// handed that pid once; a window whose process niri does not know is
	// skipped.
	windows := []*niri.Window{
		windowFixture(1, pidOf(100)),
		windowFixture(2, pidOf(200)),
		windowFixture(3, pidOf(100)),
		windowFixture(4, nil),
	}

	if got, want := processes(windows), []int{100, 200}; !slices.Equal(got, want) {
		t.Errorf("processes = %v, want %v", got, want)
	}
}

func TestLevelsByWindowSpreadsAProcessOverItsWindows(t *testing.T) {
	// Window 2 is a second window of the same terminal, so it shares the level
	// of window 1. Window 4 is a process that exited while the sample was on
	// its way, and window 5 has no pid at all: neither gets a level.
	levels := map[int]procs.Level{
		100: procs.Busy,
		200: procs.Warm,
	}
	windows := []*niri.Window{
		windowFixture(1, pidOf(100)),
		windowFixture(2, pidOf(100)),
		windowFixture(3, pidOf(200)),
		windowFixture(4, pidOf(300)),
		windowFixture(5, nil),
	}

	want := map[uint64]procs.Level{
		1: procs.Busy,
		2: procs.Busy,
		3: procs.Warm,
	}
	got := levelsByWindow(windows, levels)
	if len(got) != len(want) {
		t.Fatalf("levelsByWindow = %v, want %v", got, want)
	}
	for id, level := range want {
		if got[id] != level {
			t.Errorf("window %d is %v, want %v", id, got[id], level)
		}
	}
}
