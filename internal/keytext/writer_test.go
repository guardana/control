package keytext_test

import (
	"bytes"
	"errors"
	"os/exec"
	"slices"
	"strings"
	"testing"

	"github.com/guardana/control/internal/keytext"
)

// TestTheWriterPassesEachCellThroughPrintable: every run of text between the
// newlines and tabs of a write reaches the writer beneath with its key text
// withheld and quoted when it breaks a line, and the newlines and tabs stay.
func TestTheWriterPassesEachCellThroughPrintable(t *testing.T) {
	_, body, _ := keyTexts(t)
	var out bytes.Buffer
	in := "Usage of x:\n  -flag\tthe " + body + "\n\tbad \u202e name\n"
	n, err := keytext.Writer(&out).Write([]byte(in))
	want := "Usage of x:\n  -flag\tthe " + keytext.Withheld + "\n\t" + `"bad \u202e name"` + "\n"
	if n != len(in) || err != nil || out.String() != want {
		t.Errorf("Write = %d, %v; wrote %q, want %d, nil and %q", n, err, out.String(), len(in), want)
	}
}

type failingWriter struct{}

var errWrite = errors.New("the stream is closed")

func (failingWriter) Write([]byte) (int, error) { return 0, errWrite }

// TestTheWriterReportsAFailedWrite: a write the writer beneath refuses is
// reported as nothing written, with its error.
func TestTheWriterReportsAFailedWrite(t *testing.T) {
	if n, err := keytext.Writer(failingWriter{}).Write([]byte("a\tb\n")); n != 0 || !errors.Is(err, errWrite) {
		t.Errorf("Write = %d, %v; want 0 and %v", n, err, errWrite)
	}
}

// TestThePackageLinksOnlyTheStandardLibrary walks the non-test build of this
// package transitively: a binary that must link no key reader, and no other
// module, uses it.
func TestThePackageLinksOnlyTheStandardLibrary(t *testing.T) {
	out, err := exec.Command("go", "list", "-deps", "-f", "{{if not .Standard}}{{.ImportPath}}{{end}}", ".").Output()
	if err != nil {
		t.Fatalf("go list -deps: %v", err)
	}
	own, err := exec.Command("go", "list", ".").Output()
	if err != nil {
		t.Fatalf("go list: %v", err)
	}
	if got, want := strings.Fields(string(out)), strings.Fields(string(own)); len(want) != 1 || !slices.Equal(got, want) {
		t.Errorf("outside the standard library the package links %q, want itself alone, %q", got, want)
	}
}
