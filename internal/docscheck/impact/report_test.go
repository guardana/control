package impact

import (
	"errors"
	"slices"
	"strings"
	"testing"
)

// A correct synthetic set: two pages, three surfaces, and a walk that holds a
// file under every glob. Each case below edits one thing and names what the
// report must then say.
func pages() []Page {
	return []Page{
		{Path: "docs/guides/run.md", Covers: []string{"cmd/**", "internal/gateway/adapter.go"}},
		{Path: "docs/reference/contract.md", Covers: []string{"pkg/**"}},
	}
}

var surfaces = []string{"cmd/**", "pkg/**", "adapters/**"}

// frozen holds one exact path and one directory, spelled with its slash.
var frozen = []string{"docs/reference/contract.md", "docs/adr/"}

var files = []string{
	"Makefile",
	"cmd/gw/main.go",
	"internal/gateway/adapter.go",
	"internal/gateway/mode.go",
	"pkg/contract/contract.go",
	"adapters/mcp/mcp.go",
}

func report(t *testing.T, ps []Page, changed ...string) Result {
	t.Helper()
	r, err := Report(ps, surfaces, frozen, files, changed)
	if err != nil {
		t.Fatalf("Report: %v", err)
	}
	return r
}

func TestReportCounts(t *testing.T) {
	r := report(t, pages(), "cmd/gw/main.go")
	if r.PagesExamined != 2 || r.SurfacesExamined != 3 || r.FrozenExamined != 2 || r.FilesExamined != 6 || r.ChangedExamined != 1 {
		t.Errorf("counts = %d pages, %d surfaces, %d frozen, %d files, %d changed; want 2, 3, 2, 6, 1",
			r.PagesExamined, r.SurfacesExamined, r.FrozenExamined, r.FilesExamined, r.ChangedExamined)
	}
}

// A changed path is under a frozen path when it is that exact path or lies
// in that directory; a sibling whose name begins the same way is not.
func TestAChangeUnderAFrozenPathIsAContractChange(t *testing.T) {
	r := report(t, pages(), "docs/reference/contract.md", "docs/adr/0001-first.md", "docs/adr/deep/0002-second.md",
		"docs/reference/contract.md.bak", "docs/adrs/x.md", "cmd/gw/main.go")
	want := []Frozen{
		{Path: "docs/reference/contract.md", Under: "docs/reference/contract.md"},
		{Path: "docs/adr/0001-first.md", Under: "docs/adr/"},
		{Path: "docs/adr/deep/0002-second.md", Under: "docs/adr/"},
	}
	if !slices.Equal(r.Frozen, want) {
		t.Errorf("Frozen = %+v, want %+v", r.Frozen, want)
	}
	if len(r.Review) != 1 || r.Review[0].Page != "docs/guides/run.md" {
		t.Errorf("Review = %+v, want the guide alone: a frozen path is still reviewed like any other", r.Review)
	}
	text := r.String()
	for _, want := range []string{
		"3 changed paths under a frozen path: a contract change, of 2 frozen paths examined\n",
		"  docs/adr/0001-first.md (docs/adr/)\n",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("String() lacks %q:\n%s", want, text)
		}
	}
	if r, err := Report(pages(), surfaces, nil, files, []string{"docs/adr/0001-first.md"}); err != nil || len(r.Frozen) != 0 || r.FrozenExamined != 0 {
		t.Errorf("with nothing frozen: Frozen = %+v, %v; want nothing and no error", r.Frozen, err)
	}
}

func TestChangeUnderOnePageNamesThatPageOnly(t *testing.T) {
	r := report(t, pages(), "cmd/gw/main.go")
	if len(r.Review) != 1 || r.Review[0].Page != "docs/guides/run.md" {
		t.Fatalf("Review = %+v, want the guide alone", r.Review)
	}
	if got := r.Review[0].Changed; len(got) != 1 || got[0] != "cmd/gw/main.go" {
		t.Errorf("Review[0].Changed = %q, want the one changed path", got)
	}
	if len(r.Uncovered) != 0 {
		t.Errorf("Uncovered = %+v, want nothing: a page covers the path", r.Uncovered)
	}
}

