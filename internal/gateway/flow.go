package gateway

import (
	"fmt"
	"slices"
	"strconv"
	"strings"

	"google.golang.org/protobuf/proto"

	controlv1 "github.com/guardana/control/api/gen/go/guardana/control/v1"
	"github.com/guardana/control/internal/core"
	"github.com/guardana/control/pkg/contract"
)

// flowTagPrefix is the run-context tag prefix under which the pipeline records
// the flow state a decision used (ADR-0021).
const flowTagPrefix = "flow.v1."

// reservedPrefix is what no producer's tag may start with, compared in ASCII
// case only: the flow tags of every version, however their ASCII letters are
// cased, so no producer can forge that record. A tag that only looks like one,
// with a letter from another script, is not under the prefix and is kept.
const reservedPrefix = "flow.v"

const (
	tagUntrusted   = flowTagPrefix + "untrusted="
	tagMaxRead     = flowTagPrefix + "max_read="
	tagReadUnknown = flowTagPrefix + "max_read=UNKNOWN"
	tagUncomputed  = flowTagPrefix + "state=uncomputed"
)

// runKey names a run: the principal as the listener resolved it and the agent
// the listener names. A session, a connection and whatever a client sends
// never split a run, since each is the client's to redraw.
type runKey struct {
	tenant, kind, principal, agent string
}

// run is what one run took in so far: whether any of it was untrusted, and the
// highest sensitivity it read, which is UNSPECIFIED for good once it read
// something undeclared. The pipeline's lock guards both; id never changes.
type run struct {
	id        string
	untrusted bool
	maxRead   controlv1.Sensitivity
}

// flow is a call's view of its run, taken once when the call starts: the run
// its execution taints, and the state every decision of the call is made
// under. The zero value is the state nobody computed, with no run.
type flow struct {
	run   *run
	state contract.FlowState
	// unnamed: the call's run was to be minted and the id source gave it no
	// name, so an execution would taint nothing its next call reads.
	unnamed bool
}

func (f flow) runID() string {
	if f.run == nil {
		return ""
	}
	return f.run.id
}

// tags renders the state as the run-context tags a recorded envelope carries.
func (f flow) tags() []string {
	if f.run == nil {
		return []string{tagUncomputed}
	}
	return []string{
		tagUntrusted + strconv.FormatBool(f.state.UntrustedInfluence),
		readTag(f.state.MaxSensitivityRead),
	}
}

// readTag names a reading by the contract's name without its prefix, as a
// policy spells a floor.
func readTag(s controlv1.Sensitivity) string {
	if !contract.ValidSensitivityFloor(s) {
		return tagReadUnknown
	}
	_, name, _ := strings.Cut(s.String(), "_")
	return tagMaxRead + name
}

func keyOf(env *controlv1.ActionEnvelope) (runKey, bool) {
	principal := env.GetPrincipal()
	if principal.GetId() == "" {
		return runKey{}, false
	}
	return runKey{
		tenant: principal.GetTenantId(), kind: principal.GetType(),
		principal: principal.GetId(), agent: env.GetAgent().GetId(),
	}, true
}

// flowOf takes the flow state of env's run, minting the run the first time its
// key is seen when mint is set. A call the pipeline cannot key, whose run it
// may not mint, or whose key would be past MaxRuns, gets the state nobody
// computed; so does a call whose run the id source cannot name, which is
// marked unnamed. Every one of them is counted.
func (p *Pipeline) flowOf(env *controlv1.ActionEnvelope, mint bool) flow {
	f := p.lookupRun(env, mint)
	if f.run == nil {
		p.uncomputed()
	}
	return f
}

func (p *Pipeline) uncomputed() {
	p.counts.add(func(s *Stats) *uint64 { return &s.FlowUncomputed })
}

