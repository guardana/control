//go:build unix

package console

import (
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// TestAnAnswerToAnUnreadableRecordIsAConflict: a record file the page's user
// cannot open is a problem whose error is the system's, not the store's. An
// answer to it is refused with 409 in that problem's sentence, as an answer to
// any record the listing names among its problems is, and writes nothing.
func TestAnAnswerToAnUnreadableRecordIsAConflict(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("a file of mode 000 is readable to root, so no record here is unreadable")
	}
	dir, plane := newPlane(t)
	hold(t, plane, "HELD1")
	if err := os.WriteFile(filepath.Join(dir, "LOCKED.0-held.rec"), []byte("x"), 0o000); err != nil {
		t.Fatal(err)
	}
	s := serve(t, dir, "")
	l := readListing(t, s)
	if !slices.ContainsFunc(l.Problems, func(p problemReply) bool {
		return p.Name == "LOCKED.0-held.rec" && strings.Contains(p.Error, "permission denied")
	}) {
		t.Fatalf("no problem says the record cannot be read: %+v", l.Problems)
	}
	a := s.do(t, s.write("/api/approve", `{"id":"LOCKED","reason":"","action_digest":"`+s.digestOf(t, "HELD1")+`"}`))
	if a.status != http.StatusConflict || !strings.Contains(a.body, "permission denied") {
		t.Errorf("an answer to an unreadable record: %d %q, want 409 in the problem's sentence", a.status, a.body)
	}
	if got := s.stateOf(t, "HELD1"); got != pending {
		t.Errorf("the record beside it is %s", got)
	}
	if names, err := os.ReadDir(dir); err != nil || slices.ContainsFunc(names, func(e os.DirEntry) bool { return strings.HasPrefix(e.Name(), "LOCKED.1") }) {
		t.Errorf("the refused answer was filed: %v, %v", names, err)
	}
}
