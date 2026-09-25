package frontmatter_test

import (
	"errors"
	"slices"
	"strings"
	"testing"

	"github.com/guardana/control/internal/docscheck/frontmatter"
)

// good is a block every rule accepts, from which each case below makes one
// edit. The body after it is what Parse hands back.
const good = "---\n" +
	"title: MCP enforcement coverage\n" +
	"summary: Per revision, transport and method, what the gateway enforces today and what it does not.\n" +
	"type: reference\n" +
	"covers: [adapters/mcp/**, internal/gateway/**, cmd/gateway/**]\n" +
	"---\n"

const body = "\n# MCP enforcement coverage\n\nWhat this build does.\n"

func TestParseReadsABlockAndItsBody(t *testing.T) {
	m, got, err := frontmatter.Parse([]byte(good + body))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	want := frontmatter.Meta{
		Title:   "MCP enforcement coverage",
		Summary: "Per revision, transport and method, what the gateway enforces today and what it does not.",
		Type:    "reference",
		Covers:  []string{"adapters/mcp/**", "internal/gateway/**", "cmd/gateway/**"},
	}
	if !equal(m, want) {
		t.Errorf("Parse = %+v, want %+v", m, want)
	}
	if string(got) != body {
		t.Errorf("body = %q, want %q", got, body)
	}
}

func TestParseReadsEveryOptionalKey(t *testing.T) {
	page := "---\ntitle: Adapters\nsummary: The seam.\ntype: extending\ncovers: []\n" +
		"covers_reason: the seam is described, not implemented, here\nstability: beta\n" +
		"generated: scripts/gen-adapters.go\n---\n"
	m, _, err := frontmatter.Parse([]byte(page))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	want := frontmatter.Meta{Title: "Adapters", Summary: "The seam.", Type: "extending",
		CoversReason: "the seam is described, not implemented, here", Stability: "beta", Generated: "scripts/gen-adapters.go"}
	if !equal(m, want) {
		t.Errorf("Parse = %+v, want %+v", m, want)
	}
	if m.Covers != nil {
		t.Errorf("an empty list is nil, got %#v", m.Covers)
	}
}

