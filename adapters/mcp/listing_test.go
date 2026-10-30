package mcp_test

import (
	"slices"
	"strings"
	"testing"

	"github.com/guardana/control/adapters/mcp"
	controlv1 "github.com/guardana/control/api/gen/go/guardana/control/v1"
	"github.com/guardana/control/internal/gateway"
)

// readOneFile allows a read of /allowed alone. A listing previews a tool with
// no arguments, so the rule's match is unknown there. denySlow denies the
// slow tool whatever its call carries.
const (
	readOneFile = `{"id":"read-one-file","effect":"ALLOW","when":{"action":{"effect":["READ"]},"resource":{"id":["/allowed"]}}}`
	denySlow    = `{"id":"deny-slow","effect":"DENY","when":{"action":{"name":["slow"]}}}`
)

// TestAToolOnlyItsCallCanDecideStaysListed: a read the policy allows for one
// resource id is listed and marked DECIDED_PER_CALL, not hidden as a DENY,
// while a tool a DENY rule matches beside the same unknown ALLOW is hidden.
// The call that names the allowed id runs, and one that names another is
// denied for no matching rule.
func TestAToolOnlyItsCallCanDecideStaysListed(t *testing.T) {
	rules := []string{readOneFile, denySlow, denyMail}
	hide := newRig(t, mcp.KindStatelessHTTP, rigOptions{shaping: mcp.ShapeHide, mode: modeEnforce, rules: rules})
	agent := hide.connect(t, "agent-a")
	names := toolNames(listTools(t, agent))
	if !slices.Contains(names, "read_file") || slices.Contains(names, "slow") || slices.Contains(names, "send_mail") {
		t.Errorf("hide: tools %s, want read_file and neither slow nor send_mail", strings.Join(names, ","))
	}

	expectMarks(t, newRig(t, mcp.KindStatelessHTTP, rigOptions{shaping: mcp.ShapeAnnotate, mode: modeEnforce, rules: rules}),
		map[string]string{"read_file": "DECIDED_PER_CALL", "slow": "VERDICT_DENY", "send_mail": "VERDICT_DENY"})

	if res, err := callTool(t, agent, "read_file", map[string]any{"path": "/allowed"}); err != nil || res.IsError {
		t.Fatalf("a read of /allowed: %v %+v", err, res)
	}
	res, err := callTool(t, agent, "read_file", map[string]any{"path": "/other"})
	if err != nil || !res.IsError || !slices.Equal(codesOf(t, res), []string{"NO_MATCHING_RULE"}) {
		t.Fatalf("a read of /other: %v %+v", err, res)
	}
	if n := hide.victim.count("read_file"); n != 1 {
		t.Errorf("the upstream ran read_file %d times, want once", n)
	}
}

// expectMarks lists the tools on r and holds each tool want names to its mark.
func expectMarks(t *testing.T, r *rig, want map[string]string) {
	t.Helper()
	for _, tool := range listTools(t, r.connect(t, "agent-a")).Tools {
		if w, ok := want[tool.Name]; ok && tool.Meta[metaVerdict] != w {
			t.Errorf("annotate: %s carries %v, want %s", tool.Name, tool.Meta[metaVerdict], w)
		}
	}
}

// TestTheListingIsThePipelinesWord: shaping reads the preview's listing, never
// its verdict. The zero listing shows every classified tool and marks it
// DECIDED_PER_CALL, even under a DENY; a denied listing hides the tool even
// under an ALLOW.
func TestTheListingIsThePipelinesWord(t *testing.T) {
	classified := "delete_file,read_file,send_mail,slow,transfer"
	for _, c := range []struct {
		name    string
		preview gateway.Previewed
		hidden  string
	}{
		{"the zero listing under a DENY", gateway.Previewed{Decision: decision(controlv1.Verdict_VERDICT_DENY, "NO_MATCHING_RULE")}, classified},
		{"a denied listing under an ALLOW", gateway.Previewed{Decision: decision(controlv1.Verdict_VERDICT_ALLOW), Listing: gateway.ListingDenied}, ""},
	} {
		t.Run(c.name, func(t *testing.T) {
			r := newRig(t, mcp.KindStatelessHTTP, rigOptions{shaping: mcp.ShapeHide})
			r.pipe.preview = func(gateway.Admission) gateway.Previewed { return c.preview }
			if got := strings.Join(toolNames(listTools(t, r.connect(t, "agent-a"))), ","); got != c.hidden {
				t.Errorf("hide: tools %q, want %q", got, c.hidden)
			}
		})
	}
	r := newRig(t, mcp.KindStatelessHTTP, rigOptions{shaping: mcp.ShapeAnnotate})
	r.pipe.preview = func(gateway.Admission) gateway.Previewed {
		return gateway.Previewed{Decision: decision(controlv1.Verdict_VERDICT_DENY, "NO_MATCHING_RULE")}
	}
	for _, tool := range listTools(t, r.connect(t, "agent-a")).Tools {
		want := "DECIDED_PER_CALL"
		if tool.Name == "unlisted" {
			want = "ACTION_UNCLASSIFIED"
		}
		if tool.Meta[metaVerdict] != want {
			t.Errorf("annotate: %s carries %v, want %s", tool.Name, tool.Meta[metaVerdict], want)
		}
	}
}
