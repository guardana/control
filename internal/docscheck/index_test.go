package docscheck

import (
	"bytes"
	"io/fs"
	"testing"

	"github.com/guardana/control/internal/docscheck/docsconfig"
	"github.com/guardana/control/internal/docscheck/indexdoc"
)

// The pin between the pages and the index: the map is rendered here from
// every page's frontmatter and every record, and must be the committed page.
func TestIndexIsCurrent(t *testing.T) {
	fsys := repoFS(t)
	data, err := fs.ReadFile(fsys, "docs/docs.json")
	if err != nil {
		t.Fatal(err)
	}
	cfg, err := docsconfig.Parse(data)
	if err != nil {
		t.Fatal(err)
	}
	pages, records, err := indexdoc.Collect(fsys, func(rel string) bool { return excluded(cfg, rel) })
	if err != nil {
		t.Fatal(err)
	}
	if len(pages) < minPagesToday-1 {
		t.Fatalf("the index would list %d pages, fewer than exist", len(pages))
	}
	got, err := indexdoc.Render(pages, records)
	if err != nil {
		t.Fatal(err)
	}
	want, err := fs.ReadFile(fsys, indexdoc.Index)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, want) {
		t.Errorf("%s is not what the renderer produces; rebuild it with `make docs-gen`\n%s", indexdoc.Index, firstDifferingLine(got, want))
	}
}
