// Package obligationdoc renders the obligation reference page from the
// catalogue's source and from who applies each type. The catalogue is a list
// of names inside
// internal/policy/rules, which exports one membership test and no listing,
// and a guarded tree cannot read its own source; so this package, outside
// the guarded trees, reads the names from the file the same way a reader
// would, and the pin test holds each name to the membership test.
//
// Rendering is a pure function of the source bytes and the appliers. No
// clock, no filesystem:
// scripts/gen-obligations.go is the thin writer that puts the result on disk,
// and the test that pins the committed page calls Page directly.
package obligationdoc

import (
	"bytes"
	"errors"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"slices"
	"strconv"
	"strings"
	"unicode"

	"github.com/guardana/control/internal/docscheck/frontmatter"
)

// meta is the page's frontmatter: what the documentation checks read to place
// the page, and covers is the code whose change makes it suspect.
var meta = frontmatter.Meta{
	Title:     "Obligations",
	Summary:   "The catalogue of obligation types a policy rule may name, and what the tree does with one.",
	Type:      "reference",
	Covers:    []string{"internal/policy/rules/catalogue.go", "internal/gateway/rewrite.go", "adapters/mcp/obligations.go", "internal/docscheck/obligationdoc/**"},
	Generated: "scripts/gen-obligations.go",
}

// catalogueFunc is the function of internal/policy/rules/catalogue.go whose
// one return statement is the catalogue.
const catalogueFunc = "obligationTypes"

// The intro answers one question: what an obligation type on a rule or a
// decision is, and what the tree does with one. Status labels are the
// project's three, and the page points at docs/status.md for the rest.
const header = "\n# Obligations\n" +
	"\n" +
	"An obligation is a condition on a call that proceeds. It travels on a\n" +
	"decision in `Decision.obligations`, under `ALLOW_WITH_OBLIGATIONS` and\n" +
	"under `REQUIRE_APPROVAL` once the approval is given. A policy rule names an\n" +
	"obligation by its type, with string parameters and an `advisory` flag that\n" +
	"is false by default: false means the call proceeds only if the obligation is\n" +
	"applied ([ADR-0011](../adr/0011-contract-corrections-before-publication.md)).\n" +
	"\n" +
	"The table below is the catalogue, the closed set of types a policy document\n" +
	"may name, and who in this build applies each type. Recognising a type is\n" +
	"`implemented`: the parser and `policy lint` refuse a document naming a type\n" +
	"outside it, and the kernel refuses a configuration whose applicable types\n" +
	"include one outside it. The gateway rewrites the arguments for its types\n" +
	"before the decision it records, and an adapter applies the types it declares\n" +
	"to the call it sends. A decision carrying a non-advisory obligation that\n" +
	"nothing here applies is `DENY` with `OBLIGATION_NOT_UNDERSTOOD`, and no\n" +
	"fail-open setting relieves that; an advisory one is skipped. The name is all\n" +
	"the tree holds about a type: a parameter's key is checked as a map key, and\n" +
	"no type has a parameter schema yet. What is built and what is not is in\n" +
	"[status.md](../status.md).\n" +
	"\n" +
	"Rendered from the catalogue in `internal/policy/rules/catalogue.go` and the\n" +
	"types the gateway and the MCP adapter declare. Rebuild it with\n" +
	"`make docs-gen`; an edit made here does not survive the next run.\n" +
	"\n" +
	"| Type | Applied by |\n" +
	"| --- | --- |\n"

// unapplied is the cell of a type nothing in this build applies.
const unapplied = "`planned`: nothing in this build"

// Page renders the reference page from the catalogue's Go source and from
// appliers, each type's row naming every applier that declares it.
func Page(src []byte, appliers []Applier) ([]byte, error) {
	names, err := Names(src)
	if err != nil {
		return nil, err
	}
	return render(names, appliers)
}

