package trailfile

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	controlv1 "github.com/guardana/control/api/gen/go/guardana/control/v1"
	"github.com/guardana/control/internal/evidence"
)

func TestOpenCreatesAFileOnlyItsOwnerMayRead(t *testing.T) {
	path := trailPath(t)
	w := openWriter(t, path)
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if perm := info.Mode().Perm(); perm != 0o600 {
		t.Errorf("a new trail file has mode %04o, want 0600", perm)
	}
	mustAppend(t, w, event(1))
	if got := contents(t, path); got != firstLine {
		t.Errorf("the file holds %q, want %q", got, firstLine)
	}
}

// TestOpenRefusesWhatAnotherCouldWrite: each refused case sits beside the one
// accepted that differs from it only in what the refusal is about.
func TestOpenRefusesWhatAnotherCouldWrite(t *testing.T) {
	for name, c := range map[string]struct {
		dirMode, fileMode os.FileMode
		want              error
	}{
		"a directory the group may write":   {dirMode: 0o770, want: ErrDirMode},
		"a directory others may write":      {dirMode: 0o703, want: ErrDirMode},
		"a directory others may read":       {dirMode: 0o755},
		"a file the group may write":        {dirMode: 0o700, fileMode: 0o620, want: ErrFileMode},
		"a file others may write":           {dirMode: 0o700, fileMode: 0o602, want: ErrFileMode},
		"a file the group may read":         {dirMode: 0o700, fileMode: 0o640},
		"an existing file only its owner's": {dirMode: 0o700, fileMode: 0o600},
	} {
		dir := filepath.Join(t.TempDir(), "d")
		if err := os.Mkdir(dir, 0o700); err != nil {
			t.Fatal(err)
		}
		path := filepath.Join(dir, "trail.jsonl")
		if c.fileMode != 0 {
			writeFile(t, path, firstLine)
			if err := os.Chmod(path, c.fileMode); err != nil {
				t.Fatal(err)
			}
		}
		if err := os.Chmod(dir, c.dirMode); err != nil {
			t.Fatal(err)
		}
		w, err := Open(path)
		switch {
		case c.want == nil && err != nil:
			t.Errorf("%s: Open = %v, want it opened", name, err)
		case c.want != nil && (!errors.Is(err, c.want) || w != nil):
			t.Errorf("%s: Open = %v, %v; want %v", name, w, err, c.want)
		}
		if w != nil {
			_ = w.Close()
		}
		_ = os.Chmod(dir, 0o700) //nolint:gosec // G302: a directory needs its execute bit; 0700 is owner-only
	}
}

func TestOpenRefusesWhatIsNotAFile(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "target")
	writeFile(t, target, firstLine)
	link := filepath.Join(dir, "link")
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}
	if w, err := Open(link); err == nil || w != nil {
		t.Errorf("Open of a link = %v, %v; want a refusal", w, err)
	}
	if got := contents(t, target); got != firstLine {
		t.Errorf("the link's target changed to %q", got)
	}
	sub := filepath.Join(dir, "sub")
	if err := os.Mkdir(sub, 0o700); err != nil {
		t.Fatal(err)
	}
	if w, err := Open(sub); err == nil || w != nil {
		t.Errorf("Open of a directory = %v, %v; want a refusal", w, err)
	}
}

func TestOneWriterHoldsTheFile(t *testing.T) {
	path := trailPath(t)
	first, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	if w, err := Open(path); !errors.Is(err, ErrLocked) || w != nil {
		t.Errorf("a second Open = %v, %v; want ErrLocked", w, err)
	}
	if err := first.Close(); err != nil {
		t.Fatal(err)
	}
	openWriter(t, path)
}

