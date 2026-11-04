package ui

import (
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/gdamore/tcell/v2"
	"github.com/rivo/uniseg"
)

// Control is how kil drives the event loop from outside it.
type Control struct {
	// External carries QuitMsg and SuspendMsg from kil's signal handling.
	External <-chan Msg
	// CanSuspend reports whether kil may stop now. It is asked again after
	// the terminal is given back, as a signal may have come meanwhile.
	CanSuspend func(Model) bool
	// Stop stops kil until it is continued.
	Stop func() error
}

// Run runs the picker on s until its outcome is decided or External asks it
// to quit. Each frame is on the terminal before the next event is handled.
func Run(s tcell.Screen, m Model, c Control) (Model, error) {
	done := make(chan struct{})
	defer close(done)
	events := pump(s, done)
	results := make(chan Msg)
	run := func(cmd Cmd) {
		if cmd == nil {
			return
		}
		go func() {
			msg := cmd()
			select {
			case results <- msg:
			case <-done:
			}
		}()
	}

	s.EnablePaste()
	w, h := s.Size()
	m, cmd := m.Update(WindowSizeMsg{w, h})
	run(cmd)
	run(m.Init())
	draw(s, m)

	for m.Outcome() == Running {
		var msg Msg
		select {
		case msg = <-events:
		case msg = <-results:
		case msg = <-c.External:
		}
		switch v := msg.(type) {
		case QuitMsg:
			return m, nil
		case errorMsg:
			return m, v.err
		case batchMsg:
			for _, c := range v {
				run(c)
			}
			continue
		case SuspendMsg:
			resumed, err := suspend(s, m, c)
			if err != nil {
				return m, err
			}
			if !resumed {
				continue
			}
			w, h := s.Size()
			m, cmd = m.Update(WindowSizeMsg{w, h})
			run(cmd)
			msg = ResumeMsg{}
		}
		m, cmd = m.Update(msg)
		run(cmd)
		draw(s, m)
	}
	return m, nil
}

// suspend gives the terminal back, stops kil, and takes the terminal again.
// tcell stops reading input before it gives the terminal back, and keeps
// what it read, so keys typed around the suspend arrive in order after it.
func suspend(s tcell.Screen, m Model, c Control) (bool, error) {
	if c.CanSuspend == nil || !c.CanSuspend(m) {
		return false, nil
	}
	if err := s.Suspend(); err != nil {
		return false, err
	}
	if c.CanSuspend(m) {
		_ = c.Stop()
	}
	if err := s.Resume(); err != nil {
		return false, err
	}
	s.Sync()
	return true, nil
}

type errorMsg struct{ err error }

// pump turns tcell's events into Msgs. It keeps every event until the loop
// takes it, so that tcell never waits on a full queue, which would keep
// Suspend waiting for tcell's reader to stop.
func pump(s tcell.Screen, done <-chan struct{}) <-chan Msg {
	in := make(chan Msg)
	out := make(chan Msg)
	go func() {
		var paste *strings.Builder
		for {
			ev := s.PollEvent()
			if ev == nil {
				return
			}
			var msg Msg
			switch ev := ev.(type) {
			case *tcell.EventPaste:
				if ev.Start() {
					paste = &strings.Builder{}
					continue
				}
				if paste != nil {
					msg = PasteMsg{paste.String()}
					paste = nil
				}
			case *tcell.EventKey:
				if paste != nil {
					paste.WriteString(pasted(ev))
					continue
				}
				k, ok := keyMsg(ev)
				if !ok {
					continue
				}
				msg = k
			case *tcell.EventResize:
				w, h := ev.Size()
				msg = WindowSizeMsg{w, h}
			case *tcell.EventError:
				msg = errorMsg{ev}
			}
			if msg == nil {
				continue
			}
			select {
			case in <- msg:
			case <-done:
				return
			}
		}
	}()
	go func() {
		var queue []Msg
		for {
			var send chan Msg
			var head Msg
			if len(queue) > 0 {
				send, head = out, queue[0]
			}
			select {
			case msg := <-in:
				queue = append(queue, msg)
			case send <- head:
				queue = queue[1:]
			case <-done:
				return
			}
		}
	}()
	return out
}

// pasted returns the text a key in a paste stands for. tcell turns a
// control character in a paste into a key, which becomes a space, as the
// query turns pasted control characters into spaces.
func pasted(ev *tcell.EventKey) string {
	switch ev.Key() {
	case tcell.KeyRune:
		return string(ev.Rune())
	case tcell.KeyEnter, tcell.KeyLF, tcell.KeyCtrlJ:
		return "\n"
	case tcell.KeyTab:
		return "\t"
	}
	return " "
}

