package module

import (
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"wnw/log"
	"wnw/niri"
	"wnw/procs"
)

// The announced pids, and the file that keeps them across a restart of the bar.
//
// A window's title only carries an announcement while the shell is the program
// that owns it: a terminal shows the title of whatever runs in it, so a
// full-screen program (an editor, an agent) hides the shell's announcement for
// as long as it runs. Remembering the pid the window announced last is what
// covers that time, and remembering it in a file is what covers a restart of
// the bar: without it, every window whose program owns the title would fall back
// to the application's process tree (see pidFor) until its shell got a prompt
// again.
//
// The file is only written once a shell has announced itself, so a session
// without the shell side (see README.md) never creates it.

// announced is what one window told us: the pid niri names for it, and the pid
// its shell announced. The application pid is kept so that a window id that
// outlived the process it belonged to (or a compositor restart, which starts
// window ids over) cannot make a stale entry point at another window's process.
type announced struct {
	App int `json:"app"`
	Pid int `json:"pid"`
}

// announcementVersion is the format of the file. A file this module does not
// understand is ignored rather than guessed at.
const announcementVersion = 1

type announcementFile struct {
	Version int                  `json:"version"`
	Windows map[uint64]announced `json:"windows"`
}

// announcementCache resolves the pid to sample for a window, learning from
// titles as it goes. It is not safe for concurrent use; the activity sampler
// owns it.
type announcementCache struct {
	path    string
	windows map[uint64]announced
	dirty   bool

	// removed holds the windows this run found an entry for that cannot be
	// trusted any more. They are dropped from the file when it is next written:
	// the merge in flush would otherwise put them straight back.
	removed map[uint64]bool

	// unattributed holds the windows this run has already reported as left
	// without a level (see roots), so that the debug log says so once per window
	// instead of once per sample.
	unattributed map[uint64]bool
}

// defaultAnnouncementPath is where the announced pids are remembered. The data
// is a cache: everything in it can be learned again from a title, so it goes
// where a cache goes. An empty path (no cache directory) keeps it in memory.
func defaultAnnouncementPath() string {
	dir, err := os.UserCacheDir()
	if err != nil {
		log.Debugf("no cache directory, keeping announced pids in memory: %s", err)
		return ""
	}
	return filepath.Join(dir, "waybar-niri-windows", "announced.json")
}

func newAnnouncementCache(path string) *announcementCache {
	c := &announcementCache{
		path:         path,
		windows:      make(map[uint64]announced),
		removed:      make(map[uint64]bool),
		unattributed: make(map[uint64]bool),
	}
	c.readFile(c.windows)
	log.Debugf("remembered %d announced pids from %s", len(c.windows), path)
	return c
}

// roots returns the pid to sample for every window, each pid once, and the pid
// the level of each window should be read from.
//
// A pid appears once because the tracker keys its scores by root pid: a process
// that owns several windows, or a program several windows of one application
// announced through, must not be sampled twice.
func (c *announcementCache) roots(windows []*niri.Window) (roots []int, pidOfWindow map[uint64]int) {
	roots = make([]int, 0, len(windows))
	pidOfWindow = make(map[uint64]int, len(windows))
	seen := make(map[int]bool, len(windows))

	for _, window := range windows {
		pid, own, ok := c.pidFor(window)
		if !ok {
			continue
		}

		// The application's tree is a stand-in for a window that says nothing
		// about itself, and it is only a fair one while nothing of that tree is
		// attributed elsewhere. When another window of the same application did
		// announce, the application's number is a sibling's build as much as this
		// window's work, and reporting it here would light a tile that is idle:
		// the window goes without a level until it announces something of its
		// own (a shell in it does, see the shell side in README.md).
		if !own && c.announcedSibling(pid, window.Id) {
			if !c.unattributed[window.Id] {
				c.unattributed[window.Id] = true
				log.Debugf("window %d announces no pid while its application has one that does, leaving it unmeasured", window.Id)
			}
			continue
		}

		pidOfWindow[window.Id] = pid
		if seen[pid] {
			continue
		}
		seen[pid] = true
		roots = append(roots, pid)
	}

	return roots, pidOfWindow
}

// announcedSibling reports whether another window of the same application has an
// announcement, in this sample or among the remembered ones. The remembered ones
// are what cover the windows of that application which are on another workspace,
// or were announced before this bar started.
func (c *announcementCache) announcedSibling(app int, except uint64) bool {
	for id, entry := range c.windows {
		if id != except && entry.App == app {
			return true
		}
	}
	return false
}