// Each case is one edit to good; every one must be refused with ErrInvalid
// and hand back no Meta.
func TestParseRefusals(t *testing.T) {
	long := strings.Repeat("s", frontmatter.MaxSummary+1)
	many := make([]string, frontmatter.MaxCovers+1)
	for i := range many {
		many[i] = "pkg/x" + strings.Repeat("y", i) + "/**"
	}
	cases := map[string]struct{ page, want string }{
		"empty page":                           {"", "line 1 is not"},
		"no opening fence":                     {strings.TrimPrefix(good, "---\n"), "line 1 is not"},
		"a byte order mark":                    {"\ufeff" + good, "line 1 is not"},
		"a carriage return":                    {strings.ReplaceAll(good, "\n", "\r\n"), "carriage return"},
		"no closing fence":                     {strings.TrimSuffix(good, "\n---\n"), "no closing"},
		"no newline after the closing fence":   {strings.TrimSuffix(good, "\n"), "no newline after it"},
		"an unknown key":                       {strings.Replace(good, "type: reference\n", "type: reference\nauthor: me\n", 1), "unknown key"},
		"a key twice":                          {strings.Replace(good, "type: reference\n", "type: reference\ntype: reference\n", 1), "given twice"},
		"keys out of order":                    {"---\nsummary: The seam.\ntitle: Adapters\ntype: reference\ncovers: [pkg/**]\n---\n", "comes before"},
		"no colon":                             {strings.Replace(good, "type: reference", "type reference", 1), "not a key, a colon"},
		"no space after the colon":             {strings.Replace(good, "type: reference", "type:reference", 1), "not a key, a colon"},
		"an empty value":                       {strings.Replace(good, "type: reference", "type: ", 1), "a value is empty"},
		"a tab in a value":                     {strings.Replace(good, "MCP enforcement", "MCP\tenforcement", 1), "control character"},
		"trailing white space":                 {strings.Replace(good, "type: reference\n", "type: reference \n", 1), "white space"},
		"a quoted value":                       {strings.Replace(good, "title: MCP", "title: \"MCP", 1), "YAML reads as syntax"},
		"a colon and space in a value":         {strings.Replace(good, "title: MCP enforcement", "title: MCP: enforcement", 1), "colon followed by a space"},
		"a comment in a value":                 {strings.Replace(good, "type: reference", "type: reference #x", 1), "comment indicator"},
		"a boolean title":                      {strings.Replace(good, "title: MCP enforcement coverage", "title: yes", 1), "boolean or null"},
		"a numeric title":                      {strings.Replace(good, "title: MCP enforcement coverage", "title: 2026", 1), "a number"},
		"no title":                             {strings.Replace(good, "title: MCP enforcement coverage\n", "", 1), "title is required"},
		"no summary":                           {strings.Replace(good, "summary: Per revision, transport and method, what the gateway enforces today and what it does not.\n", "", 1), "summary is required"},
		"a summary one over the bound":         {strings.Replace(good, "summary: Per revision, transport and method, what the gateway enforces today and what it does not.", "summary: "+long, 1), "characters, the most is"},
		"no type":                              {strings.Replace(good, "type: reference\n", "", 1), "type is required"},
		"an unknown type":                      {strings.Replace(good, "type: reference", "type: guide", 1), "not one of"},
		"no covers":                            {strings.Replace(good, "covers: [adapters/mcp/**, internal/gateway/**, cmd/gateway/**]\n", "", 1), "covers is empty"},
		"covers not a list":                    {strings.Replace(good, "covers: [adapters/mcp/**, internal/gateway/**, cmd/gateway/**]", "covers: adapters/mcp/**", 1), "a list starts with ["},
		"a list without the space":             {strings.Replace(good, "adapters/mcp/**, internal", "adapters/mcp/**,internal", 1), "list punctuation"},
		"a list with a stray space":            {strings.Replace(good, "[adapters", "[ adapters", 1), "white space"},
		"a list item with a space":             {strings.Replace(good, "adapters/mcp/**", "adapters/mcp /**", 1), "holds a space"},
		"a list item twice":                    {strings.Replace(good, "internal/gateway/**", "adapters/mcp/**", 1), "twice"},
		"a list item starting with *":          {strings.Replace(good, "adapters/mcp/**", "**/README.md", 1), "YAML reads as syntax"},
		"one glob over the bound":              {strings.Replace(good, "[adapters/mcp/**, internal/gateway/**, cmd/gateway/**]", "["+strings.Join(many, ", ")+"]", 1), "the most is"},
		"empty covers without a reason":        {strings.Replace(good, "[adapters/mcp/**, internal/gateway/**, cmd/gateway/**]", "[]", 1), "covers is empty"},
		"a reason beside a list":               {strings.Replace(good, "cmd/gateway/**]\n", "cmd/gateway/**]\ncovers_reason: none\n", 1), "for an empty covers only"},
		"stability on a reference page":        {strings.Replace(good, "cmd/gateway/**]\n", "cmd/gateway/**]\nstability: development\n", 1), "stability is declared by spec"},
		"an unknown stability":                 {strings.Replace(strings.Replace(good, "type: reference", "type: spec", 1), "cmd/gateway/**]\n", "cmd/gateway/**]\nstability: solid\n", 1), "not one of"},
		"a generated value of no shape":        {strings.Replace(good, "cmd/gateway/**]\n", "cmd/gateway/**]\ngenerated: render.py\n", 1), "names neither"},
		"a generated script in a subdirectory": {strings.Replace(good, "cmd/gateway/**]\n", "cmd/gateway/**]\ngenerated: scripts/gen-x/main.go\n", 1), "names neither"},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			m, body, err := frontmatter.Parse([]byte(c.page))
			if err == nil {
				t.Fatalf("Parse accepted the page: %+v", m)
			}
			if !errors.Is(err, frontmatter.ErrInvalid) {
				t.Errorf("error %v does not wrap ErrInvalid", err)
			}
			if !strings.Contains(err.Error(), c.want) {
				t.Errorf("refused for %q, want the reason %q", err, c.want)
			}
			if body != nil {
				t.Errorf("a refused page handed back a body: %q", body)
			}
		})
	}
}

