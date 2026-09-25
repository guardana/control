package impact

import (
	"errors"
	"fmt"
	"path"
	"slices"
	"strings"
	"unicode"
)

// Page is one documentation page as the walk found it: its repository-
// relative path and the covers globs its frontmatter declares.
type Page struct {
	Path   string
	Covers []string
}

// Review is a page a change makes suspect, with the changed paths under its
// covers.
type Review struct {
	Page    string
	Changed []string
}

// Uncovered is a changed path under a documentable surface that no page
// covers, with the first surface that names it.
type Uncovered struct {
	Path    string
	Surface string
}

// Dead is a covers glob under which the walk holds no file.
type Dead struct {
	Page string
	Glob string
}

// Frozen is a changed path that is a frozen path or lies in a frozen
// directory, with the entry that freezes it: a contract change.
type Frozen struct {
	Path  string
	Under string
}

// Result is what one run examined and the four lists it found. The counts
// are printed beside every list, so an empty list says how much was looked
// at to find nothing.
type Result struct {
	PagesExamined    int
	SurfacesExamined int
	FrozenExamined   int
	FilesExamined    int
	// FilesSource says where the files came from, git or a walk, when the
	// caller knows; String prints it beside the count.
	FilesSource     string
	ChangedExamined int
	Review          []Review
	Uncovered       []Uncovered
	Frozen          []Frozen
	Dead            []Dead
}

// ErrInvalid is wrapped by every refusal of an input.
var ErrInvalid = errors.New("impact")

type page struct {
	path   string
	covers []Glob
}

// Report judges the changed paths against the pages' covers, the surfaces
// and the frozen paths, and every covers glob against the files of the walk.
// A set with no page, no surface or no file is refused: such a run would
// examine nothing and report that nothing is suspect. Nothing frozen is
// allowed, and the count says so.
func Report(pages []Page, surfaces, frozen, files, changed []string) (Result, error) {
	ps, err := compilePages(pages)
	if err != nil {
		return Result{}, err
	}
	ss, err := compileSurfaces(surfaces)
	if err != nil {
		return Result{}, err
	}
	if err := checkFrozen(frozen); err != nil {
		return Result{}, err
	}
	if len(files) == 0 {
		return Result{}, fmt.Errorf("%w: the walk holds no file", ErrInvalid)
	}
	if err := checkChanged(changed); err != nil {
		return Result{}, err
	}
	r := Result{
		PagesExamined:    len(ps),
		SurfacesExamined: len(ss),
		FrozenExamined:   len(frozen),
		FilesExamined:    len(files),
		ChangedExamined:  len(changed),
	}
	for _, p := range ps {
		if hits := matching(p.covers, changed); len(hits) > 0 {
			r.Review = append(r.Review, Review{Page: p.path, Changed: hits})
		}
		for _, g := range p.covers {
			if !slices.ContainsFunc(files, g.Match) {
				r.Dead = append(r.Dead, Dead{Page: p.path, Glob: g.String()})
			}
		}
	}
	r.judgeChanged(ps, ss, frozen, changed)
	return r, nil
}

// judgeChanged fills the frozen and the uncovered lists: a changed path
// under a frozen entry is a contract change whether or not a page covers
// it, and a path under a surface with no covering page is uncovered.
func (r *Result) judgeChanged(ps []page, ss []Glob, frozen, changed []string) {
	for _, c := range changed {
		if i := slices.IndexFunc(frozen, func(f string) bool { return underFrozen(f, c) }); i >= 0 {
			r.Frozen = append(r.Frozen, Frozen{Path: c, Under: frozen[i]})
		}
		if covered(ps, c) {
			continue
		}
		if i := slices.IndexFunc(ss, func(g Glob) bool { return g.Match(c) }); i >= 0 {
			r.Uncovered = append(r.Uncovered, Uncovered{Path: c, Surface: ss[i].String()})
		}
	}
}

// underFrozen reports whether p is the frozen entry itself or, for an entry
// ending in a slash, a path inside that directory.
func underFrozen(entry, p string) bool {
	return p == entry || strings.HasSuffix(entry, "/") && strings.HasPrefix(p, entry)
}

// Covering names the pages whose covers hold one path.
func Covering(pages []Page, p string) ([]string, error) {
	ps, err := compilePages(pages)
	if err != nil {
		return nil, err
	}
	if err := checkPath(p); err != nil {
		return nil, fmt.Errorf("%w: %w", ErrInvalid, err)
	}
	var out []string
	for _, pg := range ps {
		if len(matching(pg.covers, []string{p})) > 0 {
			out = append(out, pg.path)
		}
	}
	return out, nil
}

