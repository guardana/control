package main

import (
	"fmt"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"

	controlv1 "github.com/guardana/control/api/gen/go/guardana/control/v1"
)

// How a request ended, as a row says it.
const (
	endCompleted = "completed"
	endFailed    = "failed"
	endAborted   = "aborted"
	endBlocked   = "blocked"
	endOpen      = "open"
	endUnknown   = "unknown"
)

// requestKey is a request's scope: an id is unique only within a project.
type requestKey struct{ tenant, project, request string }

type row struct {
	key                           requestKey
	run, action, verdict, reasons string
	held                          bool
	approval, end, note           string
}

func (r row) String() string {
	held := "no"
	if r.held {
		held = "yes"
	}
	return strings.Join([]string{safe(r.key.tenant), safe(r.key.project), safe(r.key.request), safe(r.run),
		safe(r.action), safe(r.verdict), safe(r.reasons), held, safe(r.approval), r.end, safe(r.note)}, "\t")
}

// safe keeps a value from the export on one cell of one row: empty is "-",
// and a value holding a control character or invalid UTF-8 is quoted.
func safe(s string) string {
	if s == "" {
		return "-"
	}
	for _, c := range s {
		if unicode.IsControl(c) || c == utf8.RuneError {
			return strconv.Quote(s)
		}
	}
	return s
}

// lifecycles groups the events by request, in the order each request first
// appears, and reads each one's lifecycle.
func (x *export) lifecycles() []row {
	var order []requestKey
	groups := map[requestKey][]*event{}
	for _, e := range x.events {
		k := requestKey{e.ev.GetTenantId(), e.ev.GetProjectId(), e.ev.GetRequestId()}
		if _, seen := groups[k]; !seen {
			order = append(order, k)
		}
		groups[k] = append(groups[k], e)
	}
	rows := make([]row, 0, len(order))
	for _, k := range order {
		rows = append(rows, lifecycle(k, groups[k]))
	}
	return rows
}

// lifecycle reads one request. A chain that does not link is unknown, and
// its row shows what its events say in the file's order.
func lifecycle(k requestKey, events []*event) row {
	r := row{key: k}
	chain, note := linked(k, events)
	if note != "" {
		inFileOrder := make([]*controlv1.Event, len(events))
		for i, e := range events {
			inFileOrder[i] = e.ev
		}
		r.describe(inFileOrder)
		r.end, r.note = endUnknown, note
		return r
	}
	r.describe(chain)
	r.end, r.note = walk(chain)
	return r
}

// describe fills what the events say about the action, its decision and
// its approval, the last approval event deciding how the approval ended.
func (r *row) describe(events []*controlv1.Event) {
	r.run = events[0].GetRunId()
	for _, ev := range events {
		switch ev.GetKind() {
		case controlv1.EventKind_EVENT_KIND_ACTION_PROPOSED:
			if r.action == "" {
				r.action = ev.GetProposed().GetAction().GetName()
			}
		case controlv1.EventKind_EVENT_KIND_POLICY_DECIDED:
			if d := ev.GetDecision(); d != nil && r.verdict == "" {
				r.verdict = strings.TrimPrefix(d.GetVerdict().String(), "VERDICT_")
				r.reasons = strings.Join(d.GetReasonCodes(), ",")
			}
		case controlv1.EventKind_EVENT_KIND_APPROVAL_REQUESTED:
			r.held, r.approval = true, "pending"
		case controlv1.EventKind_EVENT_KIND_APPROVAL_DECIDED:
			r.approval = answer(ev.GetApproval().GetState())
		case controlv1.EventKind_EVENT_KIND_APPROVAL_EXPIRED:
			r.approval = "expired"
		}
	}
}

func answer(s controlv1.ApprovalState) string {
	switch s {
	case controlv1.ApprovalState_APPROVAL_STATE_APPROVED:
		return "approved"
	case controlv1.ApprovalState_APPROVAL_STATE_REJECTED:
		return "rejected"
	default:
		return "unknown"
	}
}

// linked returns the events in the order their prev_event_id links give,
// or why they do not make one chain from one first event.
func linked(k requestKey, events []*event) ([]*controlv1.Event, string) {
	if k.tenant == "" || k.project == "" || k.request == "" {
		return nil, "an event names no tenant, project or request"
	}
	ids, note := eventIDs(events)
	if note != "" {
		return nil, note
	}
	head, next, note := links(events, ids)
	if note != "" {
		return nil, note
	}
	chain := make([]*controlv1.Event, 0, len(events))
	for ev := head; ev != nil && len(chain) < len(events); ev = next[ev.GetEventId()] {
		chain = append(chain, ev)
	}
	if len(chain) != len(events) {
		return nil, fmt.Sprintf("%d of the request's events are not on the chain from %s",
			len(events)-len(chain), safe(head.GetEventId()))
	}
	return chain, ""
}

