package contract_test

import (
	"encoding/json"
	"errors"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"unicode"

	controlv1 "github.com/guardana/control/api/gen/go/guardana/control/v1"
	"github.com/guardana/control/pkg/contract"
)

// Relative to this package's directory, which is where go test runs.
const (
	contractsDoc   = "../../docs/contracts.md"
	packageDir     = "."
	minimalFixture = "../../testdata/contracts/action_envelope/minimal.json"

	limitsSection   = "## Limits"
	versionSection  = "## Schema versioning"
	refusalsSection = "## Refusals"

	// A description cell shorter than this cannot say what the limit protects.
	// It is a length check and nothing more: it catches a table gutted to
	// one-word cells, and forty characters of filler would pass it.
	minDescription = 30
)

type limit struct {
	name  string
	value int
}

// limits is every bound this package promises, each paired with the value the
// compiler sees. A literal repeated here would only prove that the document
// agrees with this file.
func limits() []limit {
	return []limit{
		{"MaxEnvelopeBytes", contract.MaxEnvelopeBytes},
		{"MaxArgumentsBytes", contract.MaxArgumentsBytes},
		{"MaxDelegationDepth", contract.MaxDelegationDepth},
		{"MaxLabels", contract.MaxLabels},
		{"MaxStringBytes", contract.MaxStringBytes},
		{"MaxPreviewBytes", contract.MaxPreviewBytes},
		{"MaxNesting", contract.MaxNesting},
	}
}

// sentinels is every refusal the package can be matched against. Kept in step
// with the package by TestSentinelSetIsClosed.
func sentinels() []error {
	return []error{
		contract.ErrUnsupportedSchema,
		contract.ErrUnknownField,
		contract.ErrTooLarge,
		contract.ErrMissingField,
		contract.ErrInvalidEnum,
		contract.ErrInvalidValue,
	}
}

// TestLimitsAreDocumented pins docs/contracts.md to the constants, in three
// directions: a constant whose value moved without the document moving fails,
// a constant the package declares but limits() above does not list fails, and a
// row in the document naming no constant fails.
//
// How it cannot pass over nothing: all three inputs are read rather than
// assumed, and each is fatal when it is empty. A missing or unreadable
// document, a document with no "## Limits" section and a section whose table
// this parser cannot find a single row in are all fatal in documentedLimits; a
// package declaring no exported constant is fatal in declaredNames. So an empty
// file, a deleted table or a table reformatted past recognition all fail here.
// None of them can reach the comparison loops and pass over zero constants.
func TestLimitsAreDocumented(t *testing.T) {
	documented := documentedLimits(t)
	declared := declaredNames(t, token.CONST)

	promised := make(map[string]bool, len(limits()))
	for _, l := range limits() {
		promised[l.name] = true
		value, ok := documented[l.name]
		if !ok {
			t.Errorf("%s: no row for %s under %q", contractsDoc, l.name, limitsSection)
			continue
		}
		if want := strconv.Itoa(l.value); value != want {
			t.Errorf("%s: %s is documented as %s, the constant is %s",
				contractsDoc, l.name, value, want)
		}
	}
	for _, name := range declared {
		if !promised[name] {
			t.Errorf("%s declares %s: list it in limits() and document it under %q, or unexport it. An exported constant this test never reads is a promise nothing pins",
				packageDir, name, limitsSection)
		}
	}
	declaredSet := make(map[string]bool, len(declared))
	for _, name := range declared {
		declaredSet[name] = true
	}
	for name := range documented {
		if !declaredSet[name] {
			t.Errorf("%s documents %s, which this package does not declare", contractsDoc, name)
		}
	}
}

// TestDocumentedSchemaVersionMatchesTheContracts ties the version the document
// states to the contracts, from two sides.
//
// The major comes from the Protobuf package the generated types report, which
// is the only machine-readable statement of it: a move to v2 makes a page that
// still says 1.x fail. The full version comes from the frozen fixture, which is
// the only machine-readable statement of the minor; the protos state it in a
// comment, which nothing can compare against. That half is one-directional, so
// its failure message names both files rather than blaming the page.
func TestDocumentedSchemaVersionMatchesTheContracts(t *testing.T) {
	doc := readSection(t, versionSection)

	pkg := string((&controlv1.ActionEnvelope{}).ProtoReflect().Descriptor().ParentFile().Package())
	major, ok := strings.CutPrefix(pkg[strings.LastIndex(pkg, ".")+1:], "v")
	if !ok || major == "" {
		t.Fatalf("proto package %q does not end in a version segment, so no major can be read from it", pkg)
	}
	if !strings.Contains(doc, "`"+major+".") {
		t.Errorf("%s: %q states no version of major %s, which is the major the generated types carry",
			contractsDoc, versionSection, major)
	}

	version := fixtureSchemaVersion(t)
	if !strings.HasPrefix(version, major+".") {
		t.Fatalf("%s carries schema version %q, which is not major %s: the fixture and the generated types disagree",
			minimalFixture, version, major)
	}
	if !strings.Contains(doc, "`"+version+"`") {
		t.Errorf("%s: %q never states `%s`. Either the page is stale, or %s is and the contracts moved on without it",
			contractsDoc, versionSection, version, minimalFixture)
	}
}

