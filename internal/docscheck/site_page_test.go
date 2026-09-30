package docscheck

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"path"
	"reflect"
	"regexp"
	"strconv"
	"strings"

	"github.com/guardana/control/internal/brand"
	"github.com/guardana/control/internal/docscheck/sitedoc"
	"github.com/guardana/control/internal/docscheck/sitedoc/mermaid"
)

// linkTargets lists what an element points at: href and src, and every
// url(...) inside any attribute, such as an SVG marker reference.
func linkTargets(n *xnode) []string {
	var targets []string
	for _, name := range sortedKeys(n.attrs) {
		value := n.attrs[name]
		if name == "href" || name == "src" {
			targets = append(targets, value)
		}
		for _, m := range cssURL.FindAllStringSubmatch(value, -1) {
			targets = append(targets, m[1])
		}
	}
	return targets
}

func idsOf(doc *xnode) map[string]bool {
	ids := map[string]bool{}
	doc.walk(func(n *xnode) {
		if id, ok := n.attrs["id"]; ok {
			ids[id] = true
		}
	})
	return ids
}

// linkProblems resolves every local link of the site's pages and
// stylesheets, and every link to a file of this repository on its host,
// against repo. It returns the number of links it resolved; zero is a
// problem, because a check that followed nothing proved nothing.
func linkProblems(site map[string][]byte, repo fs.FS) ([]string, int) {
	var problems []string
	docs := map[string]*xnode{}
	for _, name := range sortedKeys(site) {
		if path.Ext(name) != ".html" {
			continue
		}
		doc, err := parseXHTML(site[name])
		if err != nil {
			problems = append(problems, fmt.Sprintf("%s: %v", name, err))
			continue
		}
		docs[name] = doc
	}
	resolved := 0
	check := func(from, target string) {
		if problem, counted := resolveLink(site, docs, repo, from, target); counted {
			resolved++
			if problem != "" {
				problems = append(problems, fmt.Sprintf("%s: %s", from, problem))
			}
		}
	}
	for _, name := range sortedKeys(docs) {
		docs[name].walk(func(n *xnode) {
			for _, target := range linkTargets(n) {
				check(name, target)
			}
		})
	}
	for _, name := range sortedKeys(site) {
		if path.Ext(name) == ".css" {
			for _, m := range cssURL.FindAllStringSubmatch(string(site[name]), -1) {
				check(name, m[1])
			}
		}
	}
	if resolved == 0 {
		problems = append(problems, "no local link was resolved")
	}
	return problems, resolved
}

// resolveLink judges one target and says whether it was one to judge: a
// link to another host is not.
func resolveLink(site map[string][]byte, docs map[string]*xnode, repo fs.FS, from, target string) (string, bool) {
	if problem, ok := repoLinkProblem(repo, target); ok {
		return problem, true
	}
	if isRemote(target) {
		return "", false
	}
	dest, fragment, _ := strings.Cut(target, "#")
	if dest == "" && fragment == "" {
		return "an empty link", true
	}
	file, ok := served(site, sitePath(from, dest))
	if !ok {
		return fmt.Sprintf("%s resolves to %s, which the site does not hold", target, file), true
	}
	if fragment != "" {
		doc, ok := docs[file]
		if !ok || !idsOf(doc)[fragment] {
			return fmt.Sprintf("%s: %s holds no id %q", target, file, fragment), true
		}
	}
	return "", true
}

// repoLinkProblem judges a link to a file or directory of this repository
// on its host, and says whether target was one.
func repoLinkProblem(repo fs.FS, target string) (string, bool) {
	for _, kind := range []string{"blob", "tree"} {
		rest, ok := strings.CutPrefix(target, "https://"+brand.ModulePath+"/"+kind+"/main/")
		if !ok {
			continue
		}
		rel, fragment, _ := strings.Cut(rest, "#")
		info, err := fs.Stat(repo, rel)
		switch {
		case err != nil:
			return fmt.Sprintf("%s names %q, which is not in the repository", target, rel), true
		case info.IsDir() != (kind == "tree"):
			return fmt.Sprintf("%s: a %s link to a %s", target, kind, map[bool]string{true: "directory", false: "file"}[info.IsDir()]), true
		case fragment != "" && path.Ext(rel) != ".md":
			return fmt.Sprintf("%s: a fragment into %s, whose anchors cannot be checked", target, rel), true
		case fragment != "":
			anchors, err := newAnchorIndex(repo).anchors(rel)
			if err != nil {
				return fmt.Sprintf("%s: %v", target, err), true
			}
			if !anchors[fragment] {
				return fmt.Sprintf("%s: %s has no heading #%s", target, rel, fragment), true
			}
		}
		return "", true
	}
	return "", false
}

