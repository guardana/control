package approval_test

import (
	"crypto/sha256"
	"encoding/hex"
	"strings"
	"testing"

	"github.com/guardana/control/internal/canon"
	"github.com/guardana/control/internal/core/approval"
)

// The binding tag, written out rather than read from canon, so that a change
// there is a failure here.
const bindingTag = "agent-approval-binding/v1\n"

// FuzzBind: whatever the arguments and the bundle digest, Bind does not panic;
// it accepts exactly when the digest accepts the arguments and the bundle
// digest has the contract's form; and what it accepts it binds by the preimage
// the contract states, hashed here without the package. A refusal hands out
// neither value.
func FuzzBind(f *testing.F) {
	g := loadGolden(f, "minimal")
	for _, seed := range []struct{ args, bundle string }{
		{`{}`, string(bundle1)},
		{``, string(bundle2)},
		{`{"amount_minor":1250,"currency":"XTS"}`, string(bundle1)},
		{`null`, string(bundle1)},
		{`{"temperature":0.7}`, string(bundle1)},
		{`{"a":1,"a":2}`, string(bundle1)},
		{`{"k":1,"K":2}`, string(bundle1)},
		{`"\ud800"`, string(bundle1)},
		{strings.Repeat("[", 33) + strings.Repeat("]", 33), string(bundle1)},
		{`{}`, strings.ToUpper(string(bundle1))},
		{`{}`, "sha256:" + strings.Repeat("a", 63)},
	} {
		f.Add([]byte(seed.args), seed.bundle)
	}
	f.Fuzz(func(t *testing.T, args []byte, bundle string) {
		digest, binding, err := approval.Bind(g.env, args, approval.BundleDigest(bundle))
		_, argsErr := canon.ArgumentsHashV1(args)
		if accept := argsErr == nil && digestForm.MatchString(bundle); (err == nil) != accept {
			t.Fatalf("Bind err = %v; the arguments are refused with %v, and the bundle digest is %q", err, argsErr, bundle)
		}
		if err != nil {
			if digest != "" || binding != "" {
				t.Fatalf("a refusal handed out %q and %q", digest, binding)
			}
			return
		}
		if !digestForm.MatchString(string(digest)) {
			t.Fatalf("action digest %q is not in the contract's form", digest)
		}
		sum := sha256.Sum256([]byte(bindingTag + string(digest) + "\n" + bundle))
		if want := "sha256:" + hex.EncodeToString(sum[:]); string(binding) != want {
			t.Fatalf("binding = %s, but sha256 over the stated preimage is %s", binding, want)
		}
	})
}
