package ui

import (
	"fmt"
	"strings"

	"github.com/mattn/go-runewidth"

	"github.com/oooooooo/kil/internal/picker"
)

// The palette is fzf's default 256-color theme, so kil looks like the
// fzf-based version, plus colors for the PID, PPID, and USER columns.
const (
	colorGutter       = 236
	colorCursorBg     = 236
	colorCursorFg     = 254
	colorPointer      = 161
	colorMarker       = 168
	colorMatch        = 108
	colorMatchFocused = 151
	colorInfo         = 144
	colorBorder       = 59
	colorHeader       = 109
	colorPrompt       = 110
	colorPID          = 179
	colorPPID         = 243
	colorUser         = 73
)

// widths measures text as fzf and terminals draw it. The default condition
// follows the locale and counts ambiguous-width characters such as ·, ▌, ─,
// and │ as two columns in East Asian locales, which would cut rows short.
var widths = &runewidth.Condition{EastAsianWidth: false}

func fg(c int) string { return fmt.Sprintf("\x1b[38;5;%dm", c) }
func bg(c int) string { return fmt.Sprintf("\x1b[48;5;%dm", c) }

const (
	reset    = "\x1b[0m"
	bold     = "\x1b[1m"
	ellipsis = "··"
)

// noColor leaves a color at the terminal's default.
const noColor = -1

type style struct {
	fg, bg int
	bold   bool
}

// sgr resets and then sets s, with the foreground last so that a color is
// always directly followed by the text it colors.
func (s style) sgr() string {
	out := reset
	if s.bold {
		out += bold
	}
	if s.bg != noColor {
		out += bg(s.bg)
	}
	if s.fg != noColor {
		out += fg(s.fg)
	}
	return out
}

// headerLines is the number of lines below the rows: the key help, the info
// line, and the prompt.
const headerLines = 3

func (m Model) bodyHeight() int {
	return max(m.height-headerLines, 1)
}

func (m Model) pinnedCount() int {
	n := 0
	for _, r := range m.picker.Rows() {
		if r.Pinned {
			n++
		}
	}
	return n
}

// separator marks a body line that divides the pinned selections from the
// matches rather than showing a row.
const separator = -1

// layout lists, from the bottom of the list upward, the row index shown on
// each body line, and returns the cursor's line index, which can differ from
// its row index because of the separator.
func (m Model) layout() ([]int, int) {
	pinned := m.pinnedCount()
	var body []int
	cursorLine := 0
	for i := range m.picker.Rows() {
		if i == pinned && pinned > 0 {
			body = append(body, separator)
		}
		if i == m.picker.Cursor() {
			cursorLine = len(body)
		}
		body = append(body, i)
	}
	return body, cursorLine
}

// scrolled returns the first line to show so that the cursor line is within
// a window of the given height, moving the window as little as possible.
func scrolled(offset, cursorLine, height int) int {
	offset = min(offset, cursorLine)
	if cursorLine >= offset+height {
		offset = cursorLine - height + 1
	}
	return offset
}

// View implements tea.Model. Like fzf's default layout, the prompt is at the
// bottom and the list grows upward, best match first.
func (m Model) View() string {
	rows := m.picker.Rows()
	body, cursorLine := m.layout()
	height := m.bodyHeight()
	offset := scrolled(m.offset, cursorLine, height)
	end := min(offset+height, len(body))
	scrollbar := m.scrollbar(len(body), offset, height)

	lines := make([]string, 0, m.height)
	for range height - (end - offset) {
		lines = append(lines, "")
	}
	for i := end - 1; i >= offset; i-- {
		var line string
		if body[i] == separator {
			line = fg(colorBorder) + "  " + strings.Repeat("─", min(20, max(m.width-3, 0))) + reset
		} else {
			r := rows[body[i]]
			line = m.renderRow(r, body[i] == m.picker.Cursor())
		}
		if scrollbar[i-offset] {
			line = padTo(line, m.width-1) + fg(colorBorder) + "│" + reset
		}
		lines = append(lines, line)
	}

	header := fmt.Sprintf("kil: type to filter, Tab to select, Alt-A to select all shown, Ctrl-X to toggle exact match, Ctrl-R to refresh, Enter to send %s to selected/current line (Ctrl-C to abort)", m.label)
	lines = append(lines,
		"  "+fg(colorHeader)+cut(header, m.width-3)+reset,
		m.infoLine(len(rows)),
		bold+fg(colorPrompt)+">"+reset+fg(colorPrompt)+" "+reset+m.promptText(),
	)
	return strings.Join(lines, "\n")
}

// scrollbar returns, for each line of the window counted from the bottom,
// whether the scrollbar covers it. It is empty when everything fits.
func (m Model) scrollbar(total, offset, height int) []bool {
	bar := make([]bool, height)
	if total <= height {
		return bar
	}
	size := max(height*height/total, 1)
	start := offset * (height - size) / max(total-height, 1)
	for i := start; i < start+size && i < height; i++ {
		bar[i] = true
	}
	return bar
}

// promptText draws the query in bold with a reverse-video caret, standing in
// for the terminal cursor that fzf places there.
func (m Model) promptText() string {
	r := m.input.runes
	pos := m.input.pos
	caret := " "
	after := ""
	if pos < len(r) {
		caret = string(r[pos])
		after = string(r[pos+1:])
	}
	return bold + string(r[:pos]) + reset + "\x1b[7m" + caret + reset + bold + after + reset
}