// sitePath resolves a link's path part against the file it appears in; a
// directory stands for its index.html.
func sitePath(from, dest string) string {
	file := from
	switch {
	case dest == "":
	case strings.HasPrefix(dest, "/"):
		file = strings.TrimPrefix(dest, "/")
	default:
		file = path.Join(path.Dir(from), dest)
	}
	if file == "" || strings.HasSuffix(file, "/") {
		file += "index.html"
	}
	return path.Clean(file)
}

// served is the file the host answers a path with: the file itself, or, for
// a path with no extension, the page of that name or the directory's
// index.html, as `auto-trailing-slash` serves them.
func served(site map[string][]byte, file string) (string, bool) {
	if _, ok := site[file]; ok {
		return file, true
	}
	if path.Ext(file) != "" {
		return file, false
	}
	for _, candidate := range []string{file + ".html", file + "/index.html"} {
		if _, ok := site[candidate]; ok {
			return candidate, true
		}
	}
	return file, false
}

// idProblems refuses an empty or repeated id: a fragment or a drawing's
// reference to it would reach the wrong element.
func idProblems(doc *xnode) []string {
	var problems []string
	seen := map[string]bool{}
	doc.walk(func(n *xnode) {
		id, ok := n.attrs["id"]
		switch {
		case !ok:
		case id == "":
			problems = append(problems, fmt.Sprintf("a <%s> has an empty id", n.name))
		case seen[id]:
			problems = append(problems, fmt.Sprintf("the id %q is used twice", id))
		default:
			seen[id] = true
		}
	})
	return problems
}

var figureID = regexp.MustCompile(`^dg[0-9a-f]{6}([1-9][0-9]*)$`)

// figureIndexes lists the index each drawn figure carries in its id, in
// page order.
func figureIndexes(doc *xnode) ([]int, []string) {
	var indexes []int
	var problems []string
	doc.walk(func(n *xnode) {
		if n.name != "figure" || !n.hasClass("dg") {
			return
		}
		m := figureID.FindStringSubmatch(n.attrs["id"])
		if m == nil {
			problems = append(problems, fmt.Sprintf("figure id %q does not carry an index", n.attrs["id"]))
			return
		}
		i, err := strconv.Atoi(m[1])
		if err != nil {
			problems = append(problems, err.Error())
			return
		}
		indexes = append(indexes, i)
	})
	return indexes, problems
}

var backlogID = regexp.MustCompile(`^B[0-9]{2}$`)

// statusRows maps each row of docs/status.md's component table to its label.
func statusRows(lines []string) map[string]string {
	rows := map[string]string{}
	in := false
	for _, line := range lines {
		s := strings.TrimSpace(line)
		switch {
		case strings.HasPrefix(s, "## "):
			in = s == "## Components"
		case in && strings.HasPrefix(s, "|") && !tableSeparator.MatchString(s):
			if cells := tableCells(s); len(cells) >= 2 && cells[0] != "Component" {
				rows[cells[0]] = strings.Trim(cells[1], "`")
			}
		}
	}
	return rows
}

// backlogTitles maps each task identifier of docs/backlog.md's tables to
// its Work cell.
func backlogTitles(lines []string) map[string]string {
	titles := map[string]string{}
	for _, line := range lines {
		if s := strings.TrimSpace(line); strings.HasPrefix(s, "|") {
			if cells := tableCells(s); len(cells) >= 2 && backlogID.MatchString(cells[0]) {
				titles[cells[0]] = cells[1]
			}
		}
	}
	return titles
}

