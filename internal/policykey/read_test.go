package policykey_test

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/guardana/control/internal/files"
	"github.com/guardana/control/internal/policykey"
)

// TestReadPrivateModeTable: the mode is read from the opened descriptor, and
// every bit for the group or for others refuses on its own, write and execute
// bits as much as read bits.
func TestReadPrivateModeTable(t *testing.T) {
	good, err := policykey.MarshalPrivate(rfcKey(t))
	if err != nil {
		t.Fatal(err)
	}
	for _, perm := range []os.FileMode{0o400, 0o600, 0o700} {
		key, err := policykey.ReadPrivate(writeWithMode(t, good, perm))
		if err != nil || !key.Equal(rfcKey(t)) {
			t.Errorf("mode %04o: ReadPrivate = %v, want the key", perm, err)
		}
	}
	for _, perm := range []os.FileMode{0o640, 0o620, 0o610, 0o604, 0o602, 0o601, 0o660, 0o644} {
		key, err := policykey.ReadPrivate(writeWithMode(t, good, perm))
		if key != nil || !errors.Is(err, policykey.ErrKeyFileMode) || !errors.Is(err, files.ErrMode) {
			t.Errorf("mode %04o: ReadPrivate = %v, want ErrKeyFileMode", perm, err)
		}
	}
}

// TestReadPrivateBound: a valid key padded with white space to exactly 4096
// bytes is read, and one of 4097 bytes is refused before it is parsed.
func TestReadPrivateBound(t *testing.T) {
	good, err := policykey.MarshalPrivate(rfcKey(t))
	if err != nil {
		t.Fatal(err)
	}
	at := append(bytes.Clone(good), bytes.Repeat([]byte{'\n'}, 4096-len(good))...)
	if key, err := policykey.ReadPrivate(writeWithMode(t, at, 0o600)); err != nil || !key.Equal(rfcKey(t)) {
		t.Errorf("a key file of exactly 4096 bytes: %v", err)
	}
	over := append(bytes.Clone(at), '\n')
	if _, err := policykey.ReadPrivate(writeWithMode(t, over, 0o600)); !errors.Is(err, files.ErrTooLarge) {
		t.Errorf("a key file of 4097 bytes: %v, want ErrTooLarge", err)
	}
}

func TestReadPrivateRefusesWhatIsNotAKeyFile(t *testing.T) {
	if _, err := policykey.ReadPrivate(writeWithMode(t, nil, 0o600)); !errors.Is(err, policykey.ErrEmpty) {
		t.Errorf("an empty file: %v, want ErrEmpty", err)
	}
	dir := filepath.Join(t.TempDir(), "keys")
	if err := os.Mkdir(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	if _, err := policykey.ReadPrivate(dir); !errors.Is(err, files.ErrNotRegular) {
		t.Errorf("a directory: %v, want ErrNotRegular", err)
	}
	missing := filepath.Join(t.TempDir(), "none")
	if _, err := policykey.ReadPrivate(missing); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("a missing file: %v, want ErrNotExist", err)
	}
}

// TestReadPrivateNamesNeitherPathNorContent: a path argument may be key text
// put where a path belongs, so a refusal names the check alone. A refusal of
// the content is the bare constant, and one of the file carries only the
// cause, never the path.
func TestReadPrivateNamesNeitherPathNorContent(t *testing.T) {
	for name, c := range refusalCases(t) {
		path := writeWithMode(t, []byte(c.raw), 0o600)
		_, err := policykey.ReadPrivate(path)
		if got, ok := err.(policykey.Error); !ok || got != c.want { //nolint:errorlint // the refusal must be the bare constant
			t.Errorf("%s: ReadPrivate = %v, want exactly %q", name, err, c.want)
		}
	}
	dir := t.TempDir()
	good, err := policykey.MarshalPrivate(rfcKey(t))
	if err != nil {
		t.Fatal(err)
	}
	for name, c := range map[string]struct {
		path string
		want string
	}{
		"a missing file": {filepath.Join(dir, "absent-key-file"), "the key file: no such file or directory"},
		"a directory":    {dir, "the key file: files: not a regular file: a directory"},
		"a mode refused": {writeWithMode(t, good, 0o640), string(policykey.ErrKeyFileMode) + " (files: the mode gives access the caller forbids: mode 0640)"},
	} {
		_, err := policykey.ReadPrivate(c.path)
		if err == nil || err.Error() != c.want {
			t.Errorf("%s: ReadPrivate says %v, want %q", name, err, c.want)
		}
		if err != nil && strings.Contains(err.Error(), filepath.Base(c.path)) {
			t.Errorf("%s: the refusal names the path: %q", name, err.Error())
		}
	}
}
