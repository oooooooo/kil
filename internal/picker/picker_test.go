package picker

import (
	"math/rand/v2"
	"slices"
	"testing"
)

func items(lines ...string) []Item {
	out := make([]Item, len(lines))
	for i, l := range lines {
		out[i] = Item{ID: l, Text: l}
	}
	return out
}

func texts(rows []Row) []string {
	out := make([]string, len(rows))
	for i, r := range rows {
		out[i] = r.Item.Text
	}
	return out
}

func targetTexts(p *Picker) []string {
	var out []string
	for _, it := range p.Targets() {
		out = append(out, it.Text)
	}
	return out
}

func TestEmptyQueryShowsEveryItemInOrder(t *testing.T) {
	p := New(items("101 node a", "202 python b", "303 ruby c"))
	got := texts(p.Rows())
	want := []string{"101 node a", "202 python b", "303 ruby c"}
	if !slices.Equal(got, want) {
		t.Errorf("Rows() = %v, want %v", got, want)
	}
}

func TestQueryFiltersRows(t *testing.T) {
	p := New(items("101 node a", "202 python b", "303 ruby c"))
	p.SetQuery("python")
	got := texts(p.Rows())
	if !slices.Equal(got, []string{"202 python b"}) {
		t.Errorf("Rows() = %v, want only the python line", got)
	}
}

func TestEnterWithoutSelectionTargetsCursorRow(t *testing.T) {
	p := New(items("101 node a", "202 python b"))
	p.Next()
	if got := targetTexts(p); !slices.Equal(got, []string{"202 python b"}) {
		t.Errorf("Targets() = %v, want the cursor row", got)
	}
}

func TestNoTargetsWhenNothingMatches(t *testing.T) {
	p := New(items("101 node a"))
	p.SetQuery("zzz")
	if got := p.Targets(); len(got) != 0 {
		t.Errorf("Targets() = %v, want none", got)
	}
}

func TestSelectionHiddenByQueryStaysPinnedAndVisible(t *testing.T) {
	p := New(items("101 node a", "202 python b", "203 python c"))
	p.Toggle(1) // selects node
	p.SetQuery("python")

	rows := p.Rows()
	want := []string{"101 node a", "202 python b", "203 python c"}
	if got := texts(rows); !slices.Equal(got, want) {
		t.Fatalf("Rows() = %v, want pinned node first, then matches %v", got, want)
	}
	if !rows[0].Selected || !rows[0].Pinned {
		t.Errorf("node row = %+v, want Selected and Pinned", rows[0])
	}
	if rows[1].Pinned {
		t.Errorf("matching row is marked Pinned: %+v", rows[1])
	}
}

func TestSelectingAcrossQueries(t *testing.T) {
	p := New(items("101 node a", "202 python b", "303 ruby c"))
	p.SetQuery("node")
	p.Toggle(1)
	p.SetQuery("python")
	p.Next() // skip the pinned node row
	p.Toggle(1)

	got := targetTexts(p)
	want := []string{"101 node a", "202 python b"}
	if !slices.Equal(got, want) {
		t.Errorf("Targets() = %v, want %v", got, want)
	}
}

func TestToggleTwiceDeselects(t *testing.T) {
	p := New(items("101 node a", "202 python b"))
	p.Toggle(1)
	p.Prev() // back onto node
	p.Toggle(1)
	if p.SelectedCount() != 0 {
		t.Errorf("SelectedCount() = %d, want 0", p.SelectedCount())
	}
}

func TestToggleMovesToTheRowThatFollowedIt(t *testing.T) {
	p := New(items("101 node a", "202 python b", "303 ruby c"))
	p.Next() // python
	p.Toggle(1)
	// python is pinned to the top, so ruby, which followed it, is now row 2.
	if got := p.Rows()[p.Cursor()].Item.Text; got != "303 ruby c" {
		t.Errorf("focused %q after Toggle(1), want the row after python", got)
	}
}

func TestToggleCanMoveToThePrecedingRow(t *testing.T) {
	p := New(items("101 node a", "202 python b", "303 ruby c"))
	p.Next()
	p.Next() // ruby
	p.Toggle(-1)
	if got := p.Rows()[p.Cursor()].Item.Text; got != "202 python b" {
		t.Errorf("focused %q after Toggle(-1), want the row before ruby", got)
	}
}

func TestToggleWithoutMoveKeepsFocusOnTheToggledRow(t *testing.T) {
	p := New(items("101 node a", "202 python b"))
	p.Next() // python
	p.Toggle(0)
	if got := p.Rows()[p.Cursor()].Item.Text; got != "202 python b" {
		t.Errorf("focused %q after Toggle(0), want python", got)
	}
}

