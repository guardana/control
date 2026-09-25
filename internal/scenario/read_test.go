package scenario_test

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	controlv1 "github.com/guardana/control/api/gen/go/guardana/control/v1"
	"github.com/guardana/control/internal/pause"
	"github.com/guardana/control/internal/scenario"
)

const everyStep = `{
  "about": "Every step kind, once.",
  "kind": "agent-scenario/v1alpha1",
  "plane": {"mode": "APPROVE", "bundle": {"id": "demo", "digest": "sha256:0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"}},
  "run": "continues",
  "steps": [
    {"call": {
      "tool": "read_file",
      "args": {"path": "notes.txt", "n": 12345678901234567890, "f": 1e400},
      "answer": {"kind": "result", "codes": []},
      "decided": {"verdict": "ALLOW_WITH_OBLIGATIONS", "codes": ["OBLIGATIONS_ATTACHED", "RULE_ALLOW"], "obligations": [
        {"type": "redact_fields", "params": {"fields": ["x"]}, "advisory": false},
        {"type": "emit_alert", "params": {}, "advisory": true}]},
      "trail": {"request": "new", "kinds": ["ACTION_PROPOSED", "POLICY_DECIDED", "ACTION_STARTED", "ACTION_COMPLETED"]}
    }},
    {"call": {
      "tool": "send_mail",
      "args": {"body": "${step[0].output}"},
      "answer": {"kind": "pending", "codes": ["APPROVAL_PENDING"]},
      "decided": {"verdict": "REQUIRE_APPROVAL", "codes": ["APPROVAL_REQUIRED"], "obligations": []},
      "trail": {"request": "new", "kinds": ["ACTION_PROPOSED", "POLICY_DECIDED", "APPROVAL_REQUESTED"]}
    }},
    {"approve": {"step": 1, "approver": "alice", "reason": "looked"}},
    {"call": {
      "tool": "send_mail",
      "args": {"body": "${step[0].output}"},
      "answer": {"kind": "pending", "codes": ["APPROVAL_PENDING"]},
      "decided": "none",
      "trail": {"request": "step[1]", "kinds": []}
    }},
    {"reject": {"step": 3, "approver": "bob"}},
    {"pause": {"provider": "mail", "action": "tool", "name": "send", "reason": "demo"}},
    {"pause": {"global": true}},
    {"pause": {"provider": "files"}},
    {"pause": {"provider": "files", "action": "prompt"}},
    {"unpause": {"step": 5}},
    {"call": {
      "tool": "read_file",
      "args": {},
      "answer": {"kind": "blocked", "codes": ["RULE_DENY", "NO_MATCHING_RULE"]},
      "decided": {"verdict": "DENY", "codes": ["RULE_DENY"], "obligations": []},
      "trail": {"request": "new", "kinds": ["ACTION_PROPOSED", "POLICY_DECIDED", "ACTION_BLOCKED"]}
    }}
  ]
}
`