// The bounds are held at their edges, through Parse.
func TestBoundsBiteAtTheEdge(t *testing.T) {
	summary := strings.Repeat("s", frontmatter.MaxSummary)
	page := strings.Replace(good, "summary: Per revision, transport and method, what the gateway enforces today and what it does not.", "summary: "+summary, 1)
	if _, _, err := frontmatter.Parse([]byte(page)); err != nil {
		t.Errorf("a summary of exactly MaxSummary characters was refused: %v", err)
	}
	globs := make([]string, frontmatter.MaxCovers)
	for i := range globs {
		globs[i] = "pkg/x" + strings.Repeat("y", i) + "/**"
	}
	page = strings.Replace(good, "[adapters/mcp/**, internal/gateway/**, cmd/gateway/**]", "["+strings.Join(globs, ", ")+"]", 1)
	if _, _, err := frontmatter.Parse([]byte(page)); err != nil {
		t.Errorf("exactly MaxCovers globs were refused: %v", err)
	}
}

func TestRenderIsTheInverseOfParse(t *testing.T) {
	for name, page := range map[string]string{
		"reference": good,
		"extending with every key": "---\ntitle: Adapters\nsummary: The seam.\ntype: extending\ncovers: []\n" +
			"covers_reason: the seam is described here\nstability: beta\ngenerated: go test ./cmd/gateway -run TestCLIPage\n---\n",
	} {
		m, _, err := frontmatter.Parse([]byte(page + body))
		if err != nil {
			t.Fatalf("%s: Parse: %v", name, err)
		}
		got, err := frontmatter.Render(m)
		if err != nil {
			t.Fatalf("%s: Render: %v", name, err)
		}
		if string(got) != page {
			t.Errorf("%s: Render(Parse(page)) =\n%s\nwant\n%s", name, got, page)
		}
	}
}

func TestRenderRefusesWhatValidateRefuses(t *testing.T) {
	m := frontmatter.Meta{Title: "x", Summary: "y", Type: "reference", Covers: []string{"pkg/**"}, Stability: "beta"}
	if out, err := frontmatter.Render(m); !errors.Is(err, frontmatter.ErrInvalid) {
		t.Errorf("Render accepted stability on a reference page: %v, %q", err, out)
	}
	if out, err := frontmatter.Render(frontmatter.Meta{}); !errors.Is(err, frontmatter.ErrInvalid) {
		t.Errorf("Render accepted the zero Meta: %v, %q", err, out)
	}
}

func TestWords(t *testing.T) {
	text := "# Title\n\n| a | b |\n| --- | --- |\n| one | two |\n\n```go\nfunc x() {}\n```\n\n" +
		frontmatter.GeneratedOpen + "scripts/gen-x.go -->\nnot counted here\n" + frontmatter.GeneratedClose + "\n\n" +
		"Three words, 2 numbers -- and punctuation.\n"
	// Title, a, b, one, two, Three, words, 2, numbers, and, punctuation.
	if got := frontmatter.Words([]byte(text)); got != 11 {
		t.Errorf("Words = %d, want 11", got)
	}
	if got := frontmatter.Words(nil); got != 0 {
		t.Errorf("Words(nil) = %d", got)
	}
}

func equal(a, b frontmatter.Meta) bool {
	return a.Title == b.Title && a.Summary == b.Summary && a.Type == b.Type && slices.Equal(a.Covers, b.Covers) &&
		a.CoversReason == b.CoversReason && a.Stability == b.Stability && a.Generated == b.Generated
}
