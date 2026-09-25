package policykey_test

import (
	"bytes"
	"crypto/ed25519"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"google.golang.org/protobuf/proto"

	controlv1 "github.com/guardana/control/api/gen/go/guardana/control/v1"
	"github.com/guardana/control/internal/policy"
	"github.com/guardana/control/internal/policy/bundle"
	"github.com/guardana/control/internal/policykey"
)

const document = `{"apiVersion":"agent-policy/v1alpha1",
  "bundle":{"id":"orders-policy","version":"2026-09-24.1","serial":3,"maxStaleSeconds":600},
  "rules":[{"id":"allow-reads","effect":"ALLOW","when":{"action":{"effect":["READ"]}}}]}`

var now = time.Date(2026, 9, 24, 12, 0, 0, 0, time.UTC)

func modeOf(t *testing.T, path string) fs.FileMode {
	t.Helper()
	info, err := os.Lstat(path)
	if err != nil {
		t.Fatalf("stat %s: %v", path, err)
	}
	return info.Mode().Perm()
}

func names(t *testing.T, dir string) []string {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("listing %s: %v", dir, err)
	}
	var out []string
	for _, e := range entries {
		out = append(out, e.Name())
	}
	return out
}

func TestWriteKeyPair(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "keys")
	if err := policykey.WriteKeyPair(dir, rfcKey(t)); err != nil {
		t.Fatalf("WriteKeyPair: %v", err)
	}
	if got := names(t, dir); !slices.Equal(got, []string{"signing.key", "signing.pub"}) {
		t.Errorf("the directory holds %v", got)
	}
	for path, want := range map[string]fs.FileMode{
		dir:                               0o700,
		filepath.Join(dir, "signing.key"): 0o600,
		filepath.Join(dir, "signing.pub"): 0o644,
	} {
		if got := modeOf(t, path); got != want {
			t.Errorf("%s: mode %04o, want %04o", path, got, want)
		}
	}
	pub, err := os.ReadFile(filepath.Clean(filepath.Join(dir, "signing.pub")))
	if err != nil || string(pub) != rfcPublicLine+"\n" {
		t.Errorf("signing.pub holds %q, %v; want the RFC's public line and a newline", pub, err)
	}
	if parsed, err := policykey.ParsePublic(string(pub)); err != nil || string(parsed) != string(mustHex(t, rfcPublicHex)) {
		t.Errorf("signing.pub taken as it is: %x, %v; want the RFC's public key", parsed, err)
	}
	key, err := policykey.ReadPrivate(filepath.Join(dir, "signing.key"))
	if err != nil || !key.Equal(rfcKey(t)) {
		t.Errorf("signing.key does not read back as the key written: %v", err)
	}
}

