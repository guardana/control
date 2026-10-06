package reaction_test

import (
	"bytes"
	"errors"
	"testing"
	"time"

	"github.com/guardana/control/internal/reaction"
)

// FuzzJudge: for any bytes, Judge returns a list or a refusal that matches
// exactly one of its sentinels, and never panics. A list it accepts ends its
// prefix at a newline within the bytes, is accepted again from its own
// prefix with nothing changed, and is the list its prefix alone gives.
func FuzzJudge(f *testing.F) {
	r := listRoute(f)
	b := newList(f, r)
	b.stop("fnd-a1", "run-a", clock0, time.Hour)
	b.covered("fnd-a2", "run-a", clock0)
	b.lift("run-a", 2)
	b.stop("fnd-a3", "run-a", clock0, time.Hour)
	f.Add(b.bytes())
	f.Add(append(b.bytes(), `{"kind":"stop"`...))
	f.Add(newList(f, r).bytes())
	f.Add([]byte(headerGolden + "\n" + stopGolden + "\n" + coveredGolden + "\n"))
	f.Add([]byte{})
	f.Fuzz(func(t *testing.T, content []byte) {
		held := bytes.Clone(content)
		l, err := reaction.Judge(r, reaction.Prefix{}, content, clock0, poll)
		if !bytes.Equal(content, held) {
			t.Fatal("Judge changed its input")
		}
		if err != nil {
			if matched := matching(err); matched != 1 {
				t.Fatalf("refusal %v matches %d sentinels", err, matched)
			}
			return
		}
		checkJudged(t, r, content, l)
	})
}

func matching(err error) int {
	matched := 0
	for _, s := range judgeRefusals() {
		if errors.Is(err, s) {
			matched++
		}
	}
	return matched
}

// checkJudged holds an accepted list to its prefix: a newline within the
// bytes ends it, it is accepted again from itself, and alone.
func checkJudged(t *testing.T, r reaction.Route, content []byte, l reaction.List) {
	t.Helper()
	n := l.Prefix().Length()
	if n <= 0 || n > int64(len(content)) || content[n-1] != '\n' || bytes.IndexByte(content[n:], '\n') >= 0 {
		t.Fatalf("prefix of %d bytes in %d", n, len(content))
	}
	again, err := reaction.Judge(r, l.Prefix(), content, clock0, poll)
	if err != nil || again.Prefix() != l.Prefix() || entryRuns(again.Entries()) != entryRuns(l.Entries()) {
		t.Fatalf("judged again from its own prefix: %v", err)
	}
	alone, err := reaction.Judge(r, reaction.Prefix{}, content[:n], clock0, poll)
	if err != nil || alone.Prefix() != l.Prefix() || alone.Usage() != l.Usage() {
		t.Fatalf("its prefix alone: %v", err)
	}
}