func (m Model) infoLine(shown int) string {
	info := fmt.Sprintf("%d/%d (%d)", shown, m.total, m.picker.SelectedCount())
	if m.picker.Exact() {
		info += " [exact]"
	}
	// As in fzf, the rule stops one column short of the edge.
	rule := max(m.width-2-widths.StringWidth(info)-2, 0)
	return "  " + fg(colorInfo) + info + reset + " " + fg(colorBorder) + strings.Repeat("─", rule) + reset
}

// renderRow draws one row as fzf does: a gutter, a selection marker, and the
// text with matched characters highlighted, shifted so that the last match
// is visible when the line is wider than the screen.
func (m Model) renderRow(r picker.Row, focused bool) string {
	gutter := fg(colorGutter) + "▌" + reset
	marker := " "
	if r.Selected {
		marker = fg(colorMarker) + "┃" + reset
	}
	base := style{fg: noColor, bg: noColor}
	if focused {
		gutter = bold + bg(colorCursorBg) + fg(colorPointer) + "▌" + reset
		marker = bg(colorCursorBg) + " " + reset
		if r.Selected {
			marker = bg(colorCursorBg) + fg(colorMarker) + "┃" + reset
		}
		base = style{fg: colorCursorFg, bg: colorCursorBg, bold: true}
	}

	text := []rune(r.Item.Text)
	styles := make([]style, len(text))
	for i := range styles {
		styles[i] = base
	}
	for i, c := range columnColors(text) {
		if c != noColor {
			styles[i].fg = c
		}
	}
	positions := m.picker.Query().Positions(r.Item.Text)
	for _, p := range positions {
		if p < len(styles) {
			styles[p].fg = colorMatch
			if focused {
				styles[p].fg = colorMatchFocused
			}
		}
	}

	width := max(m.width-3, 1) // gutter, marker, and the scrollbar column
	matchEnd := 0
	if len(positions) > 0 {
		matchEnd = positions[len(positions)-1] + 1
	}
	from, to, cutLeft, cutRight := visibleRange(text, matchEnd, width)
	var b strings.Builder
	b.WriteString(gutter + marker)
	if cutLeft {
		b.WriteString(base.sgr() + ellipsis)
	}
	var current *style
	for i := from; i < to; i++ {
		if current == nil || *current != styles[i] {
			s := styles[i]
			current = &s
			b.WriteString(s.sgr())
		}
		b.WriteRune(text[i])
	}
	if cutRight {
		b.WriteString(base.sgr() + ellipsis)
	}
	b.WriteString(reset)
	return b.String()
}

// hscrollOff is fzf's default --hscroll-off: the columns kept visible after
// the last match when a long line is shifted left.
const hscrollOff = 10

// visibleRange returns the runes of text to draw within width columns, and
// whether ellipses replace the parts cut on the left and right. It follows
// fzf's printHighlighted: when the last match, plus hscrollOff columns,
// would be off screen, the line is shifted left until it fits.
func visibleRange(text []rune, matchEnd, width int) (from, to int, cutLeft, cutRight bool) {
	if runesWidth(text) <= width {
		return 0, len(text), false, false
	}
	ew := widths.StringWidth(ellipsis)
	maxe := min(max(matchEnd+min(width/2-ew, hscrollOff), 0), len(text))
	if runesWidth(text[:maxe]) <= width-ew {
		// "Stri··": the matches fit, so cut only on the right.
		used := 0
		for to < len(text) && used+widths.RuneWidth(text[to]) <= width-ew {
			used += widths.RuneWidth(text[to])
			to++
		}
		return 0, to, false, true
	}
	to = len(text)
	if runesWidth(text[maxe:]) > ew {
		to, cutRight = maxe, true
	}
	// "··ri··": drop runes on the left until the rest and the ellipses fit.
	used := runesWidth(text[:to]) + ew
	if cutRight {
		used += ew
	}
	for from < to && used > width {
		used -= widths.RuneWidth(text[from])
		from++
	}
	return from, to, true, cutRight
}

func runesWidth(rs []rune) int {
	return widths.StringWidth(string(rs))
}

// columnColors colors the PID, PPID, and USER columns, the first three
// whitespace-separated fields of a ps line.
func columnColors(text []rune) []int {
	colors := make([]int, len(text))
	for i := range colors {
		colors[i] = noColor
	}
	palette := []int{colorPID, colorPPID, colorUser}
	field, inField := -1, false
	for i, r := range text {
		space := r == ' ' || r == '\t'
		if !space && !inField {
			field++
		}
		inField = !space
		if field >= len(palette) {
			break
		}
		if inField {
			colors[i] = palette[field]
		}
	}
	return colors
}

// cut truncates s to width columns, ending it with an ellipsis if cut.
func cut(s string, width int) string {
	if widths.StringWidth(s) <= width {
		return s
	}
	return widths.Truncate(s, max(width, 0), ellipsis)
}

// padTo pads a styled line with spaces to width visible columns.
func padTo(line string, width int) string {
	visible := widths.StringWidth(stripSGR(line))
	return line + strings.Repeat(" ", max(width-visible, 0))
}

// stripSGR removes the color sequences this file emits.
func stripSGR(s string) string {
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		if s[i] == 0x1b {
			j := strings.IndexByte(s[i:], 'm')
			if j >= 0 {
				i += j
				continue
			}
		}
		b.WriteByte(s[i])
	}
	return b.String()
}
