package frontmatter

import "strings"

// Render spells the block for m, the one way Parse reads it back. It refuses
// what Validate refuses, so a generator cannot write a page the check would
// then reject.
func Render(m Meta) ([]byte, error) {
	if err := Validate(m); err != nil {
		return nil, err
	}
	var b strings.Builder
	b.WriteString(fence + "\n")
	b.WriteString(keyTitle + ": " + m.Title + "\n")
	b.WriteString(keySummary + ": " + m.Summary + "\n")
	b.WriteString(keyType + ": " + m.Type + "\n")
	b.WriteString(keyCovers + ": [" + strings.Join(m.Covers, ", ") + "]\n")
	if m.CoversReason != "" {
		b.WriteString(keyCoversReason + ": " + m.CoversReason + "\n")
	}
	if m.Stability != "" {
		b.WriteString(keyStability + ": " + m.Stability + "\n")
	}
	if m.Generated != "" {
		b.WriteString(keyGenerated + ": " + m.Generated + "\n")
	}
	b.WriteString(fence + "\n")
	return []byte(b.String()), nil
}
