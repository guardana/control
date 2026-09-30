package docsite

import (
	"bytes"
	"encoding/xml"
	"errors"
	"io"
	"strings"
	"testing"
)

// FuzzRenderBody holds the renderer to its output contract for any Markdown:
// it refuses the page, or the page is well-formed XHTML that carries no
// script element and no event handler.
func FuzzRenderBody(f *testing.F) {
	for _, seed := range []string{
		"# A\n\n[b](b.md) [c](../../cmd/tool/main.go#L1) <https://example.invalid>\n",
		"# A\n\n| a | b |\n| --- | --- |\n| `x` | *y* |\n",
		"# A\n\n```mermaid\nsequenceDiagram\n    accTitle: T\n    accDescr: D\n    A->>B: x\n```\n",
		"# A\n\n```mermaid\nflowchart LR\n    accTitle: T\n    accDescr: D\n    A --> B\n```\n",
		"# A\n\n<!-- c -->\n\n> quote & more\n\n- one\n- two\n",
		"# A &amp; &nbsp; &copy;\n\n~~gone~~ www.example.invalid\n",
	} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, body string) {
		files, err := Build(fixture(body), func(string) bool { return false })
		if err != nil {
			if !errors.Is(err, ErrSite) {
				t.Fatalf("a refusal that is not ErrSite: %v", err)
			}
			return
		}
		out := files[Dir+"/concepts/a.html"]
		lower := strings.ToLower(string(out))
		if strings.Contains(lower, "<script") || strings.Contains(lower, " onerror=") || strings.Contains(lower, " onclick=") {
			t.Fatalf("the page carries a script:\n%s", out)
		}
		d := xml.NewDecoder(bytes.NewReader(out))
		d.Strict = true
		for {
			_, err := d.Token()
			if errors.Is(err, io.EOF) {
				return
			}
			if err != nil {
				t.Fatalf("the page is not well-formed XML: %v\n%s", err, out)
			}
		}
	})
}
