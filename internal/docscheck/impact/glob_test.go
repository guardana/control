package impact

import (
	"errors"
	"strings"
	"testing"
	"time"
)

func TestGlobMatch(t *testing.T) {
	for _, tc := range []struct {
		pattern, path string
		want          bool
	}{
		{"cmd/**", "cmd/gw/main.go", true},
		{"cmd/**", "cmd/gw/internal/deep/x.go", true},
		{"cmd/**", "cmd/main.go", true},
		{"cmd/**", "cmdx/main.go", false},
		{"cmd/**", "internal/cmd/main.go", false},
		{"cmd/*", "cmd/main.go", true},
		{"cmd/*", "cmd/gw/main.go", false},
		{"**/chain.go", "chain.go", true},
		{"**/chain.go", "internal/evidence/chain.go", true},
		{"**/chain.go", "internal/evidence/chain_test.go", false},
		{"internal/**/*_test.go", "internal/a/b/c_test.go", true},
		{"internal/**/*_test.go", "internal/a/b/c.go", false},
		{"internal/evidence/chain.go", "internal/evidence/chain.go", true},
		{"internal/evidence/chain.go", "internal/evidence/chain.go.bak", false},
		{"scripts/lib/*.sh", "scripts/lib/repo-files.sh", true},
		{"api/proto/**", "api/proto", true},
	} {
		g, err := Compile(tc.pattern)
		if err != nil {
			t.Fatalf("Compile(%q): %v", tc.pattern, err)
		}
		if got := g.Match(tc.path); got != tc.want {
			t.Errorf("%q matches %q = %v, want %v", tc.pattern, tc.path, got, tc.want)
		}
	}
}

func TestCompileRefusals(t *testing.T) {
	for name, pattern := range map[string]string{
		"empty":                   "",
		"a double star in a name": "cmd/gw**",
		"an empty segment":        "cmd//main.go",
		"a trailing slash":        "cmd/",
		"an unclosed class":       "cmd/[a-",
	} {
		t.Run(name, func(t *testing.T) {
			_, err := Compile(pattern)
			if !errors.Is(err, ErrBadGlob) {
				t.Fatalf("Compile(%q) = %v, want ErrBadGlob", pattern, err)
			}
		})
	}
}

func TestAGlobCompileWouldRefuseMatchesNothing(t *testing.T) {
	for name, g := range map[string]Glob{
		"the zero Glob":     {},
		"an unclosed class": {text: "cmd/[a-", segments: []string{"cmd", "[a-"}},
	} {
		t.Run(name, func(t *testing.T) {
			for _, p := range []string{"", "cmd", "cmd/[a-", "cmd/a", "cmd/gw/main.go"} {
				if g.Match(p) {
					t.Errorf("%s matches %q", name, p)
				}
			}
		})
	}
}

func TestCompileCollapsesConsecutiveAnyDepth(t *testing.T) {
	g, err := Compile("cmd/**/**/**/main.go")
	if err != nil {
		t.Fatalf("Compile: %v", err)
	}
	if len(g.segments) != 3 {
		t.Errorf("segments = %q, want cmd, ** and main.go", g.segments)
	}
	if g.String() != "cmd/**/**/**/main.go" {
		t.Errorf("String() = %q, want the pattern as declared", g)
	}
	for p, want := range map[string]bool{"cmd/main.go": true, "cmd/a/b/main.go": true, "cmd/main.go.bak": false} {
		if got := g.Match(p); got != want {
			t.Errorf("matches %q = %v, want %v", p, got, want)
		}
	}
}

func TestCompileBoundsAnyDepthAtFour(t *testing.T) {
	four := "**/a/**/b/**/c/**/d"
	if _, err := Compile(four); err != nil {
		t.Fatalf("Compile(%q) = %v, want four ** admitted", four, err)
	}
	five := four + "/**/e"
	_, err := Compile(five)
	if !errors.Is(err, ErrBadGlob) || !strings.Contains(err.Error(), "four") {
		t.Errorf("Compile(%q) = %v, want ErrBadGlob naming the bound of four", five, err)
	}
}

// A match runs in time linear in the path, so a pattern of many ** against
// a long path that fails at its last segment finishes.
func TestMatchFinishesOnManyAnyDepthAgainstALongPath(t *testing.T) {
	deep := strings.Repeat("a/", 40) + "a"
	for name, pattern := range map[string]string{
		"twelve consecutive":   strings.Repeat("**/", 12) + "z",
		"four between letters": "**/a/**/a/**/a/**/z",
	} {
		t.Run(name, func(t *testing.T) {
			g, err := Compile(pattern)
			if err != nil {
				t.Fatalf("Compile(%q): %v", pattern, err)
			}
			done := make(chan bool, 1)
			go func() { done <- g.Match(deep) }()
			select {
			case got := <-done:
				if got {
					t.Errorf("%q matches %q; the last segment is not z", pattern, deep)
				}
			case <-time.After(5 * time.Second):
				t.Fatalf("%q against a 41-segment path did not finish", pattern)
			}
		})
	}
}
