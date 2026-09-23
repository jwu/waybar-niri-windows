// Package procs turns /proc into a per-window "is it working" signal.
//
// niri says nothing about what a window is doing, but it does report the pid of
// the process behind it, and that pid is enough to measure the process tree by
// hand. One pass over /proc costs about 5 ms for 400 processes, nearly all of
// it opening /proc/<pid>/stat once per process, which is affordable at 1 Hz;
// see docs/waybar.md for the measurements.
//
// Two counters feed one decaying score:
//
//   - CPU time (utime+stime) catches compute.
//   - Block IO (read_bytes+write_bytes) catches work that is mostly waiting on
//     the disk, which a build or a download does for long stretches.
//
// rchar/wchar are deliberately not used. They count syscall traffic, and pty
// traffic is syscall traffic: an idle terminal that repaints a spinner moves
// tens of KB/s, and waybar itself would be the busiest reader in the session.
//
// None of this measures "running a command". A tree that sleeps, or that waits
// on the network, has no CPU and no block IO, so it reads as idle.
package procs

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"
	"wnw/log"
)

const procRoot = "/proc"

// clkTck is the kernel's USER_HZ: /proc/<pid>/stat reports CPU time in these
// ticks, and 100 holds on every architecture Go runs on except alpha. The
// standard library has no way to ask the kernel for it.
const clkTck = 100

// Level is how hard a process tree is working, now or a moment ago.
type Level uint8

const (
	// Idle means the tree has not worked for the whole memory of the score,
	// about ten seconds at the default tick.
	Idle Level = iota
	// Light means it is doing something small, or did a moment ago.
	Light
	// Medium means it is working, but not saturating a core.
	Medium
	// Heavy means it is using more than a whole core, or a comparable amount
	// of disk.
	Heavy
)

// String names the level. The module uses it as the CSS class of a tile;
// Idle is the absence of class, so it has no rule of its own.
func (l Level) String() string {
	switch l {
	case Heavy:
		return "heavy"
	case Medium:
		return "medium"
	case Light:
		return "light"
	default:
		return "idle"
	}
}

// One unit of score: a whole core, or 20 MiB/s of block traffic. Each signal
// is divided by its own unit, so the tiers below are the same numbers for
// either of them.
const (
	unitCPU = 1.0      // cores per unit
	unitIO  = 20 << 20 // bytes per second per unit
)

// The tier boundaries on that score, in units, plus the hysteresis that keeps a
// tree sitting on a boundary from flapping between two classes: every change
// costs a repaint, and a bar that flickers is worse than one that is a second
// late. Each exit sits more than one decay step below its enter, so a single
// quiet sample cannot drop the level.
//
//	grey   0.03 and below  under 3% of a core, or 0.6 MiB/s on disk
//	green  0.03 - 0.2      3% to 20% of a core
//	yellow 0.2 - 1.5       20% of a core up to one and a half cores
//	red    1.5 and up      more than one and a half cores, or 30 MiB/s
//
// The boundaries are deliberately at fractional loads that real work does not
// sit on: a single-threaded task is one whole core, which is comfortably
// yellow, and it would flicker if the boundary sat at exactly one. A tree that
// does sit exactly on a boundary stays in the lower tier, because a score that
// approaches its input never quite reaches it.
const (
	lightEnter, lightExit   = 0.03, 0.02
	mediumEnter, mediumExit = 0.2, 0.1
	heavyEnter, heavyExit   = 1.5, 0.8

	// heatRise is how much of a sample the score takes on while the load
	// rises, heatFall how much of it survives a sample while the load falls.
	// A sustained load is picked up within two or three samples and fades over
	// about ten, and because the fall is a plain decay the memory does not grow
	// with the size of the burst.
	heatRise = 0.7
	heatFall = 0.6
	heatMax  = 4.0
)

// proc is the part of one process that this package reads.
type proc struct {
	ppid  int
	ticks uint64 // utime+stime
	io    uint64 // read_bytes+write_bytes
}

// root is the decaying score of one process tree, keyed by the pid it was
// sampled from.
type root struct {
	heat  float64
	level Level
}

// Tracker keeps the previous sample so that it can report deltas, plus one
// score per root. It is not safe for concurrent use; the caller owns the
// locking.
type Tracker struct {
	prev  map[int]proc
	roots map[int]*root
	last  time.Time
}

// NewTracker returns a tracker without history. The first Update only records
// a baseline, so every root starts Idle.
func NewTracker() *Tracker {
	return &Tracker{
		prev:  make(map[int]proc),
		roots: make(map[int]*root),
	}
}

// Update samples /proc and returns the level of every root pid. Roots that are
// no longer running are dropped, so the level set shrinks with the windows; a
// root that is running but was spawned since the previous sample is Idle for
// one tick, because it has no baseline to subtract from.
func (t *Tracker) Update(roots []int) map[int]Level {
	now := time.Now()
	dt := 0.0
	if !t.last.IsZero() {
		dt = now.Sub(t.last).Seconds()
	}
	t.last = now

	cur, children := scan(roots)

	levels := make(map[int]Level, len(roots))
	next := make(map[int]*root, len(roots))
	for _, pid := range roots {
		r, ok := next[pid]
		if !ok {
			r = t.roots[pid]
			if r == nil {
				r = &root{}
			}
			next[pid] = r
		}
		if _, running := cur[pid]; !running {
			continue
		}

		ticks, io := treeDelta(cur, t.prev, children, pid)
		r.advance(ticks, io, dt)
		levels[pid] = r.level
	}

	t.prev = cur
	t.roots = next
	return levels
}

