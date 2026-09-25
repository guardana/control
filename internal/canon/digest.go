package canon

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"slices"
	"strings"

	controlv1 "github.com/guardana/control/api/gen/go/guardana/control/v1"
)

// The domain tags are fixed byte strings, chosen once and never derived from
// anything a rename would rewrite (ADR-0010). Each ends with a newline: RFC 8785
// escapes every control character, so a raw newline cannot occur in the body and
// the boundary between tag and body is unambiguous.
const (
	DigestDomainV1    = "agent-action-digest/v1\n"
	BindingDomainV1   = "agent-approval-binding/v1\n"
	ArgumentsDomainV1 = "agent-arguments-hash/v1\n"

	// MaxArgumentsBytes bounds the authorized arguments document handed to
	// DigestV1 or ArgumentsHashV1. The envelope carries only a hash and a
	// preview of the arguments, so these calls are the first place they exist
	// as bytes and the only place the bound can be applied. It is not
	// contract.MaxArgumentsBytes, which bounds the encoded Arguments message;
	// the two happen to be equal.
	MaxArgumentsBytes = 65536

	digestPrefix = "sha256:"
	digestHexLen = sha256.Size * 2

	// The key the authorized arguments occupy in the canonical action, and the
	// root of the JSON pointer in a refusal about them.
	argumentsKey = "authorizedArguments"
)

// Reported for an input this file refuses before the canonical form sees it.
// A refusal from the canonical form itself carries ErrUnsupportedValue and a
// JSON pointer, and travels out of here wrapped.
var (
	ErrMissingEnvelope   = errors.New("canon: no envelope")
	ErrArgumentsTooLarge = errors.New("canon: authorized arguments are over the limit")
	ErrMalformedDigest   = errors.New("canon: malformed digest")
)

// CanonicalAction extracts the field set of ADR-0005, as ADR-0011 corrected
// it, from env, together with the authorized arguments, into a map ready for
// Canonicalize. The authorizedArguments member is opaque: the canonical bytes
// of the arguments document, of an unexported type the encoder writes as they
// stand, not a value tree. A caller that needs an argument value parses those
// bytes, which are exactly what the digest and the arguments hash cover, and
// nothing in this package does so yet.
//
// Included: the envelope's own project, tenant and environment, which say where
// the action happens; principal, agent, delegation, action, resource and
// destination; and the authorized arguments. Excluded: the schema version and
// every identifier and timestamp, which two attempts at one action differ in;
// the data labels and the run context, which cannot change inside the trail of
// the request an approval was requested for, the only place it is honoured; and
// the arguments hash and preview, which the arguments themselves stand in for.
//
// Every field of an included message is present at its proto zero value, so a
// producer that omits an empty scalar and one that writes it agree. Keys are the
// protobuf JSON names, which is what protojson emits in every language.
// testdata/digest/README.md states the whole mapping for an implementation that
// cannot read Go.
//
// A field this build does not know, in the envelope or in a message it reads,
// is refused rather than skipped.
func CanonicalAction(env *controlv1.ActionEnvelope, authorizedArgs []byte) (map[string]any, error) {
	if env == nil {
		return nil, ErrMissingEnvelope
	}
	if err := refuseUnknownFields(env); err != nil {
		return nil, err
	}
	args, err := authorizedArguments(authorizedArgs)
	if err != nil {
		return nil, err
	}
	effect, err := enumName(controlv1.EffectClass_name, int32(env.GetAction().GetEffect()), "/action/effect")
	if err != nil {
		return nil, err
	}
	zone, err := enumName(controlv1.TrustZone_name, int32(env.GetDestination().GetTrustZone()), "/destination/trustZone")
	if err != nil {
		return nil, err
	}

	principal, agent := env.GetPrincipal(), env.GetAgent()
	action, resource, destination := env.GetAction(), env.GetResource(), env.GetDestination()
	return map[string]any{
		// The envelope's own values, never the principal's or the resource's,
		// which a producer may leave empty or set differently.
		"projectId":   env.GetProjectId(),
		"tenantId":    env.GetTenantId(),
		"environment": env.GetEnvironment(),
		"principal": map[string]any{
			"id":            principal.GetId(),
			"type":          principal.GetType(),
			"authnStrength": principal.GetAuthnStrength(),
			"tenantId":      principal.GetTenantId(),
			"attributes":    stringMap(principal.GetAttributes()),
		},
		"agent": map[string]any{
			"id":         agent.GetId(),
			"instanceId": agent.GetInstanceId(),
			"framework":  agent.GetFramework(),
			"version":    agent.GetVersion(),
			"modelRef":   agent.GetModelRef(),
		},
		"delegation": delegationChain(env.GetDelegation()),
		"action": map[string]any{
			"kind":     action.GetKind(),
			"name":     action.GetName(),
			"protocol": action.GetProtocol(),
			"effect":   effect,
			"provider": action.GetProvider(),
		},
		"resource": map[string]any{
			"type":        resource.GetType(),
			"id":          resource.GetId(),
			"tenantId":    resource.GetTenantId(),
			"environment": resource.GetEnvironment(),
			"labels":      stringMap(resource.GetLabels()),
		},
		"destination": map[string]any{
			"trustZone": zone,
			"host":      destination.GetHost(),
		},
		argumentsKey: args,
	}, nil
}

