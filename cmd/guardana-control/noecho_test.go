package main

import (
	"bytes"
	"crypto/ed25519"
	"encoding/base64"
	"encoding/hex"
	"encoding/pem"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/guardana/control/internal/brand"
	"github.com/guardana/control/internal/policykey"
)

// secretSpellings is every way the key's secret half could reach an output:
// the seed, the 64-byte private key and the PKCS#8 DER, each raw, in both hex
// cases and in the four base64 alphabets, and every line of the PEM body.
func secretSpellings(t *testing.T, key ed25519.PrivateKey) map[string]string {
	t.Helper()
	pemFile, err := policykey.MarshalPrivate(key)
	if err != nil {
		t.Fatal(err)
	}
	block, _ := pem.Decode(pemFile)
	if block == nil {
		t.Fatal("the key file holds no block")
	}
	out := map[string]string{}
	for name, secret := range map[string][]byte{"seed": key.Seed(), "private key": key, "DER": block.Bytes} {
		out[name+" raw"] = string(secret)
		out[name+" hex"] = hex.EncodeToString(secret)
		out[name+" HEX"] = strings.ToUpper(hex.EncodeToString(secret))
		out[name+" base64"] = base64.StdEncoding.EncodeToString(secret)
		out[name+" base64url"] = base64.URLEncoding.EncodeToString(secret)
		out[name+" raw base64"] = base64.RawStdEncoding.EncodeToString(secret)
		out[name+" raw base64url"] = base64.RawURLEncoding.EncodeToString(secret)
	}
	for i, line := range strings.Split(string(pemFile), "\n") {
		if line != "" && !strings.HasPrefix(line, "-----") {
			out["PEM body line "+string(rune('0'+i))] = line
		}
	}
	return out
}

// outputs is what one run of a command wrote and the status it returned.
type outputs struct {
	code           int
	stdout, stderr string
}

// keyTree is a sign tree whose key file holds body with mode perm.
func keyTree(t *testing.T, body []byte, perm os.FileMode) signTree {
	t.Helper()
	tr := newSignTree(t)
	if err := os.WriteFile(tr.key, body, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(tr.key, perm); err != nil {
		t.Fatal(err)
	}
	return tr
}

// signInto runs sign over tr with the given output path and stdout, nil
// meaning a buffer.
func signInto(tr signTree, out string, stdout io.Writer) outputs {
	var so, se bytes.Buffer
	if stdout == nil {
		stdout = &so
	}
	code := sign(tr.key, out, tr.doc, time.Now(), stdout, &se)
	return outputs{code, so.String(), se.String()}
}

// noEchoPaths is every path the two commands take with one key, each run
// once. Two refusals come after the key was parsed: a bundle that cannot be
// written, and a stdout that cannot be.
func noEchoPaths(key ed25519.PrivateKey, good []byte) map[string]func(t *testing.T) outputs {
	withBody := func(body []byte, perm os.FileMode) func(t *testing.T) outputs {
		return func(t *testing.T) outputs {
			tr := keyTree(t, body, perm)
			return signInto(tr, tr.out, nil)
		}
	}
	return map[string]func(t *testing.T) outputs{
		"keygen": func(t *testing.T) outputs {
			var so, se bytes.Buffer
			code := keygen(filepath.Join(t.TempDir(), "k"), bytes.NewReader(key.Seed()), &so, &se)
			return outputs{code, so.String(), se.String()}
		},
		"keygen, stdout closed": func(t *testing.T) outputs {
			var se bytes.Buffer
			code := keygen(filepath.Join(t.TempDir(), "k"), bytes.NewReader(key.Seed()), failingWriter{}, &se)
			return outputs{code, "", se.String()}
		},
		"sign":                                   withBody(good, 0o600),
		"sign, key readable by others":           withBody(good, 0o604),
		"sign, text before the key":              withBody(append([]byte("prod key\n"), good...), 0o600),
		"sign, a word after the key":             withBody(append(bytes.Clone(good), "end"...), 0o600),
		"sign, the seed alone":                   withBody([]byte(base64.StdEncoding.EncodeToString(key.Seed())+"\n"), 0o600),
		"sign, the key under another block type": withBody(bytes.ReplaceAll(good, []byte("PRIVATE KEY"), []byte("OPENSSH PRIVATE KEY")), 0o600),
		"sign, --out the document": func(t *testing.T) outputs {
			tr := keyTree(t, good, 0o600)
			return signInto(tr, tr.doc, nil)
		},
		"sign, the bundle cannot be written, after the key was parsed": func(t *testing.T) outputs {
			if os.Geteuid() == 0 {
				t.Skip("a read-only directory does not refuse root, so this path would not be reached")
			}
			tr := keyTree(t, good, 0o600)
			locked := filepath.Join(tr.dir, "locked")
			if err := os.Mkdir(locked, 0o500); err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = os.Chmod(locked, 0o700) }) //nolint:gosec // G302: restoring the directory so the test's cleanup can remove it
			o := signInto(tr, filepath.Join(locked, "policy.bundle"), nil)
			if !strings.Contains(o.stderr, "permission denied") {
				t.Errorf("the refusal %q is not the write's, so the path after parsing was not reached", o.stderr)
			}
			return o
		},
		"sign, stdout closed, after the bundle was written": func(t *testing.T) outputs {
			tr := keyTree(t, good, 0o600)
			o := signInto(tr, tr.out, failingWriter{})
			if !strings.Contains(o.stderr, "the bundle was written to") {
				t.Errorf("the refusal %q is not stdout's", o.stderr)
			}
			return o
		},
	}
}

