package render

import (
	"sort"
	"unicode"
	"unicode/utf8"
)

// Terminal cell widths for the characters gitling prints.
//
// Panel content is not all gitling's own glyph set: author names, commit
// subjects, and file paths come from the repository and may hold anything. East
// Asian characters and emoji occupy two terminal cells, and combining marks
// occupy none, so counting runes misaligns every box border on a row that
// carries them.
//
// runeWidth implements the Unicode East Asian Width property (UAX #11) for the
// Wide and Fullwidth classes only. Ambiguous-width characters — U+00B7 MIDDLE
// DOT among them, which the heatmap prints — are treated as single-width, which
// is what a terminal in a non-CJK locale does and what gitling has always
// assumed.
//
// Known limitation: width is measured per rune, not per grapheme cluster. A ZWJ
// emoji sequence (👨‍👩‍👧) measures as the sum of its parts where a terminal draws
// it in two cells. Base characters plus combining marks, variation selectors,
// and emoji skin-tone modifiers are handled, since those parts measure zero;
// only multi-emoji ZWJ sequences overcount. They are rare in commit metadata and
// the failure is a too-narrow row rather than a broken border.

// zeroWidthExtra holds zero-width characters outside the Unicode nonspacing
// categories that isZeroWidth already covers.
var zeroWidthExtra = [][2]rune{
	{0x1160, 0x11FF},   // Hangul conjoining jungseong + jongseong (category Lo)
	{0x1F3FB, 0x1F3FF}, // emoji skin-tone modifiers (category Sk), composed onto the base
}

// isZeroWidth reports whether r occupies no cell. The nonspacing marks (Mn),
// enclosing marks (Me), and format characters (Cf — zero-width space, ZWJ,
// variation selectors, bidi controls) come from the stdlib's Unicode tables
// rather than a hand-maintained list, so scripts gitling never anticipated are
// measured correctly and the data stays current with the Go release.
func isZeroWidth(r rune) bool {
	return unicode.In(r, unicode.Mn, unicode.Me, unicode.Cf) || inRanges(zeroWidthExtra, r)
}

// wide holds characters that occupy two cells: East Asian Width Wide (W) and
// Fullwidth (F), which together cover CJK, Hangul syllables, fullwidth forms,
// and the emoji blocks.
var wide = [][2]rune{
	{0x1100, 0x115F},   // Hangul conjoining choseong
	{0x231A, 0x231B},   // watch, hourglass
	{0x2329, 0x232A},   // angle brackets
	{0x23E9, 0x23EC},   // fast-forward arrows
	{0x23F0, 0x23F0},   // alarm clock
	{0x23F3, 0x23F3},   // hourglass flowing sand
	{0x25FD, 0x25FE},   // medium small squares
	{0x2614, 0x2615},   // umbrella, hot beverage
	{0x2648, 0x2653},   // zodiac
	{0x267F, 0x267F},   // wheelchair
	{0x2693, 0x2693},   // anchor
	{0x26A1, 0x26A1},   // high voltage
	{0x26AA, 0x26AB},   // circles
	{0x26BD, 0x26BE},   // soccer, baseball
	{0x26C4, 0x26C5},   // snowman, sun behind cloud
	{0x26CE, 0x26CE},   // ophiuchus
	{0x26D4, 0x26D4},   // no entry
	{0x26EA, 0x26EA},   // church
	{0x26F2, 0x26F3},   // fountain, golf
	{0x26F5, 0x26F5},   // sailboat
	{0x26FA, 0x26FA},   // tent
	{0x26FD, 0x26FD},   // fuel pump
	{0x2705, 0x2705},   // check mark button
	{0x270A, 0x270B},   // raised fist, raised hand
	{0x2728, 0x2728},   // sparkles
	{0x274C, 0x274C},   // cross mark
	{0x274E, 0x274E},   // cross mark button
	{0x2753, 0x2755},   // question/exclamation marks
	{0x2757, 0x2757},   // exclamation mark
	{0x2795, 0x2797},   // plus, minus, divide
	{0x27B0, 0x27B0},   // curly loop
	{0x27BF, 0x27BF},   // double curly loop
	{0x2B1B, 0x2B1C},   // large squares
	{0x2B50, 0x2B50},   // star
	{0x2B55, 0x2B55},   // hollow red circle
	{0x2E80, 0x2E99},   // CJK radicals supplement
	{0x2E9B, 0x2EF3},   // CJK radicals
	{0x2F00, 0x2FD5},   // Kangxi radicals
	{0x2FF0, 0x2FFB},   // ideographic description
	{0x3000, 0x303E},   // CJK symbols and punctuation
	{0x3041, 0x3096},   // hiragana
	{0x3099, 0x30FF},   // katakana
	{0x3105, 0x312F},   // bopomofo
	{0x3131, 0x318E},   // Hangul compatibility jamo
	{0x3190, 0x31E3},   // kanbun, bopomofo extended
	{0x31F0, 0x321E},   // katakana phonetic extensions
	{0x3220, 0x3247},   // enclosed CJK
	{0x3250, 0x4DBF},   // enclosed CJK, CJK ext A
	{0x4E00, 0xA48C},   // CJK unified ideographs, Yi
	{0xA490, 0xA4C6},   // Yi radicals
	{0xA960, 0xA97C},   // Hangul jamo extended-A
	{0xAC00, 0xD7A3},   // Hangul syllables
	{0xF900, 0xFAFF},   // CJK compatibility ideographs
	{0xFE10, 0xFE19},   // vertical forms
	{0xFE30, 0xFE52},   // CJK compatibility forms
	{0xFE54, 0xFE66},   // small form variants
	{0xFE68, 0xFE6B},   // small form variants
	{0xFF01, 0xFF60},   // fullwidth forms
	{0xFFE0, 0xFFE6},   // fullwidth signs
	{0x16FE0, 0x16FE4}, // Tangut/Nushu marks
	{0x16FF0, 0x16FF1}, // Vietnamese reading marks
	{0x17000, 0x187F7}, // Tangut
	{0x18800, 0x18CD5}, // Tangut components
	{0x18D00, 0x18D08}, // Tangut supplement
	{0x1B000, 0x1B152}, // kana supplement
	{0x1B164, 0x1B167}, // small kana extension
	{0x1B170, 0x1B2FB}, // Nushu
	{0x1F004, 0x1F004}, // mahjong red dragon
	{0x1F0CF, 0x1F0CF}, // joker
	{0x1F18E, 0x1F18E}, // AB button
	{0x1F191, 0x1F19A}, // squared latin
	{0x1F200, 0x1F320}, // enclosed ideographic, weather, emoji
	{0x1F32D, 0x1F335}, // food emoji
	{0x1F337, 0x1F37C}, // plant/food emoji
	{0x1F37E, 0x1F393}, // drink/object emoji
	{0x1F3A0, 0x1F3CA}, // activity emoji
	{0x1F3CF, 0x1F3D3}, // sport emoji
	{0x1F3E0, 0x1F3F0}, // building emoji
	{0x1F3F4, 0x1F3F4}, // black flag
	{0x1F3F8, 0x1F43E}, // animal/object emoji
	{0x1F440, 0x1F440}, // eyes
	{0x1F442, 0x1F4FC}, // body/object emoji
	{0x1F4FF, 0x1F53D}, // symbol emoji
	{0x1F54B, 0x1F54E}, // religious emoji
	{0x1F550, 0x1F567}, // clock faces
	{0x1F57A, 0x1F57A}, // dancing man
	{0x1F595, 0x1F596}, // hand gestures
	{0x1F5A4, 0x1F5A4}, // black heart
	{0x1F5FB, 0x1F64F}, // emoticons
	{0x1F680, 0x1F6C5}, // transport
	{0x1F6CC, 0x1F6CC}, // person in bed
	{0x1F6D0, 0x1F6D2}, // place of worship, shopping
	{0x1F6D5, 0x1F6D7}, // hindu temple, hut
	{0x1F6DD, 0x1F6DF}, // playground slide, ring buoy
	{0x1F6EB, 0x1F6EC}, // airplane departure/arrival
	{0x1F6F4, 0x1F6FC}, // scooter, roller skate
	{0x1F7E0, 0x1F7EB}, // colored circles and squares
	{0x1F7F0, 0x1F7F0}, // heavy equals sign
	{0x1F90C, 0x1F93A}, // hand/people emoji
	{0x1F93C, 0x1F945}, // sport emoji
	{0x1F947, 0x1F9FF}, // supplemental symbols
	{0x1FA70, 0x1FA7C}, // clothing, medical
	{0x1FA80, 0x1FA88}, // toys, instruments
	{0x1FA90, 0x1FABD}, // objects, nature
	{0x1FABF, 0x1FAC5}, // people
	{0x1FACE, 0x1FADB}, // food, animals
	{0x1FAE0, 0x1FAE8}, // faces
	{0x1FAF0, 0x1FAF8}, // hands
	{0x20000, 0x2FFFD}, // CJK ext B-F
	{0x30000, 0x3FFFD}, // CJK ext G-H
}

