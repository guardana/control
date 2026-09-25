package pause

import (
	"go/ast"
	"go/parser"
	"go/token"
	"slices"
	"strconv"
	"testing"
)

// TestCausesIsEveryDeclaredCause reads the constants of type Cause from the
// source, so a cause declared without joining the set fails here.
func TestCausesIsEveryDeclaredCause(t *testing.T) {
	declared := declaredCauses(t)
	if len(declared) < 13 {
		t.Fatalf("the source declares %d causes; the walk missed some", len(declared))
	}
	if got := Causes(); !slices.Equal(got, declared) {
		t.Errorf("Causes() = %q, the source declares %q", got, declared)
	}
	got := Causes()
	got[0] = "changed"
	if Causes()[0] != CauseNeverRead {
		t.Error("a caller changed the set through the slice it was given")
	}
}

// declaredCauses is the value of every constant snapshot.go declares with
// the type Cause, in its order.
func declaredCauses(t *testing.T) []Cause {
	t.Helper()
	file, err := parser.ParseFile(token.NewFileSet(), "snapshot.go", nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	var declared []Cause
	for _, decl := range file.Decls {
		gen, ok := decl.(*ast.GenDecl)
		if !ok || gen.Tok != token.CONST {
			continue
		}
		for _, spec := range gen.Specs {
			v := spec.(*ast.ValueSpec)
			if typ, ok := v.Type.(*ast.Ident); !ok || typ.Name != "Cause" {
				continue
			}
			for _, value := range v.Values {
				text, err := strconv.Unquote(value.(*ast.BasicLit).Value)
				if err != nil {
					t.Fatal(err)
				}
				declared = append(declared, Cause(text))
			}
		}
	}
	return declared
}
