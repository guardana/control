package rules

import (
	"encoding/json"
	"fmt"
	"maps"
	"slices"

	"github.com/guardana/control/pkg/contract"
)

// The readers below take one value of the decoded tree and return it in the
// model's terms, or refuse it where it stands. None of them repeats what it
// refuses.

// reader keeps the first refusal of a run of reads, so a struct is read in one
// composite literal, field by field in the order the literal lists them, and
// every read after a refusal is skipped.
type reader struct{ err error }

// need reads a member the format requires.
func need[T any](r *reader, m map[string]any, key string, a at, read func(any, at) (T, error)) T {
	var zero T
	if r.err != nil {
		return zero
	}
	v, ok := m[key]
	if !ok {
		r.err = a.key(key).refuse(ErrMissingKey)
		return zero
	}
	out, err := read(v, a.key(key))
	if err != nil {
		r.err = err
		return zero
	}
	return out
}

// optional reads a member that may be absent, which leaves the zero value.
func optional[T any](r *reader, m map[string]any, key string, a at, read func(any, at) (T, error)) T {
	if _, ok := m[key]; !ok {
		var zero T
		return zero
	}
	return need(r, m, key, a, read)
}

func object(v any, a at) (map[string]any, error) {
	m, ok := v.(map[string]any)
	if !ok {
		return nil, a.refuse(fmt.Errorf("%w: want an object", ErrWrongType))
	}
	return m, nil
}

// onlyKeys refuses a key outside keys. Which unknown key it meets first does
// not matter: the refusal names the object, never the key.
func onlyKeys(m map[string]any, a at, keys ...string) error {
	for key := range m {
		if !slices.Contains(keys, key) {
			return a.refuse(ErrUnknownKey)
		}
	}
	return nil
}

// closedObject reads an object whose keys the format fixes.
func closedObject(v any, a at, keys ...string) (map[string]any, error) {
	m, err := object(v, a)
	if err != nil {
		return nil, err
	}
	if err := onlyKeys(m, a, keys...); err != nil {
		return nil, err
	}
	return m, nil
}

// group reads an object that constrains. One with no members would be a
// second spelling of its absence, and would read as no constraint at all.
func group(v any, a at, keys ...string) (map[string]any, error) {
	m, err := closedObject(v, a, keys...)
	if err != nil {
		return nil, err
	}
	if len(m) == 0 {
		return nil, a.refuse(fmt.Errorf("%w: an object with no members", ErrEmpty))
	}
	return m, nil
}

func text(v any, a at) (string, error) {
	s, ok := v.(string)
	if !ok {
		return "", a.refuse(fmt.Errorf("%w: want a string", ErrWrongType))
	}
	return s, nil
}

// identifier reads a string that names something: a bundle, a rule, or a
// value a constraint lists. An envelope field holding "" is an absent input,
// which no constraint compares, so a listed "" could never match; the empty
// string is refused everywhere this reader runs.
func identifier(v any, a at) (string, error) {
	s, err := text(v, a)
	if err != nil {
		return "", err
	}
	if s == "" {
		return "", a.refuse(fmt.Errorf("%w: an empty string", ErrEmpty))
	}
	if err := envelopeString(s, a); err != nil {
		return "", err
	}
	return s, nil
}

// envelopeString holds s to the rules the envelope holds its own strings to:
// the byte bound and the identifier rule of ADR-0011. A string no envelope can
// carry could never be matched, and one that breaks the rule can look like
// another.
func envelopeString(s string, a at) error {
	if len(s) > contract.MaxStringBytes {
		return a.refuse(fmt.Errorf("%w: %d bytes, limit %d", ErrTooLarge, len(s), contract.MaxStringBytes))
	}
	if err := contract.CheckIdentifier(s); err != nil {
		return a.refuse(fmt.Errorf("%w: %w", ErrIdentifier, err))
	}
	return nil
}

// list reads a non-empty array of at most limit items.
func list(v any, a at, limit int) ([]any, error) {
	items, ok := v.([]any)
	if !ok {
		return nil, a.refuse(fmt.Errorf("%w: want an array", ErrWrongType))
	}
	if len(items) == 0 {
		return nil, a.refuse(fmt.Errorf("%w: an array with no items", ErrEmpty))
	}
	if len(items) > limit {
		return nil, a.refuse(fmt.Errorf("%w: %d items, limit %d", ErrTooLarge, len(items), limit))
	}
	return items, nil
}

