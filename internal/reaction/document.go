package reaction

import (
	"encoding/json"
	"errors"
	"fmt"
	"strconv"

	"github.com/guardana/control/internal/canon"
	"github.com/guardana/control/internal/policy/strictjson"
	"github.com/guardana/control/pkg/contract"
)

// MaxSerial is the largest serial and line number a document may name: the
// largest integer every JSON reader holds exactly.
const MaxSerial = 1<<53 - 1

const memberKind = "kind"

// docRefusals is the sentinel a body reader returns for each way the body
// fails as a document.
type docRefusals struct {
	json, repeated, kind, member Error
}

// readDoc reads raw as one JSON object of the given kind holding only the
// members named and every one of required. A member is known by its exact
// name and read from the bytes it was written as, so no decoder folds a case,
// keeps the last of two, or reads null as empty.
func readDoc(raw []byte, kind string, r docRefusals, names, required []string) (strictjson.Object, error) {
	o, err := readObject(raw, r)
	if err != nil {
		return nil, err
	}
	if k, ok := o[memberKind]; ok {
		if s, isString := strictjson.String(k); !isString || s != kind {
			return nil, r.kind
		}
	}
	if err := members(o, r, names, required); err != nil {
		return nil, err
	}
	return o, nil
}

func readObject(raw []byte, r docRefusals) (strictjson.Object, error) {
	o, err := strictjson.ReadObject(raw)
	switch {
	case errors.Is(err, strictjson.ErrRepeated):
		return nil, fmt.Errorf("%w: %w", r.repeated, err)
	case err != nil:
		return nil, fmt.Errorf("%w: %w", r.json, err)
	}
	return o, nil
}

func members(o strictjson.Object, r docRefusals, names, required []string) error {
	if err := o.Only(names...); err != nil {
		return fmt.Errorf("%w: %w", r.member, err)
	}
	if err := o.Require(required...); err != nil {
		return fmt.Errorf("%w: %w", r.member, err)
	}
	return nil
}

// canonicalOf is raw's canonical form. It refuses what the object reader
// passes in silence: invalid UTF-8, an unpaired surrogate, a duplicate below
// the top level and an integer outside the JSON-safe range. The canonical
// form's own text is dropped, since it can name a member by a pointer.
func canonicalOf(raw []byte, refusal Error) ([]byte, error) {
	out, err := canon.CanonicalizeJSON(raw)
	if err == nil {
		return out, nil
	}
	for _, cause := range []error{canon.ErrUnsupportedValue, canon.ErrTooDeep} {
		if errors.Is(err, cause) {
			return nil, fmt.Errorf("%w: %w", refusal, cause)
		}
	}
	return nil, fmt.Errorf("%w: not well-formed JSON", refusal)
}

// identifier reads raw as a string of 1 to limit bytes that the contract's
// identifier rule takes.
func identifier(raw json.RawMessage, limit int) (string, bool) {
	s, ok := strictjson.String(raw)
	return s, ok && s != "" && len(s) <= limit && contract.CheckIdentifier(s) == nil
}

// integer reads raw as an integer literal from lo to hi. A fraction or an
// exponent is refused, whatever its value.
func integer(raw json.RawMessage, lo, hi int64) (int64, bool) {
	n, err := strconv.ParseInt(string(raw), 10, 64)
	return n, err == nil && n >= lo && n <= hi
}
