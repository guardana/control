package reaction

import (
	"context"
	"errors"
	"testing"
)

// TestNoFormatIsNoneOfTheFormats: every switch on a route's format refuses
// FormatNone and a value past the last format, which no reader makes, rather
// than take either as one it knows.
func TestNoFormatIsNoneOfTheFormats(t *testing.T) {
	p := Plane{ID: "plane-a", Tenant: "t", Project: "p", RouteFile: "/r", ListFile: "/l"}
	for _, f := range []RouteFormat{FormatNone, FormatV1Alpha3 + 1} {
		if err := samePlane(p, binding{format: f, plane: "plane-a"}); !errors.Is(err, ErrOtherPlane) {
			t.Errorf("format %d naming the plane: %v, want %v", f, err, ErrOtherPlane)
		}
		if err := samePlane(p, binding{format: f}); !errors.Is(err, ErrOtherPlane) {
			t.Errorf("format %d naming no plane: %v, want %v", f, err, ErrOtherPlane)
		}
		r := Route{format: f, digest: "d", voucher: fakeInternalVoucher{p}}
		if err := formatJudges(r, true); !errors.Is(err, ErrListRoute) {
			t.Errorf("format %d judged as a carry's source: %v, want %v", f, err, ErrListRoute)
		}
		if _, err := listVersion(f); !errors.Is(err, ErrListRoute) {
			t.Errorf("format %d named a list version: %v", f, err)
		}
		if err := vouchAgain(context.Background(), r, nil); !errors.Is(err, ErrListRoute) {
			t.Errorf("format %d vouched again: %v, want %v", f, err, ErrListRoute)
		}
	}
}

type fakeInternalVoucher struct{ p Plane }

func (v fakeInternalVoucher) Plane() Plane { return v.p }

func (fakeInternalVoucher) Vouch(SignedKind, Envelope) (Signer, error) {
	return Signer{Subject: "alice", KeyID: "k"}, nil
}
