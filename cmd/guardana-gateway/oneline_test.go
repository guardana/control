package main

import (
	"bytes"
	"crypto/ed25519"
	"fmt"
	"strings"
	"testing"

	"github.com/guardana/control/internal/policykey"
)

// TestOneLineQuotesWhatATerminalWouldObey: a bidirectional control reorders
// what a terminal shows, a C1 control can start an escape sequence, and bytes
// that are not UTF-8 can be read as either, so each comes back quoted, as a C0
// control does. Plain non-ASCII text stays as it is.
func TestOneLineQuotesWhatATerminalWouldObey(t *testing.T) {
	cases := map[string]struct{ in, want string }{
		"a line break":               {"a\nb", `"a\nb"`},
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

// TestOneLineQuotesALineOrParagraphSeparator: many log viewers break a line
// on U+2028 and U+2029, so each quotes the message.
func TestOneLineQuotesALineOrParagraphSeparator(t *testing.T) {
	for in, want := range map[string]string{"a\u2028b": `"a\u2028b"`, "a\u2029b": `"a\u2029b"`} {
		if got := oneLine(in); got != want {
			t.Errorf("oneLine(%q) = %q, want %q", in, got, want)
		}
	}
}

// TestOneLineQuotesAFormatCharacter: a zero width space or another format
// character hides what a line says, so it quotes the message.
func TestOneLineQuotesAFormatCharacter(t *testing.T) {
	for in, want := range map[string]string{"a\u200bb": `"a\u200bb"`, "a\ufeffb": `"a\ufeffb"`, "a\u00adb": `"a\u00adb"`} {
		if got := oneLine(in); got != want {
			t.Errorf("oneLine(%q) = %q, want %q", in, got, want)
		}
	}
}

// TestOneLineWithholdsKeyText: a key file's body line in a message is
// replaced, and the rest of the message is kept.
func TestOneLineWithholdsKeyText(t *testing.T) {
	pemFile, err := policykey.MarshalPrivate(ed25519.NewKeyFromSeed(bytes.Repeat([]byte{9}, ed25519.SeedSize)))
	if err != nil {
		t.Fatal(err)
	}
	body := strings.Split(string(pemFile), "\n")[1]
	if got, want := oneLine("reading "+body+": invalid"), "reading [key text withheld]: invalid"; got != want {
		t.Errorf("oneLine = %q, want %q", got, want)
	}
}