// TestValidationErrorMatchesItsSentinel is the promise the package exists for:
// a caller finds out why a message was refused, and which field did it, without
// parsing a message string.
func TestValidationErrorMatchesItsSentinel(t *testing.T) {
	for _, tc := range []struct {
		field    string
		sentinel error
	}{
		{"schema_version", contract.ErrUnsupportedSchema},
		{"action.<field 7>", contract.ErrUnknownField},
		{"arguments.redacted_preview", contract.ErrTooLarge},
		{"principal.id", contract.ErrMissingField},
		{"action.effect", contract.ErrInvalidEnum},
		{"arguments.canonical_hash", contract.ErrInvalidValue},
	} {
		t.Run(tc.field, func(t *testing.T) {
			var err error = &contract.ValidationError{Field: tc.field, Err: tc.sentinel}

			if !errors.Is(err, tc.sentinel) {
				t.Errorf("errors.Is(%v, %v) = false, want true", err, tc.sentinel)
			}
			var ve *contract.ValidationError
			if !errors.As(err, &ve) {
				t.Fatalf("errors.As(%v, **ValidationError) = false: no caller can reach the field", err)
			}
			if ve.Field != tc.field {
				t.Errorf("field is %q, want %q", ve.Field, tc.field)
			}

			// Wrapping is what a caller does with an error it passes on, and
			// it may not cost the classification.
			wrapped := fmt.Errorf("decide: %w", err)
			if !errors.Is(wrapped, tc.sentinel) || !errors.As(wrapped, &ve) {
				t.Errorf("%v: errors.Is or errors.As no longer reaches the cause", wrapped)
			}

			// Matching one sentinel proves nothing unless the others do not.
			for _, other := range sentinels() {
				if errors.Is(other, tc.sentinel) {
					continue // the case's own sentinel, matched above
				}
				if errors.Is(err, other) {
					t.Errorf("errors.Is(%v, %v) = true, want false", err, other)
				}
			}
		})
	}
}

// TestValidationErrorCarriesACauseWithNoSentinel records the case the sentinels
// do not cover: a payload that does not decode at all is not a missing field,
// an unknown field, an undeclared enum, a refused value, an oversized message
// or an unsupported version. The type carries it; the alternative is a catch-all
// sentinel meaning "one of the reasons we did not enumerate".
func TestValidationErrorCarriesACauseWithNoSentinel(t *testing.T) {
	cause := errors.New("proto: cannot parse invalid wire-format data")
	var err error = &contract.ValidationError{Err: cause}

	var ve *contract.ValidationError
	if !errors.As(err, &ve) {
		t.Fatalf("errors.As(%v, **ValidationError) = false", err)
	}
	if !errors.Is(err, cause) {
		t.Errorf("errors.Is(%v, cause) = false: the underlying error is unreachable", err)
	}
	for _, s := range sentinels() {
		if errors.Is(err, s) {
			t.Errorf("a decode failure matches %v, so a caller would classify it as one", s)
		}
	}
}

// TestValidationErrorMessage pins the one-line form: the field quoted, the
// cause verbatim, and a value with no cause printing rather than panicking.
func TestValidationErrorMessage(t *testing.T) {
	for _, tc := range []struct {
		name string
		err  *contract.ValidationError
		want string
	}{
		{
			name: "field and cause",
			err:  &contract.ValidationError{Field: "action.name", Err: contract.ErrTooLarge},
			want: `"action.name": "contract: size limit exceeded"`,
		},
		{
			name: "no field",
			err:  &contract.ValidationError{Err: contract.ErrUnsupportedSchema},
			want: `"contract: unsupported schema version"`,
		},
		{
			name: "no cause",
			err:  &contract.ValidationError{Field: "action.name"},
			want: `"action.name": "contract: invalid"`,
		},
		{
			name: "neither",
			err:  &contract.ValidationError{},
			want: `"contract: invalid"`,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.err.Error(); got != tc.want {
				t.Errorf("Error() = %q, want %q", got, tc.want)
			}
			// errors.Is rather than !=, and it holds in the no-cause case too:
			// errors.Is(nil, nil) is true.
			if got := tc.err.Unwrap(); !errors.Is(got, tc.err.Err) {
				t.Errorf("Unwrap() = %v, want %v", got, tc.err.Err)
			}
		})
	}
}