// TestATornLastLineIsCutAtOpen: a file cut at every byte of its last line
// opens cut back to the line before, and the record sent again appears once.
func TestATornLastLineIsCutAtOpen(t *testing.T) {
	head := lines(t, event(1))
	last := lines(t, event(2))
	for _, prefix := range []string{head, ""} {
		for cut := range len(last) {
			path := trailPath(t)
			writeFile(t, path, prefix+last[:cut])
			w, err := Open(path)
			if err != nil {
				t.Fatalf("Open of a file cut %d bytes into its last line = %v", cut, err)
			}
			if got := contents(t, path); got != prefix {
				t.Fatalf("cut at %d: the file opened as %q, want %q", cut, got, prefix)
			}
			mustAppend(t, w, event(2))
			if got := contents(t, path); got != prefix+last {
				t.Fatalf("cut at %d: after the record came again the file holds %q", cut, got)
			}
			_ = w.Close()
		}
	}
}

// TestATailLongerThanAnyLineIsDamage: the writer never leaves more than
// evidence.MaxLineBytes after the last newline, so a tail one byte longer is
// refused and left as it is, and a tail of exactly that is cut. Each tail
// opens as a record does, so the bound is what tells them apart.
func TestATailLongerThanAnyLineIsDamage(t *testing.T) {
	head := lines(t, event(1))
	const opening = `{"eventId":"`
	for _, prefix := range []string{head, ""} {
		path := trailPath(t)
		over := prefix + opening + strings.Repeat("x", evidence.MaxLineBytes+1-len(opening))
		writeFile(t, path, over)
		if w, err := Open(path); !errors.Is(err, ErrDamaged) || w != nil {
			t.Errorf("a tail of %d bytes after %q: Open = %v; want ErrDamaged", evidence.MaxLineBytes+1, prefix, err)
		}
		if got := contents(t, path); got != over {
			t.Error("a refused open changed the file")
		}
		writeFile(t, path, prefix+opening+strings.Repeat("x", evidence.MaxLineBytes-len(opening)))
		w, err := Open(path)
		if err != nil {
			t.Fatalf("a tail of exactly the bound: Open = %v", err)
		}
		if got := contents(t, path); got != prefix {
			t.Errorf("a tail of exactly the bound was not cut: %d bytes left", len(got))
		}
		_ = w.Close()
	}
}

// TestATailIsCutOnlyWhenItCanBeARecord: bytes after the last newline, a file
// of one line with none among them, are cut only when they can be the start
// of a line the codec writes: an opening the codec writes, cut anywhere, or a
// whole object that decodes as one event. Anything else is refused and left
// byte for byte. Each refused tail sits beside the nearest one that is cut.
func TestATailIsCutOnlyWhenItCanBeARecord(t *testing.T) {
	record := strings.TrimSuffix(lines(t, event(2)), "\n")
	for _, c := range []struct {
		tail string
		cut  bool
	}{
		{`{"k":1}`, false},
		{`{"k":1`, false},
		{`{"k"`, false},
		{`{"eventId":"e2","k":1}`, false},
		{`{"eventId":1}`, false},
		{`{ "eventId"`, false},
		{`{"eventId":"e2","kind"`, true},
		{`{"kind":"EVENT_KIND_ACTION_PROPOSED","requestId":"r`, true},
		{`{"prevEventDigest":`, true},
		{`{"ki`, true},
		{`{`, true},
		{`{}`, true},
		{record, true},
	} {
		for _, prefix := range []string{"", lines(t, event(1))} {
			opensWithTail(t, prefix, c.tail, c.cut)
		}
	}
}