// eventIDs returns the request's event ids, each of which a link can name
// only when it is there and held once.
func eventIDs(events []*event) (map[string]bool, string) {
	ids := make(map[string]bool, len(events))
	for _, e := range events {
		id := e.ev.GetEventId()
		switch {
		case id == "":
			return nil, "an event carries no event_id"
		case e.conflicting || ids[id]:
			return nil, fmt.Sprintf("event %s is held twice with different content", safe(id))
		}
		ids[id] = true
	}
	return ids, ""
}

// links returns the one event that follows no other and, by event id, the
// one event that follows each.
func links(events []*event, ids map[string]bool) (*controlv1.Event, map[string]*controlv1.Event, string) {
	var head *controlv1.Event
	next := make(map[string]*controlv1.Event, len(events))
	for _, e := range events {
		id, prev := e.ev.GetEventId(), e.ev.GetPrevEventId()
		switch {
		case prev == "" && head != nil:
			return nil, nil, fmt.Sprintf("events %s and %s both start the request", safe(head.GetEventId()), safe(id))
		case prev == "":
			head = e.ev
		case !ids[prev]:
			return nil, nil, fmt.Sprintf("event %s follows %s, which this export does not hold", safe(id), safe(prev))
		case next[prev] != nil:
			return nil, nil, fmt.Sprintf("events %s and %s both follow %s", safe(next[prev].GetEventId()), safe(id), safe(prev))
		default:
			next[prev] = e.ev
		}
	}
	if head == nil {
		return nil, nil, "no event starts the request"
	}
	return head, next, ""
}

// Where a request has got to in its lifecycle.
type stage int

const (
	atStart stage = iota
	atProposed
	atDecided
	atApprovalRequested
	atApprovalDecided
	atApprovalExpired
	atStarted
	atEnded
	// unchanged marks the kinds that annotate a lifecycle without moving it.
	unchanged
)

// steps is the lifecycle: the stage each kind moves to and the stages it
// may follow. An unanswered approval window lets the action be blocked or
// asked for again, never started.
var steps = map[controlv1.EventKind]struct {
	to   stage
	from []stage
}{
	controlv1.EventKind_EVENT_KIND_ACTION_PROPOSED:    {atProposed, []stage{atStart}},
	controlv1.EventKind_EVENT_KIND_POLICY_DECIDED:     {atDecided, []stage{atProposed}},
	controlv1.EventKind_EVENT_KIND_APPROVAL_REQUESTED: {atApprovalRequested, []stage{atDecided, atApprovalExpired}},
	controlv1.EventKind_EVENT_KIND_APPROVAL_DECIDED:   {atApprovalDecided, []stage{atApprovalRequested}},
	controlv1.EventKind_EVENT_KIND_APPROVAL_EXPIRED:   {atApprovalExpired, []stage{atApprovalRequested}},
	controlv1.EventKind_EVENT_KIND_ACTION_STARTED:     {atStarted, []stage{atDecided, atApprovalDecided}},
	controlv1.EventKind_EVENT_KIND_ACTION_COMPLETED:   {atEnded, []stage{atStarted}},
	controlv1.EventKind_EVENT_KIND_ACTION_FAILED:      {atEnded, []stage{atStarted}},
	controlv1.EventKind_EVENT_KIND_ACTION_BLOCKED:     {atEnded, []stage{atDecided, atApprovalDecided, atApprovalExpired}},
	controlv1.EventKind_EVENT_KIND_FINDING_RAISED:     {unchanged, []stage{atProposed, atDecided, atApprovalRequested, atApprovalDecided, atApprovalExpired, atStarted, atEnded}},
	controlv1.EventKind_EVENT_KIND_POLICY_RELOADED:    {unchanged, []stage{atStart, atProposed, atDecided, atApprovalRequested, atApprovalDecided, atApprovalExpired, atStarted, atEnded}},
}

func follows(at stage, from []stage) bool {
	for _, f := range from {
		if at == f {
			return true
		}
	}
	return false
}

func kindName(k controlv1.EventKind) string { return strings.TrimPrefix(k.String(), "EVENT_KIND_") }

func modeName(m controlv1.EnforcementMode) string {
	return strings.TrimPrefix(m.String(), "ENFORCEMENT_MODE_")
}
