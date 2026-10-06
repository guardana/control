package docscheck

import (
	"fmt"
	"io/fs"
	"regexp"
	"slices"
	"strings"
	"testing"
)

const gitleaksConfigPath = "scripts/gitleaks.toml"

// tomlValue is one value the reader admits: a boolean, a string or an array
// of strings.
type tomlValue struct {
	line    int
	key     string // lower case: gitleaks reads its keys case-insensitively
	isBool  bool
	boolean bool
	isArray bool
	strs    []string
}

// tomlTable is one table header and the keys under it; the header of the
// root table is empty.
type tomlTable struct {
	line   int
	header string // lower case
	array  bool
	values []tomlValue
}

var tomlHeader = regexp.MustCompile(`^[A-Za-z]+(\.[A-Za-z]+)?$`)

// parseGitleaksTOML reads the subset of TOML a gitleaks configuration needs
// here: table and array-of-table headers, bare or quoted keys, booleans,
// strings and arrays of strings. Anything else (a dotted key, an inline
// table, a number) is an error, never a key it skips.
func parseGitleaksTOML(text string) ([]tomlTable, error) {
	r := &tomlReader{s: strings.ReplaceAll(text, "\r\n", "\n"), line: 1}
	tables := []tomlTable{{line: 1}}
	for {
		r.skipBlank(true)
		if r.eof() {
			return tables, nil
		}
		line := r.line
		if r.peek() == '[' {
			array := strings.HasPrefix(r.rest(), "[[")
			open, closing := "[", "]"
			if array {
				open, closing = "[[", "]]"
			}
			end := strings.Index(r.rest(), closing)
			nl := strings.IndexByte(r.rest(), '\n')
			if end < 0 || (nl >= 0 && nl < end) {
				return nil, fmt.Errorf("line %d: an unclosed table header", line)
			}
			name := strings.TrimSpace(r.rest()[len(open):end])
			if !tomlHeader.MatchString(name) {
				return nil, fmt.Errorf("line %d: table header %q is not one this check reads", line, name)
			}
			r.pos += end + len(closing)
			tables = append(tables, tomlTable{line: line, header: strings.ToLower(name), array: array})
		} else {
			v, err := r.keyValue()
			if err != nil {
				return nil, err
			}
			t := &tables[len(tables)-1]
			t.values = append(t.values, v)
		}
		r.skipBlank(false)
		if !r.eof() && r.peek() != '\n' {
			return nil, fmt.Errorf("line %d: text after a value or header", r.line)
		}
	}
}

type tomlReader struct {
	s    string
	pos  int
	line int
}

func (r *tomlReader) eof() bool    { return r.pos >= len(r.s) }
func (r *tomlReader) peek() byte   { return r.s[r.pos] }
func (r *tomlReader) rest() string { return r.s[r.pos:] }

// skipBlank skips spaces, tabs and comments, and newlines when lines is set.
func (r *tomlReader) skipBlank(lines bool) {
	for !r.eof() {
		switch c := r.peek(); {
		case c == ' ' || c == '\t':
			r.pos++
		case c == '#':
			for !r.eof() && r.peek() != '\n' {
				r.pos++
			}
		case c == '\n' && lines:
			r.pos++
			r.line++
		default:
			return
		}
	}
}

var tomlBareKey = regexp.MustCompile(`^[A-Za-z0-9_-]+`)

func (r *tomlReader) keyValue() (tomlValue, error) {
	v := tomlValue{line: r.line}
	if c := r.peek(); c == '"' || c == '\'' {
		key, err := r.str()
		if err != nil {
			return v, err
		}
		v.key = key
	} else {
		key := tomlBareKey.FindString(r.rest())
		if key == "" {
			return v, fmt.Errorf("line %d: %q is not a key", v.line, firstLine(r.rest()))
		}
		r.pos += len(key)
		v.key = key
	}
	v.key = strings.ToLower(v.key)
	r.skipBlank(false)
	if r.eof() || r.peek() != '=' {
		return v, fmt.Errorf("line %d: key %q is not followed by =, as a dotted key would be", v.line, v.key)
	}
	r.pos++
	r.skipBlank(false)
	return v, r.value(&v)
}

