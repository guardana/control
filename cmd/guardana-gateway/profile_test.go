package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"testing"

	"github.com/guardana/control/internal/gatewayconfig"
)

// TestTheFilesystemProfileIsAConfigurationThisBuildReads: the profile the
// connect guide ships loads, serves stateful HTTP, names the approval-for-writes
// pack's bundle, and classifies the fourteen tools of the server version it
// names, each once, on its one upstream. Whether the fingerprints still match
// that server takes the server itself, which this test does not start.
func TestTheFilesystemProfileIsAConfigurationThisBuildReads(t *testing.T) {
	root := filepath.Join("..", "..")
	cfg, err := gatewayconfig.Load(filepath.Join(root, "examples", "connect-a-filesystem-server", "plane.yaml"), nil)
	if err != nil {
		t.Fatalf("the profile does not load: %v", err)
	}
	bundle := packBundleID(t, filepath.Join(root, "examples", "starter-packs", "approval-for-writes", "policy.json"))
	if cfg.Listener.Kind != "stateful_http" || cfg.Policy.BundleID != bundle || len(cfg.Upstreams) != 1 {
		t.Fatalf("listener %q, bundle %q (the pack's is %q), %d upstream(s)", cfg.Listener.Kind, cfg.Policy.BundleID, bundle, len(cfg.Upstreams))
	}
	effects := profileEffects(t, cfg.Overrides, cfg.Upstreams[0].Name)
	want := map[string]string{
		"read_file": "READ", "read_text_file": "READ", "read_media_file": "READ", "read_multiple_files": "READ",
		"list_directory": "READ", "list_directory_with_sizes": "READ", "directory_tree": "READ", "search_files": "READ",
		"get_file_info": "READ", "list_allowed_directories": "READ",
		"write_file": "WRITE", "edit_file": "WRITE", "create_directory": "WRITE", "move_file": "WRITE",
	}
	if len(effects) != len(want) {
		t.Errorf("%d tools classified, want %d", len(effects), len(want))
	}
	for tool, effect := range want {
		if effects[tool] != effect {
			t.Errorf("%s is classified %q, want %s", tool, effects[tool], effect)
		}
	}
}

// packBundleID is the bundle id a starter pack's document names.
func packBundleID(t *testing.T, path string) string {
	t.Helper()
	raw, err := os.ReadFile(path) //nolint:gosec // G304: a pack this repository ships
	if err != nil {
		t.Fatal(err)
	}
	var pack struct {
		Bundle struct {
			ID string `json:"id"`
		} `json:"bundle"`
	}
	if err := json.Unmarshal(raw, &pack); err != nil || pack.Bundle.ID == "" {
		t.Fatalf("reading the pack's bundle id: %v", err)
	}
	return pack.Bundle.ID
}

// profileEffects maps each overridden tool to its effect, and fails an override
// that names another upstream, repeats a tool or carries no fingerprint.
func profileEffects(t *testing.T, overrides []gatewayconfig.OverrideConfig, upstream string) map[string]string {
	t.Helper()
	fingerprint := regexp.MustCompile(`^sha256:[0-9a-f]{64}$`)
	effects := map[string]string{}
	for _, o := range overrides {
		if _, twice := effects[o.Tool]; twice || o.Upstream != upstream || !fingerprint.MatchString(o.Fingerprint) {
			t.Errorf("override %+v", o)
		}
		effects[o.Tool] = o.Effect
	}
	return effects
}
