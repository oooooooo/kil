package ui

import (
	"strings"
	"testing"
	"time"
)

// The caret moves and deletes by grapheme cluster, so that it never stops
// between a letter and its combining mark, or inside a flag or an emoji
// joined with ZWJ: text typed there split the character, and the caret was
// drawn as a blank cell.
func TestQueryIsEditedByGraphemeCluster(t *testing.T) {
	const (
		acute  = "e\u0301"                                  // e with a combining acute
		flags  = "\U0001F1EF\U0001F1F5\U0001F1FA\U0001F1F8" // two flags
		family = "\U0001F469\u200d\U0001F467"               // woman ZWJ girl
	)
	for _, tt := range []struct {
		name, query string
		keys        []Msg
		want        string
	}{
		{"Left over a combining mark", "caf" + acute, []Msg{key(KeyLeft), typed("!")}, "caf!" + acute},
		{"Right over a combining mark", acute + "x", []Msg{ctrl('a'), key(KeyRight), typed("!")}, acute + "!x"},
		{"Backspace removes a combining mark with its letter", "caf" + acute, []Msg{key(KeyBackspace)}, "caf"},
		{"Delete removes a combining mark with its letter", acute + "x", []Msg{ctrl('a'), key(KeyDelete)}, "x"},
		{"Left over a flag", flags, []Msg{key(KeyLeft), key(KeyBackspace)}, flags[8:]},
		{"Backspace removes a ZWJ sequence", "a" + family, []Msg{key(KeyBackspace)}, "a"},
		{"Alt-F ends after a word's last mark", "caf" + acute + " x", []Msg{ctrl('a'), alt('f'), typed("!")}, "caf" + acute + "! x"},
		{"Alt-B starts at a word's first letter", "x " + acute + "b", []Msg{alt('b'), typed("!")}, "x !" + acute + "b"},
		{"Ctrl-W stops at a space with a mark on it", "a \u0301b", []Msg{ctrl('w')}, "a \u0301"},
		{"a flag typed before another joins the one after", flags[8:], []Msg{ctrl('a'), typed(flags[:4]), typed("!")}, flags[:4] + flags[8:12] + "!" + flags[12:]},
		{"Ctrl-W takes a space with a mark on it as a space", "ab \u0301", []Msg{ctrl('w')}, ""},
		{"Delete that joins two flags leaves the caret after them", flags[:4] + " " + flags[4:12], []Msg{ctrl('a'), key(KeyRight), key(KeyDelete), typed("!")}, flags[:8] + "!" + flags[8:12]},
		{"Left at the start stays there", "ab", []Msg{ctrl('a'), key(KeyLeft), typed("!")}, "!ab"},
		{"Alt-B skips clusters that are not words", "ab \U0001F600 ", []Msg{alt('b'), typed("!")}, "!ab \U0001F600 "},
		{"Alt-B skips a cluster that starts with a prepended mark", "x \u0600a", []Msg{alt('b'), typed("!")}, "!x \u0600a"},
		{"Alt-F skips a cluster that starts with a prepended mark", "\u0600a x", []Msg{ctrl('a'), alt('f'), typed("!")}, "\u0600a x!"},
		{"a mark typed after a letter joins it", "cafe", []Msg{typed("\u0301"), key(KeyLeft), typed("!")}, "caf!" + acute},
	} {
		t.Run(tt.name, func(t *testing.T) {
			m := sized(New(items("101 a"), tt.query, "SIGKILL", noReload))
			m, _ = press(m, tt.keys...)
			if got := m.Query(); got != tt.want {
				t.Errorf("Query() = %+q, want %+q", got, tt.want)
			}
		})
	}
}

func TestCaretCoversAWholeGraphemeCluster(t *testing.T) {
	m := sized(New(items("101 a"), "e\u0301x", "SIGKILL", noReload))
	m, _ = press(m, ctrl('a'))
	lines := strings.Split(m.render(), "\n")
	prompt := lines[len(lines)-1]
	if !strings.Contains(prompt, "\x1b[7me\u0301"+reset) {
		t.Errorf("caret does not cover e with its mark:\n%+q", prompt)
	}
	if got := strings.TrimRight(plain(prompt), " "); got != "> e\u0301x" {
		t.Errorf("prompt = %+q, want %+q", got, "> e\u0301x")
	}
}

// A caret on a cluster of no width, as a zero-width space, is drawn as a
// space, as it would otherwise take no cell and not show.
func TestCaretOnAZeroWidthClusterShows(t *testing.T) {
	m := sized(New(items("101 a"), "\u200bx", "SIGKILL", noReload))
	m, _ = press(m, ctrl('a'))
	lines := strings.Split(m.render(), "\n")
	prompt := lines[len(lines)-1]
	if !strings.Contains(prompt, "\x1b[7m "+reset) {
		t.Errorf("caret on a zero-width space is not a space:\n%+q", prompt)
	}
}

// Word keys and edits on a long query, as a pasted Java class path, take
// time in proportion to the query: finding clusters anew at every step took
// seconds, during which kil drew nothing and read no key.
func TestEditingALongQueryIsQuick(t *testing.T) {
	long := strings.Repeat("a", 20000)
	for _, k := range []Msg{ctrl('w'), alt('b'), alt('f'), key(KeyBackspace), typed("x")} {
		in := newLineInput(long + " " + long)
		in.pos = len(long)
		done := make(chan struct{})
		go func() {
			defer close(done)
			lineEdit(k.(KeyPressMsg))(&in)
		}()
		select {
		case <-done:
		case <-time.After(time.Second):
			t.Fatalf("%v on a query of %d characters took over a second", k, 2*len(long)+1)
		}
	}
}
