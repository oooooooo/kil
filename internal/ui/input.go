package ui

import "unicode"

// lineInput is the query being typed, edited with readline's keys.
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
}

func (in *lineInput) left()  { in.pos = max(in.pos-1, 0) }
func (in *lineInput) right() { in.pos = min(in.pos+1, len(in.runes)) }
func (in *lineInput) home()  { in.pos = 0 }
func (in *lineInput) end()   { in.pos = len(in.runes) }

func isWord(r rune) bool { return unicode.IsLetter(r) || unicode.IsDigit(r) }

// wordLeft moves to the start of the word before the cursor, as readline's
// backward-word.
func (in *lineInput) wordLeft() {
	for in.pos > 0 && !isWord(in.runes[in.pos-1]) {
		in.pos--
	}
	for in.pos > 0 && isWord(in.runes[in.pos-1]) {
		in.pos--
	}
}

// wordRight moves past the end of the word after the cursor, as readline's
// forward-word.
func (in *lineInput) wordRight() {
	for in.pos < len(in.runes) && !isWord(in.runes[in.pos]) {
		in.pos++
	}
	for in.pos < len(in.runes) && isWord(in.runes[in.pos]) {
		in.pos++
	}
}

func (in *lineInput) deleteRange(from, to int) {
	in.runes = append(in.runes[:from:from], in.runes[to:]...)
	in.pos = from
}

func (in *lineInput) backspace() {
	if in.pos > 0 {
		in.deleteRange(in.pos-1, in.pos)
	}
}

func (in *lineInput) deleteChar() {
	if in.pos < len(in.runes) {
		in.deleteRange(in.pos, in.pos+1)
	}
}

func (in *lineInput) killToEnd()   { in.runes = in.runes[:in.pos] }
func (in *lineInput) killToStart() { in.deleteRange(0, in.pos) }

// killWordBack deletes back to the previous whitespace, as readline's
// unix-word-rubout (Ctrl-W).
func (in *lineInput) killWordBack() {
	from := in.pos
	for from > 0 && unicode.IsSpace(in.runes[from-1]) {
		from--
	}
	for from > 0 && !unicode.IsSpace(in.runes[from-1]) {
		from--
	}
	in.deleteRange(from, in.pos)
}
