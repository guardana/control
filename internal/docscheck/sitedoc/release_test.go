package sitedoc

import (
	"errors"
	"strings"
	"testing"

	"github.com/guardana/control/internal/brand"
)

const changelogHead = "# Changelog\n\nNotes.\n\n## [Unreleased]\n\n### Changed\n\n- Something.\n\n"

// The released version need not be the newest dated section: a section is
// dated before its tag is pushed, and the site keeps naming the release that
// is out.
func TestPublishedAcceptsADatedSection(t *testing.T) {
	for name, c := range map[string]struct{ changelog, version string }{
		"the newest section":           {changelogHead + "## [0.6.0-alpha] - 2026-10-03\n\nText.\n\n## [0.5.0-alpha] - 2026-10-02\n", "0.6.0-alpha"},
		"below a section in the works": {changelogHead + "## [0.7.0-alpha] - 2026-10-04\n\n## [0.6.0-alpha] - 2026-10-03\n", "0.6.0-alpha"},
		"with no unreleased section":   {"# Changelog\n\n## [1.2.3] - 2026-01-02\n\n## [1.2.2] - 2026-01-01\n", "1.2.2"},
		"with CRLF line ends":          {"# Changelog\r\n\r\n## [1.2.3] - 2026-01-02\r\n", "1.2.3"},
		"with build metadata":          {changelogHead + "## [1.2.3+b.7] - 2026-01-02\n", "1.2.3+b.7"},
	} {
		if err := Published([]byte(c.changelog), c.version); err != nil {
			t.Errorf("%s: Published(%s) = %v, want nil", name, c.version, err)
		}
	}
}

// A released version the changelog does not date exactly is refused, so the
// header never names a release that has no notes.
func TestPublishedRefusesAVersionWithNoDatedSection(t *testing.T) {
	for name, c := range map[string]struct{ changelog, version, want string }{
		"no section for it":           {changelogHead + "## [0.6.0] - 2026-10-03\n", "0.7.0", "CHANGELOG.md holds no section for 0.7.0"},
		"only the unreleased section": {changelogHead, "0.7.0", "CHANGELOG.md holds no section for 0.7.0"},
		"an empty file":               {"", "0.7.0", "CHANGELOG.md holds no section for 0.7.0"},
		"an undated section":          {changelogHead + "## [0.7.0]\n\n## [0.6.0] - 2026-10-03\n", "0.7.0", `line 11: "## [0.7.0]" is not`},
		"a short date":                {changelogHead + "## [0.7.0] - 2026-1-04\n", "0.7.0", `"## [0.7.0] - 2026-1-04" is not`},
		"text after the date":         {changelogHead + "## [0.7.0] - 2026-10-04 (yanked)\n", "0.7.0", `"## [0.7.0] - 2026-10-04 (yanked)" is not`},
		"the section twice":           {changelogHead + "## [0.7.0] - 2026-10-04\n\n## [0.7.0] - 2026-10-03\n", "0.7.0", "heads two sections: lines 11 and 13"},
		"a deeper heading only":       {changelogHead + "### [0.7.0] - 2026-10-04\n", "0.7.0", "holds no section for 0.7.0"},
		"a link definition only":      {changelogHead + "[0.7.0]: https://example.invalid\n", "0.7.0", "holds no section for 0.7.0"},
		"a heading with a v":          {changelogHead + "## [v0.7.0] - 2026-10-04\n", "0.7.0", "holds no section for 0.7.0"},
		"a longer version":            {changelogHead + "## [0.7.0-alpha] - 2026-10-04\n", "0.7.0", "holds no section for 0.7.0"},
		"a version with a v":          {changelogHead + "## [v0.7.0] - 2026-10-04\n", "v0.7.0", `"v0.7.0" is not a version`},
		"an empty version":            {changelogHead + "## [0.7.0] - 2026-10-04\n", "", `"" is not a version`},
	} {
		err := Published([]byte(c.changelog), c.version)
		if !errors.Is(err, ErrSlot) || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s: Published(%q) = %v; want an error containing %s", name, c.version, err, c.want)
		}
	}
}

func TestPillLinksTheReleasesTag(t *testing.T) {
	want := `<a class="vlink" href="https://` + brand.ModulePath + `/releases/tag/v0.6.0-alpha"><span class="ver">v0.6.0-alpha</span></a>`
	if got := Pill("0.6.0-alpha"); got != want {
		t.Errorf("Pill = %s, want %s", got, want)
	}
}

func TestTagDocsIsTheDocsTreeAtTheTag(t *testing.T) {
	want := "https://" + brand.ModulePath + "/tree/v0.6.0-alpha/docs"
	if got := TagDocs("0.6.0-alpha"); got != want {
		t.Errorf("TagDocs = %s, want %s", got, want)
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
