package policywatch

import (
	"bytes"
	"errors"
	"fmt"
	"io/fs"
	"path/filepath"
	"time"

	"google.golang.org/protobuf/proto"

	controlv1 "github.com/guardana/control/api/gen/go/guardana/control/v1"
	ondisk "github.com/guardana/control/internal/files"
	"github.com/guardana/control/internal/policy"
	"github.com/guardana/control/internal/policykey"
)

// MaxBundleFileBytes bounds the bundle file read. It sits above the 1 MiB
// document bound the loader enforces, so an over-long bundle meets that
// refusal and no second bound is kept here.
const MaxBundleFileBytes = 2 << 20

// diskBundle is a bundle file's bytes and what they hold: the bundle and its
// snapshot, loaded with no confirmation, or why they hold none. Every part of
// it is a verdict no clock and no floor can change.
type diskBundle struct {
	raw []byte
	// keyID is the key id the file names, read before anything verified it.
	keyID string
	msg   *controlv1.PolicyBundle
	snap  *policy.Snapshot
	cause Cause
	err   error
}

// diskStatement is a statement file's bytes and the verified statement they
// hold, or why they hold none.
type diskStatement struct {
	raw   []byte
	st    policy.Statement
	cause Cause
	err   error
}

// readBundle reads the bundle file and judges its bytes, unless they are the
// bytes kept, whose verdict stands. A read that fails is never kept.
func readBundle(o Options, kept *diskBundle) *diskBundle {
	raw, err := ondisk.ReadRegular(filepath.Clean(o.BundlePath), MaxBundleFileBytes, 0)
	if err != nil {
		return &diskBundle{cause: CauseBundleUnreadable, err: err}
	}
	if kept != nil && kept.raw != nil && bytes.Equal(raw, kept.raw) {
		return kept
	}
	out := &diskBundle{raw: raw}
	var b controlv1.PolicyBundle
	if err := proto.Unmarshal(raw, &b); err != nil {
		out.cause, out.err = CauseBundleInvalid, fmt.Errorf("not a serialized policy bundle: %w", err)
		return out
	}
	out.keyID = b.GetKeyId()
	snap, err := policy.Load(&b, o.BundleKeys, time.Time{})
	switch {
	case err != nil:
		out.cause, out.err = CauseBundleInvalid, err
	case snap.Ref().GetBundleId() != o.BundleID:
		out.cause, out.err = CauseBundleID, fmt.Errorf("%w: %q", policy.ErrBundlePin, snap.Ref().GetBundleId())
	default:
		out.msg, out.snap = &b, snap
	}
	return out
}

// readStatement reads the statement file and verifies its bytes, unless they
// are the bytes kept, whose verdict stands. A read that fails is never kept.
func readStatement(o Options, kept *diskStatement) *diskStatement {
	raw, err := ondisk.ReadRegular(filepath.Clean(o.StatementPath), policykey.MaxStatementFileBytes, 0)
	switch {
	case errors.Is(err, fs.ErrNotExist):
		return &diskStatement{cause: CauseStatementMissing, err: fmt.Errorf("%w: %w", policy.ErrStatementMissing, err)}
	case err != nil:
		return &diskStatement{cause: CauseStatementUnreadable, err: err}
	}
	if kept != nil && kept.raw != nil && bytes.Equal(raw, kept.raw) {
		return kept
	}
	out := &diskStatement{raw: raw}
	env, err := policykey.ParseStatement(raw)
	if err == nil {
		out.st, err = policy.VerifyStatement(env, o.FreshnessKeys)
	}
	if err != nil {
		out.cause, out.err = CauseStatementInvalid, err
	}
	return out
}

// binds reports whether st names snap's bundle id, serial and digest.
func binds(st policy.Statement, snap *policy.Snapshot) bool {
	ref := snap.Ref()
	return st.BundleID() == ref.GetBundleId() && st.Serial() == snap.Serial() && st.Digest() == ref.GetDigest()
}

// expired reports whether st's budget for snap had run out at now.
func expired(st policy.Statement, snap *policy.Snapshot, operator time.Duration, now time.Time) bool {
	return now.After(ExpiresAt(st.IssuedAt(), snap, operator))
}

// expiry is the refusal of st once its budget for snap ran out.
func expiry(st policy.Statement, snap *policy.Snapshot, operator time.Duration) error {
	return fmt.Errorf("%w: issued %s, expired at %s", ErrStatementExpired,
		policy.FormatIssuedAt(st.IssuedAt()), policy.FormatIssuedAt(ExpiresAt(st.IssuedAt(), snap, operator)))
}

// outlastsPoll refuses a bundle whose budget, with the operator's, is not
// longer than the poll interval: no statement could keep it fresh.
func outlastsPoll(snap *policy.Snapshot, o Options) error {
	if budget := Budget(snap, o.MaxStale); budget <= o.Interval {
		return fmt.Errorf("the budget of %v is not longer than the poll interval %v", budget, o.Interval)
	}
	return nil
}
