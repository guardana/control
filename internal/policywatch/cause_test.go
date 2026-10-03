package policywatch

import (
	"context"
	"errors"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"slices"
	"strconv"
	"testing"

	"github.com/guardana/control/internal/policy"
)

// TestCausesIsEveryDeclaredCause reads the constants of type Cause from the
// source, so a cause declared without joining the set fails here.
func TestCausesIsEveryDeclaredCause(t *testing.T) {
	declared := declaredCauses(t)
	if len(declared) < 18 {
		t.Fatalf("the source declares %d causes; the walk missed some", len(declared))
	}
	if got := Causes(); !slices.Equal(got, declared) {
		t.Errorf("Causes() = %q, the source declares %q", got, declared)
	}
	got := Causes()
	got[0] = "changed"
	if Causes()[0] != CauseBundleUnreadable {
		t.Error("a caller changed the set through the slice it was given")
	}
}

// declaredCauses is the value of every constant cause.go declares with the
// type Cause, in its order.
func declaredCauses(t *testing.T) []Cause {
	t.Helper()
	file, err := parser.ParseFile(token.NewFileSet(), "cause.go", nil, 0)
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

// TestCauseOfNamesEveryHolderRefusal holds every refusal Holder.Confirm and
// Holder.InstallConfirmed return, wrapped as they return it, to its cause.
// Only what the store's raise or the holder's floor returned is the floor's;
// a refusal no row names is unknown.
func TestCauseOfNamesEveryHolderRefusal(t *testing.T) {
	raise := func(err error) error { return fmt.Errorf("%w: %w", policy.ErrFloorRaise, err) }
	wrap := func(err error) error { return fmt.Errorf("%w: detail", err) }
	for _, c := range []struct {
		err  error
		want Cause
	}{
		{wrap(policy.ErrTooLarge), CauseBundleInvalid},
		{wrap(policy.ErrUnknownField), CauseBundleInvalid},
		{policy.ErrSignatureAlg, CauseBundleInvalid},
		{wrap(policy.ErrKey), CauseBundleInvalid},
		{wrap(policy.ErrSignature), CauseBundleInvalid},
		{wrap(policy.ErrDocument), CauseBundleInvalid},
		{policy.ErrNotCanonical, CauseBundleInvalid},
		{policy.ErrDigest, CauseBundleInvalid},
		{wrap(policy.ErrMismatch), CauseBundleInvalid},
		{policy.ErrCreatedAt, CauseBundleInvalid},
		{wrap(policy.ErrBundlePin), CauseBundleID},
		{policy.ErrStatementUnbound, CauseStatementUnbound},
		{wrap(policy.ErrConfirmationWithdrawn), CauseWithdrawn},
		{wrap(policy.ErrRollback), CauseRollback},
		{wrap(policy.ErrSerialReused), CauseSerialReused},
		{raise(policy.ErrFloorBundle), CauseBundleID},
		{raise(wrap(policy.ErrStatementFuture)), CauseStatementFuture},
		{raise(wrap(policy.ErrClockBehindFloor)), CauseClockBehindFloor},
		{raise(wrap(policy.ErrFloorSerialReused)), CauseSerialReused},
		{raise(wrap(policy.ErrBelowFloor)), CauseBelowFloor},
		{raise(policy.ErrFloorInvalid), CauseFloor},
		{raise(context.DeadlineExceeded), CauseFloor},
		{raise(context.Canceled), CauseFloor},
		{raise(errors.New("the disk failed")), CauseFloor},
		{wrap(policy.ErrFloorRaise), CauseFloor},
		{policy.ErrNoFloorStore, CauseFloor},
		{policy.ErrNoBundle, CauseUnknown},
		{context.DeadlineExceeded, CauseUnknown},
		{errors.New("a refusal no row names"), CauseUnknown},
	} {
		if got := causeOf(c.err); got != c.want {
			t.Errorf("causeOf(%v) = %s, want %s", c.err, got, c.want)
		}
	}
}