func (r *tomlReader) value(v *tomlValue) error {
	switch {
	case r.eof():
		return fmt.Errorf("line %d: key %q has no value", v.line, v.key)
	case strings.HasPrefix(r.rest(), "true"):
		v.isBool, v.boolean = true, true
		r.pos += len("true")
	case strings.HasPrefix(r.rest(), "false"):
		v.isBool = true
		r.pos += len("false")
	case r.peek() == '[':
		r.pos++
		v.isArray = true
		return r.array(v)
	default:
		s, err := r.str()
		if err != nil {
			return err
		}
		v.strs = []string{s}
	}
	return nil
}

// array reads the elements of an array of strings after its opening bracket.
func (r *tomlReader) array(v *tomlValue) error {
	for {
		r.skipBlank(true)
		if r.eof() {
			return fmt.Errorf("line %d: an unclosed array", v.line)
		}
		if r.peek() == ']' {
			r.pos++
			return nil
		}
		s, err := r.str()
		if err != nil {
			return err
		}
		v.strs = append(v.strs, s)
		r.skipBlank(true)
		if !r.eof() && r.peek() == ',' {
			r.pos++
		} else if r.eof() || r.peek() != ']' {
			return fmt.Errorf("line %d: an array element is not followed by , or ]", r.line)
		}
	}
}

// str reads one string in any of TOML's four spellings.
func (r *tomlReader) str() (string, error) {
	start := r.line
	for _, delim := range []string{"'''", `"""`, "'", `"`} {
		if !strings.HasPrefix(r.rest(), delim) {
			continue
		}
		r.pos += len(delim)
		var b strings.Builder
		for !r.eof() {
			if strings.HasPrefix(r.rest(), delim) {
				r.pos += len(delim)
				return b.String(), nil
			}
			c := r.peek()
			switch {
			case c == '\n' && len(delim) == 1:
				return "", fmt.Errorf("line %d: a string runs past its line", start)
			case c == '\n':
				r.line++
			case c == '\\' && delim[0] == '"':
				if r.pos+1 >= len(r.s) {
					return "", fmt.Errorf("line %d: a string ends in an escape", start)
				}
				esc, ok := map[byte]byte{'\\': '\\', '"': '"', 'n': '\n', 't': '\t'}[r.s[r.pos+1]]
				if !ok {
					return "", fmt.Errorf("line %d: an escape this check does not read", start)
				}
				b.WriteByte(esc)
				r.pos += 2
				continue
			}
			b.WriteByte(c)
			r.pos++
		}
		return "", fmt.Errorf("line %d: an unclosed string", start)
	}
	return "", fmt.Errorf("line %d: %q is not a boolean, a string or an array of strings", r.line, firstLine(r.rest()))
}

func firstLine(s string) string {
	line, _, _ := strings.Cut(s, "\n")
	return line
}

// The keys an allowlist may carry: it narrows by rule and by what a finding
// holds, never by where the finding is. paths and commits are the keys it
// may not, because either one silences every rule over a whole file or
// change.
var gitleaksAllowlistKeys = []string{"description", "targetrules", "regextarget", "regexes", "stopwords", "condition"}

// gitleaksConfigProblems holds the scan's configuration to the built-in rules
// and allowlist plus exceptions that name a rule or a pattern: an [extend]
// that loads the defaults and nothing else, [[rules]] entries that only name
// a default rule to attach an allowlist to, and allowlists without paths or
// commits whose every regex and stopword excepts none of the planted secrets.
func gitleaksConfigProblems(text string) []string {
	tables, err := parseGitleaksTOML(text)
	if err != nil {
		return []string{err.Error()}
	}
	var problems []string
	extends, rules := 0, 0
	for _, t := range tables {
		switch {
		case t.is("extend", false):
			extends++
		case t.is("rules", true):
			rules++
		}
		problems = append(problems, gitleaksTableProblems(t, rules)...)
	}
	if extends != 1 {
		problems = append(problems, fmt.Sprintf("found %d [extend] table(s), want 1 that loads the default rules", extends))
	}
	return problems
}

func (t tomlTable) is(header string, array bool) bool {
	return t.header == header && t.array == array
}

func (t tomlTable) value(key string) (tomlValue, bool) {
	i := slices.IndexFunc(t.values, func(v tomlValue) bool { return v.key == key })
	if i < 0 {
		return tomlValue{}, false
	}
	return t.values[i], true
}

