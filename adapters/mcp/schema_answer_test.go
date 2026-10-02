package mcp_test

import (
	"slices"
	"testing"
	"time"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/guardana/control/adapters/mcp"
	"github.com/guardana/control/internal/brand"
	"github.com/guardana/control/internal/core"
	"github.com/guardana/control/internal/gateway"
)

// TestThePlanesAnswerFitsTheToolsOutputSchema: a client checks a result's
// structured content against the output schema the tool declared, and the
// plane's block and pending fields meet no tool's schema. For a tool that
// declares one, the plane's answer carries no structured content; for one
// that declares none it carries them there too. Either way the fields are in
// _meta under the plane's namespace.
func TestThePlanesAnswerFitsTheToolsOutputSchema(t *testing.T) {
	for name, declared := range map[string]bool{"declared": true, "none": false} {
		t.Run(name, func(t *testing.T) {
			v := newVictim()
			if declared {
				tool := *v.tools["transfer"]
				tool.OutputSchema = objectSchema(map[string]any{"receipt": map[string]any{"type": "string"}})
				v.tools["transfer"] = &tool
				v.server.RemoveTools("transfer")
				v.server.AddTool(&tool, v.handler("transfer"))
			}
			r := newRigOver(t, v, mcp.KindStatelessHTTP, rigOptions{})
			agent := r.connect(t, "agent-a")
			args := map[string]any{"amount": 5, "account": "acc-1"}

			r.pipe.decide = deny
			res, err := callTool(t, agent, "transfer", args)
			if err != nil || !res.IsError {
				t.Fatalf("block on the wire: %v %+v", err, res)
			}
			assertPlaneFields(t, res, declared, "reason_codes", []any{"RULE_DENY"})

			r.pipe.decide = func(gateway.Admission) gateway.Disposition {
				return gateway.Disposition{Action: core.AwaitApproval, Pending: &gateway.Pending{
					ApprovalID: "apr-1", ActionDigest: "sha256:0", ExpiresAt: time.Now(), RetryAfter: time.Second,
				}}
			}
			res, err = callTool(t, agent, "transfer", args)
			if err != nil || !res.IsError {
				t.Fatalf("pending on the wire: %v %+v", err, res)
			}
			assertPlaneFields(t, res, declared, "approval_id", "apr-1")
			assertPlaneFields(t, res, declared, "reason_code", "APPROVAL_PENDING")
			if n := v.count("transfer"); n != 0 {
				t.Fatalf("victim ran %d times", n)
			}
		})
	}
}

func assertPlaneFields(t *testing.T, res *sdk.CallToolResult, declared bool, key string, want any) {
	t.Helper()
	got := res.Meta[brand.OTelNamespace+"/"+key]
	if !same(got, want) {
		t.Errorf("_meta %s = %v, want %v", key, got, want)
	}
	switch {
	case declared && res.StructuredContent != nil:
		t.Errorf("a tool with an output schema got structured content %v", res.StructuredContent)
	case !declared:
		if sc, _ := res.StructuredContent.(map[string]any); !same(sc[key], want) {
			t.Errorf("structured content %s = %v, want %v", key, sc[key], want)
		}
	}
}

func same(got, want any) bool {
	if w, ok := want.([]any); ok {
		g, ok := got.([]any)
		return ok && slices.Equal(g, w)
	}
	return got == want
}