func TestChangeUnderTwoPagesNamesBoth(t *testing.T) {
	ps := pages()
	ps[1].Covers = append(ps[1].Covers, "cmd/**")
	r := report(t, ps, "cmd/gw/main.go")
	if len(r.Review) != 2 {
		t.Fatalf("Review = %+v, want both pages", r.Review)
	}
}

func TestChangeUnderNoPageAndNoSurfaceIsSilent(t *testing.T) {
	r := report(t, pages(), "Makefile", "internal/gateway/mode.go")
	if len(r.Review) != 0 || len(r.Uncovered) != 0 {
		t.Errorf("Review = %+v, Uncovered = %+v; want nothing: no page and no surface names these", r.Review, r.Uncovered)
	}
	if r.ChangedExamined != 2 {
		t.Errorf("ChangedExamined = %d, want 2", r.ChangedExamined)
	}
}

func TestChangeUnderASurfaceNoPageCoversIsReported(t *testing.T) {
	r := report(t, pages(), "adapters/mcp/mcp.go")
	if len(r.Review) != 0 {
		t.Errorf("Review = %+v, want nothing", r.Review)
	}
	if len(r.Uncovered) != 1 || r.Uncovered[0].Path != "adapters/mcp/mcp.go" || r.Uncovered[0].Surface != "adapters/**" {
		t.Fatalf("Uncovered = %+v, want the adapter file under adapters/**", r.Uncovered)
	}
}

func TestAPageCoveringTheSurfaceRemovesTheUncoveredRow(t *testing.T) {
	ps := pages()
	ps[0].Covers = append(ps[0].Covers, "adapters/mcp/**")
	r := report(t, ps, "adapters/mcp/mcp.go")
	if len(r.Uncovered) != 0 {
		t.Errorf("Uncovered = %+v, want nothing once a page covers the path", r.Uncovered)
	}
	if len(r.Review) != 1 {
		t.Errorf("Review = %+v, want the covering page", r.Review)
	}
}

func TestAGlobMatchingNoFileIsReported(t *testing.T) {
	ps := pages()
	ps[1].Covers = append(ps[1].Covers, "internal/nothing/**")
	r := report(t, ps)
	if len(r.Dead) != 1 || r.Dead[0].Page != "docs/reference/contract.md" || r.Dead[0].Glob != "internal/nothing/**" {
		t.Fatalf("Dead = %+v, want the one glob under no file", r.Dead)
	}
	if r := report(t, pages()); len(r.Dead) != 0 {
		t.Errorf("Dead = %+v on the correct set, want nothing", r.Dead)
	}
}

func TestAGlobMatchingOneFileIsAlive(t *testing.T) {
	ps := pages()
	ps[1].Covers = append(ps[1].Covers, "internal/gateway/mode.go")
	if r := report(t, ps); len(r.Dead) != 0 {
		t.Errorf("Dead = %+v, want nothing: the walk holds that file", r.Dead)
	}
}

func TestZeroChangedPathsReportsTheCountExamined(t *testing.T) {
	r := report(t, pages())
	if len(r.Review) != 0 || len(r.Uncovered) != 0 || r.ChangedExamined != 0 {
		t.Errorf("Result = %+v, want empty lists over zero changes", r)
	}
	text := r.String()
	if !strings.HasPrefix(text, "0 changed paths read\n") {
		t.Errorf("String() does not open with the count read:\n%s", text)
	}
	for _, want := range []string{
		"0 pages to review of 2 examined",
		"0 changed paths under a surface no page covers, of 3 surfaces examined",
		"0 changed paths under a frozen path: a contract change, of 2 frozen paths examined",
		"0 covers globs matching no file, of 2 pages and 6 files examined",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("String() lacks %q:\n%s", want, text)
		}
	}
}

func TestStringListsEveryRow(t *testing.T) {
	ps := pages()
	ps[1].Covers = append(ps[1].Covers, "internal/nothing/**")
	r := report(t, ps, "cmd/gw/main.go", "adapters/mcp/mcp.go")
	text := r.String()
	for _, want := range []string{
		"2 changed paths read\n1 pages to review of 2 examined",
		"  docs/guides/run.md <- cmd/gw/main.go",
		"1 changed paths under a surface no page covers, of 3 surfaces examined",
		"  adapters/mcp/mcp.go (adapters/**)",
		"1 covers globs matching no file, of 2 pages and 6 files examined",
		"  docs/reference/contract.md: internal/nothing/**",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("String() lacks %q:\n%s", want, text)
		}
	}
}

