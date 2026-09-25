package main

import (
	"crypto/ed25519"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/guardana/control/internal/gatewayconfig"
	"github.com/guardana/control/internal/policy"
	"github.com/guardana/control/internal/policykey"
)

// signedTree replaces the tree's bundle with one signed by signer through the
// package the approver's binary signs with, and configures the plane from the
// lines keygen prints for pinned: the one format, read by the one parser.
func signedTree(t *testing.T, signer, pinned ed25519.PrivateKey) tree {
	t.Helper()
	tr := newTree(t)
	b, _, err := policykey.SignBundle([]byte(fixtureDocument), signer, time.Now())
	if err != nil {
		t.Fatalf("signing: %v", err)
	}
	if err := policykey.WriteBundle(filepath.Join(tr.dir, "policy.bundle"), b); err != nil {
		t.Fatalf("writing the bundle: %v", err)
	}
	pub, _ := pinned.Public().(ed25519.PublicKey)
	for _, line := range strings.Split(strings.TrimSuffix(policykey.ConfigLines(pub), "\n"), "\n") {
		name, value, ok := strings.Cut(line, ": ")
		if !ok {
			t.Fatalf("keygen's line %q is not `name: value`", line)
		}
		setEnv(t, "policy."+name, value)
	}
	return tr
}

func seededKey(b byte) ed25519.PrivateKey {
	seed := make([]byte, ed25519.SeedSize)
	for i := range seed {
		seed[i] = b
	}
	return ed25519.NewKeyFromSeed(seed)
}

func TestAKeyPairAndBundleFromTheSigningPackageStartAPlane(t *testing.T) {
	key := seededKey(11)
	tr := signedTree(t, key, key)
	holder, err := installedPolicy(tr.load(t), time.Now())
	if err != nil {
		t.Fatalf("installedPolicy refused a bundle signed under the pinned key: %v", err)
	}
	if got := holder.Current().Ref().GetBundleId(); got != "gateway-fixture" {
		t.Errorf("the plane serves %q", got)
	}
}

// TestThePublicKeyFileAsItIsStartsAPlane: signing.pub's bytes exactly as
// keygen wrote them, trailing newline included, arrive through the
// environment variable the configuration reads, as a mounted secret does, and
// the plane starts on them.
func TestThePublicKeyFileAsItIsStartsAPlane(t *testing.T) {
	key := seededKey(13)
	tr := signedTree(t, key, key)
	keys := filepath.Join(t.TempDir(), "keys")
	if err := policykey.WriteKeyPair(keys, key); err != nil {
		t.Fatalf("keygen's writer: %v", err)
	}
	raw, err := os.ReadFile(filepath.Clean(filepath.Join(keys, "signing.pub")))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasSuffix(string(raw), "=\n") || strings.Count(string(raw), "\n") != 1 {
		t.Fatalf("signing.pub is %q, not one line and its newline", raw)
	}
	if got := gatewayconfig.EnvName("policy.public_key"); got != "POLICY_PUBLIC_KEY" {
		t.Fatalf("policy.public_key is read from %q", got)
	}
	setEnv(t, "policy.public_key", string(raw))
	cfg := tr.load(t)
	if cfg.Policy.PublicKey != string(raw) {
		t.Fatalf("the configuration holds %q, not the file's bytes", cfg.Policy.PublicKey)
	}
	holder, err := installedPolicy(cfg, time.Now())
	if err != nil {
		t.Fatalf("installedPolicy refused signing.pub as keygen wrote it: %v", err)
	}
	if got := holder.Current().Ref().GetBundleId(); got != "gateway-fixture" {
		t.Errorf("the plane serves %q", got)
	}
	tr.plane(t)
}

// TestAnotherKeyIsRefusedNamingBothIDs: the refusal a rotation produces names
// the id the bundle was signed under and the id the plane pins.
func TestAnotherKeyIsRefusedNamingBothIDs(t *testing.T) {
	signer, pinned := seededKey(11), seededKey(12)
	signerID := policykey.KeyID(signer.Public().(ed25519.PublicKey))
	pinnedID := policykey.KeyID(pinned.Public().(ed25519.PublicKey))
	if signerID == pinnedID || !strings.HasPrefix(signerID, "ed25519-") {
		t.Fatalf("the two keys' ids are %q and %q", signerID, pinnedID)
	}
	_, err := installedPolicy(signedTree(t, signer, pinned).load(t), time.Now())
	if err == nil {
		t.Fatal("installedPolicy accepted a bundle signed under a key it does not pin")
	}
	for _, id := range []string{signerID, pinnedID} {
		if !strings.Contains(err.Error(), `"`+id+`"`) {
			t.Errorf("the refusal %q does not name %s", err, id)
		}
	}
}

// TestPublicKeyTakesTheOneSpelling: a value the standard decoder reads as the
// fixture's key, with padding bits set, is not the line keygen prints and is
// refused before any bundle is read.
func TestPublicKeyTakesTheOneSpelling(t *testing.T) {
	tr := newTree(t)
	setEnv(t, "policy.public_key", strings.TrimSuffix(fixturePublicKey(), "w=")+"x=")
	_, err := installedPolicy(tr.load(t), time.Now())
	if err == nil || !strings.HasPrefix(err.Error(), "policy.public_key: ") {
		t.Errorf("installedPolicy = %v, want a refusal of policy.public_key", err)
	}
}

// TestABundlesKeyIDIsBoundedInTheRefusal: the id comes from a file nothing has
// verified yet, so a long one is cut before it reaches the log line.
func TestABundlesKeyIDIsBoundedInTheRefusal(t *testing.T) {
	tr := newTree(t)
	long := strings.Repeat("k", 64) + "tail"
	b, err := policy.Sign([]byte(fixtureDocument), fixtureKey(), long)
	if err != nil {
		t.Fatal(err)
	}
	if err := policykey.WriteBundle(filepath.Join(tr.dir, "policy.bundle"), b); err != nil {
		t.Fatal(err)
	}
	_, err = installedPolicy(tr.load(t), time.Now())
	if err == nil || !strings.Contains(err.Error(), `"`+strings.Repeat("k", 64)+`..."`) || strings.Contains(err.Error(), "tail") {
		t.Errorf("installedPolicy = %v, want the id cut at 64 bytes", err)
	}
}