// pidFor returns the pid whose process tree says whether this window is working,
// and whether it is a pid the window's own tree announced rather than the
// application's.
//
// A window whose title announces a pid is measured by that pid: the shell runs
// in the window, so its tree is this window's work, while the pid niri reports
// for the window is the application's, which every window of a single-instance
// application shares. A window whose title does not announce one (the program
// running in it owns the title) is measured by the announcement it made last,
// and a window that never announced anything falls back to the application's
// pid, which is what the module measured before announcements existed.
func (c *announcementCache) pidFor(window *niri.Window) (pid int, own bool, ok bool) {
	app, ok := windowPid(window)
	if !ok {
		return 0, false, false
	}

	// The check runs on every sample, not just the first: it costs a walk up the
	// parent chain, and it is what keeps a recycled pid, or a marker left behind
	// by a program that copied a title, from being attributed to this window.
	if window.Title != nil {
		if marked, ok := parseMarker(*window.Title); ok && procs.Descendant(marked, app) {
			c.remember(window.Id, announced{App: app, Pid: marked})
			return marked, true, true
		}
	}

	if entry, ok := c.windows[window.Id]; ok {
		if entry.App == app && procs.Descendant(entry.Pid, app) {
			return entry.Pid, true, true
		}
		// The window id belongs to another process now, or the announced pid is
		// gone: the entry cannot be trusted, and keeping it would only make the
		// file grow.
		delete(c.windows, window.Id)
		c.removed[window.Id] = true
		c.dirty = true
		log.Debugf("window %d lost the pid it announced (%d)", window.Id, entry.Pid)
	}

	return app, false, true
}

// remember records an announcement, if it is news.
func (c *announcementCache) remember(id uint64, entry announced) {
	if known, ok := c.windows[id]; ok && known == entry {
		return
	}
	c.windows[id] = entry
	delete(c.removed, id)
	delete(c.unattributed, id)
	c.dirty = true
	log.Debugf("window %d is measured by announced pid %d", id, entry.Pid)
}

// flush writes the announcements to the cache file, if anything changed.
//
// It merges what another bar wrote since this one started, so that two bars
// watching different workspaces cannot drop each other's entries. It then drops
// the entries for the windows this run gave up on, and the entries whose process
// has exited: a window is gone when its shell is, which keeps the file down to
// the windows that are still open.
func (c *announcementCache) flush() {
	if !c.dirty {
		return
	}
	c.dirty = false
	if c.path == "" {
		return
	}

	windows := make(map[uint64]announced)
	c.readFile(windows)
	for id, entry := range c.windows {
		windows[id] = entry
	}
	for id := range c.removed {
		delete(windows, id)
	}
	for id, entry := range windows {
		if !procs.Alive(entry.Pid) {
			delete(windows, id)
		}
	}
	c.windows = windows
	c.removed = make(map[uint64]bool)

	dir := filepath.Dir(c.path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		log.Warnf("cannot create %s: %s", dir, err)
		return
	}

	blob, err := json.Marshal(announcementFile{Version: announcementVersion, Windows: windows})
	if err != nil {
		log.Warnf("cannot encode announced pids: %s", err)
		return
	}

	// Write beside the file and rename over it, so that a reader (another bar)
	// sees either the old file or the new one.
	tmp, err := os.CreateTemp(dir, "announced-*.json")
	if err != nil {
		log.Warnf("cannot create a cache file in %s: %s", dir, err)
		return
	}
	name := tmp.Name()
	if _, err := tmp.Write(blob); err != nil {
		log.Warnf("cannot write %s: %s", name, err)
		tmp.Close()
		os.Remove(name)
		return
	}
	if err := tmp.Close(); err != nil {
		log.Warnf("cannot write %s: %s", name, err)
		os.Remove(name)
		return
	}
	if err := os.Rename(name, c.path); err != nil {
		log.Warnf("cannot replace %s: %s", c.path, err)
		os.Remove(name)
		return
	}

	log.Debugf("remembered %d announced pids", len(windows))
}

// readFile adds the announcements in the cache file to windows. A file that is
// missing, unreadable, or not in the format this version writes is not an
// error: the announcements in it are a cache, and every one of them can be
// learned again from a title.
func (c *announcementCache) readFile(windows map[uint64]announced) {
	if c.path == "" {
		return
	}

	blob, err := os.ReadFile(c.path)
	if errors.Is(err, fs.ErrNotExist) {
		return
	}
	if err != nil {
		log.Warnf("cannot read %s: %s", c.path, err)
		return
	}

	var file announcementFile
	if err := json.Unmarshal(blob, &file); err != nil {
		log.Warnf("cannot parse %s, ignoring it: %s", c.path, err)
		return
	}
	if file.Version != announcementVersion {
		log.Warnf("ignoring %s: version %d, expected %d", c.path, file.Version, announcementVersion)
		return
	}

	for id, entry := range file.Windows {
		if entry.App > 0 && entry.Pid > 0 {
			windows[id] = entry
		}
	}
}

// windowPid returns the pid niri reports for a window, the process that owns it.
func windowPid(window *niri.Window) (int, bool) {
	if window.Pid == nil {
		return 0, false
	}
	return int(*window.Pid), true
}