func TestCoveringNamesThePagesForOnePath(t *testing.T) {
	got, err := Covering(pages(), "internal/gateway/adapter.go")
	if err != nil {
		t.Fatalf("Covering: %v", err)
	}
	if len(got) != 1 || got[0] != "docs/guides/run.md" {
		t.Errorf("Covering = %q, want the guide", got)
	}
	got, err = Covering(pages(), "internal/gateway/mode.go")
	if err != nil || len(got) != 0 {
		t.Errorf("Covering = %q, %v; want nothing and no error", got, err)
	}
}

func TestReportRefusals(t *testing.T) {
	for name, tc := range map[string]struct {
		pages    []Page
		surfaces []string
		frozen   []string
		files    []string
		changed  []string
		reason   string
	}{
		"zero pages":                            {nil, surfaces, frozen, files, nil, "no page"},
		"zero surfaces":                         {pages(), nil, frozen, files, nil, "no surface"},
		"zero files":                            {pages(), surfaces, frozen, nil, nil, "no file"},
		"a page with no path":                   {[]Page{{Covers: []string{"cmd/**"}}}, surfaces, frozen, files, nil, "no path"},
		"a page listed twice":                   {append(pages(), pages()[0]), surfaces, frozen, files, nil, "twice"},
		"a bad covers glob":                     {[]Page{{Path: "docs/x.md", Covers: []string{"cmd/[a-"}}}, surfaces, frozen, files, nil, "docs/x.md"},
		"a bad surface":                         {pages(), []string{"cmd/**", ""}, frozen, files, nil, "surface"},
		"a frozen path empty":                   {pages(), surfaces, []string{""}, files, nil, "frozen"},
		"a frozen path not clean":               {pages(), surfaces, []string{"docs//adr/"}, files, nil, "frozen"},
		"a frozen path twice":                   {pages(), surfaces, []string{"docs/adr/", "docs/adr/"}, files, nil, "frozen: docs/adr/ is listed twice"},
		"a changed path not clean":              {pages(), surfaces, frozen, files, []string{"cmd/../x.go"}, "clean"},
		"a changed path absolute":               {pages(), surfaces, frozen, files, []string{"/cmd/x.go"}, "relative"},
		"a changed path twice":                  {pages(), surfaces, frozen, files, []string{"cmd/x.go", "cmd/x.go"}, "twice"},
		"a changed path empty":                  {pages(), surfaces, frozen, files, []string{""}, "empty"},
		"a changed path with a byte order mark": {pages(), surfaces, frozen, files, []string{"\ufeffcmd/x.go"}, "byte order mark"},
		"a changed path with a leading tab":     {pages(), surfaces, frozen, files, []string{"\tcmd/x.go"}, "white space"},
		"a changed path with a trailing space":  {pages(), surfaces, frozen, files, []string{"cmd/x.go "}, "white space"},
		"a changed path with a space inside":    {pages(), surfaces, frozen, files, []string{"cmd/x y.go"}, "white space"},
		"a changed path with a control byte":    {pages(), surfaces, frozen, files, []string{"cmd/x\x7f.go"}, "control"},
		"a changed path with a newline":         {pages(), surfaces, frozen, files, []string{"cmd/x\n.go"}, "white space"},
	} {
		t.Run(name, func(t *testing.T) {
			_, err := Report(tc.pages, tc.surfaces, tc.frozen, tc.files, tc.changed)
			if !errors.Is(err, ErrInvalid) {
				t.Fatalf("Report = %v, want ErrInvalid", err)
			}
			if !strings.Contains(err.Error(), tc.reason) {
				t.Errorf("error %q does not say %q", err, tc.reason)
			}
		})
	}
}

func TestCoveringRefusesZeroPagesAndABadPath(t *testing.T) {
	if _, err := Covering(nil, "cmd/x.go"); !errors.Is(err, ErrInvalid) {
		t.Errorf("Covering over no page = %v, want ErrInvalid", err)
	}
	if _, err := Covering(pages(), "../x.go"); !errors.Is(err, ErrInvalid) {
		t.Errorf("Covering of a parent path = %v, want ErrInvalid", err)
	}
}