// tagProblems holds the page's status and backlog items to their sources.
// Each data-status element names a component row exactly and shows that
// row's own label in one `st` element; a list item also shows the row's name
// in one `t` element, and an inline label is its own `st` element. A `st`
// element outside a data-status element is refused. Each data-backlog item
// names a task and shows its title in one `t` element. The list items
// number between minStatus and maxStatus, the backlog items at least
// minBacklog.
func tagProblems(doc *xnode, status, backlog map[string]string, minStatus, maxStatus, minBacklog int) []string {
	tally := &tagTally{status: status, backlog: backlog}
	tally.visit(doc, false)
	problems := tally.problems
	if tally.statusItems < minStatus || tally.statusItems > maxStatus {
		problems = append(problems, fmt.Sprintf("%d status items, want %d to %d", tally.statusItems, minStatus, maxStatus))
	}
	if tally.backlogItems < minBacklog {
		problems = append(problems, fmt.Sprintf("%d backlog items, want at least %d", tally.backlogItems, minBacklog))
	}
	return problems
}

// tagTally walks a page for tagProblems, counting items as it goes.
type tagTally struct {
	status, backlog           map[string]string
	statusItems, backlogItems int
	problems                  []string
}

func (c *tagTally) add(problem string) {
	if problem != "" {
		c.problems = append(c.problems, problem)
	}
}

func (c *tagTally) visit(n *xnode, inStatus bool) {
	row, isStatus := n.attrs["data-status"]
	switch {
	case isStatus:
		if !n.hasClass("st") {
			c.statusItems++
		}
		c.add(statusItemProblem(n, row, c.status))
	case n.hasClass("st") && !inStatus:
		c.add(fmt.Sprintf("a status label %q outside a data-status item", n.text()))
	}
	if id, ok := n.attrs["data-backlog"]; ok {
		c.backlogItems++
		c.add(backlogItemProblem(n, id, c.backlog))
	}
	for _, p := range n.parts {
		if p.node != nil {
			c.visit(p.node, inStatus || isStatus)
		}
	}
}

// texts lists the text of n and of every element below it that carries class.
func (n *xnode) texts(class string) []string {
	var found []string
	n.walk(func(e *xnode) {
		if e.hasClass(class) {
			found = append(found, e.text())
		}
	})
	return found
}

func statusItemProblem(n *xnode, row string, status map[string]string) string {
	label, known := status[row]
	switch {
	case row == "":
		return "an empty data-status"
	case !known:
		return fmt.Sprintf("data-status %q names no row of the component table", row)
	}
	if shown := n.texts("st"); len(shown) != 1 || shown[0] != label {
		return fmt.Sprintf("data-status %q shows %q, want the row's label %q", row, shown, label)
	}
	if n.hasClass("st") {
		return ""
	}
	if titled := n.texts("t"); len(titled) != 1 || titled[0] != row {
		return fmt.Sprintf("data-status %q is titled %q, want the row %q", row, titled, row)
	}
	return ""
}

func backlogItemProblem(n *xnode, id string, backlog map[string]string) string {
	title, known := backlog[id]
	switch {
	case id == "":
		return "an empty data-backlog"
	case !known:
		return fmt.Sprintf("data-backlog %q names no task of the backlog", id)
	}
	if titled := n.texts("t"); len(titled) != 1 || titled[0] != title {
		return fmt.Sprintf("data-backlog %q is titled %q, want the task %q", id, titled, title)
	}
	return ""
}

var boldLine = regexp.MustCompile(`^\*\*(.+)\*\*$`)

// readmeTagline is the README's first line set wholly in bold.
func readmeTagline(lines []string) (string, error) {
	for _, line := range lines {
		if m := boldLine.FindStringSubmatch(strings.TrimSpace(line)); m != nil {
			return m[1], nil
		}
	}
	return "", errors.New("the README holds no bold tagline line")
}

// taglineProblem holds the one `tagline` element of a page to want.
func taglineProblem(doc *xnode, want string) string {
	var found []string
	doc.walk(func(n *xnode) {
		if n.hasClass("tagline") {
			found = append(found, n.text())
		}
	})
	switch {
	case len(found) != 1:
		return fmt.Sprintf("%d tagline elements, want 1", len(found))
	case found[0] != want:
		return fmt.Sprintf("the tagline reads %q, want the README's %q", found[0], want)
	}
	return ""
}