// TestValidationErrorRendersOneSafeLine states the property, not the mechanism:
// whatever reaches either half of the message, the rendering holds no character
// a reader could take for the end of the record. Asserting the output equals
// strconv.Quote(input) would only prove that Error calls the function this test
// called.
//
// Both halves are exercised. The field is schema-derived by rule, and a rule is
// not a type; the cause is whatever error was wrapped, including a codec's
// message about input someone else chose.
func TestValidationErrorRendersOneSafeLine(t *testing.T) {
	for _, tc := range adversarialStrings() {
		t.Run(tc.name, func(t *testing.T) {
			if printable(tc.value) {
				t.Fatalf("the vector %+q holds nothing a renderer has to escape, so this case cannot fail", tc.value)
			}
			for _, got := range []string{
				(&contract.ValidationError{Field: tc.value, Err: contract.ErrInvalidValue}).Error(),
				(&contract.ValidationError{Field: "principal.attributes[3]", Err: errors.New(tc.value)}).Error(),
				(&contract.ValidationError{Err: errors.New(tc.value)}).Error(),
			} {
				for _, r := range got {
					if !unicode.IsPrint(r) {
						t.Errorf("Error() = %+q holds %U, which can end a line, hide one or reverse one", got, r)
					}
				}
			}
		})
	}
}

// TestValidationErrorDoesNotForgeALogRecord is the semantic half: a cause that
// carries a whole refusal record must not produce that record, and a cause that
// merely reads like a sentinel must not render like one. This project's product
// is evidence, so a line about a refusal that never happened is the failure.
func TestValidationErrorDoesNotForgeALogRecord(t *testing.T) {
	genuine := (&contract.ValidationError{Field: "action.name", Err: contract.ErrTooLarge}).Error()

	forged := (&contract.ValidationError{
		Field: "principal.attributes[3]",
		Err:   errors.New("proto: cannot parse\n" + genuine),
	}).Error()
	for i, line := range strings.Split(forged, "\n") {
		if line == genuine {
			t.Errorf("rendering a cause that quotes a record produced that record again on line %d: %s", i+1, line)
		}
	}

	lookalike := &contract.ValidationError{Field: "action.name", Err: errors.New("size limit exceeded")}
	classified := &contract.ValidationError{Field: "action.name", Err: contract.ErrTooLarge}
	if lookalike.Error() == classified.Error() {
		t.Errorf("an unclassified cause renders as %q, exactly like the sentinel case, while errors.Is separates them",
			lookalike)
	}
}

// adversarialStrings is the battery both halves of the message are rendered
// with. Each either ends a line, hides the rest of one, or reverses how it
// reads.
func adversarialStrings() []struct{ name, value string } {
	// Escapes rather than literals: a source file holding these bytes is a
	// Trojan Source finding of its own, and the reader cannot see what it says.
	return []struct{ name, value string }{
		{"line feed", "a\nb"},
		{"carriage return", "a\rb"},
		{"tab", "a\tb"},
		{"escape", "a\x1bb"},
		{"nul", "a\x00b"},
		{"next line U+0085", "a\u0085b"},
		{"line separator U+2028", "a\u2028b"},
		{"paragraph separator U+2029", "a\u2029b"},
		{"right-to-left override U+202E", "a\u202eb"},
		{"zero width space U+200B", "a\u200bb"},
		{"a whole forged record", "principal.attributes[3]\n\"action.name\": \"contract: size limit exceeded\""},
	}
}

func printable(s string) bool {
	for _, r := range s {
		if !unicode.IsPrint(r) {
			return false
		}
	}
	return true
}