// runeWidth returns the number of terminal cells r occupies: 0, 1, or 2.
func runeWidth(r rune) int {
	// C0/C1 controls and DEL print nothing (a stray ESC is stripped upstream by
	// skipEscape; anything else that reaches here is not gitling's own output).
	if r < 0x20 || (r >= 0x7F && r < 0xA0) {
		return 0
	}
	if r < 0x0300 { // fast path: ASCII and Latin-1 are all single-width
		return 1
	}
	if isZeroWidth(r) {
		return 0
	}
	if inRanges(wide, r) {
		return 2
	}
	return 1
}

// variationSelector16 requests emoji presentation for the preceding character.
const variationSelector16 = 0xFE0F

// stepCell measures the display cluster starting at byte offset i in s,
// returning its width in cells and the offset just past it. It is the single
// stepper every width helper in this package walks with, so they cannot
// disagree about where one cluster ends and the next begins.
//
// Beyond the per-rune width it handles emoji presentation sequences: a
// text-default character followed by U+FE0F (❤️, ⚠️, ✔️ — common enough in commit
// subjects) is drawn by the terminal as a two-cell emoji, not the one cell its
// base rune would score alone.
func stepCell(s string, i int) (w, next int) {
	r, size := utf8.DecodeRuneInString(s[i:])
	w, next = runeWidth(r), i+size
	if next < len(s) {
		if vs, vsSize := utf8.DecodeRuneInString(s[next:]); vs == variationSelector16 {
			next += vsSize
			if w == 1 {
				w = 2 // emoji presentation: the pair occupies two cells
			}
		}
	}
	return w, next
}

// inRanges reports whether r falls inside one of the sorted, non-overlapping
// intervals in table.
func inRanges(table [][2]rune, r rune) bool {
	i := sort.Search(len(table), func(i int) bool { return table[i][1] >= r })
	return i < len(table) && r >= table[i][0]
}

// cellLen returns the terminal width of s in cells. It is the width-aware
// counterpart to len([]rune(s)) and is what every column-alignment calculation
// in this package measures with. It assumes s carries no ANSI escapes; for
// already-colored strings use visibleLen.
func cellLen(s string) int {
	n := 0
	for i := 0; i < len(s); {
		w, next := stepCell(s, i)
		n += w
		i = next
	}
	return n
}
