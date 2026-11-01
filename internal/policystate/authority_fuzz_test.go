package policystate

import (
	"bytes"
	"slices"
	"testing"
)

// FuzzAuthorityFile holds the authority floor file's reader to the floor
// file's properties: no panic, and what it accepts writes back as the same
// bytes.
func FuzzAuthorityFile(f *testing.F) {
	for _, seed := range []string{
		`{"schema_version":"1.0","roster_id":"roster-a","serial":null,"digest":null}`,
		`{"schema_version":"1.0","roster_id":"roster-a","serial":5,"digest":"sha256:5555555555555555555555555555555555555555555555555555555555555555"}`,
		`{"schema_version":"1.0","roster_id":"roster-a","serial":0,"digest":""}`,
		`{"schema_version":"2.0","roster_id":"roster-a","serial":null,"digest":null}`,
		`{"schema_version":"1.0","roster_id":"roster-a","serial":5,"serial":9}`,
	} {
		f.Add([]byte(seed))
	}
	f.Fuzz(func(t *testing.T, raw []byte) {
		floor, err := decodeAuthorityFile(raw)
		if err != nil {
			return
		}
		written, err := encodeAuthorityFile(floor)
		if err != nil {
			t.Fatalf("the writer refused what the reader accepted: %v\n%s", err, raw)
		}
		back, err := decodeAuthorityFile(written)
		if err != nil || back != floor {
			t.Fatalf("%s read back as %+v, %v; want %+v", written, back, err, floor)
		}
		if again, err := encodeAuthorityFile(back); err != nil || !bytes.Equal(again, written) {
			t.Fatalf("a second write differs: %s, %v", again, err)
		}
	})
}

// FuzzAuthorityMarker holds the authority marker's reader to the same
// properties.
func FuzzAuthorityMarker(f *testing.F) {
	for _, seed := range []string{
		`{"schema_version":"1.0","kind":"authority","roster_ids":["roster-a"]}`,
		`{"schema_version":"1.0","kind":"route","roster_ids":["roster-a"]}`,
		`{"schema_version":"1.0","kind":"authority","roster_ids":["roster-b","roster-a"]}`,
		`{"schema_version":"1.0","kind":"authority","roster_ids":[]}`,
	} {
		f.Add([]byte(seed))
	}
	f.Fuzz(func(t *testing.T, raw []byte) {
		ids, err := decodeAuthorityMarker(raw)
		if err != nil {
			return
		}
		written, err := encodeAuthorityMarker(ids)
		if err != nil {
			t.Fatalf("the writer refused what the reader accepted: %v\n%s", err, raw)
		}
		back, err := decodeAuthorityMarker(written)
		if err != nil || !slices.Equal(back, ids) {
			t.Fatalf("%s read back as %v, %v; want %v", written, back, err, ids)
		}
	})
}