// TestNoOutputHoldsTheKey runs every path above with one fixed key, success
// and refusal alike, and holds both streams to hold no spelling of its secret
// half.
func TestNoOutputHoldsTheKey(t *testing.T) {
	key := ed25519.NewKeyFromSeed(rfcSeed(t))
	good, err := policykey.MarshalPrivate(key)
	if err != nil {
		t.Fatal(err)
	}
	secrets := secretSpellings(t, key)
	for name, path := range noEchoPaths(key, good) {
		t.Run(name, func(t *testing.T) {
			o := path(t)
			want := exitFail
			if name == "keygen" || name == "sign" {
				want = exitOK
			}
			if o.code != want {
				t.Fatalf("exit %d, want %d; stderr %q", o.code, want, o.stderr)
			}
			for what, secret := range secrets {
				if strings.Contains(o.stdout, secret) || strings.Contains(o.stderr, secret) {
					t.Errorf("an output holds the %s", what)
				}
			}
		})
	}
}

// TestKeyTextGivenAsAnArgumentIsNeverRepeated: key text pasted where a path
// belongs, as --key, --out, the document or any argument of either command,
// reaches no output in any encoding. Text holding a PEM marker, a line break or
// the start of a key file's body line is refused before anything reads it; any
// other single line of base64 given as --key is refused as a file that does
// not exist, without the value.
func TestKeyTextGivenAsAnArgumentIsNeverRepeated(t *testing.T) {
	key := ed25519.NewKeyFromSeed(rfcSeed(t))
	good, err := policykey.MarshalPrivate(key)
	if err != nil {
		t.Fatal(err)
	}
	pemText := string(good)
	trimmed := strings.TrimSpace(pemText)
	bodyLine := strings.Split(pemText, "\n")[1]
	seed := base64.StdEncoding.EncodeToString(key.Seed())
	if len(seed) != 44 {
		t.Fatalf("the seed in base64 is %d characters, want 44", len(seed))
	}
	secrets := secretSpellings(t, key)
	tr := newSignTree(t)
	signRefused := brand.CLI + ": policy sign: " + policykey.ErrKeyText.Error()
	keygenRefused := brand.CLI + ": policy keygen: " + policykey.ErrKeyText.Error()
	noSuchKey := brand.CLI + ": policy sign: --key: the key file: no such file or directory"
	for name, c := range map[string]struct {
		args []string
		code int
		line string
	}{
		"sign <PEM>":                   {[]string{"policy", "sign", pemText}, exitUsage, signRefused},
		"sign --key <PEM>":             {[]string{"policy", "sign", "--key", pemText, "--out", tr.out, tr.doc}, exitUsage, signRefused},
		"sign --key=<PEM, trimmed>":    {[]string{"policy", "sign", "--key=" + trimmed, "--out", tr.out, tr.doc}, exitUsage, signRefused},
		"sign --out <PEM>":             {[]string{"policy", "sign", "--key", tr.key, "--out", pemText, tr.doc}, exitUsage, signRefused},
		"sign <PEM as the document>":   {[]string{"policy", "sign", "--key", tr.key, "--out", tr.out, pemText}, exitUsage, signRefused},
		"sign, the marker alone":       {[]string{"policy", "sign", "--key", "-----BEGIN PRIVATE KEY-----", "--out", tr.out, tr.doc}, exitUsage, signRefused},
		"sign, a tab in a path":        {[]string{"policy", "sign", "--key", tr.key + "\t", "--out", tr.out, tr.doc}, exitUsage, signRefused},
		"sign, a C1 control":           {[]string{"policy", "sign", "--key", tr.key, "--out", tr.out + "\u009b", tr.doc}, exitUsage, signRefused},
		"keygen <PEM>":                 {[]string{"policy", "keygen", pemText}, exitUsage, keygenRefused},
		"keygen --out <PEM>":           {[]string{"policy", "keygen", "--out", pemText}, exitUsage, keygenRefused},
		"sign --key <seed in base64>":  {[]string{"policy", "sign", "--key", seed, "--out", tr.out, tr.doc}, exitFail, noSuchKey},
		"sign --key <a PEM body line>": {[]string{"policy", "sign", "--key", bodyLine, "--out", tr.out, tr.doc}, exitUsage, signRefused},
		"sign --key <a body line one character short of the key's prefix>": {
			[]string{"policy", "sign", "--key", bodyLine[:len(policykey.PKCS8Prefix)*8/6-1], "--out", tr.out, tr.doc}, exitFail, noSuchKey,
		},
	} {
		code, stdout, stderr := invoke(t, c.args...)
		if code != c.code || stdout != "" {
			t.Errorf("%s: exit %d, stdout %d bytes; want %d and nothing", name, code, len(stdout), c.code)
		}
		if line := oneStderrLine(t, stderr); line != c.line {
			t.Errorf("%s: stderr is not %q", name, c.line)
		}
		holdsNoSecret(t, name, stderr, secrets)
	}
	if _, err := os.Stat(tr.out); err == nil {
		t.Error("a refused sign wrote the bundle")
	}
}

