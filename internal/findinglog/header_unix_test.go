//go:build unix

package findinglog

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

var headerLine = regexp.MustCompile(`^\{"logHeader":\{"schemaVersion":"0\.2","logId":"(log-[0-9a-f]{32})"\}\}\n`)

// logID is the id of the header the file at path starts with.
func logID(t *testing.T, path string) string {
	t.Helper()
	m := headerLine.FindSubmatch(contents(t, path))
	if m == nil {
		t.Fatalf("%s does not start with a header: %q", path, contents(t, path))
	}
	return string(m[1])
}

// TestANewLogStartsWithAHeaderOfItsOwn: the header is the file's one line
// until a write, two logs draw two ids, and a reopen neither adds a second
// nor cuts the first.
func TestANewLogStartsWithAHeaderOfItsOwn(t *testing.T) {
	a, b := logDir(t), logDir(t)
	pa, pb := filepath.Join(a, FileName), filepath.Join(b, FileName)
	mustDo(t, openLog(t, a).Close())
	mustDo(t, openLog(t, b).Close())
	if got := contents(t, pa); !headerLine.Match(got) || bytes.Count(got, []byte("\n")) != 1 {
		t.Errorf("a new log holds %q, want its header line alone", got)
	}
	first, second := logID(t, pa), logID(t, pb)
	if first == second {
		t.Errorf("two new logs share the id %s", first)
	}
	if got := readAll(t, a); len(got) != 1 || got[0].GetLogHeader().GetLogId() != first {
		t.Errorf("ReadFile of a log no write reached = %v, want its header alone", got)
	}
	created := contents(t, pa)
	l := openLog(t, a)
	if got := contents(t, pa); !bytes.Equal(got, created) {
		t.Errorf("a reopen changed the log to %q", got)
	}
	write(t, l, finding(idA, confirmed, "repeated_denial"))
	got := readAll(t, a)
	if len(got) != 3 || got[0].GetLogHeader().GetLogId() != first || got[2].GetSuperviseReport() == nil {
		t.Errorf("ReadFile = %v; want the header, the finding and the report", got)
	}
	if !bytes.HasPrefix(contents(t, pa), created) || logID(t, pa) != first {
		t.Error("a write changed the header")
	}
}

// TestALogWithoutAHeaderAppendsWithoutGainingOne: its bytes stay as the 0.9
// writer left them, and it reads as it did.
func TestALogWithoutAHeaderAppendsWithoutGainingOne(t *testing.T) {
	dir := logDir(t)
	path := filepath.Join(dir, FileName)
	writeFile(t, path, finding01+report01)
	l := openLog(t, dir)
	write(t, l, finding(idB, suspected, "outside"))
	got := contents(t, path)
	if !strings.HasPrefix(string(got), finding01+report01) || bytes.Contains(got, []byte("logHeader")) {
		t.Errorf("the old log became %q", got)
	}
	if rs := readAll(t, dir); len(rs) != 4 || rs[0].GetFindingRecord() == nil {
		t.Errorf("ReadFile = %v; want the old write then the new one", rs)
	}
}

// TestAnEmptyLogFileGainsAHeader: it holds no line, so nothing is rewritten.
func TestAnEmptyLogFileGainsAHeader(t *testing.T) {
	dir := logDir(t)
	path := filepath.Join(dir, FileName)
	writeFile(t, path, "")
	mustDo(t, openLog(t, dir).Close())
	logID(t, path)
}

