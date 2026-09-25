package console

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/guardana/control/internal/approvals"
)

// rewriteView replaces the resource id in the projection beside approvalID,
// as any writer of the directory can: the projection is not checked against
// the identifier rule when it is read back.
func rewriteView(t *testing.T, dir, approvalID, resourceID string) {
	t.Helper()
	path := filepath.Join(dir, approvalID+".view.json")
	raw, err := os.ReadFile(path) //nolint:gosec // G304: the test's own temporary directory
	if err != nil {
		t.Fatal(err)
	}
	var v approvals.View
	if err := json.Unmarshal(raw, &v); err != nil {
		t.Fatal(err)
	}
	v.ResourceID = resourceID
	out, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, append(out, '\n'), 0o600); err != nil {
		t.Fatal(err)
	}
}

// TestAgentTextComesBackAsDataOnly: a resource id holding markup or a
// bidirectional override reaches the page as a JSON string, with no angle
// bracket in the bytes, under a type a browser does not sniff, and decodes
// to what approvals list prints: the markup as it is, the override quoted.
func TestAgentTextComesBackAsDataOnly(t *testing.T) {
	dir, plane := newPlane(t)
	hold(t, plane, "MARKUP")
	hold(t, plane, "BIDI")
	markup := `<img src=x onerror=alert(1)>`
	bidi := "pay-\u202egpj.exe"
	rewriteView(t, dir, "MARKUP", markup)
	rewriteView(t, dir, "BIDI", bidi)
	s := serve(t, dir, "")
	a := s.do(t, s.readState())
	if a.status != http.StatusOK {
		t.Fatalf("answered %d %q", a.status, a.body)
	}
	if ct := a.header.Get("Content-Type"); ct != "application/json; charset=utf-8" {
		t.Errorf("Content-Type is %q", ct)
	}
	if strings.ContainsAny(a.body, "<>") {
		t.Errorf("the answer carries an angle bracket as a byte: %q", a.body)
	}
	var got stateReply
	decode(t, a, &got)
	resources := map[string]string{}
	for _, r := range got.Approvals.Records {
		if r.Readable == nil {
			t.Fatalf("%s: no readable fields: %s", r.ApprovalID, r.ReadableNote)
		}
		resources[r.ApprovalID] = r.Readable.Resource
	}
	for id, want := range map[string]string{"MARKUP": "payment " + markup, "BIDI": `"payment pay-\u202egpj.exe"`} {
		if resources[id] != want {
			t.Errorf("%s: resource %q, want %q", id, resources[id], want)
		}
	}
}

// markupSinks are the ways a script turns a string into markup or code. The
// page's script sets text only.
var markupSinks = regexp.MustCompile(`innerHTML|outerHTML|insertAdjacentHTML|document\.write|\beval\b|\bFunction\s*\(|createContextualFragment|DOMParser|setHTMLUnsafe|srcdoc|setTimeout\s*\(\s*["'\x60]|setInterval\s*\(\s*["'\x60]`)

// TestTheScriptSetsTextOnly: the embedded script names no markup sink, sets
// text through textContent, and quotes what the command line quotes. The
// shell holds no inline script, no handler attribute and no data.
func TestTheScriptSetsTextOnly(t *testing.T) {
	script, err := assets.ReadFile("assets/app.js")
	if err != nil {
		t.Fatal(err)
	}
	if m := markupSinks.FindString(string(script)); m != "" {
		t.Errorf("the script names %q", m)
	}
	if n := strings.Count(string(script), ".textContent = "); n < 3 {
		t.Errorf("the script sets textContent %d times; it renders nothing as text", n)
	}
	if !strings.Contains(string(script), `var QUOTED = /[\p{Cc}\p{Cf}\p{Zl}\p{Zp}\uFFFD]/u;`) {
		t.Error("the script does not quote control, format, line separator and replaced characters")
	}
	shell, err := assets.ReadFile("assets/index.html")
	if err != nil {
		t.Fatal(err)
	}
	scripts := regexp.MustCompile(`(?is)<script\b([^>]*)>(.*?)</script>`).FindAllStringSubmatch(string(shell), -1)
	if len(scripts) != 1 || !strings.Contains(scripts[0][1], ` src="/app.js"`) || scripts[0][2] != "" {
		t.Errorf("the shell's scripts are %q, want the one empty tag that loads /app.js", scripts)
	}
	for _, refused := range []*regexp.Regexp{
		regexp.MustCompile(`(?i)\son[a-z]+\s*=`),
		regexp.MustCompile(`(?i)\sstyle\s*=`),
		regexp.MustCompile(`\{\{|<%|\$\{`),
	} {
		if m := refused.FindString(string(shell)); m != "" {
			t.Errorf("the shell holds %q", m)
		}
	}
}

func TestTheSinkPatternsBite(t *testing.T) {
	for _, line := range []string{
		`node.innerHTML = text;`, `node.outerHTML = x`, `n.insertAdjacentHTML("beforeend", s)`,
		`document.write(s)`, `eval(s)`, `new Function("return 1")`, `setTimeout("go()", 1)`,
	} {
		if !markupSinks.MatchString(line) {
			t.Errorf("the sink pattern misses %q", line)
		}
	}
	for _, line := range []string{`node.textContent = text;`, `setInterval(load, REFRESH_MS);`, `var retrieval = 1;`} {
		if markupSinks.MatchString(line) {
			t.Errorf("the sink pattern refuses %q", line)
		}
	}
}
