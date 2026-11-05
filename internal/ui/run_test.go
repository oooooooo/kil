package ui

import (
	"errors"
	"os"
	"os/exec"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/gdamore/tcell/v2"

	"github.com/oooooooo/kil/internal/picker"
	"github.com/oooooooo/kil/internal/width"
)

func simScreen(t *testing.T) tcell.SimulationScreen {
	t.Helper()
	s := tcell.NewSimulationScreen("UTF-8")
	if err := s.Init(); err != nil {
		t.Fatal(err)
	}
	s.SetSize(80, 24)
	t.Cleanup(s.Fini)
	return s
}

// runAsync runs the picker on s and returns its result once it ends.
func runAsync(s tcell.Screen, m Model, c Control) <-chan Model {
	done := make(chan Model, 1)
	go func() {
		final, _ := Run(s, m, c)
		done <- final
	}()
	return done
}

func wait(t *testing.T, done <-chan Model) Model {
	t.Helper()
	select {
	case m := <-done:
		return m
	case <-time.After(5 * time.Second):
		t.Fatal("the picker did not end")
		return Model{}
	}
}

// A key typed while kil is stopped reaches the picker after it hears that
// kil resumed, so Enter waits for the selection to be drawn again.
func TestRunResumesBeforeKeysTypedWhileStopped(t *testing.T) {
	s := simScreen(t)
	m := New(items("101 a"), "", "SIGKILL", noReload)
	m, _ = press(m, key(KeyTab))
	var steps []string
	done := runAsync(s, m, Control{
		CanSuspend: func(Model) bool { return true },
		Stop: func() error {
			steps = append(steps, "stop")
			s.InjectKey(tcell.KeyEnter, 0, tcell.ModNone)
			s.InjectKey(tcell.KeyCtrlC, 0, tcell.ModCtrl)
			return nil
		},
	})
	// Let the first frame settle, so that Enter would signal before the
	// suspend.
	time.Sleep(2 * settleTime)
	s.InjectKey(tcell.KeyCtrlZ, 0, tcell.ModCtrl)
	got := wait(t, done)
	if !slices.Equal(steps, []string{"stop"}) {
		t.Errorf("steps = %v, want one stop", steps)
	}
	if got.Outcome() != Aborted {
		t.Errorf("Enter typed while stopped: Outcome() = %v, want Aborted by the Ctrl-C after it", got.Outcome())
	}
}

// Keys typed before, during, and after the suspend reach the query in the
// order they were typed.
func TestRunKeepsKeyOrderAcrossSuspend(t *testing.T) {
	s := simScreen(t)
	m := New(items("101 a"), "", "SIGKILL", noReload)
	ext := make(chan Msg)
	done := runAsync(s, m, Control{
		External:   ext,
		CanSuspend: func(Model) bool { return true },
		Stop: func() error {
			for _, r := range "cd" {
				s.InjectKey(tcell.KeyRune, r, tcell.ModNone)
			}
			return nil
		},
	})
	for _, r := range "ab" {
		s.InjectKey(tcell.KeyRune, r, tcell.ModNone)
	}
	s.InjectKey(tcell.KeyCtrlZ, 0, tcell.ModCtrl)
	for _, r := range "ef" {
		s.InjectKey(tcell.KeyRune, r, tcell.ModNone)
	}
	time.Sleep(100 * time.Millisecond)
	s.InjectKey(tcell.KeyCtrlC, 0, tcell.ModCtrl)
	if got := wait(t, done).Query(); got != "abcdef" && got != "abefcd" {
		t.Errorf("Query() = %q, want the keys in the order they were typed", got)
	}
}

