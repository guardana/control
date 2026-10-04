package observelog

import (
	"bytes"
	"encoding/json"
	"testing"

	"github.com/guardana/control/internal/lineexport"
)

// FuzzExport: whatever a file holds, an export from its start is whole, each
// of its lines one JSON object, and its counts are the records it wrote. An
// export after its cursor writes no line again.
func FuzzExport(f *testing.F) {
	f.Add(readGolden(f, "log.jsonl"))
	f.Add([]byte("{}\n\r\n\n"))
	f.Add([]byte(""))
	f.Fuzz(func(t *testing.T, body []byte) {
		var out bytes.Buffer
		tr, err := Export(source(t, "f.jsonl", body), Query{Limit: MaxExportLimit}, &out)
		if err != nil {
			t.Fatalf("Export = %v", err)
		}
		lines := bytes.Split(bytes.TrimSuffix(out.Bytes(), []byte("\n")), []byte("\n"))
		for _, line := range lines {
			var v map[string]any
			if err := json.Unmarshal(line, &v); err != nil {
				t.Fatalf("an output line is not one object: %q", line)
			}
		}
		total := 0
		for _, n := range tr.Counts {
			total += n
		}
		if !tr.EndReached || total != len(lines)-2 {
			t.Fatalf("trailer %+v for %d lines", tr, len(lines))
		}
		if tr.NextCursor == "" {
			return
		}
		again, err := Export(source(t, "f.jsonl", body), Query{After: tr.NextCursor, Limit: MaxExportLimit}, &bytes.Buffer{})
		if err != nil {
			t.Fatalf("an export after the cursor = %v", err)
		}
		if again.Counts["observation"]+again.Counts["import_report"]+again.Counts[lineexport.Duplicate] != 0 {
			t.Fatalf("an export after the cursor wrote %v", again.Counts)
		}
	})
}
