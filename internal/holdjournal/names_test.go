package holdjournal

import (
	"errors"
	"strings"
	"testing"
)

// hostileIDs are request ids a name encoding has to survive. None of them is
// trusted to be a file name; each has to come back identical.
var hostileIDs = []string{
	"req-1",
	"a",
	"../../etc/passwd",
	"..",
	".",
	"journal.meta",
	"tmp.x.part",
	"with a space",
	"with/a/separator",
	"with\\a\\backslash",
	"a\nnewline",
	"a\x00nul",
	"ünïcödé",
	strings.Repeat("r", MaxRequestIDBytes),
}

// TestARequestIDComesBackOutOfItsName: the id is encoded, and the name it
// takes decodes to exactly the id it came from, so nothing is ever filed under
// a name that reads back as another request.
func TestARequestIDComesBackOutOfItsName(t *testing.T) {
	for _, id := range hostileIDs {
		name, err := encodeName(id)
		if err != nil {
			t.Fatalf("encoding %q: %v", id, err)
		}
		switch {
		case strings.ContainsAny(name, `/\`):
			t.Fatalf("%q names a path: %q", id, name)
		case strings.HasPrefix(name, "."), strings.HasPrefix(name, tmpPrefix):
			t.Fatalf("%q takes a name this package reads as something else: %q", id, name)
		case name == markerFile:
			t.Fatalf("%q takes the marker's name", id)
		}
		back, ok := decodeName(name)
		if !ok || back != id {
			t.Fatalf("%q came back as %q (ok %v)", id, back, ok)
		}
		kind, named := classify(name)
		if kind != kindEntry || named != id {
			t.Fatalf("the name of %q classifies as %v naming %q", id, kind, named)
		}
	}
}

// TestTwoRequestIDsNeverShareOneName: the encoding is single-case, so two ids
// that differ only in case do not collide on a file system that ignores case.
func TestTwoRequestIDsNeverShareOneName(t *testing.T) {
	ids := append([]string{"req-a", "req-A", "REQ-A", "Req-a"}, hostileIDs...)
	seen := make(map[string]string, len(ids))
	for _, id := range ids {
		name, err := encodeName(id)
		if err != nil {
			t.Fatalf("encoding %q: %v", id, err)
		}
		folded := strings.ToUpper(name)
		if other, taken := seen[folded]; taken && other != id {
			t.Fatalf("%q and %q share the name %q", id, other, name)
		}
		seen[folded] = id
	}
	if len(seen) != len(ids) {
		t.Fatalf("%d ids took %d names", len(ids), len(seen))
	}
}

// TestARequestIDThatCannotNameAFileIsRefused: the bound at which an id stops
// being a name, and the id one byte under it that still is one.
func TestARequestIDThatCannotNameAFileIsRefused(t *testing.T) {
	for _, id := range []string{"", strings.Repeat("r", MaxRequestIDBytes+1)} {
		if _, err := encodeName(id); !errors.Is(err, ErrRequestName) {
			t.Fatalf("encoding a %d-byte id: %v, want ErrRequestName", len(id), err)
		}
	}
	if _, err := encodeName(strings.Repeat("r", MaxRequestIDBytes)); err != nil {
		t.Fatalf("encoding an id at the bound: %v", err)
	}
}

// TestANameThisPackageDidNotWriteIsForeign: a file whose name is not exactly
// what the encoding produces is never read as an entry, whatever it decodes
// to.
func TestANameThisPackageDidNotWriteIsForeign(t *testing.T) {
	valid, err := encodeName("req-1")
	if err != nil {
		t.Fatalf("encoding a request id: %v", err)
	}
	body := strings.TrimSuffix(valid, entrySuffix)
	cases := map[string]string{
		"a name in the other case":       strings.ToLower(body) + entrySuffix,
		"a padded name":                  body + "=" + entrySuffix,
		"a name with no suffix":          body,
		"a name with another suffix":     body + ".json",
		"a name that decodes to nothing": nameEncoding.EncodeToString(nil) + entrySuffix,
		"somebody else's file":           "notes.txt",
		"a name outside the alphabet":    "!!!!" + entrySuffix,
	}
	for name, candidate := range cases {
		if kind, _ := classify(candidate); kind != kindForeign {
			t.Fatalf("%s (%q) classifies as %v, want foreign", name, candidate, kind)
		}
	}
	if kind, id := classify(valid); kind != kindEntry || id != "req-1" {
		t.Fatalf("the name this package writes classifies as %v naming %q", kind, id)
	}
	if kind, _ := classify(markerFile); kind != kindMarker {
		t.Fatalf("the marker classifies as %v", kind)
	}
	if kind, _ := classify(tmpPrefix + "abc" + tmpSuffix); kind != kindTemp {
		t.Fatalf("a temporary file classifies as %v", kind)
	}
}