// tcell stops reading the terminal only once its queue has room, so the
// picker takes every key even while kil is suspending, as a long paste
// would bring.
func TestRunTakesEveryKeyWhileSuspending(t *testing.T) {
	s := simScreen(t)
	done := runAsync(s, New(items("101 a"), "", "SIGKILL", noReload), Control{
		CanSuspend: func(Model) bool { return true },
		Stop: func() error {
			for range 300 {
				s.InjectKey(tcell.KeyRune, 'x', tcell.ModNone)
			}
			s.InjectKey(tcell.KeyCtrlC, 0, tcell.ModCtrl)
			return nil
		},
	})
	s.InjectKey(tcell.KeyCtrlZ, 0, tcell.ModCtrl)
	if got := len(wait(t, done).Query()); got != 300 {
		t.Errorf("query has %d keys, want 300", got)
	}
}

// A signal caught while the terminal is given back keeps kil from stopping.
func TestRunDoesNotStopAfterASignalDuringTheRelease(t *testing.T) {
	s := simScreen(t)
	asked := 0
	stopped := false
	ext := make(chan Msg)
	done := runAsync(s, New(items("101 a"), "", "SIGKILL", noReload), Control{
		External: ext,
		CanSuspend: func(Model) bool {
			asked++
			return asked == 1
		},
		Stop: func() error {
			stopped = true
			return nil
		},
	})
	ext <- SuspendMsg{}
	ext <- QuitMsg{}
	wait(t, done)
	if stopped || asked != 2 {
		t.Errorf("stopped %v after %d questions, want no stop after asking twice", stopped, asked)
	}
}

type failingResume struct{ tcell.Screen }

func (failingResume) Resume() error { return errors.New("no terminal") }

