package main

import (
	"crypto/ed25519"
	"errors"
	"fmt"
	"os"
	"slices"
	"strings"

	"github.com/guardana/control/internal/brand"
	"github.com/guardana/control/internal/gatewayconfig"
	"github.com/guardana/control/internal/loopback"
	"github.com/guardana/control/internal/policykey"
)

// devSet is every key dev gives a value of its own. Every key under
// approvals is dev's too, set or left at its default, so a demo that tunes
// the store is refused rather than half obeyed.
var devSet = []string{
	"listener.address",
	"health.address",
	"policy.bundle_file",
	"policy.key_id",
	"policy.public_key",
	"approvals.provider",
	"approvals.dir",
	"approvals.hold_journal_dir",
	"pause.file",
	"evidence.dir",
	"export.endpoint",
	"export.allow_plaintext",
}

// devOwned is devSet and every other key under approvals.
func devOwned() []string {
	owned := slices.Clone(devSet)
	for _, f := range gatewayconfig.Fields() {
		if strings.HasPrefix(f.Path, "approvals.") && !slices.Contains(owned, f.Path) {
			owned = append(owned, f.Path)
		}
	}
	return owned
}

// refuseEnvironment names the first variable under the product's prefix. Dev
// reads none of them, since the loader gets dev's own values alone; one left
// set is refused so that nobody believes it applies.
func refuseEnvironment(environ []string) error {
	for _, kv := range environ {
		name, _, _ := strings.Cut(kv, "=")
		if strings.HasPrefix(name, brand.EnvPrefix) {
			return fmt.Errorf("%s is set; dev sets the plane's configuration itself and reads no %s variable, so unset it", name, brand.EnvPrefix+"*")
		}
	}
	return nil
}

// devLayer is dev's own values, bound over the demo's file through the
// loader's environment, the one binder a variable goes through.
type devLayer map[string]string

// newLayer is dev's values for a plane whose state is under st, which
// listens at listen and health and exports to collector, a host:port.
func newLayer(st devState, key ed25519.PrivateKey, listen, health, collector string) devLayer {
	pub, _ := key.Public().(ed25519.PublicKey)
	return devLayer{
		"listener.address":           listen,
		"health.address":             health,
		"policy.bundle_file":         st.path(stateBundle),
		"policy.key_id":              policykey.KeyID(pub),
		"policy.public_key":          policykey.FormatPublic(pub),
		"approvals.provider":         gatewayconfig.ProviderFile,
		"approvals.dir":              st.path(stateApprovals),
		"approvals.hold_journal_dir": st.path(stateHolds),
		"pause.file":                 st.path(statePause),
		"evidence.dir":               st.path(stateSpool),
		"export.endpoint":            "http://" + collector + logsPath,
		"export.allow_plaintext":     "true",
	}
}

// environ is the layer as variables, without the keys in except.
func (l devLayer) environ(except ...string) []string {
	out := make([]string, 0, len(l))
	for _, key := range devSet {
		if value, ok := l[key]; ok && !slices.Contains(except, key) {
			out = append(out, brand.Env(gatewayconfig.EnvName(key))+"="+value)
		}
	}
	return out
}

// resolveDemo loads the demo's configuration under the layer and refuses one
// that sets a key dev owns, a stdio listener, and any address dev reaches
// that is not a loopback IP literal. It reads the file and creates nothing.
func resolveDemo(path string, l devLayer) (*gatewayconfig.Config, error) {
	if probe, err := gatewayconfig.Load(path, l.environ("listener.address")); err == nil && probe.Listener.Kind == "stdio" {
		return nil, errors.New("listener.kind is stdio; a dev plane serves its agents over HTTP on the loopback")
	}
	cfg, err := gatewayconfig.Load(path, l.environ())
	if err != nil {
		return nil, err
	}
	sources := cfg.Sources()
	for _, key := range devOwned() {
		set := false
		if slices.Contains(devSet, key) {
			set = fileSets(path, l, key)
		} else {
			_, set = sources[key]
		}
		if set {
			return nil, fmt.Errorf("%s: %s sets it, and dev owns it; remove it from the demo's configuration", key, path)
		}
	}
	if err := devAddresses(cfg); err != nil {
		return nil, err
	}
	return cfg, nil
}

// fileSets reports whether the file at path sets key, which the layer sets
// too and so hides from the loaded configuration's sources. The file is
// loaded without the layer's key: a load that stands names the key's source
// if the file set it. A load that fails is loaded once more with the key's
// default set through the layer, and a file that did not set the key fails
// both with the same refusal, since both loads then hold the same values.
func fileSets(path string, l devLayer, key string) bool {
	without, err := gatewayconfig.Load(path, l.environ(key))
	if err == nil {
		_, set := without.Sources()[key]
		return set
	}
	pinned := append(l.environ(key), brand.Env(gatewayconfig.EnvName(key))+"="+defaultOf(key))
	_, again := gatewayconfig.Load(path, pinned)
	return again == nil || again.Error() != err.Error()
}

func defaultOf(key string) string {
	for _, f := range gatewayconfig.Fields() {
		if f.Path == key {
			return f.Default
		}
	}
	return ""
}

// devAddresses refuses a listener, a health address, a collector, an
// upstream's endpoint or a decision point's URL that is not a loopback IP
// literal. An upstream run as a command, and a decision point key left
// empty, name no address.
func devAddresses(cfg *gatewayconfig.Config) error {
	for _, a := range [][2]string{
		{"listener.address", cfg.Listener.Address},
		{"health.address", cfg.Health.Address},
	} {
		if err := loopback.Check(a[1]); err != nil {
			return fmt.Errorf("%s: %w", a[0], err)
		}
	}
	if err := loopback.CheckURL(cfg.Export.Endpoint); err != nil {
		return fmt.Errorf("export.endpoint: %w", err)
	}
	optional := [][2]string{
		{"pdp.identifier", cfg.PDP.Identifier},
		{"pdp.evaluation_endpoint", cfg.PDP.EvaluationEndpoint},
		{"pdp.proxy", cfg.PDP.Proxy},
	}
	for i, up := range cfg.Upstreams {
		optional = append(optional, [2]string{fmt.Sprintf("upstreams.%d.endpoint", i), up.Endpoint})
	}
	for _, a := range optional {
		if a[1] == "" {
			continue
		}
		if err := loopback.CheckURL(a[1]); err != nil {
			return fmt.Errorf("%s: %w", a[0], err)
		}
	}
	return nil
}

// writeSettings writes every scalar key the plane resolved, one `key: value`
// line each, credentials withheld as the loader withholds them.
func writeSettings(path string, cfg *gatewayconfig.Config) error {
	var b strings.Builder
	for _, s := range cfg.Settings() {
		b.WriteString(s.Path + ": " + oneLine(s.Value) + "\n")
	}
	return os.WriteFile(path, []byte(b.String()), 0o600)
}
