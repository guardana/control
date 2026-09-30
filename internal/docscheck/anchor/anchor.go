// Package anchor spells the fragment GitHub gives a Markdown heading. The link
// check and the rendered pages both use it, so a fragment that resolves on one
// resolves on the other.
package anchor

import (
	"regexp"
	"strconv"
	"strings"
)

var (
	closingHashes = regexp.MustCompile(`[ \t]+#+$`)
	inlineLink    = regexp.MustCompile(`!?\[([^\]]*)\]\([^)]*\)`)
	// What GitHub drops from a heading to make its anchor: everything but
	// letters, marks, decimal digits, connector punctuation such as "_",
	// spaces and hyphens.
	notInAnchor = regexp.MustCompile(`[^\p{L}\p{M}\p{Nd}\p{Pc} -]`)
)

// Of spells the anchor of a heading's text as written in Markdown: link syntax
// reduced to its text, lower case, the characters above dropped, each space
// turned into a hyphen.
func Of(text string) string {
	text = closingHashes.ReplaceAllString(text, "")
	text = inlineLink.ReplaceAllString(text, "$1")
	text = notInAnchor.ReplaceAllString(strings.ToLower(text), "")
	return strings.ReplaceAll(text, " ", "-")
}

// Numbering gives the headings of one page their anchors in page order, a
// repeated one numbered "-1", "-2" and on, as GitHub numbers them.
type Numbering map[string]int

// Next is the anchor of the page's next heading.
func (n Numbering) Next(text string) string {
	a := Of(text)
	seen := n[a]
	n[a]++
	if seen > 0 {
		return a + "-" + strconv.Itoa(seen)
	}
	return a
}