// TestReadEveryStepKind reads one document holding each step kind and holds
// the result to the structure written out by hand, lists in written order.
func TestReadEveryStepKind(t *testing.T) {
	got, err := scenario.Read("every-step-0.json", []byte(everyStep))
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	scenario.ForgetReferences(got)
	want := &scenario.Scenario{
		ID:    "every-step-0.json",
		About: "Every step kind, once.",
		Plane: scenario.Plane{
			Mode:         "APPROVE",
			BundleID:     "demo",
			BundleDigest: "sha256:0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef",
		},
		Run: scenario.RunContinues,
		Steps: []scenario.Step{
			{Call: &scenario.Call{
				Tool:   "read_file",
				Args:   []byte(`{"path": "notes.txt", "n": 12345678901234567890, "f": 1e400}`),
				Answer: scenario.Answer{Kind: scenario.AnswerResult, Codes: []string{}},
				Decided: scenario.Decided{
					Verdict: controlv1.Verdict_VERDICT_ALLOW_WITH_OBLIGATIONS,
					Codes:   []string{"OBLIGATIONS_ATTACHED", "RULE_ALLOW"},
					Obligations: []scenario.Obligation{
						{Type: "redact_fields", Params: []byte(`{"fields": ["x"]}`)},
						{Type: "emit_alert", Params: []byte(`{}`), Advisory: true},
					},
				},
				Trail: scenario.Trail{Request: scenario.NewRequest, Kinds: []controlv1.EventKind{
					controlv1.EventKind_EVENT_KIND_ACTION_PROPOSED, controlv1.EventKind_EVENT_KIND_POLICY_DECIDED,
					controlv1.EventKind_EVENT_KIND_ACTION_STARTED, controlv1.EventKind_EVENT_KIND_ACTION_COMPLETED,
				}},
			}},
			{Call: &scenario.Call{
				Tool:   "send_mail",
				Args:   []byte(`{"body": "${step[0].output}"}`),
				Answer: scenario.Answer{Kind: scenario.AnswerPending, Codes: []string{"APPROVAL_PENDING"}},
				Decided: scenario.Decided{
					Verdict:     controlv1.Verdict_VERDICT_REQUIRE_APPROVAL,
					Codes:       []string{"APPROVAL_REQUIRED"},
					Obligations: []scenario.Obligation{},
				},
				Trail: scenario.Trail{Request: scenario.NewRequest, Kinds: []controlv1.EventKind{
					controlv1.EventKind_EVENT_KIND_ACTION_PROPOSED, controlv1.EventKind_EVENT_KIND_POLICY_DECIDED,
					controlv1.EventKind_EVENT_KIND_APPROVAL_REQUESTED,
				}},
			}},
			{Approve: &scenario.Approval{Step: 1, Approver: "alice", Reason: "looked"}},
			{Call: &scenario.Call{
				Tool:    "send_mail",
				Args:    []byte(`{"body": "${step[0].output}"}`),
				Answer:  scenario.Answer{Kind: scenario.AnswerPending, Codes: []string{"APPROVAL_PENDING"}},
				Decided: scenario.Decided{None: true},
				Trail:   scenario.Trail{Request: 1, Kinds: []controlv1.EventKind{}},
			}},
			{Reject: &scenario.Approval{Step: 3, Approver: "bob"}},
			{Pause: &scenario.Pause{Scope: pause.Scope{Kind: pause.ScopeAction, Provider: "mail", Action: "tool", Name: "send"}, Reason: "demo"}},
			{Pause: &scenario.Pause{Scope: pause.Scope{Kind: pause.ScopeGlobal}}},
			{Pause: &scenario.Pause{Scope: pause.Scope{Kind: pause.ScopeProvider, Provider: "files"}}},
			{Pause: &scenario.Pause{Scope: pause.Scope{Kind: pause.ScopeAction, Provider: "files", Action: "prompt"}}},
			{Unpause: &scenario.Unpause{Step: 5}},
			{Call: &scenario.Call{
				Tool:   "read_file",
				Args:   []byte(`{}`),
				Answer: scenario.Answer{Kind: scenario.AnswerBlocked, Codes: []string{"RULE_DENY", "NO_MATCHING_RULE"}},
				Decided: scenario.Decided{
					Verdict:     controlv1.Verdict_VERDICT_DENY,
					Codes:       []string{"RULE_DENY"},
					Obligations: []scenario.Obligation{},
				},
				Trail: scenario.Trail{Request: scenario.NewRequest, Kinds: []controlv1.EventKind{
					controlv1.EventKind_EVENT_KIND_ACTION_PROPOSED, controlv1.EventKind_EVENT_KIND_POLICY_DECIDED,
					controlv1.EventKind_EVENT_KIND_ACTION_BLOCKED,
				}},
			}},
		},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("Read =\n%s\nwant\n%s", dump(got), dump(want))
	}
}

