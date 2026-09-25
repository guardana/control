package evidence

import (
	"fmt"

	controlv1 "github.com/guardana/control/api/gen/go/guardana/control/v1"
	"github.com/guardana/control/pkg/contract"
)

// trailScope is what every event of one trail shares: the request it accounts
// for, and the project and tenant that request belongs to.
//
// The request alone is not the scope. request_id is unique only within a
// project (action_envelope.proto), so two projects may each hold a req-1, and a
// trail mixing them reads one request's outcome as another's. The tenant is
// held the same way, because a reader that scopes a trail by its first event,
// an exporter among them, would otherwise carry a second tenant's events under
// the first one's name.
type trailScope struct {
	request, project, tenant string
}

// scopeOf reads the scope from event 0 and refuses an empty value rather than
// taking it as the scope. When no event carries one, every event equals event
// 0, and the comparison in holds would join a trail that names no request, no
// project or no tenant.
func scopeOf(first *controlv1.Event) (trailScope, error) {
	s := trailScope{
		request: first.GetRequestId(),
		project: first.GetProjectId(),
		tenant:  first.GetTenantId(),
	}
	for _, field := range [...]struct{ name, value string }{
		{"request_id", s.request},
		{"project_id", s.project},
		{"tenant_id", s.tenant},
	} {
		if field.value == "" {
			return trailScope{}, fmt.Errorf("%w: event 0 carries no %s", ErrChainBroken, field.name)
		}
	}
	return s, nil
}

// holds reports the first part of the scope that event does not share. Two
// scopes are one broken trail and not two trails.
func (s trailScope) holds(i int, event *controlv1.Event) error {
	for _, field := range [...]struct{ name, got, want string }{
		{"request", event.GetRequestId(), s.request},
		{"project", event.GetProjectId(), s.project},
		{"tenant", event.GetTenantId(), s.tenant},
	} {
		if field.got != field.want {
			return fmt.Errorf("%w: event %d belongs to %s %s, not %s",
				ErrChainBroken, i, field.name, quoteID(field.got), quoteID(field.want))
		}
	}
	return nil
}

// modeDefect refuses an event that names no enforcement mode. The mode says
// whether a DENY on the trail was enforced or only observed, so an event
// without one says nothing about what was done; NewBuilder refuses to make a
// Builder without one for the same reason. Each event names its own, and
// nothing here requires two events to agree.
func modeDefect(i int, event *controlv1.Event) error {
	if event.GetEnforcementMode() == controlv1.EnforcementMode_ENFORCEMENT_MODE_UNSPECIFIED {
		return fmt.Errorf("%w: event %d names no enforcement mode", ErrChainBroken, i)
	}
	return nil
}

// checkModes reports the first event naming an enforcement mode this version
// does not declare. It is the undetermined answer and not the broken one:
// common.proto makes an undeclared enum number INDETERMINATE for a receiver, so
// a mode a later minor version adds reads here the way a kind it adds does.
// ValidateChain asks it last, so it never stands in front of a definite defect.
func checkModes(events []*controlv1.Event) error {
	for i, event := range events {
		mode := event.GetEnforcementMode()
		if _, declared := controlv1.EnforcementMode_name[int32(mode)]; !declared {
			return fmt.Errorf("%w: event %d names enforcement mode %d, which this version does not declare",
				ErrChainIndeterminate, i, int32(mode))
		}
	}
	return nil
}

// namesExecution reports whether an event of this kind records something
// running, and so has to say what ran: ACTION_STARTED names the execution that
// began, ACTION_COMPLETED and ACTION_FAILED the one that ended.
func namesExecution(kind controlv1.EventKind) bool {
	switch kind {
	case controlv1.EventKind_EVENT_KIND_ACTION_STARTED,
		controlv1.EventKind_EVENT_KIND_ACTION_COMPLETED,
		controlv1.EventKind_EVENT_KIND_ACTION_FAILED:
		return true
	default:
		return false
	}
}

// execution carries the execution through the walk: ACTION_STARTED sets it,
// and the event that closes the action has to name the same one. The order
// allows one start per trail, so a closing event naming another execution
// accounts for something this trail never started.
//
// It is a step of the walk and not a definite defect. After a kind this
// version cannot place the walk has stopped, and a later version may add a
// kind after which a second execution is legitimate.
func execution(i int, event *controlv1.Event, startedAs string) (string, error) {
	switch event.GetKind() {
	case controlv1.EventKind_EVENT_KIND_ACTION_STARTED:
		return event.GetExecutionId(), nil
	case controlv1.EventKind_EVENT_KIND_ACTION_COMPLETED, controlv1.EventKind_EVENT_KIND_ACTION_FAILED:
		if got := event.GetExecutionId(); got != startedAs {
			return startedAs, fmt.Errorf("%w: event %d closes execution %s, not %s, which started",
				ErrChainBroken, i, quoteID(got), quoteID(startedAs))
		}
	}
	return startedAs, nil
}

// lengthDefect refuses an event carrying an identifier, among those
// ValidateChain reads, that is longer than contract.MaxStringBytes, the
// contract's bound on a string. A Builder bounds the four identifiers it is
// given. It cannot refuse the two it is handed call by call, event_id from
// newID and execution_id from Started, so a longer one is refused here, and
// the line bound is sized on none being longer. The refusal states the
// length and never quotes the value.
func lengthDefect(i int, event *controlv1.Event) error {
	for _, field := range [...]struct{ name, value string }{
		{"request_id", event.GetRequestId()},
		{"project_id", event.GetProjectId()},
		{"tenant_id", event.GetTenantId()},
		{"event_id", event.GetEventId()},
		{"prev_event_id", event.GetPrevEventId()},
		{"execution_id", event.GetExecutionId()},
	} {
		if len(field.value) > contract.MaxStringBytes {
			return fmt.Errorf("%w: event %d's %s is %d bytes, over %d",
				ErrChainBroken, i, field.name, len(field.value), contract.MaxStringBytes)
		}
	}
	return nil
}
