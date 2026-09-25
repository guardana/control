package main

import (
	"bytes"
	"context"
	"encoding/base64"
	"path/filepath"
	"strings"
	"testing"

	"github.com/guardana/control/internal/brand"
	"github.com/guardana/control/internal/policykey"
)

// TestEveryCommandRefusesKeyTextAsAnArgument: a PEM, a key file's body line
// and a control character, alone or as a flag's value, as an argument of every
// command the table lists, are refused before a flag is parsed, with one fixed
// line and the usage status, and reach neither output.
func TestEveryCommandRefusesKeyTextAsAnArgument(t *testing.T) {
	key := fixtureKey()
	pemFile, err := policykey.MarshalPrivate(key)
	if err != nil {
		t.Fatal(err)
	}
	body := strings.Split(string(pemFile), "\n")[1]
	secrets := []string{body, base64.StdEncoding.EncodeToString(key.Seed())}
	dir := t.TempDir()
	for name, text := range map[string]string{
		"a PEM":               string(pemFile),
		"a body line":         body,
		"a body line in path": filepath.Join(dir, body),
		"an escape":           filepath.Join(dir, "a\x1b[2Jb"),
	} {
		for _, c := range commands {
			for _, after := range [][]string{{text}, {"--config", text}, {"--out", text}} {
				var stdout, stderr bytes.Buffer
				code := run(context.Background(), append([]string{c.name}, after...), &stdout, &stderr)
				want := brand.Gateway + " " + c.name + ": " + policykey.ErrKeyText.Error() + "\n"
				if code != exitUsage || stdout.Len() != 0 || stderr.String() != want {
					t.Errorf("%s %v with %s: exit %d, stdout %d bytes, stderr %q; want %d and %q", c.name, after[:len(after)-1], name, code, stdout.Len(), stderr.String(), exitUsage, want)
				}
				for _, secret := range append(secrets, text) {
					if strings.Contains(stdout.String()+stderr.String(), secret) {
						t.Errorf("%s %v with %s: an output repeats it", c.name, after[:len(after)-1], name)
					}
				}
			}
		}
	}
}
