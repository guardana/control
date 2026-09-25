package frontmatter

import (
	"errors"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"
)

// A scalar is spelled plain, so the block stays a YAML document another
// reader agrees with: nothing a YAML parser would read as a quote, a comment,
// an anchor, a collection, a number or a boolean, and nothing it could not
// print back the same way.

// yamlIndicators are the characters a plain YAML scalar may not start with.
const yamlIndicators = "-?:,[]{}#&*!|>'\"%@`"

// yamlWords are plain scalars a YAML reader turns into something other than
// a string, so the block refuses them and a title that is one gets a word.
var yamlWords = []string{"true", "false", "null", "~", "yes", "no", "on", "off", "y", "n"}

// checkScalar refuses a value the grammar cannot spell as a plain scalar.
func checkScalar(s string) error {
	switch {
	case s == "":
		return errors.New("a value is empty")
	case !utf8.ValidString(s):
		return errors.New("a value is not valid UTF-8")
	case strings.TrimSpace(s) != s:
		return errors.New("a value starts or ends with white space")
	case strings.ContainsRune(yamlIndicators, firstRune(s)):
		return errors.New("a value starts with a character YAML reads as syntax")
	case strings.Contains(s, ": "), strings.HasSuffix(s, ":"):
		return errors.New("a value contains a colon followed by a space or at its end")
	case strings.Contains(s, " #"):
		return errors.New("a value contains a comment indicator")
	case yamlWord(s):
		return errors.New("a value is a word YAML reads as a boolean or null")
	case isNumber(s):
		return errors.New("a value is a number, which YAML would not read as text")
	}
	for _, r := range s {
		if unicode.IsControl(r) || r == '\t' {
			return errors.New("a value holds a control character")
		}
	}
	return nil
}

// checkItem refuses a covers glob the flow list cannot spell: a scalar with
// no space and none of the list's own punctuation.
func checkItem(s string) error {
	if err := checkScalar(s); err != nil {
		return err
	}
	if strings.ContainsAny(s, " ,[]{}") {
		return errors.New("a glob holds a space or list punctuation")
	}
	return nil
}

func firstRune(s string) rune {
	r, _ := utf8.DecodeRuneInString(s)
	return r
}

func yamlWord(s string) bool {
	lower := strings.ToLower(s)
	for _, w := range yamlWords {
		if lower == w {
			return true
		}
	}
	return false
}

func isNumber(s string) bool {
	if _, err := strconv.ParseFloat(s, 64); err == nil {
		return true
	}
	if _, err := strconv.ParseInt(s, 0, 64); err == nil {
		return true
	}
	return false
}