// gitleaksTableProblems judges one table; rules counts the [[rules]] tables
// read so far, which a [[rules.allowlists]] attaches to.
func gitleaksTableProblems(t tomlTable, rules int) []string {
	where := fmt.Sprintf("line %d: [%s]", t.line, t.header)
	switch {
	case t.is("", false):
		return tomlKeyProblems(t, []string{"title"}, "the root table")
	case t.is("extend", false):
		problems := tomlKeyProblems(t, []string{"usedefault"}, where)
		if v, ok := t.value("usedefault"); !ok || !v.boolean {
			problems = append(problems, where+" does not set useDefault = true")
		}
		return problems
	case t.is("rules", true):
		problems := tomlKeyProblems(t, []string{"id", "description"}, where)
		if v, _ := t.value("id"); len(v.strs) != 1 || v.strs[0] == "" {
			problems = append(problems, where+" names no rule id")
		}
		return problems
	case t.is("rules.allowlists", true) && rules == 0:
		return []string{where + " follows no [[rules]]"}
	case t.is("allowlists", true), t.is("rules.allowlists", true):
		return gitleaksAllowlistProblems(t, where)
	}
	return []string{where + " is not a table this configuration may hold"}
}

func gitleaksAllowlistProblems(t tomlTable, where string) []string {
	problems := tomlKeyProblems(t, gitleaksAllowlistKeys, where)
	narrows := false
	for _, v := range t.values {
		switch v.key {
		case "paths", "commits":
			problems = append(problems, fmt.Sprintf("line %d: an allowlist entry by %s, which excepts every rule there", v.line, v.key))
		case "regexes", "stopwords":
			for _, entry := range v.strs {
				if problem := allowlistEntryProblem(v.key, entry); problem != "" {
					problems = append(problems, fmt.Sprintf("line %d: %s", v.line, problem))
				} else {
					narrows = true
				}
			}
		}
	}
	if !narrows {
		problems = append(problems, where+" holds no regex or stopword to narrow by")
	}
	return problems
}

// allowlistEntryProblem judges one regex or stopword: it must be one gitleaks
// reads and except none of the planted secrets.
func allowlistEntryProblem(key, entry string) string {
	if key == "stopwords" {
		if entry == "" {
			return "an empty stopword"
		}
		return plantedStopwordProblem(entry)
	}
	re, err := regexp.Compile(entry)
	switch {
	case err != nil:
		return fmt.Sprintf("regex %q does not compile: %v", entry, err)
	case re.MatchString(""):
		return fmt.Sprintf("regex %q matches the empty string, so it excepts every finding", entry)
	}
	return plantedRegexProblem(re)
}

// tomlKeyProblems refuses a key outside allowed, a key written twice, and a
// key that is not the shape its name needs.
func tomlKeyProblems(t tomlTable, allowed []string, where string) []string {
	var problems []string
	seen := map[string]bool{}
	for _, v := range t.values {
		switch {
		case !slices.Contains(allowed, v.key):
			problems = append(problems, fmt.Sprintf("line %d: %s carries %s", v.line, where, v.key))
		case seen[v.key]:
			problems = append(problems, fmt.Sprintf("line %d: %s carries %s twice", v.line, where, v.key))
		case v.isBool != (v.key == "usedefault"):
			problems = append(problems, fmt.Sprintf("line %d: %s is the wrong type", v.line, v.key))
		case v.isArray != slices.Contains([]string{"targetrules", "regexes", "stopwords"}, v.key):
			problems = append(problems, fmt.Sprintf("line %d: %s is the wrong type", v.line, v.key))
		}
		seen[v.key] = true
	}
	return problems
}

func TestGitleaksConfigLoadsOnlyTheDefaultsAndNarrowExceptions(t *testing.T) {
	data, err := fs.ReadFile(repoFS(t), gitleaksConfigPath)
	if err != nil {
		t.Fatalf("reading %s: %v", gitleaksConfigPath, err)
	}
	for _, problem := range gitleaksConfigProblems(string(data)) {
		t.Errorf("%s: %s", gitleaksConfigPath, problem)
	}
	for _, entry := range unreviewedExceptions(string(data), reviewedGitleaksExceptions) {
		t.Errorf("%s: the exception %q is not among the reviewed ones this test names", gitleaksConfigPath, entry)
	}
}

