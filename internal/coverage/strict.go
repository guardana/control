package coverage

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"slices"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"
)

// maxTextBytes bounds a name an inventory declares, as an observation bounds
// the names it copies.
const maxTextBytes = 256

// members is one JSON object's members by their exact spelling.
type members map[string]json.RawMessage

// strictJSON refuses what encoding/json reads past without a word: a member
// twice in one object, a null, and anything after the one value.
func strictJSON(b []byte) error {
	dec := json.NewDecoder(bytes.NewReader(b))
	dec.UseNumber()
	w := walker{}
	for {
		tok, err := dec.Token()
		if errors.Is(err, io.EOF) {
			if !w.done {
				return errors.New("not one whole JSON value")
			}
			return nil
		}
		if err != nil {
			return fmt.Errorf("not JSON at byte %d", dec.InputOffset())
		}
		if err := w.step(tok); err != nil {
			return fmt.Errorf("%w at byte %d", err, dec.InputOffset())
		}
	}
}

// walker follows the token stream: one frame per open object or array, and
// for an object the keys seen and whether a key comes next.
type walker struct {
	frames []*frame
	done   bool
}

type frame struct {
	keys    map[string]bool
	wantKey bool
}

func (w *walker) step(tok json.Token) error {
	if w.done {
		return errors.New("a value after the value")
	}
	if n := len(w.frames); n > 0 && w.frames[n-1].keys != nil && w.frames[n-1].wantKey {
		return w.key(tok)
	}
	switch tok {
	case json.Delim('{'):
		w.frames = append(w.frames, &frame{keys: map[string]bool{}, wantKey: true})
		return nil
	case json.Delim('['):
		w.frames = append(w.frames, &frame{})
		return nil
	case json.Delim(']'):
		w.frames = w.frames[:len(w.frames)-1]
	case nil:
		return errors.New("a null")
	}
	w.valueDone()
	return nil
}

func (w *walker) key(tok json.Token) error {
	top := w.frames[len(w.frames)-1]
	if tok == json.Delim('}') {
		w.frames = w.frames[:len(w.frames)-1]
		w.valueDone()
		return nil
	}
	k, _ := tok.(string)
	if top.keys[k] {
		return fmt.Errorf("member %s twice", strconv.QuoteToASCII(clip(k)))
	}
	top.keys[k] = true
	top.wantKey = false
	return nil
}

func (w *walker) valueDone() {
	if len(w.frames) == 0 {
		w.done = true
		return
	}
	if top := w.frames[len(w.frames)-1]; top.keys != nil {
		top.wantKey = true
	}
}

// object reads b, which strictJSON has accepted, as one object whose members
// are all among allowed.
func object(b []byte, allowed ...string) (members, error) {
	if len(b) == 0 || b[0] != '{' {
		return nil, errors.New("not an object")
	}
	m := members{}
	if err := json.Unmarshal(b, &m); err != nil {
		return nil, errors.New("not an object")
	}
	for k := range m {
		if !slices.Contains(allowed, k) {
			return nil, fmt.Errorf("unknown member %s", strconv.QuoteToASCII(clip(k)))
		}
	}
	return m, nil
}

// str reads member name as a string; absent reads as empty.
func (m members) str(name string) (string, error) {
	raw, ok := m[name]
	if !ok {
		return "", nil
	}
	var s string
	if len(raw) == 0 || raw[0] != '"' || json.Unmarshal(raw, &s) != nil {
		return "", fmt.Errorf("%s: not a string", name)
	}
	return s, nil
}

// list reads member name as an array; absent reads as none.
func (m members) list(name string) ([]json.RawMessage, error) {
	raw, ok := m[name]
	if !ok {
		return nil, nil
	}
	var l []json.RawMessage
	if len(raw) == 0 || raw[0] != '[' || json.Unmarshal(raw, &l) != nil {
		return nil, fmt.Errorf("%s: not a list", name)
	}
	return l, nil
}

// text refuses a name an operator could misread: empty or whitespace alone,
// longer than maxTextBytes, not UTF-8, or holding a control, format,
// separator, private-use or noncharacter code point or U+FFFD, which
// encoding/json leaves where the input held invalid UTF-8.
func text(s string) error {
	switch {
	case strings.TrimSpace(s) == "":
		return errors.New("empty")
	case len(s) > maxTextBytes:
		return fmt.Errorf("%d bytes, limit %d", len(s), maxTextBytes)
	case !utf8.ValidString(s):
		return errors.New("not UTF-8")
	}
	for _, r := range s {
		if unicode.In(r, unicode.Cc, unicode.Cf, unicode.Zl, unicode.Zp, unicode.Co, unicode.Noncharacter_Code_Point) ||
			r == utf8.RuneError {
			return fmt.Errorf("code point %U", r)
		}
	}
	return nil
}

// clip cuts what a refusal quotes back.
func clip(s string) string {
	if len(s) > 64 {
		return s[:64] + "..."
	}
	return s
}

// printable is s as a line can carry it: as it is when every rune prints,
// quoted in ASCII otherwise, so a name taken from a file cannot drive the
// terminal it is printed on.
func printable(s string) string {
	for _, r := range s {
		if !unicode.IsPrint(r) || r == utf8.RuneError {
			return strconv.QuoteToASCII(s)
		}
	}
	return s
}
