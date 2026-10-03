package main

import (
	"context"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	controlv1 "github.com/guardana/control/api/gen/go/guardana/control/v1"
	"github.com/guardana/control/internal/core"
	"github.com/guardana/control/internal/gateway"
)

// unconfirmed removes the tree's statement, so its plane starts with no
// confirmation.
func (tr tree) unconfirmed(t *testing.T) {
	t.Helper()
	if err := os.Remove(filepath.Join(tr.dir, "policy.statement")); err != nil {
		t.Fatal(err)
	}
}

// policyOf is the answer's policy object.
func policyOf(t *testing.T, body map[string]any) map[string]any {
	t.Helper()
	out, ok := body["policy"].(map[string]any)
	if !ok {
		t.Fatalf("no policy in the answer: %v", body)
	}
	return out
}

// TestHealthSaysHowThePolicyStands: confirmed is ok, with the time the
// statement was issued and when its budget ends; unconfirmed, and a
// confirmation the clock has since carried past its budget, are degraded,
// answered 200, since a restart fixes neither.
func TestHealthSaysHowThePolicyStands(t *testing.T) {
	for _, c := range []struct {
		name      string
		arrange   func(t *testing.T, tr tree)
		later     time.Duration
		freshness string
		status    string
	}{
		{"confirmed", func(*testing.T, tree) {}, 0, "confirmed", "ok"},
		{"unconfirmed", func(t *testing.T, tr tree) { tr.unconfirmed(t) }, 0, "unconfirmed", "degraded"},
		{"expired", func(*testing.T, tree) {}, 11 * time.Minute, "expired", "degraded"},
	} {
		t.Run(c.name, func(t *testing.T) {
			tr := newTree(t)
			c.arrange(t, tr)
			p := tr.plane(t)
			answer, ok := p.health(p.pauseSource(), func() time.Time { return time.Now().Add(c.later) })
			if !ok || answer.Status != c.status || answer.Halted {
				t.Fatalf("ok %t, status %v, halted %v; want true, %s, false", ok, answer.Status, answer.Halted, c.status)
			}
			if c.later == 0 {
				status, body := ask(t, p, "/healthz")
				if status != http.StatusOK || body["status"] != c.status {
					t.Fatalf("answered %d, status %v; want 200, %s", status, body["status"], c.status)
				}
			}
			if answer.Policy.Freshness != c.freshness {
				t.Errorf("policy.freshness is %v, want %s", answer.Policy.Freshness, c.freshness)
			}
			if hasConfirmed := answer.Policy.ConfirmedAt != ""; hasConfirmed != (c.freshness != "unconfirmed") {
				t.Errorf("policy is %+v: confirmed_at present %t under %s", answer.Policy, hasConfirmed, c.freshness)
			}
			if len(answer.Problems) != 0 {
				t.Errorf("a policy that is %s is a problem that halts: %v", c.freshness, answer.Problems)
			}
		})
	}
}

// The confirmation's times are the statement's issuedAt and that plus the
// smaller budget, the fixture's ten minutes.
func TestHealthNamesTheConfirmationAndItsExpiry(t *testing.T) {
	tr := newTree(t)
	issued := time.Now().Add(-time.Minute).Truncate(time.Second).UTC()
	writeStatement(t, filepath.Join(tr.dir, "policy.statement"), filepath.Join(tr.dir, "policy.bundle"), issued)
	_, body := ask(t, tr.plane(t), "/healthz")
	pol := policyOf(t, body)
	if pol["confirmed_at"] != issued.Format(time.RFC3339) || pol["expires_at"] != issued.Add(10*time.Minute).Format(time.RFC3339) {
		t.Errorf("policy is %v, want confirmed at %s and expiring at %s", pol, issued.Format(time.RFC3339), issued.Add(10*time.Minute).Format(time.RFC3339))
	}
	left, _ := pol["seconds_left"].(float64)
	if left < 500 || left > 540 {
		t.Errorf("seconds_left is %v, want about nine minutes", pol["seconds_left"])
	}
}

// TestHaltedWinsOverUnconfirmed: a plane halted by a mismatch answers 503 and
// says halted, unconfirmed as its policy is; the freshness is still told.
func TestHaltedWinsOverUnconfirmed(t *testing.T) {
	tr := newTree(t)
	tr.unconfirmed(t)
	setEnv(t, "policy.fail_open_read", "true")
	p := tr.plane(t)
	d := p.pipeline.Admit(context.Background(), gateway.Admission{Envelope: readEnvelope(), Arguments: []byte(`{"id":"ord-1"}`)})
	if d.Action != core.Execute {
		t.Fatalf("a read under fail_open_read did not run: %v", d.Decision)
	}
	if err := p.pipeline.Close(context.Background(), d, []byte(`{"id":"ord-2"}`),
		&controlv1.ActionResult{Status: controlv1.ResultStatus_RESULT_STATUS_SUCCESS}); err == nil {
		t.Fatal("Close accepted bytes that were not the authorized ones")
	}
	status, body := ask(t, p, "/healthz")
	if status != http.StatusServiceUnavailable || body["status"] != "halted" {
		t.Errorf("a halted, unconfirmed plane answered %d, status %v; want 503, halted", status, body["status"])
	}
	if pol := policyOf(t, body); pol["freshness"] != "unconfirmed" {
		t.Errorf("policy.freshness is %v, want unconfirmed", pol["freshness"])
	}
}

// TestMetricsReportFreshnessAndTheSecondsLeft: the state's gauge reads 1 for
// the state /healthz names, and the seconds left are the budget's remainder
// while confirmed and 0 otherwise.
func TestMetricsReportFreshnessAndTheSecondsLeft(t *testing.T) {
	tr := newTree(t)
	p := tr.plane(t)
	families := scraped(t, p)
	if got := value(t, families, "policy_freshness", map[string]string{"state": "confirmed"}); got != "1" {
		t.Errorf("policy_freshness{state=confirmed} is %s, want 1", got)
	}
	left, err := strconv.Atoi(value(t, families, "policy_confirmation_seconds_left", map[string]string{}))
	if err != nil || left < 590 || left > 600 {
		t.Errorf("policy_confirmation_seconds_left is %d (%v), want the ten-minute budget less the seconds since", left, err)
	}

	tr = newTree(t)
	tr.unconfirmed(t)
	families = scraped(t, tr.plane(t))
	for state, want := range map[string]string{"unconfirmed": "1", "confirmed": "0", "expired": "0"} {
		if got := value(t, families, "policy_freshness", map[string]string{"state": state}); got != want {
			t.Errorf("an unconfirmed plane's policy_freshness{state=%s} is %s, want %s", state, got, want)
		}
	}
	if got := value(t, families, "policy_confirmation_seconds_left", map[string]string{}); got != "0" {
		t.Errorf("an unconfirmed plane has %s seconds left, want 0", got)
	}
}
