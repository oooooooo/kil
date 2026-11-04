package ui

import (
	"errors"
	"slices"
	"testing"
	"time"

	"github.com/gdamore/tcell/v2"
	"github.com/rivo/uniseg"
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

// kil lays rows out with ambiguous-width characters, such as ·, one column
// wide, so they are drawn one column wide even where tcell would draw them
// two wide, as with RUNEWIDTH_EASTASIAN=1.
func TestAmbiguousWidthIsDrawnAsLaidOut(t *testing.T) {
	defer func(w int) { uniseg.EastAsianAmbiguousWidth = w }(uniseg.EastAsianAmbiguousWidth)
	uniseg.EastAsianAmbiguousWidth = 2
	s := simScreen(t)
	m := New(items("101 a"), "x·y", "SIGKILL", noReload)
	m, _ = press(m, WindowSizeMsg{Width: 80, Height: 24})
	draw(s, m)
	if got, _, _ := s.Get(4, 23); got != "y" {
		t.Errorf("prompt \"> x·y\" has %q in column 4, want \"y\"", got)
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
