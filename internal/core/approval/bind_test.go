// Bind from outside the package, against the digest goldens in testdata/digest
// and against values written out below.
//
// Every expected binding, and every expected digest that is not a golden, is
// written out. Each was computed outside this repository's code, from the
// rules in testdata/digest/README.md, by a second implementation that first
// reproduced every golden there, and each binding was checked once more as
// sha256 over its stated preimage. None comes from the code under test.
package approval_test

import (
	"bytes"
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"strings"
	"testing"

	"google.golang.org/protobuf/encoding/protowire"
	"google.golang.org/protobuf/proto"

	controlv1 "github.com/guardana/control/api/gen/go/guardana/control/v1"
	"github.com/guardana/control/internal/canon"
	"github.com/guardana/control/internal/core/approval"
	"github.com/guardana/control/pkg/contract"
)

// The goldens sit at the repository root, where an implementation in another
// language reads them too.
const fixtureDir = "../../../testdata/digest"

// Two bundle digests one hex digit apart. bundle1 is the one canon's binding
// golden uses.
const (
	bundle1 approval.BundleDigest = "sha256:1111111111111111111111111111111111111111111111111111111111111111"
	bundle2 approval.BundleDigest = "sha256:1111111111111111111111111111111111111111111111111111111111111112"
)

// golden is one fixture of testdata/digest: an envelope the contract accepts,
// the authorized arguments as the file holds them, and the digest pinned for
// the two.
type golden struct {
	env    *controlv1.ActionEnvelope
	args   []byte
	digest approval.ActionDigest
}

func loadGolden(t testing.TB, name string) golden {
	t.Helper()
	raw, err := fs.ReadFile(os.DirFS(fixtureDir), name+".json")
	if err != nil {
		t.Fatalf("read fixture %s: %v", name, err)
	}
	var f struct {
		Envelope       json.RawMessage `json:"envelope"`
		AuthorizedArgs json.RawMessage `json:"authorized_args"`
		ExpectedDigest string          `json:"expected_digest"`
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&f); err != nil {
		t.Fatalf("decode fixture %s: %v", name, err)
	}
	if len(f.AuthorizedArgs) == 0 || f.ExpectedDigest == "" {
		t.Fatalf("fixture %s has no arguments or no expected digest", name)
	}
	// Decoded through the contract: an expected value over an envelope the
	// contract refuses is one for an action that cannot happen.
	env, err := contract.DecodeJSON(f.Envelope)
	if err != nil {
		t.Fatalf("fixture %s does not validate: %v", name, err)
	}
	return golden{env: env, args: f.AuthorizedArgs, digest: approval.ActionDigest(f.ExpectedDigest)}
}

func TestBindReproducesTheGoldens(t *testing.T) {
	cases := []struct {
		fixture string
		binding approval.Binding // under bundle1
	}{
		{"minimal", "sha256:cfa8d0097b7ba6fcb3e75c650922703da1b0ac9fa4716cfa0f371aa1a9c1a91e"},
		{"refund_prod", "sha256:3105283da9f4d61d9fa9558a8375d0796bce39ee48cdb7fbdff8a4ee0f34b394"},
		{"delegated", "sha256:65cf063761005a019fc9ffa5df2a23b3c7e1045ce0965a10f502b8a6f61498bd"},
		{"mutated_amount", "sha256:383dc0628ea1bafd043b64b6cfb90e2b2e2374bad673866f230b077a358cf964"},
		{"escapes", "sha256:ae6fe6d428a2165bf8d14ec4b13e8bf9c136f481ecf9a82eaf1999c13c06e5b0"},
	}
	for _, c := range cases {
		t.Run(c.fixture, func(t *testing.T) {
			g := loadGolden(t, c.fixture)
			assertBinds(t, g.env, g.args, bundle1, g.digest, c.binding)
		})
	}
}

