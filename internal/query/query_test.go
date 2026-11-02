package query

import "testing"

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
		{"lone quote is a literal", "'", "it's", true},
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
