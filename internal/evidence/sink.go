package evidence

import (
	"context"
	"errors"
	"sync"

	controlv1 "github.com/guardana/control/api/gen/go/guardana/control/v1"
	"google.golang.org/protobuf/proto"
)

// ErrNoEvent is an Append of a nil event.
var ErrNoEvent = errors.New("evidence: no event to append")

// Sink is where an enforcing plane writes the events of its trails (ADR-0014).
// Append returns only when the event is durable under the sink's own policy.
// An error means the event is not durable, and a caller never answers an agent
// as if it were: before an effect the call is blocked, after one the result is
// delivered and the plane stops taking material calls.
type Sink interface {
	Append(ctx context.Context, event *controlv1.Event) error
}

// MemorySink keeps events in memory, in the order they were appended, for
// tests. The zero value is ready to use and safe for concurrent use.
type MemorySink struct {
	mu     sync.Mutex
	events []*controlv1.Event
}

// Append stores a clone of event, so a caller that goes on mutating its event
// does not rewrite the record. It refuses a nil event and a context that is
// already done, because a record nobody waited for is not one anybody holds.
func (s *MemorySink) Append(ctx context.Context, event *controlv1.Event) error {
	if event == nil {
		return ErrNoEvent
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	clone := proto.Clone(event).(*controlv1.Event)
	s.mu.Lock()
	defer s.mu.Unlock()
	s.events = append(s.events, clone)
	return nil
}

// Events returns clones of every event appended so far, in order.
func (s *MemorySink) Events() []*controlv1.Event {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]*controlv1.Event, len(s.events))
	for i, e := range s.events {
		out[i] = proto.Clone(e).(*controlv1.Event)
	}
	return out
}
