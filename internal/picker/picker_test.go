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

func TestSelectAllShownSelectsOnlyMatches(t *testing.T) {
	p := New(items("101 node a", "202 python b", "203 python c"))
	p.SetQuery("python")
	p.SelectAllShown()
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

func TestReloadDropsSelectionsThatVanished(t *testing.T) {
	p := New(items("101 node a", "202 python b"))
	p.Toggle(1) // node
	p.Reload(items("202 python b"))
	if p.SelectedCount() != 0 {
		t.Errorf("SelectedCount() = %d, want 0 after the node line vanished", p.SelectedCount())
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
				p.SelectAllShown()
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
