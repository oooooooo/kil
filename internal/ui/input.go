package ui

import (
	"sort"
	"unicode"
	"unicode/utf8"

	"github.com/rivo/uniseg"
)

// lineInput is the query being typed, edited with readline's keys. The caret
// moves and deletes by grapheme cluster, so that it never stops between a
// letter and its combining mark, or inside a flag or an emoji joined with
// ZWJ, where typed text split the character and the caret was a blank cell.
type lineInput struct {
	runes []rune
	pos   int
}

func newLineInput(s string) lineInput {
	r := printable([]rune(s))
	return lineInput{runes: r, pos: len(r)}
}

// printable replaces control characters, such as pasted newlines, with
// spaces: written to the terminal, they would move the caret or change
// colors, and a space still separates the words they separated.
func printable(rs []rune) []rune {
	out := make([]rune, len(rs))
	for i, r := range rs {
		if unicode.IsControl(r) {
			r = ' '
		}
		out[i] = r
	}
	return out
}

func (in lineInput) String() string { return string(in.runes) }

func (in *lineInput) insert(rs []rune) {
	rs = printable(rs)
	out := make([]rune, 0, len(in.runes)+len(rs))
	out = append(out, in.runes[:in.pos]...)
	out = append(out, rs...)
	in.runes = append(out, in.runes[in.pos:]...)
	in.pos += len(rs)
	in.snap()
}

// bounds holds the rune index where each grapheme cluster of the query
// starts, followed by the query's length. A word key finds them once, not
// at each step, which took seconds on a pasted query.
type bounds []int

func (in lineInput) bounds() bounds {
	b := bounds{0}
	rest, state := string(in.runes), -1
	for rest != "" {
		var g string
		g, rest, _, state = uniseg.FirstGraphemeClusterInString(rest, state)
		b = append(b, b[len(b)-1]+utf8.RuneCountInString(g))
	}
	return b
}

// prev returns where the cluster before rune index i starts.
func (b bounds) prev(i int) int {
	k := sort.SearchInts(b, i)
	if k == 0 {
		return 0
	}
	return b[k-1]
}

// next returns where the cluster after the one holding rune index i starts.
func (b bounds) next(i int) int {
	k := sort.SearchInts(b, i+1)
	if k == len(b) {
		return b[len(b)-1]
	}
	return b[k]
}

func (in lineInput) next(i int) int { return in.bounds().next(i) }

// snap moves the caret to the end of the cluster it is inside of. Inserted
// text can join the cluster after it, as a regional indicator typed before
// another, and deleted text can join the clusters around it, as a space
// deleted between two regional indicators.
func (in *lineInput) snap() {
	b := in.bounds()
	in.pos = b[sort.SearchInts(b, in.pos)]
}

func (in *lineInput) left()  { in.pos = in.bounds().prev(in.pos) }
func (in *lineInput) right() { in.pos = in.next(in.pos) }
func (in *lineInput) home()  { in.pos = 0 }
func (in *lineInput) end()   { in.pos = len(in.runes) }

// isWord reports whether the cluster starting at rune index i is part of a
// word. The cluster's first rune decides, as a combining mark is neither a
// letter nor a digit.
func (in lineInput) isWord(i int) bool {
	r := in.runes[i]
	return unicode.IsLetter(r) || unicode.IsDigit(r)
}

// wordLeft moves to the start of the word before the cursor, as readline's
// backward-word.
func (in *lineInput) wordLeft() {
	b := in.bounds()
	for in.pos > 0 && !in.isWord(b.prev(in.pos)) {
		in.pos = b.prev(in.pos)
	}
	for in.pos > 0 && in.isWord(b.prev(in.pos)) {
		in.pos = b.prev(in.pos)
	}
}

// wordRight moves past the end of the word after the cursor, as readline's
// forward-word.
func (in *lineInput) wordRight() {
	b := in.bounds()
	for in.pos < len(in.runes) && !in.isWord(in.pos) {
		in.pos = b.next(in.pos)
	}
	for in.pos < len(in.runes) && in.isWord(in.pos) {
		in.pos = b.next(in.pos)
	}
}

func (in *lineInput) deleteRange(from, to int) {
	in.runes = append(in.runes[:from:from], in.runes[to:]...)
	in.pos = from
	in.snap()
}

func (in *lineInput) backspace() {
	if in.pos > 0 {
		in.deleteRange(in.bounds().prev(in.pos), in.pos)
	}
}

func (in *lineInput) deleteChar() {
	if in.pos < len(in.runes) {
		in.deleteRange(in.pos, in.next(in.pos))
	}
}

func (in *lineInput) killToEnd()   { in.runes = in.runes[:in.pos] }
func (in *lineInput) killToStart() { in.deleteRange(0, in.pos) }

// killWordBack deletes back to the previous whitespace, as readline's
// unix-word-rubout (Ctrl-W).
func (in *lineInput) killWordBack() {
	b := in.bounds()
	from := in.pos
	for from > 0 && unicode.IsSpace(in.runes[b.prev(from)]) {
		from = b.prev(from)
	}
	for from > 0 && !unicode.IsSpace(in.runes[b.prev(from)]) {
		from = b.prev(from)
	}
	in.deleteRange(from, in.pos)
}
