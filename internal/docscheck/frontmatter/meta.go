package frontmatter

import (
	"errors"
	"fmt"
	"strings"
	"unicode/utf8"
)

// Meta is the metadata a page declares. The zero value is not a valid block:
// Validate refuses it, so a page nobody described cannot pass as described.
type Meta struct {
	Title   string
	Summary string
	// Type names the page's kind and, through the docs configuration, the
	// directory it may sit in.
	Type string
	// Covers lists the code whose change makes the page suspect, as globs
	// where ** means any depth. An empty list needs CoversReason.
	Covers       []string
	CoversReason string
	// Stability is declared by spec and extending pages only.
	Stability string
	// Generated names the generator of a rendered page: a scripts/gen-*.go
	// program or a `go test` pin. Hand edits to such a page do not survive.
	Generated string
}

// The keys, in the one order a block spells them.
const (
	keyTitle        = "title"
	keySummary      = "summary"
	keyType         = "type"
	keyCovers       = "covers"
	keyCoversReason = "covers_reason"
	keyStability    = "stability"
	keyGenerated    = "generated"
)

var keys = []string{keyTitle, keySummary, keyType, keyCovers, keyCoversReason, keyStability, keyGenerated}

// Types is every page type, in the order the index lists them.
var Types = []string{"tutorial", "how-to", "explanation", "reference", "spec", "extending", "project"}

// Stabilities is every stability level a spec or extending page may declare.
var Stabilities = []string{"development", "alpha", "beta", "stable", "deprecated"}

const (
	// MaxSummary is the longest summary, in characters.
	MaxSummary = 160
	// MaxCovers is the most globs one page may cover.
	MaxCovers = 32

	generatedScriptPrefix = "scripts/gen-"
	generatedScriptSuffix = ".go"
	generatedTestPrefix   = "go test "
)

// ErrInvalid is wrapped by every refusal of a block or a Meta.
var ErrInvalid = errors.New("frontmatter")

// Validate refuses a Meta that no page may carry: a missing title, summary,
// type or covers; a summary past MaxSummary characters; a type or stability
// outside the declared sets; stability on a page that is not spec or
// extending; covers past MaxCovers, a glob twice, or an empty list without a
// reason, or a reason beside a list; a generated value naming neither a
// script nor a `go test` pin; and any value the block could not spell.
func Validate(m Meta) error {
	for _, check := range []func(Meta) error{checkScalars, checkRequired, checkCovers, checkOptional} {
		if err := check(m); err != nil {
			return fmt.Errorf("%w: %w", ErrInvalid, err)
		}
	}
	return nil
}

func checkScalars(m Meta) error {
	for _, kv := range []struct{ key, value string }{
		{keyTitle, m.Title}, {keySummary, m.Summary}, {keyType, m.Type},
		{keyCoversReason, m.CoversReason}, {keyStability, m.Stability}, {keyGenerated, m.Generated},
	} {
		if kv.value == "" {
			continue
		}
		if err := checkScalar(kv.value); err != nil {
			return fmt.Errorf("%s: %w", kv.key, err)
		}
	}
	return nil
}

func checkRequired(m Meta) error {
	switch {
	case m.Title == "":
		return errors.New("title is required")
	case m.Summary == "":
		return errors.New("summary is required")
	case utf8.RuneCountInString(m.Summary) > MaxSummary:
		return fmt.Errorf("summary is %d characters, the most is %d", utf8.RuneCountInString(m.Summary), MaxSummary)
	case m.Type == "":
		return errors.New("type is required")
	case !contains(Types, m.Type):
		return fmt.Errorf("type %q is not one of %s", m.Type, strings.Join(Types, ", "))
	}
	return nil
}

func checkCovers(m Meta) error {
	switch {
	case len(m.Covers) > MaxCovers:
		return fmt.Errorf("covers lists %d globs, the most is %d", len(m.Covers), MaxCovers)
	case len(m.Covers) == 0 && m.CoversReason == "":
		return errors.New("covers is empty and covers_reason says nothing")
	case len(m.Covers) > 0 && m.CoversReason != "":
		return errors.New("covers_reason is for an empty covers only")
	}
	for i, glob := range m.Covers {
		if err := checkItem(glob); err != nil {
			return fmt.Errorf("covers[%d]: %w", i, err)
		}
		if contains(m.Covers[:i], glob) {
			return fmt.Errorf("covers lists %q twice", glob)
		}
	}
	return nil
}

func checkOptional(m Meta) error {
	switch {
	case m.Stability != "" && !contains(Stabilities, m.Stability):
		return fmt.Errorf("stability %q is not one of %s", m.Stability, strings.Join(Stabilities, ", "))
	case m.Stability != "" && m.Type != "spec" && m.Type != "extending":
		return fmt.Errorf("stability is declared by spec and extending pages, not by a %s page", m.Type)
	case m.Generated != "" && !generatedOK(m.Generated):
		return fmt.Errorf("generated %q names neither %s<x>%s nor a %q pin", m.Generated, generatedScriptPrefix, generatedScriptSuffix, generatedTestPrefix)
	}
	return nil
}

func generatedOK(v string) bool {
	if strings.HasPrefix(v, generatedTestPrefix) {
		return true
	}
	name := strings.TrimPrefix(v, generatedScriptPrefix)
	return name != v && strings.HasSuffix(name, generatedScriptSuffix) &&
		len(name) > len(generatedScriptSuffix) && !strings.Contains(name, "/")
}

func contains(list []string, s string) bool {
	for _, x := range list {
		if x == s {
			return true
		}
	}
	return false
}
