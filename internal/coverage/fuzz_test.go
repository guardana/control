package coverage_test

import (
	"bytes"
	"errors"
	"io/fs"
	"os"
	"testing"
	"time"

	"github.com/guardana/control/internal/coverage"
)

// FuzzReadInventory: a refusal is always ErrInventory with nothing read, and
// an accepted inventory has a path and unique ids and tools.
func FuzzReadInventory(f *testing.F) {
	f.Add([]byte(`{"schema_version":"0.1","paths":[{"id":"a","kind":"mcp_tool","upstream":"u","tool":"t",` +
		`"sources":[{"source_id":"s","name":"t","server_address":"h"}]},{"id":"b","kind":"egress","host":"h"}]}`))
	f.Add([]byte(`{"schema_version":"0.1","paths":[{"id":"a","id":"a","kind":"process","name":"n"}]}`))
	f.Add([]byte(`{"schema_version":"0.1","paths":[{"id":"a","kind":"http_api","name":null}]}`))
	f.Fuzz(func(t *testing.T, b []byte) {
		inv, err := coverage.ReadInventory(b)
		if err != nil {
			if !errors.Is(err, coverage.ErrInventory) || inv != nil {
				t.Fatalf("refusal %v with %+v, want ErrInventory and nothing", err, inv)
			}
			return
		}
		if len(inv.Paths) == 0 {
			t.Fatal("an accepted inventory has no path")
		}
		ids, tools := map[string]bool{}, map[[2]string]bool{}
		for _, p := range inv.Paths {
			if p.ID == "" || ids[p.ID] {
				t.Fatalf("id %q empty or repeated", p.ID)
			}
			ids[p.ID] = true
			if p.Kind == coverage.KindMCPTool {
				if tools[[2]string{p.Upstream, p.Tool}] {
					t.Fatalf("tool %q on %q repeated", p.Tool, p.Upstream)
				}
				tools[[2]string{p.Upstream, p.Tool}] = true
			}
		}
	})
}

// FuzzReadExport: a refusal is always ErrExport with nothing read; an export
// that reads as whole had a trailer, and a map made with it never fails.
func FuzzReadExport(f *testing.F) {
	for _, b := range exportGoldens(f) {
		f.Add(b)
	}
	f.Add(lines(header(plainQuery), proposal{trace: traceA, span: spanA}.line(), trailer(1, 0, 0, true)))
	f.Fuzz(func(t *testing.T, b []byte) {
		x, err := coverage.ReadExport(bytes.NewReader(b))
		if err != nil {
			if !errors.Is(err, coverage.ErrExport) || x != nil {
				t.Fatalf("refusal %v with %+v, want ErrExport and nothing", err, x)
			}
			return
		}
		if x.Whole == (x.NotWhole != "") {
			t.Fatalf("whole %v with reason %q", x.Whole, x.NotWhole)
		}
		if x.Whole && !bytes.Contains(b, []byte(`"trailer"`)) {
			t.Fatal("an export with no trailer read as whole")
		}
		r, err := coverage.Map(coverage.Input{Inventory: toolInventory(t),
			Planes:  []coverage.Plane{withExport(plane("a", modeEnforce, false, override(effectWrite)), x)},
			Sources: []coverage.Source{liveSource(selfReported, observedA.record())}, Now: now.Add(time.Duration(len(b)))})
		if err != nil || len(r.Paths) != 1 || len(r.Paths[0].Joins) != 1 {
			t.Fatalf("Map with an accepted export: %+v, %v", r, err)
		}
	})
}

// exportGoldens are the trail export goldens, each file's bytes.
func exportGoldens(tb testing.TB) [][]byte {
	tb.Helper()
	dir := os.DirFS("../../testdata/export")
	names, err := fs.Glob(dir, "*.jsonl")
	if err != nil || len(names) == 0 {
		tb.Fatalf("no export goldens: %v", err)
	}
	out := make([][]byte, 0, len(names))
	for _, name := range names {
		b, err := fs.ReadFile(dir, name)
		if err != nil {
			tb.Fatal(err)
		}
		out = append(out, b)
	}
	return out
}
