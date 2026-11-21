// Package width measures text as the terminal draws it. Characters of
// ambiguous East Asian width, such as ·, ─, │, and ▌, take one column on
// most terminals and two on some set up for East Asian text, which say so
// with RUNEWIDTH_EASTASIAN=1, as fzf and go-runewidth read it. kil lays
// rows out and tcell draws them with the same width, or the columns no
// longer line up, so this package measures with uniseg, as tcell does, and
// keeps uniseg's setting for ambiguous widths.
package width

import (
	"slices"
	"strings"
	"unicode"

	"github.com/rivo/uniseg"
)

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

// Reveal returns s with mark in place of what the terminal would not show,
// so that text with it does not look like text without it: each grapheme
// cluster of no width, as U+200B ZERO WIDTH SPACE, and each default-ignorable
// character inside a cluster, as U+200C ZERO WIDTH NON-JOINER or a tag
// character after a letter, unless it changes how its cluster is drawn, as
// UTS #55 asks of what shows such characters.
func Reveal(s, mark string) string {
	if printableASCII(s) {
		return s
	}
	var b strings.Builder
	RevealEach(s, func(text string, marked bool) {
		if marked {
			text = mark
		}
		b.WriteString(text)
	})
	return b.String()
}

// RevealEach calls each with the pieces of s in order, as Reveal lays them
// out: text that shows, and, with marked set, what Reveal marks.
func RevealEach(s string, each func(text string, marked bool)) {
	state := -1
	for s != "" {
		var cluster string
		var w int
		cluster, s, w, state = uniseg.FirstGraphemeClusterInString(s, state)
		if w == 0 {
			each(cluster, true)
			continue
		}
		rs := []rune(cluster)
		keycap := slices.Contains(rs, '\u20E3')
		flag := flagTags(rs)
		marked, from := false, 0
		for i, r := range rs {
			// A ZWJ after any mark in its cluster is marked too: what it
			// joins to the emoji after it no longer shows as it did.
			if ignorable(r) && (marked && r == '\u200D' || !drawsWith(rs, i, keycap, flag)) {
				if from < i {
					each(string(rs[from:i]), false)
				}
				each(string(r), true)
				marked, from = true, i+1
			}
		}
		if from < len(rs) {
			each(string(rs[from:]), false)
		}
	}
}

// printableASCII reports whether s has only printable ASCII, which the
// terminal shows as it is, as most ps lines and queries have.
func printableASCII(s string) bool {
	for i := 0; i < len(s); i++ {
		if s[i] < 0x20 || s[i] > 0x7e {
			return false
		}
	}
	return true
}

// ignorable reports whether r is a default-ignorable code point, which a
// terminal draws as nothing. Prepended concatenation marks, as U+0600, are
// format characters that show.
func ignorable(r rune) bool {
	return (unicode.Is(unicode.Cf, r) && !unicode.Is(unicode.Prepended_Concatenation_Mark, r)) ||
		unicode.Is(unicode.Other_Default_Ignorable_Code_Point, r) ||
		unicode.Is(unicode.Variation_Selector, r)
}

// drawsWith reports whether the ignorable rs[i] changes how its cluster rs is
// drawn: the ZWJ between two emoji, VS15 or VS16 right after an emoji or a
// keycap base, a variation selector of an ideographic variant right after an
// ideograph, and the tag characters of a flag.
// keycap says rs ends a keycap, and flag that rs is a flag's tag sequence,
// which Reveal finds once for the cluster rather than at each character.
func drawsWith(rs []rune, i int, keycap, flag bool) bool {
	r := rs[i]
	switch {
	case r == '\u200D':
		return i > 0 && i+1 < len(rs) && pictographic(rs[i+1])
	case unicode.Is(unicode.Variation_Selector, r):
		if i == 0 {
			return false
		}
		// Emoji take only VS15 and VS16, and ideographs only the
		// selectors of their standardized and ideographic variants.
		prev := rs[i-1]
		if r == '\uFE0E' || r == '\uFE0F' {
			return pictographic(prev) || (keycap && strings.ContainsRune("0123456789#*", prev))
		}
		return unicode.Is(unicode.Ideographic, prev) && (r <= '\uFE02' || r >= 0xE0100)
	case r >= 0xE0020 && r <= 0xE007F:
		return flag
	}
	return false
}

// flagTags lists the flags that tag characters make, as U+1F3F4 followed by
// "gbeng" in tags and a cancel tag: Unicode recommends only these three, and
// any other tags after U+1F3F4 draw as the black flag alone, hiding text.
var flagTags = func() func([]rune) bool {
	flags := map[string]bool{}
	for _, name := range []string{"gbeng", "gbsct", "gbwls"} {
		f := "\U0001F3F4"
		for _, c := range name {
			f += string(0xE0000 + c)
		}
		flags[f+"\U000E007F"] = true
	}
	return func(rs []rune) bool { return flags[string(rs)] }
}()

// pictographic reports whether r is Extended_Pictographic, which uniseg
// knows but does not export: only such a character joins another emoji
// after a ZWJ into one cluster.
func pictographic(r rune) bool {
	return uniseg.GraphemeClusterCount(string(r)+"\u200D\U0001F466") == 1
}
