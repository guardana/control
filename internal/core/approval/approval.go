// Package approval computes the value an approval binds to: the canonical action
// digest under one policy bundle's digest (ADR-0005, ADR-0011).
package approval

import (
	"fmt"

	controlv1 "github.com/guardana/control/api/gen/go/guardana/control/v1"
	"github.com/guardana/control/internal/canon"
)

// ActionDigest is a canonical action digest: "sha256:" and 64 lowercase hex
// digits.
type ActionDigest string

// BundleDigest is a policy bundle digest, in the same form.
type BundleDigest string

// Binding is the value an approval is stored and compared against.
type Binding string

// Bind returns the action digest of env with authorizedArgs and the binding of
// that digest to bundle. It refuses what canon.DigestV1 refuses and a bundle
// digest canon.ApprovalBinding refuses, and canon's sentinel stays reachable
// through errors.Is. A refusal returns neither value: an action digest handed
// out beside a refused bundle digest is one a caller could store an approval
// against.
func Bind(env *controlv1.ActionEnvelope, authorizedArgs []byte, bundle BundleDigest) (ActionDigest, Binding, error) {
	digest, err := canon.DigestV1(env, authorizedArgs)
	if err != nil {
		return "", "", fmt.Errorf("approval: %w", err)
	}
	binding, err := canon.ApprovalBinding(digest, string(bundle))
	if err != nil {
		return "", "", fmt.Errorf("approval: %w", err)
	}
	return ActionDigest(digest), Binding(binding), nil
}
