package width

import (
	"testing"

	"github.com/rivo/uniseg"
)

// Ambiguous-width characters, such as ·, count as one column unless the
// terminal draws them wide, as RUNEWIDTH_EASTASIAN=1 says.
func TestAmbiguousWide(t *testing.T) {
	t.Cleanup(func() { SetAmbiguousWide(false) })
	if got := String("·"); got != 1 || Ambiguous() != 1 {
		t.Errorf("default: width of · = %d, Ambiguous() = %d; want 1 and 1", got, Ambiguous())
	}
	SetAmbiguousWide(true)
	if got := String("·"); got != 2 || Ambiguous() != 2 {
		t.Errorf("wide: width of · = %d, Ambiguous() = %d; want 2 and 2", got, Ambiguous())
	}
}

// RUNEWIDTH_EASTASIAN=1, which fzf, tcell, and go-runewidth read, makes
// ambiguous-width characters wide; any other value does not.
func TestFromEnv(t *testing.T) {
	for v, want := range map[string]bool{"1": true, "": false, "0": false, "yes": false} {
		if got := FromEnv(func(string) string { return v }); got != want {
			t.Errorf("FromEnv(RUNEWIDTH_EASTASIAN=%q) = %v, want %v", v, got, want)
		}
	}
}

// String and Rune count as tcell draws, so that ® and ★, which some
// libraries count apart, take the same columns in the layout as on screen.
func TestCountsAsTcellDraws(t *testing.T) {
	t.Cleanup(func() { SetAmbiguousWide(false) })
	for _, wide := range []bool{false, true} {
		SetAmbiguousWide(wide)
		for _, s := range []string{"\u00ae", "\u2605", "\u00b7", "a", "\u65e5", "\U0001F468\u200D\U0001F469"} {
			if got, want := String(s), uniseg.StringWidth(s); got != want {
				t.Errorf("wide %v: String(%q) = %d, tcell draws %d", wide, s, got, want)
			}
		}
	}
}