// TestValidationErrorIsNilSafe holds the package to the comment on Error: a
// typed nil in an error interface is a caller bug, and it reads as a refusal
// downstream, which is the safe direction. Crashing the process that is
// enforcing is not.
func TestValidationErrorIsNilSafe(t *testing.T) {
	var ve *contract.ValidationError

	// The premise is that a typed nil in an error interface is not nil, so
	// everything downstream treats it as a refusal and calls these methods on
	// it. Asserting that here is not possible: staticcheck rejects the
	// comparison as one that can never be true, which is the proof.
	var err error = ve

	if got, want := ve.Error(), `"contract: invalid: nil *ValidationError"`; got != want {
		t.Errorf("Error() = %q, want %q", got, want)
	}
	if got := ve.Unwrap(); got != nil {
		t.Errorf("Unwrap() = %v, want nil", got)
	}
	// errors.Is unwraps, so this is the path that would panic.
	if errors.Is(err, contract.ErrTooLarge) {
		t.Error("a nil *ValidationError matches a sentinel it does not carry")
	}
}

// TestSentinelSetIsClosed fails when the package grows an exported package-level
// variable this file does not know about, in any non-test file of the package.
// Task 3 writes other files here, and a sentinel declared in one of them would
// otherwise be unpinned with every gate still green.
//
// What it does not reach: a sentinel returned from an exported function rather
// than declared as a variable. Nothing in the package does that today, and a
// closure test over function bodies is a different tool than this one.
func TestSentinelSetIsClosed(t *testing.T) {
	declared := declaredNames(t, token.VAR)
	if len(declared) != len(sentinels()) {
		t.Fatalf("%s declares %d exported variables (%v), this file knows %d",
			packageDir, len(declared), declared, len(sentinels()))
	}

	seen := make(map[string]bool, len(sentinels()))
	for i, a := range sentinels() {
		if !strings.HasPrefix(a.Error(), "contract: ") {
			t.Errorf("%q does not start with the package prefix every sentinel carries", a)
		}
		if seen[a.Error()] {
			t.Errorf("two sentinels share the text %q, so a log line cannot tell them apart", a)
		}
		seen[a.Error()] = true

		// Distinct identities, not several spellings of one error: an alias
		// would make errors.Is answer true for a refusal that did not happen.
		for j, b := range sentinels() {
			if i != j && errors.Is(a, b) {
				t.Errorf("errors.Is(%v, %v) = true, want false", a, b)
			}
		}
	}
}

// documentedLimits parses the limits table out of the "## Limits" section: a
// row whose first cell is a backquoted name, whose second is a decimal and
// whose third says something.
//
// Searching the whole page for a bare number would be weaker than it looks. Two
// of the limits are 32, so a substring search accepts a document that gives a
// value to the wrong constant, and it accepts a document that lost the table
// entirely as long as the number survives somewhere in the prose. Reading only
// the section also keeps another table on this page, the digest table a later
// task adds, from being read as a limits table.
func documentedLimits(t *testing.T) map[string]string {
	t.Helper()

	rows := make(map[string]string)
	for _, line := range strings.Split(readSection(t, limitsSection), "\n") {
		trimmed := strings.TrimSpace(line)
		if !strings.HasPrefix(trimmed, "|") {
			continue
		}
		cells := strings.Split(strings.Trim(trimmed, "|"), "|")
		if len(cells) < 3 {
			continue
		}
		name, value := strings.TrimSpace(cells[0]), strings.TrimSpace(cells[1])
		if !strings.HasPrefix(name, "`") || !strings.HasSuffix(name, "`") {
			continue
		}
		if _, err := strconv.Atoi(value); err != nil {
			continue
		}
		name = strings.Trim(name, "`")
		description := strings.TrimSpace(cells[2])
		if len(description) < minDescription {
			t.Errorf("%s: %s is described in %d characters (%q); a row that explains nothing documents nothing",
				contractsDoc, name, len(description), description)
		}
		checkKiB(t, name, value, description)
		if previous, duplicate := rows[name]; duplicate {
			t.Errorf("%s: %s has two rows, %s and %s; one of them is unread",
				contractsDoc, name, previous, value)
		}
		rows[name] = value
	}
	if len(rows) == 0 {
		t.Fatalf("%s: %q holds no row of a backquoted name and a decimal; an unparsed table is not a documented limit",
			contractsDoc, limitsSection)
	}
	return rows
}

// checkKiB pins the prose restatement of a value to the value. The decimal in
// the table is checked against the constant, so a row can still say "256 KiB"
// beside a number that is no longer 256 KiB, and prose is what a reader
// believes. Only this restatement is pinned; the rest of a description is not.
func checkKiB(t *testing.T, name, value, description string) {
	t.Helper()

	bytes, err := strconv.Atoi(value)
	if err != nil {
		t.Fatalf("%s: %s has the non-decimal value %q, which this test should never have accepted",
			contractsDoc, name, value)
	}
	for _, m := range regexp.MustCompile(`(\d+) KiB`).FindAllStringSubmatch(description, -1) {
		kib, err := strconv.Atoi(m[1])
		if err != nil {
			t.Errorf("%s: %s says %q, which is not a number of KiB", contractsDoc, name, m[0])
			continue
		}
		if kib*1024 != bytes {
			t.Errorf("%s: %s is %d bytes, which is not %s", contractsDoc, name, bytes, m[0])
		}
	}
}

