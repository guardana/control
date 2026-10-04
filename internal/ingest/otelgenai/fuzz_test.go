package otelgenai_test

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"

	"github.com/guardana/control/internal/ingest/otelgenai"
)

// FuzzImport: any input imports without an error from a reader that cannot
// fail, every record it yields is a valid log line, and the counts account
// for every span read.
func FuzzImport(f *testing.F) {
	names, err := filepath.Glob(filepath.Join("testdata", "*.jsonl"))
	if err != nil || len(names) == 0 {
		f.Fatalf("no seeds: %v", err)
	}
	for _, name := range names {
		b, err := os.ReadFile(filepath.Clean(name))
		if err != nil {
			f.Fatal(err)
		}
		f.Add(b)
	}
	f.Add([]byte(oneSpan("", attr(nested(33, inArray))) + "\n"))
	f.Add([]byte(`{"x":1,"resource\u0053pans":[{"scopeSpans":[{"s":"\"]}","spans":[0,"\\",{"a":[]}]}]}]}` + "\n"))
	desc := descriptor(f)
	f.Fuzz(func(t *testing.T, in []byte) {
		b, err := otelgenai.Import(bytes.NewReader(in), desc, descSHA, received)
		if err != nil {
			t.Fatalf("Import: %v", err)
		}
		checkBatch(t, b)
	})
}