func TestToggleAtTheEdgeStaysOnTheToggledRow(t *testing.T) {
	p := New(items("101 node a", "202 python b"))
	p.Toggle(-1) // node is row 0; there is no row before it
	if got := p.Rows()[p.Cursor()].Item.Text; got != "101 node a" {
		t.Errorf("focused %q, want node", got)
	}
}

func TestToggleRangeSelectsOnlyMatches(t *testing.T) {
	p := New(items("101 node a", "202 python b", "203 python c"))
	p.SetQuery("python")
	p.ToggleRange(0, len(p.Rows()))
	got := targetTexts(p)
	want := []string{"202 python b", "203 python c"}
	if !slices.Equal(got, want) {
		t.Errorf("Targets() = %v, want %v", got, want)
	}
}

func TestReloadKeepsSelectionsThatStillExist(t *testing.T) {
	p := New(items("101 node a", "202 python b"))
	p.Toggle(1) // node
	p.Reload(items("101 node a", "202 python b", "404 go d"))
	if got := targetTexts(p); !slices.Equal(got, []string{"101 node a"}) {
		t.Errorf("Targets() = %v, want node kept", got)
	}
}

// A selected process that exited stays selected, so that Enter does not fall
// back to the cursor row, which the user never chose.
func TestReloadKeepsSelectionsThatVanished(t *testing.T) {
	p := New(items("101 node a", "202 python b"))
	p.Toggle(1) // node
	p.Reload(items("202 python b"))
	if got := targetTexts(p); !slices.Equal(got, []string{"101 node a"}) {
		t.Errorf("Targets() = %v, want the vanished node line still selected", got)
	}
	if got := texts(p.Rows()); !slices.Contains(got, "101 node a") {
		t.Errorf("Rows() = %v, want the vanished selection still shown", got)
	}
}

// Once deselected, a vanished process is gone for good: it no longer
// matches queries as if it still existed.
func TestDeselectedVanishedItemDisappears(t *testing.T) {
	p := New(items("101 node a", "202 python b"))
	p.Toggle(1) // node
	p.Reload(items("202 python b"))
	p.ClearSelection()
	if got := texts(p.Rows()); slices.Contains(got, "101 node a") {
		t.Errorf("Rows() = %v, want the vanished node line gone after deselecting", got)
	}
}

func TestReloadKeepsQuery(t *testing.T) {
	p := New(items("101 node a"))
	p.SetQuery("python")
	p.Reload(items("101 node a", "202 python b"))
	if got := texts(p.Rows()); !slices.Equal(got, []string{"202 python b"}) {
		t.Errorf("Rows() = %v, want the query still applied", got)
	}
}

func TestCursorStaysInRange(t *testing.T) {
	p := New(items("101 node a", "202 python b"))
	p.Prev()
	if p.Cursor() != 0 {
		t.Errorf("Cursor() after Up at top = %d, want 0", p.Cursor())
	}
	p.Next()
	p.Next()
	if p.Cursor() != 1 {
		t.Errorf("Cursor() after Down past end = %d, want 1", p.Cursor())
	}
	p.SetQuery("python")
	if p.Cursor() != 0 {
		t.Errorf("Cursor() after query change = %d, want 0", p.Cursor())
	}
}

// The safety property the picker exists for: whatever the user does, every
// process Enter would signal is a row on screen.
func TestTargetsAreAlwaysShownRows(t *testing.T) {
	lines := []string{"101 node a", "202 python b", "203 python c", "303 ruby d", "404 go e"}
	queries := []string{"", "node", "python", "py", "zzz", "!python", "node | ruby", "^4"}
	rng := rand.New(rand.NewPCG(1, 2))
	for run := range 500 {
		p := New(items(lines...))
		for step := range 30 {
			switch rng.IntN(7) {
			case 0:
				p.SetQuery(queries[rng.IntN(len(queries))])
			case 1:
				p.Toggle(rng.IntN(3) - 1)
			case 2:
				p.Prev()
			case 3:
				p.Next()
			case 4:
				p.ToggleRange(0, len(p.Rows()))
			case 5:
				subset := slices.Clone(lines)
				rng.Shuffle(len(subset), func(i, j int) { subset[i], subset[j] = subset[j], subset[i] })
				p.Reload(items(subset[:rng.IntN(len(subset)+1)]...))
			case 6:
				p.ClearSelection()
			}
			shown := texts(p.Rows())
			// A selected item must neither be signalled while hidden nor be
			// silently left out of what Enter signals.
			if n := p.SelectedCount(); n > 0 && len(p.Targets()) != n {
				t.Fatalf("run %d step %d: %d selected but %d targets", run, step, n, len(p.Targets()))
			}
			for _, target := range targetTexts(p) {
				if !slices.Contains(shown, target) {
					t.Fatalf("run %d step %d: target %q is not shown in %v", run, step, target, shown)
				}
			}
		}
	}
}

