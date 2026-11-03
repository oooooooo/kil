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
	"strings"
	"unicode/utf8"

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
	items []Item
	// gone holds selected items that a reload no longer listed. They stay
	// pinned while selected, so that Enter does not fall back to the cursor
	// row, which the user never chose; signalling reports them as skipped.
	gone  []Item
	text  string
	exact bool
	query query.Query
	// selected maps a selected item's ID to when it was selected, so that
	// pinned rows keep the order in which they were selected.
	selected map[string]int
	selects  int
	rows     []Row
	cursor   int
}

// New returns a Picker showing items in the given order.
func New(items []Item) *Picker {
	p := &Picker{selected: map[string]int{}}
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

// Total returns the number of items: the listed ones, plus the selected ones
// a reload no longer listed, which stay on screen.
func (p *Picker) Total() int {
	n := len(p.items)
	for _, it := range p.gone {
		if _, ok := p.selected[it.ID]; ok {
			n++
		}
	}
	return n
}

// SetQuery filters the rows with s and moves the cursor to the first row.
func (p *Picker) SetQuery(s string) {
	p.text = s
	p.reparse()
}

// SetExact switches between fuzzy matching and fzf --exact matching, where
// plain terms match substrings, and moves the cursor to the first row.
func (p *Picker) SetExact(exact bool) {
	p.exact = exact
	p.reparse()
}

// Exact reports whether plain terms match exactly.
func (p *Picker) Exact() bool {
	return p.exact
}

// Query returns the parsed query in the current mode.
func (p *Picker) Query() query.Query {
	return p.query
}

func (p *Picker) reparse() {
	if p.exact {
		p.query = query.ParseExact(p.text)
	} else {
		p.query = query.Parse(p.text)
	}
	p.refresh()
	p.cursor = 0
}

// Prev moves the cursor to the previous row.
func (p *Picker) Prev() {
	p.cursor = max(p.cursor-1, 0)
}

// Next moves the cursor to the next row.
func (p *Picker) Next() {
	p.cursor = min(p.cursor+1, max(len(p.rows)-1, 0))
}

// Toggle flips the selection of the focused row and then moves the cursor
// step rows from it, to the row that was there before the toggle. That row
// may have moved, because the toggled row joined or left the pinned rows.
// If there is no such row, the cursor stays on the toggled row.
func (p *Picker) Toggle(step int) {
	if len(p.rows) == 0 {
		return
	}
	toggled := p.rows[p.cursor].Item.ID
	next := toggled
	if i := p.cursor + step; i >= 0 && i < len(p.rows) {
		next = p.rows[i].Item.ID
	}
	if _, ok := p.selected[toggled]; ok {
		delete(p.selected, toggled)
	} else {
		p.selectItem(toggled)
	}
	p.refresh()
	p.focus(next)
}

// ToggleAllShown deselects every row shown if all of them are selected, and
// otherwise selects the rest of them, in the order shown so that the rows
// do not move.
func (p *Picker) ToggleAllShown() {
	allSelected := true
	for _, r := range p.rows {
		if !r.Selected {
			allSelected = false
			break
		}
	}
	for _, r := range p.rows {
		switch {
		case allSelected:
			delete(p.selected, r.Item.ID)
		case !r.Selected:
			p.selectItem(r.Item.ID)
		}
	}
	p.refreshKeepingFocus()
}

func (p *Picker) selectItem(id string) {
	p.selects++
	p.selected[id] = p.selects
}

// ClearSelection deselects every item.
func (p *Picker) ClearSelection() {
	clear(p.selected)
	p.refreshKeepingFocus()
}

// Reload replaces the items, keeping the query, the selections, and the focus
// if its item still exists. Selected items that are no longer listed stay
// selected until deselected.
func (p *Picker) Reload(items []Item) {
	present := map[string]bool{}
	for _, it := range items {
		present[it.ID] = true
	}
	var gone []Item
	for _, it := range slices.Concat(p.items, p.gone) {
		if _, ok := p.selected[it.ID]; ok && !present[it.ID] {
			gone = append(gone, it)
		}
	}
	p.items = slices.Clone(items)
	p.gone = gone
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
	for _, it := range p.gone {
		if _, ok := p.selected[it.ID]; ok {
			pinned = append(pinned, Row{Item: it, Selected: true, Pinned: true})
		}
	}
	for _, it := range p.items {
		if _, ok := p.selected[it.ID]; ok {
			pinned = append(pinned, Row{Item: it, Selected: true, Pinned: true})
			continue
		}
		if score, ok := p.query.Match(it.Text); ok {
			matches = append(matches, match{it, score})
		}
	}
	slices.SortFunc(pinned, func(a, b Row) int {
		return p.selected[a.Item.ID] - p.selected[b.Item.ID]
	})
	if p.query.Sortable() {
		// fzf's default --tiebreak=length,index: among equal scores, the
		// shorter line first, then the earlier one (the sort is stable).
		slices.SortStableFunc(matches, func(a, b match) int {
			if a.score != b.score {
				return b.score - a.score
			}
			return trimmedLen(a.item.Text) - trimmedLen(b.item.Text)
		})
	}
	p.rows = pinned
	for _, m := range matches {
		p.rows = append(p.rows, Row{Item: m.item})
	}
}

func trimmedLen(s string) int {
	return utf8.RuneCountInString(strings.TrimSpace(s))
}
