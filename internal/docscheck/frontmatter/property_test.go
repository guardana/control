package frontmatter_test

import (
	"bytes"
	"testing"

	"pgregory.net/rapid"

	"github.com/guardana/control/internal/docscheck/frontmatter"
)

const (
	letters    = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ"
	scalarTail = letters + "0123456789 .,'()-/_"
	globTail   = "abcdefghijklmnopqrstuvwxyz0123456789/_.*-"
)

func scalar(t *rapid.T, label string) string {
	head := rapid.SampledFrom([]rune(letters)).Draw(t, label+" head")
	tail := rapid.StringOfN(rapid.RuneFrom([]rune(scalarTail)), 0, 40, -1).Draw(t, label+" tail")
	return string(head) + tail
}

func glob(t *rapid.T) string {
	head := rapid.SampledFrom([]rune("abcdefghijklmnopqrstuvwxyz")).Draw(t, "glob head")
	tail := rapid.StringOfN(rapid.RuneFrom([]rune(globTail)), 0, 24, -1).Draw(t, "glob tail")
	return string(head) + tail
}

// validMeta draws metadata the grammar admits; Validate filters what the
// alphabets alone cannot rule out, such as a trailing space or a word YAML
// reads as a boolean.
func validMeta() *rapid.Generator[frontmatter.Meta] {
	return rapid.Custom(func(t *rapid.T) frontmatter.Meta {
		m := frontmatter.Meta{
			Title:   scalar(t, "title"),
			Summary: scalar(t, "summary"),
			Type:    rapid.SampledFrom(frontmatter.Types).Draw(t, "type"),
		}
		n := rapid.IntRange(0, frontmatter.MaxCovers).Draw(t, "covers")
		seen := map[string]bool{}
		for i := 0; i < n; i++ {
			g := glob(t)
			if !seen[g] {
				seen[g] = true
				m.Covers = append(m.Covers, g)
			}
		}
		if len(m.Covers) == 0 {
			m.CoversReason = scalar(t, "reason")
		}
		if m.Type == "spec" || m.Type == "extending" {
			m.Stability = rapid.SampledFrom(append([]string{""}, frontmatter.Stabilities...)).Draw(t, "stability")
		}
		switch rapid.IntRange(0, 2).Draw(t, "generated") {
		case 1:
			m.Generated = "scripts/gen-" + glob(t) + ".go"
		case 2:
			m.Generated = "go test ./" + glob(t) + " -run Test"
		}
		return m
	}).Filter(func(m frontmatter.Meta) bool { return frontmatter.Validate(m) == nil })
}

func TestRenderThenParseIsTheIdentity(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		m := validMeta().Draw(t, "meta")
		block, err := frontmatter.Render(m)
		if err != nil {
			t.Fatalf("Render refused what Validate accepted: %v", err)
		}
		tail := rapid.SampledFrom([]string{"", "\n# H1\n\nbody\n", "no newline at the end"}).Draw(t, "body")
		got, body, err := frontmatter.Parse(append(append([]byte(nil), block...), tail...))
		if err != nil {
			t.Fatalf("Parse refused a rendered block: %v\n%s", err, block)
		}
		if !equal(got, m) {
			t.Fatalf("Parse(Render(m)) = %+v, want %+v", got, m)
		}
		if string(body) != tail {
			t.Fatalf("body = %q, want %q", body, tail)
		}
		again, err := frontmatter.Render(got)
		if err != nil || !bytes.Equal(again, block) {
			t.Fatalf("a second Render differs: %v\n%s\n%s", err, again, block)
		}
	})
}
