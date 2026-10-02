package trailfile_test

import (
	"bytes"
	"encoding/json"
	"io"
	"io/fs"
	"os"
	"strings"
	"testing"

	"github.com/guardana/control/internal/trailfile"
)

// consumerFixtures holds the trails the evidence-report example reads as
// exports, and the exports, which that module cannot produce without this
// one. The directory sits in this repository only: a program that requires
// this module does not hold it, and this test fails there.
const consumerFixtures = "../../examples/evidence-report/testdata"

// exportsManifest names, for each committed export, the trail it was
// exported from and how.
type exportsManifest struct {
	Exports []fixtureExport `json:"exports"`
}

type fixtureExport struct {
	Trail  string `json:"trail"`
	Export string `json:"export"`
	Query  struct {
		After    string   `json:"after"`
		Limit    int      `json:"limit"`
		MaxBytes int64    `json:"max_bytes"`
		Request  []string `json:"request"`
		Run      []string `json:"run"`
		Tenant   []string `json:"tenant"`
		Project  []string `json:"project"`
		Kind     []string `json:"kind"`
	} `json:"query"`
	WriterHeld bool `json:"writer_held"`
}

// TestConsumerFixturesAreTheExporters exports every trail the manifest names
// with Export and holds the committed export to its bytes. A trail or an
// export in the directory the manifest does not name fails, so nothing there
// is read by the example unchecked.
func TestConsumerFixturesAreTheExporters(t *testing.T) {
	root, err := os.OpenRoot(consumerFixtures)
	if err != nil {
		t.Fatalf("the example's fixtures: %v", err)
	}
	defer func() { _ = root.Close() }()
	manifest := readExportsManifest(t, root)
	named := map[string]bool{}
	for _, e := range manifest.Exports {
		if named[e.Export] {
			t.Errorf("exports.json names %s twice", e.Export)
		}
		named[e.Export], named[e.Trail] = true, true
		t.Run(e.Export, func(t *testing.T) { checkFixtureExport(t, root, e) })
	}
	err = fs.WalkDir(root.FS(), ".", func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		if (strings.HasSuffix(path, ".trail") || strings.HasSuffix(path, ".jsonl")) && !named[path] {
			t.Errorf("%s is in the example's testdata and not in exports.json", path)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

func readExportsManifest(t *testing.T, root *os.Root) exportsManifest {
	t.Helper()
	b, err := fs.ReadFile(root.FS(), "exports.json")
	if err != nil {
		t.Fatal(err)
	}
	dec := json.NewDecoder(bytes.NewReader(b))
	dec.DisallowUnknownFields()
	var m exportsManifest
	if err := dec.Decode(&m); err != nil {
		t.Fatalf("exports.json: %v", err)
	}
	if _, err := dec.Token(); err != io.EOF {
		t.Fatal("exports.json holds more than one JSON value")
	}
	if len(m.Exports) == 0 {
		t.Fatal("exports.json names no export: the check would examine nothing")
	}
	return m
}

func checkFixtureExport(t *testing.T, root *os.Root, e fixtureExport) {
	if !strings.HasSuffix(e.Trail, ".trail") || !strings.HasSuffix(e.Export, ".jsonl") {
		t.Fatalf("exports.json pairs %q with %q, want a .trail and a .jsonl", e.Trail, e.Export)
	}
	want, err := fs.ReadFile(root.FS(), e.Export)
	if err != nil {
		t.Fatal(err)
	}
	got, committed := strings.Split(exportFixture(t, root, e), "\n"), strings.Split(string(want), "\n")
	for i := 0; i < len(got) || i < len(committed); i++ {
		switch {
		case i >= len(got) || i >= len(committed):
			t.Fatalf("the exporter wrote %d lines and %s holds %d", len(got), e.Export, len(committed))
		case got[i] != committed[i]:
			t.Fatalf("line %d: the exporter wrote\n%s\n%s holds\n%s", i+1, got[i], e.Export, committed[i])
		}
	}
}

// exportFixture exports the trail e names with the query it names, the
// default limit when it names none.
func exportFixture(t *testing.T, root *os.Root, e fixtureExport) string {
	t.Helper()
	f, err := root.Open(e.Trail)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = f.Close() }()
	info, err := f.Stat()
	if err != nil {
		t.Fatal(err)
	}
	q := trailfile.Query{After: e.Query.After, Limit: e.Query.Limit, MaxBytes: e.Query.MaxBytes,
		Requests: e.Query.Request, Runs: e.Query.Run, Tenants: e.Query.Tenant, Projects: e.Query.Project, Kinds: e.Query.Kind}
	if q.Limit == 0 {
		q.Limit = trailfile.DefaultExportLimit
	}
	held := e.WriterHeld
	src := trailfile.Source{Name: e.Trail, R: f, Size: info.Size(), Held: func() (bool, error) { return held, nil }}
	var out bytes.Buffer
	if _, err := trailfile.Export(src, q, &out); err != nil {
		t.Fatalf("exporting %s: %v", e.Trail, err)
	}
	return out.String()
}
