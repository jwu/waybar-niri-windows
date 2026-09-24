package module

import (
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"unicode"
)

// announceScript returns the path of the shell side of the protocol, or "" when
// this checkout cannot see it. That file ships with the shell configuration, not
// with the module, so this looks where a shell would source it: $WNW_ANNOUNCE
// names it explicitly, the path the module's README installs it to comes next,
// and an older checkout of this repository kept it in contrib/.
func announceScript() string {
	candidates := []string{os.Getenv("WNW_ANNOUNCE")}
	if home, err := os.UserHomeDir(); err == nil {
		candidates = append(candidates, filepath.Join(home, ".config", "waybar", "zsh-announce.zsh"))
	}
	candidates = append(candidates, filepath.Join("..", "contrib", "zsh-announce.zsh"))

	for _, path := range candidates {
		if path == "" {
			continue
		}
		if _, err := os.Stat(path); err == nil {
			return path
		}
	}
	return ""
}

func TestShellSideSpeaksTheSameProtocol(t *testing.T) {
	// The two ends of the announcement live in different languages, and nothing
	// else checks them against each other: the Go tests would pass just as well
	// if the shell wrote a different encoding.
	zsh, err := exec.LookPath("zsh")
	if err != nil {
		t.Skipf("zsh is not installed: %s", err)
	}
	script := announceScript()
	if script == "" {
		t.Skip("zsh-announce.zsh is not installed; set $WNW_ANNOUNCE to its path")
	}

	if got, want := runZsh(t, zsh, script, "_wnw_tag 4242"), marker(4242); got != want {
		t.Errorf("_wnw_tag 4242 = %q, want %q", got, want)
	}

	// The announcement has to land inside the escape sequence a terminal takes
	// its title from, and it has to name the shell that wrote it ($$ is the pid
	// of the shell, not of the subshell a command substitution runs in). All of
	// it comes out of one shell, because a second one would have a different pid.
	out := runZsh(t, zsh, script, `print -r -- $$; _wnw_title x; print; print -r -- $$; ZSH_THEME_TERM_TITLE_IDLE='jwu@archlinux:~'; _wnw_precmd; print`)
	lines := strings.Split(strings.TrimSuffix(out, "\n"), "\n")
	if len(lines) != 4 {
		t.Fatalf("the shell wrote %d lines (%q), want 4", len(lines), out)
	}

	pid := atoi(t, lines[0])
	if got, want := lines[1], "\x1b]2;x"+marker(pid)+"\x07"; got != want {
		t.Errorf("_wnw_title x = %q, want %q", got, want)
	}
	if second := atoi(t, lines[2]); second != pid {
		t.Fatalf("the shell changed pid between writes: %d, then %d", pid, second)
	}

	// The title at a prompt is the one the prompt itself writes, so that a window
	// is named the same whether or not this file is installed. oh-my-zsh and most
	// themes keep that text in ZSH_THEME_TERM_TITLE_IDLE.
	if got, want := lines[3], "\x1b]2;jwu@archlinux:~"+marker(pid)+"\x07"; got != want {
		t.Errorf("_wnw_precmd = %q, want %q", got, want)
	}
}

// runZsh runs a line of shell against the given script (the shell side of the
// protocol) and returns what it wrote to stdout. The shell is started without
// startup files and without a terminal, so the pipe this test reads is the only
// thing it can write to.
func runZsh(t *testing.T, zsh, script, code string) string {
	t.Helper()
	cmd := exec.Command(zsh, "-dfc", `source "$1"; `+code, "zsh", script)
	cmd.Env = append(os.Environ(), "TTY=", "_ghostty_fd=")
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("running %s: %s", code, err)
	}
	return string(out)
}

func atoi(t *testing.T, s string) int {
	t.Helper()
	v, err := strconv.Atoi(strings.TrimSpace(s))
	if err != nil {
		t.Fatalf("parsing %q as a pid: %s", s, err)
	}
	return v
}

func TestMarkerRoundTrips(t *testing.T) {
	for _, pid := range []int{1, 7, 42, 1234, 4194304, 999999999} {
		got, ok := parseMarker(marker(pid))
		if !ok || got != pid {
			t.Errorf("parseMarker(marker(%d)) = %d, %v", pid, got, ok)
		}
		if stripped := stripMarker("~" + marker(pid)); stripped != "~" {
			t.Errorf("stripMarker(~ + marker(%d)) = %q, want ~", pid, stripped)
		}
	}
}

