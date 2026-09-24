package module

import (
	"strconv"
	"strings"
	"unicode/utf8"
)

// A shell can announce itself in the title of its window, and the module then
// measures that shell's process tree instead of the tree of the process niri
// names for the window.
//
// The announcement exists because a pid is all niri has to tell windows apart,
// and an application that keeps every window in one process (ghostty's
// single-instance mode, a browser, ...) gives niri one pid for all of them: see
// levelsByWindow. niri cannot ask an application which of its windows is which,
// but a shell knows that the window title of the terminal it runs in is shown by
// that window, so the shell can write its pid where the bar can read it. The
// shell side is a zsh file that ships with the shell configuration rather than
// with the module; README.md points at it.
//
// An announcement is written in Unicode tag characters, which are
// default-ignorable: they are part of the title string, but nothing renders
// them, so the window title is unchanged in the bar's tooltip, in the
// terminal's own title bar, and anywhere else a title is shown. (The tagged
// title was rendered against the untagged one with pango-view and came out
// byte-identical; marker_test.go checks the character class rather than the
// pixels, which is as close as a test can get.) The grammar is
//
//	U+E0001                        opens an announcement (LANGUAGE TAG)
//	one or more U+E0030 - U+E0039  the pid, in decimal (TAG DIGITs)
//	U+E007F                        closes it (CANCEL TAG)
//
// A pid is what an announcement carries, rather than, say, the terminal's own
// surface id, because a pid needs no table of ids and no cooperation beyond
// what /proc already offers: the module can look the tree up directly.
const (
	markerStart = '\U000E0001' // LANGUAGE TAG
	markerEnd   = '\U000E007F' // CANCEL TAG
	markerDigit = '\U000E0030' // TAG DIGIT ZERO

	// markerDigits is the widest pid the grammar accepts. Pids stop at 2^22 by
	// default, so this is slack; it is here so that a title full of tag
	// characters cannot make the parser walk a long digit run or read a number
	// too large to be a pid.
	markerDigits = 9
)

// markerSpan is one announcement found in a title: the bytes it occupies and
// the pid it names.
type markerSpan struct {
	start, end int
	pid        int
}

// marker returns the announcement that names a pid. The shell side builds the
// same string, and the tests use this to check both ends of the protocol.
func marker(pid int) string {
	var b strings.Builder
	b.WriteRune(markerStart)
	for _, digit := range strconv.Itoa(pid) {
		b.WriteRune(markerDigit + rune(digit-'0'))
	}
	b.WriteRune(markerEnd)
	return b.String()
}

// markers returns every well-formed announcement in a title, left to right.
//
// Everything else in the string is left alone. A title is whatever the program
// in the window wrote, and tag characters are not this module's private
// property: an emoji tag sequence (a flag, for one) ends with the same CANCEL
// TAG, and text that only resembles an announcement must survive untouched.
func markers(title string) []markerSpan {
	var spans []markerSpan

	for i := 0; i < len(title); {
		r, size := utf8.DecodeRuneInString(title[i:])
		if r != markerStart {
			i += size
			continue
		}

		start := i
		i += size

		pid, digits := 0, 0
		for i < len(title) && digits < markerDigits {
			r, size = utf8.DecodeRuneInString(title[i:])
			if r < markerDigit || r > markerDigit+9 {
				break
			}
			pid = pid*10 + int(r-markerDigit)
			digits++
			i += size
		}

		// An announcement is an open, at least one digit, and a close. The scan
		// resumes at the rune that ended the digits either way, so a lone tag
		// character cannot hide a real announcement behind it.
		r, size = utf8.DecodeRuneInString(title[i:])
		if digits == 0 || r != markerEnd {
			continue
		}
		i += size

		spans = append(spans, markerSpan{start: start, end: i, pid: pid})
	}

	return spans
}

// parseMarker returns the pid the last announcement in a title names. The last
// one is the one that matters: announcements are appended to the text a window
// shows, so a program that replaced the title without knowing about the
// announcement leaves an older one in front of the shell's.
func parseMarker(title string) (int, bool) {
	spans := markers(title)
	if len(spans) == 0 {
		return 0, false
	}
	// Zero is not a process.
	if pid := spans[len(spans)-1].pid; pid > 0 {
		return pid, true
	}
	return 0, false
}

// stripMarker removes every announcement from a title, leaving the text the
// window shows. The bar matches window rules against titles and shows them in
// tooltips without the invisible tag characters a shell wrote for this module.
func stripMarker(title string) string {
	spans := markers(title)
	if len(spans) == 0 {
		return title
	}

	var b strings.Builder
	b.Grow(len(title))
	last := 0
	for _, span := range spans {
		b.WriteString(title[last:span.start])
		last = span.end
	}
	b.WriteString(title[last:])
	return b.String()
}
