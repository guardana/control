// The site checks, each a function over a file map or an fs.FS so that the
// committed tree and the failing fixtures run through the same code.
package docscheck

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"path"
	"regexp"
	"slices"
	"sort"
	"strings"
)

// xnode is one element of a page read as XML.
type xnode struct {
	name  string
	attrs map[string]string
	parts []xpart
}

// xpart is a run of text or a child element, in document order.
type xpart struct {
	text string
	node *xnode
}

// parseXHTML reads a page in strict XML mode, with HTML's named entities.
// A page that does not parse fails every check that reads it.
func parseXHTML(data []byte) (*xnode, error) {
	d := xml.NewDecoder(bytes.NewReader(data))
	d.Strict = true
	d.Entity = xml.HTMLEntity
	root := &xnode{name: "#document", attrs: map[string]string{}}
	stack := []*xnode{root}
	for {
		tok, err := d.Token()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return nil, err
		}
		top := stack[len(stack)-1]
		switch t := tok.(type) {
		case xml.StartElement:
			n := &xnode{name: t.Name.Local, attrs: map[string]string{}}
			for _, a := range t.Attr {
				if a.Name.Space == "" {
					n.attrs[a.Name.Local] = a.Value
				}
			}
			top.parts = append(top.parts, xpart{node: n})
			stack = append(stack, n)
		case xml.EndElement:
			stack = stack[:len(stack)-1]
		case xml.CharData:
			top.parts = append(top.parts, xpart{text: string(t)})
		}
	}
	var elements int
	for _, p := range root.parts {
		if p.node != nil {
			elements++
		}
	}
	if elements != 1 {
		return nil, fmt.Errorf("the document holds %d root elements, want 1", elements)
	}
	return root, nil
}

// walk visits n and every element below it, in document order.
func (n *xnode) walk(visit func(*xnode)) {
	visit(n)
	for _, p := range n.parts {
		if p.node != nil {
			p.node.walk(visit)
		}
	}
}

// text is the element's text content, whitespace collapsed.
func (n *xnode) text() string {
	var b strings.Builder
	var collect func(*xnode)
	collect = func(e *xnode) {
		for _, p := range e.parts {
			if p.node != nil {
				collect(p.node)
			} else {
				b.WriteString(p.text)
			}
		}
	}
	collect(n)
	return strings.Join(strings.Fields(b.String()), " ")
}

func (n *xnode) hasClass(class string) bool {
	return slices.Contains(strings.Fields(n.attrs["class"]), class)
}

var sumLine = regexp.MustCompile(`^([0-9a-f]{64})  (\S.*)$`)

// brandProblems holds a vendored brand directory to its SHA256SUMS: the
// file's own digest is pinned, every line is well formed, the lines and the
// files beside it are the same set with the same digests, and both number
// want. want below one is refused, so a check over nothing cannot pass.
func brandProblems(fsys fs.FS, pinned string, want int) []string {
	if want < 1 {
		return []string{fmt.Sprintf("the brand check expects %d files; it must examine at least one", want)}
	}
	sums, err := fs.ReadFile(fsys, "SHA256SUMS")
	if err != nil {
		return []string{fmt.Sprintf("reading SHA256SUMS: %v", err)}
	}
	var problems []string
	if got := sha256.Sum256(sums); hex.EncodeToString(got[:]) != pinned {
		problems = append(problems, "SHA256SUMS is not the pinned version; a change to the brand is a new version at a new path")
	}
	listed, lineProblems := parseSums(sums)
	problems = append(problems, lineProblems...)
	onDisk, err := digestFiles(fsys)
	if err != nil {
		return append(problems, fmt.Sprintf("walking the brand directory: %v", err))
	}
	problems = append(problems, compareSums(listed, onDisk)...)
	if len(listed) != want || len(onDisk) != want {
		problems = append(problems, fmt.Sprintf("SHA256SUMS lists %d files and %d are on disk, want %d each", len(listed), len(onDisk), want))
	}
	return problems
}

