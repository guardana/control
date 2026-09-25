package metricstest

import (
	"math"
	"strings"
	"testing"
)

// FuzzParse holds the reader to what it promises for any input: no panic,
// and a text it accepts has a type on every family, a finite value on every
// sample, no counter below zero and no family named twice.
func FuzzParse(f *testing.F) {
	f.Add([]byte(valid))
	f.Add([]byte("# HELP a b\n# TYPE a counter\na{c=\"\\\\\\n\\\"\"} 1\n"))
	f.Add([]byte("# HELP a b\n# TYPE a gauge\na -1e3\n"))
	f.Fuzz(func(t *testing.T, text []byte) {
		families, err := Parse(text)
		if err != nil {
			return
		}
		names := map[string]bool{}
		for _, fam := range families {
			if names[fam.Name] || fam.Type == "" || fam.Help == "" {
				t.Fatalf("accepted a family named twice or without help or type: %+v", fam)
			}
			names[fam.Name] = true
			for _, s := range fam.Samples {
				if math.IsNaN(s.Value) || math.IsInf(s.Value, 0) || fam.Type == "counter" && (s.Value < 0 || strings.HasPrefix(s.Raw, "-")) {
					t.Fatalf("accepted %s at %s", fam.Name, s.Raw)
				}
			}
		}
	})
}
