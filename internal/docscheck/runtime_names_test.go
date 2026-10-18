package docscheck

import (
	"encoding/json"
	"fmt"
	"html"
	"io/fs"
	"path"
	"regexp"
	"strings"
	"testing"
)

// A runtime's name may appear in its profile's directory, on its page and the
// page's generated copy, and inside a link to that page: the target, the
// link's text and attributes, and the summary an index row carries beside
// it. The name a profile is checked for is its id up to the first hyphen,
// so this file spells none.
const profilesDir = "internal/hookbridge/profiles/"

var (
	mdLink    = regexp.MustCompile(`\[([^\]]*)\]\(([^)\s]+)\)`)
	mdIndex   = regexp.MustCompile(`^- \[[^\]]*\]\(([^)\s]+)\): (.*)$`)
	htmlLink  = regexp.MustCompile(`<a\s[^>]*?href="([^"]*)"[^>]*>.*?</a>`)
	htmlIndex = regexp.MustCompile(`^<li><a\s[^>]*?href="([^"]*)"[^>]*>.*?</a>: (.*)</li>$`)
	siteLoc   = regexp.MustCompile(`<loc>([^<]*)</loc>`)
)

// runtimeName is one built-in profile as the rule reads it.
type runtimeName struct {
	term, dir, page, site, url, summary string
}

// runtimeNames reads every built-in profile among files: the term is its id
// up to the first hyphen, the page is its claims.page.
func runtimeNames(fsys fs.FS, files []string) ([]runtimeName, error) {
	var out []runtimeName
	for _, rel := range files {
		dir, ok := strings.CutSuffix(rel, "/profile.json")
		if !ok || !strings.HasPrefix(dir, profilesDir) || strings.Contains(strings.TrimPrefix(dir, profilesDir), "/") {
			continue
		}
		raw, err := fs.ReadFile(fsys, rel)
		if err != nil {
			return nil, err
		}
		var p struct {
			ID     string `json:"id"`
			Claims struct {
				Page string `json:"page"`
			} `json:"claims"`
		}
		if err := json.Unmarshal(raw, &p); err != nil {
			return nil, fmt.Errorf("%s: %w", rel, err)
		}
		term, _, _ := strings.Cut(p.ID, "-")
		stem, isPage := strings.CutSuffix(strings.TrimPrefix(p.Claims.Page, "docs/"), ".md")
		if len(term) < 4 || !isPage || !strings.HasPrefix(p.Claims.Page, "docs/") {
			return nil, fmt.Errorf("%s: no name of four letters or more, or no page under docs/", rel)
		}
		summary, err := pageSummary(fsys, p.Claims.Page)
		if err != nil {
			return nil, err
		}
		out = append(out, runtimeName{
			term: strings.ToLower(term), dir: dir + "/", page: p.Claims.Page,
			site: "site/docs/" + stem + ".html", url: "/docs/" + stem, summary: summary,
		})
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("no profile under %s", profilesDir)
	}
	return out, nil
}

func pageSummary(fsys fs.FS, page string) (string, error) {
	raw, err := fs.ReadFile(fsys, page)
	if err != nil {
		return "", fmt.Errorf("the profile's page: %w", err)
	}
	for _, line := range strings.Split(string(raw), "\n") {
		if s, ok := strings.CutPrefix(line, "summary: "); ok {
			return s, nil
		}
	}
	return "", fmt.Errorf("%s: no summary", page)
}

// mdTo reports whether a Markdown link in file with target points at the
// page.
func (n runtimeName) mdTo(file, target string) bool {
	target, _, _ = strings.Cut(target, "#")
	if uriScheme.MatchString(target) {
		return n.urlTo(target)
	}
	return path.Join(path.Dir(file), target) == n.page
}

// urlTo reports whether a site URL or path points at the page's copy.
func (n runtimeName) urlTo(target string) bool {
	target, _, _ = strings.Cut(target, "#")
	target, _, _ = strings.Cut(target, "?")
	if i := strings.Index(target, "://"); i >= 0 {
		rest := target[i+3:]
		target = rest[strings.IndexByte(rest+"/", '/'):]
	}
	return strings.TrimSuffix(target, ".html") == n.url
}

// unlinked returns line without each link to the page, or empty when the line
// is an index row linking the page beside its own summary.
func (n runtimeName) unlinked(file, line string) string {
	if m := mdIndex.FindStringSubmatch(line); m != nil && n.mdTo(file, m[1]) && m[2] == n.summary {
		return ""
	}
	if m := htmlIndex.FindStringSubmatch(line); m != nil && n.urlTo(html.UnescapeString(m[1])) && html.UnescapeString(m[2]) == n.summary {
		return ""
	}
	line = mdLink.ReplaceAllStringFunc(line, func(s string) string {
		if n.mdTo(file, mdLink.FindStringSubmatch(s)[2]) {
			return ""
		}
		return s
	})
	for _, re := range []*regexp.Regexp{htmlLink, siteLoc} {
		line = re.ReplaceAllStringFunc(line, func(s string) string {
			if n.urlTo(html.UnescapeString(re.FindStringSubmatch(s)[1])) {
				return ""
			}
			return s
		})
	}
	return line
}

// runtimeNameProblems names every file and line among files that names a
// built-in profile's runtime where the rule does not let it.
func runtimeNameProblems(fsys fs.FS, files []string) ([]string, error) {
	names, err := runtimeNames(fsys, files)
	if err != nil {
		return nil, err
	}
	var problems []string
	for _, rel := range files {
		for _, n := range names {
			if strings.HasPrefix(rel, n.dir) || rel == n.page || rel == n.site {
				continue
			}
			if strings.Contains(strings.ToLower(rel), n.term) {
				problems = append(problems, rel+": the path names a runtime")
				continue
			}
			raw, err := fs.ReadFile(fsys, rel)
			if err != nil {
				return nil, err
			}
			if !strings.Contains(strings.ToLower(string(raw)), n.term) {
				continue
			}
			for i, line := range strings.Split(string(raw), "\n") {
				if strings.Contains(strings.ToLower(n.unlinked(rel, line)), n.term) {
					problems = append(problems, fmt.Sprintf("%s:%d: names a runtime outside its profile, its page and a link to the page", rel, i+1))
				}
			}
		}
	}
	return problems, nil
}

func TestARuntimeIsNamedOnlyByItsProfileAndPage(t *testing.T) {
	fsys, files := repoFS(t), repoFiles(t, repoRoot(t))
	names, err := runtimeNames(fsys, files)
	if err != nil {
		t.Fatal(err)
	}
	for _, n := range names {
		if page, err := fs.ReadFile(fsys, n.page); err != nil || !strings.Contains(strings.ToLower(string(page)), n.term) {
			t.Fatalf("%s does not name the runtime the rule looks for: %v", n.page, err)
		}
	}
	problems, err := runtimeNameProblems(fsys, files)
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range problems {
		t.Error(p)
	}
}
