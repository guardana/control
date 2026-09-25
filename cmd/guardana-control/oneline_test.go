package main

import (
	"bytes"
	"crypto/ed25519"
	"strings"
	"testing"

	"github.com/guardana/control/internal/policykey"
)

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
