package ui

import (
	"errors"
	"slices"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"

	"github.com/oooooooo/kil/internal/picker"
)

func items(lines ...string) []picker.Item {
	out := make([]picker.Item, len(lines))
	for i, l := range lines {
		out[i] = picker.Item{ID: l, Text: l}
	}
	return out
}

// plain strips colors, so tests about content need not know the palette.
func plain(view string) string { return ansi.Strip(view) }

func noReload() ([]picker.Item, error) { return nil, nil }

func press(m Model, keys ...tea.KeyMsg) (Model, tea.Cmd) {
	var cmd tea.Cmd
	for _, k := range keys {
		var next tea.Model
		next, cmd = m.Update(k)
		m = next.(Model)
	}
	return m, cmd
}

func typed(s string) tea.KeyMsg {
	return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(s)}
}

func alt(r rune) tea.KeyMsg {
	return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}, Alt: true}
}

func key(t tea.KeyType) tea.KeyMsg {
	return tea.KeyMsg{Type: t}
}

func sized(m Model) Model {
	next, _ := m.Update(tea.WindowSizeMsg{Width: 80, Height: 20})
	return next.(Model)
}

func targetTexts(m Model) []string {
	var out []string
	for _, it := range m.Targets() {
		out = append(out, it.Text)
	}
	return out
}

func TestTypingFiltersTheView(t *testing.T) {
	m := sized(New(items("101 node a", "202 python b"), "", "SIGKILL", noReload))
	m, _ = press(m, typed("pyt"))
	view := plain(m.View())
	if !strings.Contains(view, "202 python b") || strings.Contains(view, "101 node a") {
		t.Errorf("view after typing \"pyt\":\n%s", view)
	}
}

func TestInitialQueryIsApplied(t *testing.T) {
	m := sized(New(items("101 node a", "202 python b"), "node", "SIGKILL", noReload))
	if strings.Contains(plain(m.View()), "202 python b") {
		t.Errorf("initial query not applied:\n%s", plain(m.View()))
	}
}

func TestBackspaceAndCtrlUEditTheQuery(t *testing.T) {
	m := sized(New(items("101 node a", "202 python b"), "", "SIGKILL", noReload))
	m, _ = press(m, typed("pyx"), key(tea.KeyBackspace))
	if m.Query() != "py" {
		t.Errorf("Query() after backspace = %q, want \"py\"", m.Query())
	}
	m, _ = press(m, key(tea.KeyCtrlU))
	if m.Query() != "" {
		t.Errorf("Query() after Ctrl-U = %q, want empty", m.Query())
	}
}

func TestSelectionStaysVisibleAfterQueryChange(t *testing.T) {
	m := sized(New(items("101 node a", "202 python b"), "node", "SIGKILL", noReload))
	m, _ = press(m, key(tea.KeyTab), key(tea.KeyCtrlU), typed("python"))
	view := plain(m.View())
	if !strings.Contains(view, "101 node a") {
		t.Errorf("selected node row is not shown after changing the query:\n%s", view)
	}
}

func TestEnterAcceptsTargets(t *testing.T) {
	m := sized(New(items("101 node a", "202 python b"), "", "SIGKILL", noReload))
	m, cmd := press(m, key(tea.KeyUp), key(tea.KeyEnter))
	if m.Outcome() != Accepted {
		t.Fatalf("Outcome() = %v, want Accepted", m.Outcome())
	}
	if got := targetTexts(m); !slices.Equal(got, []string{"202 python b"}) {
		t.Errorf("Targets() = %v", got)
	}
	if cmd == nil {
		t.Error("Enter did not quit")
	}
}

func TestEnterWithNothingShownSelectsNothing(t *testing.T) {
	m := sized(New(items("101 node a"), "zzz", "SIGKILL", noReload))
	m, _ = press(m, key(tea.KeyEnter))
	if m.Outcome() != NothingSelected {
		t.Errorf("Outcome() = %v, want NothingSelected", m.Outcome())
	}
}

func TestEscAndCtrlCAbort(t *testing.T) {
	for _, k := range []tea.KeyType{tea.KeyEsc, tea.KeyCtrlC} {
		m := sized(New(items("101 node a"), "", "SIGKILL", noReload))
		m, _ = press(m, key(k))
		if m.Outcome() != Aborted {
			t.Errorf("Outcome() after %v = %v, want Aborted", k, m.Outcome())
		}
		if len(m.Targets()) != 0 {
			t.Errorf("Targets() after %v = %v, want none", k, m.Targets())
		}
	}
}

