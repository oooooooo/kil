// Package ui is kil's interactive process picker.
package ui

import (
	"maps"
	"time"

	tea "charm.land/bubbletea/v2"

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
	// Stopped means the user confirmed leaving after Enter had signalled
	// some selected rows; Left returns the selections not signalled.
	Stopped
)

// A Model is the picker's Bubble Tea model.
type Model struct {
	picker    *picker.Picker
	input     lineInput
	label     string
	reload    func() ([]picker.Item, error)
	reloading bool
	// help is set while Alt-H's key list is drawn in place of the rows.
	help    bool
	width   int
	height  int
	offset  int
	outcome Outcome
	targets []picker.Item
	err     error
	send    func([]picker.Item)
	// sent counts the rows a partial Enter has signalled so far.
	sent int
	// confirming is set while Esc asks whether to leave selections that
	// have not been signalled.
	confirming bool
	left       []picker.Item
	// Bubble Tea draws asynchronously, so a row that has just come on screen
	// may not be on the terminal yet, and Enter waits rather than signal a
	// row the user may not have seen. Each change that brings rows on screen
	// starts a wait numbered settleGen. fresh maps each such row to the wait
	// that ends its own, and redrawGen is the wait after the whole screen
	// was drawn again, as when the help closed or kil resumed after Ctrl-Z.
	fresh     map[string]int
	redrawGen int
	settleGen int
}

// settledMsg ends the wait numbered gen and every earlier one.
type settledMsg struct{ gen int }

type reloadMsg struct {
	items []picker.Item
	err   error
}

// New returns a picker over items, filtered by initial. label names the
// signal Enter sends, and reload lists the processes again for Ctrl-R.
func New(items []picker.Item, initial, label string, reload func() ([]picker.Item, error)) Model {
	input := newLineInput(initial)
	p := picker.New(items)
	p.SetQuery(input.String())
	m := Model{
		picker: p,
		input:  input,
		label:  label,
		reload: reload,
		width:  80,
		height: 24,
	}
	return m.unseen()
}

// unseen returns m with the rows it draws waiting for the first frame:
// nothing is on the terminal until Bubble Tea draws it, and Init starts the
// wait. Each option that changes the rows calls it again.
func (m Model) unseen() Model {
	m.settleGen = 1
	m.fresh = map[string]int{}
	for id := range m.drawnIDs() {
		m.fresh[id] = m.settleGen
	}
	return m
}

// WithExact returns m with plain terms matching exactly, as fzf --exact.
func (m Model) WithExact(exact bool) Model {
	m.picker.SetExact(exact)
	return m.unseen()
}

// WithSender returns m with send signalling the selected rows on screen when
// Enter is pressed while other selections are off screen. Without it, such an
// Enter does nothing, so that no off-screen process is signalled.
func (m Model) WithSender(send func([]picker.Item)) Model {
	m.send = send
	return m
}

// Left returns the selections not signalled once the outcome is Stopped, or
// ListFailed after a partial Enter.
func (m Model) Left() []picker.Item { return m.left }

// Query returns the current query.
func (m Model) Query() string { return m.input.String() }

// Outcome returns how the picker ended.
func (m Model) Outcome() Outcome { return m.outcome }

// Targets returns the items to signal once the outcome is Accepted.
func (m Model) Targets() []picker.Item { return m.targets }

// Err returns the reload error once the outcome is ListFailed.
func (m Model) Err() error { return m.err }

// Init implements tea.Model.
func (m Model) Init() tea.Cmd { return m.settleAfter(m.settleGen) }

// settleAfter ends the wait numbered gen once Bubble Tea has had time to draw.
func (m Model) settleAfter(gen int) tea.Cmd {
	return tea.Tick(settleTime, func(time.Time) tea.Msg { return settledMsg{gen} })
}

// Update implements tea.Model.
func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	// Bubble Tea keeps delivering messages until it has quit, and a key
	// typed then must not change what kil signals.
	if m.outcome != Running {
		return m, nil
	}
	// The picker is shared with the next model, so what is on screen now
	// is taken before it changes.
	drawn := m.drawnIDs()
	next, cmd := m.update(msg)
	nm := next.(Model)
	body, cursorLine := nm.layout()
	nm.offset = scrolled(nm.offset, cursorLine, nm.bodyHeight(), len(body))
	if nm.outcome != Running {
		return nm, cmd
	}
	_, resumed := msg.(tea.ResumeMsg)
	redrawn := resumed || m.help && !nm.help
	var came []string
	for id := range nm.drawnIDs() {
		if !drawn[id] {
			came = append(came, id)
		}
	}
	if !redrawn && len(came) == 0 {
		return nm, cmd
	}
	nm.settleGen++
	// The map is copied, as earlier models share it.
	fresh := maps.Clone(nm.fresh)
	if fresh == nil {
		fresh = map[string]int{}
	}
	for _, id := range came {
		fresh[id] = nm.settleGen
	}
	nm.fresh = fresh
	if redrawn {
		nm.redrawGen = nm.settleGen
	}
	return nm, tea.Batch(cmd, nm.settleAfter(nm.settleGen))
}

