package metricsdoc

import (
	"bytes"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/guardana/control/internal/docscheck/frontmatter"
	"github.com/guardana/control/internal/metrics"
)

// TestMetricsDocIsCurrent pins the committed page to the table. Before the
// byte comparison each metric is looked for in the page on its own, so a
// renderer that dropped a column and a page regenerated from it cannot pass
// together.
func TestMetricsDocIsCurrent(t *testing.T) {
	want := committedPage(t)
	table := metrics.Table()
	lines := strings.Split(string(want), "\n")
	for _, m := range table {
		row := rowOf(lines, "| `"+m.Name+"` |")
		if row == "" {
			t.Errorf("%s: no row, or more than one, for %s", Path, m.Name)
			continue
		}
		for _, cell := range []string{" " + string(m.Type) + " ", "`" + m.Reads + "`", m.Help} {
			if !strings.Contains(row, cell) {
				t.Errorf("%s: the row of %s does not carry %q: %s", Path, m.Name, cell, row)
			}
		}
	}
	if rows := strings.Count(string(want), "\n| `"); rows != len(table) {
		t.Errorf("%s holds %d rows, the table %d", Path, rows, len(table))
	}
	got, err := Page(table)
	if err != nil {
		t.Fatalf("rendering the page: %v", err)
	}
	if !bytes.Equal(got, want) {
		t.Errorf("%s is not what the renderer produces; rebuild it with `make docs-gen`", Path)
	}
}

func fixture() []metrics.Metric {
	return []metrics.Metric{
		{Name: metrics.Prefix() + "a_total", Type: metrics.Counter, Label: "code", Reads: "Pipeline.Blocks", Help: "Blocks."},
		{Name: metrics.Prefix() + "b", Type: metrics.Gauge, Reads: "Spool.Bytes", Help: "Bytes now."},
	}
}

func TestPageHasOneRowPerMetricInOrder(t *testing.T) {
	page, err := Page(fixture())
	if err != nil {
		t.Fatalf("Page: %v", err)
	}
	text := string(page)
	first := "| `" + metrics.Prefix() + "a_total` | counter | `code` | `Pipeline.Blocks` | Blocks. |\n"
	second := "| `" + metrics.Prefix() + "b` | gauge |  | `Spool.Bytes` | Bytes now. |\n"
	i, j := strings.Index(text, first), strings.Index(text, second)
	if i < 0 || j < 0 || j < i {
		t.Errorf("the rows are missing or out of order:\n%s", text)
	}
	meta, _, err := frontmatter.Parse(page)
	if err != nil {
		t.Fatalf("the page's frontmatter: %v", err)
	}
	if meta.Generated != Generator || meta.Type != "reference" {
		t.Errorf("frontmatter says generated %q, type %q", meta.Generated, meta.Type)
	}
	if !strings.Contains(text, "\n# Metrics\n") || !strings.Contains(text, metrics.ContentType) {
		t.Error("the page has no H1 or does not name the content type")
	}
}

// Each refusal is one change to the fixture, which renders.
func TestPageRefuses(t *testing.T) {
	for _, c := range []struct {
		name   string
		change func([]metrics.Metric) []metrics.Metric
	}{
		{"an empty table", func([]metrics.Metric) []metrics.Metric { return nil }},
		{"a name twice", func(m []metrics.Metric) []metrics.Metric { m[1].Name = m[0].Name; return m }},
		{"a name outside the prefix", func(m []metrics.Metric) []metrics.Metric { m[1].Name = "other_b"; return m }},
		{"a pipe in a cell", func(m []metrics.Metric) []metrics.Metric { m[1].Help = "a | b"; return m }},
		{"a backtick in a cell", func(m []metrics.Metric) []metrics.Metric { m[1].Reads = "a`b"; return m }},
		{"a newline in a cell", func(m []metrics.Metric) []metrics.Metric { m[1].Help = "a\nb"; return m }},
		{"no meaning", func(m []metrics.Metric) []metrics.Metric { m[1].Help = ""; return m }},
		{"no statistic", func(m []metrics.Metric) []metrics.Metric { m[1].Reads = ""; return m }},
		{"no type", func(m []metrics.Metric) []metrics.Metric { m[1].Type = ""; return m }},
	} {
		t.Run(c.name, func(t *testing.T) {
			if _, err := Page(c.change(fixture())); err == nil {
				t.Error("Page rendered the table")
			}
		})
	}
}

func committedPage(t *testing.T) []byte {
	t.Helper()
	_, self, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("cannot resolve this file's own path; refusing to fall back on the working directory")
	}
	root := filepath.Join(filepath.Dir(self), "..", "..", "..")
	if _, err := os.Stat(filepath.Join(root, "go.mod")); err != nil {
		t.Fatalf("repository root %q holds no go.mod: %v", root, err)
	}
	page, err := fs.ReadFile(os.DirFS(root), Path)
	if err != nil {
		t.Fatalf("reading %s: %v", Path, err)
	}
	return page
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