// parseSums reads `<sha256>  <path>` lines into a map by path.
func parseSums(sums []byte) (map[string]string, []string) {
	var problems []string
	listed := map[string]string{}
	for i, line := range strings.Split(strings.TrimSuffix(string(sums), "\n"), "\n") {
		m := sumLine.FindStringSubmatch(line)
		switch {
		case m == nil:
			problems = append(problems, fmt.Sprintf("SHA256SUMS line %d is malformed: %q", i+1, line))
		case listed[m[2]] != "":
			problems = append(problems, fmt.Sprintf("SHA256SUMS lists %s twice", m[2]))
		default:
			listed[m[2]] = m[1]
		}
	}
	return listed, problems
}

// digestFiles hashes every file under fsys but SHA256SUMS itself.
func digestFiles(fsys fs.FS) (map[string]string, error) {
	onDisk := map[string]string{}
	err := fs.WalkDir(fsys, ".", func(p string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil || d.IsDir() || p == "SHA256SUMS" {
			return walkErr
		}
		data, err := fs.ReadFile(fsys, p)
		if err != nil {
			return err
		}
		sum := sha256.Sum256(data)
		onDisk[p] = hex.EncodeToString(sum[:])
		return nil
	})
	return onDisk, err
}

func compareSums(listed, onDisk map[string]string) []string {
	var problems []string
	for _, name := range sortedKeys(onDisk) {
		switch digest, ok := listed[name]; {
		case !ok:
			problems = append(problems, fmt.Sprintf("%s is not listed in SHA256SUMS", name))
		case digest != onDisk[name]:
			problems = append(problems, fmt.Sprintf("%s does not match its line in SHA256SUMS", name))
		}
	}
	for _, name := range sortedKeys(listed) {
		if _, ok := onDisk[name]; !ok {
			problems = append(problems, fmt.Sprintf("SHA256SUMS lists %s, which is missing", name))
		}
	}
	return problems
}

// siteFiles reads every file under fsys, dotfiles included: the host
// uploads what the directory holds, not what a listing shows.
func siteFiles(fsys fs.FS) (map[string][]byte, error) {
	files := map[string][]byte{}
	err := fs.WalkDir(fsys, ".", func(p string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil || d.IsDir() {
			return walkErr
		}
		data, err := fs.ReadFile(fsys, p)
		files[p] = data
		return err
	})
	return files, err
}

// manifestProblems compares the files of the site with the pinned list.
func manifestProblems(files map[string][]byte, want []string) []string {
	if len(files) == 0 {
		return []string{"the site holds no file"}
	}
	var problems []string
	for _, name := range sortedKeys(files) {
		if !slices.Contains(want, name) {
			problems = append(problems, fmt.Sprintf("%s is not in the pinned manifest", name))
		}
	}
	for _, name := range want {
		if _, ok := files[name]; !ok {
			problems = append(problems, fmt.Sprintf("the pinned manifest names %s, which is missing", name))
		}
	}
	return problems
}

// hostRules are read over the lowercased text of every page, stylesheet and
// drawing: each names something that would load or run what the site does
// not serve itself.
var hostRules = []struct {
	name string
	re   *regexp.Regexp
}{
	{"a script element", regexp.MustCompile(`<script`)},
	{"an event handler attribute", regexp.MustCompile(`<[^>]*\son[a-z]+\s*=\s*["']`)},
	{"a javascript: URL", regexp.MustCompile(`javascript:`)},
	{"an @import", regexp.MustCompile(`@import`)},
	{"a srcset", regexp.MustCompile(`srcset`)},
	{"an xlink:href", regexp.MustCompile(`xlink:href`)},
	{"a <use> with a reference", regexp.MustCompile(`<use\b[^>]*href`)},
	{"a meta refresh", regexp.MustCompile(`http-equiv\s*=\s*["']?\s*refresh`)},
	{"a connection hint", regexp.MustCompile(`rel\s*=\s*["'][^"']*\b(preconnect|dns-prefetch|prefetch|prerender)\b`)},
	{"an embedding element", regexp.MustCompile(`<(iframe|object|embed|base)\b`)},
	{"a remote source", regexp.MustCompile(`\b(src|data|poster|action|formaction|background)\s*=\s*["']?\s*(https?:|//)`)},
	{"a protocol-relative URL", regexp.MustCompile(`=\s*["']\s*//`)},
	{"an <image> or <feImage> reference", regexp.MustCompile(`<(image|feimage)\b[^>]*href`)},
	{"a ping attribute", regexp.MustCompile(`\sping\s*=`)},
	{"an image-set()", regexp.MustCompile(`image-set\(`)},
}

