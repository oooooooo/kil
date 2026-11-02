// Package query matches process lines with fzf's extended search syntax:
// space-separated terms are ANDed, "|" between terms ORs them, and a term
// may be fuzzy (default), 'exact, ^prefix, suffix$, ^equal$, or !negated.
// The matching itself is fzf's own algorithm, so results rank as in fzf.
package query

import (
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
	t.text = []rune(token)
	return t, true
}

// Empty reports whether q has no terms, so that it matches every line.
func (q Query) Empty() bool {
	return len(q.groups) == 0
}

// Match reports whether text satisfies q and, if so, a score that is higher
// for tighter matches.
func (q Query) Match(text string) (int, bool) {
	chars := util.ToChars([]byte(text))
	total := 0
	for _, group := range q.groups {
		best, matched := 0, false
		for _, t := range group {
			score, ok := q.matchTerm(t, &chars)
			if ok && (!matched || score > best) {
				best, matched = score, true
			}
		}
		if !matched {
			return 0, false
		}
		total += best
	}
	return total, true
}

func (q Query) matchTerm(t term, chars *util.Chars) (int, bool) {
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
	res, _ := fn(q.caseSensitive, false, true, chars, t.text, false, nil)
	found := res.Start >= 0
	if t.negated {
		return 0, !found
	}
	return res.Score, found
}
