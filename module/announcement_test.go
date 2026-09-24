package module

import (
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"wnw/niri"
)

// announcedWindow returns a window that shows a title announcing a pid.
func announcedWindow(id uint64, app int32, pid int) *niri.Window {
	title := "~" + marker(pid)
	return &niri.Window{Id: id, Pid: &app, Title: &title}
}

// quietWindow returns a window whose title is owned by something that does not
// announce anything (a full-screen program, a browser page, ...).
func quietWindow(id uint64, app int32) *niri.Window {
	title := "π - jwu"
	return &niri.Window{Id: id, Pid: &app, Title: &title}
}

func cachePath(t *testing.T) string {
	t.Helper()
	return filepath.Join(t.TempDir(), "announced.json")
}

func readCacheFile(t *testing.T, path string) announcementFile {
	t.Helper()
	blob, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading %s: %s", path, err)
	}
	var file announcementFile
	if err := json.Unmarshal(blob, &file); err != nil {
		t.Fatalf("parsing %s: %s", path, err)
	}
	return file
}

func TestRootsUseTheAnnouncedPid(t *testing.T) {
	// The announced pid has to run inside the window: the test process owns the
	// window, and a child of it announces itself.
	child := exec.Command("sleep", "10")
	if err := child.Start(); err != nil {
		t.Fatalf("spawning a child: %s", err)
	}
	defer child.Wait()
	defer child.Process.Kill()

	cache := newAnnouncementCache(cachePath(t))
	window := announcedWindow(3, int32(os.Getpid()), child.Process.Pid)

	roots, pidOfWindow := cache.roots([]*niri.Window{window})
	if len(roots) != 1 || roots[0] != child.Process.Pid {
		t.Errorf("roots = %v, want the announced pid %d", roots, child.Process.Pid)
	}
	if got := pidOfWindow[3]; got != child.Process.Pid {
		t.Errorf("window 3 is measured by %d, want %d", got, child.Process.Pid)
	}

	// A bar that restarts while the window's program owns the title, so that the
	// announcement is not in the title any more, still measures the shell: this
	// is what the cache file is for.
	cache.flush()
	restarted := newAnnouncementCache(cache.path)
	roots, pidOfWindow = restarted.roots([]*niri.Window{quietWindow(3, int32(os.Getpid()))})
	if got := pidOfWindow[3]; got != child.Process.Pid {
		t.Errorf("after a restart window 3 is measured by %d, want %d (roots %v)", got, child.Process.Pid, roots)
	}
}

func TestRootsFallBackToTheWindowPid(t *testing.T) {
	// Without an announcement the module measures what it always measured, the
	// process niri names for the window, and a pid that several windows share is
	// sampled once.
	cache := newAnnouncementCache(cachePath(t))
	windows := []*niri.Window{quietWindow(1, 100), quietWindow(2, 100), quietWindow(3, 200)}

	roots, pidOfWindow := cache.roots(windows)
	if len(roots) != 2 || roots[0] != 100 || roots[1] != 200 {
		t.Errorf("roots = %v, want [100 200]", roots)
	}
	if pidOfWindow[1] != 100 || pidOfWindow[2] != 100 || pidOfWindow[3] != 200 {
		t.Errorf("pidOfWindow = %v, want the window pids", pidOfWindow)
	}

	// A window niri knows nothing about is skipped: there is nothing to measure.
	if roots, pidOfWindow := cache.roots([]*niri.Window{{Id: 4}}); len(roots) != 0 || len(pidOfWindow) != 0 {
		t.Errorf("a window without a pid gave roots %v and %v", roots, pidOfWindow)
	}
}

func TestAnnouncementFromOutsideTheWindowIsIgnored(t *testing.T) {
	// A title can name any pid, so the module only believes one that runs in the
	// window's own tree. Here the announcement names init, which is not a
	// descendant of the test process: the window falls back to the process niri
	// reports.
	cache := newAnnouncementCache(cachePath(t))
	_, pidOfWindow := cache.roots([]*niri.Window{announcedWindow(1, int32(os.Getpid()), 1)})
	if got := pidOfWindow[1]; got != os.Getpid() {
		t.Errorf("window 1 is measured by %d, want the window pid %d", got, os.Getpid())
	}
	if len(cache.windows) != 0 {
		t.Errorf("cache remembers %v, want nothing", cache.windows)
	}
}

