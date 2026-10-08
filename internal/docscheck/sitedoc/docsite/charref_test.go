package docsite

import (
	"errors"
	"testing"
)

// TestACharacterReferenceXHTMLCannotCarryIsRefused: the source check reads
// the Markdown, and a character reference becomes its character only when it
// renders, so the rendered page is checked too.
func TestACharacterReferenceXHTMLCannotCarryIsRefused(t *testing.T) {
	for _, body := range []string{"&#1;", "&#x1F;", "&#xFFFE;", "a `x` &#8; b"} {
		t.Run(body, func(t *testing.T) {
			_, err := buildAll(fixture("# A\n\n" + body + "\n"))
			if !errors.Is(err, ErrSite) {
				t.Fatalf("Build with %q: %v, want ErrSite", body, err)
			}
		})
	}
}