// TestAKeyBodyLineIsRefusedWherePathsGo: the body line of a key file is the
// whole private key on one line, with no marker and no line break. As the
// document, inside --out or as keygen's --out, which the commands would
// otherwise repeat in a refusal or on success, it is refused before anything
// reads it, for any key.
func TestAKeyBodyLineIsRefusedWherePathsGo(t *testing.T) {
	tr := newSignTree(t)
	for _, key := range []ed25519.PrivateKey{
		ed25519.NewKeyFromSeed(rfcSeed(t)),
		ed25519.NewKeyFromSeed(bytes.Repeat([]byte{0xa7}, ed25519.SeedSize)),
	} {
		good, err := policykey.MarshalPrivate(key)
		if err != nil {
			t.Fatal(err)
		}
		body := strings.Split(string(good), "\n")[1]
		for name, args := range map[string][]string{
			"sign <body as the document>": {"policy", "sign", "--key", tr.key, "--out", tr.out, body},
			"sign --out <body>/x":         {"policy", "sign", "--key", tr.key, "--out", body + "/x", tr.doc},
			"keygen --out <body>":         {"policy", "keygen", "--out", filepath.Join(t.TempDir(), body)},
		} {
			code, stdout, stderr := invoke(t, args...)
			if want := brand.CLI + ": " + args[0] + " " + args[1] + ": " + policykey.ErrKeyText.Error(); code != exitUsage || stdout != "" || oneStderrLine(t, stderr) != want {
				t.Errorf("%s: exit %d, stdout %d bytes, stderr %d bytes; want %d and %q", name, code, len(stdout), len(stderr), exitUsage, want)
			}
			holdsNoSecret(t, name, stdout+stderr, secretSpellings(t, key))
		}
	}
	if _, err := os.Stat(tr.out); err == nil {
		t.Error("a refused sign wrote the bundle")
	}
}

