// Package query matches process lines with fzf's extended search syntax:
// space-separated terms are ANDed, "|" between terms ORs them, and a term
// may be fuzzy (default), 'exact, ^prefix, suffix$, ^equal$, or !negated.
// The matching itself is fzf's own algorithm, so results rank as in fzf.
package query

import (
	"slices"
	"strings"
	"sync"
	"unicode"

	"github.com/junegunn/fzf/src/algo"
	"github.com/junegunn/fzf/src/util"
)

type kind int

const (
	fuzzy kind = iota
	exact
	prefix
	suffix
	equal
)

type term struct {
	kind    kind
	negated bool
	text    []rune
}

// A Query is a parsed search string. The zero value matches everything.
type Query struct {
	// groups are ANDed; the terms inside one group are ORed.
	groups        [][]term
	caseSensitive bool
}

var initAlgo sync.Once

// Parse parses s with fzf's extended search syntax.
func Parse(s string) Query {
	initAlgo.Do(func() { algo.Init("default") })

	// fzf's smart case: an uppercase letter anywhere makes the whole query
	// case sensitive.
	q := Query{caseSensitive: strings.ToLower(s) != s}
	orNext := false
	for _, token := range strings.Fields(s) {
		if token == "|" {
			orNext = len(q.groups) > 0
			continue
		}
		t, ok := parseTerm(token, q.caseSensitive)
		if !ok {
			continue
		}
		if orNext {
			last := len(q.groups) - 1
			q.groups[last] = append(q.groups[last], t)
		} else {
			q.groups = append(q.groups, []term{t})
		}
		orNext = false
	}
	return q
}

func parseTerm(token string, caseSensitive bool) (term, bool) {
	t := term{kind: fuzzy}
	if strings.HasPrefix(token, "!") {
		t.negated = true
		// As in fzf, a negated term matches exactly unless marked otherwise.
		t.kind = exact
		token = token[1:]
	}
	switch {
	case strings.HasPrefix(token, "'") && len(token) > 1:
		t.kind = exact
		token = token[1:]
	case strings.HasPrefix(token, "^") && strings.HasSuffix(token, "$") && len(token) > 2:
		t.kind = equal
		token = token[1 : len(token)-1]
	case strings.HasPrefix(token, "^") && len(token) > 1:
		t.kind = prefix
		token = token[1:]
	case strings.HasSuffix(token, "$") && len(token) > 1:
		t.kind = suffix
		token = token[:len(token)-1]
	}
	if token == "" {
		return term{}, false
	}
	if !caseSensitive {
		token = strings.Map(unicode.ToLower, token)
	}
	t.text = algo.NormalizeRunes([]rune(token))
	return t, true
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
	case prefix:
		fn = algo.PrefixMatch
	case suffix:
		fn = algo.SuffixMatch
	case equal:
		fn = algo.EqualMatch
	default:
		fn = algo.FuzzyMatchV2
	}
	// normalize is on as in fzf's default, which folds accented letters.
	res, pos := fn(q.caseSensitive, true, true, chars, t.text, withPos, nil)
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
