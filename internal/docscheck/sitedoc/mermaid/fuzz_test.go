package mermaid

import (
	"encoding/xml"
	"errors"
	"io"
	"io/fs"
	"strings"
	"testing"
)

func FuzzParse(f *testing.F) {
	inputs, err := fs.Glob(testdata, "*.mmd")
	if err != nil || len(inputs) == 0 {
		f.Fatalf("no seed inputs in testdata: %v", err)
	}
	for _, input := range inputs {
		data, err := fs.ReadFile(testdata, input)
		if err != nil {
			f.Fatalf("reading %s: %v", input, err)
		}
		f.Add(string(data))
	}
	f.Add(head + "  A[a & b] -->|x| B([y]) ==> C[(z)]\n  class A,C cmd\n")
	f.Fuzz(func(t *testing.T, source string) {
		if _, err := Parse(source); err != nil {
			return
		}
		drawn, err := Figure(source, 0)
		if err != nil {
			return
		}
		decoder := xml.NewDecoder(strings.NewReader(drawn))
		for {
			_, err := decoder.Token()
			if errors.Is(err, io.EOF) {
				return
			}
			if err != nil {
				t.Fatalf("an accepted source drew a figure XML cannot read: %v\nsource %q", err, source)
			}
		}
	})
}