// reviewedGitleaksExceptions is every regex and stopword the scan's
// configuration may hold. Whether a narrow pattern hides a real secret of a
// rule's shape is a judgement no check here makes, so an exception added to
// the configuration fails until this list, which the security maintainers
// own, names it too.
var reviewedGitleaksExceptions = []string{}

// unreviewedExceptions is each regex and stopword of the configuration text
// that reviewed does not name; a text that does not parse is its own entry.
func unreviewedExceptions(text string, reviewed []string) []string {
	tables, err := parseGitleaksTOML(text)
	if err != nil {
		return []string{err.Error()}
	}
	var out []string
	for _, t := range tables {
		for _, v := range t.values {
			if v.key != "regexes" && v.key != "stopwords" {
				continue
			}
			for _, entry := range v.strs {
				if !slices.Contains(reviewed, entry) {
					out = append(out, entry)
				}
			}
		}
	}
	return out
}

// TestANarrowExceptionNeedsAReview: an exception too narrow for the planted
// secrets to show, such as one prefix of a token's shape, is still named as
// unreviewed, and one the list names is not.
func TestANarrowExceptionNeedsAReview(t *testing.T) {
	text := "[extend]\nuseDefault = true\n\n[[allowlists]]\ntargetRules = ['''github-pat''']\n" +
		"regexTarget = '''secret'''\nregexes = ['''^ghp_Z''']\nstopwords = ['''zzqx''']\n"
	if got := unreviewedExceptions(text, nil); !slices.Equal(got, []string{"^ghp_Z", "zzqx"}) {
		t.Errorf("unreviewed exceptions %q, want both", got)
	}
	if got := unreviewedExceptions(text, []string{"^ghp_Z", "zzqx"}); len(got) != 0 {
		t.Errorf("reviewed exceptions named as unreviewed: %q", got)
	}
}

const gitleaksFixture = `# A comment.
[extend]
useDefault = true # the built-in rules

[[allowlists]]
description = "a fixture that is not a secret"
targetRules = ["generic-api-key"]
regexes = [
  '''^fixture-[0-9]+$''', # a comment inside the array
  "^other\\.value$",
]

[[rules]]
id = "github-pat"
[[rules.allowlists]]
stopwords = ['''example''']
`

