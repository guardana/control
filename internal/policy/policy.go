// Package policy loads a signed policy bundle into a snapshot the kernel decides
// against, and holds the current one. What a bundle must be to load is
// ADR-0011's; what a snapshot promises is ADR-0012's.
package policy

import (
	"time"

	"google.golang.org/protobuf/proto"

	controlv1 "github.com/guardana/control/api/gen/go/guardana/control/v1"
	"github.com/guardana/control/internal/policy/match"
)

// Snapshot is one verified, compiled bundle, confirmed current at one time. It
// is never changed after it is made; a refresh makes a new one. A nil Snapshot,
// and the zero value, hold no policy: they report nothing and evaluate as a
// program nobody compiled does.
type Snapshot struct {
	ref         *controlv1.PolicyBundleRef
	confirmedAt time.Time
	maxStale    time.Duration
	serial      int64
	program     *match.Program
}

// Ref returns a clone of the bundle's reference: its id, version and digest.
// It never carries a creation time, since none is signed.
func (s *Snapshot) Ref() *controlv1.PolicyBundleRef {
	if s == nil {
		return nil
	}
	return proto.CloneOf(s.ref)
}

// ConfirmedAt is when the bundle was last confirmed current: the time handed
// to the Load, or the Install, that made this snapshot.
func (s *Snapshot) ConfirmedAt() time.Time {
	if s == nil {
		return time.Time{}
	}
	return s.confirmedAt
}

// MaxStale is the author's staleness budget.
func (s *Snapshot) MaxStale() time.Duration {
	if s == nil {
		return 0
	}
	return s.maxStale
}

// Serial is the bundle's serial, the rollback check's input.
func (s *Snapshot) Serial() int64 {
	if s == nil {
		return 0
	}
	return s.serial
}

// ReadsExternal reports whether a rule of the bundle reads the external
// decision point's answer.
func (s *Snapshot) ReadsExternal() bool {
	return s != nil && s.program.ReadsExternal()
}

// Evaluate decides one envelope against the bundle's program.
func (s *Snapshot) Evaluate(env *controlv1.ActionEnvelope, in match.Inputs) match.Result {
	var program *match.Program
	if s != nil {
		program = s.program
	}
	return program.Evaluate(env, in)
}
