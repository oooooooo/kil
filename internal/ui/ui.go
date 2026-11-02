// Package ui is kil's interactive process picker.
package ui

import (
	tea "github.com/charmbracelet/bubbletea"

	"github.com/oooooooo/kil/internal/picker"
)

// An Outcome is how the picker ended.
type Outcome int

const (
	// Running means the picker has not ended yet.
	Running Outcome = iota
	// Accepted means Enter was pressed with something to signal.
	Accepted
	// NothingSelected means Enter was pressed with no row shown.
	NothingSelected
	// Aborted means Esc or Ctrl-C was pressed.
	Aborted
	// ListFailed means a Ctrl-R reload could not list processes.
	ListFailed
)

// A Model is the picker's Bubble Tea model.
type Model struct {
	picker    *picker.Picker
	input     lineInput
	label     string
	total     int
	reload    func() ([]picker.Item, error)
	reloading bool
	width     int
	height    int
	offset    int
	outcome   Outcome
	targets   []picker.Item
	err       error
}

type reloadMsg struct {
	items []picker.Item
	err   error
}

// New returns a picker over items, filtered by initial. label names the
// signal Enter sends, and reload lists the processes again for Ctrl-R.
func New(items []picker.Item, initial, label string, reload func() ([]picker.Item, error)) Model {
	p := picker.New(items)
	p.SetQuery(initial)
	return Model{
		picker: p,
		input:  newLineInput(initial),
		label:  label,
		total:  len(items),
		reload: reload,
		width:  80,
		height: 24,
	}
}

// WithExact returns m with plain terms matching exactly, as fzf --exact.
func (m Model) WithExact(exact bool) Model {
	m.picker.SetExact(exact)
	return m
}

// Query returns the current query.
func (m Model) Query() string { return m.input.String() }

// Outcome returns how the picker ended.
func (m Model) Outcome() Outcome { return m.outcome }

// Targets returns the items to signal once the outcome is Accepted.
func (m Model) Targets() []picker.Item { return m.targets }

// Err returns the reload error once the outcome is ListFailed.
func (m Model) Err() error { return m.err }

// Init implements tea.Model.
func (m Model) Init() tea.Cmd { return nil }

// Update implements tea.Model.
func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	next, cmd := m.update(msg)
	nm := next.(Model)
	_, cursorLine := nm.layout()
	nm.offset = scrolled(nm.offset, cursorLine, nm.bodyHeight())
	return nm, cmd
}

func (m Model) update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
	case reloadMsg:
		m.reloading = false
		if msg.err != nil {
			// A failed ps may have produced a partial list, so nothing is
			// signalled from it.
			m.outcome, m.err = ListFailed, msg.err
			return m, tea.Quit
		}
		m.picker.Reload(msg.items)
		m.total = len(msg.items)
	case tea.KeyMsg:
		return m.handleKey(msg)
	}
	return m, nil
}

func (m Model) handleKey(k tea.KeyMsg) (tea.Model, tea.Cmd) {
	if k.Alt && k.Type == tea.KeyRunes && len(k.Runes) == 1 {
		switch k.Runes[0] {
		case 'a':
			m.picker.ToggleAllShown()
		case 'b':
			m.input.wordLeft()
		case 'f':
			m.input.wordRight()
		}
		return m, nil
	}

	before := m.input.String()
	switch k.Type {
	case tea.KeyCtrlC, tea.KeyEsc:
		m.outcome = Aborted
		return m, tea.Quit
	case tea.KeyEnter:
		if m.reloading {
			return m, nil
		}
		m.targets = m.picker.Targets()
		m.outcome = Accepted
		if len(m.targets) == 0 {
			m.outcome = NothingSelected
		}
		return m, tea.Quit
	// The list grows upward from the prompt, as in fzf's default layout, so
	// moving up on screen goes to the next row and down to the previous.
	case tea.KeyUp, tea.KeyCtrlP:
		m.picker.Next()
	case tea.KeyDown, tea.KeyCtrlN, tea.KeyCtrlJ:
		m.picker.Prev()
	case tea.KeyTab:
		m.picker.Toggle(-1) // fzf's toggle+down
	case tea.KeyShiftTab:
		m.picker.Toggle(1) // fzf's toggle+up
	case tea.KeyCtrlX:
		m.picker.SetExact(!m.picker.Exact())
		m.offset = 0
	case tea.KeyCtrlR:
		if m.reloading {
			return m, nil
		}
		m.reloading = true
		reload := m.reload
		return m, func() tea.Msg {
			items, err := reload()
			return reloadMsg{items, err}
		}

	// The query is edited with readline's keys.
	case tea.KeyCtrlA, tea.KeyHome:
		m.input.home()
	case tea.KeyCtrlE, tea.KeyEnd:
		m.input.end()
	case tea.KeyLeft, tea.KeyCtrlB:
		m.input.left()
	case tea.KeyRight, tea.KeyCtrlF:
		m.input.right()
	case tea.KeyBackspace, tea.KeyCtrlH:
		m.input.backspace()
	case tea.KeyDelete:
		m.input.deleteChar()
	case tea.KeyCtrlK:
		m.input.killToEnd()
	case tea.KeyCtrlU:
		m.input.killToStart()
	case tea.KeyCtrlW:
		m.input.killWordBack()
	case tea.KeySpace:
		m.input.insert([]rune{' '})
	case tea.KeyRunes:
		m.input.insert(k.Runes)
	}
	// Moving the caret keeps the list as it is; only a changed query filters
	// again and moves the cursor back to the best match.
	if m.input.String() != before {
		m.picker.SetQuery(m.input.String())
		m.offset = 0
	}
	return m, nil
}
