package impact

import (
	"errors"
	"fmt"
	"path"
	"strings"
)

const (
	anyDepth = "**"
	// maxAnyDepth bounds the ** segments of one pattern once neighbours are
	// collapsed; a covers glob that needs more is describing no real tree.
	maxAnyDepth = 4
)

// Glob is a compiled covers or surface pattern: segments split on "/", where
// a segment of ** matches any number of path segments, including none, and
// every other segment follows path.Match, so * never crosses a slash. A run
// of ** segments is one, and a pattern holds at most four of them.
type Glob struct {
	text     string
	segments []string
}

// ErrBadGlob is wrapped by every refusal of a pattern.
var ErrBadGlob = errors.New("glob")

// Compile checks a pattern once, so a malformed one is refused where it is
// declared rather than matching nothing quietly.
func Compile(pattern string) (Glob, error) {
	if pattern == "" {
		return Glob{}, fmt.Errorf("%w: a pattern is empty", ErrBadGlob)
	}
	var segments []string
	for _, seg := range strings.Split(pattern, "/") {
		if err := checkSegment(pattern, seg); err != nil {
			return Glob{}, err
		}
		if seg == anyDepth && len(segments) > 0 && segments[len(segments)-1] == anyDepth {
			continue
		}
		segments = append(segments, seg)
	}
	if n := count(segments, anyDepth); n > maxAnyDepth {
		return Glob{}, fmt.Errorf("%w: %q holds %d ** segments; at most four are allowed", ErrBadGlob, pattern, n)
	}
	return Glob{text: pattern, segments: segments}, nil
}

func checkSegment(pattern, seg string) error {
	switch {
	case seg == anyDepth:
		return nil
	case strings.Contains(seg, anyDepth):
		return fmt.Errorf("%w: %q holds ** inside a segment; ** stands alone between slashes", ErrBadGlob, pattern)
	case seg == "":
		return fmt.Errorf("%w: %q holds an empty segment", ErrBadGlob, pattern)
	}
	if _, err := path.Match(seg, ""); err != nil {
		return fmt.Errorf("%w: %q: %w", ErrBadGlob, pattern, err)
	}
	return nil
}

func count(segments []string, seg string) int {
	n := 0
	for _, s := range segments {
		if s == seg {
			n++
		}
	}
	return n
}

// String returns the pattern as declared.
func (g Glob) String() string { return g.text }

// Match reports whether a clean slash path is under the pattern.
func (g Glob) Match(p string) bool {
	return matchSegments(g.segments, strings.Split(p, "/"))
}

// matchSegments walks pattern and path together. A ** is left behind as a
// mark to come back to: when a later segment fails, the match resumes after
// the last ** with one more path segment given to it. Every step moves one
// of two indices forward, so the work is bounded by the product of the two
// lengths and never by the number of ** segments.
func matchSegments(pattern, segs []string) bool {
	p, s := 0, 0
	star, mark := -1, 0
	for s < len(segs) {
		switch {
		case p < len(pattern) && pattern[p] == anyDepth:
			star, mark = p, s
			p++
		case p < len(pattern) && matchSegment(pattern[p], segs[s]):
			p++
			s++
		case star >= 0:
			mark++
			p, s = star+1, mark
		default:
			return false
		}
	}
	for p < len(pattern) && pattern[p] == anyDepth {
		p++
	}
	return p == len(pattern)
}

func matchSegment(pattern, seg string) bool {
	ok, err := path.Match(pattern, seg)
	return err == nil && ok
}
