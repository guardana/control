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

// TestThePlanesAnswerCarriesNoStructuredContent: a client checks a result's
// structured content against the output schema the tool listed, perhaps in an
// earlier listing, and the plane's block and pending fields meet no tool's
// schema. Whatever the tool declares, the plane's answer carries its fields in
// _meta under the namespace and no structured content.
func TestThePlanesAnswerCarriesNoStructuredContent(t *testing.T) {
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
			assertPlaneFields(t, res, "reason_codes", []any{"RULE_DENY"})

			r.pipe.decide = func(gateway.Admission) gateway.Disposition {
				return gateway.Disposition{Action: core.AwaitApproval, Pending: &gateway.Pending{
					ApprovalID: "apr-1", ActionDigest: "sha256:0", ExpiresAt: time.Now(), RetryAfter: time.Second,
				}}
			}
			res, err = callTool(t, agent, "transfer", args)
			if err != nil || !res.IsError {
				t.Fatalf("pending on the wire: %v %+v", err, res)
			}
			assertPlaneFields(t, res, "approval_id", "apr-1")
			assertPlaneFields(t, res, "reason_code", "APPROVAL_PENDING")
			if n := v.count("transfer"); n != 0 {
				t.Fatalf("victim ran %d times", n)
			}
		})
	}
}

func assertPlaneFields(t *testing.T, res *sdk.CallToolResult, key string, want any) {
	t.Helper()
	if got := res.Meta[brand.OTelNamespace+"/"+key]; !same(got, want) {
		t.Errorf("_meta %s = %v, want %v", key, got, want)
	}
	if res.StructuredContent != nil {
		t.Errorf("the plane's answer carries structured content %v", res.StructuredContent)
	}
}

func same(got, want any) bool {
	if w, ok := want.([]any); ok {
		g, ok := got.([]any)
		return ok && slices.Equal(g, w)
	}
	return got == want
}