// TestArgumentsKeptAsWritten: a number no double holds and one no float
// holds reach the plane as the author wrote them, as does the spacing.
func TestArgumentsKeptAsWritten(t *testing.T) {
	s := accept(t, doc(call(`{ "n" : 12345678901234567890,"f":1e400 , "neg": -0.0e-0 }`, resultAnswer, noDecision, newTrail)))
	got, err := s.Steps[0].Call.Arguments(nil)
	want := `{ "n" : 12345678901234567890,"f":1e400 , "neg": -0.0e-0 }`
	if err != nil || string(got) != want {
		t.Errorf("Arguments = %q, %v; want %q", got, err, want)
	}
	if string(s.Steps[0].Call.Args) != want {
		t.Errorf("Args = %q, want %q", s.Steps[0].Call.Args, want)
	}
}

// TestRunDefaultsToFresh: a scenario that says nothing starts on a fresh run.
func TestRunDefaultsToFresh(t *testing.T) {
	if s := accept(t, doc(plain())); s.Run != scenario.RunFresh {
		t.Errorf("Run = %q, want fresh", s.Run)
	}
	if s := accept(t, top(`"about":"a","plane":{"mode":"ENFORCE","bundle":{"id":"demo"}},"run":"fresh","steps":[`+plain()+`]`)); s.Run != scenario.RunFresh {
		t.Errorf("Run = %q, want fresh", s.Run)
	}
}

// TestEveryModeNameIsAccepted: the mode names are the gateway's, all six.
func TestEveryModeNameIsAccepted(t *testing.T) {
	for _, mode := range []string{"OBSERVE", "SHADOW", "WARN", "APPROVE", "ENFORCE", "LOCKDOWN"} {
		raw := strings.Replace(doc(plain()), `"ENFORCE"`, `"`+mode+`"`, 1)
		if s := accept(t, raw); s.Plane.Mode != mode {
			t.Errorf("Mode = %q, want %q", s.Plane.Mode, mode)
		}
	}
}

// TestReadFile reads a scenario by its path and takes its id from the file
// name; a directory and a missing file are errors, never an empty scenario.
func TestReadFile(t *testing.T) {
	dir := t.TempDir()
	good := filepath.Join(dir, "allowed-read.json")
	if err := os.WriteFile(good, []byte(doc(plain())), 0o600); err != nil {
		t.Fatal(err)
	}
	s, err := scenario.ReadFile(good)
	if err != nil || s.ID != "allowed-read.json" {
		t.Fatalf("ReadFile = %+v, %v", s, err)
	}
	bad := filepath.Join(dir, "Allowed.json")
	if err := os.WriteFile(bad, []byte(doc(plain())), 0o600); err != nil {
		t.Fatal(err)
	}
	if s, err := scenario.ReadFile(bad); err == nil {
		t.Errorf("ReadFile accepted a file named Allowed.json: %+v", s)
	}
	sub := filepath.Join(dir, "sub.json")
	if err := os.Mkdir(sub, 0o700); err != nil {
		t.Fatal(err)
	}
	for _, p := range []string{sub, filepath.Join(dir, "none.json")} {
		if s, err := scenario.ReadFile(p); err == nil {
			t.Errorf("ReadFile(%s) = %+v, want an error", p, s)
		}
	}
}

// TestReadFileStopsAtTheBound: a file past MaxBytes is refused as too large
// without being read whole.
func TestReadFileStopsAtTheBound(t *testing.T) {
	p := filepath.Join(t.TempDir(), "big.json")
	raw := doc(plain())
	if err := os.WriteFile(p, []byte(raw+strings.Repeat(" ", 1<<20-len(raw)+1)), 0o600); err != nil {
		t.Fatal(err)
	}
	_, err := scenario.ReadFile(p)
	if !isClass(err, scenario.ErrTooLarge) {
		t.Errorf("ReadFile = %v, want ErrTooLarge", err)
	}
}
