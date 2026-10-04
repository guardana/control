package observe_test

import (
	"testing"

	observev1 "github.com/guardana/control/api/gen/go/guardana/control/observe/v1alpha1"
	"github.com/guardana/control/internal/observe"
)

func TestTrustOf(t *testing.T) {
	cases := map[observev1.Trust]observev1.Trust{
		0:  observev1.Trust_TRUST_SELF_REPORTED,
		1:  observev1.Trust_TRUST_SELF_REPORTED,
		2:  observev1.Trust_TRUST_PLATFORM,
		3:  observev1.Trust_TRUST_INDEPENDENT,
		4:  observev1.Trust_TRUST_SELF_REPORTED,
		-1: observev1.Trust_TRUST_SELF_REPORTED,
	}
	for in, want := range cases {
		if got := observe.TrustOf(in); got != want {
			t.Errorf("TrustOf(%d) = %v, want %v", in, got, want)
		}
	}
}

func TestBasisOf(t *testing.T) {
	cases := map[observev1.Basis]observev1.Basis{
		0:  observev1.Basis_BASIS_NONE,
		1:  observev1.Basis_BASIS_NONE,
		2:  observev1.Basis_BASIS_CLAIMED,
		3:  observev1.Basis_BASIS_JOINED,
		4:  observev1.Basis_BASIS_BOUND,
		5:  observev1.Basis_BASIS_NONE,
		99: observev1.Basis_BASIS_NONE,
	}
	for in, want := range cases {
		if got := observe.BasisOf(in); got != want {
			t.Errorf("BasisOf(%d) = %v, want %v", in, got, want)
		}
	}
}

func TestStageOf(t *testing.T) {
	cases := map[observev1.Stage]observev1.Stage{
		0:  observev1.Stage_STAGE_UNSPECIFIED,
		1:  observev1.Stage_STAGE_PROPOSED,
		2:  observev1.Stage_STAGE_STARTED,
		3:  observev1.Stage_STAGE_COMPLETED,
		4:  observev1.Stage_STAGE_FAILED,
		5:  observev1.Stage_STAGE_INDIRECT,
		6:  observev1.Stage_STAGE_UNSPECIFIED,
		-3: observev1.Stage_STAGE_UNSPECIFIED,
	}
	for in, want := range cases {
		if got := observe.StageOf(in); got != want {
			t.Errorf("StageOf(%d) = %v, want %v", in, got, want)
		}
	}
}

func TestSamplingOf(t *testing.T) {
	cases := map[observev1.Sampling]observev1.Sampling{
		0: observev1.Sampling_SAMPLING_PARTIAL,
		1: observev1.Sampling_SAMPLING_COMPLETE,
		2: observev1.Sampling_SAMPLING_PARTIAL,
		3: observev1.Sampling_SAMPLING_PARTIAL,
	}
	for in, want := range cases {
		if got := observe.SamplingOf(in); got != want {
			t.Errorf("SamplingOf(%d) = %v, want %v", in, got, want)
		}
	}
}
