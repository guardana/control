package secretscan

import (
	"errors"
	"fmt"
	"slices"
)

// Kind is how sure the configuration is that a value is a credential.
type Kind int

const (
	// KindUnspecified is no kind; New refuses it.
	KindUnspecified Kind = iota
	// Credential is a value that is one by where it is configured, such as a
	// userinfo password or a header value; it is matched whatever its length.
	Credential
	// MaybeCredential is a value that may be one, such as a query value or a
	// variable a command receives; it is matched from MinMaybeBytes.
	MaybeCredential
)

// MinMaybeBytes is the shortest MaybeCredential value the scan matches.
const MinMaybeBytes = 8

// Secret is one configured value and the configuration key it came from.
// Key is what a log or doctor prints; Value never is.
type Secret struct {
	Key   string
	Value string
	Kind  Kind
}

// State is a scan's outcome. The zero value is Unscanned, which a caller
// withholds like Found.
type State int

const (
	// Unscanned is a scan that did not run to the end.
	Unscanned State = iota
	// Clean is a scan that read everything and found no secret.
	Clean
	// Found is a scan that found a secret.
	Found
)

// Verdict is what one scan found: on Found, the matched secret's key and the
// spelling it was found in, never the value or the text around it.
type Verdict struct {
	State    State
	Key      string
	Spelling string
}

// Withhold reports whether the scanned answer may not reach the agent.
func (v Verdict) Withhold() bool { return v.State != Clean }

// Set is the plane's secrets with every spelling the scan looks for,
// computed once. A Set is immutable and safe for concurrent use.
type Set struct {
	m          *matcher
	notScanned []string
}

// ErrSecret is a secret New refuses: an unspecified kind or an empty key.
var ErrSecret = errors.New("secretscan: a secret needs a key and a kind")

// New builds the set. An empty value is skipped, a MaybeCredential shorter
// than MinMaybeBytes is not scanned and its key is listed by NotScanned.
func New(secrets []Secret) (*Set, error) {
	s := &Set{}
	var entries []entry
	for i, sec := range secrets {
		if sec.Key == "" {
			return nil, fmt.Errorf("%w: secret %d has no key", ErrSecret, i)
		}
		if sec.Kind != Credential && sec.Kind != MaybeCredential {
			return nil, fmt.Errorf("%w: %q has kind %d", ErrSecret, sec.Key, int(sec.Kind))
		}
		if sec.Value == "" {
			continue
		}
		if sec.Kind == MaybeCredential && len(sec.Value) < MinMaybeBytes {
			s.notScanned = append(s.notScanned, sec.Key)
			continue
		}
		patterns, err := spellings(sec.Value)
		if err != nil {
			return nil, fmt.Errorf("%q: %w", sec.Key, err)
		}
		entries = append(entries, entry{key: sec.Key, patterns: patterns})
	}
	s.m = newMatcher(entries)
	return s, nil
}

// NotScanned lists, in configuration order, the keys of the values too short
// to scan.
func (s *Set) NotScanned() []string {
	if s == nil {
		return nil
	}
	return slices.Clone(s.notScanned)
}

// ScanJSON reads a JSON document: every object key, string and number's
// text. A document that does not parse is Unscanned.
func (s *Set) ScanJSON(doc []byte) Verdict {
	if s == nil {
		return Verdict{}
	}
	return s.scanDocument(doc)
}

// ScanText reads one text, such as an error's message.
func (s *Set) ScanText(text string) Verdict {
	if s == nil {
		return Verdict{}
	}
	if v, ok := s.match(text); ok {
		return v
	}
	return Verdict{State: Clean}
}
