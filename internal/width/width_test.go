package width

import (
	"math/rand/v2"
	"strings"
	"testing"
	"time"

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

// Reveal marks each character the terminal would not show: a cluster of no
// width, and a default-ignorable character joined to the character before
// it, as a ZWNJ, a ZWJ, a variation selector, or a tag character after a
// letter, so that "zz<U+200C>q" does not look like "zzq". It keeps those
// that change how their cluster is drawn: the ZWJ of an emoji sequence, a
// variation selector on an emoji, a keycap, or an ideograph, and the tags of
// a flag. A combining mark on a letter shows, so it is kept.
func TestReveal(t *testing.T) {
	tests := []struct{ in, want string }{
		{"zz\u200bq", "zz?q"},
		{"zz\u200cq", "zz?q"},
		{"zz\u200dq", "zz?q"},
		{"zz\u200d\u0301q", "zz?\u0301q"},
		{"zz\u034fq", "zz?q"},
		{"z\ufe0fq", "z?q"},
		{"z\U000E0041\U000E0042q", "z??q"},
		{"a\u2060b \ufeffc d\u00ade", "a?b ?c d?e"},
		{"\u3164x \u115fy", "?x ?y"},
		{"\u0301\u0302x", "?x"},
		{"\u200b\u200b", "??"},
		{"e\u0301 \u0301x", "e\u0301 \u0301x"},
		{"\U0001F468\u200D\U0001F469", "\U0001F468\u200D\U0001F469"},
		{"\u2764\uFE0F \U0001F600\uFE0E", "\u2764\uFE0F \U0001F600\uFE0E"},
		{"1\uFE0F\u20E3", "1\uFE0F\u20E3"},
		{"\u845B\U000E0100 \u8FBB\uFE00", "\u845B\U000E0100 \u8FBB\uFE00"},
		{"\U0001F3F4\U000E0067\U000E0062\U000E0065\U000E006E\U000E0067\U000E007F", "\U0001F3F4\U000E0067\U000E0062\U000E0065\U000E006E\U000E0067\U000E007F"},
		{"\u0600x", "\u0600x"},
		{"plain ascii", "plain ascii"},
		// Only a flag's tag sequence shows; other tags after U+1F3F4 hide
		// ASCII text in the flag's cell.
		{"\U0001F3F4" + tags("rm -rf ~"), "\U0001F3F4????????"},
		{"\U0001F3F4" + tags("evil") + "\U000E007F", "\U0001F3F4?????"},
		{"\U0001F3F4" + tags("gbsct") + "\U000E007F", "\U0001F3F4" + tags("gbsct") + "\U000E007F"},
		// A variation selector shows only right after what it varies.
		{"\U0001F600\uFE0F\uFE0F\uFE0F", "\U0001F600\uFE0F??"},
		{"\u845B\U000E0100\U000E0101", "\u845B\U000E0100?"},
		{"\u2764\u034F\uFE0F", "\u2764??"},
		{"\U0001F441\uFE0F\u200D\U0001F5E8\uFE0F", "\U0001F441\uFE0F\u200D\U0001F5E8\uFE0F"},
		// Emoji take only VS15 and VS16, and ideographs only the variation
		// selectors of their standardized and ideographic variants.
		{"\U0001F600\uFE00", "\U0001F600?"},
		{"1\uFE00\u20E3", "1?\u20E3"},
		// A keycap's selector shows only between its base and U+20E3: any
		// other varies nothing, and the one before it, kept, would no longer
		// share a cluster with U+20E3 once that one is shown as "?".
		{"1\uFE0F\uFE0F\u20E3", "1??\u20E3"},
		{"1\uFE0E\uFE0F\u20E3", "1??\u20E3"},
		{"#\uFE0F\u20E3\uFE0F", "#\uFE0F\u20E3?"},
		{"\U0001F600\U000E0100", "\U0001F600?"},
		{"\u845B\uFE0F", "\u845B?"},
		// A ZWJ after a mark joins nothing that shows, so it is marked too.
		{"\U0001F600\u200C\u200D\U0001F600", "\U0001F600??\U0001F600"},
		{"\u00A9\U000E0041\u200D\u00A9", "\u00A9??\u00A9"},
		{"\U0001F3F4" + tags("gbsct") + "\U000E007F\u200D\U0001F600", "\U0001F3F4???????\U0001F600"},
		// An emoji after the mark starts a pair that shows as it did.
		{"\U0001F600\u200C\u200D\U0001F600\u200D\U0001F600", "\U0001F600??\U0001F600\u200D\U0001F600"},
		{"\U0001F600\u200C\u0301\u200D\U0001F600", "\U0001F600?\u0301?\U0001F600"},
		// Mongolian variation selectors vary no ideograph.
		{"\u845B\u180B \u845B\u180C \u845B\u180D \u845B\u180F", "\u845B? \u845B? \u845B? \u845B?"},
		// Marks at the start that only a spacing mark after an invisible
		// character gave a width have nothing to combine with once that
		// character is a "?", so they are one too, as without it.
		{"\u0301\u200C\u0E33", "??\u0E33"},
		{"\u0301\u0302\u200C\u093F", "??\u093F"},
		{"\u20E3\uFE0F\u0E33", "??\u0E33"},
		{"\U0001F3FB\u200D\u0E33", "??\u0E33"},
	}
	for _, tt := range tests {
		got := Reveal(tt.in, "?")
		if got != tt.want {
			t.Errorf("Reveal(%+q) = %+q, want %+q", tt.in, got, tt.want)
		}
		// What Reveal returns has nothing left to reveal.
		if again := Reveal(got, "?"); again != got {
			t.Errorf("Reveal(Reveal(%+q)) = %+q, want %+q", tt.in, again, got)
		}
	}
}

// What Reveal returns has nothing left to reveal and no cluster of no width,
// whatever the characters a ps line field starts with or holds.
func TestRevealIsIdempotent(t *testing.T) {
	runes := []rune{
		'a', '1', '#', ' ', '\u0301', '\u0302', '\u093F', '\u0E33', '\u200B', '\u200C', '\u200D',
		'\u20E3', '\uFE0E', '\uFE0F', '\uFE00', 0xE0100, 0xE0041, 0xE007F, '\u2764', 0x1F600,
		0x1F3FB, 0x1F3F4, 0x1F1EF, 0x1F1F5, '\u845B', '\u1100', '\u1161', '\u11A8', '\u115F',
		'\u3164', '\u00AD', '\u0600', '\u034F', '\u2060', '\u0915', '\u094D', '\u180B',
	}
	rnd := rand.New(rand.NewPCG(1, 2))
	for range 300000 {
		var b strings.Builder
		for range 1 + rnd.IntN(6) {
			b.WriteRune(runes[rnd.IntN(len(runes))])
		}
		for _, in := range []string{b.String(), "pid " + b.String()} {
			got := Reveal(in, "?")
			if again := Reveal(got, "?"); again != got {
				t.Fatalf("Reveal(Reveal(%+q)) = %+q, want %+q", in, again, got)
			}
			rest, state := got, -1
			for rest != "" {
				var c string
				var w int
				c, rest, w, state = uniseg.FirstGraphemeClusterInString(rest, state)
				if w == 0 {
					t.Fatalf("Reveal(%+q) = %+q, which has the cluster %+q of no width", in, got, c)
				}
			}
		}
	}
}

// tags returns s as Unicode tag characters, which draw as nothing.
func tags(s string) string {
	var b strings.Builder
	for _, r := range s {
		b.WriteRune(0xE0000 + r)
	}
	return b.String()
}

// A cluster of many variation selectors, which an argument of 128 KiB can
// hold, takes time in proportion to its length, so that a process cannot
// keep kil from listing processes for seconds.
func TestRevealIsLinearInLongClusters(t *testing.T) {
	long := "1" + strings.Repeat("\uFE0F", 200000) + "\u20E3"
	start := time.Now()
	Reveal(long, "?")
	if d := time.Since(start); d > 3*time.Second {
		t.Errorf("Reveal of a cluster of 200000 variation selectors took %v", d)
	}
}

// Reveal runs on every field of every ps line at each listing and on the
// prompt at each frame, so a line of printable ASCII, the common case, costs
// no allocation, as csvless keeps such strings.
func TestRevealLeavesPrintableASCIIAlone(t *testing.T) {
	line := "/usr/bin/python3 -m http.server 8000 --bind 127.0.0.1 --directory /srv/www"
	if got := Reveal(line, "?"); got != line {
		t.Fatalf("Reveal(%q) = %q", line, got)
	}
	if n := testing.AllocsPerRun(100, func() { _ = Reveal(line, "?") }); n != 0 {
		t.Errorf("Reveal of printable ASCII allocates %v times, want 0", n)
	}
}
