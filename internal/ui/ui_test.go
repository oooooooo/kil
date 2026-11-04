package ui

import (
	"errors"
	"fmt"
	"maps"
	"reflect"
	"slices"
	"strings"
	"testing"

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

func press(m Model, keys ...Msg) (Model, Cmd) {
	var cmd Cmd
	for _, k := range keys {
		var next Model
		next, cmd = m.Update(k)
		m = next
	}
	return m, cmd
}

func typed(s string) KeyPressMsg {
	return KeyPressMsg{Code: []rune(s)[0], Text: s}
}

func alt(r rune) KeyPressMsg {
	return KeyPressMsg{Code: r, Mod: ModAlt}
}

func ctrl(r rune) KeyPressMsg {
	return KeyPressMsg{Code: r, Mod: ModCtrl}
}

func key(code rune, mods ...KeyMod) KeyPressMsg {
	k := KeyPressMsg{Code: code}
	for _, mod := range mods {
		k.Mod |= mod
	}
	return k
}

func sized(m Model) Model {
	return drawn(m, 80, 20)
}

// drawn returns m on a terminal of the given size, after the user has had
// time to see it, so that Enter does not wait for rows to be seen.
func drawn(m Model, width, height int) Model {
	return settled(resized(m, width, height))
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
	view := plain(m.render())
	if !strings.Contains(view, "202 python b") || strings.Contains(view, "101 node a") {
		t.Errorf("view after typing \"pyt\":\n%s", view)
	}
}

func TestInitialQueryIsApplied(t *testing.T) {
	m := sized(New(items("101 node a", "202 python b"), "node", "SIGKILL", noReload))
	if strings.Contains(plain(m.render()), "202 python b") {
		t.Errorf("initial query not applied:\n%s", plain(m.render()))
	}
}

func TestBackspaceAndCtrlUEditTheQuery(t *testing.T) {
	m := sized(New(items("101 node a", "202 python b"), "", "SIGKILL", noReload))
	m, _ = press(m, typed("pyx"), key(KeyBackspace))
	if m.Query() != "py" {
		t.Errorf("Query() after backspace = %q, want \"py\"", m.Query())
	}
	m, _ = press(m, ctrl('u'))
	if m.Query() != "" {
		t.Errorf("Query() after Ctrl-U = %q, want empty", m.Query())
	}
}

func TestSelectionStaysVisibleAfterQueryChange(t *testing.T) {
	m := sized(New(items("101 node a", "202 python b"), "node", "SIGKILL", noReload))
	m, _ = press(m, key(KeyTab), ctrl('u'), typed("python"))
	view := plain(m.render())
	if !strings.Contains(view, "101 node a") {
		t.Errorf("selected node row is not shown after changing the query:\n%s", view)
	}
}

func TestEnterAcceptsTargets(t *testing.T) {
	m := sized(New(items("101 node a", "202 python b"), "", "SIGKILL", noReload))
	m, cmd := press(m, key(KeyUp), key(KeyEnter))
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
	m, _ = press(m, key(KeyEnter))
	if m.Outcome() != NothingSelected {
		t.Errorf("Outcome() = %v, want NothingSelected", m.Outcome())
	}
}

func TestEscAndCtrlCAbort(t *testing.T) {
	for _, k := range []KeyPressMsg{key(KeyEscape), ctrl('c')} {
		m := sized(New(items("101 node a"), "", "SIGKILL", noReload))
		m, _ = press(m, k)
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
	m, _ = press(m, alt('a'), key(KeyEnter))
	if got := targetTexts(m); !slices.Equal(got, []string{"202 python b", "203 python c"}) {
		t.Errorf("Targets() = %v", got)
	}
}

func TestCtrlRReloadsTheList(t *testing.T) {
	reload := func() ([]picker.Item, error) { return items("101 node a", "404 go d"), nil }
	m := sized(New(items("101 node a"), "", "SIGKILL", reload))
	m, cmd := press(m, ctrl('r'))
	if cmd == nil {
		t.Fatal("Ctrl-R returned no command")
	}
	next, _ := m.Update(cmd())
	m = next
	if !strings.Contains(plain(m.render()), "404 go d") {
		t.Errorf("reloaded row not shown:\n%s", plain(m.render()))
	}
}

func TestFailedReloadEndsWithoutTargets(t *testing.T) {
	reload := func() ([]picker.Item, error) { return nil, errors.New("ps failed") }
	m := sized(New(items("101 node a"), "", "SIGKILL", reload))
	m, _ = press(m, key(KeyTab))
	m, cmd := press(m, ctrl('r'))
	next, quit := m.Update(cmd())
	m = next
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

// A narrow terminal cut the full key list before the keys that matter, so
// the header names only the selection keys and the key for the full help.
// Enter is in it, with the signal it sends, because KIL_SIGNAL can change it.
func TestHeaderShowsTheMainKeysOnly(t *testing.T) {
	m := drawn(New(items("101 node a"), "", "SIGTERM", noReload), 60, 20)
	lines := viewLines(m)
	if got, want := lines[len(lines)-3], "  Tab select, Alt-A all, Enter SIGTERM, Alt-H help"; got != want {
		t.Errorf("header line = %q, want %q", got, want)
	}
}

func TestAltHShowsHelpNamingTheSignal(t *testing.T) {
	m := sized(New(items("101 node a"), "", "SIGTERM", noReload))
	m, _ = press(m, alt('h'))
	view := plain(m.render())
	for _, want := range []string{"Enter", "SIGTERM", "Ctrl-X", "Ctrl-R", "Esc"} {
		if !strings.Contains(view, want) {
			t.Errorf("help does not mention %q:\n%s", want, view)
		}
	}
	if strings.Contains(view, "101 node a") {
		t.Errorf("help is drawn over the rows, but a row is shown:\n%s", view)
	}
}

// The rows are hidden while the help is shown, so Enter must not signal
// one; any key only closes the help.
func TestKeyWhileHelpIsShownOnlyClosesIt(t *testing.T) {
	m := sized(New(items("101 node a"), "", "SIGKILL", noReload))
	m, _ = press(m, alt('h'), key(KeyEnter))
	if m.Outcome() != Running {
		t.Errorf("Enter with the help shown: Outcome() = %v, want Running", m.Outcome())
	}
	if !strings.Contains(plain(m.render()), "101 node a") {
		t.Errorf("Enter did not close the help:\n%s", plain(m.render()))
	}
}

// Esc leaves the help back to the list, as it leaves a help screen in most
// programs; Ctrl-C still aborts.
func TestEscWhileHelpIsShownClosesIt(t *testing.T) {
	m := sized(New(items("101 node a"), "", "SIGKILL", noReload))
	m, _ = press(m, alt('h'), key(KeyEsc))
	if m.Outcome() != Running {
		t.Errorf("Esc with the help shown: Outcome() = %v, want Running", m.Outcome())
	}
	if !strings.Contains(plain(m.render()), "101 node a") {
		t.Errorf("Esc did not close the help:\n%s", plain(m.render()))
	}
}

func TestCtrlCWhileHelpIsShownAborts(t *testing.T) {
	m := sized(New(items("101 node a"), "", "SIGKILL", noReload))
	m, _ = press(m, alt('h'), ctrl('c'))
	if m.Outcome() != Aborted {
		t.Errorf("Ctrl-C with the help shown: Outcome() = %v, want Aborted", m.Outcome())
	}
}

func TestViewScrollsToKeepTheCursorVisible(t *testing.T) {
	var lines []string
	for i := range 50 {
		lines = append(lines, "proc line "+string(rune('A'+i%26))+string(rune('a'+i/26)))
	}
	m := sized(New(items(lines...), "", "SIGKILL", noReload))
	for range 40 {
		m, _ = press(m, key(KeyUp))
	}
	view := plain(m.render())
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
		m, _ = press(m, key(KeyUp))
	}
	// Rows grow upward from the header, so the window's topmost row is the
	// first non-blank line.
	firstRow := func(m Model) string {
		for _, l := range strings.Split(plain(m.render()), "\n") {
			if strings.TrimSpace(l) != "" {
				return l
			}
		}
		return ""
	}
	before := firstRow(m)
	m, _ = press(m, key(KeyDown))
	if after := firstRow(m); after != before {
		t.Errorf("first row changed from %q to %q after moving back inside the window", before, after)
	}
}

func viewLines(m Model) []string {
	return strings.Split(plain(m.render()), "\n")
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
	if !strings.HasPrefix(lines[n-3], "  Tab select") {
		t.Errorf("header line = %q, want the key help", lines[n-3])
	}
	if !strings.HasPrefix(lines[n-4], "▌ 202 python b") {
		t.Errorf("first row = %q, want the best match just above the header", lines[n-4])
	}
}

func TestSelectedRowShowsMarker(t *testing.T) {
	m := sized(New(items("101 node a", "202 python b"), "", "SIGKILL", noReload))
	m, _ = press(m, key(KeyTab))
	view := plain(m.render())
	if !strings.Contains(view, "▌┃101 node a") {
		t.Errorf("selected row has no ┃ marker:\n%s", view)
	}
	if !strings.Contains(view, "(1)") {
		t.Errorf("info line does not count the selection:\n%s", view)
	}
}

// The PID, PPID, USER, and COMM columns stay visible, as fzf --freeze-left=4,
// and only the command line scrolls to show the match.
func TestLongLineShiftsToShowTheMatch(t *testing.T) {
	long := "  101     1 alice    node            " + "/usr/bin/node " + strings.Repeat("x", 100) + " needle"
	m := sized(New(items(long), "'needle", "SIGKILL", noReload))
	row := viewLines(m)[len(viewLines(m))-4]
	if !strings.HasPrefix(row, "▌   101     1 alice    node            ··") {
		t.Errorf("row does not keep the leading columns before the ellipsis:\n%q", row)
	}
	if !strings.Contains(row, "needle") {
		t.Errorf("row does not show the match:\n%q", row)
	}
	if w := ansi.StringWidth(row); w > 80 {
		t.Errorf("row is %d columns wide, want at most 80: %q", w, row)
	}
}

func TestLongLineWithoutMatchIsCutWithEllipsis(t *testing.T) {
	long := "101 java " + strings.Repeat("x", 100)
	m := sized(New(items(long), "", "SIGKILL", noReload))
	view := plain(m.render())
	if !strings.Contains(view, "xx··") {
		t.Errorf("long line is not cut with a trailing ellipsis:\n%s", view)
	}
}

func TestColumnsAndMatchesAreColored(t *testing.T) {
	line := "  101     1 alice    sleep           sleep 60"
	other := "  202     1 bob      sleep           sleep 99"
	m := sized(New(items(line, other), "'60", "SIGKILL", noReload))
	m, _ = press(m, ctrl('u')) // empty query: both rows, cursor on the first
	m, _ = press(m, typed("'99"))
	view := m.render()
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

	m, _ = press(m, ctrl('u'))
	view = m.render()
	if !strings.Contains(view, fg(colorPID)+"101") {
		t.Errorf("PID of a non-cursor row is not colored:\n%q", view)
	}
}

func TestMatchOnNonCursorRowIsColored(t *testing.T) {
	m := sized(New(items("  101 1 alice sleep zzz", "  202 1 bob sleep zzz"), "zzz", "SIGKILL", noReload))
	view := m.render()
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
}

// As in fzf's default layout, the best match is at the bottom: Up moves to
// the next match, and Tab toggles and then moves down, toward the best.
func TestKeysMoveAsInFzfDefaultLayout(t *testing.T) {
	m := sized(New(items("101 node a", "202 python b", "303 ruby c"), "", "SIGKILL", noReload))
	m, _ = press(m, key(KeyUp), key(KeyUp)) // ruby
	m, _ = press(m, key(KeyTab))            // select ruby, move down to python
	m, _ = press(m, key(KeyTab, ModShift))  // select python, move up
	m, _ = press(m, key(KeyEnter))
	// Targets come in the order they were selected.
	if got := targetTexts(m); !slices.Equal(got, []string{"303 ruby c", "202 python b"}) {
		t.Errorf("Targets() = %v, want ruby, then python", got)
	}
}

func TestTabMovesDownTowardTheBestMatch(t *testing.T) {
	m := sized(New(items("101 node a", "202 python b"), "", "SIGKILL", noReload))
	m, _ = press(m, key(KeyUp), key(KeyTab), key(KeyTab), key(KeyEnter))
	if got := targetTexts(m); !slices.Equal(got, []string{"202 python b", "101 node a"}) {
		t.Errorf("Targets() = %v, want python, then node, as selected", got)
	}
}

// As fzf with its default --hscroll-off=10, a shifted line keeps 10 columns
// after the last match visible.
func TestShiftedLineKeepsTenColumnsAfterTheMatch(t *testing.T) {
	long := "  101     1 alice    node            " + strings.Repeat("x", 100) + "needle" + "abcdefghijklmnopqrstuvwxyz"
	m := sized(New(items(long), "'needle", "SIGKILL", noReload))
	view := plain(m.render())
	if !strings.Contains(view, "needleabcdefghij··") {
		t.Errorf("shifted line does not keep 10 columns after the match:\n%s", view)
	}
}

// When little follows the match, fzf shows the line up to its end.
func TestShiftedLineShowsAShortTail(t *testing.T) {
	long := "  101     1 alice    node            " + strings.Repeat("x", 100) + "needle tail"
	m := sized(New(items(long), "'needle", "SIGKILL", noReload))
	view := plain(m.render())
	if !strings.Contains(view, "needle tail") || strings.Contains(view, "tail··") {
		t.Errorf("shifted line does not end with the short tail:\n%s", view)
	}
}

func editing(t *testing.T, want string, keys ...Msg) {
	t.Helper()
	m := sized(New(items("101 ruby a"), "", "SIGKILL", noReload))
	m, _ = press(m, keys...)
	if m.Query() != want {
		t.Errorf("Query() = %q, want %q", m.Query(), want)
	}
}

func TestLineEditingKeys(t *testing.T) {
	left, right := key(KeyLeft), key(KeyRight)
	t.Run("Ctrl-A moves to the start", func(t *testing.T) {
		editing(t, "ruby", typed("uby"), ctrl('a'), typed("r"))
	})
	t.Run("Home moves to the start", func(t *testing.T) {
		editing(t, "ruby", typed("uby"), key(KeyHome), typed("r"))
	})
	t.Run("Ctrl-E moves to the end", func(t *testing.T) {
		editing(t, "ruby", typed("rub"), ctrl('a'), ctrl('e'), typed("y"))
	})
	t.Run("End moves to the end", func(t *testing.T) {
		editing(t, "ruby", typed("rub"), key(KeyHome), key(KeyEnd), typed("y"))
	})
	t.Run("Left and Right move by a character", func(t *testing.T) {
		editing(t, "ruby", typed("ry"), left, left, right, typed("ub"))
	})
	t.Run("Ctrl-B and Ctrl-F move by a character", func(t *testing.T) {
		editing(t, "ruby", typed("ry"), ctrl('b'), ctrl('b'), ctrl('f'), typed("ub"))
	})
	t.Run("Alt-B moves to the previous word", func(t *testing.T) {
		editing(t, "node ruby", typed("node uby"), alt('b'), typed("r"))
	})
	t.Run("Alt-F moves past the next word", func(t *testing.T) {
		editing(t, "nodes ruby", typed("node ruby"), ctrl('a'), alt('f'), typed("s"))
	})
	t.Run("Ctrl-K deletes to the end", func(t *testing.T) {
		editing(t, "ruby", typed("rubyxx"), left, left, ctrl('k'))
	})
	t.Run("Ctrl-U deletes to the start", func(t *testing.T) {
		editing(t, "ruby", typed("xxruby"), ctrl('a'), right, right, ctrl('u'))
	})
	t.Run("Ctrl-W deletes the previous word", func(t *testing.T) {
		editing(t, "node ", typed("node ruby"), ctrl('w'))
	})
	t.Run("Backspace deletes before the cursor", func(t *testing.T) {
		editing(t, "ruby", typed("rubby"), left, left, key(KeyBackspace))
	})
	t.Run("Delete deletes at the cursor", func(t *testing.T) {
		editing(t, "ruby", typed("ruxby"), left, left, left, key(KeyDelete))
	})
	t.Run("typing inserts at the cursor", func(t *testing.T) {
		editing(t, "ruby", typed("rby"), ctrl('a'), right, typed("u"))
	})
}

func TestCaretIsDrawnAtTheCursor(t *testing.T) {
	m := sized(New(items("101 ruby a"), "", "SIGKILL", noReload))
	m, _ = press(m, typed("ruby"), ctrl('a'))
	lines := strings.Split(m.render(), "\n")
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
	if strings.Contains(plain(m.render()), "[exact]") {
		t.Errorf("fuzzy mode shows [exact]:\n%s", plain(m.render()))
	}
	m, _ = press(m, ctrl('x'))
	view := plain(m.render())
	if strings.Contains(view, "resolve") || !strings.Contains(view, "[exact]") {
		t.Errorf("Ctrl-X did not switch to exact matching:\n%s", view)
	}
	m, _ = press(m, ctrl('x'))
	if !strings.Contains(plain(m.render()), "resolve") {
		t.Errorf("second Ctrl-X did not switch back to fuzzy matching:\n%s", plain(m.render()))
	}
}

func TestWithExactStartsInExactMode(t *testing.T) {
	m := sized(New(items("101 ruby a", "202 resolve /usr/lib/systemd"), "ruby", "SIGKILL", noReload).WithExact(true))
	if strings.Contains(plain(m.render()), "resolve") {
		t.Errorf("WithExact(true) still matches fuzzily:\n%s", plain(m.render()))
	}
}

func TestAltATogglesEveryShownRow(t *testing.T) {
	m := sized(New(items("101 node a", "202 python b"), "", "SIGKILL", noReload))
	m, _ = press(m, alt('a'))
	if !strings.Contains(plain(m.render()), "(2)") {
		t.Fatalf("first Alt-A did not select both rows:\n%s", plain(m.render()))
	}
	m, _ = press(m, alt('a'))
	if !strings.Contains(plain(m.render()), "(0)") {
		t.Errorf("second Alt-A did not deselect both rows:\n%s", plain(m.render()))
	}
}

// A selected row pinned under another query scrolls the same way, so its
// PID stays visible.
func TestPinnedRowKeepsTheLeadingColumns(t *testing.T) {
	long := "  101     1 alice    node            " + strings.Repeat("x", 100) + " needle"
	m := sized(New(items(long, "  202     1 bob      sleep           sleep 60"), "", "SIGKILL", noReload))
	m, _ = press(m, key(KeyTab), typed("'needle"))
	if !strings.Contains(plain(m.render()), "┃  101     1 alice    node            ··") {
		t.Errorf("pinned row lost its leading columns:\n%s", plain(m.render()))
	}
}

// When the frozen columns alone fill the screen, the line is cut on the
// right instead.
func TestNarrowScreenCutsOnTheRight(t *testing.T) {
	long := "  101     1 alice    node            " + strings.Repeat("x", 100) + " needle"
	next, _ := New(items(long), "'needle", "SIGKILL", noReload).Update(WindowSizeMsg{Width: 30, Height: 10})
	m := next
	row := viewLines(m)[len(viewLines(m))-4]
	if !strings.HasPrefix(row, "▌   101     1 alice") || ansi.StringWidth(row) > 30 {
		t.Errorf("narrow row = %q, want the leading columns cut to 30 columns", row)
	}
}

func resized(m Model, width, height int) Model {
	next, _ := m.Update(WindowSizeMsg{Width: width, Height: height})
	return next
}

func numberedLines(n int) []string {
	var lines []string
	for i := range n {
		lines = append(lines, fmt.Sprintf("%03d proc", 100+i))
	}
	return lines
}

// Rows hidden below the window would still be signalled by Enter, so
// growing the terminal must bring them back once everything fits.
func TestGrowingTheTerminalShowsEveryRowThatFits(t *testing.T) {
	lines := numberedLines(30)
	m := drawn(New(items(lines...), "", "SIGKILL", noReload), 80, 8)
	for range 25 {
		m, _ = press(m, key(KeyUp))
	}
	m = resized(m, 80, 40)
	view := plain(m.render())
	for _, l := range lines {
		if !strings.Contains(view, l) {
			t.Fatalf("row %q is hidden although all 30 rows fit:\n%s", l, view)
		}
	}
}

// Lines below the terminal's height are not drawn, so on a tiny terminal
// the view must fit, keeping the cursor row and the prompt.
func TestTinyTerminalKeepsTheCursorRowVisible(t *testing.T) {
	for _, height := range []int{2, 3} {
		m := drawn(New(items("101 node a"), "", "SIGKILL", noReload), 80, height)
		lines := viewLines(m)
		if len(lines) > height {
			t.Errorf("height %d: view has %d lines:\n%s", height, len(lines), plain(m.render()))
		}
		if !strings.Contains(plain(m.render()), "101 node a") || !strings.HasPrefix(lines[len(lines)-1], ">") {
			t.Errorf("height %d: cursor row or prompt missing:\n%s", height, plain(m.render()))
		}
	}
}

// With room for the prompt only, no row is visible, so Enter must not
// signal one.
func TestEnterDoesNothingWithoutRoomForRows(t *testing.T) {
	m := drawn(New(items("101 node a"), "", "SIGKILL", noReload), 80, 1)
	m, cmd := press(m, key(KeyEnter))
	if m.Outcome() != Running || cmd != nil {
		t.Errorf("Enter on a one-line terminal: Outcome() = %v, want Running and no command", m.Outcome())
	}
}

// Control characters written to the terminal would move the caret or change
// colors, so pasted ones become spaces, which separate terms as a newline
// between pasted words means.
func TestPastedControlCharactersBecomeSpaces(t *testing.T) {
	m := sized(New(items("101 node a"), "", "SIGKILL", noReload))
	m, _ = press(m, PasteMsg{Content: "node\r\npy\x1b[31m"})
	if got := m.Query(); got != "node  py [31m" {
		t.Errorf("Query() = %q, want control characters replaced with spaces", got)
	}
}

func TestInitialQueryControlCharactersBecomeSpaces(t *testing.T) {
	m := sized(New(items("101 node a", "202 python b"), "node\npython", "SIGKILL", noReload))
	if got := m.Query(); got != "node python" {
		t.Errorf("Query() = %q, want \"node python\"", got)
	}
	if strings.Contains(plain(m.render()), "101 node a") {
		t.Errorf("initial query not applied as \"node python\":\n%s", plain(m.render()))
	}
}

func promptLine(m Model) string {
	lines := strings.Split(m.render(), "\n")
	return lines[len(lines)-1]
}

// A query wider than the terminal scrolls so that the caret stays visible.
func TestLongQueryKeepsTheCaretVisible(t *testing.T) {
	query := strings.Repeat("a", 60) + "END"
	m := drawn(New(items("101 node a"), query, "SIGKILL", noReload), 20, 8)
	prompt := promptLine(m)
	if w := ansi.StringWidth(prompt); w > 20 {
		t.Errorf("prompt is %d columns wide, want at most 20: %q", w, plain(prompt))
	}
	if !strings.Contains(plain(prompt), "END") || !strings.Contains(prompt, "\x1b[7m ") {
		t.Errorf("prompt does not show the end of the query and the caret: %q", prompt)
	}
	m, _ = press(m, ctrl('a'))
	prompt = promptLine(m)
	if !strings.HasPrefix(plain(prompt), "> aaa") || !strings.Contains(prompt, "\x1b[7ma") || ansi.StringWidth(prompt) > 20 {
		t.Errorf("after Ctrl-A, prompt does not show the start with the caret: %q", prompt)
	}
}

// On a one-line terminal no row fits, and the view must still draw the
// prompt rather than index the scrollbar outside its lines.
func TestOneLineTerminalDrawsOnlyThePrompt(t *testing.T) {
	for _, n := range []int{1, 5} {
		m := drawn(New(items(numberedLines(n)...), "", "SIGKILL", noReload), 80, 1)
		for range n {
			m, _ = press(m, key(KeyUp))
		}
		lines := viewLines(m)
		if len(lines) != 1 || !strings.HasPrefix(lines[0], ">") {
			t.Errorf("%d rows on a one-line terminal: view = %q, want the prompt only", n, lines)
		}
	}
}

// A selected process that exited stays on screen after Ctrl-R, so the total
// counts it too; otherwise more rows are shown than the total.
func TestInfoLineCountsKeptSelectionsInTheTotal(t *testing.T) {
	reload := func() ([]picker.Item, error) { return items("202 python b"), nil }
	m := sized(New(items("101 node a", "202 python b"), "", "SIGKILL", reload))
	m, cmd := press(m, key(KeyTab), ctrl('r'))
	next, _ := m.Update(cmd())
	m = next
	if !strings.Contains(plain(m.render()), "2/2 (1)") {
		t.Errorf("info line after the selected process exited, want \"2/2 (1)\":\n%s", plain(m.render()))
	}
}

// A ZWJ emoji is one character on screen, so a query that fits must not be
// cut inside it.
func TestQueryThatFitsIsNotCutInsideAnEmoji(t *testing.T) {
	query := "a👨‍👩‍👧‍👦END"
	m := drawn(New(nil, query, "SIGKILL", noReload), 8, 8)
	m, _ = press(m, ctrl('a'))
	if got := plain(promptLine(m)); got != "> "+query {
		t.Errorf("prompt = %q, want %q", got, "> "+query)
	}
}

// The help lists every key, so the header's short key list is not repeated
// below it; the help takes the header's line instead.
func TestHelpReplacesTheHeader(t *testing.T) {
	m := drawn(New(items("101 node a"), "", "SIGKILL", noReload), 80, 22)
	m, _ = press(m, alt('h'))
	lines := viewLines(m)
	if strings.Contains(plain(m.render()), "Alt-H help") {
		t.Errorf("header is shown with the help:\n%s", plain(m.render()))
	}
	if len(lines) != 22 || !strings.Contains(lines[len(lines)-3], "case sensitive") {
		t.Errorf("help does not fill the header's line in a 22-line view:\n%s", plain(m.render()))
	}
}

// The query syntax is fzf's, which a user who does not use fzf cannot
// guess, so the help explains it below the keys.
func TestHelpExplainsTheQuerySyntax(t *testing.T) {
	m := drawn(New(items("101 node a"), "", "SIGKILL", noReload), 80, 22)
	m, _ = press(m, alt('h'))
	view := plain(m.render())
	for _, want := range []string{"Tab", "Ctrl-Z", "close this help", "'node", "'node'", "^node", ".js$", "!node", "a | b", "a b", "case sensitive"} {
		if !strings.Contains(view, want) {
			t.Errorf("help on a 22-line terminal does not mention %q:\n%s", want, view)
		}
	}
}

// The keys and the query syntax are both two columns of text, so headings
// and a blank line tell them apart.
func TestHelpSeparatesKeysFromTheQuerySyntax(t *testing.T) {
	m := drawn(New(items("101 node a"), "", "SIGKILL", noReload), 80, 22)
	m, _ = press(m, alt('h'))
	lines := viewLines(m)
	keys := slices.Index(lines, "  Keys")
	query := slices.IndexFunc(lines, func(l string) bool { return strings.HasPrefix(l, "  Query") })
	if keys < 0 || query < 1 || keys > query || lines[query-1] != "" {
		t.Errorf("help has no \"Keys\" heading, or no blank line before the \"Query\" heading:\n%s", strings.Join(lines, "\n"))
	}
}

// Enter must only signal processes the user can see, so Alt-A selects the
// rows drawn on screen, not every match.
func TestAltASelectsOnlyTheRowsOnScreen(t *testing.T) {
	m := drawn(New(items(numberedLines(30)...), "", "SIGKILL", noReload), 80, 24)
	m, _ = press(m, alt('a'), key(KeyEnter))
	view := plain(m.render())
	for _, target := range targetTexts(m) {
		if !strings.Contains(view, target) {
			t.Errorf("target %q is not on screen:\n%s", target, view)
		}
	}
	if got := len(m.Targets()); got != 21 {
		t.Errorf("len(Targets()) = %d, want the 21 rows on screen", got)
	}
}

// recorder stands in for main's sender and records what Enter signalled.
type recorder struct{ sent [][]string }

func (r *recorder) send(its []picker.Item) {
	var texts []string
	for _, it := range its {
		texts = append(texts, it.Text)
	}
	r.sent = append(r.sent, texts)
}

// offscreenSelections returns a model with 21 rows selected by Alt-A on a
// 24-line terminal, then shrunk to 10 lines so that most of them are off
// screen.
func offscreenSelections(r *recorder) Model {
	m := drawn(New(items(numberedLines(30)...), "", "SIGKILL", noReload).WithSender(r.send), 80, 24)
	m, _ = press(m, alt('a'))
	return resized(m, 80, 10)
}

// Alt-A pins the rows it selects, which reorders the list, so the window
// must follow them: every row Alt-A selected stays on screen, and Alt-A
// again deselects exactly them.
func TestAltAKeepsTheRowsItSelectedOnScreen(t *testing.T) {
	m := drawn(New(items(numberedLines(30)...), "", "SIGKILL", noReload), 80, 24)
	for range 29 {
		m, _ = press(m, key(KeyUp))
	}
	m, _ = press(m, alt('a'))
	view := plain(m.render())
	for _, r := range m.picker.Rows() {
		if r.Selected && !strings.Contains(view, r.Item.Text) {
			t.Errorf("selected row %q is off screen after Alt-A:\n%s", r.Item.Text, view)
		}
	}
	m, _ = press(m, alt('a'))
	if n := m.picker.SelectedCount(); n != 0 {
		t.Errorf("SelectedCount() = %d after Alt-A twice, want 0", n)
	}
}

// Enter signals only the selected rows on screen and keeps kil open with
// the rest of the selection brought on screen, so the user sees what is
// left before signalling it.
func TestEnterSignalsOnlyTheSelectedRowsOnScreen(t *testing.T) {
	var r recorder
	m := offscreenSelections(&r)
	before := plain(m.render())
	m, _ = press(m, key(KeyEnter))
	if m.Outcome() != Running {
		t.Fatalf("Enter with selections off screen: Outcome() = %v, want Running", m.Outcome())
	}
	if len(r.sent) != 1 || len(r.sent[0]) == 0 {
		t.Fatalf("sent = %v, want one batch", r.sent)
	}
	after := plain(m.render())
	for _, s := range r.sent[0] {
		if !strings.Contains(before, s) {
			t.Errorf("signalled %q, which was off screen:\n%s", s, before)
		}
		if strings.Contains(after, s) {
			t.Errorf("signalled %q is still listed:\n%s", s, after)
		}
	}
	if left := 21 - len(r.sent[0]); !strings.Contains(after, fmt.Sprintf("%d selected left", left)) {
		t.Errorf("info line does not say %d selected left:\n%s", left, after)
	}
}

// Pressing Enter until kil ends signals every selection once, each while it
// was on screen.
func TestEnterRepeatedSignalsEverySelectionOnce(t *testing.T) {
	var r recorder
	m := offscreenSelections(&r)
	var all []string
	for range 10 {
		m = settled(m)
		view := plain(m.render())
		m, _ = press(m, key(KeyEnter))
		var batch []string
		if m.Outcome() == Accepted {
			batch = targetTexts(m)
		} else {
			batch = r.sent[len(r.sent)-1]
		}
		for _, s := range batch {
			if !strings.Contains(view, s) {
				t.Errorf("signalled %q, which was off screen:\n%s", s, view)
			}
		}
		all = append(all, batch...)
		if m.Outcome() != Running {
			break
		}
	}
	slices.Sort(all)
	if len(all) != 21 || len(slices.Compact(all)) != 21 {
		t.Errorf("signalled %d rows (%v), want the 21 selected once each", len(all), all)
	}
}

func partlySent(t *testing.T) Model {
	t.Helper()
	var r recorder
	m, _ := press(offscreenSelections(&r), key(KeyEnter))
	if m.Outcome() != Running {
		t.Fatalf("Outcome() = %v after a partial Enter, want Running", m.Outcome())
	}
	return m
}

// After a partial Enter, leaving with Esc would leave chosen processes
// running, so kil asks first.
func TestEscAfterAPartialEnterAsks(t *testing.T) {
	m, cmd := press(partlySent(t), key(KeyEsc))
	if m.Outcome() != Running || cmd != nil || !strings.Contains(plain(m.render()), "[Y/n]") {
		t.Fatalf("Esc after a partial Enter: Outcome() = %v, want Running and a [Y/n] question:\n%s", m.Outcome(), plain(m.render()))
	}
	for _, k := range []KeyPressMsg{typed("y"), typed("Y"), key(KeyEnter)} {
		m, _ := press(partlySent(t), key(KeyEsc), k)
		if m.Outcome() != Stopped || len(m.Left()) == 0 {
			t.Errorf("%v: Outcome() = %v, Left() = %v; want Stopped with the selections left", k, m.Outcome(), m.Left())
		}
	}
}

func TestNoKeepsKilOpenWithTheSelections(t *testing.T) {
	m := partlySent(t)
	selected := m.picker.SelectedCount()
	m, cmd := press(m, key(KeyEsc), typed("n"))
	if m.Outcome() != Running || cmd != nil || m.picker.SelectedCount() != selected {
		t.Errorf("n: Outcome() = %v, SelectedCount() = %d; want Running with %d selected", m.Outcome(), m.picker.SelectedCount(), selected)
	}
	if strings.Contains(plain(m.render()), "[Y/n]") {
		t.Errorf("question still shown after n:\n%s", plain(m.render()))
	}
}

func TestCtrlCAfterAPartialEnterAbortsWithoutAsking(t *testing.T) {
	m, _ := press(partlySent(t), ctrl('c'))
	if m.Outcome() != Aborted {
		t.Errorf("Ctrl-C after a partial Enter: Outcome() = %v, want Aborted", m.Outcome())
	}
}

// An Enter typed right after a partial Enter can arrive before the user has
// seen the rows it would signal.
// Enter waits until the list has settled.
func TestEnterRightAfterAPartialEnterWaits(t *testing.T) {
	var r recorder
	m, _ := press(offscreenSelections(&r), key(KeyEnter), key(KeyEnter))
	if len(r.sent) != 1 || m.Outcome() != Running {
		t.Fatalf("second Enter before the list settled: sent %d batches, Outcome() = %v; want 1 and Running", len(r.sent), m.Outcome())
	}
	m, _ = press(settled(m), key(KeyEnter))
	if len(r.sent) != 2 && m.Outcome() != Accepted {
		t.Errorf("Enter after the list settled did nothing: sent %d batches, Outcome() = %v", len(r.sent), m.Outcome())
	}
}

// When every selection is off screen, Enter signals nothing but must bring
// them on screen, or pressing Enter again would never make progress.
func TestEnterBringsSelectionsOnScreenWhenNoneAreShown(t *testing.T) {
	var r recorder
	m := drawn(New(items(numberedLines(30)...), "", "SIGKILL", noReload).WithSender(r.send), 80, 10)
	m, _ = press(m, key(KeyTab))
	for range 30 {
		m, _ = press(m, key(KeyUp))
	}
	m, _ = press(m, key(KeyEnter))
	if !strings.Contains(plain(m.render()), "▌┃100 proc") && !strings.Contains(plain(m.render()), "┃100 proc") {
		t.Fatalf("selected row not brought on screen:\n%s", plain(m.render()))
	}
	m, _ = press(settled(m), key(KeyEnter))
	if m.Outcome() != Accepted || !slices.Equal(targetTexts(m), []string{"100 proc"}) {
		t.Errorf("Enter with the selection on screen: Outcome() = %v, Targets() = %v", m.Outcome(), targetTexts(m))
	}
}

// With more selections than fit, Alt-A must still show the rows it just
// selected, so that the user sees them and Alt-A again deselects them.
func TestAltAShowsTheRowsItJustSelected(t *testing.T) {
	m := drawn(New(items(numberedLines(30)...), "", "SIGKILL", noReload), 80, 13)
	m, _ = press(m, alt('a'))
	m = resized(m, 80, 8)
	for range 10 {
		m, _ = press(m, key(KeyUp))
	}
	m, _ = press(m, key(KeyDown), key(KeyDown), key(KeyDown))
	was := map[string]bool{}
	for _, r := range m.picker.Rows() {
		was[r.Item.Text] = r.Selected
	}
	m, _ = press(m, alt('a'))
	view := plain(m.render())
	var added []string
	for _, r := range m.picker.Rows() {
		if r.Selected && !was[r.Item.Text] {
			added = append(added, r.Item.Text)
			if !strings.Contains(view, "┃"+r.Item.Text) {
				t.Errorf("row %q Alt-A just selected is off screen:\n%s", r.Item.Text, view)
			}
		}
	}
	if len(added) == 0 {
		t.Fatalf("Alt-A selected nothing new:\n%s", view)
	}
	m, _ = press(m, alt('a'))
	for _, r := range m.picker.Rows() {
		if r.Selected && slices.Contains(added, r.Item.Text) {
			t.Errorf("row %q is still selected after Alt-A again", r.Item.Text)
		}
	}
}

// settled delivers the end of the wait that Enter observes after the rows on
// screen changed.
func settled(m Model) Model {
	next, _ := m.Update(settledMsg{gen: m.settleGen})
	return next
}

// A message handled after the outcome is decided must not change it or
// signal anything.
func TestKeysAfterTheOutcomeIsDecidedAreIgnored(t *testing.T) {
	var r recorder
	m, _ := press(offscreenSelections(&r), ctrl('c'), key(KeyEnter))
	if m.Outcome() != Aborted || len(r.sent) != 0 {
		t.Errorf("Ctrl-C then Enter: Outcome() = %v, sent %v; want Aborted and nothing sent", m.Outcome(), r.sent)
	}

	r = recorder{}
	m = drawn(New(items(numberedLines(30)...), "", "SIGKILL", noReload).WithSender(r.send), 80, 24)
	m, _ = press(m, key(KeyTab), key(KeyEnter))
	targets := targetTexts(m)
	m, _ = press(m, key(KeyUp), key(KeyTab), key(KeyEnter))
	if m.Outcome() != Accepted || !slices.Equal(targetTexts(m), targets) || len(r.sent) != 0 {
		t.Errorf("keys after Enter: Outcome() = %v, Targets() = %v, sent %v; want Accepted with %v and nothing sent", m.Outcome(), targetTexts(m), r.sent, targets)
	}
}

// Selected rows that have just come on screen may not have been seen yet,
// as keys typed ahead arrive before the user sees the frames drawn between
// them. Enter waits after any change that
// brings selections on screen, not only after a partial Enter.
func TestEnterRightAfterSelectionsComeOnScreenWaits(t *testing.T) {
	bring := map[string]func(Model) Model{
		"closing the help": func(m Model) Model {
			m, _ = press(m, alt('h'), key(KeyEsc))
			return m
		},
		"scrolling": func(m Model) Model {
			for range 30 {
				m, _ = press(m, key(KeyDown))
			}
			return m
		},
		"growing the terminal": func(m Model) Model { return resized(m, 80, 24) },
		// After fg, Run draws the whole screen again.
		"resuming after Ctrl-Z": func(m Model) Model {
			m, _ = press(m, ResumeMsg{})
			return m
		},
	}
	for name, f := range bring {
		var r recorder
		m := settled(partlySent(t))
		if name == "scrolling" {
			for range 30 {
				m, _ = press(m, key(KeyUp))
			}
		}
		m = settled(m)
		before := len(r.sent)
		m, _ = press(f(m).WithSender(r.send), key(KeyEnter))
		if len(r.sent) != before || m.Outcome() != Running {
			t.Errorf("Enter right after %s: sent %v, Outcome() = %v; want nothing sent and Running", name, r.sent, m.Outcome())
		}
		m, _ = press(settled(m), key(KeyEnter))
		if len(r.sent) == before && m.Outcome() == Running {
			t.Errorf("Enter after %s settled did nothing", name)
		}
	}
}

// Some terminals, such as serial consoles, report a size of 0x0. kil then
// keeps its default size rather than drawing no row, so Enter still works.
func TestZeroTerminalSizeKeepsTheDefaultSize(t *testing.T) {
	m := drawn(New(items("101 node a"), "", "SIGKILL", noReload), 0, 0)
	if !strings.Contains(plain(m.render()), "101 node a") {
		t.Errorf("row not drawn with a 0x0 size:\n%s", plain(m.render()))
	}
	m, _ = press(m, key(KeyEnter))
	if m.Outcome() != Accepted {
		t.Errorf("Enter with a 0x0 size: Outcome() = %v, want Accepted", m.Outcome())
	}
}

// Enter answers the question, so its answers must stay on screen however
// narrow the terminal is.
func TestNarrowTerminalKeepsTheAnswersOfTheQuestion(t *testing.T) {
	m, _ := press(partlySent(t), key(KeyEsc))
	for _, width := range []int{80, 30, 12, 6} {
		m = resized(m, width, 10)
		if prompt := plain(promptLine(m)); !strings.Contains(prompt, "[Y/n]") || ansi.StringWidth(prompt) > width {
			t.Errorf("width %d: question %q has no [Y/n] or does not fit", width, prompt)
		}
	}
}

// Rows that have just come on screen may not be on the terminal yet, so
// selecting them right away with Alt-A must not let Enter signal them either.
func TestEnterRightAfterSelectingRowsJustOnScreenWaits(t *testing.T) {
	var r recorder
	m := drawn(New(items(numberedLines(30)...), "", "SIGKILL", noReload).WithSender(r.send), 80, 5)
	m, _ = press(m, alt('a'), key(KeyUp), key(KeyUp), key(KeyUp))
	m = settled(m)
	m, _ = press(m, key(KeyUp), alt('a'), key(KeyEnter))
	if len(r.sent) != 0 || m.Outcome() != Running {
		t.Fatalf("Enter right after selecting rows just on screen: sent %v, Outcome() = %v; want nothing sent and Running", r.sent, m.Outcome())
	}
	m, _ = press(settled(m), key(KeyEnter))
	if len(r.sent) == 0 && m.Outcome() == Running {
		t.Error("Enter after the rows settled did nothing")
	}
}

// Each dimension is drawn as it is given, so kil keeps only the dimension
// reported as 0, not both.
func TestOneZeroDimensionStillApplies(t *testing.T) {
	m := drawn(New(items(numberedLines(30)...), "", "SIGKILL", noReload), 80, 24)
	m, _ = press(m, alt('a'))
	m, _ = press(resized(m, 0, 1), key(KeyEnter))
	if m.Outcome() != Running {
		t.Errorf("Enter on a 1-line terminal reported as 0x1: Outcome() = %v, want Running", m.Outcome())
	}
	if lines := strings.Count(m.render(), "\n") + 1; lines != 1 {
		t.Errorf("view has %d lines on a 1-line terminal, want 1", lines)
	}
}

// Nothing has been seen before the first frame, so the rows kil starts with
// count as just come on screen.
func TestEnterRightAfterStartWaits(t *testing.T) {
	var r recorder
	m := New(items(numberedLines(30)...), "", "SIGKILL", noReload).WithSender(r.send)
	if m.Init() == nil {
		t.Error("Init() starts no wait for the first frame")
	}
	m, _ = press(m, alt('a'))
	m, _ = press(resized(m, 80, 5), key(KeyEnter))
	if len(r.sent) != 0 || m.Outcome() != Running {
		t.Fatalf("Enter before the first frame: sent %v, Outcome() = %v; want nothing sent and Running", r.sent, m.Outcome())
	}
	m, _ = press(settled(m), key(KeyEnter))
	if len(r.sent) == 0 && m.Outcome() == Running {
		t.Error("Enter after the first frame did nothing")
	}
}

// Each row's wait ends 300 ms after it came on screen, so rows that keep
// coming on screen do not keep Enter waiting for rows shown long before.
func TestEachRowWaitsOnlyForItself(t *testing.T) {
	var r recorder
	next := 200
	reload := func() ([]picker.Item, error) {
		next++
		return items("100 proc", fmt.Sprintf("%d proc", next)), nil
	}
	m := drawn(New(items("100 proc", "200 proc"), "", "SIGKILL", reload).WithSender(r.send), 80, 6)
	m = settled(m)
	m, _ = press(m, key(KeyTab))
	m = resized(resized(m, 80, 1), 80, 6)
	selectedGen := m.settleGen
	m, cmd := press(m, ctrl('r'))
	nm, _ := m.Update(cmd())
	m = nm
	if m.settleGen == selectedGen {
		t.Fatalf("the reloaded row started no wait:\n%s", plain(m.render()))
	}
	nm, _ = m.Update(settledMsg{gen: selectedGen})
	m, _ = press(nm, key(KeyEnter))
	if m.Outcome() != Accepted || !slices.Equal(targetTexts(m), []string{"100 proc"}) {
		t.Errorf("Enter after the selected row's wait ended: Outcome() = %v, Targets() = %v; want Accepted with 100 proc", m.Outcome(), targetTexts(m))
	}
}

// Exact mode changes which rows match, so the rows kil starts with are those
// of exact mode, and they wait for the first frame too.
func TestEnterRightAfterStartInExactModeWaits(t *testing.T) {
	var r recorder
	m := New(items("101 a_b"), "'ab", "SIGKILL", noReload).WithExact(true).WithSender(r.send)
	m, _ = press(m, alt('a'), key(KeyEnter))
	if m.Outcome() != Running {
		t.Fatalf("Enter before the first frame in exact mode: Outcome() = %v, want Running", m.Outcome())
	}
	m, _ = press(settled(m), key(KeyEnter))
	if !slices.Equal(targetTexts(m), []string{"101 a_b"}) {
		t.Errorf("Enter after the first frame: Targets() = %v, want 101 a_b", targetTexts(m))
	}
}

// Ctrl-Z suspends kil as it would a shell job, even while the help or the
// question is shown, as Ctrl-C aborts there too.
func TestCtrlZSuspends(t *testing.T) {
	list := sized(New(items("101 node a"), "", "SIGKILL", noReload))
	help, _ := press(list, alt('h'))
	question, _ := press(partlySent(t), key(KeyEsc))
	for name, m := range map[string]Model{"list": list, "help": help, "question": question} {
		next, cmd := press(m, ctrl('z'))
		// The command is compared rather than run, as another command,
		// such as a wait, would block.
		if cmd == nil || reflect.ValueOf(cmd).Pointer() != reflect.ValueOf(Suspend).Pointer() {
			t.Errorf("Ctrl-Z in the %s: command is not Suspend", name)
		}
		if next.Outcome() != Running || next.help != m.help || next.confirming != m.confirming {
			t.Errorf("Ctrl-Z in the %s changed the picker: Outcome() = %v, help %v, confirming %v", name, next.Outcome(), next.help, next.confirming)
		}
	}
}

// Alt-A keeps every row it toggled on screen, the ones selected before it
// too, so that pressing it again deselects them as AGENTS.md promises,
// rather than select rows that came on screen as kil scrolled.
func TestAltATwiceDeselectsRowsSelectedBefore(t *testing.T) {
	m := drawn(New(items(numberedLines(30)...), "", "SIGKILL", noReload), 80, 12)
	m, _ = press(m, key(KeyTab, ModShift), key(KeyTab, ModShift))
	if n := m.picker.SelectedCount(); n != 2 {
		t.Fatalf("Shift-Tab twice selected %d rows, want 2", n)
	}
	m, _ = press(m, alt('a'))
	if _, offscreen := m.selectedOnScreen(); offscreen {
		t.Errorf("Alt-A left selected rows off screen:\n%s", plain(m.render()))
	}
	m, _ = press(m, alt('a'))
	if n := m.picker.SelectedCount(); n != 0 {
		t.Errorf("Alt-A again left %d rows selected, want 0:\n%s", n, plain(m.render()))
	}
}

// selectedTexts returns the texts of the selected rows.
func selectedTexts(m Model) map[string]bool {
	sel := map[string]bool{}
	for _, r := range m.picker.Rows() {
		if r.Selected {
			sel[r.Item.Text] = true
		}
	}
	return sel
}

// secondAltADeselects presses Alt-A twice and fails unless the second one
// deselects exactly the rows on screen, as AGENTS.md promises.
func secondAltADeselects(t *testing.T, m Model) {
	t.Helper()
	m, _ = press(m, alt('a'))
	want := selectedTexts(m)
	for _, r := range m.picker.Rows() {
		if m.drawnIDs()[r.Item.ID] {
			if !r.Selected {
				t.Fatalf("Alt-A left %q on screen unselected:\n%s", r.Item.Text, plain(m.render()))
			}
			delete(want, r.Item.Text)
		}
	}
	m, _ = press(m, alt('a'))
	if got := selectedTexts(m); !maps.Equal(got, want) {
		t.Errorf("Alt-A again left %v selected, want %v, the selections that were off screen:\n%s", slices.Sorted(maps.Keys(got)), slices.Sorted(maps.Keys(want)), plain(m.render()))
	}
}

// With more selections than fit, the rows Alt-A selected and those selected
// before do not all fit, and Alt-A again still only deselects.
func TestAltATwiceWithMoreSelectionsThanFit(t *testing.T) {
	m := drawn(New(items(numberedLines(30)...), "", "SIGKILL", noReload), 80, 13)
	m, _ = press(m, alt('a'))
	m = resized(m, 80, 8)
	for range 10 {
		m, _ = press(m, key(KeyUp))
	}
	m, _ = press(m, key(KeyTab, ModShift), key(KeyTab, ModShift))
	secondAltADeselects(t, m)
}

// A row selected under another query, which the query now hides, stays
// pinned below the rows selected after it, and Alt-A again still deselects
// only the rows on screen.
func TestAltATwiceWithSelectionsFromAnotherQuery(t *testing.T) {
	m := drawn(New(items(numberedLines(30)...), "", "SIGKILL", noReload), 80, 12)
	// 110 is selected under the query "11", and 102 and 112 under "2",
	// which 110 does not match.
	m, _ = press(m, typed("11"), key(KeyTab), ctrl('u'), typed("2"))
	m, _ = press(m, key(KeyTab, ModShift), key(KeyTab, ModShift))
	if got := slices.Sorted(maps.Keys(selectedTexts(m))); !slices.Equal(got, []string{"102 proc", "110 proc", "112 proc"}) {
		t.Fatalf("selected %v, want 102, 110, and 112:\n%s", got, plain(m.render()))
	}
	secondAltADeselects(t, m)
}

// When Alt-A selects the last unselected rows, the separator goes and the
// list moves by a line, which can bring on screen a row selected earlier
// that the user has not seen. Enter then waits, as for any row that has just
// come on screen.
func TestEnterWaitsForASelectionAltABroughtOnScreen(t *testing.T) {
	var r recorder
	m := drawn(New(items(numberedLines(3)...), "", "SIGKILL", noReload).WithSender(r.send), 80, 5)
	m, _ = press(m, key(KeyTab, ModShift), key(KeyTab, ModShift))
	m = settled(m)
	before := m.drawnIDs()
	m, _ = press(m, alt('a'))
	revealed := false
	for id := range m.drawnIDs() {
		revealed = revealed || !before[id]
	}
	if !revealed {
		t.Fatalf("Alt-A brought no row on screen:\n%s", plain(m.render()))
	}
	m, _ = press(m, key(KeyEnter))
	if len(r.sent) != 0 || m.Outcome() != Running {
		t.Errorf("Enter right after Alt-A: sent %v, Outcome() = %v; want nothing sent and Running", r.sent, m.Outcome())
	}
}

// Tab right after a query change selects the best match: it deselected the
// selection pinned at the bottom, which the cursor was on.
func TestTabAfterQueryChangeSelectsTheBestMatch(t *testing.T) {
	m := sized(New(items("101 node a", "202 python b", "203 python c"), "", "SIGKILL", noReload))
	m, _ = press(m, typed("node"), key(KeyTab), ctrl('u'), typed("python"), key(KeyTab))
	if got := slices.Sorted(maps.Keys(selectedTexts(m))); !slices.Equal(got, []string{"101 node a", "202 python b"}) {
		t.Errorf("selected %v after Tab, want node kept and the best match \"202 python b\" added:\n%s", got, plain(m.render()))
	}
}

// Tab moves the cursor down after toggling, but not from the matches onto
// the selections pinned below them, so that Tab again after Tab on the best
// match deselects that match, as fzf's Tab does at the end of the list,
// rather than deselect a row chosen earlier, which may then leave the
// screen.
func TestTabTwiceAfterQueryChangeKeepsTheEarlierSelection(t *testing.T) {
	m := sized(New(items("101 node a", "202 python b", "203 python c"), "", "SIGKILL", noReload))
	m, _ = press(m, typed("node"), key(KeyTab), ctrl('u'), typed("python"), key(KeyTab), key(KeyTab))
	if got := slices.Sorted(maps.Keys(selectedTexts(m))); !slices.Equal(got, []string{"101 node a"}) {
		t.Errorf("selected %v after Tab twice, want only node, as the second Tab undoes the first:\n%s", got, plain(m.render()))
	}
}

// With more selections than fit beside the matches, a query change puts the
// cursor on the first match above them and the window follows it, so some
// selections leave the screen. Enter then signals only the selections on
// screen and brings the rest back, as after any scroll.
func TestQueryChangeWithManySelectionsMakesEnterPartial(t *testing.T) {
	var r recorder
	m := drawn(New(items(numberedLines(30)...), "", "SIGKILL", noReload).WithSender(r.send), 80, 10)
	m, _ = press(m, alt('a'))
	m, _ = press(settled(m), typed("1"))
	m = settled(m)
	shown, offscreen := m.selectedOnScreen()
	if !offscreen {
		t.Fatalf("every selection is still on screen after the query change:\n%s", plain(m.render()))
	}
	var want []string
	for _, it := range shown {
		want = append(want, it.Text)
	}
	m, _ = press(m, key(KeyEnter))
	if len(r.sent) != 1 || !slices.Equal(r.sent[0], want) || m.Outcome() != Running {
		t.Fatalf("Enter: sent %v, Outcome() = %v; want only %v, the selections on screen, and Running", r.sent, m.Outcome(), want)
	}
	if _, offscreen := m.selectedOnScreen(); offscreen {
		t.Errorf("the selections left are not back on screen:\n%s", plain(m.render()))
	}
}
