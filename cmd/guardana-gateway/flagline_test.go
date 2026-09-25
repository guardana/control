package main

import (
	"bytes"
	"context"
	"fmt"
	"strings"
	"testing"
)

// TestAFlagRefusalQuotesWhatBreaksALine: the flag package names an undefined
// flag as it was given, and a separator, a bidirectional control or a format
// character in that name reaches stderr quoted, for every command's flags,
// scenario's own parse among them.
func TestAFlagRefusalQuotesWhatBreaksALine(t *testing.T) {
	for _, code := range []rune{0x2028, 0x2029, 0x202e, 0x200b} {
		r, escaped := string(code), fmt.Sprintf(`\u%04x`, code)
		for _, c := range []struct {
			args []string
			want string
		}{
			{[]string{"collect", "--x" + r + "ok      upstreams     all reachable"},
				`"flag provided but not defined: -x` + escaped + `ok      upstreams     all reachable"`},
			{[]string{"trail", "--x" + r + "y"}, `"flag provided but not defined: -x` + escaped + `y"`},
			{[]string{"doctor", "--config", "c", "--x" + r + "y"}, `"flag provided but not defined: -x` + escaped + `y"`},
			{[]string{"scenario", "run", "--x" + r + "y"}, `"flag provided but not defined: -x` + escaped + `y"`},
		} {
			var stdout, stderr bytes.Buffer
			code := run(context.Background(), c.args, &stdout, &stderr)
			first, _, _ := strings.Cut(stderr.String(), "\n")
			if code != exitUsage || first != c.want {
				t.Errorf("%q: exit %d, first line %q, want %d and %q", c.args, code, first, exitUsage, c.want)
			}
			if strings.Contains(stderr.String(), r) {
				t.Errorf("%q: stderr holds %q raw", c.args, r)
			}
		}
	}
}

// TestAFlagRefusalKeepsItsHelp: what the flag package prints after the
// refusal, and for -h, is the command's help as it always was, newlines and
// tabs included.
func TestAFlagRefusalKeepsItsHelp(t *testing.T) {
	for _, args := range [][]string{{"trail", "--nope"}, {"trail", "-h"}} {
		var stdout, stderr bytes.Buffer
		if code := run(context.Background(), args, &stdout, &stderr); code != exitUsage {
			t.Errorf("%q: exit %d, want %d", args, code, exitUsage)
		}
		if !strings.Contains(stderr.String(), "Usage of ") || strings.Contains(stderr.String(), `"Usage`) || strings.Contains(stderr.String(), `\t`) {
			t.Errorf("%q: the help is not printed as it is:\n%s", args, stderr.String())
		}
	}
	var stdout, stderr bytes.Buffer
	run(context.Background(), []string{"doctor", "-h"}, &stdout, &stderr)
	want := "Usage of " + commandFlags("doctor", &bytes.Buffer{}).Name() + ":\n  -config string\n    \tthe configuration file to read\n"
	if stderr.String() != want {
		t.Errorf("doctor -h printed\n%q\nwant\n%q", stderr.String(), want)
	}
}
