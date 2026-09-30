package contract_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/guardana/control/pkg/contract"
)

// TestTheSharedVersionTable holds Validate to the version table that
// internal/evidence holds its event check to, so the envelope rule and the
// event rule cannot drift apart without one of the two tests failing.
func TestTheSharedVersionTable(t *testing.T) {
	for _, tc := range sharedVersionTable(t) {
		env := valid()
		env.SchemaVersion = tc.Version
		err := contract.Validate(env)
		switch {
		case tc.Admitted && err != nil:
			t.Errorf("version %q: Validate = %v, want it admitted", tc.Version, err)
		case !tc.Admitted:
			assertRefused(t, err, contract.ErrUnsupportedSchema, "schema_version")
		}
	}
}

type versionCase struct {
	Version  string `json:"version"`
	Admitted bool   `json:"admitted"`
}

// sharedVersionTable reads the table and refuses one that could not tell a
// rule from its absence: it must admit a higher minor and refuse another major
// and an absent version.
func sharedVersionTable(t *testing.T) []versionCase {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("..", "..", "testdata", "contracts", "schema_versions.json"))
	if err != nil {
		t.Fatal(err)
	}
	var table []versionCase
	if err := json.Unmarshal(raw, &table); err != nil {
		t.Fatal(err)
	}
	must := map[versionCase]bool{{"1.3", true}: false, {"2.0", false}: false, {"", false}: false}
	for _, tc := range table {
		if _, ok := must[tc]; ok {
			must[tc] = true
		}
	}
	for tc, found := range must {
		if !found {
			t.Fatalf("the version table lacks %+v", tc)
		}
	}
	return table
}