// advance folds one sample into the score. dt is the wall time since the
// previous sample, measured rather than assumed so that a late sample cannot
// inflate the rate.
func (r *root) advance(ticks, io uint64, dt float64) {
	if dt <= 0 {
		return
	}

	cpu := float64(ticks) / clkTck / dt // cores
	rate := float64(io) / dt            // bytes per second
	instant := max(cpu/unitCPU, rate/unitIO)

	if instant > r.heat {
		r.heat = r.heat*(1-heatRise) + instant*heatRise
	} else {
		r.heat *= heatFall
	}
	r.heat = min(r.heat, heatMax)
	r.level = r.levelFor()
}

// levelFor maps the score to a level, staying at the level it is already on
// while the score is inside that level's hysteresis band.
func (r *root) levelFor() Level {
	switch {
	case r.heat >= heavyEnter:
		return Heavy
	case r.level == Heavy && r.heat > heavyExit:
		return Heavy
	case r.heat >= mediumEnter:
		return Medium
	case r.level == Medium && r.heat > mediumExit:
		return Medium
	case r.heat >= lightEnter:
		return Light
	case r.level == Light && r.heat > lightExit:
		return Light
	default:
		return Idle
	}
}

// scan reads one sample of /proc: the parent and the CPU ticks of every
// process, plus the block IO of the processes in the given trees.
//
// IO is read only for those trees because /proc/<pid>/io is the expensive file
// and most processes are in no tree at all. The returned index maps a pid to
// its children.
func scan(roots []int) (map[int]proc, map[int][]int) {
	procs := statScan()
	children := index(procs)

	for _, pid := range roots {
		for _, member := range tree(children, pid) {
			p, ok := procs[member]
			if !ok {
				continue
			}
			p.io = readIO(member)
			procs[member] = p
		}
	}

	return procs, children
}

func statScan() map[int]proc {
	entries, err := os.ReadDir(procRoot)
	if err != nil {
		log.Warnf("cannot read %s: %s", procRoot, err)
		return nil
	}

	procs := make(map[int]proc, len(entries))
	for _, entry := range entries {
		pid, err := strconv.Atoi(entry.Name())
		if err != nil {
			continue // not a process entry
		}
		p, ok := readStat(pid)
		if !ok {
			continue // exited while we were reading it
		}
		procs[pid] = p
	}
	return procs
}

// readStat parses the fields of /proc/<pid>/stat that are not the command
// name. Everything up to the last ')' is skipped, so a comm containing spaces
// or parentheses cannot shift the fields.
func readStat(pid int) (proc, bool) {
	b, err := os.ReadFile(fmt.Sprintf("%s/%d/stat", procRoot, pid))
	if err != nil {
		return proc{}, false
	}

	s := string(b)
	end := strings.LastIndexByte(s, ')')
	if end < 0 {
		return proc{}, false
	}
	fields := strings.Fields(s[end+1:])
	// fields[0] is the state, fields[1] the parent pid, fields[11] and
	// fields[12] utime and stime (see proc(5)).
	if len(fields) < 13 {
		return proc{}, false
	}

	ppid, err := strconv.Atoi(fields[1])
	if err != nil {
		return proc{}, false
	}
	utime, err := strconv.ParseUint(fields[11], 10, 64)
	if err != nil {
		return proc{}, false
	}
	stime, err := strconv.ParseUint(fields[12], 10, 64)
	if err != nil {
		return proc{}, false
	}

	return proc{ppid: ppid, ticks: utime + stime}, true
}

// readIO returns the block traffic of one process. Accounts of what a process
// read and wrote are only available to processes that may ptrace it, so a
// process owned by another user reports zero instead of failing the sample.
func readIO(pid int) uint64 {
	b, err := os.ReadFile(fmt.Sprintf("%s/%d/io", procRoot, pid))
	if err != nil {
		return 0
	}

	var total uint64
	for _, line := range strings.Split(string(b), "\n") {
		value, ok := strings.CutPrefix(line, "read_bytes:")
		if !ok {
			value, ok = strings.CutPrefix(line, "write_bytes:")
		}
		if !ok {
			continue
		}
		n, err := strconv.ParseUint(strings.TrimSpace(value), 10, 64)
		if err != nil {
			continue
		}
		total += n
	}
	return total
}

// index maps every process to its children.
func index(procs map[int]proc) map[int][]int {
	children := make(map[int][]int)
	for pid, p := range procs {
		children[p.ppid] = append(children[p.ppid], pid)
	}
	return children
}

// tree returns a process and all of its descendants, a pid appearing once even
// if the sample contains a cycle (a cycle is impossible in a live process tree,
// but a pid recycled between two reads can fake one).
func tree(children map[int][]int, root int) []int {
	seen := map[int]bool{root: true}
	members := []int{root}
	for i := 0; i < len(members); i++ {
		for _, child := range children[members[i]] {
			if seen[child] {
				continue
			}
			seen[child] = true
			members = append(members, child)
		}
	}
	return members
}

// treeDelta sums the ticks and the block IO of a whole tree between two
// samples. Processes that are missing from the older sample are skipped, not
// counted in full: their counters are totals for their whole lifetime.
func treeDelta(cur, prev map[int]proc, children map[int][]int, root int) (ticks, io uint64) {
	for _, pid := range tree(children, root) {
		c, ok := cur[pid]
		if !ok {
			continue
		}
		p, ok := prev[pid]
		if !ok {
			continue
		}
		ticks += counterDelta(p.ticks, c.ticks)
		io += counterDelta(p.io, c.io)
	}
	return ticks, io
}

// counterDelta returns the increase of a monotonic counter. A counter that went
// backwards belongs to a recycled pid, and reporting its whole value as an
// increase would fake a burst of work, so it counts as nothing.
func counterDelta(before, after uint64) uint64 {
	if after < before {
		return 0
	}
	return after - before
}