// fzf's default --tiebreak=length,index: among equal scores, the shorter
// line ranks first, then the earlier one.
func TestEqualScoresRankShorterLinesFirst(t *testing.T) {
	p := New(items("101 java -cp a very long classpath node", "202 node b", "303 node c"))
	p.SetQuery("'node")
	got := texts(p.Rows())
	want := []string{"202 node b", "303 node c", "101 java -cp a very long classpath node"}
	if !slices.Equal(got, want) {
		t.Errorf("Rows() = %v, want %v", got, want)
	}
}

// As in fzf, a query with only negated terms keeps the input order.
func TestOnlyNegatedTermsKeepInputOrder(t *testing.T) {
	p := New(items("101 a much longer line", "202 short"))
	p.SetQuery("!zzz")
	got := texts(p.Rows())
	want := []string{"101 a much longer line", "202 short"}
	if !slices.Equal(got, want) {
		t.Errorf("Rows() = %v, want %v", got, want)
	}
}

func TestExactModeMatchesSubstringsOnly(t *testing.T) {
	p := New(items("101 ruby app.rb", "202 resolve /usr/lib/systemd"))
	p.SetQuery("ruby")
	if got := texts(p.Rows()); len(got) != 2 {
		t.Fatalf("fuzzy Rows() = %v, want both lines", got)
	}
	p.SetExact(true)
	if got := texts(p.Rows()); !slices.Equal(got, []string{"101 ruby app.rb"}) {
		t.Errorf("exact Rows() = %v, want only the ruby line", got)
	}
	if !p.Exact() {
		t.Error("Exact() = false after SetExact(true)")
	}
	p.SetExact(false)
	if got := texts(p.Rows()); len(got) != 2 {
		t.Errorf("Rows() after SetExact(false) = %v, want both lines again", got)
	}
}

func TestPinnedRowsKeepTheOrderTheyWereSelectedIn(t *testing.T) {
	p := New(items("101 node a", "202 python b", "303 ruby c"))
	p.Next()
	p.Next()
	p.Toggle(0) // ruby first
	p.SetQuery("")
	p.Next() // node is the first unpinned row now
	p.Toggle(0)
	got := targetTexts(p)
	want := []string{"303 ruby c", "101 node a"}
	if !slices.Equal(got, want) {
		t.Errorf("Targets() = %v, want selection order %v", got, want)
	}
}

func TestToggleRangeKeepsTheShownOrder(t *testing.T) {
	// Equal-length lines rank by score, so "node" puts the tight match first.
	p := New(items("101 n o d e", "202 node b"))
	p.SetQuery("node")
	before := texts(p.Rows())
	p.ToggleRange(0, len(p.Rows()))
	if got := texts(p.Rows()); !slices.Equal(got, before) {
		t.Errorf("Rows() after ToggleRange = %v, want the order unchanged %v", got, before)
	}
}

func TestToggleRangeDeselectsWhenEverythingShownIsSelected(t *testing.T) {
	p := New(items("101 node a", "202 python b", "203 python c"))
	p.SetQuery("python")
	p.ToggleRange(0, len(p.Rows()))
	if p.SelectedCount() != 2 {
		t.Fatalf("SelectedCount() = %d after the first toggle, want 2", p.SelectedCount())
	}
	p.ToggleRange(0, len(p.Rows()))
	if p.SelectedCount() != 0 {
		t.Errorf("SelectedCount() = %d after the second toggle, want 0", p.SelectedCount())
	}
}

func TestToggleRangeSelectsTheRestWhenSomeAreSelected(t *testing.T) {
	p := New(items("101 node a", "202 python b"))
	p.Toggle(0)
	p.ToggleRange(0, len(p.Rows()))
	if p.SelectedCount() != 2 {
		t.Errorf("SelectedCount() = %d, want 2", p.SelectedCount())
	}
}

// Alt-A acts on the rows drawn on screen, which the UI passes as a range,
// so rows outside it stay unselected.
func TestToggleRangeSelectsOnlyTheRange(t *testing.T) {
	p := New(items("101 a", "202 b", "303 c"))
	p.ToggleRange(1, 2)
	if got := targetTexts(p); !slices.Equal(got, []string{"202 b"}) {
		t.Errorf("Targets() = %v, want only the row in the range", got)
	}
}
