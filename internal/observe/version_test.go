package observe_test

import (
	"errors"
	"strings"
	"testing"

	"github.com/guardana/control/internal/observe"
)

func TestCheckVersion(t *testing.T) {
	cases := []struct {
		v    string
		want string // "" accepts; otherwise a word the refusal carries
	}{
		{"0.1", ""},
		{"", "absent"},
		{"0.2", "minor"},
		{"0.10", "minor"},
		{"0.0", "minor"},
		{"1.1", "major"},
		{"1.0", "major"},
		{"2.1", "major"},
		{"00.1", "malformed"},
		{"0.01", "malformed"},
		{"0.1.0", "malformed"},
		{"0.", "malformed"},
		{".1", "malformed"},
		{"0", "malformed"},
		{"v0.1", "malformed"},
		{" 0.1", "malformed"},
		{"0.1 ", "malformed"},
		{"0.1\n", "malformed"},
		{"+0.1", "malformed"},
		{"0.-1", "malformed"},
		{"0.1234567890", "malformed"},
	}
	for _, c := range cases {
		err := observe.CheckVersion(c.v)
		if c.want == "" {
			if err != nil {
				t.Errorf("CheckVersion(%q) = %v, want nil", c.v, err)
			}
			continue
		}
		if !errors.Is(err, observe.ErrVersion) || !strings.Contains(err.Error(), c.want) {
			t.Errorf("CheckVersion(%q) = %v, want ErrVersion saying %q", c.v, err, c.want)
		}
	}
}

func TestSchemaVersionIsInTheTable(t *testing.T) {
	if observe.SchemaVersion != "0.1" {
		t.Fatalf("SchemaVersion = %q, want 0.1", observe.SchemaVersion)
	}
	if err := observe.CheckVersion(observe.SchemaVersion); err != nil {
		t.Fatalf("the version this package writes does not read: %v", err)
	}
}
