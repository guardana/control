package approval_test

import (
	"errors"
	"regexp"
	"strings"
	"testing"

	"pgregory.net/rapid"

	controlv1 "github.com/guardana/control/api/gen/go/guardana/control/v1"
	"github.com/guardana/control/internal/canon"
	"github.com/guardana/control/internal/core/approval"
)

// digestForm is the form the contract states for every digest, written here
// rather than taken from canon.
var digestForm = regexp.MustCompile(`^sha256:[0-9a-f]{64}$`)

// The alphabets a changed byte is drawn from. The argument's has no quote and
// no backslash, so the value stays one JSON string whose canonical form
// changes in exactly the byte that changed.
const (
	identAlphabet = "abcdefghijklmnopqrstuvwxyz0123456789-"
	valueAlphabet = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789 "
	hexAlphabet   = "0123456789abcdef"
)

// action is the part of a proposed action the property varies.
type action struct {
	project, tenant, environment, argument, bundleHex string
}

func drawAction(t *rapid.T) action {
	ident := rapid.StringMatching(`[a-z0-9-]{1,16}`)
	return action{
		project:     ident.Draw(t, "project"),
		tenant:      ident.Draw(t, "tenant"),
		environment: ident.Draw(t, "environment"),
		argument:    rapid.StringMatching(`[a-zA-Z0-9 ]{1,32}`).Draw(t, "argument"),
		bundleHex:   rapid.StringMatching(`[0-9a-f]{64}`).Draw(t, "bundle"),
	}
}

func (a action) bind(t *rapid.T) (approval.ActionDigest, approval.Binding) {
	env := &controlv1.ActionEnvelope{
		ProjectId:   a.project,
		TenantId:    a.tenant,
		Environment: a.environment,
		Principal:   &controlv1.Principal{Id: "user-1"},
		Action:      &controlv1.Action{Name: "read_item", Effect: controlv1.EffectClass_EFFECT_CLASS_READ},
		Resource:    &controlv1.Resource{Type: "item"},
	}
	args := []byte(`{"value":"` + a.argument + `"}`)
	digest, binding, err := approval.Bind(env, args, approval.BundleDigest("sha256:"+a.bundleHex))
	if err != nil {
		t.Fatalf("Bind: %v", err)
	}
	return digest, binding
}

// TestOneByteApartIsAnotherBindingForAnyAction is the exit criterion over
// drawn actions: one byte of the project, the tenant, the environment, an
// argument or the bundle digest changes the binding, and each of them but the
// bundle digest changes the action digest too. Bind is asked twice about the
// unchanged input and has to answer the same both times, so a Bind that
// answered differently on every call could not pass by being different.
func TestOneByteApartIsAnotherBindingForAnyAction(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		a := drawAction(t)
		digest, binding := a.bind(t)
		if again, bindingAgain := a.bind(t); again != digest || bindingAgain != binding {
			t.Fatalf("one input bound twice gave %s %s, then %s %s", digest, binding, again, bindingAgain)
		}
		if !digestForm.MatchString(string(digest)) || !digestForm.MatchString(string(binding)) {
			t.Fatalf("Bind gave %q and %q, want two digests in the contract's form", digest, binding)
		}

		b := a
		what := rapid.SampledFrom([]string{"project", "tenant", "environment", "argument", "bundle digest"}).Draw(t, "what changes")
		switch what {
		case "project":
			b.project = changeOneByte(t, a.project, identAlphabet)
		case "tenant":
			b.tenant = changeOneByte(t, a.tenant, identAlphabet)
		case "environment":
			b.environment = changeOneByte(t, a.environment, identAlphabet)
		case "argument":
			b.argument = changeOneByte(t, a.argument, valueAlphabet)
		default:
			b.bundleHex = changeOneByte(t, a.bundleHex, hexAlphabet)
		}
		otherDigest, otherBinding := b.bind(t)
		if otherBinding == binding {
			t.Fatalf("one byte of the %s apart gave the same binding %s", what, binding)
		}
		if (what == "bundle digest") != (otherDigest == digest) {
			t.Fatalf("one byte of the %s apart: action digest %s, then %s", what, digest, otherDigest)
		}
	})
}

func changeOneByte(t *rapid.T, s, alphabet string) string {
	i := rapid.IntRange(0, len(s)-1).Draw(t, "at")
	others := strings.ReplaceAll(alphabet, s[i:i+1], "")
	b := []byte(s)
	b[i] = others[rapid.IntRange(0, len(others)-1).Draw(t, "to")]
	return string(b)
}

// TestBindAcceptsABundleDigestExactlyInTheContractsForm draws near misses of a
// well-formed bundle digest, up to two edits away, and holds Bind to the form
// the contract states: accepted exactly when it matches.
func TestBindAcceptsABundleDigestExactlyInTheContractsForm(t *testing.T) {
	g := loadGolden(t, "minimal")
	rapid.Check(t, func(t *rapid.T) {
		s := []rune("sha256:" + rapid.StringMatching(`[0-9a-f]{64}`).Draw(t, "hex"))
		for range rapid.IntRange(0, 2).Draw(t, "edits") {
			s = edit(t, s)
		}
		digest, binding, err := approval.Bind(g.env, g.args, approval.BundleDigest(string(s)))
		if digestForm.MatchString(string(s)) {
			if err != nil {
				t.Fatalf("Bind refused %q, which has the contract's form: %v", string(s), err)
			}
			return
		}
		if !errors.Is(err, canon.ErrMalformedDigest) || digest != "" || binding != "" {
			t.Fatalf("Bind(%q) = %q, %q, %v; want a refusal carrying ErrMalformedDigest and no values",
				string(s), digest, binding, err)
		}
	})
}

// edit inserts, deletes or replaces one rune, drawn from runes a digest is
// often mistaken for: digits and letters on both sides of the hex range, both
// cases, the separator, white space and a digit outside ASCII.
func edit(t *rapid.T, s []rune) []rune {
	r := rapid.SampledFrom([]rune{'0', '9', 'a', 'f', 'g', 'A', 'F', ':', 's', ' ', '\n', '٠'}).Draw(t, "rune")
	i := rapid.IntRange(0, len(s)).Draw(t, "where")
	out := append([]rune(nil), s[:i]...)
	switch op := rapid.IntRange(0, 2).Draw(t, "edit"); {
	case op == 0:
		out = append(out, r)
		return append(out, s[i:]...)
	case i == len(s):
		return s
	case op == 1:
		return append(out, s[i+1:]...)
	default:
		out = append(out, r)
		return append(out, s[i+1:]...)
	}
}
