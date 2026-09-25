package scenario_test

import (
	"encoding/base64"
	"strings"
	"testing"

	"github.com/guardana/control/internal/policykey"
	"github.com/guardana/control/internal/scenario"
)

// keyBody is the start of the one body line of every key file keygen
// writes, which is key text even without a PEM marker around it.
var keyBody = base64.StdEncoding.EncodeToString([]byte(policykey.PKCS8Prefix))[:len(policykey.PKCS8Prefix)*8/6]

// The PEM markers, each built from two parts so that no literal in this file
// reads as a key block to a secret scanner.
var beginMarker, endMarker = "-----BEGIN " + "PRIVATE KEY-----", "-----END " + "PRIVATE KEY-----"

// TestKeyTextInAnOperatorValueIsRefused: every value a step puts on the
// approver's binary's command line refuses key text at load, naming the
// member and never quoting the value, so nothing runs and no process table
// shows it.
func TestKeyTextInAnOperatorValueIsRefused(t *testing.T) {
	ap := func(body string) string { return doc(pending(), `{"approve":`+body+`}`) }
	rj := func(body string) string { return doc(pending(), `{"reject":`+body+`}`) }
	ps := func(body string) string { return doc(plain(), `{"pause":`+body+`}`) }
	for _, c := range []refusalCase{
		{"approver key body", ap(`{"step":0,"approver":"` + keyBody + `"}`), scenario.ErrValue, "steps[1].approve.approver"},
		{"approve reason marker", ap(`{"step":0,"approver":"alice","reason":"` + beginMarker + `"}`), scenario.ErrValue, "steps[1].approve.reason"},
		{"reject reason closing marker", rj(`{"step":0,"approver":"alice","reason":"` + endMarker + `"}`), scenario.ErrValue, "steps[1].reject.reason"},
		{"reject reason key body", rj(`{"step":0,"approver":"alice","reason":"see ` + keyBody + `"}`), scenario.ErrValue, "steps[1].reject.reason"},
		{"pause reason", ps(`{"global":true,"reason":"` + beginMarker + `"}`), scenario.ErrValue, "steps[1].pause.reason"},
		{"pause provider", ps(`{"provider":"` + keyBody + `"}`), scenario.ErrValue, "steps[1].pause.provider"},
		{"pause action", ps(`{"provider":"orders","action":"-----BEGIN"}`), scenario.ErrValue, "steps[1].pause.action"},
		{"pause name", ps(`{"provider":"orders","action":"tool","name":"` + keyBody + `"}`), scenario.ErrValue, "steps[1].pause.name"},
		{"a pause before any call", `{"kind":"agent-scenario/v1alpha1","about":"k","plane":{"mode":"APPROVE","bundle":{"id":"b"}},
"steps":[
 {"pause":{"global":true,"reason":"` + beginMarker + `"}},
 {"call":{"tool":"update_order","args":{"id":"x"},"answer":{"kind":"pending","codes":["APPROVAL_PENDING"]},
   "decided":{"verdict":"ALLOW","codes":["RULE_ALLOW"],"obligations":[]},
   "trail":{"request":"new","kinds":["ACTION_PROPOSED"]}}},
 {"approve":{"step":1,"approver":"alice","reason":"` + beginMarker + `"}}]}`, scenario.ErrValue, "steps[0].pause.reason"},
	} {
		t.Run(c.name, func(t *testing.T) {
			r := refused(t, c.raw, c.err, c.path)
			if text := r.Error(); strings.Contains(text, "BEGIN") || strings.Contains(text, "END") || strings.Contains(text, keyBody[:8]) {
				t.Errorf("the refusal quotes the key text: %s", text)
			}
		})
	}
	// Short of a marker or of the whole body start, a value is not key text.
	short := keyBody[:len(keyBody)-1]
	s := accept(t, doc(pending(), `{"approve":{"step":0,"approver":"`+short+`","reason":"-----BEGI and ----- BEGIN"}}`,
		`{"pause":{"provider":"orders","action":"tool","name":"`+short+`","reason":"-----"}}`))
	if got := s.Steps[1].Approve.Reason; got != "-----BEGI and ----- BEGIN" {
		t.Errorf("the reason read as %q", got)
	}
}
