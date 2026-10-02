package policystate

import (
	"bytes"
	"fmt"
	"testing"
	"time"

	"github.com/guardana/control/internal/policy"
)

// FuzzFloorFile holds the floor file's reader to two properties: it never
// panics, and whatever it accepts the writer writes as bytes the reader reads
// back as the same record, so no file reads one way and writes another.
func FuzzFloorFile(f *testing.F) {
	for _, seed := range []string{
		`{"schema_version":"1.0","bundle_id":"bundle-a","serial":null,"digest":null,"issued_at":null,"latest_issued_at":null,"reset_reason":null,"reset_from":null}`,
		`{"schema_version":"1.0","bundle_id":"bundle-a","serial":5,"digest":"sha256:5555555555555555555555555555555555555555555555555555555555555555","issued_at":"2026-09-11T10:00:00Z","latest_issued_at":"2026-09-11T11:00:00Z","reset_reason":null,"reset_from":null}`,
		`{"schema_version":"1.2","bundle_id":"bundle-a","serial":3,"digest":"sha256:3333333333333333333333333333333333333333333333333333333333333333","issued_at":"2026-09-11T09:00:00Z","latest_issued_at":"2026-09-11T09:00:00Z","reset_reason":"serial 5 withdrawn","reset_from":{"serial":5,"digest":"sha256:5555555555555555555555555555555555555555555555555555555555555555","issued_at":"2026-09-11T11:00:00Z","latest_issued_at":"2026-09-11T11:00:00Z"}}`,
		`{"schema_version":"1.0","bundle_id":"bundle-a","serial":null,"digest":null,"issued_at":null,"latest_issued_at":null,"reset_reason":"a new authority","reset_from":{"serial":null,"digest":null,"issued_at":null,"latest_issued_at":null}}`,
		`{"schema_version":"1.0","bundle_id":"bundle-a","serial":5,"serial":9}`,
	} {
		f.Add([]byte(seed))
	}
	f.Fuzz(func(t *testing.T, raw []byte) {
		id, rec, err := decodeFloorFile(raw)
		if err != nil {
			return
		}
		if rec.Floor.BundleID() != id {
			t.Fatalf("accepted a floor of %q in a file of %q", rec.Floor.BundleID(), id)
		}
		written, err := encodeFloorFile(rec)
		if err != nil {
			t.Fatalf("the writer refused what the reader accepted: %v\n%s", err, raw)
		}
		again, back, err := decodeFloorFile(written)
		if err != nil || again != id || !sameRecord(rec, back) {
			t.Fatalf("read back as %q, %+v, %v\nfrom %s\nwritten from %s", again, back, err, written, raw)
		}
		if rewritten, err := encodeFloorFile(back); err != nil || !bytes.Equal(rewritten, written) {
			t.Fatalf("written twice as\n%s\n%s", written, rewritten)
		}
	})
}

// FuzzFloorWritten holds the writer to the reader the other way: whatever
// floor NewFloor makes, and whatever reset is recorded beside it, the writer
// writes as bytes the reader reads back as the same record, or refuses to
// write at all; it never writes what the reader refuses.
func FuzzFloorWritten(f *testing.F) {
	f.Add(int64(5), uint64(5), int64(1789120800), int64(3600), "serial 5 withdrawn", int64(3), false)
	f.Add(int64(1<<53-1), uint64(7), int64(0), int64(0), "", int64(0), false)
	f.Add(int64(1<<53), uint64(7), int64(0), int64(0), "the last serial", int64(1<<53), false)
	f.Add(int64(3), uint64(3), int64(1789120800), int64(0), "the file was lost", int64(0), true)
	f.Fuzz(func(t *testing.T, serial int64, digestWord uint64, issued, renewed int64, reason string, priorSerial int64, missing bool) {
		digest := fmt.Sprintf("sha256:%064x", digestWord)
		at := time.Unix(issued, 0).UTC()
		floor, err := policy.NewFloor("bundle-a", serial, digest, at, at.Add(time.Duration(renewed)*time.Second))
		if err != nil {
			return
		}
		rec := Record{Floor: floor}
		if reason != "" {
			prior, err := policy.NewFloor("bundle-a", priorSerial, digest, at, at)
			if err != nil || missing {
				prior, _ = policy.EmptyFloor("bundle-a")
			}
			rec.Reset = &ResetNote{Reason: reason, From: prior, FileMissing: missing}
		}
		written, err := encodeFloorFile(rec)
		if err != nil {
			if rec.Reset == nil || checkReason(reason) == nil {
				t.Fatalf("the writer refused a floor NewFloor made: %v: %+v", err, rec)
			}
			return
		}
		id, back, err := decodeFloorFile(written)
		if err != nil || id != "bundle-a" || !sameRecord(rec, back) {
			t.Fatalf("written as\n%s\nread back as %q, %+v, %v", written, id, back, err)
		}
	})
}

// FuzzMarker holds the marker's reader to the same properties as the floor
// file's: no panic, and whatever it accepts writes back as bytes it reads
// back the same.
func FuzzMarker(f *testing.F) {
	for _, seed := range []string{
		`{"schema_version":"1.0","kind":"plane","bundle_ids":["bundle-a"]}`,
		`{"schema_version":"1.4","kind":"signer","bundle_ids":["bundle-a","bundle-b"]}`,
		`{"schema_version":"1.0","kind":"plane","bundle_ids":["bundle-b","bundle-a"]}`,
		`{"schema_version":"1.0","kind":"plane","bundle_ids":[]}`,
	} {
		f.Add([]byte(seed))
	}
	f.Fuzz(func(t *testing.T, raw []byte) {
		kind, ids, err := decodeMarker(raw)
		if err != nil {
			return
		}
		written, err := encodeMarker(kind, ids)
		if err != nil {
			t.Fatalf("the writer refused what the reader accepted: %v\n%s", err, raw)
		}
		k2, ids2, err := decodeMarker(written)
		if err != nil || k2 != kind || fmt.Sprint(ids2) != fmt.Sprint(ids) {
			t.Fatalf("read back as %q, %q, %v\nfrom %s", k2, ids2, err, raw)
		}
		if again, err := encodeMarker(k2, ids2); err != nil || !bytes.Equal(again, written) {
			t.Fatalf("written twice as\n%s\n%s", written, again)
		}
	})
}
