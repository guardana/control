package main

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"
)

var errDeviceFull = errors.New("no space left on device")

// fullDevice takes room bytes and refuses the rest, as a full device does.
type fullDevice struct {
	room    int
	written bytes.Buffer
}

func (f *fullDevice) Write(p []byte) (int, error) {
	left := f.room - f.written.Len()
	if len(p) <= left {
		return f.written.Write(p)
	}
	f.written.Write(p[:left])
	return left, errDeviceFull
}

// TestDoctorFailsAReportItCouldNotWrite: a configuration every check passes,
// printed to a stdout that fills at the first line, in the middle of the
// settings and before the last check; each run fails and says why on stderr.
// The same tree with room for the whole report passes, so it is the write
// that fails each of them.
func TestDoctorFailsAReportItCouldNotWrite(t *testing.T) {
	tr := newTree(t)
	setEnv(t, "upstreams.0.endpoint", upstream(t))
	whole := &fullDevice{room: 1 << 20}
	var stderr bytes.Buffer
	if status := doctor(context.Background(), tr.config, whole, &stderr); status != exitOK {
		t.Fatalf("doctor did not pass with room for its report:\n%s\n%s", whole.written.String(), stderr.String())
	}
	size := whole.written.Len()
	for _, room := range []int{0, size / 2, size - 1} {
		out := &fullDevice{room: room}
		stderr.Reset()
		if status := doctor(context.Background(), tr.config, out, &stderr); status == exitOK {
			t.Errorf("room for %d of %d bytes: doctor passed a report it could not write", room, size)
		}
		if !strings.Contains(stderr.String(), errDeviceFull.Error()) {
			t.Errorf("room for %d of %d bytes: stderr does not say the report was not written: %q", room, size, stderr.String())
		}
	}
}