func (p *Pipeline) lookupRun(env *controlv1.ActionEnvelope, mint bool) flow {
	key, ok := keyOf(env)
	if !ok {
		return flow{}
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	r := p.runs[key]
	if r == nil {
		if !mint || len(p.runs) >= p.cfg.MaxRuns {
			return flow{}
		}
		id := p.cfg.NewID()
		if id == "" {
			return flow{unnamed: true}
		}
		r = &run{id: id, maxRead: controlv1.Sensitivity_SENSITIVITY_PUBLIC}
		p.runs[key] = r
	}
	return flow{run: r, state: contract.NewFlowState(r.untrusted, r.maxRead)}
}

// took records in f's run what a result brings in. The pipeline calls it when
// it hands an execution out, so every call whose arguments could carry that
// result is decided after it, and no closing and no evidence outcome can skip
// it.
func (p *Pipeline) took(f flow, trust controlv1.TrustZone, read controlv1.Sensitivity) {
	if f.run == nil {
		return
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if contract.IsUntrusted(trust) {
		f.run.untrusted = true
	}
	f.run.maxRead = joinRead(f.run.maxRead, read)
}

// joinRead is the higher of two readings. An unknown one, or one this build
// cannot place, leaves the run unknown for good: after a known INTERNAL and an
// undeclared read, INTERNAL would put a CONFIDENTIAL floor out of reach of
// data the run may hold, once an envelope carries a label.
func joinRead(current, read controlv1.Sensitivity) controlv1.Sensitivity {
	if !contract.ValidSensitivityFloor(current) || !contract.ValidSensitivityFloor(read) {
		return controlv1.Sensitivity_SENSITIVITY_UNSPECIFIED
	}
	return max(current, read)
}

// takeFlow takes the call's flow state and stamps it on a copy of its
// envelope, before anything decides the call. A call its producer forged a
// flow tag on reads no run. A call that carries a refusal mints none, since
// the plane blocks it in every mode but OBSERVE, which hands it out and so
// needs its run to take in what it returns.
func (c *call) takeFlow() {
	c.refuseFlowTags()
	if c.forged {
		c.p.uncomputed()
	} else {
		c.flow = c.p.flowOf(c.in.Envelope, c.in.Refusal == nil || c.p.cfg.Mode == modeObserve)
	}
	c.stampFlow()
}

// refuseFlowTags marks a call whose producer sent a tag under the reserved
// prefix forged, and refuses the first such tag when the call carries no
// refusal yet. An earlier refusal stays the call's alone, since the kernel
// codes a joined one by the first sentinel it finds and would name the tag
// instead.
func (c *call) refuseFlowTags() {
	for i, tag := range c.in.Envelope.GetContext().GetTags() {
		if isFlowTag(tag) {
			if c.in.Refusal == nil {
				c.in.Refusal = &contract.ValidationError{
					Field: "context.tags[" + strconv.Itoa(i) + "]",
					Err:   fmt.Errorf("%w: the %s prefix is the plane's own", contract.ErrInvalidValue, reservedPrefix),
				}
			}
			c.forged = true
			return
		}
	}
}

// stampFlow puts the call's flow state on a copy of its envelope as run-context
// tags, so ACTION_PROPOSED records the state its decision used. The copy keeps
// none of the producer's tags under the reserved prefix.
func (c *call) stampFlow() {
	env := c.in.Envelope
	if env == nil {
		return
	}
	out := proto.CloneOf(env)
	if out.Context == nil {
		out.Context = &controlv1.RunContext{}
	}
	out.Context.Tags = append(slices.DeleteFunc(out.Context.Tags, isFlowTag), c.flow.tags()...)
	c.in.Envelope = out
}

// refuseFlow blocks two calls in every mode: one whose producer sent a tag
// under the reserved prefix, whose record would otherwise carry a state the
// plane never computed, and one whose run the id source could not name,
// whose execution would leave no taint for the next call of its principal.
// A forged call reaches the kernel with a refusal, the producer's own or the
// tag's, so the block keeps the code the kernel gave that refusal.
func (c *call) refuseFlow() {
	switch {
	case c.action == core.Block:
	case c.forged:
		c.decide(verdictIndeterminate, c.kernel.GetReasonCodes()...)
	case c.flow.unnamed:
		c.decide(verdictIndeterminate, codeEvidenceUnavailable)
	}
}

// isFlowTag reports whether tag starts with the reserved prefix, ignoring
// ASCII case.
func isFlowTag(tag string) bool {
	if len(tag) < len(reservedPrefix) {
		return false
	}
	for i := range len(reservedPrefix) {
		b := tag[i]
		if 'A' <= b && b <= 'Z' {
			b += 'a' - 'A'
		}
		if b != reservedPrefix[i] {
			return false
		}
	}
	return true
}

// withoutFlowTags is a run context as a retry has to repeat it: the flow tags
// say what the run took in, which the fresh decision already weighs.
func withoutFlowTags(rc *controlv1.RunContext) *controlv1.RunContext {
	out := proto.CloneOf(orEmpty(rc))
	out.Tags = slices.DeleteFunc(out.Tags, isFlowTag)
	return out
}