// TestKeyTextAsTheKeyPathIsNeverRepeated: past the argument check, key text
// handed to sign as its key path is refused without the path.
func TestKeyTextAsTheKeyPathIsNeverRepeated(t *testing.T) {
	key := ed25519.NewKeyFromSeed(rfcSeed(t))
	good, err := policykey.MarshalPrivate(key)
	if err != nil {
		t.Fatal(err)
	}
	secrets := secretSpellings(t, key)
	tr := newSignTree(t)
	for name, keyPath := range map[string]string{
		"the PEM":          string(good),
		"the PEM, trimmed": strings.TrimSpace(string(good)),
		"the seed":         base64.StdEncoding.EncodeToString(key.Seed()),
	} {
		var so, se bytes.Buffer
		if code := sign(keyPath, tr.out, tr.doc, time.Now(), &so, &se); code != exitFail {
			t.Errorf("sign with %s as the key path: exit %d", name, code)
		}
		holdsNoSecret(t, name, so.String()+se.String(), secrets)
	}
}

// holdsNoSecret fails the test when output holds any spelling in secrets.
func holdsNoSecret(t *testing.T, name, output string, secrets map[string]string) {
	t.Helper()
	for what, secret := range secrets {
		if strings.Contains(output, secret) {
			t.Errorf("%s: an output holds the %s", name, what)
		}
	}
}

// TestEveryCommandRefusesKeyTextAsAnArgument: a PEM, a key file's body line
// and a control character, alone or after a flag, as an argument of every
// command the table lists, are refused before the command reads anything,
// with one fixed line and the usage status, and reach neither output.
func TestEveryCommandRefusesKeyTextAsAnArgument(t *testing.T) {
	key := ed25519.NewKeyFromSeed(rfcSeed(t))
	good, err := policykey.MarshalPrivate(key)
	if err != nil {
		t.Fatal(err)
	}
	secrets := secretSpellings(t, key)
	dir := t.TempDir()
	for name, text := range map[string]string{
		"a PEM":               string(good),
		"a body line":         strings.Split(string(good), "\n")[1],
		"a body line in path": filepath.Join(dir, strings.Split(string(good), "\n")[1]),
		"an escape":           filepath.Join(dir, "a\x1b[2Jb"),
	} {
		for _, c := range commands {
			for _, after := range [][]string{{text}, {"--out", text}} {
				args := append(strings.Fields(c.words()), after...)
				code, stdout, stderr := invoke(t, args...)
				want := brand.CLI + ": " + c.words() + ": " + policykey.ErrKeyText.Error()
				if code != exitUsage || stdout != "" || stderr != want+"\n" {
					t.Errorf("%s with %s: exit %d, stdout %d bytes, stderr %q; want %d and %q", c.words(), name, code, len(stdout), stderr, exitUsage, want)
				}
				if strings.Contains(stdout+stderr, text) {
					t.Errorf("%s with %s: an output repeats it", c.words(), name)
				}
				holdsNoSecret(t, c.words()+" with "+name, stdout+stderr, secrets)
			}
		}
	}
}
