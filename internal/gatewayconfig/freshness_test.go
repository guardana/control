package gatewayconfig

import (
	"os"
	"strings"
	"testing"
	"time"

	"github.com/guardana/control/internal/brand"
)

// The keys ADR-0038 adds, as the document spells them.
var freshnessLines = []string{
	"  statement_file: policy.statement",
	"  state_dir: floors",
	"  freshness_key_id: f1",
	"  freshness_public_key: BBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBB=",
	"  poll_interval: 5s",
}

// TestAConfigurationOfAnEarlierReleaseIsRefused: a document that names none
// of the freshness keys is refused naming the first of them, and a document
// lacking any one of them is refused naming that one. Nothing defaults: the
// plane would otherwise start with no statement to confirm it.
func TestAConfigurationOfAnEarlierReleaseIsRefused(t *testing.T) {
	earlier := document
	for _, line := range freshnessLines {
		earlier = strings.Replace(earlier, line+"\n", "", 1)
	}
	if earlier == document || strings.Contains(earlier, "statement_file") {
		t.Fatal("the document does not hold the freshness keys this test removes")
	}
	_, err := Load(writeDocument(t, earlier), os.Environ())
	if err == nil || !strings.Contains(err.Error(), "policy.statement_file: no value, and it has no default") {
		t.Fatalf("an earlier release's configuration: %v, want policy.statement_file named", err)
	}
	for _, line := range freshnessLines {
		key := "policy." + strings.TrimSpace(strings.SplitN(line, ":", 2)[0])
		t.Run(key, func(t *testing.T) {
			_, err := Load(writeDocument(t, without(t, line)), os.Environ())
			if err == nil || !strings.Contains(err.Error(), key+": no value, and it has no default") {
				t.Fatalf("without %s: %v, want it named", key, err)
			}
		})
	}
}

func TestTheFreshnessKeysAreRead(t *testing.T) {
	cfg := load(t, write(t, ""))
	p := cfg.Policy
	if p.StatementFile != "policy.statement" || p.StateDir != "floors" || p.FreshnessKeyID != "f1" ||
		p.FreshnessPublicKey != "BBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBB=" || p.PollInterval != 5*time.Second {
		t.Errorf("policy is %+v", p)
	}
}

// TestThePollIntervalIsBounded: at least a second, and shorter than
// policy.max_stale; each bound is shown at the value it bites and the value
// beside it that loads.
func TestThePollIntervalIsBounded(t *testing.T) {
	for _, c := range []struct {
		poll, maxStale string
		wants          string
	}{
		{"999ms", "10m", "policy.poll_interval: 999ms is under 1s"},
		{"1s", "10m", ""},
		{"10m", "10m", "policy.poll_interval: 10m0s is not shorter than policy.max_stale 10m0s"},
		{"599s", "10m", ""},
		{"2s", "2s", "is not shorter than policy.max_stale"},
		{"1s", "1001ms", ""},
		{"0s", "10m", "policy.poll_interval: not a positive duration"},
		{"-1s", "10m", "policy.poll_interval: a duration cannot be negative"},
	} {
		t.Run(c.poll+" under "+c.maxStale, func(t *testing.T) {
			path := write(t, "")
			setEnv(t, "policy.poll_interval", c.poll)
			setEnv(t, "policy.max_stale", c.maxStale)
			_, err := Load(path, os.Environ())
			switch {
			case c.wants == "" && err != nil:
				t.Fatalf("refused: %v", err)
			case c.wants != "" && (err == nil || !strings.Contains(err.Error(), c.wants)):
				t.Fatalf("Load = %v, want %q", err, c.wants)
			}
		})
	}
}

func TestTheFreshnessFieldsSpellTheTable(t *testing.T) {
	byPath := map[string]Field{}
	for _, f := range Fields() {
		byPath[f.Path] = f
	}
	for _, want := range []Field{
		{Path: "policy.statement_file", Kind: "string", Required: true, Env: brand.Env("POLICY_STATEMENT_FILE")},
		{Path: "policy.state_dir", Kind: "string", Required: true, Env: brand.Env("POLICY_STATE_DIR")},
		{Path: "policy.freshness_key_id", Kind: "string", Required: true, Env: brand.Env("POLICY_FRESHNESS_KEY_ID")},
		{Path: "policy.freshness_public_key", Kind: "string", Required: true, Env: brand.Env("POLICY_FRESHNESS_PUBLIC_KEY")},
		{Path: "policy.poll_interval", Kind: "duration", Required: true, Env: brand.Env("POLICY_POLL_INTERVAL")},
	} {
		got := byPath[want.Path]
		if got.Path != want.Path || got.Kind != want.Kind || got.Default != want.Default || got.Required != want.Required || got.Env != want.Env {
			t.Errorf("%s is %+v, want %+v", want.Path, got, want)
		}
	}
}

// TestTheFreshnessKeyIsNeverTheBundleKey: one key named in both roles is
// refused at load, naming both keys, whether by its id or by its public half,
// and whatever white space the public half carries; a key apart in both is
// taken.
func TestTheFreshnessKeyIsNeverTheBundleKey(t *testing.T) {
	const bundlePublic = "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA="
	for _, c := range []struct {
		name  string
		env   map[string]string
		wants []string
	}{
		{"the same key id", map[string]string{"policy.freshness_key_id": "k1"},
			[]string{"policy.freshness_key_id", "policy.key_id"}},
		{"the same public key", map[string]string{"policy.freshness_public_key": bundlePublic},
			[]string{"policy.freshness_public_key", "policy.public_key"}},
		{"the same public key with its newline", map[string]string{"policy.freshness_public_key": bundlePublic + "\n"},
			[]string{"policy.freshness_public_key", "policy.public_key"}},
		{"keys apart", map[string]string{"policy.freshness_key_id": "k2"}, nil},
	} {
		t.Run(c.name, func(t *testing.T) {
			path := write(t, "")
			for key, value := range c.env {
				setEnv(t, key, value)
			}
			_, err := Load(path, os.Environ())
			if c.wants == nil {
				if err != nil {
					t.Fatalf("refused: %v", err)
				}
				return
			}
			if err == nil {
				t.Fatal("one key in both roles loaded")
			}
			for _, key := range c.wants {
				if !strings.Contains(err.Error(), key) {
					t.Errorf("the refusal %q does not name %s", err, key)
				}
			}
		})
	}
}
