package evidence_test

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	controlv1 "github.com/guardana/control/api/gen/go/guardana/control/v1"
	"github.com/guardana/control/internal/evidence"
	"github.com/guardana/control/pkg/contract"
)

// TestCheckEventVersionFollowsTheSharedTable holds the event check to the
// table pkg/contract holds envelopes to: the same versions admitted and the
// same refused.
func TestCheckEventVersionFollowsTheSharedTable(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("..", "..", "testdata", "contracts", "schema_versions.json"))
	if err != nil {
		t.Fatal(err)
	}
	var table []struct {
		Version  string `json:"version"`
		Admitted bool   `json:"admitted"`
	}
	if err := json.Unmarshal(raw, &table); err != nil {
		t.Fatal(err)
	}
	var admitted, refused int
	for _, tc := range table {
		err := evidence.CheckEventVersion(&controlv1.Event{EventId: "e1", SchemaVersion: tc.Version})
		switch {
		case tc.Admitted && err != nil:
			t.Errorf("version %q: CheckEventVersion = %v, want it admitted", tc.Version, err)
		case !tc.Admitted && !errors.Is(err, contract.ErrUnsupportedSchema):
			t.Errorf("version %q: CheckEventVersion = %v, want ErrUnsupportedSchema", tc.Version, err)
		}
		if tc.Admitted {
			admitted++
		} else {
			refused++
		}
	}
	if admitted < 3 || refused < 10 {
		t.Fatalf("the table holds %d admitted and %d refused versions, too few to pin the rule", admitted, refused)
	}
}

// TestCheckEventVersionRefusesNoEvent: a nil event carries no version, and no
// version is not the current one.
func TestCheckEventVersionRefusesNoEvent(t *testing.T) {
	if err := evidence.CheckEventVersion(nil); !errors.Is(err, contract.ErrUnsupportedSchema) {
		t.Errorf("CheckEventVersion(nil) = %v, want ErrUnsupportedSchema", err)
	}
}

// TestTheCodecStillDecodesAnEventOfAnotherMajor: the check is apart from the
// codec, which carries a record it does not judge.
func TestTheCodecStillDecodesAnEventOfAnotherMajor(t *testing.T) {
	events, err := evidence.DecodeJSONL(strings.NewReader(`{"eventId":"e1","schemaVersion":"2.0"}`+"\n"), 1)
	if err != nil || len(events) != 1 {
		t.Fatalf("DecodeJSONL = %v, %v; want the one event", events, err)
	}
	if err := evidence.CheckEventVersion(events[0]); !errors.Is(err, contract.ErrUnsupportedSchema) {
		t.Errorf("CheckEventVersion = %v, want ErrUnsupportedSchema", err)
	}
}
