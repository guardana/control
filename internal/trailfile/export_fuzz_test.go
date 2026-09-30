package trailfile

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/guardana/control/internal/evidence"
)

// FuzzExportCursor: a cursor parses only in its one spelling, and one an
// export accepts names the end of a line of the file. The empty string is no
// cursor, and starts at the first byte.
func FuzzExportCursor(f *testing.F) {
	body := file(lineE1, lineE2, lineE3)
	for n := 1; n <= 3; n++ {
		f.Add(cursorAfter(body, n))
	}
	f.Add(cursorAfter(file(lineE3), 1))
	f.Add("v1::1:")
	f.Add("v1:" + strings.Repeat("0", 64) + ":01:" + strings.Repeat("f", 64))
	f.Fuzz(func(t *testing.T, s string) {
		c, err := parseCursor(s)
		if err == nil && c.String() != s {
			t.Fatalf("%q parses and spells %q", s, c.String())
		}
		if s == "" {
			return
		}
		q := query()
		q.After = s
		if _, err := Export(source(body, false), q, &bytes.Buffer{}); err == nil {
			for n := 1; n <= 3; n++ {
				if s == cursorAfter(body, n) {
					return
				}
			}
			t.Fatalf("%q was accepted and names no line's end", s)
		}
	})
}

const maxFuzzLines = 64

// FuzzExport: any bytes export without a refusal, framed, with no carriage
// return, each record where its line starts, each event the line's bytes but
// for its line ending and one event the codec reads, and the trailer's counts
// those of the records.
func FuzzExport(f *testing.F) {
	for _, name := range []string{"records", "resumed", "empty"} {
		seed, err := os.ReadFile(filepath.Join(goldenDir, name+".trail")) //nolint:gosec // G304: the test's own fixture
		if err != nil {
			f.Fatal(err)
		}
		f.Add(seed, false, uint8(0))
		f.Add(seed, true, uint8(2))
	}
	f.Add([]byte(lineE1+"\r\n"+strings.Replace(lineE2, ",", ",\r", 1)+"\n"), false, uint8(7))
	f.Fuzz(func(t *testing.T, body []byte, held bool, limit uint8) {
		// The codec takes a line-sized buffer per line it decodes, so an input
		// of many short lines costs the fuzzer its throughput and finds nothing
		// a few lines do not.
		if bytes.Count(body, []byte{'\n'}) > maxFuzzLines {
			return
		}
		var out bytes.Buffer
		tr, err := Export(source(string(body), held), Query{Limit: int(limit)%8 + 1}, &out)
		if err != nil {
			t.Fatalf("Export = %v", err)
		}
		counts := checkRecords(t, body, out.String())
		if counts["event"] != tr.Events || counts["gap"] != tr.Gaps || counts["duplicate"] != tr.Duplicates {
			t.Fatalf("records %v, trailer %+v", counts, tr)
		}
		if tr.NextCursor != "" {
			q := query()
			q.After = tr.NextCursor
			if _, err := Export(source(string(body), held), q, &bytes.Buffer{}); err != nil {
				t.Fatalf("the next cursor %q is refused: %v", tr.NextCursor, err)
			}
		}
	})
}

// checkRecords holds an export to its frame and each record to its line, and
// counts the records by type.
func checkRecords(t *testing.T, body []byte, out string) map[string]int {
	t.Helper()
	if strings.Contains(out, "\r") {
		t.Fatalf("a carriage return in the export:\n%q", out)
	}
	lines := strings.Split(strings.TrimSuffix(out, "\n"), "\n")
	counts := map[string]int{}
	last := int64(-1)
	for i, line := range lines {
		var r record
		if err := json.Unmarshal([]byte(line), &r); err != nil {
			t.Fatalf("line %d %q: %v", i+1, line, err)
		}
		if (i == 0) != (r.Type == "header") || (i == len(lines)-1) != (r.Type == "trailer") {
			t.Fatalf("a %s record at line %d of %d", r.Type, i+1, len(lines))
		}
		if r.Type == "header" || r.Type == "trailer" {
			continue
		}
		counts[r.Type]++
		if *r.Offset <= last {
			t.Fatalf("record at %d after one at %d", *r.Offset, last)
		}
		last = *r.Offset
		if r.Type == "event" {
			checkEventRecord(t, body, line, *r.Offset)
		}
	}
	return counts
}

// checkEventRecord holds an event record to the line at its offset: its bytes
// as they stand without its newline and one carriage return before it, and one
// event the codec reads.
func checkEventRecord(t *testing.T, body []byte, record string, offset int64) {
	t.Helper()
	rest := body[offset:]
	own := bytes.TrimSuffix(rest[:bytes.IndexByte(rest, '\n')], []byte("\r"))
	if !strings.Contains(record, `"event":`+string(own)+"}") {
		t.Fatalf("event at %d is not its line %q", offset, own)
	}
	if _, err := evidence.DecodeJSONL(bytes.NewReader(append(bytes.Clone(own), '\n')), 1); err != nil {
		t.Fatalf("event at %d does not decode: %v", offset, err)
	}
}
