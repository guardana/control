package main

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// newState makes a state directory with -init and returns its path.
func newState(t *testing.T) string {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "s")
	if got := report(t, "", "-state", dir, "-init"); got.code != 0 || got.stdout != "" || got.stderr != "" {
		t.Fatalf("-init: exit %d, stdout %q, stderr %q; want 0 and nothing printed", got.code, got.stdout, got.stderr)
	}
	return dir
}

// consume feeds one export to the state in dir.
func consume(t *testing.T, dir, in string) outcome {
	t.Helper()
	return report(t, in, "-state", dir)
}

// fixture reads a file of testdata.
func fixture(t *testing.T, name string) string {
	t.Helper()
	return readIn(t, "testdata", name)
}

// readIn reads the file name of the directory dir.
func readIn(t *testing.T, dir, name string) string {
	t.Helper()
	root, err := os.OpenRoot(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = root.Close() }()
	b, err := fs.ReadFile(root.FS(), name)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func writeIn(t *testing.T, dir, name, body string) {
	t.Helper()
	root, err := os.OpenRoot(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = root.Close() }()
	if err := root.WriteFile(name, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
}

func alertLog(t *testing.T, dir string) string { return readIn(t, dir, "alerts.jsonl") }

func stateOf(t *testing.T, dir string) string { return readIn(t, dir, "state.json") }

// alertLines is the alerts a run wrote to stderr, one JSON line each.
func alertLines(stderr string) string {
	var out []string
	for _, l := range strings.Split(stderr, "\n") {
		if s, ok := strings.CutPrefix(l, name+": alert "); ok {
			out = append(out, s)
		}
	}
	if len(out) == 0 {
		return ""
	}
	return strings.Join(out, "\n") + "\n"
}

// cursorOf is the cursor the committed export gives the record at offset.
func cursorOf(t *testing.T, export string, offset int64) string {
	t.Helper()
	marker := fmt.Sprintf(`,"offset":%d,"cursor":"`, offset)
	for _, l := range strings.Split(fixture(t, export), "\n") {
		if _, after, ok := strings.Cut(l, marker); ok {
			c, _, _ := strings.Cut(after, `"`)
			return c
		}
	}
	t.Fatalf("%s holds no record at offset %d with a cursor", export, offset)
	return ""
}

// trailerOf is the next cursor the committed export's trailer names.
func trailerOf(t *testing.T, export string) string {
	t.Helper()
	lines := strings.Split(strings.TrimSuffix(fixture(t, export), "\n"), "\n")
	_, after, ok := strings.Cut(lines[len(lines)-1], `"next_cursor":"`)
	if !ok {
		t.Fatalf("%s ends in no trailer naming a next_cursor", export)
	}
	c, _, _ := strings.Cut(after, `"`)
	return c
}

func digest(b string) string {
	sum := sha256.Sum256([]byte(b))
	return hex.EncodeToString(sum[:])
}

// framed is an export of a trail of whole event lines, framed as the
// exporter frames one: the source is the first line's digest and each cursor
// names the line it ends. TestFramedIsTheExporters holds it to the demo's
// committed export.
func framed(file string, all []string, from, to int, limit int) string {
	source := digest(all[0] + "\n")
	var offset int64
	cursorAt := func(i int) string {
		return fmt.Sprintf("v1:%s:%d:%s", source, offset, digest(all[i]+"\n"))
	}
	var after string
	for i := range from {
		offset += int64(len(all[i]) + 1)
		after = cursorAt(i)
	}
	query := fmt.Sprintf(`{"limit":%d}`, limit)
	if after != "" {
		query = fmt.Sprintf(`{"after":"%s","limit":%d}`, after, limit)
	}
	out := []string{fmt.Sprintf(`{"type":"header","format":"%s","version":"1.0","file":"%s","source":"%s","query":%s}`,
		exportFormat, file, source, query)}
	start, next := offset, after
	for i := from; i < to; i++ {
		at := offset
		offset += int64(len(all[i]) + 1)
		next = cursorAt(i)
		out = append(out, fmt.Sprintf(`{"type":"event","offset":%d,"cursor":"%s","event":%s}`, at, next, all[i]))
	}
	out = append(out, fmt.Sprintf(`{"type":"trailer","next_cursor":"%s","end_reached":%t,"tail_bytes":0,"writer_held":false,`+
		`"counts":{"event":%d,"gap":0,"duplicate":0},"scanned_bytes":%d,"dedup_scope":"export"}`,
		next, to == len(all), to-from, offset-start))
	return strings.Join(out, "\n") + "\n"
}

// errCrash stands for the process dying where crashAt is called.
var errCrash = errors.New("the test's crash")

// crashOnce makes the next run stop at point, as a crash there would, and
// the runs after it go on.
func crashOnce(t *testing.T, point string) {
	t.Helper()
	t.Cleanup(func() { crashAt = nil })
	crashAt = func(p string) error {
		if p != point {
			return nil
		}
		crashAt = nil
		return errCrash
	}
}

var cursorMember = regexp.MustCompile(`"cursor":"[^"]*",`)

// withoutCursors is alert lines without their cursors, for an input framed
// here, whose cursors the fixtures' tests already hold to the exporter's.
func withoutCursors(lines string) string { return cursorMember.ReplaceAllString(lines, "") }
