// Package ui is kil's interactive process picker.
package ui

import (
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/mattn/go-runewidth"

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
	query     []rune
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

// New returns a picker over items, filtered by query. label names the
// signal Enter sends, and reload lists the processes again for Ctrl-R.
func New(items []picker.Item, query, label string, reload func() ([]picker.Item, error)) Model {
	p := picker.New(items)
	p.SetQuery(query)
	return Model{
		picker: p,
		query:  []rune(query),
		label:  label,
		total:  len(items),
		reload: reload,
		width:  80,
		height: 24,
	}
}

// Query returns the current query.
func (m Model) Query() string { return string(m.query) }

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
	case tea.KeyUp, tea.KeyCtrlP, tea.KeyCtrlK:
		m.picker.Up()
	case tea.KeyDown, tea.KeyCtrlN, tea.KeyCtrlJ:
		m.picker.Down()
	case tea.KeyTab:
		m.picker.Toggle()
	case tea.KeyCtrlA:
		m.picker.SelectAllShown()
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
	case tea.KeyBackspace:
		if len(m.query) > 0 {
			m.setQuery(m.query[:len(m.query)-1])
		}
	case tea.KeyCtrlU:
		m.setQuery(nil)
	case tea.KeySpace:
		m.setQuery(append(m.query, ' '))
	case tea.KeyRunes:
		m.setQuery(append(m.query, k.Runes...))
	}
	return m, nil
}

func (m *Model) setQuery(q []rune) {
	m.query = append([]rune(nil), q...)
	m.picker.SetQuery(string(m.query))
	m.offset = 0
}

// View implements tea.Model.
func (m Model) View() string {
	rows := m.picker.Rows()
	lines := []string{
		"> " + string(m.query),
		fmt.Sprintf("  %d/%d (%d selected)  Enter: send %s  Tab: select  Ctrl-A: select all shown  Ctrl-R: refresh  Esc: abort",
			len(rows)-m.pinnedCount(), m.total, m.picker.SelectedCount(), m.label),
	}
	body, cursorLine := m.layout()
	offset := scrolled(m.offset, cursorLine, m.bodyHeight())
	end := min(offset+m.bodyHeight(), len(body))
	lines = append(lines, body[offset:end]...)

	for i, l := range lines {
		lines[i] = runewidth.Truncate(l, m.width, "")
	}
	return strings.Join(lines, "\n")
}

// headerLines is the number of lines above the rows: the query and the
// status line.
const headerLines = 2

func (m Model) bodyHeight() int {
	return max(m.height-headerLines, 1)
}

func (m Model) pinnedCount() int {
	n := 0
	for _, r := range m.picker.Rows() {
		if r.Pinned {
			n++
		}
	}
	return n
}

// layout renders the rows with a separator between the pinned selections
// and the matches, so it also returns the cursor's line index, which can
// differ from its row index.
func (m Model) layout() ([]string, int) {
	pinned := m.pinnedCount()
	var body []string
	cursorLine := 0
	for i, r := range m.picker.Rows() {
		if i == pinned && pinned > 0 {
			body = append(body, "  "+strings.Repeat("─", 20))
		}
		if i == m.picker.Cursor() {
			cursorLine = len(body)
		}
		body = append(body, rowLine(r, i == m.picker.Cursor()))
	}
	return body, cursorLine
}

// scrolled returns the first line to show so that the cursor line is within
// a window of the given height, moving the window as little as possible.
func scrolled(offset, cursorLine, height int) int {
	offset = min(offset, cursorLine)
	if cursorLine >= offset+height {
		offset = cursorLine - height + 1
	}
	return offset
}

func rowLine(r picker.Row, focused bool) string {
	cursor, mark := " ", " "
	if focused {
		cursor = ">"
	}
	if r.Selected {
		mark = "●"
	}
	return cursor + mark + " " + r.Item.Text
}
