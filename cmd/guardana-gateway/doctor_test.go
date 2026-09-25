package main

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestDoctorPrintsEveryCheckAndStopsAtTheFirstUnknown runs the committed
// fixture, whose upstream is not there, and pins the whole shape: one line per
// check, every bound with where it came from, and a non-zero status at the
// check that could not be made.
func TestDoctorPrintsEveryCheckAndStopsAtTheFirstUnknown(t *testing.T) {
	tr := newTree(t)
	var stdout, stderr bytes.Buffer
	status := doctor(context.Background(), tr.config, &stdout, &stderr)
	if status == exitOK {
		t.Error("doctor reported a pass although no upstream answered")
	}
	golden(t, "doctor.golden", tr.output(stdout.String()))
	if !strings.Contains(stderr.String(), "doctor: upstreams:") {
		t.Errorf("the refusal on stderr does not name the check: %q", stderr.String())
	}
}

// TestDoctorRefusesABundleTheKeyDoesNotVerify is the negative control of the
// policy check: the fixture's key id with a well-formed public key that did
// not sign the bundle must not pass, and what fails is the policy check's
// signature verification.
func TestDoctorRefusesABundleTheKeyDoesNotVerify(t *testing.T) {
	tr := newTree(t)
	setEnv(t, "policy.public_key", unsignedPublicKey())
	var stdout, stderr bytes.Buffer
	if status := doctor(context.Background(), tr.config, &stdout, &stderr); status == exitOK {
		t.Fatal("doctor verified a bundle under a key that did not sign it")
	}
	if !strings.Contains(stdout.String(), "fail    policy") || !strings.Contains(stdout.String(), "policy: the signature does not verify") {
		t.Errorf("the policy check did not fail on the signature:\n%s", stdout.String())
	}
}

// TestDoctorRefusesAPinnedIdTheBundleDoesNotCarry holds the Holder's pin: the
// bundle verifies and is still refused, because the plane serves one bundle id.
func TestDoctorRefusesAPinnedIdTheBundleDoesNotCarry(t *testing.T) {
	tr := newTree(t)
	setEnv(t, "policy.bundle_id", "another-bundle")
	var stdout, stderr bytes.Buffer
	if status := doctor(context.Background(), tr.config, &stdout, &stderr); status == exitOK {
		t.Fatal("doctor accepted a bundle whose id is not the pinned one")
	}
	if !strings.Contains(stdout.String(), "fail    policy") {
		t.Errorf("the policy check did not fail:\n%s", stdout.String())
	}
}

// TestDoctorReachesAnUpstreamAndItsFingerprintClassifies is the round trip an
// operator makes: doctor names an unclassified tool and its fingerprint, that
// fingerprint in an override classifies it, and outside OBSERVE an unclassified
// tool is not a pass.
func TestDoctorReachesAnUpstreamAndItsFingerprintClassifies(t *testing.T) {
	tr := newTree(t)
	setEnv(t, "upstreams.0.endpoint", upstream(t))
	var first bytes.Buffer
	if status := doctor(context.Background(), tr.config, &first, &first); status != exitOK {
		t.Fatalf("doctor did not pass under OBSERVE with an unclassified tool:\n%s", first.String())
	}
	if !strings.Contains(first.String(), "1 tool(s) listed, 0 classified, 1 unclassified") {
		t.Fatalf("the upstream check did not report the listed tool:\n%s", first.String())
	}

	// The same configuration in ENFORCE: an unclassified tool is blocked in
	// every mode but OBSERVE, so doctor must not report a pass.
	setEnv(t, "mode", "ENFORCE")
	var second bytes.Buffer
	if status := doctor(context.Background(), tr.config, &second, &second); status == exitOK {
		t.Fatalf("doctor passed in ENFORCE with an unclassified tool:\n%s", second.String())
	}

	// With the fingerprint doctor printed, the operator's override classifies
	// the tool and the same check passes.
	withOverride(t, tr, fingerprintOf(t, first.String(), "read_order"))
	var third bytes.Buffer
	if status := doctor(context.Background(), tr.config, &third, &third); status != exitOK {
		t.Fatalf("doctor refused a configuration whose override classifies every tool:\n%s", third.String())
	}
	if !strings.Contains(third.String(), "1 tool(s) listed, 1 classified, 0 unclassified") {
		t.Errorf("the override did not classify the tool:\n%s", third.String())
	}
}

