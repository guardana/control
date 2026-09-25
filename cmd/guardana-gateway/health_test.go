package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"slices"
	"testing"
	"time"

	"google.golang.org/protobuf/types/known/timestamppb"

	controlv1 "github.com/guardana/control/api/gen/go/guardana/control/v1"
	"github.com/guardana/control/internal/brand"
	"github.com/guardana/control/internal/core"
	"github.com/guardana/control/internal/gateway"
)

// TestHealthAnswersTheModeAndTheBundle is the answer an operator reads while
// the plane is sound.
func TestHealthAnswersTheModeAndTheBundle(t *testing.T) {
	p := newTree(t).plane(t)
	status, body := ask(t, p, "/healthz")
	if status != http.StatusOK {
		t.Fatalf("a sound plane answered %d: %v", status, body)
	}
	for key, want := range map[string]any{"status": "ok", "mode": "OBSERVE", "adapter": "mcp", "halted": false} {
		if body[key] != want {
			t.Errorf("%s is %v, want %v", key, body[key], want)
		}
	}
	bundle, ok := body["bundle"].(map[string]any)
	if !ok {
		t.Fatalf("no bundle in the answer: %v", body)
	}
	if bundle["id"] != "gateway-fixture" || bundle["serial"] != float64(1) {
		t.Errorf("bundle is %v, want the fixture's id and serial", bundle)
	}
	if _, ok := body["spool"].(map[string]any); !ok {
		t.Errorf("no spool stats in the answer: %v", body)
	}
}

// TestHealthAnswers503WhenTheArgumentsSentWereNotAuthorized is the halt of
// ADR-0013: an execution closed with other bytes stops material calls until
// restart, and the plane says so where a load balancer can read it.
func TestHealthAnswers503WhenTheArgumentsSentWereNotAuthorized(t *testing.T) {
	p := newTree(t).plane(t)
	if _, body := ask(t, p, "/healthz"); body["halted"] != false {
		t.Fatalf("the plane was halted before the test did anything: %v", body)
	}

	disposition := p.pipeline.Admit(context.Background(), gateway.Admission{
		Envelope: readEnvelope(), Arguments: []byte(`{"id":"ord-1"}`),
	})
	if disposition.Action != core.Execute {
		t.Fatalf("the fixture policy did not allow a read: %v", disposition.Decision)
	}
	err := p.pipeline.Close(context.Background(), disposition, []byte(`{"id":"ord-2"}`),
		&controlv1.ActionResult{Status: controlv1.ResultStatus_RESULT_STATUS_SUCCESS})
	if err == nil {
		t.Fatal("Close accepted bytes that were not the authorized ones")
	}

	status, body := ask(t, p, "/healthz")
	if status != http.StatusServiceUnavailable {
		t.Errorf("a halted plane answered %d, want %d", status, http.StatusServiceUnavailable)
	}
	if body["status"] != "halted" || body["halted"] != true {
		t.Errorf("the answer does not say the plane is halted: %v", body)
	}
	halt, ok := body["halt"].(map[string]any)
	if !ok || halt["executed_args_mismatch"] != true {
		t.Errorf("the halt does not name the mismatch: %v", body["halt"])
	}
}

// TestHealthAnswers503WhenTheSpoolCannotAnswer holds the other half of
// invariant 5 at the health answer: a plane whose evidence store cannot report
// on itself is not a plane anything should route to.
func TestHealthAnswers503WhenTheSpoolCannotAnswer(t *testing.T) {
	p := newTree(t).plane(t)
	if err := p.spool.Close(); err != nil {
		t.Fatalf("closing the spool: %v", err)
	}
	status, body := ask(t, p, "/healthz")
	if status != http.StatusServiceUnavailable {
		t.Errorf("a plane with no usable spool answered %d, want %d", status, http.StatusServiceUnavailable)
	}
	problems, ok := body["problems"].([]any)
	if !ok || len(problems) == 0 {
		t.Fatalf("the answer names no problem: %v", body)
	}
}

// TestBrandAnswersWhatTheProductIsCalled keeps the one place a name is served
// from the brand package, not from a literal.
func TestBrandAnswersWhatTheProductIsCalled(t *testing.T) {
	p := newTree(t).plane(t)
	status, body := ask(t, p, "/brand")
	if status != http.StatusOK {
		t.Fatalf("/brand answered %d", status)
	}
	for key, want := range map[string]any{
		"name": brand.Name, "slug": brand.Slug, "command": brand.Gateway,
		"proto_package": brand.ProtoPackage, "otel_namespace": brand.OTelNamespace, "version": version,
	} {
		if body[key] != want {
			t.Errorf("%s is %v, want %v", key, body[key], want)
		}
	}
}

