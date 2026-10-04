package observe_test

import (
	"bytes"
	"errors"
	"testing"

	"github.com/guardana/control/internal/observe"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
)

// FuzzReadDescriptor: a descriptor that reads, written again by protojson,
// reads to the same descriptor, and a refusal is always ErrDescriptor.
func FuzzReadDescriptor(f *testing.F) {
	fixture := readTestdata(f, "descriptor.json")
	f.Add(fixture)
	f.Add(bytes.Replace(fixture, []byte(`"source_id"`), []byte(`"sourceId"`), 1))
	f.Add(bytes.Replace(fixture, []byte(`"TRUST_SELF_REPORTED"`), []byte(`3`), 1))
	f.Add([]byte(`{"source_id":"a","sourceId":"b"}`))
	f.Add([]byte(`{"select":null}`))
	f.Fuzz(func(t *testing.T, b []byte) {
		d, err := observe.ReadDescriptor(b)
		if err != nil {
			if !errors.Is(err, observe.ErrDescriptor) || d != nil {
				t.Fatalf("refusal %v with %v, want ErrDescriptor and nothing", err, d)
			}
			return
		}
		again, err := protojson.Marshal(d)
		if err != nil {
			t.Fatalf("an accepted descriptor does not encode: %v", err)
		}
		d2, err := observe.ReadDescriptor(again)
		if err != nil || !proto.Equal(d, d2) {
			t.Fatalf("an accepted descriptor written again reads as %v, %v", d2, err)
		}
	})
}

// FuzzUnmarshalLine: a line that reads is a record the writer writes, whose
// line is in the writer's form and reads back to the same record; a refusal
// is always a codec error.
func FuzzUnmarshalLine(f *testing.F) {
	for _, line := range fixtureLines(f) {
		f.Add(line)
		f.Add(bytes.TrimSuffix(line, []byte("\n")))
	}
	f.Add([]byte(`{"observation":null}`))
	f.Add([]byte(`{}`))
	f.Fuzz(func(t *testing.T, line []byte) {
		r, err := observe.UnmarshalLine(line)
		if err != nil {
			if (!errors.Is(err, observe.ErrLine) && !errors.Is(err, observe.ErrLineTooLong)) || r != nil {
				t.Fatalf("refusal %v with %v, want a codec error and nothing", err, r)
			}
			return
		}
		out, err := observe.MarshalLine(r)
		if errors.Is(err, observe.ErrLineTooLong) {
			return
		}
		if err != nil {
			t.Fatalf("an accepted record does not write: %v", err)
		}
		if err := observe.CheckLineForm(out); err != nil {
			t.Fatalf("the writer's line is not in its form: %v", err)
		}
		back, err := observe.UnmarshalLine(out)
		if err != nil || !proto.Equal(back, r) {
			t.Fatalf("written again it reads as %v, %v", back, err)
		}
	})
}
