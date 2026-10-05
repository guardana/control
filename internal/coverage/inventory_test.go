package coverage_test

import (
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/guardana/control/internal/coverage"
)

func TestReadInventoryReadsEveryKind(t *testing.T) {
	inv, err := coverage.ReadInventory([]byte(`{"schema_version":"0.1","paths":[
		{"id":"gh-create","kind":"mcp_tool","upstream":"github","tool":"create_issue",
		 "sources":[{"source_id":"s1","name":"create_issue","server_address":"api.github.com"}]},
		{"id":"billing","kind":"http_api","name":"billing api"},
		{"id":"shell","kind":"process","name":"bash","sources":[]},
		{"id":"out.example","kind":"egress","host":"example.com"}]}`))
	if err != nil {
		t.Fatalf("ReadInventory: %v", err)
	}
	want := []coverage.Path{
		{ID: "gh-create", Kind: "mcp_tool", Upstream: "github", Tool: "create_issue",
			Sources: []coverage.PathSource{{SourceID: "s1", Name: "create_issue", ServerAddress: "api.github.com"}}},
		{ID: "billing", Kind: "http_api", Name: "billing api"},
		{ID: "shell", Kind: "process", Name: "bash"},
		{ID: "out.example", Kind: "egress", Host: "example.com"},
	}
	if len(inv.Paths) != len(want) {
		t.Fatalf("read %d paths, want %d: %+v", len(inv.Paths), len(want), inv.Paths)
	}
	for i := range want {
		got := inv.Paths[i]
		if len(got.Sources) == 0 {
			got.Sources = nil
		}
		if !reflect.DeepEqual(got, want[i]) {
			t.Errorf("path %d = %+v, want %+v", i, got, want[i])
		}
	}
}

func TestReadInventoryRefuses(t *testing.T) {
	const tool = `{"id":"a","kind":"mcp_tool","upstream":"u","tool":"t"}`
	doc := func(paths ...string) string {
		return `{"schema_version":"0.1","paths":[` + strings.Join(paths, ",") + `]}`
	}
	cases := map[string]string{
		"zero paths":              doc(),
		"no paths member":         `{"schema_version":"0.1"}`,
		"another schema_version":  `{"schema_version":"0.2","paths":[` + tool + `]}`,
		"a major":                 `{"schema_version":"1.0","paths":[` + tool + `]}`,
		"no schema_version":       `{"paths":[` + tool + `]}`,
		"a numeric version":       `{"schema_version":0.1,"paths":[` + tool + `]}`,
		"an unknown member":       `{"schema_version":"0.1","paths":[` + tool + `],"extra":1}`,
		"a member in other case":  `{"Schema_version":"0.1","paths":[` + tool + `]}`,
		"a duplicate member":      `{"schema_version":"0.1","schema_version":"0.1","paths":[` + tool + `]}`,
		"a duplicate path member": doc(`{"id":"a","id":"b","kind":"mcp_tool","upstream":"u","tool":"t"}`),
		"a duplicate id":          doc(tool, `{"id":"a","kind":"egress","host":"h"}`),
		"a duplicate tool":        doc(tool, `{"id":"b","kind":"mcp_tool","upstream":"u","tool":"t"}`),
		"an unknown path member":  doc(`{"id":"a","kind":"mcp_tool","upstream":"u","tool":"t","effect":"READ"}`),
		"another kind's member":   doc(`{"id":"a","kind":"mcp_tool","upstream":"u","tool":"t","host":"h"}`),
		"an unknown kind":         doc(`{"id":"a","kind":"socket","name":"n"}`),
		"no tool":                 doc(`{"id":"a","kind":"mcp_tool","upstream":"u"}`),
		"an empty upstream":       doc(`{"id":"a","kind":"mcp_tool","upstream":"","tool":"t"}`),
		"no host":                 doc(`{"id":"a","kind":"egress"}`),
		"no name":                 doc(`{"id":"a","kind":"process"}`),
		"no id":                   doc(`{"kind":"egress","host":"h"}`),
		"an upper-case id":        doc(`{"id":"A","kind":"egress","host":"h"}`),
		"a slash in an id":        doc(`{"id":"a/b","kind":"egress","host":"h"}`),
		"a null":                  doc(`{"id":"a","kind":"egress","host":null}`),
		"a number for a name":     doc(`{"id":"a","kind":"process","name":7}`),
		"a control character":     doc(`{"id":"a","kind":"process","name":"ba\u0007sh"}`),
		"a bidi override":         doc("{\"id\":\"a\",\"kind\":\"process\",\"name\":\"ba\\u202esh\"}"),
		"whitespace only":         doc(`{"id":"a","kind":"process","name":"  "}`),
		"invalid UTF-8 in a name": doc("{\"id\":\"a\",\"kind\":\"process\",\"name\":\"ba\xffsh\"}"),
		"U+FFFD in a name":        doc("{\"id\":\"a\",\"kind\":\"process\",\"name\":\"ba\\ufffdsh\"}"),
		"a line separator":        doc("{\"id\":\"a\",\"kind\":\"process\",\"name\":\"ba\\u2028sh\"}"),
		"a paragraph separator":   doc("{\"id\":\"a\",\"kind\":\"process\",\"name\":\"ba\\u2029sh\"}"),
		"a trailing value":        doc(tool) + `{}`,
		"not an object":           `["schema_version"]`,
		"paths not a list":        `{"schema_version":"0.1","paths":{}}`,
		"a source twice":          doc(`{"id":"a","kind":"process","name":"n","sources":[{"source_id":"s","name":"x"},{"source_id":"s","name":"y"}]}`),
		"a source member unknown": doc(`{"id":"a","kind":"process","name":"n","sources":[{"source_id":"s","name":"x","trust":"platform"}]}`),
		"a source without a name": doc(`{"id":"a","kind":"process","name":"n","sources":[{"source_id":"s"}]}`),
		"a source without its id": doc(`{"id":"a","kind":"process","name":"n","sources":[{"name":"x"}]}`),
		"not JSON":                `{"schema_version":"0.1",`,
		"empty":                   ``,
	}
	for name, d := range cases {
		t.Run(name, func(t *testing.T) {
			inv, err := coverage.ReadInventory([]byte(d))
			if !errors.Is(err, coverage.ErrInventory) || inv != nil {
				t.Fatalf("ReadInventory = %+v, %v; want ErrInventory", inv, err)
			}
		})
	}
}

func TestInventoryBounds(t *testing.T) {
	doc := func(id, name string) []byte {
		return []byte(`{"schema_version":"0.1","paths":[{"id":"` + id + `","kind":"process","name":"` + name + `"}]}`)
	}
	small := doc("a", "n")
	pad := func(n int) []byte { return append(append([]byte(nil), small...), strings.Repeat(" ", n)...) }
	cases := []struct {
		name string
		doc  []byte
		ok   bool
	}{
		{"id of 64 bytes", doc(strings.Repeat("a", 64), "n"), true},
		{"id of 65 bytes", doc(strings.Repeat("a", 65), "n"), false},
		{"name of 256 bytes", doc("a", strings.Repeat("n", 256)), true},
		{"name of 257 bytes", doc("a", strings.Repeat("n", 257)), false},
		{"document at the size bound", pad(1<<20 - len(small)), true},
		{"document past the size bound", pad(1<<20 - len(small) + 1), false},
	}
	for _, tc := range cases {
		_, err := coverage.ReadInventory(tc.doc)
		if (err == nil) != tc.ok {
			t.Errorf("%s: ReadInventory err = %v, want accepted %v", tc.name, err, tc.ok)
		}
	}
}
