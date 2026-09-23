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
		100: {ppid: 0, ticks: 30, io: 3000},
		200: {ppid: 100, ticks: 20, io: 2000},
		300: {ppid: 200, ticks: 10, io: 1000},
		400: {ppid: 100, ticks: 5, io: 500},
		900: {ppid: 1, ticks: 999, io: 999999},
	}
	prev := map[int]proc{
		100: {ticks: 20, io: 2000},
		200: {ticks: 15, io: 1500},
		300: {ticks: 6, io: 400},
		400: {ticks: 0, io: 0},
		900: {ticks: 0, io: 0},
	}

	ticks, io := treeDelta(cur, prev, index(cur), 100)
	if want := uint64(24); ticks != want {
		t.Errorf("ticks = %d, want %d", ticks, want)
	}
	if want := uint64(2600); io != want {
		t.Errorf("io = %d, want %d", io, want)
	}

	// The middle of the tree is measured on its own, not as a share of root.
	ticks, io = treeDelta(cur, prev, index(cur), 200)
	if want := uint64(9); ticks != want {
		t.Errorf("subtree ticks = %d, want %d", ticks, want)
	}
	if want := uint64(1100); io != want {
		t.Errorf("subtree io = %d, want %d", io, want)
	}
}

func TestTreeDeltaSkipsProcessesWithoutABaseline(t *testing.T) {
	// 300 was spawned since the previous sample: its counters are totals for
	// its whole lifetime, so counting them would fake a burst of work.
	cur := map[int]proc{
		100: {ppid: 0, ticks: 20, io: 2000},
		300: {ppid: 100, ticks: 900, io: 90000},
	}
	prev := map[int]proc{
		100: {ticks: 10, io: 1000},
	}

	ticks, io := treeDelta(cur, prev, index(cur), 100)
	if want := uint64(10); ticks != want {
		t.Errorf("ticks = %d, want %d (the new process must not count)", ticks, want)
	}
	if want := uint64(1000); io != want {
		t.Errorf("io = %d, want %d (the new process must not count)", io, want)
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

// feed runs n samples of the given per-sample counters through a fresh score.
func feed(n int, ticks, io uint64) []Level {
	r := &root{}
	levels := make([]Level, 0, n)
	for i := 0; i < n; i++ {
		r.advance(ticks, io, 1)
		levels = append(levels, r.level)
	}
	return levels
}

func TestLevelsFollowLoad(t *testing.T) {
	// A whole core is yellow and stays there: Heavy is reserved for more than a
	// core, so a single-threaded task can never flicker into red.
	if levels := feed(8, clkTck, 0); levels[7] != Medium {
		t.Errorf("a whole core settled at %v, want Medium", levels[7])
	}

	// Two cores get there in the second sample, eight in the first (the score
	// is capped, so a big build does not push it far past the top tier).
	want := []Level{Medium, Heavy}
	if levels := feed(2, clkTck*2, 0); !slices.Equal(levels, want) {
		t.Errorf("two cores is %v, want %v", levels, want)
	}
	if levels := feed(1, clkTck*8, 0); levels[0] != Heavy {
		t.Errorf("eight cores is %v, want Heavy", levels[0])
	}

	// 79% of a core is a terminal running something: Medium, never Heavy.
	if levels := feed(8, clkTck*79/100, 0); levels[7] != Medium {
		t.Errorf("79%% of a core settled at %v, want Medium", levels[7])
	}

	// The boundary between "a little" and "a lot" sits at 20% of a core.
	if levels := feed(3, clkTck*25/100, 0); levels[2] != Medium {
		t.Errorf("25%% of a core is %v, want Medium", levels[2])
	}
	if levels := feed(8, clkTck*15/100, 0); levels[7] != Light {
		t.Errorf("15%% of a core settled at %v, want Light", levels[7])
	}

	// Light starts at 3% of a core: a background tab at 4% shows up in the
	// second sample, a tree at 1% never leaves Idle.
	want = []Level{Idle, Light}
	if levels := feed(2, clkTck*4/100, 0); !slices.Equal(levels, want) {
		t.Errorf("4%% of a core is %v, want %v", levels, want)
	}
	if levels := feed(8, clkTck/100, 0); levels[7] != Idle {
		t.Errorf("1%% of a core settled at %v, want Idle", levels[7])
	}

	// Block traffic counts on its own: 40 MiB/s is Heavy with no CPU to speak
	// of, which is what a download being written to disk looks like.
	want = []Level{Medium, Heavy}
	if levels := feed(2, 0, 40<<20); !slices.Equal(levels, want) {
		t.Errorf("a disk-bound tree is %v, want %v", levels, want)
	}
}

func TestLevelsDecayAfterWorkStops(t *testing.T) {
	r := &root{}
	for i := 0; i < 3; i++ {
		r.advance(clkTck*8, 0, 1) // eight cores, capped
	}
	if r.level != Heavy {
		t.Fatalf("a saturated tree is %v, want Heavy", r.level)
	}

	// The fade spells out the memory of the score: Heavy for three samples,
	// Medium for four, Light for three, then Idle.
	want := []Level{Heavy, Heavy, Heavy, Medium, Medium, Medium, Medium, Light, Light, Light, Idle}
	for i, expected := range want {
		r.advance(0, 0, 1)
		if r.level != expected {
			t.Fatalf("sample %d after work stopped is %v, want %v", i+1, r.level, expected)
		}
	}
}

func TestAdvanceIgnoresNonPositiveInterval(t *testing.T) {
	r := &root{}
	r.advance(clkTck, 0, 0)
	if r.level != Idle || r.heat != 0 {
		t.Errorf("a zero interval scored %v/%v, want Idle/0", r.level, r.heat)
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

	procs, children := scan([]int{os.Getpid()})

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
