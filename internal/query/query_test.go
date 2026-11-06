package query

import (
	"runtime"
	"slices"
	"strings"
	"testing"
)

func TestMatch(t *testing.T) {
	tests := []struct {
		name  string
		query string
		text  string
		want  bool
	}{
		{"empty query matches everything", "", "anything", true},
		{"blank query matches everything", "   ", "anything", true},
		{"fuzzy matches scattered characters", "ndsv", "node server.js", true},
		{"fuzzy needs characters in order", "vsdn", "node server.js", false},
		{"lowercase query ignores case", "node", "NODE server.js", true},
		{"uppercase query is case sensitive", "Node", "node server.js", false},
		{"terms are ANDed", "node srv", "node server.js", true},
		{"every term must match", "node python", "node server.js", false},
		{"exact term needs a substring", "'nsv", "node server.js", false},
		{"exact term matches a substring", "'server", "node server.js", true},
		{"prefix term", "^node", "node server.js", true},
		{"prefix term rejects middle", "^server", "node server.js", false},
		{"suffix term", ".js$", "node server.js", true},
		{"suffix term rejects middle", "node$", "node server.js", false},
		{"equal term", "^node$", "node", true},
		{"equal term rejects longer text", "^node$", "node server.js", false},
		{"negated term excludes", "!server", "node server.js", false},
		{"negated term keeps others", "!python", "node server.js", true},
		{"negated prefix", "!^node", "node server.js", false},
		{"negated suffix", "!.js$", "node server.js", false},
		{"OR matches either side", "python | node", "node server.js", true},
		{"OR matches neither side", "python | ruby", "node server.js", false},
		{"OR group is ANDed with other terms", "python | node srv", "node server.js", true},
		{"OR group fails the AND", "python | node 'worker", "node server.js", false},
		{"lone quote is ignored", "'", "anything", true},
		{"exact boundary matches a whole word", "'wild'", "a wild b", true},
		{"exact boundary rejects part of a word", "'wild'", "wilder", false},
		{"negated quote is fuzzy", "!'fr", "fire", false},
		{"negated quote keeps non-matches", "!'fr", "ice", true},
		{"case is decided per term", "Node srv", "Node SERVER", true},
		{"uppercase term is still case sensitive", "Node srv", "node server", false},
		{"escaped space is part of the term", `node\ ser`, "node server", true},
		{"escaped space must match a space", `node\ ser`, "nodeXser", false},
		{"lone dollar is a literal", "$", "a $ b", true},
		{"leading bar is a term", "|", "a|b", true},
		{"lone bang is ignored", "!", "anything", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, got := Parse(tt.query).Match(tt.text)
			if got != tt.want {
				t.Errorf("Parse(%q).Match(%q) = %v, want %v", tt.query, tt.text, got, tt.want)
			}
		})
	}
}

func TestScoreRanksTighterMatchesHigher(t *testing.T) {
	q := Parse("node")
	tight, ok := q.Match("node server.js")
	if !ok {
		t.Fatal("tight text did not match")
	}
	loose, ok := q.Match("n o d e")
	if !ok {
		t.Fatal("loose text did not match")
	}
	if tight <= loose {
		t.Errorf("score(tight) = %d, score(loose) = %d; want tight > loose", tight, loose)
	}
}

func TestEmpty(t *testing.T) {
	if !Parse("  ").Empty() {
		t.Error("blank query is not Empty")
	}
	if Parse("x").Empty() {
		t.Error("query with a term is Empty")
	}
}

// As in fzf, an OR group scores with its first matching term, not its best.
func TestORGroupScoresWithFirstMatchingTerm(t *testing.T) {
	first, ok := Parse("'od").Match("node xx")
	if !ok {
		t.Fatal("'od did not match")
	}
	got, ok := Parse("'od | ^node").Match("node xx")
	if !ok || got != first {
		t.Errorf("score of \"'od | ^node\" = %d, %v; want the score of 'od, %d", got, ok, first)
	}
}

// fzf normalizes accented letters by default, so "cafe" finds "café".
func TestAccentsAreNormalized(t *testing.T) {
	if _, ok := Parse("cafe").Match("café"); !ok {
		t.Error("\"cafe\" did not match \"café\"")
	}
}

func TestPositions(t *testing.T) {
	tests := []struct {
		query string
		text  string
		want  []int
	}{
		{"", "node", nil},
		{"nd", "node", []int{0, 2}},
		{"'od", "node", []int{1, 2}},
		{"^no", "node", []int{0, 1}},
		{"de$", "node", []int{2, 3}},
		{"!zz", "node", nil},
		{"zz | od", "node", []int{1, 2}},
		{"no de", "node", []int{0, 1, 2, 3}},
	}
	for _, tt := range tests {
		got := Parse(tt.query).Positions(tt.text)
		if !slices.Equal(got, tt.want) {
			t.Errorf("Parse(%q).Positions(%q) = %v, want %v", tt.query, tt.text, got, tt.want)
		}
	}
}

func TestExactMode(t *testing.T) {
	tests := []struct {
		query string
		text  string
		want  bool
	}{
		{"ruby", "ruby app.rb", true},
		{"ruby", "resolve /usr/lib/systemd", false},
		{"'rby", "ruby app.rb", true},   // ' flips to fuzzy, as fzf --exact
		{"!'rby", "ruby app.rb", false}, // negated fuzzy
		{"^rub", "ruby app.rb", true},
	}
	for _, tt := range tests {
		_, got := ParseExact(tt.query).Match(tt.text)
		if got != tt.want {
			t.Errorf("ParseExact(%q).Match(%q) = %v, want %v", tt.query, tt.text, got, tt.want)
		}
	}
}

// A long fuzzy term against a long line stays within fzf's working memory
// and falls back to fzf's lighter algorithm, as fzf does, rather than
// allocating a matrix of term by line: 53 MB for 200 characters against a
// 64 KiB line, done again for filtering and for highlighting.
func TestLongFuzzyMatchesStayWithinFzfsMemory(t *testing.T) {
	q := Parse(strings.Repeat("a", 200))
	line := strings.Repeat("a", 64*1024)
	q.Positions(line) // the working memory is allocated once
	var before, after runtime.MemStats
	runtime.ReadMemStats(&before)
	if _, ok := q.Match(line); !ok {
		t.Fatal("the line does not match")
	}
	q.Positions(line)
	runtime.ReadMemStats(&after)
	if used := after.TotalAlloc - before.TotalAlloc; used > 4<<20 {
		t.Errorf("matching allocated %d MB, want under 4", used>>20)
	}
}