// DigestV1 returns the canonical action digest of env: sha256 over the domain
// tag followed by the canonical bytes of CanonicalAction, as "sha256:" and
// lowercase hex. The case is part of the contract, because the string is
// compared for equality to authorize a call.
func DigestV1(env *controlv1.ActionEnvelope, authorizedArgs []byte) (string, error) {
	action, err := CanonicalAction(env, authorizedArgs)
	if err != nil {
		return "", err
	}
	body, err := Canonicalize(action)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(slices.Concat([]byte(DigestDomainV1), body))
	return digestPrefix + hex.EncodeToString(sum[:]), nil
}

// ApprovalBinding returns the value an approval is stored and compared against:
// sha256 over the binding tag, the action digest, a newline and the policy
// bundle digest. Binding both is what stops an approval from being replayed
// against changed parameters or against a different bundle than the approver
// saw.
//
// It refuses an input that is not "sha256:" and 64 lowercase hex digits. A
// malformed digest is not something to hash: it means the caller has already
// lost track of which action it is binding.
func ApprovalBinding(actionDigest, bundleDigest string) (string, error) {
	for _, input := range []struct{ name, value string }{
		{"action", actionDigest},
		{"bundle", bundleDigest},
	} {
		if !validDigest(input.value) {
			return "", fmt.Errorf("%w: the %s digest is not %s and %d lowercase hex digits",
				ErrMalformedDigest, input.name, digestPrefix, digestHexLen)
		}
	}
	// A newline separates them for the same reason the tag ends in one, even
	// though both are fixed length today.
	sum := sha256.Sum256(slices.Concat(
		[]byte(BindingDomainV1), []byte(actionDigest), []byte("\n"), []byte(bundleDigest)))
	return digestPrefix + hex.EncodeToString(sum[:]), nil
}

// delegationChain keeps the hops in their given order, because that order is the
// authority chain and reversing it inverts the "child exceeds parent" check. The
// scopes inside a hop are a set, so they are sorted, in the same UTF-16 order
// the canonical form sorts object keys by. They are not deduplicated: ADR-0005
// says sorted and says nothing about repeats, and dropping one would give two
// documents one digest.
//
// Timestamps are not in the digest: a producer that mints a short-lived
// delegation per attempt would otherwise give the same action a different digest
// every time, and an approval could never match a retry.
func delegationChain(hops []*controlv1.Delegation) []any {
	chain := make([]any, 0, len(hops))
	for _, hop := range hops {
		// Cloned before sorting: the envelope belongs to the caller and
		// computing a digest may not reorder it.
		scopes := slices.Clone(hop.GetScopes())
		sortUTF16(scopes)
		items := make([]any, len(scopes))
		for i, scope := range scopes {
			items[i] = scope
		}
		chain = append(chain, map[string]any{
			"from":   hop.GetFrom(),
			"to":     hop.GetTo(),
			"scopes": items,
			"reason": hop.GetReason(),
		})
	}
	return chain
}

func stringMap(m map[string]string) map[string]any {
	out := make(map[string]any, len(m))
	for key, value := range m {
		out[key] = value
	}
	return out
}

// enumName refuses a number the enum has no name for. protojson emits such a
// value as a number, so accepting it here would let two implementations disagree
// on the digest of the same envelope; ADR-0005 fixes the encoding as the name.
func enumName(names map[int32]string, number int32, pointer string) (string, error) {
	name, ok := names[number]
	if !ok {
		return "", fmt.Errorf("%w at %q: the enum has no name for number %d",
			ErrUnsupportedValue, pointer, number)
	}
	return name, nil
}

func validDigest(digest string) bool {
	hexPart, ok := strings.CutPrefix(digest, digestPrefix)
	if !ok || len(hexPart) != digestHexLen {
		return false
	}
	// Not hex.DecodeString: it accepts upper case, and the case is part of the
	// contract because these strings are compared for equality.
	return strings.IndexFunc(hexPart, func(r rune) bool {
		return !strings.ContainsRune(hexDigits, r)
	}) < 0
}