// If the terminal cannot be taken again, the picker ends with the error
// rather than run without input or drawing.
func TestRunEndsWhenResumeFails(t *testing.T) {
	s := simScreen(t)
	ext := make(chan Msg)
	errc := make(chan error, 1)
	go func() {
		_, err := Run(failingResume{s}, New(items("101 a"), "", "SIGKILL", noReload), Control{
			External:   ext,
			CanSuspend: func(Model) bool { return true },
			Stop:       func() error { return nil },
		})
		errc <- err
	}()
	ext <- SuspendMsg{}
	select {
	case err := <-errc:
		if err == nil {
			t.Error("Run ended without an error")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Run kept running after the terminal could not be taken again")
	}
}

// tcell makes ambiguous-width characters wide as it starts when
// RUNEWIDTH_EASTASIAN is "yes", which kil does not take to mean that, so
// they are narrow until kil says otherwise. This test runs itself with
// that setting in a child, which starts afresh.
func TestAmbiguousWidthIsNarrowDespiteTcell(t *testing.T) {
	if os.Getenv("KIL_TEST_WIDTH_CHILD") != "" {
		if width.Ambiguous() != 1 {
			t.Fatalf("Ambiguous() = %d with RUNEWIDTH_EASTASIAN=yes, want 1", width.Ambiguous())
		}
		return
	}
	child := exec.Command(os.Args[0], "-test.run=^TestAmbiguousWidthIsNarrowDespiteTcell$")
	child.Env = append(os.Environ(), "KIL_TEST_WIDTH_CHILD=1", "RUNEWIDTH_EASTASIAN=yes")
	if out, err := child.CombinedOutput(); err != nil {
		t.Errorf("child failed: %v\n%s", err, out)
	}
}

// The caret is drawn in reverse video, as kil hides the terminal's cursor.
func TestCaretIsDrawn(t *testing.T) {
	s := simScreen(t)
	m := New(items("101 a"), "ab", "SIGKILL", noReload)
	m, _ = press(m, WindowSizeMsg{Width: 80, Height: 24}, key(KeyLeft))
	draw(s, m)
	got, st, _ := s.Get(3, 23)
	if _, _, attr := st.Decompose(); got != "b" || attr&tcell.AttrReverse == 0 {
		t.Errorf("caret cell = %q with attributes %v, want \"b\" in reverse video", got, attr)
	}
}

// Ctrl held with a key such as Left is kept, so that Ctrl-Left, which kil
// does not bind, does not act as Left.
func TestCtrlIsKeptOnSpecialKeys(t *testing.T) {
	for _, tt := range []struct {
		ev   *tcell.EventKey
		want string
	}{
		{tcell.NewEventKey(tcell.KeyLeft, 0, tcell.ModCtrl), "ctrl+left"},
		{tcell.NewEventKey(tcell.KeyUp, 0, tcell.ModCtrl), "ctrl+up"},
		{tcell.NewEventKey(tcell.KeyDelete, 0, tcell.ModCtrl), "ctrl+delete"},
		{tcell.NewEventKey(tcell.KeyLeft, 0, tcell.ModNone), "left"},
		{tcell.NewEventKey(tcell.KeyCtrlSpace+1, 0, tcell.ModCtrl), "ctrl+a"},
		{tcell.NewEventKey(tcell.KeyCtrlSpace+10, 0, tcell.ModCtrl), "ctrl+j"},
		{tcell.NewEventKey(tcell.KeyEnter, 0, tcell.ModNone), "enter"},
	} {
		k, ok := keyMsg(tt.ev)
		if !ok || k.String() != tt.want {
			t.Errorf("%s = %q (ok %v), want %q", tt.ev.Name(), k.String(), ok, tt.want)
		}
	}
}

// A control character in a paste becomes a space in the query, as it did
// before kil used tcell, so that it still separates the words around it.
func TestPastedControlCharacterSeparatesWords(t *testing.T) {
	s := simScreen(t)
	done := make(chan struct{})
	defer close(done)
	events := pump(s, done)
	for _, ev := range []tcell.Event{
		tcell.NewEventPaste(true),
		tcell.NewEventKey(tcell.KeyRune, 'a', tcell.ModNone),
		tcell.NewEventKey(tcell.KeyCtrlSpace+7, 0, tcell.ModCtrl), // BEL
		tcell.NewEventKey(tcell.KeyRune, 'b', tcell.ModNone),
		tcell.NewEventKey(tcell.KeyEsc, 0, tcell.ModNone),
		tcell.NewEventKey(tcell.KeyRune, 'c', tcell.ModNone),
		tcell.NewEventPaste(false),
	} {
		if err := s.PostEvent(ev); err != nil {
			t.Fatal(err)
		}
	}
	m := New(items("101 a"), "", "SIGKILL", noReload)
	select {
	case msg := <-events:
		m, _ = m.Update(msg)
	case <-time.After(5 * time.Second):
		t.Fatal("no paste")
	}
	if m.Query() != "a b c" {
		t.Errorf("Query() after the paste = %q, want \"a b c\"", m.Query())
	}
}

type colorless struct{ tcell.Screen }

func (colorless) Colors() int { return 0 }

// Without colors, as with NO_COLOR, kil draws no color at all rather than
// let tcell stand reverse video in for some of them.
func TestNoColorsDrawsNoColor(t *testing.T) {
	s := simScreen(t)
	m := New(items("101 1 alice node a"), "", "SIGKILL", noReload)
	m, _ = press(m, WindowSizeMsg{Width: 80, Height: 24})
	draw(colorless{s}, m)
	cells, w, _ := s.GetContents()
	for i, c := range cells {
		fg, bg, attr := c.Style.Decompose()
		x, y := i%w, i/w
		caret := y == 23 && x == 2
		if fg != tcell.ColorDefault || bg != tcell.ColorDefault || (attr&tcell.AttrReverse != 0) != caret {
			t.Fatalf("cell (%d, %d) %q has fg %v, bg %v, attributes %v; want no color, reverse only on the caret", x, y, c.Runes, fg, bg, attr)
		}
	}
}

// A control character in a row is drawn as "?", never as part of an escape
// sequence, whatever gave the row: kil's drawing reads its own colors from
// escape sequences in the rendered text.
func TestControlCharactersInARowAreNotInterpreted(t *testing.T) {
	s := simScreen(t)
	m := New(items("101 a \x1b[7mx\x1b[0m"), "", "SIGKILL", noReload)
	m, _ = press(m, WindowSizeMsg{Width: 80, Height: 24})
	draw(s, m)
	var row strings.Builder
	for x := range 20 {
		str, _, _ := s.Get(x, 20)
		row.WriteString(str)
	}
	if got := row.String(); !strings.Contains(got, "101 a ?[7mx?[0m") {
		t.Errorf("row drawn as %q, want the escapes shown as \"?\"", got)
	}
	_, st, _ := s.Get(13, 20)
	if _, _, attr := st.Decompose(); attr&tcell.AttrReverse != 0 {
		t.Error("the row's escape sequence turned on reverse video")
	}
}

// A bidirectional control in a row, which could reorder it on screen, is
// drawn as "?".
func TestBidiControlsInARowAreDrawnAsQuestionMarks(t *testing.T) {
	s := simScreen(t)
	m := New(items("101 a \u202Etxt.exe"), "", "SIGKILL", noReload)
	m, _ = press(m, WindowSizeMsg{Width: 80, Height: 24})
	draw(s, m)
	var row strings.Builder
	for x := range 20 {
		str, _, _ := s.Get(x, 20)
		row.WriteString(str)
	}
	if got := row.String(); !strings.Contains(got, "101 a ?txt.exe") {
		t.Errorf("row drawn as %q, want the override shown as \"?\"", got)
	}
}

// With ambiguous-width characters drawn wide, each line still fits the
// terminal, and the drawing puts each character where the layout counted
// it.
func TestAmbiguousWideRowsFitAndAreDrawnWide(t *testing.T) {
	width.SetAmbiguousWide(true)
	t.Cleanup(func() { width.SetAmbiguousWide(false) })
	m := sized(New(items(numberedLines(40)...), "x·y", "SIGKILL", noReload))
	m, _ = press(m, ctrl('u'))
	for _, keys := range [][]Msg{nil, {key(KeyTab, ModShift)}, {alt('a')}} {
		m, _ = press(m, keys...)
		for i, line := range viewLines(m) {
			if w := width.String(line); w > 80 {
				t.Errorf("line %d is %d columns wide, over 80: %q", i, w, line)
			}
		}
	}
	m, _ = press(m, typed("x"), typed("·"), typed("y"))
	s := simScreen(t)
	m, _ = press(m, WindowSizeMsg{Width: 80, Height: 24})
	draw(s, m)
	if got, _, _ := s.Get(5, 23); got != "y" {
		t.Errorf("prompt \"> x·y\" has %q in column 5, want \"y\" after a two-column ·", got)
	}
}

// wideMode makes ambiguous-width characters two columns wide for a test.
func wideMode(t *testing.T) {
	t.Helper()
	width.SetAmbiguousWide(true)
	t.Cleanup(func() { width.SetAmbiguousWide(false) })
}

// No line, however narrow the terminal, runs past its edge, in either
// width mode, including ellipses, the prompt and its caret, and the info
// line after a partial Enter.
func TestEveryLineFitsANarrowTerminal(t *testing.T) {
	for _, wide := range []bool{false, true} {
		width.SetAmbiguousWide(wide)
		t.Cleanup(func() { width.SetAmbiguousWide(false) })
		long := "  101     1 alice    node            " + strings.Repeat("x", 100) + " needle"
		for w := 1; w <= 50; w++ {
			var r recorder
			m := offscreenSelections(&r)
			m, _ = press(settled(m), key(KeyEnter), key(KeyEnter))
			m = resized(m, w, 10)
			views := []Model{m}
			n := sized(New(items(long, "  102     1 bob      sh              sh"), "x\u00b7yneedle", "SIGKILL", noReload))
			n = resized(n, w, 6)
			n, _ = press(n, ctrl('a'), key(KeyRight))
			views = append(views, n)
			h, _ := press(n, alt('h'))
			views = append(views, h)
			for _, v := range views {
				for i, line := range viewLines(v) {
					if got := width.String(line); got > w {
						t.Errorf("wide %v, width %d: line %d is %d columns: %q", wide, w, i, got, line)
					}
				}
			}
		}
	}
}

// A caret too wide for the prompt is drawn as a one-column space rather
// than not at all.
func TestTooWideACaretIsStillDrawn(t *testing.T) {
	wideMode(t)
	m := New(items("101 a"), "x\u00b7y", "SIGKILL", noReload)
	m, _ = press(m, WindowSizeMsg{Width: 3, Height: 5}, ctrl('a'), key(KeyRight))
	s := simScreen(t)
	s.SetSize(3, 5)
	draw(s, m)
	if _, st, _ := s.Get(2, 4); func() bool { _, _, attr := st.Decompose(); return attr&tcell.AttrReverse == 0 }() {
		t.Errorf("no caret drawn in the prompt %q on 3 columns", plain(promptLine(m)))
	}
}

// The marker of a row not selected is as wide as a selected row's, so
// that their text starts in the same column.
func TestSelectedAndUnselectedRowsLineUp(t *testing.T) {
	wideMode(t)
	m := sized(New(items("101 a", "202 b"), "", "SIGKILL", noReload))
	m, _ = press(m, key(KeyTab))
	lines := viewLines(m)
	col := func(text string) int {
		for _, line := range lines {
			if i := strings.Index(line, text); i >= 0 {
				return width.String(line[:i])
			}
		}
		t.Fatalf("%q is not on screen:\n%s", text, strings.Join(lines, "\n"))
		return 0
	}
	if a, b := col("101 a"), col("202 b"); a != b {
		t.Errorf("text starts at columns %d and %d, want the same:\n%s", a, b, strings.Join(lines, "\n"))
	}
}

// A long line is shifted by grapheme clusters, as uniseg measures them:
// a flag takes two columns, though each of its two runes alone measures
// two as well, so shifting by runes stopped short and the match was cut.
func TestLongLineOfFlagsStillShowsTheMatch(t *testing.T) {
	text := "1 0 u c " + strings.Repeat("\U0001F1EF\U0001F1F5", 80) + " needle"
	for _, w := range []int{20, 40, 80} {
		it := picker.Item{ID: "a", Text: text, Frozen: len([]rune("1 0 u c "))}
		m := New([]picker.Item{it}, "'needle", "SIGKILL", noReload)
		m, _ = press(m, WindowSizeMsg{Width: w, Height: 6})
		lines := viewLines(m)
		if row := lines[len(lines)-4]; !strings.Contains(row, "needle") {
			t.Errorf("width %d: the match is not shown in the row %q", w, row)
		}
	}
}

// When the context after a match does not fit beside it, the match stays
// on screen and the context goes, rather than the match being trimmed off
// on the left.
func TestMatchStaysWhenItsContextDoesNotFit(t *testing.T) {
	wideMode(t)
	text := "1 0 u c " + strings.Repeat("x", 80) + "\U0001F1EF\U0001F1F5" + strings.Repeat("y", 80)
	it := picker.Item{ID: "a", Text: text, Frozen: 8}
	m := New([]picker.Item{it}, "'\U0001F1EF\U0001F1F5", "SIGKILL", noReload)
	m, _ = press(m, WindowSizeMsg{Width: 24, Height: 6})
	if row := viewLines(m)[2]; !strings.Contains(row, "\U0001F1EF\U0001F1F5") {
		t.Errorf("row %q does not show the matched flag", row)
	}
}

// A match covering part of a grapheme cluster colors the whole cluster, so
// that no color change splits it and tcell draws it whole.
func TestPartlyMatchedClusterIsDrawnWhole(t *testing.T) {
	text := "1 0 u c \U0001F1EF\U0001F1F5 END"
	it := picker.Item{ID: "a", Text: text, Frozen: 8}
	m := New([]picker.Item{it}, "'\U0001F1EF", "SIGKILL", noReload)
	m, _ = press(m, WindowSizeMsg{Width: 17, Height: 6})
	s := simScreen(t)
	s.SetSize(17, 6)
	draw(s, m)
	var row strings.Builder
	for x := range 17 {
		str, _, _ := s.Get(x, 2)
		row.WriteString(str)
	}
	if got := row.String(); !strings.Contains(got, "\U0001F1EF\U0001F1F5") || !strings.Contains(got, "END") {
		t.Errorf("row drawn as %q, want the flag whole and END", got)
	}
}
