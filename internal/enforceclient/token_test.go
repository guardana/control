package enforceclient_test

import (
	"strings"
	"testing"

	"github.com/guardana/control/internal/enforceclient"
)

func TestIsTokenHoldsTheAlphabetAndTheBound(t *testing.T) {
	for _, tc := range []struct {
		s     string
		limit int
		want  bool
	}{
		{"", 4, true},
		{"aZ09._-", 7, true},
		{"aZ09._-", 6, false},
		{strings.Repeat("k", 64), 64, true},
		{strings.Repeat("k", 65), 64, false},
		// The neighbours of each range and of each punctuation mark.
		{"a/", 4, false},
		{"a:", 4, false},
		{"a@", 4, false},
		{"a[", 4, false},
		{"a`", 4, false},
		{"a{", 4, false},
		{"a,", 4, false},
		{"a^", 4, false},
		{"a b", 4, false},
		{"a\n", 4, false},
		{"é", 4, false},
	} {
		if got := enforceclient.IsToken(tc.s, tc.limit); got != tc.want {
			t.Errorf("IsToken(%q, %d) = %v, want %v", tc.s, tc.limit, got, tc.want)
		}
	}
}
