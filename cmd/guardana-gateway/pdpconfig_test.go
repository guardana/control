package main

import (
	"bytes"
	"context"
	"fmt"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/guardana/control/internal/core"
)

// TestAVariableWinsOverTheFilesHeaderOnEveryAsk: the variable and the file
// spell one header differently, and every question carries the variable's
// value alone.
func TestAVariableWinsOverTheFilesHeaderOnEveryAsk(t *testing.T) {
	dp := newPDPDouble(t, `{"decision":true}`)
	tr := vetoTree(t, dp.url, "  timeout: 5s\n  headers:\n    X-Tenant-Key: from-file\n")
	setEnv(t, "pdp.headers.x_tenant_key", "from-env")
	p := tr.plane(t)
	for i := range 15 {
		admitRead(p, fmt.Sprintf("req-%d", i))
	}
	asked := dp.questions()
	if len(asked) != 15 {
		t.Fatalf("the decision point was asked %d time(s), want 15", len(asked))
	}
	for i, r := range asked {
		if got := r.Header.Values("X-Tenant-Key"); !slices.Equal(got, []string{"from-env"}) {
			t.Errorf("question %d carries X-Tenant-Key %q, want the variable's value alone", i, got)
		}
	}
}

// TestDoctorNeverPrintsAnAddressThatCouldHideACredential: userinfo, a query
// or a fragment in an address withholds it from every line doctor writes,
// and the settings say why.
func TestDoctorNeverPrintsAnAddressThatCouldHideACredential(t *testing.T) {
	for _, c := range []struct{ key, value, config, line string }{
		{"pdp.identifier", "https://operator:s3cret@pdp.example.test/t", "", "pdp.identifier               set, not printed: it carries userinfo"},
		{"pdp.identifier", "https://pdp.example.test/t?api_key=s3cret", "", "pdp.identifier               set, not printed: it carries a query"},
		{"pdp.identifier", "https://pdp.example.test/t#s3cret", "", "pdp.identifier               set, not printed: it carries a fragment"},
		{"pdp.evaluation_endpoint", "https://pdp.example.test/e?api_key=s3cret", "\npdp:\n  identifier: https://pdp.example.test\n",
			"pdp.evaluation_endpoint      set, not printed: it carries a query"},
		{"export.endpoint", "https://collector:s3cret@127.0.0.1:4318/v1/logs", "", "export.endpoint              set, not printed: it carries userinfo"},
		{"upstreams.0.endpoint", "http://127.0.0.1:1/mcp?token=s3cret", "", "orders -> set, not printed: it carries a query"},
	} {
		t.Run(c.key+" "+c.value, func(t *testing.T) {
			tr := newTree(t)
			withConfig(t, tr, c.config)
			setEnv(t, c.key, c.value)
			var out, stderr bytes.Buffer
			doctor(context.Background(), tr.config, &out, &stderr)
			if text := out.String() + stderr.String(); strings.Contains(text, "s3cret") {
				t.Errorf("doctor printed the credential:\n%s", text)
			}
			if !strings.Contains(out.String(), c.line) {
				t.Errorf("doctor does not print %q:\n%s", c.line, out.String())
			}
		})
	}
}

// TestTheConfiguredTimeoutBoundsTheClientAndThePipeline: pdp.timeout is the
// client's timeout and the pipeline's deadline on an ask, both.
func TestTheConfiguredTimeoutBoundsTheClientAndThePipeline(t *testing.T) {
	p := vetoTree(t, "http://127.0.0.1:1", "  timeout: 1500ms\n").plane(t)
	if got := pdpOptions(p.cfg).Timeout; got != 1500*time.Millisecond {
		t.Errorf("the client's timeout is %v, want 1.5s", got)
	}
	mode, err := p.cfg.Mode()
	if err != nil {
		t.Fatalf("Mode: %v", err)
	}
	if got := p.pipelineConfig(p.adapter, mode, p.pdp).DecisionTimeout; got != 1500*time.Millisecond {
		t.Errorf("the pipeline's deadline on an ask is %v, want 1.5s", got)
	}
}

// TestDoctorsDiscoveryGivesUpAtTheConfiguredTimeout: metadata that would
// agree, answered at twice pdp.timeout, is unknown.
func TestDoctorsDiscoveryGivesUpAtTheConfiguredTimeout(t *testing.T) {
	dp := newPDPDouble(t, `{"decision":true}`)
	dp.metadata = `{"policy_decision_point":"` + dp.url + `","access_evaluation_endpoint":"` + dp.url + evaluationPath + `"}`
	dp.delay = 600 * time.Millisecond
	tr := vetoTree(t, dp.url, "  timeout: 300ms\n")
	setEnv(t, "upstreams.0.endpoint", upstream(t))
	setEnv(t, "mode", "OBSERVE")
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	var out, stderr bytes.Buffer
	doctor(ctx, tr.config, &out, &stderr)
	if !strings.Contains(out.String(), "\nunknown pdp") {
		t.Errorf("the pdp line is not unknown with the metadata past pdp.timeout:\n%s", out.String())
	}
}

// TestAnAskAnsweredWithinTheConfiguredTimeoutIsAllowed: an answer at 400ms
// under a 1s pdp.timeout decides the veto.
func TestAnAskAnsweredWithinTheConfiguredTimeoutIsAllowed(t *testing.T) {
	dp := newPDPDouble(t, `{"decision":true}`)
	dp.delay = 400 * time.Millisecond
	p := vetoTree(t, dp.url, "  timeout: 1s\n").plane(t)
	expectPDP(t, admitRead(p, "req-1"), core.Execute, "PDP_ALLOW", dp.url)
}