func TestGitleaksConfigProblems(t *testing.T) {
	if problems := gitleaksConfigProblems(gitleaksFixture); len(problems) != 0 {
		t.Fatalf("the correct fixture reported %q", problems)
	}
	allowlist := "[[allowlists]]\ndescription"
	cases := map[string]struct{ old, replacement, want string }{
		"a paths entry":                {allowlist, "[[allowlists]]\npaths = ['''internal/core/''']\ndescription", "an allowlist entry by paths"},
		"a paths entry in upper case":  {allowlist, "[[allowlists]]\nPaths = ['''internal/core/''']\ndescription", "an allowlist entry by paths"},
		"a quoted paths key":           {allowlist, "[[allowlists]]\n\"paths\" = ['''internal/core/''']\ndescription", "an allowlist entry by paths"},
		"a commits entry":              {allowlist, "[[allowlists]]\ncommits = ['''abc123''']\ndescription", "an allowlist entry by commits"},
		"a paths entry on a rule":      {"stopwords = ['''example''']", "paths = ['''internal/''']", "an allowlist entry by paths"},
		"the defaults off":             {"useDefault = true", "useDefault = false", "does not set useDefault = true"},
		"the defaults as a string":     {"useDefault = true", `useDefault = "true"`, "usedefault is the wrong type"},
		"no extend":                    {"[extend]\nuseDefault = true", "", "found 0 [extend] table(s)"},
		"a disabled rule":              {"useDefault = true", "useDefault = true\ndisabledRules = [\"github-pat\"]", "[extend] carries disabledrules"},
		"another base configuration":   {"useDefault = true", "useDefault = true\npath = \"other.toml\"", "[extend] carries path"},
		"a rule that redefines":        {"id = \"github-pat\"", "id = \"github-pat\"\nregex = '''x^'''", "[rules] carries regex"},
		"a rule without an id":         {"id = \"github-pat\"", "description = \"x\"", "names no rule id"},
		"a dotted key":                 {"useDefault = true", "useDefault = true\n\n[[allowlists]]\nregexes.x = ['''a''']", "is not followed by ="},
		"an inline table":              {allowlist, "allowlists = [{paths = ['''x''']}]\n" + allowlist, "is not a boolean, a string or an array of strings"},
		"a legacy global allowlist":    {allowlist, "[allowlist]\nregexes = ['''x''']\n" + allowlist, "[allowlist] is not a table"},
		"a legacy rule allowlist":      {"[[rules.allowlists]]", "[rules.allowlist]", "[rules.allowlist] is not a table"},
		"a quoted header":              {"[extend]", "[\"extend\"]", "is not one this check reads"},
		"a regex matching everything":  {"'''^fixture-[0-9]+$'''", "'''.*'''", "matches the empty string"},
		"a regex that does not parse":  {"'''^fixture-[0-9]+$'''", "'''(x'''", "does not compile"},
		"an empty stopword":            {"stopwords = ['''example''']", "stopwords = ['']", "an empty stopword"},
		"an allowlist narrowing by no": {"stopwords = ['''example''']", "description = \"x\"", "holds no regex or stopword"},
		"a repeated key":               {"useDefault = true", "useDefault = true\nUSEDEFAULT = true", "carries usedefault twice"},
		"a rule allowlist alone":       {"[[rules]]\nid = \"github-pat\"\n", "", "follows no [[rules]]"},
		"a number":                     {"useDefault = true", "useDefault = 1", "is not a boolean, a string or an array of strings"},
		"an unclosed array":            {"  \"^other\\\\.value$\",\n]", "  \"^other\\\\.value$\",\n", "is not a boolean, a string or an array of strings"},
		"a regex of one character":     {"'''^fixture-[0-9]+$'''", "'''.'''", "excepts the planted"},
		"every line, as a line target": {"targetRules = [\"generic-api-key\"]\nregexes = [\n  '''^fixture-[0-9]+$''',", "regexTarget = \"line\"\nregexes = [\n  '''(?s).+''',", "excepts the planted"},
		"a rule's own prefix":          {"'''^fixture-[0-9]+$'''", "'''^ghp_'''", "excepts the planted github-pat"},
		"the key name, as a match":     {"'''^fixture-[0-9]+$'''", "'''api_key'''", "excepts the planted generic-api-key"},
		"a line's own text":            {"'''^fixture-[0-9]+$'''", "'''export VALUE='''", "excepts the planted"},
		"a one-letter stopword":        {"stopwords = ['''example''']", "stopwords = ['e']", "excepts the planted"},
		"an upper-case stopword":       {"stopwords = ['''example''']", "stopwords = ['E']", "excepts the planted"},
		"a stopword of a rule prefix":  {"stopwords = ['''example''']", "stopwords = ['akia']", "excepts the planted aws-access-token"},
	}
	for name, c := range cases {
		text := replaceOnce(t, gitleaksFixture, c.old, c.replacement)
		problems := gitleaksConfigProblems(text)
		if !slices.ContainsFunc(problems, func(s string) bool { return strings.Contains(s, c.want) }) {
			t.Errorf("%s: want a problem containing %q, got %q", name, c.want, problems)
		}
	}
}

func TestParseGitleaksTOMLReadsEverySpelling(t *testing.T) {
	tables, err := parseGitleaksTOML(gitleaksFixture)
	if err != nil {
		t.Fatal(err)
	}
	if len(tables) != 5 {
		t.Fatalf("read %d tables, want the root and 4", len(tables))
	}
	regexes := tables[2].values[2]
	if want := []string{`^fixture-[0-9]+$`, `^other\.value$`}; regexes.key != "regexes" || !slices.Equal(regexes.strs, want) {
		t.Errorf("regexes = %q %q, want %q", regexes.key, regexes.strs, want)
	}
	_, err = parseGitleaksTOML("a = '''x\ny'''\nb = \"z")
	if err == nil || !strings.Contains(err.Error(), "line 3: an unclosed string") {
		t.Errorf("an unclosed string on line 3 reported %v", err)
	}
}
