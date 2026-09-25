// Package metricstest reads the Prometheus text exposition format, version
// 0.0.4, strictly, so a test can hold what /metrics writes to the format
// rather than to the renderer's own idea of it.
//
// It reads the subset the renderer writes and refuses the rest, including
// input the format itself admits: a blank line, a comment that is neither
// HELP nor TYPE, a family without HELP and TYPE ahead of its samples, a
// timestamp, a trailing comma in a label set, and any type but counter and
// gauge.
package metricstest

import (
	"errors"
	"fmt"
	"maps"
	"math"
	"slices"
	"strconv"
	"strings"
	"unicode/utf8"
)

// Family is one metric family: its help, its type and its samples in the
// order the text gives them.
type Family struct {
	Name    string
	Help    string
	Type    string
	Samples []Sample
}

// Sample is one line of a family. Labels is empty, never nil, for a sample
// with no label set.
type Sample struct {
	Labels map[string]string
	// Raw is the value exactly as the text spells it.
	Raw   string
	Value float64
}

// Parse reads text into its families, in order, or refuses it naming the
// line.
func Parse(text []byte) ([]Family, error) {
	if len(text) == 0 {
		return nil, errors.New("the exposition is empty")
	}
	if !utf8.Valid(text) {
		return nil, errors.New("the exposition is not UTF-8")
	}
	if text[len(text)-1] != '\n' {
		return nil, errors.New("the exposition does not end with a newline")
	}
	var r reader
	for i, line := range strings.Split(string(text[:len(text)-1]), "\n") {
		if err := r.line(line); err != nil {
			return nil, fmt.Errorf("line %d: %w", i+1, err)
		}
	}
	if r.pendingType {
		return nil, fmt.Errorf("%s has a HELP line and no TYPE line", r.cur().Name)
	}
	return r.families, nil
}

// Find returns the family called name.
func Find(families []Family, name string) (Family, bool) {
	for _, f := range families {
		if f.Name == name {
			return f, true
		}
	}
	return Family{}, false
}

// One returns the sample of f whose labels are exactly labels, and false
// where there is none.
func (f Family) One(labels map[string]string) (Sample, bool) {
	for _, s := range f.Samples {
		if maps.Equal(s.Labels, labels) {
			return s, true
		}
	}
	return Sample{}, false
}

type reader struct {
	families    []Family
	seen        map[string]bool
	pendingType bool
	keys        map[string]bool
}

func (r *reader) cur() *Family { return &r.families[len(r.families)-1] }

func (r *reader) line(line string) error {
	switch {
	case line == "":
		return errors.New("a blank line")
	case strings.HasPrefix(line, "# HELP "):
		return r.help(strings.TrimPrefix(line, "# HELP "))
	case strings.HasPrefix(line, "# TYPE "):
		return r.typ(strings.TrimPrefix(line, "# TYPE "))
	case strings.HasPrefix(line, "#"):
		return fmt.Errorf("a comment that is neither HELP nor TYPE: %q", line)
	}
	return r.sample(line)
}

func (r *reader) help(rest string) error {
	if r.pendingType {
		return fmt.Errorf("%s has a HELP line and no TYPE line", r.cur().Name)
	}
	name, text, ok := strings.Cut(rest, " ")
	if !ok || text == "" {
		return fmt.Errorf("HELP %q carries no text", rest)
	}
	if !validName(name) {
		return fmt.Errorf("%q is not a metric name", name)
	}
	if r.seen[name] {
		return fmt.Errorf("%s is described twice", name)
	}
	help, err := unescape(text, false)
	if err != nil {
		return fmt.Errorf("HELP of %s: %w", name, err)
	}
	if r.seen == nil {
		r.seen = map[string]bool{}
	}
	r.seen[name] = true
	r.families = append(r.families, Family{Name: name, Help: help})
	r.pendingType, r.keys = true, map[string]bool{}
	return nil
}

func (r *reader) typ(rest string) error {
	if !r.pendingType {
		return fmt.Errorf("TYPE %q does not follow a HELP line", rest)
	}
	name, typ, _ := strings.Cut(rest, " ")
	if name != r.cur().Name {
		return fmt.Errorf("TYPE names %q after the HELP of %s", name, r.cur().Name)
	}
	if typ != "counter" && typ != "gauge" {
		return fmt.Errorf("%s has type %q, not counter or gauge", name, typ)
	}
	r.cur().Type, r.pendingType = typ, false
	return nil
}