// TestDoctorRefusesAnOverrideOfAnotherDefinition is the fingerprint's own
// negative control: a classification of a definition the upstream no longer
// serves classifies nothing.
func TestDoctorRefusesAnOverrideOfAnotherDefinition(t *testing.T) {
	tr := newTree(t)
	setEnv(t, "upstreams.0.endpoint", upstream(t))
	setEnv(t, "mode", "ENFORCE")
	withOverride(t, tr, strings.Repeat("0", 64))
	var out bytes.Buffer
	if status := doctor(context.Background(), tr.config, &out, &out); status == exitOK {
		t.Fatalf("doctor passed with an override that pins another definition:\n%s", out.String())
	}
	if !strings.Contains(out.String(), "is unclassified") {
		t.Errorf("doctor did not say the tool is unclassified:\n%s", out.String())
	}
}

// TestDoctorRefusesASpoolDirectoryAnotherPlaneHolds is the lock: two planes on
// one directory would interleave their records (ADR-0014).
func TestDoctorRefusesASpoolDirectoryAnotherPlaneHolds(t *testing.T) {
	tr := newTree(t)
	tr.plane(t)
	var out bytes.Buffer
	if status := doctor(context.Background(), tr.config, &out, &out); status == exitOK {
		t.Fatal("doctor opened a spool directory another plane holds")
	}
	if !strings.Contains(out.String(), "fail    evidence") {
		t.Errorf("the evidence check did not fail:\n%s", out.String())
	}
}

// TestDoctorRefusesAMissingSpoolDirectory keeps the directory the operator's:
// the plane does not create it, because a typo would then be a spool nobody
// watches.
func TestDoctorRefusesAMissingSpoolDirectory(t *testing.T) {
	tr := newTree(t)
	setEnv(t, "evidence.dir", filepath.Join(tr.dir, "nowhere"))
	var out bytes.Buffer
	if status := doctor(context.Background(), tr.config, &out, &out); status == exitOK {
		t.Fatal("doctor accepted a spool directory that does not exist")
	}
}

// TestDoctorRefusesASpoolDirectoryItCannotWriteIn is the other half of the
// evidence check: the directory lock is taken on a directory this process may
// only read, so the lock alone would report a spool that cannot take a record.
func TestDoctorRefusesASpoolDirectoryItCannotWriteIn(t *testing.T) {
	tr := newTree(t)
	dir := filepath.Join(tr.dir, "spool")
	if err := os.Chmod(dir, 0o500); err != nil { //nolint:gosec // G302: the point of the test is a directory this process cannot write in
		t.Fatalf("taking write permission off the directory: %v", err)
	}
	t.Cleanup(func() {
		if err := os.Chmod(dir, 0o750); err != nil { //nolint:gosec // G302: put back so the temporary tree can be removed
			t.Errorf("restoring the directory: %v", err)
		}
	})
	if err := writable(dir); err == nil {
		t.Skip("this process writes in a directory with no write permission; the check cannot be exercised here")
	}
	var out bytes.Buffer
	if status := doctor(context.Background(), tr.config, &out, &out); status == exitOK {
		t.Fatalf("doctor passed on a spool directory it cannot write in:\n%s", out.String())
	}
	if !strings.Contains(out.String(), "not writable") {
		t.Errorf("the evidence check does not say the directory is not writable:\n%s", out.String())
	}
}

// withOverride appends one classification to the tree's configuration file,
// which is how an operator writes one.
func withOverride(t *testing.T, tr tree, fingerprint string) {
	t.Helper()
	raw, err := os.ReadFile(filepath.Clean(tr.config))
	if err != nil {
		t.Fatalf("reading the configuration: %v", err)
	}
	override := "\noverrides:\n  - upstream: orders\n    tool: read_order\n    fingerprint: " + fingerprint +
		"\n    effect: READ\n    resource_type: order\n    resource_from: /id\n"
	if err := os.WriteFile(filepath.Clean(tr.config), append(raw, override...), 0o600); err != nil { //nolint:gosec // G703: the configuration under the test's temp dir
		t.Fatalf("writing the configuration: %v", err)
	}
}
