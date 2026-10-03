package sitedoc

import (
	"errors"
	"strings"
	"testing"

	"github.com/guardana/control/internal/brand"
)

const changelogHead = "# Changelog\n\nNotes.\n\n## [Unreleased]\n\n### Changed\n\n- Something.\n\n"

func TestReleaseIsTheNewestDatedSection(t *testing.T) {
	for name, c := range map[string]struct{ changelog, want string }{
		"after the unreleased section": {changelogHead + "## [0.6.0-alpha] - 2026-10-03\n\nText.\n\n## [0.5.0-alpha] - 2026-10-02\n", "0.6.0-alpha"},
		"with no unreleased section":   {"# Changelog\n\n## [1.2.3] - 2026-01-02\n\n## [1.2.2] - 2026-01-01\n", "1.2.3"},
		"a deeper heading is text":     {changelogHead + "### [9.9.9] - 2026-12-31\n\n## [0.1.0] - 2026-01-01\n", "0.1.0"},
		"a link definition is text":    {changelogHead + "## [0.2.0] - 2026-01-02\n\n[0.3.0]: https://example.invalid\n", "0.2.0"},
	} {
		got, err := Release([]byte(c.changelog))
		if err != nil || got != c.want {
			t.Errorf("%s: Release = %q, %v; want %q", name, got, err, c.want)
		}
	}
}

// Every heading that could be the newest release and is not one read
// exactly is refused, so a typo cannot hand the header an older version.
func TestReleaseRefusesWhatItCannotReadExactly(t *testing.T) {
	for name, c := range map[string]struct{ changelog, want string }{
		"no released section":        {changelogHead, "no dated release section"},
		"an empty file":              {"", "no dated release section"},
		"an undated release":         {changelogHead + "## [0.7.0]\n\n## [0.6.0] - 2026-10-03\n", `line 11: "## [0.7.0]"`},
		"a version with a v":         {changelogHead + "## [v0.7.0] - 2026-10-04\n", `"## [v0.7.0] - 2026-10-04"`},
		"a version with two parts":   {changelogHead + "## [0.7] - 2026-10-04\n", `"## [0.7] - 2026-10-04"`},
		"a leading zero":             {changelogHead + "## [0.07.0] - 2026-10-04\n", `"## [0.07.0] - 2026-10-04"`},
		"a short date":               {changelogHead + "## [0.7.0] - 2026-1-04\n", `"## [0.7.0] - 2026-1-04"`},
		"text after the date":        {changelogHead + "## [0.7.0] - 2026-10-04 (yanked)\n", `"## [0.7.0] - 2026-10-04 (yanked)"`},
		"an unreleased in lowercase": {"# Changelog\n\n## [unreleased]\n\n## [0.6.0] - 2026-10-03\n", `"## [unreleased]"`},
	} {
		got, err := Release([]byte(c.changelog))
		if !errors.Is(err, ErrSlot) || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s: Release = %q, %v; want an error containing %s", name, got, err, c.want)
		}
	}
}

func TestPillLinksTheReleasesPage(t *testing.T) {
	want := `<a class="vlink" href="https://` + brand.ModulePath + `/releases"><span class="ver">v0.6.0-alpha</span></a>`
	if got := Pill("0.6.0-alpha"); got != want {
		t.Errorf("Pill = %s, want %s", got, want)
	}
}

func TestFillReleaseWritesThePillIntoItsOneSlot(t *testing.T) {
	page := "<p>a</p><!-- release -->old<!-- /release --><p>b</p>"
	want := "<p>a</p><!-- release -->" + Pill("1.2.3") + "<!-- /release --><p>b</p>"
	got, err := FillRelease([]byte(page), "1.2.3")
	if err != nil || string(got) != want {
		t.Fatalf("FillRelease = %s, %v; want %s", got, err, want)
	}
	again, err := FillRelease(got, "1.2.3")
	if err != nil || string(again) != want {
		t.Errorf("a second fill changed the page: %v\n%s", err, again)
	}
	next, err := FillRelease(got, "1.2.4")
	if err != nil || !strings.Contains(string(next), ">v1.2.4<") || strings.Contains(string(next), "1.2.3") {
		t.Errorf("a new release left the old pill: %v\n%s", err, next)
	}
}

func TestFillReleaseRefusesABadSlot(t *testing.T) {
	for name, c := range map[string]struct{ page, want string }{
		"no slot":              {"<p>a</p>", "holds no release slot"},
		"two slots":            {"<!-- release -->a<!-- /release --><!-- release -->b<!-- /release -->", "holds 2 release slots"},
		"an unclosed slot":     {"<!-- release -->a", "is not closed"},
		"a close with no open": {"a<!-- /release -->", "holds no release slot"},
		"a close before open":  {"<!-- /release -->a<!-- release -->", "is not closed"},
		"two closes":           {"<!-- release -->a<!-- /release --><!-- /release -->", "closes 2 times"},
	} {
		out, err := FillRelease([]byte(c.page), "1.2.3")
		if !errors.Is(err, ErrSlot) || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s: FillRelease = %q, %v; want an error containing %q", name, out, err, c.want)
		}
	}
}