func (r *reader) sample(line string) error {
	if len(r.families) == 0 || r.pendingType {
		return fmt.Errorf("a sample before its family's HELP and TYPE: %q", line)
	}
	f := r.cur()
	if !strings.HasPrefix(line, f.Name) {
		return fmt.Errorf("a sample of another family inside %s: %q", f.Name, line)
	}
	rest := line[len(f.Name):]
	labels := map[string]string{}
	if strings.HasPrefix(rest, "{") {
		var err error
		if labels, rest, err = labelSet(rest[1:]); err != nil {
			return fmt.Errorf("%s: %w", f.Name, err)
		}
	}
	raw, value, err := sampleValue(f.Type, rest)
	if err != nil {
		return fmt.Errorf("%s: %w", f.Name, err)
	}
	key := labelKey(labels)
	if r.keys[key] {
		return fmt.Errorf("%s: the label set %s appears twice", f.Name, key)
	}
	r.keys[key] = true
	f.Samples = append(f.Samples, Sample{Labels: labels, Raw: raw, Value: value})
	return nil
}

// sampleValue reads " <value>", the end of a sample line of a family of
// type typ.
func sampleValue(typ, rest string) (string, float64, error) {
	raw, ok := strings.CutPrefix(rest, " ")
	if !ok || raw == "" || strings.ContainsAny(raw, " \t") {
		return "", 0, fmt.Errorf("%q is not one space and a value", rest)
	}
	value, err := strconv.ParseFloat(raw, 64)
	if err != nil || math.IsNaN(value) || math.IsInf(value, 0) {
		return "", 0, fmt.Errorf("%q is not a finite number", raw)
	}
	if typ == "counter" && (value < 0 || strings.HasPrefix(raw, "-")) {
		return "", 0, fmt.Errorf("a counter at %s", raw)
	}
	return raw, value, nil
}

// labelSet reads `name="value",...}` and returns the labels and what follows
// the closing brace.
func labelSet(s string) (map[string]string, string, error) {
	labels := map[string]string{}
	for {
		name, rest, ok := strings.Cut(s, "=\"")
		if !ok || !validLabelName(name) {
			return nil, "", fmt.Errorf("%q does not start with a label name and =\"", s)
		}
		if _, dup := labels[name]; dup {
			return nil, "", fmt.Errorf("label %s appears twice", name)
		}
		end := closingQuote(rest)
		if end < 0 {
			return nil, "", fmt.Errorf("the value of %s is not closed", name)
		}
		value, err := unescape(rest[:end], true)
		if err != nil {
			return nil, "", fmt.Errorf("the value of %s: %w", name, err)
		}
		labels[name] = value
		rest = rest[end+1:]
		switch {
		case strings.HasPrefix(rest, "}"):
			return labels, rest[1:], nil
		case strings.HasPrefix(rest, ",") && !strings.HasPrefix(rest, ",}"):
			s = rest[1:]
		default:
			return nil, "", fmt.Errorf("%q follows the value of %s", rest, name)
		}
	}
}

// closingQuote is the index of the first quote in s no backslash escapes.
func closingQuote(s string) int {
	for i := 0; i < len(s); i++ {
		switch s[i] {
		case '\\':
			i++
		case '"':
			return i
		}
	}
	return -1
}

// unescape undoes the format's escapes: a backslash and n in a HELP text,
// and a quote besides in a label value. Any other escape is refused; a raw
// newline or quote cannot reach here, since lines are split and a value ends
// at its first unescaped quote.
func unescape(s string, label bool) (string, error) {
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c != '\\' {
			b.WriteByte(c)
			continue
		}
		if i+1 == len(s) {
			return "", errors.New("a backslash at the end")
		}
		i++
		switch s[i] {
		case '\\':
			b.WriteByte('\\')
		case 'n':
			b.WriteByte('\n')
		case '"':
			if !label {
				return "", errors.New(`\" outside a label value`)
			}
			b.WriteByte('"')
		default:
			return "", fmt.Errorf("the escape \\%c", s[i])
		}
	}
	return b.String(), nil
}

func validName(s string) bool {
	return s != "" && strings.IndexFunc(s, func(r rune) bool { return !nameRune(r, true) }) < 0 && !digit(rune(s[0]))
}

func validLabelName(s string) bool {
	return s != "" && !strings.HasPrefix(s, "__") && !digit(rune(s[0])) &&
		strings.IndexFunc(s, func(r rune) bool { return !nameRune(r, false) }) < 0
}

func nameRune(r rune, colon bool) bool {
	return r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || digit(r) || r == '_' || colon && r == ':'
}

func digit(r rune) bool { return r >= '0' && r <= '9' }

func labelKey(labels map[string]string) string {
	names := make([]string, 0, len(labels))
	for name := range labels {
		names = append(names, name)
	}
	slices.Sort(names)
	var b strings.Builder
	for _, name := range names {
		fmt.Fprintf(&b, "%s=%q,", name, labels[name])
	}
	return "{" + b.String() + "}"
}
