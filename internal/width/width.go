// Package width measures text as the terminal draws it. Characters of
// ambiguous East Asian width, such as ·, ─, │, and ▌, take one column on
// most terminals and two on some set up for East Asian text, which say so
// with RUNEWIDTH_EASTASIAN=1, as fzf and go-runewidth read it. kil lays
// rows out and tcell draws them with the same width, or the columns no
// longer line up, so this package measures with uniseg, as tcell does, and
// keeps uniseg's setting for ambiguous widths.
package width

import "github.com/rivo/uniseg"

// SetAmbiguousWide makes ambiguous-width characters take two columns, or
// one. kil sets it once, before it lists or draws anything.
func SetAmbiguousWide(wide bool) {
	uniseg.EastAsianAmbiguousWidth = 1
	if wide {
		uniseg.EastAsianAmbiguousWidth = 2
	}
}

// Ambiguous returns the columns an ambiguous-width character takes.
func Ambiguous() int { return uniseg.EastAsianAmbiguousWidth }

// String returns the columns s takes on the terminal.
func String(s string) int { return uniseg.StringWidth(s) }

// Rune returns the columns r takes on the terminal on its own.
func Rune(r rune) int { return uniseg.StringWidth(string(r)) }

// Truncate cuts s, between grapheme clusters, to at most width columns,
// ending it with tail if it is cut. A tail that does not fit is left out.
func Truncate(s string, width int, tail string) string {
	if String(s) <= width {
		return s
	}
	if String(tail) > width {
		tail = ""
	}
	room := width - String(tail)
	var out []byte
	used := 0
	state := -1
	for s != "" {
		var cluster string
		var w int
		cluster, s, w, state = uniseg.FirstGraphemeClusterInString(s, state)
		if used+w > room {
			break
		}
		out = append(out, cluster...)
		used += w
	}
	return string(out) + tail
}

// FromEnv reports whether getenv says the terminal draws ambiguous-width
// characters wide: RUNEWIDTH_EASTASIAN=1, as go-runewidth reads it. kil
// does not guess from the locale, as many terminals in East Asian locales
// draw them narrow.
func FromEnv(getenv func(string) string) bool {
	return getenv("RUNEWIDTH_EASTASIAN") == "1"
}
