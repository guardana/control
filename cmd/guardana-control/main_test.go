package main

import (
	"bytes"
	"fmt"
	"io"
	"strings"
	"testing"

	"github.com/guardana/control/internal/brand"
)

// invoke runs the program in process and returns its exit status and what it
// wrote to each stream.
func invoke(t *testing.T, args ...string) (code int, stdout, stderr string) {
	t.Helper()
	var out, errOut bytes.Buffer
	code = run(args, &out, &errOut)
	return code, out.String(), errOut.String()
}

// invokeCommand runs one command's function past the dispatcher, which
// refuses a control character in any argument before a command sees it. A
// test of a command's own refusal of one starts here.
func invokeCommand(t *testing.T, command func(args []string, stdout, stderr io.Writer) int, args ...string) (code int, stdout, stderr string) {
	t.Helper()
	var out, errOut bytes.Buffer
	code = command(args, &out, &errOut)
	return code, out.String(), errOut.String()
}

// oneStderrLine fails unless stderr holds exactly one line ending in a
// newline, and returns it without the newline.
func oneStderrLine(t *testing.T, stderr string) string {
	t.Helper()
	if !strings.HasSuffix(stderr, "\n") || strings.Count(stderr, "\n") != 1 {
		t.Fatalf("stderr is not one line: %q", stderr)
	}
	return strings.TrimSuffix(stderr, "\n")
}

func TestNoArgumentsPrintsTheVersion(t *testing.T) {
	code, stdout, stderr := invoke(t)
	if code != 0 {
		t.Errorf("exit %d, want 0", code)
	}
	if want := brand.Name + " dev\n" + statusLine + "\n"; stdout != want {
		t.Errorf("stdout = %q, want %q", stdout, want)
	}
	if stderr != "" {
		t.Errorf("stderr = %q, want nothing", stderr)
	}
}

// TestUsageErrorsExitTwoWithTheHelp: a call that names no listed command, or
// gives a flagless one the wrong number of arguments, exits 2 with the whole
// help on stderr and nothing at all on stdout.
func TestUsageErrorsExitTwoWithTheHelp(t *testing.T) {
	for name, args := range map[string][]string{
		"unknown command":       {"serve"},
		"group alone":           {"policy"},
		"policy unknown":        {"policy", "explain", "x.json"},
		"lint without a file":   {"policy", "lint"},
		"lint with two files":   {"policy", "lint", "a.json", "b.json"},
		"test without a dir":    {"policy", "test"},
		"lint with a bare flag": {"policy", "lint", "-h", "x.json"},
		"approvals alone":       {"approvals"},
		"approvals unknown":     {"approvals", "explain", "dir"},
		"list without a dir":    {"approvals", "list"},
		"list with two dirs":    {"approvals", "list", "a", "b"},
	} {
		t.Run(name, func(t *testing.T) {
			code, stdout, stderr := invoke(t, args...)
			if code != 2 {
				t.Errorf("exit %d, want 2", code)
			}
			if stdout != "" {
				t.Errorf("stdout = %q, want nothing", stdout)
			}
			if stderr != helpText() {
				t.Errorf("stderr = %q, want the help %q", stderr, helpText())
			}
		})
	}
}

func TestOneLineQuotesAControlCharacter(t *testing.T) {
	for name, tc := range map[string]struct{ in, want string }{
		"plain":            {"open x.json: no such file", "open x.json: no such file"},
		"newline":          {"open a\nb.json: no such file", `"open a\nb.json: no such file"`},
		"carriage return":  {"a\rb", `"a\rb"`},
		"tab":              {"a\tb", `"a\tb"`},
		"delete":           {"a\x7fb", `"a\x7fb"`},
		"non-ascii stays":  {"rule \"żółw\"", "rule \"żółw\""},
		"quotes stay bare": {`"rules[0]": "rules: empty"`, `"rules[0]": "rules: empty"`},
	} {
		t.Run(name, func(t *testing.T) {
			if got := oneLine(tc.in); got != tc.want {
				t.Errorf("oneLine(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}

// TestOneLineQuotesWhatATerminalWouldObey: a bidirectional control reorders
// what a terminal shows, a C1 control can start an escape sequence, and bytes
// that are not UTF-8 can be read as either, so each comes back quoted, as a C0
// control does. Plain non-ASCII text stays as it is.
func TestOneLineQuotesWhatATerminalWouldObey(t *testing.T) {
	cases := map[string]struct{ in, want string }{
		"a next line, C1":            {"a\u0085b", `"a\u0085b"`},
		"a control sequence intro":   {"a\u009b2Jb", `"a\u009b2Jb"`},
		"an invalid byte":            {"a\xffb", `"a\xffb"`},
		"a truncated sequence":       {"a\xc3", `"a\xc3"`},
		"plain non-ASCII text stays": {"reguła „żółw” — ok", "reguła „żółw” — ok"},
	}
	for _, r := range []rune{0x061c, 0x200e, 0x200f, 0x202a, 0x202b, 0x202c, 0x202d, 0x202e, 0x2066, 0x2067, 0x2068, 0x2069} {
		cases[fmt.Sprintf("bidirectional control U+%04X", r)] = struct{ in, want string }{
			"a" + string(r) + "b", fmt.Sprintf(`"a\u%04xb"`, r),
		}
	}
	for name, tc := range cases {
		if got := oneLine(tc.in); got != tc.want {
			t.Errorf("%s: oneLine(%q) = %q, want %q", name, tc.in, got, tc.want)
		}
	}
}
