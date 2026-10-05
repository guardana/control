package main

import (
	"bytes"
	"context"
	"log/slog"
	"slices"
	"strings"
	"testing"

	"github.com/guardana/control/internal/secretscan"
)

// withoutProxyVariables empties the proxy variables the HTTP client reads,
// whose userinfo is in the plane's secret set, so the developer's shell does
// not change what a test counts.
func withoutProxyVariables(t *testing.T) {
	t.Helper()
	for _, name := range []string{"HTTP_PROXY", "http_proxy", "HTTPS_PROXY", "https_proxy"} {
		t.Setenv(name, "")
	}
}

// TestTheAdapterConfigCarriesThePlanesSecrets: the configuration run and dev
// hand the adapter carries the secret set built from the plane's own, so an
// upstream's password is found in an answer and a short query value is named
// as not scanned.
func TestTheAdapterConfigCarriesThePlanesSecrets(t *testing.T) {
	tr := newTree(t)
	withoutProxyVariables(t)
	setEnv(t, "upstreams.0.endpoint", "http://op:Pw-7%3Ax@127.0.0.1:1/mcp?v=zQ9wX")
	cfg, err := adapterConfig(tr.load(t), slog.New(slog.DiscardHandler))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Secrets == nil {
		t.Fatal("the adapter's configuration carries no secret set")
	}
	if v := cfg.Secrets.ScanText(`{"echo":"Pw-7:x"}`); v.State != secretscan.Found || v.Key != "upstreams.0.endpoint password" {
		t.Errorf("an answer quoting the upstream's password scanned as %+v, want found under its key", v)
	}
	if v := cfg.Secrets.ScanText("an answer that quotes nothing"); v.State != secretscan.Clean {
		t.Errorf("an answer quoting nothing scanned as %+v, want clean", v)
	}
	if got := cfg.Secrets.NotScanned(); !slices.Equal(got, []string{"upstreams.0.endpoint query value 1"}) {
		t.Errorf("the set does not scan %q, want the short query value's key alone", got)
	}
}

// TestDoctorNamesAValueTooShortToScanByItsKey: doctor names a query value
// shorter than the floor by its key and says it is not scanned, counts the
// values it scans, and prints neither value anywhere.
func TestDoctorNamesAValueTooShortToScanByItsKey(t *testing.T) {
	tr := newTree(t)
	withoutProxyVariables(t)
	setEnv(t, "upstreams.0.endpoint", "http://op:Pw-7%3Ax@127.0.0.1:1/mcp?v=zQ9wX")
	var stdout, stderr bytes.Buffer
	if status := doctor(context.Background(), tr.config, &stdout, &stderr); status == exitOK {
		t.Error("doctor reported a pass although no upstream answered")
	}
	out := stdout.String() + stderr.String()
	for _, value := range []string{"zQ9wX", "Pw-7", "b3A6UHctNzp4"} {
		if strings.Contains(out, value) {
			t.Errorf("doctor printed the configured value %q:\n%s", value, out)
		}
	}
	short := "       upstreams.0.endpoint query value 1 shorter than 8 bytes, not scanned"
	if !slices.Contains(strings.Split(stdout.String(), "\n"), short) {
		t.Errorf("no line %q in:\n%s", short, stdout.String())
	}
	line := doctorLine(t, tr, "ok      secrets ")
	if want := "upstream answers are scanned for 2 configured value(s); 1 value(s) shorter than 8 bytes are not"; !strings.HasSuffix(line, want) {
		t.Errorf("the secrets line is %q, want it to end %q", line, want)
	}
}

// TestDoctorNamesAShortCredentialItMatchesAnyway: a credential shorter than
// the floor is matched whatever its length, so an answer that merely holds it
// is withheld; doctor names it by its key, never its value.
func TestDoctorNamesAShortCredentialItMatchesAnyway(t *testing.T) {
	tr := newTree(t)
	withoutProxyVariables(t)
	setEnv(t, "upstreams.0.endpoint", "http://op:Zq9@127.0.0.1:1/mcp")
	var stdout, stderr bytes.Buffer
	_ = doctor(context.Background(), tr.config, &stdout, &stderr)
	if strings.Contains(stdout.String()+stderr.String(), "Zq9") {
		t.Fatalf("doctor printed the credential:\n%s", stdout.String())
	}
	want := "       upstreams.0.endpoint password shorter than 8 bytes, matched anyway: an answer holding it is withheld"
	if !slices.Contains(strings.Split(stdout.String(), "\n"), want) {
		t.Errorf("no line %q in:\n%s", want, stdout.String())
	}
}