var (
	linkTag  = regexp.MustCompile(`<link\b[^>]*>`)
	attrIn   = regexp.MustCompile(`\b(rel|href)\s*=\s*["']([^"']*)["']`)
	cssURL   = regexp.MustCompile(`url\(\s*['"]?\s*([^'")\s]*)`)
	anScheme = regexp.MustCompile(`^[a-z][a-z0-9+.\-]*:`)
)

// hostProblems finds anything under the site that loads from another host or
// runs script: every .html, .css and .svg, with a floor of one of each read.
func hostProblems(files map[string][]byte) []string {
	var problems []string
	read := map[string]int{}
	for _, name := range sortedKeys(files) {
		ext := path.Ext(name)
		if ext != ".html" && ext != ".css" && ext != ".svg" {
			continue
		}
		read[ext]++
		found := fileHostProblems(strings.ToLower(string(files[name])))
		if ext == ".html" {
			decoded, err := decodedTags(files[name])
			if err != nil {
				found = append(found, fmt.Sprintf("does not parse: %v", err))
			}
			found = append(found, fileHostProblems(strings.ToLower(decoded))...)
		}
		slices.Sort(found)
		for _, problem := range slices.Compact(found) {
			problems = append(problems, name+": "+problem)
		}
	}
	for _, ext := range []string{".html", ".css", ".svg"} {
		if read[ext] == 0 {
			problems = append(problems, fmt.Sprintf("no %s file was read", ext))
		}
	}
	return problems
}

// decodedTags writes a page's start tags back out with their attribute
// values as the XML reader decoded them, and the text of its style
// elements, so a character reference cannot hide a URL from the rules.
func decodedTags(page []byte) (string, error) {
	doc, err := parseXHTML(page)
	if err != nil {
		return "", err
	}
	var b strings.Builder
	doc.walk(func(n *xnode) {
		b.WriteString("<" + n.name)
		for _, name := range sortedKeys(n.attrs) {
			fmt.Fprintf(&b, " %s=%q", name, n.attrs[name])
		}
		b.WriteString(">")
		if n.name == "style" {
			b.WriteString(n.text())
		}
	})
	return b.String(), nil
}

// fileHostProblems judges one lowercased file: every rule, every <link> but
// a canonical one to another host, and every url() that is not a site path.
func fileHostProblems(text string) []string {
	var problems []string
	for _, rule := range hostRules {
		if rule.re.MatchString(text) {
			problems = append(problems, rule.name)
		}
	}
	for _, tag := range linkTag.FindAllString(text, -1) {
		attrs := map[string]string{}
		for _, m := range attrIn.FindAllStringSubmatch(tag, -1) {
			attrs[m[1]] = strings.TrimSpace(m[2])
		}
		if isRemote(attrs["href"]) && attrs["rel"] != "canonical" {
			problems = append(problems, fmt.Sprintf("a <link rel=%q> to %s", attrs["rel"], attrs["href"]))
		}
	}
	for _, m := range cssURL.FindAllStringSubmatch(text, -1) {
		if target := m[1]; isRemote(target) {
			problems = append(problems, fmt.Sprintf("url(%s) is not a path of the site", target))
		}
	}
	return problems
}

