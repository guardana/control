package otelgenai

import (
	"bytes"
	"testing"
)

// TestTheImportBoundsArePinned: the reference states a line's bounds as
// 16 MiB and 262,144 array elements.
func TestTheImportBoundsArePinned(t *testing.T) {
	if MaxLineBytes != 16777216 || MaxLineElements != 262144 {
		t.Fatalf("MaxLineBytes = %d, MaxLineElements = %d; want 16777216 and 262144", MaxLineBytes, MaxLineElements)
	}
}

// TestAnOverLongLineIsNotHeld: a line one byte over the bound comes back empty
// and marked too long, one at the bound comes back whole, and the line after
// either is read from its own start.
func TestAnOverLongLineIsNotHeld(t *testing.T) {
	const bound = 16 << 20
	for _, c := range []struct {
		name    string
		length  int
		tooLong bool
	}{
		{"one byte over the bound", bound + 1, true},
		{"at the bound", bound, false},
	} {
		in := append(bytes.Repeat([]byte{'a'}, c.length), "\nnext\n"...)
		l := newLines(bytes.NewReader(in))
		line, tooLong, ok, err := l.next()
		switch {
		case err != nil || !ok || tooLong != c.tooLong:
			t.Errorf("%s: next = tooLong %v, ok %v, %v; want tooLong %v, ok", c.name, tooLong, ok, err, c.tooLong)
		case c.tooLong && len(line) != 0:
			t.Errorf("%s: next held %d bytes of a line it refuses", c.name, len(line))
		case !c.tooLong && len(line) != c.length:
			t.Errorf("%s: next returned %d bytes, want %d", c.name, len(line), c.length)
		}
		wantNext(t, c.name, l, uint64(c.length+6)) //nolint:gosec // G115: a test length, at most 16 MiB and a few bytes
	}
}

// wantNext fails unless the next line of l is "next" and l has then consumed
// consumed bytes.
func wantNext(t *testing.T, name string, l *lines, consumed uint64) {
	t.Helper()
	if line, tooLong, ok, err := l.next(); string(line) != "next" || tooLong || !ok || err != nil {
		t.Errorf("%s: the following line = %q, tooLong %v, ok %v, %v; want \"next\"", name, line, tooLong, ok, err)
	}
	if l.consumed != consumed {
		t.Errorf("%s: consumed %d bytes, want %d", name, l.consumed, consumed)
	}
}