// TestPageNamesEverySentinel pins the sentinel list on the page to the package.
// The reviewer's mutation was cutting that list from six entries to two with
// every gate green: a reader would then believe the package classifies four
// fewer refusals than it does, and a caller would write a switch with four
// missing branches.
func TestPageNamesEverySentinel(t *testing.T) {
	section := readSection(t, refusalsSection)

	named := make(map[string]bool)
	for _, m := range regexp.MustCompile("`(Err[A-Za-z0-9]*)`").FindAllStringSubmatch(section, -1) {
		named[m[1]] = true
	}
	if len(named) == 0 {
		t.Fatalf("%s: %q names no sentinel at all", contractsDoc, refusalsSection)
	}

	declared := make(map[string]bool)
	for _, name := range declaredNames(t, token.VAR) {
		declared[name] = true
		if !named[name] {
			t.Errorf("%s: %q does not name %s, which the package declares", contractsDoc, refusalsSection, name)
		}
	}
	for name := range named {
		if !declared[name] {
			t.Errorf("%s: %q names %s, which the package does not declare", contractsDoc, refusalsSection, name)
		}
	}
}

// readSection returns the body of one "## " section of the document. A missing
// section is fatal: every caller here would otherwise search an empty string
// and report that the page is fine.
func readSection(t *testing.T, heading string) string {
	t.Helper()

	data, err := os.ReadFile(contractsDoc)
	if err != nil {
		t.Fatalf("reading %s: %v", contractsDoc, err)
	}
	var body []string
	in := false
	for _, line := range strings.Split(string(data), "\n") {
		if strings.HasPrefix(line, "## ") {
			in = strings.TrimSpace(line) == heading
			continue
		}
		if in {
			body = append(body, line)
		}
	}
	if len(body) == 0 {
		t.Fatalf("%s has no %q section, so there is nothing to check it against", contractsDoc, heading)
	}
	return strings.Join(body, "\n")
}

func fixtureSchemaVersion(t *testing.T) string {
	t.Helper()

	data, err := os.ReadFile(minimalFixture)
	if err != nil {
		t.Fatalf("reading %s: %v", minimalFixture, err)
	}
	var envelope struct {
		SchemaVersion string `json:"schemaVersion"`
	}
	if err := json.Unmarshal(data, &envelope); err != nil {
		t.Fatalf("parsing %s: %v", minimalFixture, err)
	}
	if envelope.SchemaVersion == "" {
		t.Fatalf("%s carries no schemaVersion, so there is nothing to compare", minimalFixture)
	}
	return envelope.SchemaVersion
}

// declaredNames returns the exported identifiers declared with tok at the top
// level of the package, test files excluded. The whole package and not one
// named file: the promise these tests defend is package-wide, and the files
// this package will grow are written by other tasks. Go cannot enumerate a
// package's constants or variables at run time, so the source is the only way.
func declaredNames(t *testing.T, tok token.Token) []string {
	t.Helper()

	entries, err := os.ReadDir(packageDir)
	if err != nil {
		t.Fatalf("reading %s: %v", packageDir, err)
	}
	var names []string
	files := 0
	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		files++
		names = append(names, exportedIn(t, filepath.Join(packageDir, name), tok)...)
	}
	if files == 0 {
		t.Fatalf("%s holds no non-test Go file; this test reads its subject from there", packageDir)
	}
	if len(names) == 0 {
		t.Fatalf("%s declares no exported %s in %d file(s); this test reads its subject from there",
			packageDir, tok, files)
	}
	return names
}

func exportedIn(t *testing.T, path string, tok token.Token) []string {
	t.Helper()

	file, err := parser.ParseFile(token.NewFileSet(), path, nil, 0)
	if err != nil {
		t.Fatalf("parsing %s: %v", path, err)
	}
	var names []string
	for _, decl := range file.Decls {
		gen, ok := decl.(*ast.GenDecl)
		if !ok || gen.Tok != tok {
			continue
		}
		for _, spec := range gen.Specs {
			values, ok := spec.(*ast.ValueSpec)
			if !ok {
				continue
			}
			for _, name := range values.Names {
				if name.IsExported() {
					names = append(names, name.Name)
				}
			}
		}
	}
	return names
}