func TestAltASelectsEveryShownRow(t *testing.T) {
	m := sized(New(items("101 node a", "202 python b", "203 python c"), "python", "SIGKILL", noReload))
	m, _ = press(m, alt('a'), key(tea.KeyEnter))
	if got := targetTexts(m); !slices.Equal(got, []string{"202 python b", "203 python c"}) {
		t.Errorf("Targets() = %v", got)
	}
}

func TestCtrlRReloadsTheList(t *testing.T) {
	reload := func() ([]picker.Item, error) { return items("101 node a", "404 go d"), nil }
	m := sized(New(items("101 node a"), "", "SIGKILL", reload))
	m, cmd := press(m, key(tea.KeyCtrlR))
	if cmd == nil {
		t.Fatal("Ctrl-R returned no command")
	}
	next, _ := m.Update(cmd())
	m = next.(Model)
	if !strings.Contains(plain(m.View()), "404 go d") {
		t.Errorf("reloaded row not shown:\n%s", plain(m.View()))
	}
}

func TestFailedReloadEndsWithoutTargets(t *testing.T) {
	reload := func() ([]picker.Item, error) { return nil, errors.New("ps failed") }
	m := sized(New(items("101 node a"), "", "SIGKILL", reload))
	m, _ = press(m, key(tea.KeyTab))
	m, cmd := press(m, key(tea.KeyCtrlR))
	next, quit := m.Update(cmd())
	m = next.(Model)
	if m.Outcome() != ListFailed || m.Err() == nil {
		t.Errorf("Outcome() = %v, Err() = %v; want ListFailed and an error", m.Outcome(), m.Err())
	}
	if len(m.Targets()) != 0 {
		t.Errorf("Targets() = %v, want none", m.Targets())
	}
	if quit == nil {
		t.Error("failed reload did not quit")
	}
}

func TestHeaderNamesTheSignal(t *testing.T) {
	// The help text is the fzf-based kil's and needs a wide terminal to show
	// the signal, as it did there.
	next, _ := New(items("101 node a"), "", "SIGTERM", noReload).Update(tea.WindowSizeMsg{Width: 200, Height: 20})
	m := next.(Model)
	if !strings.Contains(plain(m.View()), "Enter to send SIGTERM") {
		t.Errorf("view does not name the signal:\n%s", plain(m.View()))
	}
}

func TestViewScrollsToKeepTheCursorVisible(t *testing.T) {
	var lines []string
	for i := range 50 {
		lines = append(lines, "proc line "+string(rune('A'+i%26))+string(rune('a'+i/26)))
	}
	m := sized(New(items(lines...), "", "SIGKILL", noReload))
	for range 40 {
		m, _ = press(m, key(tea.KeyUp))
	}
	view := plain(m.View())
	if !strings.Contains(view, lines[40]) {
		t.Errorf("cursor row %q not in view:\n%s", lines[40], view)
	}
	if n := strings.Count(view, "\n") + 1; n != 20 {
		t.Errorf("view has %d lines, want the window height 20", n)
	}
}

func TestMovingBackInsideTheWindowDoesNotScroll(t *testing.T) {
	var lines []string
	for i := range 50 {
		lines = append(lines, "proc line "+string(rune('A'+i%26))+string(rune('a'+i/26)))
	}
	m := sized(New(items(lines...), "", "SIGKILL", noReload))
	for range 40 {
		m, _ = press(m, key(tea.KeyUp))
	}
	// Rows grow upward from the header, so the window's topmost row is the
	// first non-blank line.
	firstRow := func(m Model) string {
		for _, l := range strings.Split(plain(m.View()), "\n") {
			if strings.TrimSpace(l) != "" {
				return l
			}
		}
		return ""
	}
	before := firstRow(m)
	m, _ = press(m, key(tea.KeyDown))
	if after := firstRow(m); after != before {
		t.Errorf("first row changed from %q to %q after moving back inside the window", before, after)
	}
}

func viewLines(m Model) []string {
	return strings.Split(plain(m.View()), "\n")
}