// drawnIDs returns the IDs of the rows drawn on screen. The help hides
// every row.
func (m Model) drawnIDs() map[string]bool {
	drawn := map[string]bool{}
	if m.help {
		return drawn
	}
	rows := m.picker.Rows()
	from, to := m.shownRows()
	for _, r := range rows[from:to] {
		drawn[r.Item.ID] = true
	}
	return drawn
}

// waiting reports whether Enter must wait because the whole screen has just
// been drawn again or a selected row on screen has just come on screen. The cursor row alone
// does not wait, so that typing a query and pressing Enter at once still
// works, as in fzf.
func (m Model) waiting() bool {
	if m.redrawGen > 0 {
		return true
	}
	shown, _ := m.selectedOnScreen()
	for _, it := range shown {
		if m.fresh[it.ID] > 0 {
			return true
		}
	}
	return false
}

func (m Model) update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		// Some terminals, such as serial consoles, report 0 for an unknown
		// dimension, which would leave no room for a row, so that dimension
		// keeps its size from before. Bubble Tea does not cut the view
		// along it either.
		if msg.Width > 0 {
			m.width = msg.Width
		}
		if msg.Height > 0 {
			m.height = msg.Height
		}
	case settledMsg:
		// Waits end in the order they started, so this ends every
		// earlier one as well.
		if m.redrawGen <= msg.gen {
			m.redrawGen = 0
		}
		m.fresh = maps.Clone(m.fresh)
		maps.DeleteFunc(m.fresh, func(_ string, gen int) bool { return gen <= msg.gen })
	case reloadMsg:
		m.reloading = false
		if msg.err != nil {
			// A failed ps may have produced a partial list, so nothing is
			// signalled from it. After a partial Enter, the selections not
			// signalled are reported, as when the user stops.
			m.outcome, m.err = ListFailed, msg.err
			if m.sent > 0 && m.picker.SelectedCount() > 0 {
				m.left = m.picker.Targets()
			}
			return m, tea.Quit
		}
		m.picker.Reload(msg.items)
	case tea.KeyPressMsg:
		return m.handleKey(msg)
	case tea.PasteMsg:
		// A paste only edits the query: it must not answer the question
		// or close the help, which the user did not see it do.
		if !m.confirming && !m.help {
			m.edit(func(in *lineInput) { in.insert([]rune(msg.Content)) })
		}
	}
	return m, nil
}

func (m Model) handleKey(k tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	key := k.String()
	// Ctrl-Z suspends kil as a shell job, in the help and the question too,
	// as Ctrl-C aborts there.
	if key == "ctrl+z" {
		return m, tea.Suspend
	}
	if m.confirming {
		return m.confirm(key)
	}
	// The help hides the rows, so a key other than Ctrl-C only closes it:
	// Enter must not signal a row the user cannot see, and Esc goes back to
	// the list rather than leaving kil.
	if m.help && key != "ctrl+c" {
		m.help = false
		return m, nil
	}

	switch key {
	case "alt+a":
		added := m.picker.ToggleRange(m.shownRows())
		// The rows Alt-A selected are now pinned at the bottom of the
		// list, so the window goes to them to keep them on screen.
		m.offset = m.firstLineOf(added)
	case "alt+h":
		m.help = true
	case "esc":
		// After a partial Enter, leaving would leave chosen processes
		// running, so Esc asks first.
		if m.sent > 0 && m.picker.SelectedCount() > 0 {
			m.confirming = true
			return m, nil
		}
		m.outcome = Aborted
		return m, tea.Quit
	case "ctrl+c":
		m.outcome = Aborted
		return m, tea.Quit
	case "enter":
		// Without room for rows, the cursor row is not on screen.
		if m.reloading || m.waiting() || m.bodyHeight() == 0 {
			return m, nil
		}
		if shown, offscreen := m.selectedOnScreen(); offscreen {
			return m.sendShown(shown)
		}
		m.targets = m.picker.Targets()
		m.outcome = Accepted
		if len(m.targets) == 0 {
			m.outcome = NothingSelected
		}
		return m, tea.Quit
	// The list grows upward from the prompt, as in fzf's default layout, so
	// moving up on screen goes to the next row and down to the previous.
	case "up", "ctrl+p":
		m.picker.Next()
	case "down", "ctrl+n", "ctrl+j":
		m.picker.Prev()
	case "tab":
		m.picker.Toggle(-1) // fzf's toggle+down
	case "shift+tab":
		m.picker.Toggle(1) // fzf's toggle+up
	case "ctrl+x":
		m.picker.SetExact(!m.picker.Exact())
		m.offset = 0
	case "ctrl+r":
		if m.reloading {
			return m, nil
		}
		m.reloading = true
		reload := m.reload
		return m, func() tea.Msg {
			items, err := reload()
			return reloadMsg{items, err}
		}
	default:
		if edit := lineEdit(k); edit != nil {
			m.edit(edit)
		}
	}
	return m, nil
}

