package policy_test

import (
	"strings"
	"testing"

	"github.com/guardana/control/internal/policy"
)

func TestValidID(t *testing.T) {
	for _, c := range []struct {
		name string
		id   string
		want bool
	}{
		{"one byte", "a", true},
		{"1024 bytes", strings.Repeat("a", 1024), true},
		{"1025 bytes", strings.Repeat("a", 1025), false},
		{"empty", "", false},
		{"a line break", "pay\nments", false},
		{"a space at the end", "payments ", false},
		{"invalid UTF-8", "pay\xffments", false},
		{"a format character", "pay\u200bments", false},
	} {
		if got := policy.ValidID(c.id); got != c.want {
			t.Errorf("ValidID of %s = %v, want %v", c.name, got, c.want)
		}
	}
}

func TestMaxSerialIsTheLargestJSONSafeInteger(t *testing.T) {
	if policy.MaxSerial != 9007199254740991 {
		t.Fatalf("MaxSerial = %d, want 2^53-1", int64(policy.MaxSerial))
	}
}