func TestLayoutMatchesFzf(t *testing.T) {
	m := sized(New(items("101 node a", "202 python b"), "py", "SIGKILL", noReload))
	lines := viewLines(m)
	n := len(lines)
	// The trailing space is the reverse-video caret.
	if strings.TrimRight(lines[n-1], " ") != "> py" {
		t.Errorf("prompt line = %q, want \"> py\" at the bottom", lines[n-1])
	}
	if !strings.HasPrefix(lines[n-2], "  1/2 (0) ─") {
		t.Errorf("info line = %q, want \"  1/2 (0) ─...\"", lines[n-2])
	}
	if !strings.HasPrefix(lines[n-3], "  kil: type to filter") {
		t.Errorf("header line = %q, want the key help", lines[n-3])
	}
	if !strings.HasPrefix(lines[n-4], "▌ 202 python b") {
		t.Errorf("first row = %q, want the best match just above the header", lines[n-4])
	}
}

func TestSelectedRowShowsMarker(t *testing.T) {
	m := sized(New(items("101 node a", "202 python b"), "", "SIGKILL", noReload))
	m, _ = press(m, key(tea.KeyTab))
	view := plain(m.View())
	if !strings.Contains(view, "▌┃101 node a") {
		t.Errorf("selected row has no ┃ marker:\n%s", view)
	}
	if !strings.Contains(view, "(1)") {
		t.Errorf("info line does not count the selection:\n%s", view)
	}
}

func TestLongLineShiftsToShowTheMatch(t *testing.T) {
	long := "101 java " + strings.Repeat("x", 100) + " needle"
	m := sized(New(items(long), "'needle", "SIGKILL", noReload))
	view := plain(m.View())
	if !strings.Contains(view, "··") || !strings.Contains(view, "needle") {
		t.Errorf("long line does not show the match with an ellipsis:\n%s", view)
	}
	for _, l := range strings.Split(view, "\n") {
		if w := ansi.StringWidth(l); w > 80 {
			t.Errorf("line is %d columns wide, want at most 80: %q", w, l)
		}
	}
}

func TestLongLineWithoutMatchIsCutWithEllipsis(t *testing.T) {
	long := "101 java " + strings.Repeat("x", 100)
	m := sized(New(items(long), "", "SIGKILL", noReload))
	view := plain(m.View())
	if !strings.Contains(view, "xx··") {
		t.Errorf("long line is not cut with a trailing ellipsis:\n%s", view)
	}
}

func TestColumnsAndMatchesAreColored(t *testing.T) {
	line := "  101     1 alice    sleep           sleep 60"
	other := "  202     1 bob      sleep           sleep 99"
	m := sized(New(items(line, other), "'60", "SIGKILL", noReload))
	m, _ = press(m, key(tea.KeyCtrlU)) // empty query: both rows, cursor on the first
	m, _ = press(m, typed("'99"))
	view := m.View()
	for name, want := range map[string]string{
		"PID":           fg(colorPID) + "202",
		"PPID":          fg(colorPPID) + "1",
		"USER":          fg(colorUser) + "bob",
		"cursor match":  fg(colorMatchFocused) + "9",
		"cursor row bg": bg(colorCursorBg),
	} {
		if !strings.Contains(view, want) {
			t.Errorf("%s is not colored (want %q in the view):\n%q", name, want, view)
		}
	}

	m, _ = press(m, key(tea.KeyCtrlU))
	view = m.View()
	if !strings.Contains(view, fg(colorPID)+"101") {
		t.Errorf("PID of a non-cursor row is not colored:\n%q", view)
	}
}

func TestMatchOnNonCursorRowIsColored(t *testing.T) {
	m := sized(New(items("  101 1 alice sleep zzz", "  202 1 bob sleep zzz"), "zzz", "SIGKILL", noReload))
	view := m.View()
	if !strings.Contains(view, fg(colorMatch)+"z") {
		t.Errorf("match on the non-cursor row is not colored:\n%q", view)
	}
}

