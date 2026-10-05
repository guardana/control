package findinglog

import (
	"errors"
	"strings"

	findingv1alpha1 "github.com/guardana/control/api/gen/go/guardana/control/finding/v1alpha1"
	controlv1 "github.com/guardana/control/api/gen/go/guardana/control/v1"
)

// SchemaVersion is the one version of both record kinds this build writes
// and reads.
const SchemaVersion = "0.1"

// checkRecord holds a record to ADR-0045: exactly one member, of version
// SchemaVersion, whose and which run it is. Its errors name a field and
// quote nothing.
func checkRecord(r *findingv1alpha1.Record) error {
	if f := r.GetFindingRecord(); f != nil {
		return checkFinding(f)
	}
	if rep := r.GetSuperviseReport(); rep != nil {
		return checkReport(rep)
	}
	return errors.New("no record set")
}

func checkOrigin(version, tenant, project string) error {
	switch {
	case version != SchemaVersion:
		return errors.New("schema_version: not " + SchemaVersion)
	case tenant == "":
		return errors.New("tenant_id: required")
	case project == "":
		return errors.New("project_id: required")
	}
	return nil
}

func checkReport(r *findingv1alpha1.SuperviseReport) error {
	if err := checkOrigin(r.GetSchemaVersion(), r.GetTenantId(), r.GetProjectId()); err != nil {
		return err
	}
	if !prefixedHex(r.GetRunId(), "run-") {
		return errors.New(`run_id: not "run-" and 32 lowercase hex digits`)
	}
	return nil
}

// checkFinding holds the finding to what supervision alone produces: a
// deterministic finding whose typed references say what it rests on once,
// so the fields that would say it again stay empty.
func checkFinding(r *findingv1alpha1.FindingRecord) error {
	if err := checkOrigin(r.GetSchemaVersion(), r.GetTenantId(), r.GetProjectId()); err != nil {
		return err
	}
	if !known(int32(r.GetEscalation()), findingv1alpha1.Escalation_name) {
		return errors.New("escalation: neither inform nor alert")
	}
	f := r.GetFinding()
	switch {
	case f == nil:
		return errors.New("finding: required")
	case !prefixedHex(f.GetFindingId(), "fnd-"):
		return errors.New(`finding.finding_id: not "fnd-" and 32 lowercase hex digits`)
	case f.GetSource() != controlv1.FindingSource_FINDING_SOURCE_DETERMINISTIC:
		return errors.New("finding.source: not deterministic")
	case !known(int32(f.GetVerdict()), controlv1.FindingVerdict_name):
		return errors.New("finding.verdict: not set")
	case !known(int32(f.GetSeverity()), controlv1.FindingSeverity_name):
		return errors.New("finding.severity: not set")
	}
	return checkSaidOnce(f)
}

func checkSaidOnce(f *controlv1.Finding) error {
	switch {
	case len(f.GetEvidenceRefs()) != 0:
		return errors.New("finding.evidence_refs: set beside the typed references")
	case f.GetRequestId() != "":
		return errors.New("finding.request_id: set beside the typed references")
	case f.GetRecommendedAction() != "":
		return errors.New("finding.recommended_action: set")
	case len(f.GetFrameworkMappings()) != 0:
		return errors.New("finding.framework_mappings: set")
	}
	return nil
}

// known reports whether n is a value of its enum other than the unspecified
// zero.
func known(n int32, names map[int32]string) bool {
	_, ok := names[n]
	return ok && n != 0
}

// prefixedHex reports whether s is prefix and 32 lowercase hex digits.
func prefixedHex(s, prefix string) bool {
	hex, ok := strings.CutPrefix(s, prefix)
	if !ok || len(hex) != 32 {
		return false
	}
	for _, c := range []byte(hex) {
		if (c < '0' || c > '9') && (c < 'a' || c > 'f') {
			return false
		}
	}
	return true
}
