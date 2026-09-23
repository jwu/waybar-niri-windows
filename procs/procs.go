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
	"math"
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

// Level is how recently a process tree was working.
type Level uint8

const (
	// Idle means the tree has not worked for the whole memory of the score,
	// about six seconds at the default tick.
	Idle Level = iota
	// Warm means it worked recently, but is not working hard.
	Warm
	// Busy means it is working now, or was working a few seconds ago.
	Busy
)

// String names the level. The module uses it as the CSS class of a tile;
// Idle is the absence of class, so it has no rule of its own.
func (l Level) String() string {
	switch l {
	case Busy:
		return "busy"
	case Warm:
		return "warm"
	default:
		return "idle"
	}
}

// The score is normalised so that a tree sitting exactly at one of these
// thresholds sustains 1.0. busyEnter therefore has to be below 1.0: a score
// that decays towards its input only approaches it asymptotically.
const (
	// busyCPU is the sustained load, as a fraction of one core, that counts as
	// working hard. A build on several cores goes far above it within one
	// sample; a browser tab playing a video stays around 10%.
	busyCPU = 0.15
	// busyIO is the block traffic that counts as working hard.
	busyIO = 5 << 20 // bytes per second

	// heatDecay is how much of the score survives one sample. The score is capped,
	// so the memory of the tracker does not grow with the size of the burst: a tree
	// that stops working is Warm for about five seconds and Idle after six, whether
	// it was a full core for a moment or eight cores for a minute.
	heatDecay = 0.65

	// busyEnter leaves Idle, busyExit leaves Busy again: the gap between them
	// is the hysteresis band that keeps a tree at the threshold from flapping
	// between classes, since every change costs a repaint.
	busyEnter = 0.6
	busyExit  = 0.5

	// warmEnter is the score above which a tree still counts as recently used.
	warmEnter = 0.08
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
	instant := math.Max(cpu/busyCPU, rate/busyIO)

	r.heat = math.Min(r.heat*heatDecay+instant*(1-heatDecay), 1)
	r.level = r.levelFor()
}

// levelFor maps the current score to a level, staying at Busy while the score
// is inside the hysteresis band.
func (r *root) levelFor() Level {
	switch {
	case r.heat >= busyEnter:
		return Busy
	case r.level == Busy && r.heat > busyExit:
		return Busy
	case r.heat >= warmEnter:
		return Warm
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
