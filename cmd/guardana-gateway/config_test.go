package main

import (
	"os"
	"strings"
	"testing"

	"github.com/guardana/control/internal/brand"
	"github.com/guardana/control/internal/gatewayconfig"
)

// TestAHeaderComesFromTheEnvironmentAndIsNotPrinted keeps a collector's
// credential out of the file and out of every line the program writes.
func TestAHeaderComesFromTheEnvironmentAndIsNotPrinted(t *testing.T) {
	tr := newTree(t)
	t.Setenv(brand.EnvPrefix+gatewayconfig.EnvName(gatewayconfig.HeadersPrefix)+"X_API_KEY", "s3cret-token")
	cfg := tr.load(t)
	if cfg.Export.Headers["X-Api-Key"] != "s3cret-token" {
		t.Fatalf("export.headers is %v, want the variable's header", cfg.Export.Headers)
	}
	var out strings.Builder
	printSettings(&out, cfg)
	if strings.Contains(out.String(), "s3cret-token") {
		t.Errorf("doctor printed the credential:\n%s", out.String())
	}
	if !strings.Contains(out.String(), "export.headers.X-Api-Key") {
		t.Errorf("doctor does not say the header is set:\n%s", out.String())
	}
}

// TestPublicKeyFixtureMatchesTheSigningKey keeps the fixture honest: the key in
// the committed configuration is the public half of the key the tests sign with.
func TestPublicKeyFixtureMatchesTheSigningKey(t *testing.T) {
	raw, err := os.ReadFile("testdata/doctor.yaml")
	if err != nil {
		t.Fatalf("reading the fixture: %v", err)
	}
	if !strings.Contains(string(raw), fixturePublicKey()) {
		t.Errorf("testdata/doctor.yaml does not carry the public key of the fixture's signing key")
	}
}
