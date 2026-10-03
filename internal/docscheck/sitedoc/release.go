package sitedoc

import (
	"bytes"
	"fmt"
	"html"
	"regexp"
	"strings"

	"github.com/guardana/control/internal/brand"
)

// Changelog is the file whose newest dated section names the release the
// header shows.
const Changelog = "CHANGELOG.md"

const (
	releaseOpen  = "<!-- release -->"
	releaseClose = "<!-- /release -->"
	unreleased   = "## [Unreleased]"
)

// releaseHeading is a dated section, its version semantic versioning 2.0.0.
var releaseHeading = regexp.MustCompile(`^## \[((?:0|[1-9][0-9]*)\.(?:0|[1-9][0-9]*)\.(?:0|[1-9][0-9]*)(?:-[0-9A-Za-z.-]+)?(?:\+[0-9A-Za-z.-]+)?)\] - [0-9]{4}-[0-9]{2}-[0-9]{2}$`)

// Release returns the version of the changelog's newest dated section: the
// first `## [` heading after any `## [Unreleased]`. That heading must read
// `## [<version>] - <YYYY-MM-DD>` exactly; one that does not is refused rather
// than passed over, so the header never falls back to an older release.
func Release(changelog []byte) (string, error) {
	for i, line := range strings.Split(string(changelog), "\n") {
		line = strings.TrimSuffix(line, "\r")
		if !strings.HasPrefix(line, "## [") || line == unreleased {
			continue
		}
		m := releaseHeading.FindStringSubmatch(line)
		if m == nil {
			return "", fmt.Errorf("%w: %s line %d: %q is the newest release heading and is not `## [<version>] - <YYYY-MM-DD>`", ErrSlot, Changelog, i+1, line)
		}
		return m[1], nil
	}
	return "", fmt.Errorf("%w: %s holds no dated release section", ErrSlot, Changelog)
}

// Pill is the header's link to the releases page, naming the version.
func Pill(version string) string {
	return `<a class="vlink" href="https://` + brand.ModulePath + `/releases"><span class="ver">v` +
		html.EscapeString(version) + `</span></a>`
}

// FillRelease returns the page with the body of its one release slot,
// `<!-- release -->…<!-- /release -->`, replaced by the pill for version.
func FillRelease(page []byte, version string) ([]byte, error) {
	opens, closes := bytes.Count(page, []byte(releaseOpen)), bytes.Count(page, []byte(releaseClose))
	switch {
	case opens == 0:
		return nil, fmt.Errorf("%w: the page holds no release slot", ErrSlot)
	case opens > 1:
		return nil, fmt.Errorf("%w: the page holds %d release slots, want one", ErrSlot, opens)
	case closes > 1:
		return nil, fmt.Errorf("%w: the release slot closes %d times", ErrSlot, closes)
	}
	start := bytes.Index(page, []byte(releaseOpen)) + len(releaseOpen)
	end := bytes.Index(page[start:], []byte(releaseClose))
	if closes == 0 || end < 0 {
		return nil, fmt.Errorf("%w: the release slot at byte %d is not closed", ErrSlot, start-len(releaseOpen))
	}
	out := make([]byte, 0, len(page)+64)
	out = append(out, page[:start]...)
	out = append(out, Pill(version)...)
	return append(out, page[start+end:]...), nil
}