// wranglerConfig is the whole deploy configuration, key for key.
func wranglerConfig() map[string]any {
	return map[string]any{
		"name":               brand.Slug,
		"compatibility_date": "2026-09-28",
		"assets": map[string]any{
			"directory":     "./site",
			"html_handling": "auto-trailing-slash",
		},
		"routes": []any{map[string]any{
			"pattern":       strings.TrimSuffix(strings.TrimPrefix(siteOrigin, "https://"), "/"),
			"custom_domain": true,
		}},
		"workers_dev":  false,
		"preview_urls": false,
	}
}

// wranglerProblems decodes the deploy file as strict JSON: no comment, no
// repeated key, nothing after the value, and exactly the pinned keys.
func wranglerProblems(data []byte) []string {
	d := json.NewDecoder(bytes.NewReader(data))
	if err := duplicateKeys(d, "$"); err != nil {
		return []string{err.Error()}
	}
	if _, err := d.Token(); !errors.Is(err, io.EOF) {
		return []string{"something follows the configuration's value"}
	}
	var got any
	if err := json.Unmarshal(data, &got); err != nil {
		return []string{err.Error()}
	}
	gotMap, ok := got.(map[string]any)
	if !ok {
		return []string{"the configuration is not an object"}
	}
	want := wranglerConfig()
	var problems []string
	keys := append(sortedKeys(gotMap), sortedKeys(want)...)
	seen := map[string]bool{}
	for _, key := range keys {
		if seen[key] {
			continue
		}
		seen[key] = true
		if !reflect.DeepEqual(gotMap[key], want[key]) {
			problems = append(problems, fmt.Sprintf("%s is %v, want %v", key, gotMap[key], want[key]))
		}
	}
	return problems
}

// duplicateKeys reads one JSON value and refuses an object that names a key
// twice, which encoding/json would silently resolve to the last.
func duplicateKeys(d *json.Decoder, at string) error {
	tok, err := d.Token()
	if err != nil {
		return fmt.Errorf("%s: %w", at, err)
	}
	switch tok {
	case json.Delim('{'):
		seen := map[string]bool{}
		for d.More() {
			key, err := d.Token()
			if err != nil {
				return fmt.Errorf("%s: %w", at, err)
			}
			name, _ := key.(string)
			if seen[name] {
				return fmt.Errorf("%s: the key %q appears twice", at, name)
			}
			seen[name] = true
			if err := duplicateKeys(d, at+"."+name); err != nil {
				return err
			}
		}
		_, err = d.Token()
	case json.Delim('['):
		for i := 0; d.More(); i++ {
			if err := duplicateKeys(d, fmt.Sprintf("%s[%d]", at, i)); err != nil {
				return err
			}
		}
		_, err = d.Token()
	}
	if err != nil {
		return fmt.Errorf("%s: %w", at, err)
	}
	return nil
}

// slotTitleProblems holds each slot of the page to the accessible title of
// the block it draws, so a block inserted in the README cannot put another
// diagram under a slot's unchanged number.
func slotTitleProblems(page, readme []byte, want []string) []string {
	slots, err := sitedoc.Slots(page)
	if err != nil {
		return []string{err.Error()}
	}
	blocks, err := sitedoc.Blocks(readme)
	if err != nil {
		return []string{err.Error()}
	}
	if len(slots) != len(want) {
		return []string{fmt.Sprintf("%d slots, want %d", len(slots), len(want))}
	}
	var problems []string
	for i, slot := range slots {
		if slot.Block > len(blocks) {
			problems = append(problems, fmt.Sprintf("slot %d asks for block %d; the README holds %d", i+1, slot.Block, len(blocks)))
			continue
		}
		g, err := mermaid.Parse(blocks[slot.Block-1])
		switch {
		case err != nil:
			problems = append(problems, fmt.Sprintf("slot %d: %v", i+1, err))
		case g.Title != want[i]:
			problems = append(problems, fmt.Sprintf("slot %d draws %q, want %q", i+1, g.Title, want[i]))
		}
	}
	return problems
}
