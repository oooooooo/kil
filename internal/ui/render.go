package ui

import (
	"fmt"
	"strings"
	"unicode"

	"github.com/mattn/go-runewidth"
	"github.com/rivo/uniseg"

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
// line, and the prompt. The view never has more lines than the terminal, so
// a short terminal drops the help and then the info line to keep a row on
// screen.
func (m Model) headerLines() int {
	return max(min(3, m.height-1), 1)
}

// bodyHeight is the number of lines for rows. It is 0 when the terminal has
// room for the prompt only.
func (m Model) bodyHeight() int {
	return max(m.height-m.headerLines(), 0)
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
// a window of the given height, moving the window as little as possible. The
// window never extends past the last of the total lines, so lines hidden
// below it come back when the window grows. Without room for lines, the
// window stays at the top, as there is no line to keep in it.
func scrolled(offset, cursorLine, height, total int) int {
	if height <= 0 {
		return 0
	}
	offset = min(offset, cursorLine, max(total-height, 0))
	if cursorLine >= offset+height {
		offset = cursorLine - height + 1
	}
	return offset
}

// shownRows returns the range of row indices drawn on screen, from up to
// to. Alt-A selects only these, so that Enter never signals a process the
// user cannot see.
func (m Model) shownRows() (from, to int) {
	body, _ := m.layout()
	from, to = len(body), 0
	for _, i := range body[min(m.offset, len(body)):min(m.offset+m.bodyHeight(), len(body))] {
		if i != separator {
			from, to = min(from, i), max(to, i+1)
		}
	}
	return min(from, to), to
}

// render draws the screen. Like fzf's default layout, the prompt is at the
// bottom and the list grows upward, best match first.
func (m Model) render() string {
	rows := m.picker.Rows()
	body, cursorLine := m.layout()
	height := m.bodyHeight()
	offset := scrolled(m.offset, cursorLine, height, len(body))
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

	// A narrow terminal cut a full key list before the keys that matter, so
	// the header names only those, tersely, and Alt-H shows the rest. Enter
	// names its signal, which KIL_SIGNAL can change.
	header := "Tab select, Alt-A all, Enter " + m.label + ", Alt-H help"
	footer := []string{
		"  " + fg(colorHeader) + cut(header, m.width-3) + reset,
		m.infoLine(len(rows)),
		bold + fg(colorPrompt) + ">" + reset + fg(colorPrompt) + " " + reset + m.promptText(),
	}
	if m.confirming {
		// Enter answers the question, so a narrow terminal cuts the
		// explanation rather than the answers.
		question := "Quit? [Y/n]"
		why := fmt.Sprintf("%d selected processes were not signalled.", m.picker.SelectedCount())
		switch room := m.width - 2 - widths.StringWidth(question); {
		case room >= 10:
			question = cut(why, room) + " " + question
		case room < -1:
			question = "[Y/n]"
		}
		footer[2] = bold + fg(colorPointer) + question + reset
	}
	footer = footer[len(footer)-m.headerLines():]
	if m.help {
		// The help lists every key, so it takes the header's line too
		// rather than repeating the header's short list below it.
		helpHeight := height
		if len(footer) == 3 {
			footer = footer[1:]
			helpHeight++
		}
		lines = m.helpLines(helpHeight)
	}
	lines = append(lines, footer...)
	return strings.Join(lines, "\n")
}

// helpLines draws the full key list and then the query syntax in the height
// lines of the rows, aligned to the bottom as the rows are. On a short
// terminal the query syntax is cut first, keeping the keys.
func (m Model) helpLines(height int) []string {
	entries := [][2]string{
		{"Keys", ""},
		{"Tab / Shift-Tab", "select or deselect, then move down / up"},
		{"Alt-A", "select all shown, or deselect them"},
		{"Enter", "send " + m.label + " to the selected lines, or the current line"},
		{"Up / Down", "move (also Ctrl-P / Ctrl-N)"},
		{"Ctrl-X", "toggle exact match"},
		{"Ctrl-R", "refresh the process list"},
		{"Esc / Ctrl-C", "abort without sending anything"},
		{"Ctrl-Z", "suspend kil as a shell job (fg resumes it)"},
		{"any key", "close this help (Ctrl-C aborts)"},
		// The query syntax is fzf's, which users who do not use fzf
		// cannot guess. The blank line and the headings set it apart from
		// the keys, which are drawn the same way.
		{"", ""},
		{"Query", "matches lines that"},
		{"node", "contain n, o, d, e in this order (fuzzy)"},
		{"'node", "contain node (fuzzy in exact mode)"},
		{"'node'", "contain node as a whole word"},
		{"^node / .js$", "start with node / end with .js"},
		{"!node", "do not contain node"},
		{"a b", "match a and b"},
		{"a | b", "match a or b"},
		{"Node", "match case sensitive (has an uppercase letter)"},
	}
	entries = entries[:min(len(entries), height)]
	lines := make([]string, height-len(entries), height)
	for _, e := range entries {
		switch e[0] {
		case "":
			lines = append(lines, "")
		case "Keys", "Query":
			line := strings.TrimRight(fmt.Sprintf("%-17s %s", e[0], e[1]), " ")
			lines = append(lines, "  "+bold+fg(colorHeader)+cut(line, m.width-3)+reset)
		default:
			line := fmt.Sprintf("  %-15s %s", e[0], e[1])
			lines = append(lines, "  "+fg(colorHeader)+cut(line, m.width-3)+reset)
		}
	}
	return lines
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
// for the terminal cursor that fzf places there. A query wider than the
// screen scrolls, as in a shell, so that the caret stays on screen. Text is
// kept or cut by grapheme cluster, so an emoji joined with ZWJ is never split.
func (m Model) promptText() string {
	r := m.input.runes
	pos := m.input.pos
	caret := " "
	if pos < len(r) {
		caret = string(r[pos])
	}
	room := max(m.width-2, 1) - widths.StringWidth(caret) // after "> "
	before := lastClusters(string(r[:pos]), room)
	after := ""
	if pos < len(r) {
		after = firstClusters(string(r[pos+1:]), room-widths.StringWidth(before))
	}
	return bold + before + reset + "\x1b[7m" + caret + reset + bold + after + reset
}

// firstClusters returns the longest prefix of s, cut between grapheme
// clusters, that fits within width columns.
func firstClusters(s string, width int) string {
	end, used := 0, 0
	g := uniseg.NewGraphemes(s)
	for g.Next() {
		w := widths.StringWidth(g.Str())
		if used+w > width {
			break
		}
		used += w
		_, end = g.Positions()
	}
	return s[:end]
}

// lastClusters returns the longest suffix of s, cut between grapheme
// clusters, that fits within width columns.
func lastClusters(s string, width int) string {
	var starts []int
	var ws []int
	g := uniseg.NewGraphemes(s)
	for g.Next() {
		from, _ := g.Positions()
		starts = append(starts, from)
		ws = append(ws, widths.StringWidth(g.Str()))
	}
	start, used := len(s), 0
	for i := len(ws) - 1; i >= 0 && used+ws[i] <= width; i-- {
		used += ws[i]
		start = starts[i]
	}
	return s[start:]
}

func (m Model) infoLine(shown int) string {
	info := fmt.Sprintf("%d/%d (%d)", shown, m.picker.Total(), m.picker.SelectedCount())
	// An ignored Enter comes first, where a narrow terminal still shows it.
	// In the help and the question, Enter closes or answers instead.
	if m.enterHeld && !m.help && !m.confirming {
		reason := "rows just shown"
		if m.reloading {
			reason = "Ctrl-R running"
		}
		info = "Enter ignored: " + reason + "  " + info
	}
	if m.picker.Exact() {
		info += " [exact]"
	}
	if m.sent > 0 {
		info += fmt.Sprintf(" sent %d, %d selected left: Enter to send", m.sent, m.picker.SelectedCount())
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

	// A control character, bidirectional control, or line separator is
	// drawn as "?", one for one so that match positions still line up, so
	// that a row can never carry an escape sequence, which kil's drawing
	// would read as its own color, nor reorder or break on screen.
	text := []rune(r.Item.Text)
	for i, c := range text {
		if unsafeOnScreen(c) {
			text[i] = '?'
		}
	}
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

	// The PID, PPID, USER, and COMM columns never scroll away, as with fzf
	// --freeze-left=4: without them the user cannot tell which process a
	// row is. Only the command line after them scrolls to show the match.
	frozen := min(r.Item.Frozen, len(text))
	frozenWidth := runesWidth(text[:frozen])
	if frozenWidth+widths.StringWidth(ellipsis) >= width {
		// No room to scroll: cut the whole line on the right.
		frozen, frozenWidth, matchEnd = 0, 0, 0
	}
	from, to, cutLeft, cutRight := visibleRange(text[frozen:], max(matchEnd-frozen, 0), width-frozenWidth)
	from += frozen
	to += frozen

	var b strings.Builder
	b.WriteString(gutter + marker)
	var current *style
	write := func(lo, hi int) {
		for i := lo; i < hi; i++ {
			if current == nil || *current != styles[i] {
				s := styles[i]
				current = &s
				b.WriteString(s.sgr())
			}
			b.WriteRune(text[i])
		}
	}
	write(0, frozen)
	if cutLeft {
		b.WriteString(base.sgr() + ellipsis)
		current = nil
	}
	write(from, to)
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

// unsafeOnScreen reports whether r, written to a terminal, could do more
// than show a character. internal/proc cleans ps lines with the same rule.
func unsafeOnScreen(r rune) bool {
	return unicode.IsControl(r) || unicode.Is(unicode.Bidi_Control, r) || r == '\u2028' || r == '\u2029'
}
