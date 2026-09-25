package frontmatter_test

import (
	"bytes"
	"errors"
	"testing"

	"github.com/guardana/control/internal/docscheck/frontmatter"
)

// The seeds are the heads of the pages that carry a block today, one of
// which the grammar refuses, and the fixture the tests edit.
func FuzzParse(f *testing.F) {
	f.Add([]byte(good + body))
	f.Add([]byte("---\ntitle: Run the gateway\nsummary: Configure and start the gateway, check it with doctor, and read what it answers.\n" +
		"type: how-to\ncovers: [cmd/gateway/**, adapters/mcp/**]\n---\n\n# Run the gateway\n"))
	f.Add([]byte("---\ntitle: The MCP gateway\nsummary: How a call is intercepted, decided, held and recorded.\n" +
		"type: explanation\ncovers: [internal/gateway/**, adapters/mcp/**]\n---\n"))
	f.Add([]byte("---\ntitle: Adapters\nsummary: The seam an adapter implements and what the plane does with its capabilities.\n" +
		"type: extending\ncovers: [internal/gateway/adapter.go]\nstability: development\n---\n"))
	f.Add([]byte("---\ntitle: MCP enforcement coverage\nsummary: Per revision, transport and method, what the gateway enforces today and what it does not.\n" +
		"type: reference\ncovers: [adapters/mcp/**, internal/gateway/**, cmd/gateway/**]\nstability: development\n---\n"))
	f.Fuzz(func(t *testing.T, page []byte) {
		m, rest, err := frontmatter.Parse(page)
		if err != nil {
			if !errors.Is(err, frontmatter.ErrInvalid) {
				t.Fatalf("a refusal does not wrap ErrInvalid: %v", err)
			}
			if rest != nil {
				t.Fatalf("a refused page handed back a body: %q", rest)
			}
			return
		}
		block, err := frontmatter.Render(m)
		if err != nil {
			t.Fatalf("Render refused what Parse accepted: %v", err)
		}
		// One spelling: the block Parse read is the block Render writes.
		if !bytes.HasPrefix(page, block) || !bytes.Equal(page[len(block):], rest) {
			t.Fatalf("Render(Parse(page)) is not the page's own head:\n%s\n%s", block, page)
		}
	})
}
