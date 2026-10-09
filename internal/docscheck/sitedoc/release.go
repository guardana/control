package sitedoc

import (
	"bytes"
	"fmt"
	"html"
	"regexp"
	"strings"

	"github.com/guardana/control/internal/brand"
	"github.com/guardana/control/internal/docscheck/docsconfig"
)

// Changelog is the file that must date the release the header names.
const Changelog = "CHANGELOG.md"

const (
	releaseOpen  = "<!-- release -->"
	releaseClose = "<!-- /release -->"
)

var (
	version        = regexp.MustCompile(`^` + docsconfig.VersionPattern + `$`)
	releaseHeading = regexp.MustCompile(`^## \[` + docsconfig.VersionPattern + `\] - [0-9]{4}-[0-9]{2}-[0-9]{2}$`)
)

// Published checks that released, the version docs/docs.json names as
// published, heads exactly one section of the changelog, read as
// `## [<version>] - <YYYY-MM-DD>` exactly. Whether a newer section is dated
// above it does not matter: a section is dated before its tag exists.
func Published(changelog []byte, released string) error {
	if !version.MatchString(released) {
		return fmt.Errorf("%w: the released version %q is not a version", ErrSlot, released)
	}
	prefix := "## [" + released + "]"
	found := 0
	for i, line := range strings.Split(string(changelog), "\n") {
		line = strings.TrimSuffix(line, "\r")
		if !strings.HasPrefix(line, prefix) {
			continue
		}
		if !releaseHeading.MatchString(line) {
			return fmt.Errorf("%w: %s line %d: %q is not `## [<version>] - <YYYY-MM-DD>`", ErrSlot, Changelog, i+1, line)
		}
		if found > 0 {
			return fmt.Errorf("%w: %s: %s heads two sections: lines %d and %d", ErrSlot, Changelog, released, found, i+1)
		}
		found = i + 1
	}
	if found == 0 {
		return fmt.Errorf("%w: %s holds no section for %s, the released version", ErrSlot, Changelog, released)
	}
	return nil
}

// Pill is the header's link to the released version's page, naming it.
func Pill(released string) string {
	return `<a class="vlink" href="https://` + brand.ModulePath + `/releases/tag/v` + html.EscapeString(released) +
		`"><span class="ver">v` + html.EscapeString(released) + `</span></a>`
}

// TagDocs is the documentation directory at the released version's tag. It
// names the directory rather than a page, since a page added after the tag
// has no copy there.
func TagDocs(released string) string {
	return "https://" + brand.ModulePath + "/tree/v" + released + "/docs"
}

// FillRelease returns the page with the body of its one release slot,
// `<!-- release -->…<!-- /release -->`, replaced by the pill for released.
func FillRelease(page []byte, released string) ([]byte, error) {
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
	out = append(out, Pill(released)...)
	return append(out, page[start+end:]...), nil
}