func TestRememberedPidIsDroppedWhenTheWindowChangesOwner(t *testing.T) {
	// A window id outlives its window: a compositor restart starts ids over, and
	// the file survives the restart. An entry whose application pid is not the
	// one niri reports now cannot be trusted.
	path := cachePath(t)
	cache := newAnnouncementCache(path)
	cache.remember(1, announced{App: 500, Pid: os.Getpid()})
	cache.flush()

	restarted := newAnnouncementCache(path)
	_, pidOfWindow := restarted.roots([]*niri.Window{quietWindow(1, 600)})
	if got := pidOfWindow[1]; got != 600 {
		t.Errorf("window 1 is measured by %d, want the process niri reports (600)", got)
	}

	// And the next write forgets it, rather than merging it back in from the
	// file it is still sitting in.
	restarted.flush()
	if file := readCacheFile(t, path); len(file.Windows) != 0 {
		t.Errorf("the cache still holds %v", file.Windows)
	}
}

func TestFlushDropsAnnouncementsWhoseProcessIsGone(t *testing.T) {
	// A pid that is not running cannot be the shell of a window that is.
	path := cachePath(t)
	cache := newAnnouncementCache(path)
	cache.remember(1, announced{App: 500, Pid: 1 << 30})
	cache.remember(2, announced{App: 500, Pid: os.Getpid()})
	cache.flush()

	file := readCacheFile(t, path)
	if _, ok := file.Windows[1]; ok {
		t.Errorf("the cache kept an announcement for a pid that is gone: %v", file.Windows)
	}
	if _, ok := file.Windows[2]; !ok {
		t.Errorf("the cache dropped a live announcement: %v", file.Windows)
	}
	if file.Version != announcementVersion {
		t.Errorf("version = %d, want %d", file.Version, announcementVersion)
	}
}

func TestWindowWithoutAnnouncementIsLeftAloneWhenASiblingAnnounced(t *testing.T) {
	// Two windows of one application, and only one of them announced a shell: a
	// program owns the other window's title. The application's process tree is
	// the announced window's work as much as the quiet one's, so the quiet window
	// is left without a level instead of mirroring its sibling's load.
	child := exec.Command("sleep", "10")
	if err := child.Start(); err != nil {
		t.Fatalf("spawning a child: %s", err)
	}
	defer child.Wait()
	defer child.Process.Kill()

	cache := newAnnouncementCache(cachePath(t))
	app := int32(os.Getpid())
	windows := []*niri.Window{
		announcedWindow(1, app, child.Process.Pid),
		quietWindow(2, app),
		// A third window belongs to an application that announced nothing
		// anywhere: nothing of that application is attributed elsewhere, so its
		// own tree is still the best answer.
		quietWindow(3, 4242),
	}

	roots, pidOfWindow := cache.roots(windows)
	if len(roots) != 2 || roots[0] != child.Process.Pid || roots[1] != 4242 {
		t.Errorf("roots = %v, want [%d 4242]", roots, child.Process.Pid)
	}
	if _, ok := pidOfWindow[2]; ok {
		t.Errorf("window 2 is measured by %d, want no level", pidOfWindow[2])
	}
	if got := pidOfWindow[3]; got != 4242 {
		t.Errorf("window 3 is measured by %d, want 4242", got)
	}
}

func TestFlushKeepsWhatAnotherBarWrote(t *testing.T) {
	// Two bars share one file, and each of them only learns about the windows on
	// its own workspaces: writing the whole file from one bar's memory would drop
	// the other bar's entries.
	path := cachePath(t)
	first := newAnnouncementCache(path)
	second := newAnnouncementCache(path)

	first.remember(1, announced{App: 500, Pid: os.Getpid()})
	first.flush()
	second.remember(2, announced{App: 600, Pid: os.Getpid()})
	second.flush()

	file := readCacheFile(t, path)
	if len(file.Windows) != 2 {
		t.Errorf("the cache holds %v, want both windows", file.Windows)
	}
}

func TestNoCacheFileWithoutAnnouncements(t *testing.T) {
	// A session that never installs the shell side must not leave a file behind.
	path := cachePath(t)
	cache := newAnnouncementCache(path)
	cache.roots([]*niri.Window{quietWindow(1, 100)})
	cache.flush()

	if _, err := os.Stat(path); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("stat %s = %v, want no file", path, err)
	}
}

func TestUnreadableCacheIsIgnored(t *testing.T) {
	path := cachePath(t)
	if err := os.WriteFile(path, []byte("not json"), 0o600); err != nil {
		t.Fatalf("writing the cache: %s", err)
	}

	// A file that cannot be read is a cache miss, not an error.
	cache := newAnnouncementCache(path)
	if len(cache.windows) != 0 {
		t.Errorf("cache = %v, want nothing", cache.windows)
	}
	if _, pidOfWindow := cache.roots([]*niri.Window{quietWindow(1, 100)}); pidOfWindow[1] != 100 {
		t.Errorf("pidOfWindow = %v, want the window pid", pidOfWindow)
	}
}
