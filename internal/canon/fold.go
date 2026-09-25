package canon

import (
	"fmt"
	"strings"
	"unicode"
	"unicode/utf8"
)

// Two member names in one object that are equal under simple case folding are
// refused like a duplicate (ADR-0011). A consumer that matches names without
// regard to case, as encoding/json does when it fills a struct, would otherwise
// read one value where the digest covers another. The relation is that of
// CaseFolding.txt, statuses C and S, compared code point by code point, from
// the Unicode tables of the toolchain; a test pins them at 17.0.0.

// FoldKey maps every code point of key to the least member of its simple case
// folding class, so two keys fold together exactly when their fold keys are
// equal. It is strings.EqualFold made into a map key: comparing every pair of
// keys would cost quadratic time in an object of many members, and the
// arguments document is caller input. It is exported so that a consumer that
// matches member names by name matches them under the same relation.
func FoldKey(key string) string {
	return strings.Map(foldRune, key)
}

func foldRune(r rune) rune {
	if r < utf8.RuneSelf {
		if 'a' <= r && r <= 'z' {
			return r - ('a' - 'A')
		}
		return r
	}
	// SimpleFold steps through the class in a cycle, back to r.
	least := r
	for next := unicode.SimpleFold(r); next != r; next = unicode.SimpleFold(next) {
		least = min(least, next)
	}
	return least
}

// foldedMember returns the position of the first key that folds together with
// an earlier one, and the position of that earlier key, counting in the order
// keys are given: canonical order for a Go map, which has no order of its own.
func foldedMember(keys []string) (later, earlier int, found bool) {
	if len(keys) < 2 {
		return 0, 0, false
	}
	seen := make(map[string]int, len(keys))
	for i, key := range keys {
		fold := FoldKey(key)
		if j, ok := seen[fold]; ok {
			return i, j, true
		}
		seen[fold] = i
	}
	return 0, 0, false
}

// foldsTogether names the two members by position, never by name: a member
// name is caller content.
func foldsTogether(later, earlier int) string {
	return fmt.Sprintf("object key at member %d folds together with member %d", later, earlier)
}
