package docscheck

import (
	"bytes"
	"io/fs"
	"testing"

	"github.com/guardana/control/internal/docscheck/diagramdoc"
)

// The pin between the listings the code exports and the generated block of
// each concept page: the block is rendered here, spliced into the committed
// page, and the result must be the committed page. The renderer is called
// directly, never through a subprocess, and every claimed block is a page
// the renderer knows.
func TestConceptDiagramsAreCurrent(t *testing.T) {
	fsys := repoFS(t)
	if len(diagramdoc.Pages) == 0 {
		t.Fatal("diagramdoc names no page, so nothing would be pinned")
	}
	for path, name := range diagramdoc.Pages {
		block, err := diagramdoc.Render(name)
		if err != nil {
			t.Errorf("%s: rendering %s: %v", path, name, err)
			continue
		}
		want, err := fs.ReadFile(fsys, path)
		if err != nil {
			t.Errorf("reading %s: %v", path, err)
			continue
		}
		got, err := diagramdoc.Splice(want, block)
		if err != nil {
			t.Errorf("%s: %v", path, err)
			continue
		}
		if !bytes.Equal(got, want) {
			t.Errorf("%s holds a block the code no longer produces; rebuild it with `make docs-gen`\n%s", path, firstDifferingLine(got, want))
		}
	}
	for _, claim := range generatedBlocks {
		if claim.generator == diagramdoc.Script {
			if _, ok := diagramdoc.Pages[claim.page]; !ok {
				t.Errorf("%s claims a block of %s that the renderer does not know", claim.page, claim.generator)
			}
		}
	}
	for path := range diagramdoc.Pages {
		if !containsClaim(generatedBlocks, blockClaim{path, diagramdoc.Script}) {
			t.Errorf("%s holds a block nothing claims", path)
		}
	}
}

func containsClaim(claims []blockClaim, c blockClaim) bool {
	for _, x := range claims {
		if x == c {
			return true
		}
	}
	return false
}
