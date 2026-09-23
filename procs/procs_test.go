package procs

import (
	"os"
	"os/exec"
	"slices"
	"testing"
)

func TestTreeDeltaSumsAWholeTree(t *testing.T) {
	//   100 ── 200 ── 300
	//      └─── 400
	// 900 is an unrelated process: it is in the sample, but in no tree.
	cur := map[int]proc{
		100: {ppid: 0, ticks: 30},
		200: {ppid: 100, ticks: 20},
		300: {ppid: 200, ticks: 10},
		400: {ppid: 100, ticks: 5},
		900: {ppid: 1, ticks: 999},
	}
	prev := map[int]proc{
		100: {ticks: 20},
		200: {ticks: 15},
		300: {ticks: 6},
		400: {ticks: 0},
		900: {ticks: 0},
	}

	if got, want := treeDelta(cur, prev, index(cur), 100), uint64(24); got != want {
		t.Errorf("treeDelta = %d, want %d", got, want)
	}

	// The middle of the tree is measured on its own, not as a share of root.
	if got, want := treeDelta(cur, prev, index(cur), 200), uint64(9); got != want {
		t.Errorf("subtree treeDelta = %d, want %d", got, want)
	}
}

func TestTreeDeltaSkipsProcessesWithoutABaseline(t *testing.T) {
	// 300 was spawned since the previous sample: its counters are totals for
	// its whole lifetime, so counting them would fake a burst of work.
	cur := map[int]proc{
		100: {ppid: 0, ticks: 20},
		300: {ppid: 100, ticks: 900},
	}
	prev := map[int]proc{
		100: {ticks: 10},
	}

	if got, want := treeDelta(cur, prev, index(cur), 100), uint64(10); got != want {
		t.Errorf("treeDelta = %d, want %d (the new process must not count)", got, want)
	}
}

func TestCounterDeltaClampsRecycledPid(t *testing.T) {
	if got := counterDelta(500, 20); got != 0 {
		t.Errorf("counterDelta(500, 20) = %d, want 0", got)
	}
	if got := counterDelta(20, 500); got != 480 {
		t.Errorf("counterDelta(20, 500) = %d, want 480", got)
	}
}

func TestTreeVisitsEveryMemberOnce(t *testing.T) {
	// 2 is its own grandparent: a pid recycled between two reads can fake a
	// cycle, which must not turn into an endless walk.
	cur := map[int]proc{
		1: {ppid: 2},
		2: {ppid: 1},
		3: {ppid: 1},
	}

	members := tree(index(cur), 1)
	slices.Sort(members)
	if want := []int{1, 2, 3}; !slices.Equal(members, want) {
		t.Errorf("tree = %v, want %v", members, want)
	}
}

// feed runs n samples of the given per-sample counters through a fresh tree.
func feed(n int, ticks uint64) []Level {
	r := &root{}
	levels := make([]Level, 0, n)
	for i := 0; i < n; i++ {
		r.advance(ticks, 1)
		levels = append(levels, r.level)
	}
	return levels
}

func TestLevelsFollowLoad(t *testing.T) {
	// A whole core is Medium, and it takes three samples to get there: the
	// level is the median of the last five samples, and a window starts out as
	// zeroes.
	want := []Level{Idle, Idle, Medium, Medium}
	if levels := feed(4, clkTck); !slices.Equal(levels, want) {
		t.Errorf("a whole core is %v, want %v", levels, want)
	}

	// Over the heavy threshold there is nothing to filter, so it is taken as it
	// comes: two cores and eight cores are both red from the first sample.
	for _, tc := range []struct {
		name  string
		ticks uint64
	}{
		{"two cores", clkTck * 2},
		{"eight cores", clkTck * 8},
	} {
		if levels := feed(1, tc.ticks); levels[0] != Heavy {
			t.Errorf("%s is %v, want Heavy", tc.name, levels[0])
		}
	}

	// 79% of a core is a terminal running something: Medium, never Heavy,
	// because a boundary on exactly one core would flicker.
	if levels := feed(8, clkTck*79/100); levels[7] != Medium {
		t.Errorf("79%% of a core settled at %v, want Medium", levels[7])
	}

	// The boundaries: 25% is Medium, 15% is Light, and what an idle browser
	// does (1% to 4%) is nothing at all.
	if levels := feed(6, clkTck*25/100); levels[5] != Medium {
		t.Errorf("25%% of a core settled at %v, want Medium", levels[5])
	}
	if levels := feed(6, clkTck*15/100); levels[5] != Light {
		t.Errorf("15%% of a core settled at %v, want Light", levels[5])
	}
	if levels := feed(30, clkTck*4/100); levels[29] != Idle {
		t.Errorf("4%% of a core settled at %v, want Idle", levels[29])
	}
}