// TestOneThingApartIsAnotherBinding holds the binding to what docs/contracts.md
// promises under "The approval binding": an approval bound to an action does
// not match it one argument byte apart, in another project, tenant or
// environment, or under another bundle. Each pair differs in one byte of one
// input, which the test establishes before it compares anything, and both
// sides are held to written-out values, so a Bind that answered differently
// on every call could not pass by being different.
func TestOneThingApartIsAnotherBinding(t *testing.T) {
	base := loadGolden(t, "refund_prod")
	// canon's binding golden for this fixture under bundle1.
	const baseBinding approval.Binding = "sha256:3105283da9f4d61d9fa9558a8375d0796bce39ee48cdb7fbdff8a4ee0f34b394"
	assertBinds(t, base.env, base.args, bundle1, base.digest, baseBinding)

	t.Run("one argument byte apart", func(t *testing.T) {
		args := bytes.Replace(base.args, []byte(`"amount_minor": 1250`), []byte(`"amount_minor": 1251`), 1)
		assertOneByteApart(t, base.args, args)
		const digest, binding = "sha256:0dea53394b63562f1c86d4a697af87c10ef4ff7c60509ac160ce94c8bb46649a",
			"sha256:e0d162b9cfa441a6c0067bc204a9b5b011dc3e0509a994d01983569f367ee6bd"
		assertApart(t, digest, base.digest, binding, baseBinding)
		assertBinds(t, base.env, args, bundle1, digest, binding)
	})

	fields := []struct {
		name    string
		get     func(*controlv1.ActionEnvelope) string
		set     func(*controlv1.ActionEnvelope, string)
		value   string
		digest  approval.ActionDigest
		binding approval.Binding
	}{
		{"project", (*controlv1.ActionEnvelope).GetProjectId,
			func(e *controlv1.ActionEnvelope, v string) { e.ProjectId = v }, "proj-fixturf",
			"sha256:75c3094025901446bc573f225332939ce0e1b7486373e8ed65bf990ff5e8df82",
			"sha256:c19f6649155fdf02522439a9e1ea4718d97e12a0dfc8f9ca11eb3b7d5d604fee"},
		{"tenant", (*controlv1.ActionEnvelope).GetTenantId,
			func(e *controlv1.ActionEnvelope, v string) { e.TenantId = v }, "tenant-fixturf",
			"sha256:26c0a7c3012fb4710a560937499fe64f7fdbe7e475928794cffdfb28da93f723",
			"sha256:9a3adc29754655f21f81ee8fe9506e7eb353743cebc9beb2cc2a6aabfdee9404"},
		{"environment", (*controlv1.ActionEnvelope).GetEnvironment,
			func(e *controlv1.ActionEnvelope, v string) { e.Environment = v }, "proe",
			"sha256:b0b4cf489e3c6d8207e863c58efd12825af2fbccf587d6a022361ee8f3ec1355",
			"sha256:94de49ee28c48b9a14df27b54a2fc8dfa3cbd4093f207ec48a48775353c08427"},
	}
	for _, f := range fields {
		t.Run("one "+f.name+" apart", func(t *testing.T) {
			env := proto.CloneOf(base.env)
			f.set(env, f.value)
			assertOneByteApart(t, []byte(f.get(base.env)), []byte(f.get(env)))
			assertDiffersOnlyIn(t, base.env, env, func(e *controlv1.ActionEnvelope) { f.set(e, "") })
			if err := contract.Validate(env); err != nil {
				t.Fatalf("the changed envelope is one the contract refuses: %v", err)
			}
			assertApart(t, f.digest, base.digest, f.binding, baseBinding)
			assertBinds(t, env, base.args, bundle1, f.digest, f.binding)
		})
	}

	t.Run("one bundle digit apart", func(t *testing.T) {
		assertOneByteApart(t, []byte(bundle1), []byte(bundle2))
		const binding approval.Binding = "sha256:a369c6981d1ead6426f64a51a316e75b3a98bcef103b3f9d11b69b1844e07288"
		if binding == baseBinding {
			t.Fatal("the expected binding is the base's; the pair tests nothing")
		}
		// The same action under another bundle: the same digest, another
		// binding.
		assertBinds(t, base.env, base.args, bundle2, base.digest, binding)
	})
}

// TestBindRefusesAMalformedBundleDigest: the binding is compared for equality
// to authorize a call, so a near miss of a bundle digest is refused rather
// than bound. Each refusal sits beside the accepted value it is one edit from.
func TestBindRefusesAMalformedBundleDigest(t *testing.T) {
	g := loadGolden(t, "minimal")
	hex64 := strings.Repeat("0123456789abcdef", 4)
	good := "sha256:" + hex64
	if _, _, err := approval.Bind(g.env, g.args, approval.BundleDigest(good)); err != nil {
		t.Fatalf("Bind refused %q, which holds every lowercase hex digit: %v", good, err)
	}
	for _, c := range []struct{ name, bundle string }{
		{"empty", ""},
		{"no prefix", hex64},
		{"prefix only", "sha256:"},
		{"upper case prefix", "SHA256:" + hex64},
		{"another algorithm", "sha512:" + hex64},
		{"one upper case digit", "sha256:" + hex64[:63] + "F"},
		{"a digit past f", "sha256:" + hex64[:63] + "g"},
		{"63 digits", "sha256:" + hex64[:63]},
		{"65 digits", good + "0"},
		{"leading space", " " + good},
		{"trailing space", good + " "},
		{"trailing newline", good + "\n"},
		{"a digit outside ASCII", "sha256:" + hex64[:63] + "٠"},
	} {
		t.Run(c.name, func(t *testing.T) {
			digest, binding, err := approval.Bind(g.env, g.args, approval.BundleDigest(c.bundle))
			assertRefused(t, digest, binding, err, canon.ErrMalformedDigest)
		})
	}
}

