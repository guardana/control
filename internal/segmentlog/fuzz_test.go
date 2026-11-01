package segmentlog

import (
	"bytes"
	"strings"
	"testing"

	"github.com/guardana/control/internal/brand"
)

// FuzzManifest: a manifest is read only in the one spelling the writer gives
// it, so whatever decodes encodes to the same bytes, and nothing panics.
func FuzzManifest(f *testing.F) {
	f.Add([]byte(manifestText))
	f.Add([]byte(strings.Replace(manifestText, `"state":"sealed"`, `"state":"pruned"`, 1)))
	f.Add([]byte(strings.Replace(manifestText, `"sealed_at":"2027-01-15T08:00:00Z"}`,
		`"sealed_at":"2027-01-15T08:00:00Z","pruned_at":"2027-01-15T08:00:01Z"}`, 1)))
	f.Add([]byte(`{"format":"` + brand.OTelNamespace + `.segments","version":"0.1","tenant_id":"t1","project_id":"p1","dropped":0,"segments":[]}` + "\n"))
	f.Add([]byte(`{}`))
	f.Fuzz(func(t *testing.T, raw []byte) {
		m, err := decodeManifest(raw)
		if err != nil {
			return
		}
		again, err := encodeManifest(m.tenant, m.project, m.dropped, m.segs)
		if err != nil || !bytes.Equal(again, raw) {
			t.Fatalf("decoded and encoded again it is %q (%v), was %q", again, err, raw)
		}
	})
}
