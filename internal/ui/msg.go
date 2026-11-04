package ui

import (
	"time"
	"unicode"
)

// A Msg is an event the picker handles: a key, a paste, a resize, or the
// result of a Cmd.
type Msg = any

// A Cmd runs outside the event loop and returns a Msg for it.
type Cmd func() Msg

// KeyMod is the set of modifiers held with a key.
type KeyMod int

// The modifiers kil tells apart.
const (
	ModShift KeyMod = 1 << iota
	ModAlt
	ModCtrl
)

// Key codes for keys without text. Printable keys use their rune.
const (
	KeyTab       rune = '\t'
	KeyEnter     rune = '\r'
	KeyEscape    rune = 0x1b
	KeyEsc            = KeyEscape
	KeyBackspace rune = 0x7f
	KeySpace     rune = ' '

	KeyUp rune = unicode.MaxRune + 1 + iota
	KeyDown
	KeyLeft
	KeyRight
	KeyHome
	KeyEnd
	KeyDelete
)

var keyNames = map[rune]string{
	KeyTab:       "tab",
	KeyEnter:     "enter",
	KeyEscape:    "esc",
	KeyBackspace: "backspace",
	KeySpace:     "space",
	KeyUp:        "up",
	KeyDown:      "down",
	KeyLeft:      "left",
	KeyRight:     "right",
	KeyHome:      "home",
	KeyEnd:       "end",
	KeyDelete:    "delete",
}

// A KeyPressMsg is a key pressed. Text is what the key types, empty for a
// key that types nothing or is held with Ctrl or Alt.
type KeyPressMsg struct {
	Code rune
	Text string
	Mod  KeyMod
}

// String names k as "ctrl+a", "alt+h", "shift+tab", "enter", or the text it
// types.
func (k KeyPressMsg) String() string {
	if k.Text != "" && k.Mod&(ModCtrl|ModAlt) == 0 {
		if k.Text == " " {
			return "space"
		}
		return k.Text
	}
	var s string
	if k.Mod&ModCtrl != 0 {
		s += "ctrl+"
	}
	if k.Mod&ModAlt != 0 {
		s += "alt+"
	}
	if k.Mod&ModShift != 0 {
		s += "shift+"
	}
	if name, ok := keyNames[k.Code]; ok {
		return s + name
	}
	return s + string(k.Code)
}

// A PasteMsg is text pasted into the terminal.
type PasteMsg struct{ Content string }

// A WindowSizeMsg is the terminal's size.
type WindowSizeMsg struct{ Width, Height int }

// A ResumeMsg tells the picker that kil was continued after a suspend and the
// whole screen was drawn again.
type ResumeMsg struct{}

// A SuspendMsg asks the event loop to suspend kil.
type SuspendMsg struct{}

// A QuitMsg asks the event loop to end.
type QuitMsg struct{}

// batchMsg runs its commands.
type batchMsg []Cmd

// Quit ends the event loop.
func Quit() Msg { return QuitMsg{} }

// Suspend suspends kil as a shell job.
func Suspend() Msg { return SuspendMsg{} }

// Batch runs cmds concurrently.
func Batch(cmds ...Cmd) Cmd {
	var valid []Cmd
	for _, c := range cmds {
		if c != nil {
			valid = append(valid, c)
		}
	}
	switch len(valid) {
	case 0:
		return nil
	case 1:
		return valid[0]
	}
	return func() Msg { return batchMsg(valid) }
}

// Tick returns the Msg f makes after d.
func Tick(d time.Duration, f func(time.Time) Msg) Cmd {
	return func() Msg {
		return f(<-time.After(d))
	}
}