func TestBurstsDoNotLightATile(t *testing.T) {
	// An idle browser: a few percent of a core with one-second bursts to a
	// third of one, which is what a mean over the samples turned into a green
	// tile.
	r := &root{}
	for _, burst := range []float64{0.03, 0.04, 0.32, 0.03, 0.02, 0.05, 0.31, 0.03} {
		r.advance(uint64(burst*clkTck), 1)
		if r.level != Idle {
			t.Fatalf("a burst of %.0f%% in an idle tree lit the tile as %v", burst*100, r.level)
		}
	}

	// Three of the last five samples is what it takes for work that keeps
	// going: 12% of a core is Light.
	for i := 0; i < 3; i++ {
		r.advance(clkTck*12/100, 1)
	}
	if r.level != Light {
		t.Errorf("12%% of a core kept up is %v, want Light", r.level)
	}

	// And where it lands is the load itself, not the noise that came before.
	for i := 0; i < 3; i++ {
		r.advance(clkTck*30/100, 1)
	}
	if r.level != Medium {
		t.Errorf("30%% of a core kept up is %v, want Medium", r.level)
	}
}

func TestLevelsDropWhenWorkStops(t *testing.T) {
	r := &root{}
	for i := 0; i < window; i++ {
		r.advance(clkTck*8, 1)
	}
	if r.level != Heavy {
		t.Fatalf("a saturated tree is %v, want Heavy", r.level)
	}

	// The level follows the window, so three quiet samples out of five are what
	// it takes to fall: two samples of red, then straight to grey, with no
	// yellow in between.
	want := []Level{Heavy, Heavy, Idle}
	for i, expected := range want {
		r.advance(0, 1)
		if r.level != expected {
			t.Fatalf("sample %d after work stopped is %v, want %v", i+1, r.level, expected)
		}
	}
}

func TestAdvanceIgnoresNonPositiveInterval(t *testing.T) {
	r := &root{}
	r.advance(clkTck, 0)
	if r.level != Idle || r.samples[0] != 0 {
		t.Errorf("a zero interval scored %v/%v, want Idle/0", r.level, r.samples[0])
	}
}

func TestReadStatReadsThisProcess(t *testing.T) {
	p, ok := readStat(os.Getpid())
	if !ok {
		t.Fatalf("readStat(%d) failed", os.Getpid())
	}
	if p.ppid != os.Getppid() {
		t.Errorf("ppid = %d, want %d", p.ppid, os.Getppid())
	}

	// Field offsets are the kind of thing that fails silently, so check that
	// burning CPU actually moves the counter that is read.
	before, _ := readStat(os.Getpid())
	for attempt := 0; attempt < 200; attempt++ {
		burn()
		after, ok := readStat(os.Getpid())
		if !ok {
			t.Fatal("readStat failed while burning CPU")
		}
		if after.ticks > before.ticks {
			return
		}
	}
	t.Fatalf("ticks stayed at %d while burning CPU", before.ticks)
}

func TestScanSeesChildren(t *testing.T) {
	child := exec.Command("sleep", "10")
	if err := child.Start(); err != nil {
		t.Fatalf("spawning a child: %s", err)
	}
	// Wait is deferred first so that it runs last: killing the child before
	// reaping it returns at once instead of holding the test for the sleep.
	defer child.Wait()
	defer child.Process.Kill()

	procs, children := scan()

	p, ok := procs[child.Process.Pid]
	if !ok {
		t.Fatalf("scan does not see the child process %d", child.Process.Pid)
	}
	if p.ppid != os.Getpid() {
		t.Errorf("child ppid = %d, want %d", p.ppid, os.Getpid())
	}
	if !slices.Contains(tree(children, os.Getpid()), child.Process.Pid) {
		t.Errorf("the child is not in the tree: %v", tree(children, os.Getpid()))
	}
}

var burnSink uint64

func burn() {
	for i := uint64(0); i < 4_000_000; i++ {
		burnSink += i
	}
}
