package docscheck

import (
	"bytes"
	"io/fs"
	"strings"
	"testing"

	"github.com/guardana/control/internal/docscheck/configdoc"
	"github.com/guardana/control/internal/gatewayconfig"
)

// The pin between the loader's field table and the page. The renderer is
// called directly, never through `go run`, for the reason obligations_test.go
// gives. Before the byte comparison, every key the table declares is looked
// for in the committed page on its own, so a renderer that dropped a column
// and a page regenerated from it cannot pass together. An empty cell is
// skipped rather than matched, since every row contains the empty string.
func TestConfigurationDocIsCurrent(t *testing.T) {
	fsys := repoFS(t)
	fields := gatewayconfig.Fields()
	if len(fields) == 0 {
		t.Fatal("the field table is empty; nothing to pin")
	}
	want, err := fs.ReadFile(fsys, configdoc.Path)
	if err != nil {
		t.Fatalf("reading %s: %v", configdoc.Path, err)
	}
	lines := strings.Split(string(want), "\n")
	for _, f := range fields {
		row := rowOf(lines, "| `"+f.Path+"` |")
		if row == "" {
			t.Errorf("%s: no row, or more than one, for %s", configdoc.Path, f.Path)
			continue
		}
		required := "| no |"
		if f.Required {
			required = "| yes |"
		}
		for _, cell := range append([]string{"`" + f.Env + "`", f.Kind, f.Default, required}, f.Values...) {
			if cell == "" {
				continue
			}
			if !strings.Contains(row, cell) {
				t.Errorf("%s: the row of %s does not carry %q: %s", configdoc.Path, f.Path, cell, row)
			}
		}
	}
	got, err := configdoc.Page(fields)
	if err != nil {
		t.Fatalf("rendering the page: %v", err)
	}
	if !bytes.Equal(got, want) {
		t.Errorf("%s is not what the renderer produces; rebuild it with `%s`\n%s",
			configdoc.Path, obligationsCommand, firstDifferingLine(got, want))
	}
}

// rowOf returns the one line starting with prefix, and "" when there is none
// or more than one.
func rowOf(lines []string, prefix string) string {
	found := ""
	for _, line := range lines {
		if strings.HasPrefix(line, prefix) {
			if found != "" {
				return ""
			}
			found = line
		}
	}
	return found
}
