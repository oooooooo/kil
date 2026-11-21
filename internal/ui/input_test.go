package ui

import (
	"strings"
	"testing"
	"time"

	"github.com/oooooooo/kil/internal/width"
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

// placeholder is how the prompt draws a cluster of no width.
const placeholder = "\x1b[4m" + "\x1b[38;5;161m" + "?"

// A cluster of no width in the query, as a zero-width space, is drawn as a
// "?" set apart by its underline and color, so that the query does not look
// like one without it, nor like one with a "?" typed, which match otherwise.
// It still takes part in matching.
func TestZeroWidthClusterInTheQueryShows(t *testing.T) {
	m := sized(New(items("101 zzq", "102 zz?q"), "zz\u200bq", "SIGKILL", noReload))
	prompt := promptLine(m)
	if got := strings.TrimRight(plain(prompt), " "); got != "> zz?q" {
		t.Errorf("prompt = %+q, want %+q", got, "> zz?q")
	}
	if strings.Count(prompt, placeholder) != 1 {
		t.Errorf("the placeholder is not set apart:\n%+q", prompt)
	}
	if got := strings.TrimSpace(infoText(m)); !strings.HasPrefix(got, "0/2") {
		t.Errorf("info line = %q, want the query with U+200B to match nothing", got)
	}
	m = sized(New(items("101 a"), "zz?q", "SIGKILL", noReload))
	if prompt := promptLine(m); strings.Contains(prompt, placeholder) || strings.Contains(prompt, "\x1b[4m") {
		t.Errorf("a typed ? is drawn as a placeholder:\n%+q", prompt)
	}
}

// An invisible character joined to a letter in the query, as U+200C ZERO
// WIDTH NON-JOINER, is drawn as a placeholder after the letter, as it would
// hide in the letter's cell otherwise.
func TestJoinedInvisibleInTheQueryShows(t *testing.T) {
	m := sized(New(items("101 a"), "zz\u200cq", "SIGKILL", noReload))
	prompt := promptLine(m)
	if got := strings.TrimRight(plain(prompt), " "); got != "> zz?q" || strings.Count(prompt, placeholder) != 1 {
		t.Errorf("prompt = %+q, want %+q with one placeholder", prompt, "> zz?q")
	}
	m = sized(New(items("101 a"), "z\uFE0Fq z\U000E0041q", "SIGKILL", noReload))
	if got := strings.TrimRight(plain(promptLine(m)), " "); got != "> z?q z?q" {
		t.Errorf("prompt = %+q, want a variation selector and a tag after a letter shown", got)
	}
}

// A cluster whose placeholders are wider than the prompt is laid out a
// placeholder at a time, so its letters and the caret still show rather than
// the whole cluster leave the prompt.
func TestClusterWiderThanThePromptShows(t *testing.T) {
	query := "zzq" + strings.Repeat("\u200c", 100)
	m := drawn(New(items("101 a"), query, "SIGKILL", noReload), 40, 20)
	if got := plain(promptLine(m)); got != "> "+strings.Repeat("?", 37)+" " {
		t.Errorf("caret at the end: prompt = %q", got)
	}
	m, _ = press(m, key(KeyLeft))
	if got := plain(promptLine(m)); got != "> q"+strings.Repeat("?", 37) || !strings.Contains(promptLine(m), "\x1b[7mq") {
		t.Errorf("caret on q: prompt = %+q, want q reversed and cut at the edge", promptLine(m))
	}
	m = drawn(New(items("101 a"), "ab\u200c\u200c", "SIGKILL", noReload), 3, 20)
	m, _ = press(m, key(KeyLeft))
	if got := plain(promptLine(m)); got != "> b" {
		t.Errorf("3 columns, caret on b: prompt = %q, want \"> b\"", got)
	}
}

// The caret's cluster is laid out before the text before it, so that long
// text before the caret cannot push its placeholders or a wide character off
// the prompt.
func TestCaretClusterKeepsItsRoom(t *testing.T) {
	a := strings.Repeat("a", 60)
	m := drawn(New(items("101 a"), a+"z\u200cb", "SIGKILL", noReload), 40, 20)
	m, _ = press(m, key(KeyLeft), key(KeyLeft))
	if got := plain(promptLine(m)); !strings.HasSuffix(got, "az?") || width.String(got) != 40 {
		t.Errorf("caret on z with a ZWNJ: prompt = %q, want it to end with the z and its placeholder", got)
	}
	m = drawn(New(items("101 a"), a+"\u6F22b", "SIGKILL", noReload), 40, 20)
	m, _ = press(m, key(KeyLeft), key(KeyLeft))
	if got := plain(promptLine(m)); !strings.HasSuffix(got, "a\u6F22") {
		t.Errorf("caret on a wide character: prompt = %q, want it to end with the character", got)
	}
	m = drawn(New(items("101 a"), "a\u6F22", "SIGKILL", noReload), 4, 20)
	m, _ = press(m, key(KeyLeft))
	if got := plain(promptLine(m)); got != "> \u6F22" {
		t.Errorf("4 columns, caret on a wide character: prompt = %q, want %q", got, "> \u6F22")
	}
}

// A combining mark after a placeholder would combine with no cell, as the
// placeholder's colors part them, so it is a placeholder too.
func TestMarkAfterAPlaceholderShows(t *testing.T) {
	m := sized(New(items("101 a"), "z\u200c\u0301q", "SIGKILL", noReload))
	if got := strings.TrimRight(plain(promptLine(m)), " "); got != "> z??q" {
		t.Errorf("prompt = %+q, want %+q", got, "> z??q")
	}
}

// Combining marks at the start of the query have nothing to combine with,
// so they are a cluster of no width, drawn as one placeholder.
func TestLeadingCombiningMarkInTheQueryShows(t *testing.T) {
	m := sized(New(items("101 a"), "\u0301\u0302x", "SIGKILL", noReload))
	if got := strings.TrimRight(plain(promptLine(m)), " "); got != "> ?x" {
		t.Errorf("prompt = %+q, want %+q", got, "> ?x")
	}
}

// A caret on a cluster of no width shows the placeholder in reverse video,
// as the caret moves over each one and Backspace deletes it.
func TestCaretOnAZeroWidthClusterShows(t *testing.T) {
	m := sized(New(items("101 a"), "a\u200b\u200bb", "SIGKILL", noReload))
	m, _ = press(m, ctrl('a'), key(KeyRight), key(KeyRight))
	prompt := promptLine(m)
	if got := strings.TrimRight(plain(prompt), " "); got != "> a??b" {
		t.Errorf("prompt = %+q, want %+q", got, "> a??b")
	}
	if strings.Count(prompt, placeholder) != 2 || !strings.HasSuffix(prompt[:strings.Index(prompt, "\x1b[7m"+placeholder)], "\x1b[1ma"+placeholder+"\x1b[24m\x1b[39m"+reset) {
		t.Errorf("caret is not a reversed placeholder on the second one:\n%+q", prompt)
	}
	m, _ = press(m, key(KeyBackspace))
	if got := strings.TrimRight(plain(promptLine(m)), " "); got != "> a?b" || m.Query() != "a\u200bb" {
		t.Errorf("after Backspace prompt = %+q, query %+q; want \"> a?b\" and one U+200B left", got, m.Query())
	}
}

// Placeholders take a column each, so a query of many still scrolls to keep
// the caret on screen and the prompt within the terminal, whether the caret
// is at its end, its start, or in its middle.
func TestManyZeroWidthClustersFitThePrompt(t *testing.T) {
	query := strings.Repeat("\u200b", 100) + "x" + strings.Repeat("\u200b", 100)
	m := drawn(New(items("101 a"), query, "SIGKILL", noReload), 40, 20)
	check := func(where string, want string) {
		t.Helper()
		prompt := plain(promptLine(m))
		if prompt != want {
			t.Errorf("with the caret at %s, prompt = %q (%d columns), want %q", where, prompt, width.String(prompt), want)
		}
	}
	check("the end", "> "+strings.Repeat("?", 37)+" ")
	m, _ = press(m, ctrl('a'))
	check("the start", "> "+strings.Repeat("?", 38))
	for range 100 {
		m, _ = press(m, key(KeyRight))
	}
	check("the x", "> "+strings.Repeat("?", 37)+"x")
}

// On a terminal of three columns, a caret on a cluster of no width still
// shows, as a reversed placeholder.
func TestZeroWidthCaretOnANarrowTerminal(t *testing.T) {
	m := drawn(New(items("101 a"), "\u200b", "SIGKILL", noReload), 3, 20)
	m, _ = press(m, ctrl('a'))
	if prompt := promptLine(m); !strings.Contains(prompt, "\x1b[7m"+placeholder) || width.String(plain(prompt)) > 3 {
		t.Errorf("prompt = %+q, want a reversed placeholder within 3 columns", prompt)
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
