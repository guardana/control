package mermaid

import (
	"io/fs"
	"strings"
	"testing"

	"github.com/guardana/control/internal/brand"
)

// The brand check exempts testdata because the rename rewrites it. Here a
// rewrite would part each golden from its input: the figure's id hashes the
// source and its widths count the characters, so the goldens would stop
// matching after a rename that reports success.
func TestTestdataSpellsNoProductName(t *testing.T) {
	literals := []string{
		brand.Name, brand.Slug, brand.CLI, brand.Gateway, strings.TrimSuffix(brand.EnvPrefix, "_"),
		brand.OTelNamespace, brand.ProtoPackage, brand.ModulePath, brand.Image,
	}
	files := 0
	err := fs.WalkDir(testdata, ".", func(path string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		for _, literal := range literals {
			if strings.Contains(path, literal) {
				t.Errorf("testdata/%s: the path spells %q", path, literal)
			}
		}
		if d.IsDir() {
			return nil
		}
		data, err := fs.ReadFile(testdata, path)
		if err != nil {
			return err
		}
		files++
		for _, literal := range literals {
			if strings.Contains(string(data), literal) {
				t.Errorf("testdata/%s spells %q", path, literal)
			}
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walking testdata: %v", err)
	}
	if files < 2*len(requiredGoldens) {
		t.Fatalf("scanned %d files in testdata, want at least %d", files, 2*len(requiredGoldens))
	}
}
