// Package query matches process lines with fzf's extended search syntax:
// space-separated terms are ANDed, "|" between terms ORs them, and a term
// may be fuzzy (default), 'exact, 'boundary', ^prefix, suffix$, ^equal$, or
// !negated. Parsing is a port of fzf's parseTerms, and the matching itself
// is fzf's own algorithm, so results rank as in fzf.
package query

import (
	"regexp"
	"slices"
	"strings"
	"sync"

	"github.com/junegunn/fzf/src/algo"
	"github.com/junegunn/fzf/src/util"
)

type kind int

const (
	fuzzy kind = iota
	exact
	boundary
	prefix
	suffix
	equal
)

type term struct {
	kind          kind
	negated       bool
	text          []rune
	caseSensitive bool
	normalize     bool
}

// A Query is a parsed search string. The zero value matches everything.
type Query struct {
	// groups are ANDed; the terms inside one group are ORed.
	groups [][]term
}

var (
	initAlgo sync.Once
	spaces   = regexp.MustCompile(" +")
)

// Parse parses s with fzf's extended search syntax, where a plain term
// matches fuzzily.
func Parse(s string) Query {
	return parse(s, true)
}

// ParseExact parses s as fzf --exact does: a plain term matches exactly,
// and a leading ' makes a term fuzzy instead.
func ParseExact(s string) Query {
	return parse(s, false)
}

// parse follows fzf's parseTerms (src/pattern.go), with fzf's defaults of
// smart case and normalization.
func parse(s string, fuzzyByDefault bool) Query {
	initAlgo.Do(func() { algo.Init("default") })

	var q Query
	var group []term
	newGroup, afterBar := false, false
	// "\ " is a literal space inside a term.
	s = strings.ReplaceAll(s, "\\ ", "\t")
	for _, token := range spaces.Split(s, -1) {
		text := strings.ReplaceAll(token, "\t", " ")
		lower := strings.ToLower(text)
		// Smart case is decided per term.
		t := term{
			kind:          fuzzy,
			caseSensitive: text != lower,
			normalize:     lower == string(algo.NormalizeRunes([]rune(lower))),
		}
		if !t.caseSensitive {
			text = lower
		}
		if !fuzzyByDefault {
			t.kind = exact
		}

		if len(group) > 0 && !afterBar && text == "|" {
			newGroup, afterBar = false, true
			continue
		}
		afterBar = false

		if strings.HasPrefix(text, "!") {
			t.negated, t.kind = true, exact
			text = text[1:]
		}
		if text != "$" && strings.HasSuffix(text, "$") {
			t.kind = suffix
			text = text[:len(text)-1]
		}
		switch {
		case len(text) > 2 && strings.HasPrefix(text, "'") && strings.HasSuffix(text, "'"):
			t.kind = boundary
			text = text[1 : len(text)-1]
		case strings.HasPrefix(text, "'"):
			// A quote flips exactness.
			if fuzzyByDefault && !t.negated {
				t.kind = exact
			} else {
				t.kind = fuzzy
			}
			text = text[1:]
		case strings.HasPrefix(text, "^"):
			if t.kind == suffix {
				t.kind = equal
			} else {
				t.kind = prefix
			}
			text = text[1:]
		}

		if text == "" {
			continue
		}
		if newGroup {
			q.groups = append(q.groups, group)
			group = nil
		}
		t.text = []rune(text)
		if t.normalize {
			t.text = algo.NormalizeRunes(t.text)
		}
		group = append(group, t)
		newGroup = true
	}
	if len(group) > 0 {
		q.groups = append(q.groups, group)
	}
	return q
}

// Empty reports whether q has no terms, so that it matches every line.
func (q Query) Empty() bool {
	return len(q.groups) == 0
}

// Sortable reports whether matches should be ranked by score. As in fzf, a
// query with only negated terms gives every line the same score, so its
// matches keep their input order.
func (q Query) Sortable() bool {
	for _, group := range q.groups {
		for _, t := range group {
			if !t.negated {
				return true
			}
		}
	}
	return false
}

// Match reports whether text satisfies q and, if so, a score that is higher
// for tighter matches.
func (q Query) Match(text string) (int, bool) {
	score, _, ok := q.match(text, false)
	return score, ok
}

// Positions returns the rune indexes in text that q matched, for
// highlighting, in ascending order. It returns nil if text does not match.
func (q Query) Positions(text string) []int {
	_, pos, ok := q.match(text, true)
	if !ok || len(pos) == 0 {
		return nil
	}
	slices.Sort(pos)
	return slices.Compact(pos)
}

func (q Query) match(text string, withPos bool) (int, []int, bool) {
	chars := util.ToChars([]byte(text))
	total := 0
	var positions []int
	for _, group := range q.groups {
		score, pos, ok := q.matchGroup(group, &chars, withPos)
		if !ok {
			return 0, nil, false
		}
		total += score
		positions = append(positions, pos...)
	}
	return total, positions, true
}

// matchGroup follows fzf's extendedMatch: the first matching term decides
// the score, and a negated term that holds matches with score 0 but lets a
// later term still supply a score.
func (q Query) matchGroup(group []term, chars *util.Chars, withPos bool) (int, []int, bool) {
	matched := false
	for _, t := range group {
		res, pos := q.matchTerm(t, chars, withPos)
		found := res.Start >= 0
		if t.negated {
			if !found {
				matched = true
			}
			continue
		}
		if found {
			return res.Score, pos, true
		}
	}
	return 0, nil, matched
}

func (q Query) matchTerm(t term, chars *util.Chars, withPos bool) (algo.Result, []int) {
	var fn algo.Algo
	switch t.kind {
	case exact:
		fn = algo.ExactMatchNaive
	case boundary:
		fn = algo.ExactMatchBoundary
	case prefix:
		fn = algo.PrefixMatch
	case suffix:
		fn = algo.SuffixMatch
	case equal:
		fn = algo.EqualMatch
	default:
		fn = algo.FuzzyMatchV2
	}
	slabMu.Lock()
	defer slabMu.Unlock()
	res, pos := fn(t.caseSensitive, t.normalize, true, chars, t.text, withPos, slab)
	if !withPos || res.Start < 0 || t.negated {
		return res, nil
	}
	if pos != nil {
		return res, *pos
	}
	// Only the fuzzy algorithm reports positions; the others match one
	// contiguous range, as fzf highlights them.
	var out []int
	for i := res.Start; i < res.End; i++ {
		out = append(out, i)
	}
	return res, out
}

// slab is the working memory fzf's algorithms use, of fzf's own size. Given
// none, the fuzzy algorithm allocated a matrix of term by line, 53 MB for a
// term of 200 characters against a line of 64 KiB; with it, a match too
// large for it falls back to fzf's lighter algorithm, as in fzf.
var (
	slab   = util.MakeSlab(100*1024, 2048)
	slabMu sync.Mutex
)
