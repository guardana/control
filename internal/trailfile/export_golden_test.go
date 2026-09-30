package trailfile

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
)

// goldenDir holds the export's goldens beside the other contract goldens:
// each <name>.trail is a file and <name>.jsonl the export of it.
var goldenDir = filepath.Join("..", "..", "testdata", "export")

// goldenCases are the queries the goldens were written for. Between them
// they hold a record of every type, a gap of every reason but too_long and
// carriage_return, and a header naming every query member.
var goldenCases = map[string]struct {
	held bool
	q    Query
}{
	"records": {false, Query{Limit: DefaultExportLimit}},
	"resumed": {true, Query{
		After: "v1:57f7f9c13a3299681c3a7122a448585ec8e5984f4c854f0c3dd54b08c997e2a3:140:" +
			"57f7f9c13a3299681c3a7122a448585ec8e5984f4c854f0c3dd54b08c997e2a3",
		Limit: 5, MaxBytes: 100_000, Requests: []string{"r1", "r2"}, Runs: []string{"run1", "run2"},
		Tenants: []string{"t1", "t2"}, Projects: []string{"p2", "p1"},
		Kinds: []string{"EVENT_KIND_POLICY_DECIDED", "EVENT_KIND_ACTION_PROPOSED"},
	}},
	"empty": {false, Query{Limit: DefaultExportLimit}},
}

func TestTheExportMatchesItsGoldens(t *testing.T) {
	for name, c := range goldenCases {
		body, err := os.ReadFile(filepath.Join(goldenDir, name+".trail")) //nolint:gosec // G304: the test's own fixture
		if err != nil {
			t.Fatal(err)
		}
		want, err := os.ReadFile(filepath.Join(goldenDir, name+".jsonl")) //nolint:gosec // G304: the test's own fixture
		if err != nil {
			t.Fatal(err)
		}
		src := source(string(body), c.held)
		src.Name = name + ".trail"
		var out bytes.Buffer
		if _, err := Export(src, c.q, &out); err != nil {
			t.Fatalf("%s: Export = %v", name, err)
		}
		if !bytes.Equal(out.Bytes(), want) {
			t.Errorf("%s: export does not match its golden\n--- got ---\n%s--- want ---\n%s", name, out.String(), want)
		}
	}
}
