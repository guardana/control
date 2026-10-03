package main

import (
	"context"
	"fmt"

	"github.com/guardana/control/internal/policy"
	"github.com/guardana/control/internal/policystate"
	"github.com/guardana/control/internal/policywatch"
)

// policy judges the bundle, the statement and the floor as a start would,
// and raises nothing: the floor is read, its bytes and its time left as they
// are. A bundle the start would refuse, or install unconfirmed, fails, as does
// a statement whose budget has run out.
func (d *examination) policy(ctx context.Context) (string, string, string) {
	store, err := policystate.Open(d.cfg.Resolve(d.cfg.Policy.StateDir), policystate.KindPlane)
	if err != nil {
		return verdictFail, "policy", "policy.state_dir: " + err.Error()
	}
	defer func() {
		if err := store.Close(); err != nil {
			writeLine(d.out, "       the floor directory did not close cleanly: "+oneLine(err.Error()))
		}
	}()
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
