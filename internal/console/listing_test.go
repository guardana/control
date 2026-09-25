package console

import (
	"os"
	"path/filepath"
	"testing"
)

// TestAListingOfOnlyABrokenRecordIsNotEmpty: the one record will not decode,
// so no record is listed, and the page says the listing is incomplete rather
// than that there are no records.
func TestAListingOfOnlyABrokenRecordIsNotEmpty(t *testing.T) {
	dir, _ := newPlane(t)
	if err := os.WriteFile(filepath.Join(dir, "BROKEN.0-held.rec"), []byte("not a record"), 0o600); err != nil {
		t.Fatal(err)
	}
	l := readListing(t, serve(t, dir, ""))
	if len(l.Records) != 0 || l.Complete || l.Error != "" {
		t.Fatalf("the listing reads %+v", l)
	}
	if l.Empty != "" {
		t.Errorf("an incomplete listing with no readable record says %q", l.Empty)
	}
	namesTheBrokenRecord(t, l)
}