// Names reads the obligation types out of the catalogue's Go source: the
// string literals of the one return statement of obligationTypes, in order.
// Anything else in that function, a second return, a call or a name where
// a literal belongs, is refused rather than read as an empty catalogue.
func Names(src []byte) ([]string, error) {
	file, err := parser.ParseFile(token.NewFileSet(), "catalogue.go", src, parser.SkipObjectResolution)
	if err != nil {
		return nil, fmt.Errorf("parsing the catalogue: %w", err)
	}
	fn := findFunc(file, catalogueFunc)
	if fn == nil || fn.Body == nil {
		return nil, fmt.Errorf("the catalogue has no function %s with a body", catalogueFunc)
	}
	list, err := singleReturn(fn.Body)
	if err != nil {
		return nil, err
	}
	names := make([]string, 0, len(list.Elts))
	for i, elt := range list.Elts {
		name, err := stringLiteral(elt)
		if err != nil {
			return nil, fmt.Errorf("%s: element %d: %w", catalogueFunc, i, err)
		}
		if err := checkName(name); err != nil {
			return nil, fmt.Errorf("%s: element %d: %w", catalogueFunc, i, err)
		}
		names = append(names, name)
	}
	if len(names) == 0 {
		return nil, errors.New("the catalogue is empty; refusing to render a page listing no types")
	}
	for i, name := range names {
		if j := slices.Index(names[:i], name); j >= 0 {
			return nil, fmt.Errorf("%s: %q at element %d and again at %d", catalogueFunc, name, j, i)
		}
	}
	return names, nil
}

func findFunc(file *ast.File, name string) *ast.FuncDecl {
	for _, decl := range file.Decls {
		if fn, ok := decl.(*ast.FuncDecl); ok && fn.Recv == nil && fn.Name.Name == name {
			return fn
		}
	}
	return nil
}

// singleReturn is the composite literal of the body's one return statement.
// The walk covers nested statements, so a return inside an if is a second
// return, not an invisible one.
func singleReturn(body *ast.BlockStmt) (*ast.CompositeLit, error) {
	var returns []*ast.ReturnStmt
	ast.Inspect(body, func(n ast.Node) bool {
		if ret, ok := n.(*ast.ReturnStmt); ok {
			returns = append(returns, ret)
		}
		return true
	})
	if len(returns) != 1 {
		return nil, fmt.Errorf("%s has %d return statements, want exactly one", catalogueFunc, len(returns))
	}
	if len(returns[0].Results) != 1 {
		return nil, fmt.Errorf("%s returns %d values, want one", catalogueFunc, len(returns[0].Results))
	}
	list, ok := returns[0].Results[0].(*ast.CompositeLit)
	if !ok {
		return nil, fmt.Errorf("%s does not return a literal list", catalogueFunc)
	}
	return list, nil
}

// stringLiteral reads one element, refusing anything but an interpreted
// string literal: a raw string could hold a backtick's neighbour that the
// table would render differently from the source.
func stringLiteral(elt ast.Expr) (string, error) {
	lit, ok := elt.(*ast.BasicLit)
	if !ok || lit.Kind != token.STRING || !strings.HasPrefix(lit.Value, `"`) {
		return "", errors.New("not an interpreted string literal")
	}
	name, err := strconv.Unquote(lit.Value)
	if err != nil {
		return "", err
	}
	return name, nil
}

// checkName refuses a name a table cell in code font cannot carry as it is:
// the pipe that ends the cell, the backtick that ends the code span, and
// every control, format or separator character, which is invisible or a
// line break to some readers.
func checkName(name string) error {
	if name == "" {
		return errors.New("an empty name")
	}
	if i := strings.IndexFunc(name, breaksTheRow); i >= 0 {
		return fmt.Errorf("%q holds %U at byte %d, which a table row cannot carry", name, []rune(name[i:])[0], i)
	}
	return nil
}

func breaksTheRow(r rune) bool {
	return r == '|' || r == '`' || unicode.IsControl(r) || unicode.Is(unicode.Cf, r) || r == 0x2028 || r == 0x2029
}

// render builds the page in memory, so a catalogue it refuses leaves the
// previous page on disk rather than truncating it.
func render(names []string, appliers []Applier) ([]byte, error) {
	if len(names) == 0 {
		return nil, errors.New("the catalogue is empty; refusing to render a page listing no types")
	}
	if err := checkAppliers(names, appliers); err != nil {
		return nil, err
	}
	front, err := frontmatter.Render(meta)
	if err != nil {
		return nil, err
	}
	var buf bytes.Buffer
	buf.Write(front)
	buf.WriteString(header)
	for _, name := range names {
		if err := checkName(name); err != nil {
			return nil, err
		}
		fmt.Fprintf(&buf, "| `%s` | %s |\n", name, appliedBy(name, appliers))
	}
	return buf.Bytes(), nil
}

// appliedBy is the cell of one type: every applier that declares it, in the
// order given, or the planned label when none does.
func appliedBy(name string, appliers []Applier) string {
	var by []string
	for _, a := range appliers {
		if slices.Contains(a.Types, name) {
			by = append(by, a.Name)
		}
	}
	if len(by) == 0 {
		return unapplied
	}
	return strings.Join(by, ", ")
}