// TestOnUnwritableDecidesWhatAnUnrecordableReadDoes is the risk setting of
// invariant 5, at the one key that turns it on: with a spool that takes
// nothing, a read is blocked with EVIDENCE_UNAVAILABLE under `block` and runs
// unrecorded under `allow_reads`.
func TestOnUnwritableDecidesWhatAnUnrecordableReadDoes(t *testing.T) {
	for _, c := range []struct {
		setting string
		want    core.EnforcementAction
	}{{"block", core.Block}, {"allow_reads", core.Execute}} {
		t.Run(c.setting, func(t *testing.T) {
			tr := newTree(t)
			setEnv(t, "evidence.on_unwritable", c.setting)
			p := tr.plane(t)
			if err := p.spool.Close(); err != nil {
				t.Fatalf("closing the spool: %v", err)
			}
			d := p.pipeline.Admit(context.Background(), gateway.Admission{
				Envelope: readEnvelope(), Arguments: []byte(`{}`),
			})
			if d.Action != c.want {
				t.Fatalf("a read under %s is %v, want %v (decision %v)", c.setting, d.Action, c.want, d.Decision)
			}
			if c.want == core.Block && !slices.Contains(d.Decision.GetReasonCodes(), "EVIDENCE_UNAVAILABLE") {
				t.Errorf("the block does not name EVIDENCE_UNAVAILABLE: %v", d.Decision.GetReasonCodes())
			}
			if reads := p.pipeline.Stats().ReadsUnrecorded; (reads > 0) != (c.setting == "allow_reads") {
				t.Errorf("%d unrecorded read(s) under %s", reads, c.setting)
			}
		})
	}
}

// TestFailOpenReadReachesTheKernel is the other risk setting: the key has to
// arrive in the kernel's options, and a decision made under it says so. The
// snapshot is past its staleness budget here, which is the availability cause
// the setting is about.
func TestFailOpenReadReachesTheKernel(t *testing.T) {
	for _, setting := range []string{"false", "true"} {
		t.Run(setting, func(t *testing.T) {
			tr := newTree(t)
			setEnv(t, "policy.max_stale", "1ns")
			setEnv(t, "policy.fail_open_read", setting)
			p := tr.plane(t)
			d := p.pipeline.Admit(context.Background(), gateway.Admission{
				Envelope: readEnvelope(), Arguments: []byte(`{}`),
			})
			codes := d.Decision.GetReasonCodes()
			if !slices.Contains(codes, "POLICY_STALE") {
				t.Fatalf("the snapshot was not past its budget: %v", codes)
			}
			if got := slices.Contains(codes, "FAIL_OPEN_READ_CONFIGURED"); got != (setting == "true") {
				t.Errorf("the decision under fail_open_read=%s carries %v", setting, codes)
			}
		})
	}
}

// TestApprovalCountersReadEachKeyFromItsOwnField gives every counter a value
// no other has, so a key read from the wrong field shows.
func TestApprovalCountersReadEachKeyFromItsOwnField(t *testing.T) {
	out := approvalCounters("memory", gateway.Stats{
		HoldsClosed: 11, HoldsUnmeasured: 13, UnheldRecords: 17, JournalRefusals: 19,
		HeldTrailsLeftOpen: 23, MultiUseRefused: 29, Mismatches: 31, ReadsUnrecorded: 37,
		SinkFailuresBeforeEffect: 41, SinkFailuresAfterEffect: 43, Pending: 47, Executed: 53,
		Open: 59, Held: 61, HoldJournal: true, ReconcileIncomplete: true,
	})
	for key, want := range map[string]uint64{
		"holds_closed": 11, "holds_unmeasured": 13, "unheld_records": 17,
		"journal_refusals": 19, "held_trails_left_open": 23,
	} {
		if got, ok := out[key].(uint64); !ok || got != want {
			t.Errorf("%s is %v, want %d", key, out[key], want)
		}
	}
}

// ask puts one request through the health mux and decodes the answer.
func ask(t *testing.T, p *plane, path string) (int, map[string]any) {
	t.Helper()
	recorder := httptest.NewRecorder()
	p.healthMux().ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, path, nil))
	var body map[string]any
	if err := json.Unmarshal(recorder.Body.Bytes(), &body); err != nil {
		t.Fatalf("the answer is not JSON: %v: %q", err, recorder.Body.String())
	}
	return recorder.Code, body
}

// readEnvelope is one proposed read, as an adapter would translate it.
func readEnvelope() *controlv1.ActionEnvelope {
	return &controlv1.ActionEnvelope{
		SchemaVersion: "1.0",
		RequestId:     "req-1",
		ProjectId:     "orders",
		TenantId:      "acme",
		Environment:   "dev",
		OccurredAt:    timestamppb.New(time.Date(2026, time.September, 20, 9, 0, 0, 0, time.UTC)),
		Principal:     &controlv1.Principal{Id: "agent-runner", Type: "service", TenantId: "acme"},
		Agent:         &controlv1.Agent{Id: "orders-assistant"},
		Action:        &controlv1.Action{Name: "orders.read", Effect: controlv1.EffectClass_EFFECT_CLASS_READ, Provider: "orders"},
		Resource:      &controlv1.Resource{Type: "order", Id: "ord-1", TenantId: "acme", Environment: "dev"},
		Context:       &controlv1.RunContext{RunId: "run-1"},
	}
}
