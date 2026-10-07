package runview_test

import (
	"regexp"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/guardana/control/internal/runview"
	"github.com/guardana/control/internal/supervise"
)

const csp = `<meta http-equiv="Content-Security-Policy" content="default-src 'none'; style-src 'unsafe-inline'">`

// TestThePageCarriesItsPolicyAndNoScript: one policy tag that allows inline
// style alone, before anything else of the head; no script, no link, no
// source, no URL in a style and nothing that moves.
func TestThePageCarriesItsPolicyAndNoScript(t *testing.T) {
	for _, s := range goldens {
		page := draw(t, s)
		lower := strings.ToLower(page)
		if strings.Count(page, csp) != 1 || !strings.HasPrefix(page[strings.Index(page, "<head>")+len("<head>"):], "\n"+csp) {
			t.Errorf("%s: the policy tag is missing, repeated or not first in the head", s.name)
		}
		for _, banned := range []string{"<script", "javascript:", "href", "src=", "url(", "@import", "<link",
			"<iframe", "<object", "<embed", "<img", "<form", "animation", "transition", "@keyframes", "<animate", "<marquee"} {
			if strings.Contains(lower, banned) {
				t.Errorf("%s: the page holds %q", s.name, banned)
			}
		}
	}
}

var tag = regexp.MustCompile(`<[^>]*>`)

// tags is the markup of a page, every tag in order with its attributes.
func tags(page string) string { return strings.Join(tag.FindAllString(page, -1), "\n") }

// stand is a string the page draws as it draws s, uncut or cut alike, made
// of letters alone.
func stand(s string) string {
	switch n := utf8.RuneCountInString(s); {
	case n == 0:
		return ""
	case n > runview.MaxText:
		return strings.Repeat("a", runview.MaxText+1)
	}
	return "a"
}

// hostile puts s in every string of res an agent, a source or a plane can
// choose.
func hostile(res *supervise.Result, s string) {
	for i := range res.Instances {
		in := &res.Instances[i]
		in.Tool, in.Upstream, in.Request, in.Run, in.Step = s, s, s, s, s
		if in.Observation != "" {
			in.Observation, in.Source = s, s
		}
		in.Resource = supervise.Resource{Type: s, ID: s, Tenant: s, Environment: s}
		in.Reasons = []string{s, s}
	}
	r := res.Report
	r.TenantId, r.ProjectId, r.RunId = s, s, s
	for _, rule := range r.GetRules() {
		rule.Why = s
	}
	r.GetRead().EventsLeftOut = map[string]uint64{s: 1}
	for _, m := range r.GetRunTree() {
		m.RunId, m.ParentRunId = s, s
	}
	for _, f := range res.Findings {
		f.GetFinding().FindingId, f.GetFinding().RunId = s, s
	}
	res.NeverHeard, res.Silent, res.SourcesNotRead = []string{s}, []string{s}, []string{s}
}

// drawnWith is the page of the scenario with every chosen string s.
func drawnWith(t *testing.T, sc scenario, s string) string {
	t.Helper()
	p, res := sc.build(t)
	hostile(res, s)
	page, err := runview.Page(p, res, sc.opt)
	if err != nil {
		t.Fatalf("Page: %v", err)
	}
	return string(page)
}

// FuzzChosenStringsStayText: whatever a tool name, a resource id, a reason
// or any other chosen string holds, the page's markup is the markup it has
// with letters in its place, so no string adds a tag, an attribute, a link
// or a style, and none is placed in one.
func FuzzChosenStringsStayText(f *testing.F) {
	for _, s := range []string{
		`<script>alert(1)</script>`, `" onmouseover="x`, `' style='x`, `javascript:alert(1)`,
		`</style><svg onload=1>`, "\u202egnp.exe", `{{.}}`, `&lt;b&gt;`, "\xff\xfe", "a\nb\rc\td",
		`<!--`, `]]>`, `</td></tr></table><a href="http://x">`, strings.Repeat("<i>", 40), "url(x)",
		`<meta http-equiv="Content-Security-Policy" content="default-src *">`,
	} {
		f.Add(s)
	}
	sc := goldens[1]
	f.Fuzz(func(t *testing.T, s string) {
		got := drawnWith(t, sc, s)
		if lower := strings.ToLower(got); strings.Contains(lower, "<script") {
			t.Fatalf("a chosen string %q brought a script", s)
		}
		if strings.Count(tags(got), `http-equiv`) != 1 {
			t.Fatalf("a chosen string %q brought a second policy tag", s)
		}
		if a, b := tags(got), tags(drawnWith(t, sc, stand(s))); a != b {
			t.Fatalf("a chosen string %q changed the markup:\n%s\n---- with letters\n%s", s, a, b)
		}
	})
}

// TestAChosenStringIsCutAtTheBoundAndSaysSo: a string of MaxText characters
// is drawn whole; one more is cut to MaxText with the cut shown apart from
// the string; a character that does not print, and a backslash, is drawn
// escaped.
func TestAChosenStringIsCutAtTheBoundAndSaysSo(t *testing.T) {
	const cutMark = `</code><span class="cut">[cut]</span>`
	whole := strings.Repeat("x", runview.MaxText)
	if page := drawnWith(t, goldens[1], whole); !strings.Contains(page, "<code>"+whole+"</code>") || strings.Contains(page, cutMark) {
		t.Errorf("a string of %d characters is not drawn whole", runview.MaxText)
	}
	page := drawnWith(t, goldens[1], whole+"y")
	if !strings.Contains(page, "<code>"+whole+cutMark) || strings.Contains(page, whole+"y") {
		t.Errorf("a string of %d characters is not cut at %d with the cut shown", runview.MaxText+1, runview.MaxText)
	}
	page = drawnWith(t, goldens[1], "a\u202eb\x00c\xffd")
	if !strings.Contains(page, `<code>a\u202eb\x00c\xffd</code>`) {
		t.Errorf("characters that do not print are not drawn escaped")
	}
	if page := drawnWith(t, goldens[1], `a\u202e`); !strings.Contains(page, `<code>a\u005cu202e</code>`) {
		t.Errorf("a backslash is not drawn escaped, so a string can spell an escape")
	}
	if long := drawnWith(t, goldens[1], strings.Repeat("\u202e", runview.MaxText+1)); !strings.Contains(long,
		"<code>"+strings.Repeat(`\u202e`, runview.MaxText)+cutMark) {
		t.Errorf("an escaped character does not count as one")
	}
}
