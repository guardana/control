package main

import (
	"context"
	"fmt"

	"github.com/guardana/control/internal/policy"
	"github.com/guardana/control/internal/policystate"
	"github.com/guardana/control/internal/policywatch"
)

// floorReader is the floor directory as doctor holds it: read, then closed.
type floorReader interface {
	policy.FloorStore
	Close() error
}

// openFloors opens the floor directory the policy check reads.
var openFloors = func(dir string) (floorReader, error) {
	return policystate.Open(dir, policystate.KindPlane)
}

// policy judges the bundle, the statement and the floor as a start would,
// and raises nothing: the floor is read, its bytes and its time left as they
// are. A bundle the start would refuse, or install unconfirmed, fails, as does
// a statement whose budget has run out. A floor directory that does not close
// is not a pass either.
func (d *examination) policy(ctx context.Context) (string, string, string) {
	store, err := openFloors(d.cfg.Resolve(d.cfg.Policy.StateDir))
	if err != nil {
		return verdictFail, "policy", "policy.state_dir: " + err.Error()
	}
	verdict, name, found := d.judgePolicy(ctx, store)
	closeErr := store.Close()
	switch {
	case closeErr == nil:
		return verdict, name, found
	case verdict == verdictOK:
		return verdictUnknown, name, "the floor directory was read and did not close: " + closeErr.Error()
	}
	return verdict, name, found + "; and the floor directory did not close: " + closeErr.Error()
}

func (d *examination) judgePolicy(ctx context.Context, store policy.FloorStore) (string, string, string) {
	o, err := policyOptions(d.cfg, nil, store, nil)
	if err != nil {
		return verdictFail, "policy", err.Error()
	}
	ctx, cancel := context.WithTimeout(ctx, floorLockWait)
	defer cancel()
	rep, err := policywatch.Check(ctx, o)
	if err != nil {
		return verdictFail, "policy", keyedRefusal(d.cfg, err).Error()
	}
	ref := rep.Bundle.Ref()
	return verdictOK, "policy", fmt.Sprintf("bundle %s version %s serial %d verifies under key %s, digest %s, staleness budget %s; "+
		"a statement under key %s issued %s confirms it until %s; the floor holds %s; %s",
		ref.GetBundleId(), ref.GetVersion(), rep.Bundle.Serial(), d.cfg.Policy.KeyID, ref.GetDigest(), rep.Bundle.MaxStale(),
		d.cfg.Policy.FreshnessKeyID, policy.FormatIssuedAt(rep.Statement.IssuedAt()), policy.FormatIssuedAt(rep.Expires),
		floorText(rep.Floor), failOpenReadText(d.cfg.Policy.FailOpenRead))
}

// failOpenReadText names the risk setting a read runs under while the policy
// is unavailable, on either side.
func failOpenReadText(on bool) string {
	if on {
		return "policy.fail_open_read true, so a read runs while the policy is unavailable"
	}
	return "policy.fail_open_read false, so a read fails closed while the policy is unavailable"
}