// TestATailPastTheOpeningIsJudged: a tail that agrees with an opening is cut
// only while it is still inside one JSON value. A whole value with bytes
// after it, a second value begun after the first, and a syntax error after
// the opening are each refused and left; a value cut inside a literal is cut.
func TestATailPastTheOpeningIsJudged(t *testing.T) {
	for _, c := range []struct {
		tail string
		cut  bool
	}{
		{`{"kind":"production"} // settings, keep`, false},
		{`{"eventId":1}x`, false},
		{`{"eventId":"e1"}{"eventId":"e2"`, false},
		{`{"eventId":"e2" "kind"`, false},
		{`{"eventId":"e2","kind":]`, false},
		{`{"eventId":"e2","kind":tru`, true},
		{`{"eventId":"e2","kind":"a\`, true},
		{`{"eventId":"e2","kind":[1,{"a":[`, true},
	} {
		for _, prefix := range []string{"", lines(t, event(1))} {
			opensWithTail(t, prefix, c.tail, c.cut)
		}
	}
}

// TestPrefixOfOneValue holds the token walk to its definition on inputs no
// opening lets through: nothing, white space and a whole scalar are not a
// proper prefix of a value, and a cut literal is.
func TestPrefixOfOneValue(t *testing.T) {
	for in, want := range map[string]bool{
		"": false, " ": false, "1": false, `"a"`: false, "{}{": false, "[]": false,
		"tru": true, `"a`: true, "[": true, "[1,": true, `{"a":{}`: true,
	} {
		if got := prefixOfOneValue([]byte(in)); got != want {
			t.Errorf("prefixOfOneValue(%q) = %t, want %t", in, got, want)
		}
	}
}

// TestEveryCutOfARichLineIsCut: a line the codec wrote holding escapes,
// multi-byte characters, a bool, a number spelled as a string and nested
// objects is cut at open wherever a crash stopped it.
func TestEveryCutOfARichLineIsCut(t *testing.T) {
	ev := event(2)
	ev.RunId = "run \"q\" \\ żółw \U0001F600 \n \x01 <&>"
	ev.Kind = controlv1.EventKind_EVENT_KIND_POLICY_DECIDED
	ev.Payload = &controlv1.Event_Decision{Decision: &controlv1.Decision{
		DecisionLatencyUs: 1234, ReasonCodes: []string{"A", "B"},
		Obligations: []*controlv1.Obligation{{Type: "t", Params: map[string]string{"k": "v"}, Advisory: true}},
	}}
	line := strings.TrimSuffix(lines(t, ev), "\n")
	for _, want := range []string{`\"`, `\\`, `\n`, "true", `"1234"`, "ż", `"decision":{`} {
		if !strings.Contains(line, want) {
			t.Fatalf("the line %q does not hold %q, so it does not test that", line, want)
		}
	}
	head := lines(t, event(1))
	for cut := 1; cut <= len(line); cut++ {
		opensWithTail(t, head, line[:cut], true)
	}
}

// opensWithTail opens a file of prefix and tail, and checks that the tail was
// cut, or that the open was refused as ErrDamaged and the file left whole.
func opensWithTail(t *testing.T, prefix, tail string, cut bool) {
	t.Helper()
	path := trailPath(t)
	writeFile(t, path, prefix+tail)
	w, err := Open(path)
	if w != nil {
		defer func() { _ = w.Close() }()
	}
	want := prefix + tail
	if cut {
		want = prefix
	}
	switch {
	case cut && err != nil:
		t.Errorf("%q after %d bytes: Open = %v, want the tail cut", tail, len(prefix), err)
	case !cut && (!errors.Is(err, ErrDamaged) || w != nil):
		t.Errorf("%q after %d bytes: Open = %v, want ErrDamaged", tail, len(prefix), err)
	case contents(t, path) != want:
		t.Errorf("%q after %d bytes: the file holds %q, want %q", tail, len(prefix), contents(t, path), want)
	}
}

// TestAFailedAppendLeavesNothingBehind: a write cut short and a sync that
// fails each leave the file as it was before the append, and the writer takes
// the record again; without the cut the file would hold it twice.
func TestAFailedAppendLeavesNothingBehind(t *testing.T) {
	for name, ops := range map[string]fileOps{
		"a short write": {write: func(f *os.File, b []byte) (int, error) {
			n, _ := f.Write(b[:len(b)/2])
			return n, errors.New("no space left")
		}, sync: osOps.sync},
		"a write cut short without an error": {write: func(f *os.File, b []byte) (int, error) {
			return f.Write(b[:len(b)/2])
		}, sync: osOps.sync},
		"a failed sync": {write: osOps.write, sync: func(*os.File) error { return errors.New("the disk went away") }},
	} {
		path := trailPath(t)
		w := openWriter(t, path)
		mustAppend(t, w, event(1))
		w.ops = ops
		if err := w.Append(context.Background(), []*controlv1.Event{event(2)}); !errors.Is(err, ErrWrite) {
			t.Errorf("%s: Append = %v, want ErrWrite", name, err)
		}
		if got := contents(t, path); got != firstLine {
			t.Errorf("%s: the failed append left %q", name, got)
		}
		w.ops = osOps
		mustAppend(t, w, event(2))
		if got := contents(t, path); got != firstLine+lines(t, event(2)) {
			t.Errorf("%s: after the append again the file holds %q", name, got)
		}
	}
}

// TestAWriterThatCannotCutBackTakesNoMore: when the cut after a failed append
// fails too, the file may hold part of that append, and every later append is
// refused until the file is opened again.
func TestAWriterThatCannotCutBackTakesNoMore(t *testing.T) {
	path := trailPath(t)
	w, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	mustAppend(t, w, event(1))
	w.ops = fileOps{write: func(f *os.File, b []byte) (int, error) {
		n, _ := f.Write(b[:len(b)/2])
		_ = f.Close()
		return n, errors.New("the device went away")
	}, sync: osOps.sync}
	if err := w.Append(context.Background(), []*controlv1.Event{event(2)}); !errors.Is(err, ErrWrite) {
		t.Fatalf("Append = %v, want ErrWrite", err)
	}
	w.ops = osOps
	if err := w.Append(context.Background(), []*controlv1.Event{event(3)}); !errors.Is(err, ErrFailed) {
		t.Fatalf("an append after a cut that failed = %v, want ErrFailed", err)
	}
	_ = w.Close()
	again := openWriter(t, path)
	if got := contents(t, path); got != firstLine {
		t.Errorf("opening again left %q, want the torn line cut", got)
	}
	mustAppend(t, again, event(2))
}

func TestAppendRefusesWhatItCannotWrite(t *testing.T) {
	var zero Writer
	if err := zero.Append(context.Background(), []*controlv1.Event{event(1)}); !errors.Is(err, ErrClosed) {
		t.Errorf("Append on the zero value = %v, want ErrClosed", err)
	}
	path := trailPath(t)
	w := openWriter(t, path)
	if err := w.Append(context.Background(), []*controlv1.Event{event(1), nil}); !errors.Is(err, ErrEvent) {
		t.Errorf("Append with a nil event = %v, want ErrEvent", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := w.Append(ctx, []*controlv1.Event{event(1)}); !errors.Is(err, context.Canceled) {
		t.Errorf("Append on an ended context = %v", err)
	}
	if got := contents(t, path); got != "" {
		t.Errorf("refused appends left %q", got)
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	if err := w.Append(context.Background(), []*controlv1.Event{event(1)}); !errors.Is(err, ErrClosed) {
		t.Errorf("Append after Close = %v, want ErrClosed", err)
	}
}

// TestAppendsFromManyCallersDoNotInterleave: concurrent appends each land as
// whole lines, every event once.
func TestAppendsFromManyCallersDoNotInterleave(t *testing.T) {
	path := trailPath(t)
	w := openWriter(t, path)
	const callers, each = 8, 25
	var wg sync.WaitGroup
	for c := range callers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := range each {
				if err := w.Append(context.Background(), []*controlv1.Event{event(c*each + i)}); err != nil {
					t.Errorf("Append = %v", err)
				}
			}
		}()
	}
	wg.Wait()
	rep, err := ReadFile(path, DefaultMaxLines)
	if err != nil {
		t.Fatal(err)
	}
	if rep.Lines != callers*each || rep.Duplicates != 0 || len(rep.Trails) != callers*each {
		t.Errorf("read back %d lines, %d duplicates, %d trails; want %d, 0, %d", rep.Lines, rep.Duplicates, len(rep.Trails), callers*each, callers*each)
	}
}
