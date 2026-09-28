package approval_test

import (
	"testing"

	"github.com/guardana/control/internal/core/approval"
)

// Computed outside this repository's code, from testdata/digest/README.md, for
// the minimal golden's envelope under bundle1.
const (
	digestOfSevenTenths  approval.ActionDigest = "sha256:d68b5b16738cc8eb8b74a026fa75f83ad546f3ca9a6d136424e5aef0dbe8ed40"
	bindingOfSevenTenths approval.Binding      = "sha256:68d492613382b94910eb5324def4d0a72b3fddea4ef3699c1dbd0d4357442db6"
	bindingOfNextDouble  approval.Binding      = "sha256:748dc328f20ae511fb201fc020c1d1f89ace0c5081992787a9ea676b79aab144"
)

// TestAnApprovalOfAFractionCoversItsValueOnly: an approval bound over 0.7
// matches every spelling of 0.7 and not the next double up, which differs
// from it in the sixteenth decimal place.
func TestAnApprovalOfAFractionCoversItsValueOnly(t *testing.T) {
	g := loadGolden(t, "minimal")
	bind := func(args string) (approval.ActionDigest, approval.Binding) {
		t.Helper()
		digest, binding, err := approval.Bind(g.env, []byte(args), bundle1)
		if err != nil {
			t.Fatalf("Bind(%s): %v", args, err)
		}
		return digest, binding
	}
	digest, approved := bind(`{"t":0.7}`)
	if digest != digestOfSevenTenths || approved != bindingOfSevenTenths {
		t.Fatalf("Bind(0.7) = %s, %s; want %s, %s", digest, approved, digestOfSevenTenths, bindingOfSevenTenths)
	}
	for _, spelling := range []string{`{"t":0.70}`, `{ "t" : 7e-1 }`, `{"t":0.700000000000000000}`} {
		if _, got := bind(spelling); got != approved {
			t.Errorf("%s binds to %s; the approval of 0.7 is %s", spelling, got, approved)
		}
	}
	if _, got := bind(`{"t":0.7000000000000001}`); got != bindingOfNextDouble || got == approved {
		t.Errorf("0.7000000000000001 binds to %s, want %s and never the approval of 0.7", got, bindingOfNextDouble)
	}
}