var tcellKeys = map[tcell.Key]rune{
	tcell.KeyEnter:     KeyEnter,
	tcell.KeyTab:       KeyTab,
	tcell.KeyEsc:       KeyEscape,
	tcell.KeyBackspace: KeyBackspace,
	tcell.KeyUp:        KeyUp,
	tcell.KeyDown:      KeyDown,
	tcell.KeyLeft:      KeyLeft,
	tcell.KeyRight:     KeyRight,
	tcell.KeyHome:      KeyHome,
	tcell.KeyEnd:       KeyEnd,
	tcell.KeyDelete:    KeyDelete,
}

// keyMsg converts a tcell key into kil's.
func keyMsg(ev *tcell.EventKey) (KeyPressMsg, bool) {
	// Ctrl is kept on keys such as Left, so that Ctrl-Left, which kil does
	// not bind, does not act as Left.
	var mod KeyMod
	if ev.Modifiers()&tcell.ModCtrl != 0 {
		mod |= ModCtrl
	}
	if ev.Modifiers()&tcell.ModAlt != 0 {
		mod |= ModAlt
	}
	if ev.Modifiers()&tcell.ModShift != 0 {
		mod |= ModShift
	}
	k := ev.Key()
	if k == tcell.KeyBacktab {
		return KeyPressMsg{Code: KeyTab, Mod: mod | ModShift}, true
	}
	if code, ok := tcellKeys[k]; ok {
		return KeyPressMsg{Code: code, Mod: mod}, true
	}
	if k >= tcell.KeyCtrlA && k <= tcell.KeyCtrlZ {
		return KeyPressMsg{Code: rune(k-tcell.KeyCtrlA) + 'a', Mod: mod | ModCtrl}, true
	}
	if k != tcell.KeyRune {
		return KeyPressMsg{}, false
	}
	r := ev.Rune()
	if mod&ModAlt != 0 {
		return KeyPressMsg{Code: r, Mod: mod}, true
	}
	return KeyPressMsg{Code: r, Text: string(r)}, true
}

// draw puts the rendered screen on s and shows it. render's lines carry SGR
// sequences for colors and bold only, which are turned into tcell styles.
func draw(s tcell.Screen, m Model) {
	// render lays out ambiguous-width characters such as ·, ▌, ─, and │ one
	// column wide, as widths does, and tcell must draw them so. tcell makes
	// them two wide when RUNEWIDTH_EASTASIAN is set, which would push the
	// rest of each row out of place.
	uniseg.EastAsianAmbiguousWidth = 1
	s.Clear()
	// Without colors, as with NO_COLOR, tcell stands reverse video in for
	// some colors, which marks cells for no reason, so none are set.
	colored := s.Colors() > 0
	for y, line := range strings.Split(m.render(), "\n") {
		x := 0
		st := tcell.StyleDefault
		for line != "" {
			if strings.HasPrefix(line, "\x1b[") {
				end := strings.IndexByte(line, 'm')
				if end < 0 {
					break
				}
				st = applySGR(st, line[2:end], colored)
				line = line[end+1:]
				continue
			}
			next := strings.Index(line, "\x1b[")
			if next < 0 {
				next = len(line)
			}
			text := line[:next]
			line = line[next:]
			state := -1
			for text != "" {
				var g string
				var w int
				g, text, w, state = uniseg.FirstGraphemeClusterInString(text, state)
				if w == 0 && utf8.RuneCountInString(g) > 0 {
					w = 1
				}
				s.Put(x, y, g, st)
				x += w
			}
		}
	}
	s.HideCursor()
	s.Show()
}

func applySGR(st tcell.Style, params string, colored bool) tcell.Style {
	ps := strings.Split(params, ";")
	for i := 0; i < len(ps); i++ {
		switch ps[i] {
		case "0", "":
			st = tcell.StyleDefault
		case "1":
			st = st.Bold(true)
		case "7":
			// The caret: kil hides the terminal's cursor and draws its own.
			st = st.Reverse(true)
		case "38", "48":
			if i+2 < len(ps) && ps[i+1] == "5" {
				n, _ := strconv.Atoi(ps[i+2])
				switch {
				case !colored:
				case ps[i] == "38":
					st = st.Foreground(tcell.PaletteColor(n))
				default:
					st = st.Background(tcell.PaletteColor(n))
				}
				i += 2
			}
		}
	}
	return st
}