// lineEdit returns how k edits the query with readline's keys, or nil if k
// does not edit it.
func lineEdit(k tea.KeyPressMsg) func(*lineInput) {
	switch k.String() {
	case "ctrl+a", "home":
		return (*lineInput).home
	case "ctrl+e", "end":
		return (*lineInput).end
	case "left", "ctrl+b":
		return (*lineInput).left
	case "right", "ctrl+f":
		return (*lineInput).right
	case "alt+b":
		return (*lineInput).wordLeft
	case "alt+f":
		return (*lineInput).wordRight
	case "backspace", "ctrl+h":
		return (*lineInput).backspace
	case "delete":
		return (*lineInput).deleteChar
	case "ctrl+k":
		return (*lineInput).killToEnd
	case "ctrl+u":
		return (*lineInput).killToStart
	case "ctrl+w":
		return (*lineInput).killWordBack
	}
	// Text typed with Ctrl or Alt is a key binding kil does not have, not
	// text for the query.
	if k.Text != "" && k.Mod&(tea.ModCtrl|tea.ModAlt) == 0 {
		return func(in *lineInput) { in.insert([]rune(k.Text)) }
	}
	return nil
}

// edit applies f to the query. Moving the caret keeps the list as it is;
// only a changed query filters again and moves the cursor back to the best
// match.
func (m *Model) edit(f func(*lineInput)) {
	before := m.input.String()
	f(&m.input)
	if m.input.String() != before {
		m.picker.SetQuery(m.input.String())
		m.offset = 0
	}
}

// selectedOnScreen returns the selected rows drawn on screen, and whether
// other selected rows are off screen.
func (m Model) selectedOnScreen() (shown []picker.Item, offscreen bool) {
	from, to := m.shownRows()
	for i, r := range m.picker.Rows() {
		switch {
		case !r.Selected:
		case i >= from && i < to:
			shown = append(shown, r.Item)
		default:
			offscreen = true
		}
	}
	return shown, offscreen
}

// sendShown signals the selected rows on screen and keeps kil open with the
// rest of the selection brought on screen, so that Enter never signals a
// process the user cannot see and the user sees what is left.
func (m Model) sendShown(shown []picker.Item) (Model, tea.Cmd) {
	if m.send == nil {
		return m, nil
	}
	if len(shown) > 0 {
		m.send(shown)
		m.sent += len(shown)
	}
	// The selections left are pinned at the bottom of the list, where
	// Forget also moves the cursor, so that the window follows them even
	// when none of them was on screen.
	m.picker.Forget(shown)
	m.offset = 0
	return m, nil
}

// settleTime is how long Enter waits after selections come on screen: long
// enough for Bubble Tea to draw them, short enough not to feel slow.
const settleTime = 300 * time.Millisecond

// confirm answers Esc's question about leaving selections not signalled:
// Y or Enter leaves, and n or Esc goes back to the list. Ctrl-C still aborts.
func (m Model) confirm(key string) (tea.Model, tea.Cmd) {
	switch key {
	case "ctrl+c":
		m.outcome = Aborted
		return m, tea.Quit
	case "enter", "y", "Y":
		m.left = m.picker.Targets()
		m.outcome = Stopped
		return m, tea.Quit
	case "esc", "n", "N":
		m.confirming = false
	}
	return m, nil
}

// firstLineOf returns the lowest body line showing one of the items with the
// given IDs, or 0 if none is shown.
func (m Model) firstLineOf(ids []string) int {
	want := map[string]bool{}
	for _, id := range ids {
		want[id] = true
	}
	body, _ := m.layout()
	rows := m.picker.Rows()
	for line, i := range body {
		if i != separator && want[rows[i].Item.ID] {
			return line
		}
	}
	return 0
}