// String spells the lists, each with the counts behind it. The first line
// is how many changed paths were read: a producer that gave nothing shows
// as zero read, never as nothing to review.
func (r Result) String() string {
	var b strings.Builder
	fmt.Fprintf(&b, "%d changed paths read\n", r.ChangedExamined)
	fmt.Fprintf(&b, "%d pages to review of %d examined\n", len(r.Review), r.PagesExamined)
	for _, rv := range r.Review {
		fmt.Fprintf(&b, "  %s <- %s\n", rv.Page, strings.Join(rv.Changed, ", "))
	}
	fmt.Fprintf(&b, "%d changed paths under a surface no page covers, of %d surfaces examined\n", len(r.Uncovered), r.SurfacesExamined)
	for _, u := range r.Uncovered {
		fmt.Fprintf(&b, "  %s (%s)\n", u.Path, u.Surface)
	}
	fmt.Fprintf(&b, "%d changed paths under a frozen path: a contract change, of %d frozen paths examined\n", len(r.Frozen), r.FrozenExamined)
	for _, f := range r.Frozen {
		fmt.Fprintf(&b, "  %s (%s)\n", f.Path, f.Under)
	}
	fmt.Fprintf(&b, "%d covers globs matching no file, of %d pages and %d files examined%s\n", len(r.Dead), r.PagesExamined, r.FilesExamined, source(r.FilesSource))
	for _, d := range r.Dead {
		fmt.Fprintf(&b, "  %s: %s\n", d.Page, d.Glob)
	}
	return b.String()
}

func source(s string) string {
	if s == "" {
		return ""
	}
	return " (" + s + ")"
}

func compilePages(pages []Page) ([]page, error) {
	if len(pages) == 0 {
		return nil, fmt.Errorf("%w: no page to examine", ErrInvalid)
	}
	out := make([]page, 0, len(pages))
	for i, p := range pages {
		if p.Path == "" {
			return nil, fmt.Errorf("%w: page %d has no path", ErrInvalid, i)
		}
		if slices.ContainsFunc(out, func(q page) bool { return q.path == p.Path }) {
			return nil, fmt.Errorf("%w: %s is listed twice", ErrInvalid, p.Path)
		}
		globs, err := compileAll(p.Covers)
		if err != nil {
			return nil, fmt.Errorf("%w: %s: covers: %w", ErrInvalid, p.Path, err)
		}
		out = append(out, page{path: p.Path, covers: globs})
	}
	return out, nil
}

func compileSurfaces(surfaces []string) ([]Glob, error) {
	if len(surfaces) == 0 {
		return nil, fmt.Errorf("%w: no surface to examine", ErrInvalid)
	}
	globs, err := compileAll(surfaces)
	if err != nil {
		return nil, fmt.Errorf("%w: surfaces: %w", ErrInvalid, err)
	}
	return globs, nil
}

func compileAll(patterns []string) ([]Glob, error) {
	globs := make([]Glob, 0, len(patterns))
	for _, p := range patterns {
		g, err := Compile(p)
		if err != nil {
			return nil, err
		}
		globs = append(globs, g)
	}
	return globs, nil
}

// checkFrozen admits the frozen entries of the configuration: each a clean
// path, a directory spelled with its trailing slash, listed once.
func checkFrozen(frozen []string) error {
	for i, f := range frozen {
		if err := checkPath(strings.TrimSuffix(f, "/")); err != nil {
			return fmt.Errorf("%w: frozen: %w", ErrInvalid, err)
		}
		if slices.Contains(frozen[:i], f) {
			return fmt.Errorf("%w: frozen: %s is listed twice", ErrInvalid, f)
		}
	}
	return nil
}

func checkChanged(changed []string) error {
	for i, c := range changed {
		if err := checkPath(c); err != nil {
			return fmt.Errorf("%w: changed: %w", ErrInvalid, err)
		}
		if slices.Contains(changed[:i], c) {
			return fmt.Errorf("%w: changed: %s is listed twice", ErrInvalid, c)
		}
	}
	return nil
}

// checkPath admits a clean, relative, slash path with no parent reference
// and no white space, control byte or byte order mark: what git and a walk
// of this repository produce. A line that is not such a path, a signature
// line of the log or a byte order mark before a path, is refused rather than
// matched against nothing.
func checkPath(p string) error {
	switch {
	case p == "":
		return errors.New("a path is empty")
	case strings.Contains(p, "\ufeff"):
		return fmt.Errorf("%q holds a byte order mark", p)
	case strings.ContainsFunc(p, unicode.IsSpace):
		return fmt.Errorf("%q holds white space", p)
	case strings.ContainsFunc(p, unicode.IsControl):
		return fmt.Errorf("%q holds a control byte", p)
	case strings.HasPrefix(p, "/"), strings.Contains(p, "\\"):
		return fmt.Errorf("%q is not a relative slash path", p)
	case path.Clean(p) != p, p == "." || p == "..", strings.HasPrefix(p, "../"):
		return fmt.Errorf("%q is not a clean path", p)
	}
	return nil
}

func matching(globs []Glob, paths []string) []string {
	var hits []string
	for _, p := range paths {
		if slices.ContainsFunc(globs, func(g Glob) bool { return g.Match(p) }) {
			hits = append(hits, p)
		}
	}
	return hits
}

func covered(ps []page, p string) bool {
	for _, pg := range ps {
		if len(matching(pg.covers, []string{p})) > 0 {
			return true
		}
	}
	return false
}