// TestWriteKeyPairRefusesAnExistingPath: whatever is at the path, keygen
// writes nothing, and a directory already holding a public half keeps it as
// it was rather than gaining a private key nobody printed the public half of.
func TestWriteKeyPairRefusesAnExistingPath(t *testing.T) {
	root := t.TempDir()
	half := filepath.Join(root, "half")
	if err := os.Mkdir(half, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(half, "signing.pub"), []byte("an older key\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(root, "file")
	if err := os.WriteFile(file, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	dangling := filepath.Join(root, "dangling")
	if err := os.Symlink(filepath.Join(root, "nowhere"), dangling); err != nil {
		t.Fatal(err)
	}
	empty := filepath.Join(root, "empty")
	if err := os.Mkdir(empty, 0o700); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{half, file, dangling, empty} {
		if err := policykey.WriteKeyPair(path, rfcKey(t)); !errors.Is(err, policykey.ErrKeyPairExists) {
			t.Errorf("%s: WriteKeyPair = %v, want ErrKeyPairExists", path, err)
		}
	}
	if got := names(t, half); !slices.Equal(got, []string{"signing.pub"}) {
		t.Errorf("the directory holding a public half now holds %v", got)
	}
	if got, _ := os.ReadFile(filepath.Clean(filepath.Join(half, "signing.pub"))); string(got) != "an older key\n" {
		t.Errorf("the public half now holds %q", got)
	}
	if got := names(t, empty); len(got) != 0 {
		t.Errorf("the empty directory now holds %v", got)
	}
	if _, err := os.Lstat(filepath.Join(root, "nowhere")); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("the dangling link's target was created: %v", err)
	}
}

func TestSignBundle(t *testing.T) {
	b, snap, err := policykey.SignBundle([]byte(document), rfcKey(t), now)
	if err != nil {
		t.Fatalf("SignBundle: %v", err)
	}
	if b.GetKeyId() != rfcKeyID {
		t.Errorf("key_id %q, want %q", b.GetKeyId(), rfcKeyID)
	}
	if snap.Serial() != 3 || snap.Ref().GetBundleId() != "orders-policy" || snap.Ref().GetVersion() != "2026-09-24.1" {
		t.Errorf("the snapshot is %v serial %d", snap.Ref(), snap.Serial())
	}
	// The bundle verifies under the RFC's public half, named independently of
	// the code that signed it.
	if _, err := policy.Load(b, bundle.Keyring{rfcKeyID: mustHex(t, rfcPublicHex)}, now); err != nil {
		t.Errorf("Load under the RFC's public key: %v", err)
	}
	if _, err := policy.Load(b, bundle.Keyring{rfcKeyID: otherKey().Public().(ed25519.PublicKey)}, now); !errors.Is(err, policy.ErrSignature) {
		t.Errorf("Load under another key = %v, want ErrSignature", err)
	}
	if _, _, err := policykey.SignBundle([]byte(`{"apiVersion":"agent-policy/v1alpha1"}`), rfcKey(t), now); !errors.Is(err, policy.ErrDocument) {
		t.Errorf("SignBundle(a document Parse refuses) = %v, want ErrDocument", err)
	}
	if _, _, err := policykey.SignBundle([]byte(document), rfcKey(t)[:40], now); err == nil {
		t.Error("SignBundle signed with a key of 40 bytes")
	}
}

// TestWriteBundle: the file is 0644 whatever was there, holds the bundle in
// deterministic bytes, and two signs of one document are the same file.
func TestWriteBundle(t *testing.T) {
	dir := t.TempDir()
	first, second := filepath.Join(dir, "first.bundle"), filepath.Join(dir, "second.bundle")
	if err := os.WriteFile(second, []byte("an older bundle"), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{first, second} {
		b, _, err := policykey.SignBundle([]byte(document), rfcKey(t), now)
		if err != nil {
			t.Fatal(err)
		}
		if err := policykey.WriteBundle(path, b); err != nil {
			t.Fatalf("WriteBundle(%s): %v", path, err)
		}
		if got := modeOf(t, path); got != 0o644 {
			t.Errorf("%s: mode %04o, want 0644", path, got)
		}
	}
	a, _ := os.ReadFile(filepath.Clean(first))
	b, _ := os.ReadFile(filepath.Clean(second))
	if len(a) == 0 || !bytes.Equal(a, b) {
		t.Errorf("two signs of one document differ: %d and %d bytes", len(a), len(b))
	}
	var read controlv1.PolicyBundle
	if err := proto.Unmarshal(a, &read); err != nil {
		t.Fatalf("the file is not a bundle: %v", err)
	}
	if _, err := policy.Load(&read, bundle.Keyring{rfcKeyID: mustHex(t, rfcPublicHex)}, now); err != nil {
		t.Errorf("the written bundle does not load: %v", err)
	}
	if got := names(t, dir); len(got) != 2 {
		t.Errorf("the directory holds %v, want the two bundles and no temporary file", got)
	}
}

// TestIsBundle: a signed bundle is one, and so is nothing else sign could find
// at --out: a key file, a public line, a document, an empty file, and a
// message that decodes but is not a bundle's.
func TestIsBundle(t *testing.T) {
	b, _, err := policykey.SignBundle([]byte(document), rfcKey(t), now)
	if err != nil {
		t.Fatal(err)
	}
	marshal := func(m *controlv1.PolicyBundle) []byte {
		raw, err := proto.Marshal(m)
		if err != nil {
			t.Fatal(err)
		}
		return raw
	}
	good := marshal(b)
	if !policykey.IsBundle(good) {
		t.Fatal("a signed bundle is not one")
	}
	otherAlg, _ := proto.Clone(b).(*controlv1.PolicyBundle)
	otherAlg.SignatureAlg = "ed25519"
	noCanonical, _ := proto.Clone(b).(*controlv1.PolicyBundle)
	noCanonical.Canonical = nil
	key, err := policykey.MarshalPrivate(rfcKey(t))
	if err != nil {
		t.Fatal(err)
	}
	for name, raw := range map[string][]byte{
		"a private key file":         key,
		"a public key file":          []byte(rfcPublicLine + "\n"),
		"a document":                 []byte(document),
		"an empty file":              {},
		"another signature_alg":      marshal(otherAlg),
		"no canonical bytes":         marshal(noCanonical),
		"a bundle with a broken end": append(bytes.Clone(good), 0xff),
	} {
		if policykey.IsBundle(raw) {
			t.Errorf("%s is taken for a bundle", name)
		}
	}
}