// fzf draws ·, ▌, ─, and │ one column wide even in East Asian locales, where
// they are ambiguous-width, and so must kil, or rows are cut short.
func TestWidthsIgnoreEastAsianLocale(t *testing.T) {
	t.Setenv("LANG", "ja_JP.UTF-8")
	t.Setenv("LC_ALL", "ja_JP.UTF-8")
	t.Setenv("RUNEWIDTH_EASTASIAN", "1")
	long := "101 java " + strings.Repeat("x", 200)
	m := sized(New(items(long), "", "SIGKILL", noReload))
	lines := viewLines(m)
	n := len(lines)
	// Each check counts runes, which is the column count when every
	// character is one column wide.
	if got := len([]rune(lines[n-4])); got != 79 {
		t.Errorf("row is %d columns, want 79 (as fzf: the last column is left for the scrollbar):\n%q", got, lines[n-4])
	}
	if got := len([]rune(lines[n-2])); got != 79 {
		t.Errorf("info line is %d columns, want 79:\n%q", got, lines[n-2])
	}
	if got := len([]rune(lines[n-3])); got != 79 {
		t.Errorf("header line is %d columns, want 79:\n%q", got, lines[n-3])
	}
}

// As in fzf's default layout, the best match is at the bottom: Up moves to
// the next match, and Tab toggles and then moves down, toward the best.
func TestKeysMoveAsInFzfDefaultLayout(t *testing.T) {
	m := sized(New(items("101 node a", "202 python b", "303 ruby c"), "", "SIGKILL", noReload))
	m, _ = press(m, key(tea.KeyUp), key(tea.KeyUp)) // ruby
	m, _ = press(m, key(tea.KeyTab))                // select ruby, move down to python
	m, _ = press(m, key(tea.KeyShiftTab))           // select python, move up
	m, _ = press(m, key(tea.KeyEnter))
	// Targets come in the order they were selected.
	if got := targetTexts(m); !slices.Equal(got, []string{"303 ruby c", "202 python b"}) {
		t.Errorf("Targets() = %v, want ruby, then python", got)
	}
}

func TestTabMovesDownTowardTheBestMatch(t *testing.T) {
	m := sized(New(items("101 node a", "202 python b"), "", "SIGKILL", noReload))
	m, _ = press(m, key(tea.KeyUp), key(tea.KeyTab), key(tea.KeyTab), key(tea.KeyEnter))
	if got := targetTexts(m); !slices.Equal(got, []string{"202 python b", "101 node a"}) {
		t.Errorf("Targets() = %v, want python, then node, as selected", got)
	}
}

// As fzf with its default --hscroll-off=10, a shifted line keeps 10 columns
// after the last match visible.
func TestShiftedLineKeepsTenColumnsAfterTheMatch(t *testing.T) {
	long := "101 " + strings.Repeat("x", 100) + "needle" + "abcdefghijklmnopqrstuvwxyz"
	m := sized(New(items(long), "'needle", "SIGKILL", noReload))
	view := plain(m.View())
	if !strings.Contains(view, "needleabcdefghij··") {
		t.Errorf("shifted line does not keep 10 columns after the match:\n%s", view)
	}
}

// When little follows the match, fzf shows the line up to its end.
func TestShiftedLineShowsAShortTail(t *testing.T) {
	long := "101 " + strings.Repeat("x", 100) + "needle tail"
	m := sized(New(items(long), "'needle", "SIGKILL", noReload))
	view := plain(m.View())
	if !strings.Contains(view, "needle tail") || strings.Contains(view, "tail··") {
		t.Errorf("shifted line does not end with the short tail:\n%s", view)
	}
}

func editing(t *testing.T, want string, keys ...tea.KeyMsg) {
	t.Helper()
	m := sized(New(items("101 ruby a"), "", "SIGKILL", noReload))
	m, _ = press(m, keys...)
	if m.Query() != want {
		t.Errorf("Query() = %q, want %q", m.Query(), want)
	}
}