// each reads every item of a list, naming each by its position.
func each[T any](v any, a at, limit int, read func(any, at) (T, error)) ([]T, error) {
	items, err := list(v, a, limit)
	if err != nil {
		return nil, err
	}
	out := make([]T, len(items))
	for i, item := range items {
		if out[i], err = read(item, a.index(i)); err != nil {
			return nil, err
		}
	}
	return out, nil
}

func identifiers(v any, a at) ([]string, error) { return each(v, a, maxValues, identifier) }

// entries reads an object whose keys are the author's: a map constraint or
// params. The keys come back sorted, which is the order a refusal names an
// entry in, by position, since a key is the author's content.
func entries(v any, a at) ([]string, map[string]any, error) {
	m, err := object(v, a)
	if err != nil {
		return nil, nil, err
	}
	if len(m) == 0 {
		return nil, nil, a.refuse(fmt.Errorf("%w: an object with no members", ErrEmpty))
	}
	if len(m) > contract.MaxLabels {
		return nil, nil, a.refuse(fmt.Errorf("%w: %d keys, limit %d", ErrTooLarge, len(m), contract.MaxLabels))
	}
	keys := slices.Sorted(maps.Keys(m))
	for i, key := range keys {
		if err := mapKey(key, a.index(i).key("key")); err != nil {
			return nil, nil, err
		}
	}
	return keys, m, nil
}

// mapKey holds a key to the rules the envelope holds its own map keys to
// (ADR-0011), through the contract's own check, so that a key no envelope
// carries is refused here by the same rule that refuses it there. Keys
// that fold together are the canonical form's refusal. CheckMapKey has one
// rule beyond the identifier rule, the reserved namespace, so a key it refuses
// after CheckIdentifier accepted it is in that namespace.
func mapKey(key string, a at) error {
	if err := envelopeString(key, a); err != nil {
		return err
	}
	if contract.CheckMapKey(key) != nil {
		return a.refuse(ErrReservedKey)
	}
	return nil
}

// host reads a value of destination.host: an identifier that also has the
// one spelling a host has, through the contract's own check, so a DENY on a
// host is never spelled in a way no envelope can carry.
func host(v any, a at) (string, error) {
	s, err := identifier(v, a)
	if err != nil {
		return "", err
	}
	if err := contract.CheckHost(s); err != nil {
		return "", a.refuse(fmt.Errorf("%w: %w", ErrIdentifier, err))
	}
	return s, nil
}

func hosts(v any, a at) ([]string, error) { return each(v, a, maxValues, host) }

// valueMap reads a map constraint: each key names an entry the envelope's map
// must hold, with the values it may hold there.
func valueMap(v any, a at) (map[string][]string, error) {
	keys, m, err := entries(v, a)
	if err != nil {
		return nil, err
	}
	out := make(map[string][]string, len(keys))
	for i, key := range keys {
		if out[key], err = identifiers(m[key], a.index(i)); err != nil {
			return nil, err
		}
	}
	return out, nil
}

// params reads an obligation's parameters, held to the envelope's rules for a
// string map, which admit an empty value.
func params(v any, a at) (map[string]string, error) {
	keys, m, err := entries(v, a)
	if err != nil {
		return nil, err
	}
	out := make(map[string]string, len(keys))
	for i, key := range keys {
		s, err := text(m[key], a.index(i))
		if err != nil {
			return nil, err
		}
		if err := envelopeString(s, a.index(i)); err != nil {
			return nil, err
		}
		out[key] = s
	}
	return out, nil
}

// count reads an integer above zero and at most limit. The canonical form
// admits only integer literals inside the JSON-safe range, so the conversion
// cannot fail on one; were it to, the value is refused all the same.
func count(v any, a at, limit int64) (int64, error) {
	num, ok := v.(json.Number)
	if !ok {
		return 0, a.refuse(fmt.Errorf("%w: want an integer", ErrWrongType))
	}
	n, err := num.Int64()
	if err != nil || n <= 0 {
		return 0, a.refuse(fmt.Errorf("%w: want 1 or more", ErrNotPositive))
	}
	if n > limit {
		return 0, a.refuse(fmt.Errorf("%w: limit %d", ErrTooLarge, limit))
	}
	return n, nil
}

func flag(v any, a at) (bool, error) {
	b, ok := v.(bool)
	if !ok {
		return false, a.refuse(fmt.Errorf("%w: want true or false", ErrWrongType))
	}
	return b, nil
}