func TestMarkerIsMadeOfInvisibleCharacters(t *testing.T) {
	// The whole point of the encoding is that nothing renders it. Go cannot
	// render anything, so this checks the property the renderer goes by:
	// every character is a format character (Cf), which renderers are required
	// to ignore. A tagged title was also rendered by hand with pango-view
	// against the untagged one; the two images came out identical.
	for _, title := range []string{marker(1), marker(1939)} {
		for _, r := range title {
			if !unicode.Is(unicode.Cf, r) {
				t.Errorf("%U in %q is not a format character", r, title)
			}
		}
	}
}

func TestParseMarkerIgnoresTitlesWithoutOne(t *testing.T) {
	for _, title := range []string{
		"",
		"π - jwu",
		"~/bin/pi-config",
		// A tag character with no digits after it, digits with no close, more
		// digits than a pid can have, and a pid of zero: none of these is an
		// announcement.
		"\U000E0001text",
		"\U000E0001\U000E0031",
		"\U000E0001" + strings.Repeat(string(markerDigit), markerDigits+1) + "\U000E007F",
		"\U000E0001\U000E0030\U000E007F",
	} {
		if pid, ok := parseMarker(title); ok {
			t.Errorf("parseMarker(%q) = %d, want no pid", title, pid)
		}
	}
}

func TestParseMarkerTakesTheLastAnnouncement(t *testing.T) {
	// A program that replaced the title without knowing about the announcement
	// leaves the old one in front of the shell's, and the shell's is the one
	// that runs in the window.
	title := "old" + marker(11) + "new" + marker(22)
	if pid, ok := parseMarker(title); !ok || pid != 22 {
		t.Errorf("parseMarker(%q) = %d, %v, want 22", title, pid, ok)
	}
}

func TestStripMarkerLeavesTheVisibleTitle(t *testing.T) {
	for _, tc := range []struct {
		name  string
		title string
		want  string
	}{
		{"no marker", "π - jwu", "π - jwu"},
		{"appended", "~" + marker(1), "~"},
		{"in the middle", "a" + marker(2) + "b", "ab"},
		{"two of them", "a" + marker(3) + "b" + marker(4), "ab"},
		{"nothing but a marker", marker(5), ""},
		{"a malformed one is text", "\U000E0001\U000E0031", "\U000E0001\U000E0031"},
	} {
		if got := stripMarker(tc.title); got != tc.want {
			t.Errorf("%s: stripMarker = %q, want %q", tc.name, got, tc.want)
		}
	}
}

func TestMarkerDoesNotTouchEmojiTagSequences(t *testing.T) {
	// A tag sequence is how an emoji carries a subdivision flag: TAG LETTERs
	// ended by the same CANCEL TAG this protocol ends with. It belongs to the
	// title's author, and the module must neither read a pid out of it nor
	// strip it.
	flag := "\U0001F3F4\U000E0067\U000E0062\U000E0073\U000E0063\U000E0074\U000E007F"
	if pid, ok := parseMarker(flag); ok {
		t.Errorf("parseMarker(%q) = %d, want no pid", flag, pid)
	}
	if got := stripMarker(flag); got != flag {
		t.Errorf("stripMarker(%q) = %q, want it unchanged", flag, got)
	}

	// Together with an announcement, only the announcement goes.
	if got := stripMarker("a" + flag + marker(6)); got != "a"+flag {
		t.Errorf("stripMarker = %q, want the flag kept", got)
	}
	if pid, ok := parseMarker(flag + marker(7)); !ok || pid != 7 {
		t.Errorf("parseMarker = %d, %v, want 7", pid, ok)
	}
}

func TestMarkersKeepsItsBookkeepingStraight(t *testing.T) {
	// The scanner resumes at the rune that ended a failed attempt, so a lone
	// tag character must not hide the announcement behind it.
	title := "\U000E0001x" + marker(8)
	if pid, ok := parseMarker(title); !ok || pid != 8 {
		t.Errorf("parseMarker = %d, %v, want 8", pid, ok)
	}
	if got := stripMarker(title); got != "\U000E0001x" {
		t.Errorf("stripMarker = %q, want the stray tag character kept", got)
	}

	// A long run of digits is not an announcement either, and the text after it
	// still is not swallowed.
	long := "\U000E0001" + strings.Repeat(string(markerDigit), 40) + "~" + marker(9)
	if pid, ok := parseMarker(long); !ok || pid != 9 {
		t.Errorf("parseMarker = %d, %v, want 9", pid, ok)
	}
	if got := stripMarker(long); got != "\U000E0001"+strings.Repeat(string(markerDigit), 40)+"~" {
		t.Errorf("stripMarker = %q, want the digit run kept", got)
	}
}
