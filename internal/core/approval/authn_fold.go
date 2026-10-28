package approval

import (
	"slices"
	"unicode"
)

// The comparison behind the reserved prefix. The standard library carries no
// confusables table, so the letters of "authn" and the colon are compared
// through four rules that together cover what a reader cannot tell apart:
// letter case, a Latin letter outside ASCII (which may be that letter with a
// mark, a small capital, a modifier form or a fullwidth form), the
// compatibility blocks that restyle ASCII letters, and a table of lookalikes
// from other scripts. A false match only refuses a claim; a missed one lets a
// claim read as authenticated, so each rule errs toward matching.

// ignorable is a code point a reader does not see as a character of its own:
// a mark, a format character, a variation selector or another
// default-ignorable code point.
func ignorable(r rune) bool {
	return unicode.In(r, unicode.Mn, unicode.Mc, unicode.Me, unicode.Cf,
		unicode.Variation_Selector, unicode.Other_Default_Ignorable_Code_Point)
}

// letterLike reports whether r reads as the lowercase ASCII letter want.
func letterLike(r, want rune) bool {
	if r <= unicode.MaxASCII {
		return unicode.ToLower(r) == want
	}
	if unicode.Is(unicode.Latin, r) && unicode.IsLetter(r) {
		return true
	}
	if styled, ok := styledASCII(r); ok {
		return styled == want
	}
	return slices.Contains(lookalikes[want], r) || slices.Contains(lookalikes[want], unicode.ToLower(r))
}

// styledASCII maps a letter of a block that restyles the ASCII alphabet,
// mathematical, parenthesized, circled, squared or a regional indicator, to
// the lowercase letter it restyles.
func styledASCII(r rune) (rune, bool) {
	switch {
	case r >= 0x1d400 && r <= 0x1d6a3:
		return 'a' + (r-0x1d400)%52%26, true
	case r >= 0x249c && r <= 0x24b5:
		return 'a' + r - 0x249c, true
	case r >= 0x24b6 && r <= 0x24cf:
		return 'a' + r - 0x24b6, true
	case r >= 0x24d0 && r <= 0x24e9:
		return 'a' + r - 0x24d0, true
	}
	for _, first := range []rune{0x1f110, 0x1f130, 0x1f150, 0x1f170, 0x1f1e6} {
		if r >= first && r < first+26 {
			return 'a' + r - first, true
		}
	}
	return 0, false
}

// lookalikes are the letters of other scripts and the symbols that read as
// each letter of "authn", in either case.
var lookalikes = map[rune][]rune{
	'a': {0x0430, 0x03b1, 0x03ac, 0x237a, 0x13aa},
	'u': {0x03c5, 0x03cd, 0x057d, 0x054d, 0x03bc, 0x222a, 0x22c3, 0x144c},
	't': {0x0442, 0x03c4, 0x13a2},
	'h': {0x04bb, 0x0570, 0x043d, 0x045b, 0x03b7, 0x13c2, 0xab92, 0x13bb, 0xab8b, 0x210b, 0x210c, 0x210d, 0x210e},
	'n': {0x0578, 0x057c, 0x03b7, 0x03ae, 0x043f, 0x03bd, 0x2115},
}

// colonLike reports whether r reads as a colon.
func colonLike(r rune) bool {
	switch r {
	case ':', 0xff1a, 0xfe55, 0xfe13, 0xfe30, 0x2236, 0xa789, 0x02d0, 0x02f8, 0x05c3, 0x0703, 0x0704,
		0x0589, 0xa4fd, 0x1361, 0x16ec, 0x205a, 0x2982, 0x1804, 0x0903, 0x0a83:
		return true
	}
	return false
}
