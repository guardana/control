package trust

import (
	"context"
	"time"
)

// Roster is the trust roster port: which key a key id names, under the last
// roster the plane verified. Its zero Answer is unavailable, and an
// implementation answers a key only from a Document, so whatever delivers
// the roster's bytes never decides what they say.
type Roster interface {
	Lookup(ctx context.Context, keyID string) Answer
	Status(ctx context.Context) Status
}

// State is what a lookup answered.
type State uint8

// The states. The zero State is StateUnavailable.
const (
	// StateUnavailable is no usable roster: none verified yet, the one held
	// expired, or the lookup was cancelled. Nothing it would name is trusted.
	StateUnavailable State = iota
	// StateUnknownKey is a usable roster that names no key under the id.
	StateUnknownKey
	// StateKey is the key the roster names under the id.
	StateKey
)

// Cause says why an answer is not a key, and what the last attempt to take
// a roster met. It is a closed set, counted per cause.
type Cause string

// The causes. CauseNone goes with StateKey and with a refresh that took or
// kept a roster.
const (
	CauseNone Cause = ""
	// CauseNoRoster is a roster port that never verified a roster.
	CauseNoRoster Cause = "no_roster"
	// CauseUnreadable is a roster file that could not be read.
	CauseUnreadable Cause = "roster_unreadable"
	// CauseRefused is a roster whose envelope, signature or body is refused.
	CauseRefused Cause = "roster_refused"
	// CauseBelowFloor is a roster whose serial is below the floor or below
	// the roster held.
	CauseBelowFloor Cause = "roster_below_floor"
	// CauseForked is a roster at the floor's serial with another digest.
	CauseForked Cause = "roster_forked"
	// CauseFloor is a floor directory that could not be read or raised.
	CauseFloor Cause = "floor_unavailable"
	// CauseOtherRoster is a roster of another id than the one held.
	CauseOtherRoster Cause = "other_roster"
	// CauseFuture is a roster issued after the clock.
	CauseFuture Cause = "roster_future"
	// CauseExpired is a roster at or after its expiry.
	CauseExpired Cause = "roster_expired"
	// CauseCanceled is a lookup whose context ended first.
	CauseCanceled Cause = "canceled"
	// CauseUnknownKey goes with StateUnknownKey.
	CauseUnknownKey Cause = "unknown_key"
)

// Answer is a lookup's answer. The zero Answer is unavailable with no cause
// named, which a consumer treats as any other unavailable answer.
type Answer struct {
	state State
	cause Cause
	key   Key
}

// Unavailable is an answer that names no key. Anything may build one: it
// only takes trust away.
func Unavailable(cause Cause) Answer { return Answer{cause: cause} }

// State is what the lookup answered.
func (a Answer) State() State { return a.state }

// Cause is why the answer is not a key.
func (a Answer) Cause() Cause { return a.cause }

// Key is the key the answer names, and the zero Key, which verifies nothing
// and holds no role or grant, for every state but StateKey.
func (a Answer) Key() Key {
	if a.state != StateKey {
		return Key{}
	}
	return a.key
}

func unknownKey() Answer { return Answer{state: StateUnknownKey, cause: CauseUnknownKey} }

func keyAnswer(k Key) Answer { return Answer{state: StateKey, key: k} }

// Health is whether a roster port holds a usable roster.
type Health uint8

// The healths. The zero Health is HealthUnavailable.
const (
	HealthUnavailable Health = iota
	HealthUsable
)

// Status is what doctor and the health page show of a roster port: its
// kind, whether it holds a usable roster and which, what the last refresh
// met, and how often each cause was met.
type Status struct {
	Kind      Kind
	Health    Health
	RosterID  string
	Serial    int64
	Digest    string
	ExpiresAt time.Time
	Last      Cause
	Counts    map[Cause]uint64
}
