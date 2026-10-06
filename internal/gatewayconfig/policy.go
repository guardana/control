package gatewayconfig

import (
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/guardana/control/internal/policykey"
)

// minPolicyPoll is the floor of policy.poll_interval: each poll reads two
// files and may take the floor directory's lock.
const minPolicyPoll = time.Second

// checkPolicy bounds the poll interval: at least minPolicyPoll, and shorter
// than policy.max_stale, since a confirmation has to be read again before the
// budget it opens runs out; and refuses one key named as the bundle's and as
// the freshness key. A bundle's own budget is held to the interval
// when it is installed.
func (c *Config) checkPolicy() error {
	switch {
	case c.Policy.FreshnessKeyID == c.Policy.KeyID:
		return fmt.Errorf("policy.freshness_key_id: %s is policy.key_id too; the freshness key is never the bundle's key (ADR-0038)",
			quoteValue(c.Policy.FreshnessKeyID))
	case samePublicKey(c.Policy.FreshnessPublicKey, c.Policy.PublicKey):
		return errors.New("policy.freshness_public_key: it is policy.public_key too; the freshness key is never the bundle's key (ADR-0038)")
	case c.Policy.PollInterval < minPolicyPoll:
		return fmt.Errorf("policy.poll_interval: %v is under %v", c.Policy.PollInterval, minPolicyPoll)
	case c.Policy.PollInterval >= c.Policy.MaxStale:
		return fmt.Errorf("policy.poll_interval: %v is not shorter than policy.max_stale %v", c.Policy.PollInterval, c.Policy.MaxStale)
	}
	return nil
}

// samePublicKey reports whether a and b name one public key: one key but
// for its sign bit once both read as keys, or the same text where either does
// not, which the plane's start then refuses on its own.
func samePublicKey(a, b string) bool {
	keyA, errA := policykey.ParsePublic(a)
	keyB, errB := policykey.ParsePublic(b)
	if errA == nil && errB == nil {
		return policykey.SameKey(keyA, keyB)
	}
	return strings.TrimSpace(a) == strings.TrimSpace(b)
}
