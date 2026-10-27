package testreport

import (
	"errors"
	"fmt"
	"slices"
	"strings"
	"unicode"
)

// ErrExpectedSkips is returned for a list of expected skips that is not one
// entry per line, or that names a test twice.
var ErrExpectedSkips = errors.New("testreport: a malformed list of expected skips")

// Skip names one test, subtest or fuzz seed of one package.
type Skip struct {
	Package, Test string
}

// ExpectedSkips maps each test the repository expects some run to skip to the
// reason it may. A nil or empty set expects none.
type ExpectedSkips map[Skip]string

// testPrefixes are the names go test runs; anything else in the test column
// is a swapped or mistyped field.
var testPrefixes = []string{"Test", "Fuzz", "Example", "Benchmark"}

// ParseExpectedSkips reads one entry per line, "<import path> <test name>
// <reason>" with a single space between the fields and the reason the rest of
// the line. A line that starts with "#" and a blank line are ignored. A line
// with an empty field, white space inside the import path or the test name, a
// test name go test does not give, or a test named a second time is refused
// with ErrExpectedSkips.
func ParseExpectedSkips(text []byte) (ExpectedSkips, error) {
	set := ExpectedSkips{}
	n := 0
	for line := range strings.Lines(string(text)) {
		n++
		line = strings.TrimSuffix(line, "\n")
		if strings.HasPrefix(line, "#") || strings.TrimSpace(line) == "" {
			continue
		}
		pkg, rest, _ := strings.Cut(line, " ")
		test, reason, _ := strings.Cut(rest, " ")
		reason = strings.TrimSpace(reason)
		switch {
		case pkg == "" || test == "" || reason == "":
			return nil, fmt.Errorf("%w: line %d is not <import path> <test name> <reason>", ErrExpectedSkips, n)
		case strings.ContainsFunc(pkg+test, unicode.IsSpace):
			return nil, fmt.Errorf("%w: line %d holds white space in its import path or test name", ErrExpectedSkips, n)
		case !hasTestPrefix(test):
			return nil, fmt.Errorf("%w: line %d names %q, which is no test go test runs", ErrExpectedSkips, n, test)
		}
		key := Skip{pkg, test}
		if _, ok := set[key]; ok {
			return nil, fmt.Errorf("%w: line %d names %s %s a second time", ErrExpectedSkips, n, pkg, test)
		}
		set[key] = reason
	}
	return set, nil
}

func hasTestPrefix(test string) bool {
	for _, p := range testPrefixes {
		if strings.HasPrefix(test, p) {
			return true
		}
	}
	return false
}

// skip records one skipped test and judges it against the expected set.
func (rep *reporter) skip(pkg, test, message string) {
	rep.sum.Skipped++
	rep.skips = append(rep.skips, skipped{pkg, test, message})
	key := Skip{pkg, test}
	rep.didSkip[key] = true
	if _, ok := rep.expected[key]; !ok {
		rep.sum.Unexpected++
		rep.surprises = append(rep.surprises, pkg+" "+test)
	}
}

// writeExpectedRan names, in order, each expected skip whose package reached
// a result in this stream without that test skipping. A package the stream
// never reached says nothing about its tests, so it is not named.
func (rep *reporter) writeExpectedRan() {
	var ran []string
	for key := range rep.expected {
		if rep.finished[key.Package] && !rep.didSkip[key] {
			ran = append(ran, key.Package+" "+key.Test)
		}
	}
	slices.Sort(ran)
	for _, test := range ran {
		rep.write(fmt.Sprintf("%sEXPECTED SKIP RAN %s\n", label, test))
	}
}
