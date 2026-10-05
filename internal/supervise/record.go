package supervise

import (
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"

	findingv1alpha1 "github.com/guardana/control/api/gen/go/guardana/control/finding/v1alpha1"
	controlv1 "github.com/guardana/control/api/gen/go/guardana/control/v1"
)

const (
	confirmed     = controlv1.FindingVerdict_FINDING_VERDICT_CONFIRMED
	suspected     = controlv1.FindingVerdict_FINDING_VERDICT_SUSPECTED
	indeterminate = controlv1.FindingVerdict_FINDING_VERDICT_INDETERMINATE
)

// ref is one record a finding rests on, with the strongest verdict it can
// carry: a plane event in a coherent trail can confirm, an observation can
// only suggest, a record in doubt can do neither.
type ref struct {
	r *findingv1alpha1.Reference
	v controlv1.FindingVerdict
}

// weaker is the weaker of two verdicts; anything not confirmed or suspected
// reads as indeterminate.
func weaker(a, b controlv1.FindingVerdict) controlv1.FindingVerdict {
	rank := func(v controlv1.FindingVerdict) int {
		switch v {
		case confirmed:
			return 2
		case suspected:
			return 1
		}
		return 0
	}
	if rank(b) < rank(a) {
		return b
	}
	if rank(a) == 0 {
		return indeterminate
	}
	return a
}

// draft is one finding before it is a record: its rule, what it is about,
// the strongest verdict the rule allows, and what it rests on.
type draft struct {
	rule   string
	anchor []string
	cap    controlv1.FindingVerdict
	refs   []ref
}

// findingIDDomain separates this hash from every other SHA-256 the product
// takes, as the observation id's domain does.
const findingIDDomain = "finding-id:1"

// findingID is "fnd-" and the first 32 lowercase hex digits of SHA-256 over
// the domain, the tenant, project and run, the procedure's id and version,
// the rule's id and version and the anchor's fields, each after its length
// as eight big-endian bytes. The anchor is the tool and upstream of a plane
// call, the one name of an observed call, a step's id, a failed request's
// id, or nothing for a deadline. What the finding rests on is left out, so
// more evidence of one finding keeps its id.
func findingID(fields ...string) string {
	h := sha256.New()
	var n [8]byte
	for _, s := range append([]string{findingIDDomain}, fields...) {
		binary.BigEndian.PutUint64(n[:], uint64(len(s)))
		h.Write(n[:])
		h.Write([]byte(s))
	}
	return "fnd-" + hex.EncodeToString(h.Sum(nil)[:16])
}

// scope is what every record of one supervision shares.
type scope struct {
	tenant, project, run string
	proc                 *Procedure
}

func (s scope) procedureRef() *findingv1alpha1.ProcedureRef {
	return &findingv1alpha1.ProcedureRef{ProcedureId: s.proc.id, Version: s.proc.version, Digest: s.proc.digest}
}

func (s scope) record(d draft) *findingv1alpha1.FindingRecord {
	spec := s.proc.rules[d.rule]
	verdict := d.cap
	refs := make([]*findingv1alpha1.Reference, 0, len(d.refs))
	for _, r := range d.refs {
		verdict = weaker(verdict, r.v)
		refs = append(refs, r.r)
	}
	fields := append([]string{s.tenant, s.project, s.run, s.proc.id, s.proc.version, d.rule, RuleVersion}, d.anchor...)
	return &findingv1alpha1.FindingRecord{
		SchemaVersion: RecordSchemaVersion, TenantId: s.tenant, ProjectId: s.project,
		Procedure: s.procedureRef(), Escalation: spec.Escalation, Refs: refs,
		Finding: &controlv1.Finding{
			FindingId: findingID(fields...), RuleId: d.rule, RuleVersion: RuleVersion,
			Severity: spec.Severity, Verdict: verdict, RunId: s.run,
			Source: controlv1.FindingSource_FINDING_SOURCE_DETERMINISTIC,
		},
	}
}
