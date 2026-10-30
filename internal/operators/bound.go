package operators

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"sync"
	"time"
)

// Error is a refusal by this package, matched with errors.Is. The values are
// constants.
type Error string

// Error returns the refusal's text.
func (e Error) Error() string { return string(e) }

const (
	// ErrUnknownKind is a kind this build does not have.
	ErrUnknownKind Error = "operators: not a kind this build has"
	// ErrConfig is a configuration a port cannot start from.
	ErrConfig Error = "operators: the configuration is refused"
)

// The bounds every call to another port is held to.
const (
	// DefaultTimeout is how long a call to another port may take when the
	// configuration names no time.
	DefaultTimeout = 2 * time.Second
	// MaxTimeout bounds the time a configuration may name.
	MaxTimeout = 30 * time.Second
	// DefaultInFlight is how many calls to another port may run at once when
	// the configuration names no number.
	DefaultInFlight = 16
	// MaxInFlight bounds the number a configuration may name.
	MaxInFlight = 256
)

// Bounds is the time and concurrency a port's calls to another port are
// held to. A zero member takes its default.
type Bounds struct {
	Timeout  time.Duration
	InFlight int
}

func (b Bounds) checked() (Bounds, error) {
	if b.Timeout == 0 {
		b.Timeout = DefaultTimeout
	}
	if b.InFlight == 0 {
		b.InFlight = DefaultInFlight
	}
	switch {
	case b.Timeout < 0 || b.Timeout > MaxTimeout:
		return Bounds{}, fmt.Errorf("%w: a timeout of %s, at most %s", ErrConfig, b.Timeout, MaxTimeout)
	case b.InFlight < 0 || b.InFlight > MaxInFlight:
		return Bounds{}, fmt.Errorf("%w: %d calls at once, at most %d", ErrConfig, b.InFlight, MaxInFlight)
	}
	return b, nil
}

// caller runs calls to another port within its bounds and counts each cause
// it answers.
type caller struct {
	timeout time.Duration
	slots   chan struct{}

	mu     sync.Mutex
	counts map[Cause]uint64
}

func newCaller(b Bounds) (*caller, error) {
	b, err := b.checked()
	if err != nil {
		return nil, err
	}
	return &caller{timeout: b.Timeout, slots: make(chan struct{}, b.InFlight), counts: map[Cause]uint64{}}, nil
}

// call runs fn under the timeout and returns what it answered, or the cause
// that kept it from answering in time: CauseBusy when every slot is taken,
// CauseCanceled when ctx ended, CauseLate when the timeout did. A call that
// outlives its timeout keeps its slot until it returns, so a port that never
// answers holds at most InFlight goroutines.
func call[T any](ctx context.Context, c *caller, fn func(context.Context) T) (T, Cause) {
	var zero T
	if ctx.Err() != nil {
		return zero, CauseCanceled
	}
	select {
	case c.slots <- struct{}{}:
	default:
		return zero, CauseBusy
	}
	ctx, cancel := context.WithTimeout(ctx, c.timeout)
	defer cancel()
	done := make(chan T, 1)
	go func() {
		defer func() { <-c.slots }()
		done <- fn(ctx)
	}()
	select {
	case v := <-done:
		if ctx.Err() == nil {
			return v, CauseNone
		}
	case <-ctx.Done():
	}
	if errors.Is(ctx.Err(), context.DeadlineExceeded) {
		return zero, CauseLate
	}
	return zero, CauseCanceled
}

func (c *caller) count(cause Cause) {
	if cause == CauseNone {
		return
	}
	c.mu.Lock()
	c.counts[cause]++
	c.mu.Unlock()
}

func (c *caller) snapshot() map[Cause]uint64 {
	c.mu.Lock()
	defer c.mu.Unlock()
	return maps.Clone(c.counts)
}

// Status is what doctor and the health page show of a port: its kind and
// how often each cause was answered.
type Status struct {
	Kind   string
	Counts map[Cause]uint64
}
