package findinglog

import (
	"bytes"
	"testing"

	"google.golang.org/protobuf/proto"
)

// FuzzReadLog: whatever a file holds, the reader returns whole writes only,
// each ending with its report, and every record it returns the writer
// writes again into a log the reader reads back the same.
func FuzzReadLog(f *testing.F) {
	a := line(f, findingRecord(finding(idA, confirmed, "repeated_denial")))
	b := line(f, findingRecord(finding(idB, suspected, "a\u0085b\u202ec")))
	r := line(f, writtenReport(1))
	f.Add([]byte(a + r + b + r))
	f.Add([]byte(a + r + b + r[:30]))
	f.Add([]byte(a + b))
	f.Add([]byte("{}\n\r\n\n"))
	f.Add([]byte(""))
	f.Fuzz(func(t *testing.T, body []byte) {
		got, err := readCommitted(bytes.NewReader(body), int64(len(body)))
		if err != nil {
			return
		}
		if len(got) > 0 && got[len(got)-1].GetSuperviseReport() == nil {
			t.Fatalf("the last record returned is no report: %v", got[len(got)-1])
		}
		var again []byte
		for _, rec := range got {
			l, err := marshalLine(rec)
			if err != nil {
				t.Fatalf("the writer refuses a record the reader returned: %v", err)
			}
			again = append(again, l...)
		}
		back, err := readCommitted(bytes.NewReader(again), int64(len(again)))
		if err != nil || len(back) != len(got) {
			t.Fatalf("the rewritten log reads as %d records, %v; want %d", len(back), err, len(got))
		}
		for i := range got {
			if !proto.Equal(back[i], got[i]) {
				t.Fatalf("record %d reads back as %v, want %v", i, back[i], got[i])
			}
		}
	})
}