// TestBindRefusesWhatTheDigestRefuses: an action that cannot be digested
// cannot be approved, and the refusal keeps canon's sentinel so a caller can
// tell which it was.
func TestBindRefusesWhatTheDigestRefuses(t *testing.T) {
	g := loadGolden(t, "minimal")
	undeclared := proto.CloneOf(g.env)
	undeclared.Action.Effect = controlv1.EffectClass(99)
	unknown := proto.CloneOf(g.env)
	unknown.ProtoReflect().SetUnknown(protowire.AppendVarint(protowire.AppendTag(nil, 4095, protowire.VarintType), 1))
	oversize := []byte(`{"pad":"` + strings.Repeat("a", canon.MaxArgumentsBytes) + `"}`)

	for _, c := range []struct {
		name string
		env  *controlv1.ActionEnvelope
		args []byte
		want error
	}{
		{"no envelope", nil, g.args, canon.ErrMissingEnvelope},
		{"arguments over the bound", g.env, oversize, canon.ErrArgumentsTooLarge},
		{"a float in the arguments", g.env, []byte(`{"temperature":0.7}`), canon.ErrUnsupportedValue},
		{"arguments that are null", g.env, []byte(`null`), canon.ErrUnsupportedValue},
		{"an effect this build cannot name", undeclared, g.args, canon.ErrUnsupportedValue},
		{"a field this build does not know", unknown, g.args, canon.ErrUnsupportedValue},
	} {
		t.Run(c.name, func(t *testing.T) {
			digest, binding, err := approval.Bind(c.env, c.args, bundle1)
			assertRefused(t, digest, binding, err, c.want)
		})
	}
}

func assertBinds(t *testing.T, env *controlv1.ActionEnvelope, args []byte, bundle approval.BundleDigest,
	digest approval.ActionDigest, binding approval.Binding,
) {
	t.Helper()
	gotDigest, gotBinding, err := approval.Bind(env, args, bundle)
	if err != nil {
		t.Fatalf("Bind: %v", err)
	}
	if gotDigest != digest {
		t.Errorf("action digest = %s, want %s", gotDigest, digest)
	}
	if gotBinding != binding {
		t.Errorf("binding = %s, want %s", gotBinding, binding)
	}
}

// assertRefused holds a refusal to its sentinel and to handing out neither
// value: an action digest beside a refused bundle is one a caller could store
// an approval against.
func assertRefused(t *testing.T, digest approval.ActionDigest, binding approval.Binding, err, want error) {
	t.Helper()
	if !errors.Is(err, want) {
		t.Errorf("err = %v, want %v", err, want)
	}
	if digest != "" || binding != "" {
		t.Errorf("a refusal handed out %q and %q", digest, binding)
	}
}

// assertApart fails when an expected value equals the base's, which would
// leave the pair testing nothing.
func assertApart(t *testing.T, digest, baseDigest approval.ActionDigest, binding, baseBinding approval.Binding) {
	t.Helper()
	if digest == baseDigest || binding == baseBinding {
		t.Fatal("an expected value is the base's; the pair tests nothing")
	}
}

func assertOneByteApart(t *testing.T, a, b []byte) {
	t.Helper()
	if len(a) != len(b) {
		t.Fatalf("%d bytes and %d bytes; want two inputs of one length, one byte apart", len(a), len(b))
	}
	differ := 0
	for i := range a {
		if a[i] != b[i] {
			differ++
		}
	}
	if differ != 1 {
		t.Fatalf("%d bytes differ, want exactly 1", differ)
	}
}

// assertDiffersOnlyIn fails unless a and b differ and are equal once reset has
// cleared the one field under test on both.
func assertDiffersOnlyIn(t *testing.T, a, b *controlv1.ActionEnvelope, reset func(*controlv1.ActionEnvelope)) {
	t.Helper()
	if proto.Equal(a, b) {
		t.Fatal("the two envelopes are equal")
	}
	ca, cb := proto.CloneOf(a), proto.CloneOf(b)
	reset(ca)
	reset(cb)
	if !proto.Equal(ca, cb) {
		t.Fatal("the two envelopes differ outside the field under test")
	}
}
