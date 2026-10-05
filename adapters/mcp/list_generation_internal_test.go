package mcp

import (
	"context"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	controlv1 "github.com/guardana/control/api/gen/go/guardana/control/v1"
)

// TestListCacheAnswersOnlyItsGeneration: a list kept from one generation of
// the manifest answers a caller that snapshotted that generation and no
// other, even before a reset has run.
func TestListCacheAnswersOnlyItsGeneration(t *testing.T) {
	now := time.Now()
	c := newListCache(time.Minute)
	c.put("alice", []*mcp.Tool{{Name: "t"}}, "b1", 1, now)
	if _, ok := c.get("alice", "b1", 1, now); !ok {
		t.Fatal("a list kept from generation 1 did not answer generation 1")
	}
	if _, ok := c.get("alice", "b1", 2, now); ok {
		t.Error("a list kept from generation 1 answered generation 2")
	}
}

// TestListToolsBetweenRefreshAndReset: a tools/list that snapshots the
// manifest after a refresh replaced it, and before the cache was reset, is
// answered from the new manifest and not from the list cached before.
func TestListToolsBetweenRefreshAndReset(t *testing.T) {
	a := &Adapter{
		cfg: Config{
			Shaping: ShapeNone,
			ListTTL: time.Minute,
			Clock:   time.Now,
			Listener: Listener{Identity: Identity{
				Principal: &controlv1.Principal{Id: "svc-agent", Type: "service", TenantId: "t1"},
			}},
		},
		manifest:  newManifest(nil, emptySecrets(t)),
		names:     []string{"up"},
		upstreams: map[string]*mcp.ClientSession{},
		lists:     newListCache(time.Minute),
	}
	a.manifest.replace("up", []*Entry{{Upstream: "up", Tool: &mcp.Tool{Name: "old"}}})
	if got := listedNames(t, a); len(got) != 1 || got[0] != "old" {
		t.Fatalf("the first list is %v, want [old]", got)
	}
	a.manifest.replace("up", []*Entry{{Upstream: "up", Tool: &mcp.Tool{Name: "new"}}})
	if got := listedNames(t, a); len(got) != 1 || got[0] != "new" {
		t.Errorf("a list after the refresh is %v, want [new]", got)
	}
}

func listedNames(t *testing.T, a *Adapter) []string {
	t.Helper()
	res, err := a.listTools(context.Background(), &mcp.ListToolsRequest{})
	if err != nil {
		t.Fatalf("listTools: %v", err)
	}
	var names []string
	for _, tool := range res.(*mcp.ListToolsResult).Tools {
		names = append(names, tool.Name)
	}
	return names
}