// TestATornHeaderIsATornTail: a crash while the header was written leaves
// a tail ReadFile leaves out and Open cuts, so the log starts again with a
// header of its own.
func TestATornHeaderIsATornTail(t *testing.T) {
	whole := strings.TrimSuffix(header02, "\n")
	for name, tail := range map[string]string{
		"an opening":        `{"logHe`,
		"half a header":     whole[:50],
		"all but a newline": whole,
	} {
		dir := logDir(t)
		path := filepath.Join(dir, FileName)
		writeFile(t, path, tail)
		if got := readAll(t, dir); len(got) != 0 {
			t.Errorf("%s: ReadFile = %v, want nothing", name, got)
		}
		if got := string(contents(t, path)); got != tail {
			t.Errorf("%s: ReadFile changed the file to %q", name, got)
		}
		mustDo(t, openLog(t, dir).Close())
		if id := logID(t, path); id == "log-0123456789abcdef0123456789abcdef" || bytes.Count(contents(t, path), []byte("\n")) != 1 {
			t.Errorf("%s: after Open the log is %q, want one header of a new id", name, contents(t, path))
		}
	}
}

// TestAHeaderAnywhereButTheFirstLineIsDamaged: a writer writes it once, at
// the start of an empty file, so Open refuses it and cuts nothing, whole or
// torn.
func TestAHeaderAnywhereButTheFirstLineIsDamaged(t *testing.T) {
	whole := strings.TrimSuffix(header02, "\n")
	for name, body := range map[string]string{
		"after a write":            finding01 + report01 + header02,
		"twice":                    header02 + header02,
		"inside a write":           header02 + finding01 + header02 + report01,
		"torn after a write":       finding01 + report01 + whole[:40],
		"whole but its newline":    finding01 + report01 + whole,
		"of another version first": strings.Replace(header02, `"0.2"`, `"0.1"`, 1) + report01,
		"of an id of another form": strings.Replace(header02, "0123456789abcdef0123456789abcdef", "0123", 1),
	} {
		dir := logDir(t)
		path := filepath.Join(dir, FileName)
		writeFile(t, path, body)
		if l, err := Open(dir); !errors.Is(err, ErrDamaged) || l != nil {
			t.Errorf("%s: Open = %v, %v; want ErrDamaged", name, l, err)
		}
		if _, err := ReadFile(path); !errors.Is(err, ErrDamaged) {
			t.Errorf("%s: ReadFile = %v; want ErrDamaged", name, err)
		}
		if got := string(contents(t, path)); got != body {
			t.Errorf("%s: the refused Open changed the file", name)
		}
	}
}

// TestAnUnknownSchemaIsRefusedByName: the reader names the field and never
// quotes the line.
func TestAnUnknownSchemaIsRefusedByName(t *testing.T) {
	dir := logDir(t)
	path := filepath.Join(dir, FileName)
	writeFile(t, path, strings.Replace(finding01, `"0.1"`, `"0.7"`, 1)+report01)
	_, err := ReadFile(path)
	if !errors.Is(err, ErrDamaged) || !strings.Contains(err.Error(), "line 1, at byte 0: schema_version:") || strings.Contains(err.Error(), "0.7") {
		t.Errorf("ReadFile = %v; want ErrDamaged naming schema_version and not the value", err)
	}
}

// TestAHeaderThatDoesNotReachTheDiskFailsTheOpen: the cut back leaves the
// file empty, so the next open writes a header of its own.
func TestAHeaderThatDoesNotReachTheDiskFailsTheOpen(t *testing.T) {
	for name, ops := range map[string]fileOps{
		"write": {write: func(f *os.File, b []byte) (int, error) { n, _ := f.Write(b[:10]); return n, errCrash }, sync: (*os.File).Sync, truncate: (*os.File).Truncate},
		"sync":  {write: (*os.File).Write, sync: func(*os.File) error { return errCrash }, truncate: (*os.File).Truncate},
	} {
		dir := logDir(t)
		path := filepath.Join(dir, FileName)
		if l, err := open(dir, ops); !errors.Is(err, ErrWrite) || !errors.Is(err, errCrash) || l != nil {
			t.Errorf("%s: open = %v, %v; want ErrWrite carrying the cause", name, l, err)
		}
		if got := contents(t, path); len(got) != 0 {
			t.Errorf("%s: the failed open left %q", name, got)
		}
		mustDo(t, openLog(t, dir).Close())
		logID(t, path)
	}
}
