package gatewayconfig

import (
	"os"
	"strings"
	"testing"
	"time"
)

// TestTheCallTimeoutHasToBePositive: the smallest positive bound is taken; at
// zero a call would have no bound of the adapter's own, and below zero it
// would also lose an obligation's, so both are refused naming the key.
func TestTheCallTimeoutHasToBePositive(t *testing.T) {
	for _, c := range []struct {
		value string
		want  time.Duration
		ok    bool
	}{{"1ns", time.Nanosecond, true}, {"0s", 0, false}, {"-1ns", 0, false}, {"-30s", 0, false}} {
		path := write(t, "")
		setEnv(t, "upstream.call_timeout", c.value)
		cfg, err := Load(path, os.Environ())
		switch {
		case c.ok && err != nil:
			t.Errorf("upstream.call_timeout=%s was refused: %v", c.value, err)
		case c.ok && cfg.Upstream.CallTimeout != c.want:
			t.Errorf("upstream.call_timeout=%s read as %v, want %v", c.value, cfg.Upstream.CallTimeout, c.want)
		case !c.ok && err == nil:
			t.Errorf("upstream.call_timeout=%s was accepted", c.value)
		case !c.ok && !strings.Contains(err.Error(), "upstream.call_timeout"):
			t.Errorf("the refusal does not name the key: %v", err)
		}
	}
}

// TestTheOtherTimeoutsRefuseWhatTheirClientsRefuse: a negative bound is
// refused naming its key, for every timeout. Zero keeps its meaning: the
// adapter's own bound for upstream.list_timeout, and for pdp.timeout and
// export.timeout a refusal, since neither client takes a bound that is not
// positive.
func TestTheOtherTimeoutsRefuseWhatTheirClientsRefuse(t *testing.T) {
	for _, c := range []struct {
		key, file, value string
		ok               bool
	}{
		{"upstream.list_timeout", "", "1ns", true},
		{"upstream.list_timeout", "", "0s", true},
		{"upstream.list_timeout", "", "-1ns", false},
		{"pdp.timeout", pdpBlock, "1ns", true},
		{"pdp.timeout", pdpBlock, "0s", false},
		{"pdp.timeout", pdpBlock, "-1ns", false},
		{"export.timeout", "", "1ns", true},
		{"export.timeout", "", "0s", false},
		{"export.timeout", "", "-1ns", false},
	} {
		t.Run(c.key+"="+c.value, func(t *testing.T) {
			path := write(t, c.file)
			setEnv(t, c.key, c.value)
			_, err := Load(path, os.Environ())
			switch {
			case c.ok && err != nil:
				t.Errorf("refused: %v", err)
			case !c.ok && err == nil:
				t.Error("accepted")
			case !c.ok && !strings.HasPrefix(err.Error(), c.key+": "):
				t.Errorf("the refusal does not start with the key: %v", err)
			}
		})
	}
}
