// Package picker holds what the user has filtered, selected, and focused.
//
// Unlike fzf, a selected item stays on screen when the query no longer
// matches it: every selected item is pinned above the matches. Enter signals
// the selected items, so pinning them guarantees that kil never signals a
// process the user cannot see, while still allowing selections to be built
// up across several queries.
package picker

import (
	"slices"

	"github.com/oooooooo/kil/internal/query"
)

// An Item is one selectable line.
type Item struct {
	// ID identifies the item across reloads. kil uses the whole ps line, so
	// a selection does not carry over to a new process that reused the PID.
	ID   string
	Text string
}

// A Row is an Item as currently shown.
type Row struct {
	Item     Item
	Selected bool
	// Pinned is set for rows shown only because they are selected, above the
	// rows that match the query.
	Pinned bool
}

// A Picker is the filter, selection, and cursor state of one kil session.
type Picker struct {
	items    []Item
	query    query.Query
	selected map[string]bool
	rows     []Row
	cursor   int
}

// New returns a Picker showing items in the given order.
func New(items []Item) *Picker {
	p := &Picker{selected: map[string]bool{}}
	p.Reload(items)
	return p
}

// Rows returns the rows to show: selected items first, then the unselected
// items that match the query, best match first.
func (p *Picker) Rows() []Row {
	return p.rows
}

// Cursor returns the index of the focused row in Rows.
func (p *Picker) Cursor() int {
	return p.cursor
}

// SelectedCount returns the number of selected items.
func (p *Picker) SelectedCount() int {
	return len(p.selected)
}

// SetQuery filters the rows with s and moves the cursor to the first row.
func (p *Picker) SetQuery(s string) {
	p.query = query.Parse(s)
	p.refresh()
	p.cursor = 0
}

// Up moves the cursor one row up.
func (p *Picker) Up() {
	p.cursor = max(p.cursor-1, 0)
}

// Down moves the cursor one row down.
func (p *Picker) Down() {
	p.cursor = min(p.cursor+1, max(len(p.rows)-1, 0))
}

// Toggle flips the selection of the focused row and moves the cursor on to
// the row that followed it, which may have moved because the toggled row
// joined or left the pinned rows.
func (p *Picker) Toggle() {
	if len(p.rows) == 0 {
		return
	}
	toggled := p.rows[p.cursor].Item.ID
	next := toggled
	if p.cursor+1 < len(p.rows) {
		next = p.rows[p.cursor+1].Item.ID
	}
	if p.selected[toggled] {
		delete(p.selected, toggled)
	} else {
		p.selected[toggled] = true
	}
	p.refresh()
	p.focus(next)
}

// SelectAllShown selects every row currently shown.
func (p *Picker) SelectAllShown() {
	for _, r := range p.rows {
		p.selected[r.Item.ID] = true
	}
	p.refreshKeepingFocus()
}

// ClearSelection deselects every item.
func (p *Picker) ClearSelection() {
	clear(p.selected)
	p.refreshKeepingFocus()
}

// Reload replaces the items, keeping the query, the selections whose items
// still exist, and the focus if its item still exists.
func (p *Picker) Reload(items []Item) {
	p.items = slices.Clone(items)
	present := map[string]bool{}
	for _, it := range items {
		present[it.ID] = true
	}
	for id := range p.selected {
		if !present[id] {
			delete(p.selected, id)
		}
	}
	p.refreshKeepingFocus()
}

// Targets returns the items Enter acts on: the selected items, or the focused
// row when nothing is selected. Every target is in Rows.
func (p *Picker) Targets() []Item {
	if len(p.selected) > 0 {
		var out []Item
		for _, r := range p.rows {
			if r.Selected {
				out = append(out, r.Item)
			}
		}
		return out
	}
	if len(p.rows) == 0 {
		return nil
	}
	return []Item{p.rows[p.cursor].Item}
}

func (p *Picker) refreshKeepingFocus() {
	focused := ""
	if p.cursor < len(p.rows) {
		focused = p.rows[p.cursor].Item.ID
	}
	p.refresh()
	p.focus(focused)
}

func (p *Picker) focus(id string) {
	for i, r := range p.rows {
		if r.Item.ID == id {
			p.cursor = i
			return
		}
	}
	p.cursor = min(p.cursor, max(len(p.rows)-1, 0))
}

func (p *Picker) refresh() {
	type match struct {
		item  Item
		score int
	}
	var pinned []Row
	var matches []match
	for _, it := range p.items {
		if p.selected[it.ID] {
			pinned = append(pinned, Row{Item: it, Selected: true, Pinned: true})
			continue
		}
		if score, ok := p.query.Match(it.Text); ok {
			matches = append(matches, match{it, score})
		}
	}
	if !p.query.Empty() {
		slices.SortStableFunc(matches, func(a, b match) int { return b.score - a.score })
	}
	p.rows = pinned
	for _, m := range matches {
		p.rows = append(p.rows, Row{Item: m.item})
	}
}