// isRemote is true for a URL with a scheme or a protocol-relative one.
func isRemote(target string) bool {
	return anScheme.MatchString(target) || strings.HasPrefix(target, "//")
}

// siteHeaders is every header the host sends, for the one path block.
var siteHeaders = map[string]string{
	"X-Frame-Options":           "DENY",
	"X-Content-Type-Options":    "nosniff",
	"Referrer-Policy":           "strict-origin-when-cross-origin",
	"Permissions-Policy":        "geolocation=(), microphone=(), camera=(), interest-cohort=()",
	"Strict-Transport-Security": "max-age=31536000; includeSubDomains",
	"Content-Security-Policy":   "",
}

// sitePolicy is the Content-Security-Policy, directive by directive.
var sitePolicy = map[string]string{
	"default-src":     "'none'",
	"style-src":       "'self' 'unsafe-inline'",
	"font-src":        "'self'",
	"img-src":         "'self'",
	"connect-src":     "'none'",
	"script-src":      "'none'",
	"base-uri":        "'none'",
	"form-action":     "'none'",
	"frame-ancestors": "'none'",
}

// headerProblems reads _headers: one block, `/*`, whose headers are exactly
// siteHeaders, each once, and whose one policy line is exactly sitePolicy.
func headerProblems(data []byte) []string {
	blocks, got, problems := parseHeaders(data)
	if !slices.Equal(blocks, []string{"/*"}) {
		problems = append(problems, fmt.Sprintf("the path blocks are %q, want exactly [/*]", blocks))
	}
	for _, name := range sortedKeys(siteHeaders) {
		switch value, ok := got[name]; {
		case !ok:
			problems = append(problems, fmt.Sprintf("%s is not set", name))
		case name == "Content-Security-Policy":
			problems = append(problems, policyProblems(value)...)
		case value != siteHeaders[name]:
			problems = append(problems, fmt.Sprintf("%s is %q, want %q", name, value, siteHeaders[name]))
		}
	}
	for _, name := range sortedKeys(got) {
		if _, ok := siteHeaders[name]; !ok {
			problems = append(problems, fmt.Sprintf("%s is not a header this site sends", name))
		}
	}
	return problems
}

// parseHeaders returns the path lines of _headers and the headers of the
// first block; a header repeated or a line of another shape is a problem.
func parseHeaders(data []byte) ([]string, map[string]string, []string) {
	var blocks, problems []string
	got := map[string]string{}
	for i, line := range strings.Split(strings.TrimSuffix(string(data), "\n"), "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}
		if !strings.HasPrefix(line, " ") {
			blocks = append(blocks, line)
			continue
		}
		name, value, ok := strings.Cut(strings.TrimSpace(line), ": ")
		switch {
		case !ok:
			problems = append(problems, fmt.Sprintf("line %d is not `Name: value`: %q", i+1, line))
		case len(blocks) != 1:
		case got[name] != "":
			problems = append(problems, fmt.Sprintf("line %d: %s is set twice", i+1, name))
		default:
			got[name] = value
		}
	}
	return blocks, got, problems
}

func policyProblems(value string) []string {
	var problems []string
	got := map[string]string{}
	for _, directive := range strings.Split(value, ";") {
		fields := strings.Fields(directive)
		if len(fields) == 0 {
			continue
		}
		if _, seen := got[fields[0]]; seen {
			problems = append(problems, fmt.Sprintf("the policy names %s twice", fields[0]))
		}
		got[fields[0]] = strings.Join(fields[1:], " ")
	}
	names := append(sortedKeys(got), sortedKeys(sitePolicy)...)
	sort.Strings(names)
	for _, name := range slices.Compact(names) {
		if got[name] != sitePolicy[name] {
			problems = append(problems, fmt.Sprintf("the policy's %s is %q, want %q", name, got[name], sitePolicy[name]))
		}
	}
	return problems
}