func TestLineEditingKeys(t *testing.T) {
	left, right := key(tea.KeyLeft), key(tea.KeyRight)
	t.Run("Ctrl-A moves to the start", func(t *testing.T) {
		editing(t, "ruby", typed("uby"), key(tea.KeyCtrlA), typed("r"))
	})
	t.Run("Home moves to the start", func(t *testing.T) {
		editing(t, "ruby", typed("uby"), key(tea.KeyHome), typed("r"))
	})
	t.Run("Ctrl-E moves to the end", func(t *testing.T) {
		editing(t, "ruby", typed("rub"), key(tea.KeyCtrlA), key(tea.KeyCtrlE), typed("y"))
	})
	t.Run("End moves to the end", func(t *testing.T) {
		editing(t, "ruby", typed("rub"), key(tea.KeyHome), key(tea.KeyEnd), typed("y"))
	})
	t.Run("Left and Right move by a character", func(t *testing.T) {
		editing(t, "ruby", typed("ry"), left, left, right, typed("ub"))
	})
	t.Run("Ctrl-B and Ctrl-F move by a character", func(t *testing.T) {
		editing(t, "ruby", typed("ry"), key(tea.KeyCtrlB), key(tea.KeyCtrlB), key(tea.KeyCtrlF), typed("ub"))
	})
	t.Run("Alt-B moves to the previous word", func(t *testing.T) {
		editing(t, "node ruby", typed("node uby"), alt('b'), typed("r"))
	})
	t.Run("Alt-F moves past the next word", func(t *testing.T) {
		editing(t, "nodes ruby", typed("node ruby"), key(tea.KeyCtrlA), alt('f'), typed("s"))
	})
	t.Run("Ctrl-K deletes to the end", func(t *testing.T) {
		editing(t, "ruby", typed("rubyxx"), left, left, key(tea.KeyCtrlK))
	})
	t.Run("Ctrl-U deletes to the start", func(t *testing.T) {
		editing(t, "ruby", typed("xxruby"), key(tea.KeyCtrlA), right, right, key(tea.KeyCtrlU))
	})
	t.Run("Ctrl-W deletes the previous word", func(t *testing.T) {
		editing(t, "node ", typed("node ruby"), key(tea.KeyCtrlW))
	})
	t.Run("Backspace deletes before the cursor", func(t *testing.T) {
		editing(t, "ruby", typed("rubby"), left, left, key(tea.KeyBackspace))
	})
	t.Run("Delete deletes at the cursor", func(t *testing.T) {
		editing(t, "ruby", typed("ruxby"), left, left, left, key(tea.KeyDelete))
	})
	t.Run("typing inserts at the cursor", func(t *testing.T) {
		editing(t, "ruby", typed("rby"), key(tea.KeyCtrlA), right, typed("u"))
	})
}

func TestCaretIsDrawnAtTheCursor(t *testing.T) {
	m := sized(New(items("101 ruby a"), "", "SIGKILL", noReload))
	m, _ = press(m, typed("ruby"), key(tea.KeyCtrlA))
	lines := strings.Split(m.View(), "\n")
	prompt := lines[len(lines)-1]
	if !strings.Contains(prompt, "\x1b[7mr") {
		t.Errorf("caret is not on the first character:\n%q", prompt)
	}
	if got := strings.TrimRight(plain(prompt), " "); got != "> ruby" {
		t.Errorf("prompt = %q, want \"> ruby\"", got)
	}
}

func TestCtrlXTogglesExactMatching(t *testing.T) {
	m := sized(New(items("101 ruby a", "202 resolve /usr/lib/systemd"), "ruby", "SIGKILL", noReload))
	if strings.Contains(plain(m.View()), "[exact]") {
		t.Errorf("fuzzy mode shows [exact]:\n%s", plain(m.View()))
	}
	m, _ = press(m, key(tea.KeyCtrlX))
	view := plain(m.View())
	if strings.Contains(view, "resolve") || !strings.Contains(view, "[exact]") {
		t.Errorf("Ctrl-X did not switch to exact matching:\n%s", view)
	}
	m, _ = press(m, key(tea.KeyCtrlX))
	if !strings.Contains(plain(m.View()), "resolve") {
		t.Errorf("second Ctrl-X did not switch back to fuzzy matching:\n%s", plain(m.View()))
	}
}

func TestWithExactStartsInExactMode(t *testing.T) {
	m := sized(New(items("101 ruby a", "202 resolve /usr/lib/systemd"), "ruby", "SIGKILL", noReload).WithExact(true))
	if strings.Contains(plain(m.View()), "resolve") {
		t.Errorf("WithExact(true) still matches fuzzily:\n%s", plain(m.View()))
	}
}

func TestAltATogglesEveryShownRow(t *testing.T) {
	m := sized(New(items("101 node a", "202 python b"), "", "SIGKILL", noReload))
	m, _ = press(m, alt('a'))
	if !strings.Contains(plain(m.View()), "(2)") {
		t.Fatalf("first Alt-A did not select both rows:\n%s", plain(m.View()))
	}
	m, _ = press(m, alt('a'))
	if !strings.Contains(plain(m.View()), "(0)") {
		t.Errorf("second Alt-A did not deselect both rows:\n%s", plain(m.View()))
	}
}
