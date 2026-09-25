package console

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"reflect"
	"strconv"
	"strings"
	"unicode/utf16"
	"unicode/utf8"

	"github.com/guardana/control/internal/keytext"
)

// route is one API path: the method it takes and what answers it. A write
// decodes its body into what newBody returns before it runs.
type route struct {
	method string
	serve  func(p *page, w http.ResponseWriter, r *http.Request, body any)
	// newBody is nil for a read, which takes no body.
	newBody func() any
}

// sessionBody is the empty object a trade of the printed token carries.
type sessionBody struct{}

var routes = map[string]route{
	SessionPath:    {http.MethodPost, (*page).startSession, func() any { return new(sessionBody) }},
	"/api/state":   {http.MethodGet, (*page).state, nil},
	"/api/approve": {http.MethodPost, (*page).approve, func() any { return new(answerBody) }},
	"/api/reject":  {http.MethodPost, (*page).reject, func() any { return new(answerBody) }},
	"/api/pause":   {http.MethodPost, (*page).pause, func() any { return new(pauseBody) }},
	"/api/unpause": {http.MethodPost, (*page).unpause, func() any { return new(unpauseBody) }},
}

func (p *page) serveAPI(w http.ResponseWriter, r *http.Request) {
	rt, ok := routes[r.URL.Path]
	if !ok {
		refuse(w, http.StatusNotFound, "not found")
		return
	}
	if r.Method != rt.method {
		w.Header().Set("Allow", rt.method)
		refuse(w, http.StatusMethodNotAllowed, "only "+rt.method+" is served here")
		return
	}
	if rt.newBody == nil {
		rt.serve(p, w, r, nil)
		return
	}
	body := rt.newBody()
	if status, msg := decodeBody(w, r, body); status != 0 {
		refuse(w, status, msg)
		return
	}
	rt.serve(p, w, r, body)
}

// decodeBody reads a write's body into v, or returns the status and the
// sentence that refuse it. JSON is required so that a form another site posts,
// which a browser sends without asking this page first, is refused whatever
// else it carries.
func decodeBody(w http.ResponseWriter, r *http.Request, v any) (int, string) {
	media, params, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil || media != "application/json" || !utf8Params(params) {
		return http.StatusUnsupportedMediaType, "a write takes application/json"
	}
	raw, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxBodyBytes))
	var tooLarge *http.MaxBytesError
	switch {
	case errors.As(err, &tooLarge):
		return http.StatusRequestEntityTooLarge, "the request is over its size bound"
	case err != nil:
		return http.StatusBadRequest, "the request could not be read"
	}
	// The decoder would put U+FFFD in place of a byte that is not UTF-8, and
	// the store would then keep a value nobody sent.
	if !utf8.Valid(raw) {
		return http.StatusBadRequest, "the request is not valid UTF-8"
	}
	// An escape of half a UTF-16 pair is ASCII, so it passes the check above,
	// and the decoder would put U+FFFD in its place just the same.
	if loneSurrogate(raw) {
		return http.StatusBadRequest, "the request holds a \\u escape of half a character that no pair completes"
	}
	if err := decodeStrict(raw, v); err != nil {
		return http.StatusBadRequest, "the request is not one JSON object of the members this write takes: " + err.Error()
	}
	return 0, ""
}

// loneSurrogate reports whether raw holds a \u escape of a UTF-16 surrogate
// that is not a high half followed at once by an escape of a low one. In
// JSON a backslash starts an escape and appears nowhere else, so reading the
// escapes in order is reading every one.
func loneSurrogate(raw []byte) bool {
	for i := 0; i < len(raw); i++ {
		if raw[i] != '\\' {
			continue
		}
		i++
		r, ok := unicodeEscape(raw, i)
		if !ok || !utf16.IsSurrogate(r) {
			continue
		}
		low, ok := unicodeEscape(raw, i+6)
		if lowHalf(r) || !ok || raw[i+5] != '\\' || !lowHalf(low) {
			return true
		}
		i += 10
	}
	return false
}

// lowHalf reports whether r is the second half of a UTF-16 pair.
func lowHalf(r rune) bool { return 0xDC00 <= r && r <= 0xDFFF }

// unicodeEscape reads the escape whose u stands at raw[at] and the four hex
// digits after it, or reports that there is none.
func unicodeEscape(raw []byte, at int) (rune, bool) {
	if at+5 > len(raw) || raw[at] != 'u' {
		return 0, false
	}
	n, err := strconv.ParseUint(string(raw[at+1:at+5]), 16, 16)
	return rune(n), err == nil
}

// utf8Params allows no parameter but a UTF-8 charset, the one encoding JSON
// is read in.
func utf8Params(params map[string]string) bool {
	for k, v := range params {
		if k != "charset" || (v != "utf-8" && v != "UTF-8") {
			return false
		}
	}
	return true
}

// decodeStrict reads exactly one JSON object into v, refusing a member v does
// not name exactly, a member named twice and anything after the object.
func decodeStrict(raw []byte, v any) error {
	if err := exactMembers(raw, reflect.TypeOf(v)); err != nil {
		return err
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		return err
	}
	if _, err := dec.Token(); !errors.Is(err, io.EOF) {
		return errors.New("text after the object")
	}
	return nil
}

// exactMembers refuses an object, at any depth, holding a member its type
// does not spell exactly, or one member twice. The decoder matches a member to
// a field ignoring case and keeps the last match, so {"id":"A","ID":"B"}
// would decode as B, and a reader of the same bytes that took the first would
// act on another request.
func exactMembers(raw []byte, t reflect.Type) error {
	return walkValue(json.NewDecoder(bytes.NewReader(raw)), t)
}

func walkValue(dec *json.Decoder, t reflect.Type) error {
	for t.Kind() == reflect.Pointer {
		t = t.Elem()
	}
	tok, err := dec.Token()
	if err != nil {
		return err
	}
	switch tok {
	case json.Delim('{'):
		if t.Kind() != reflect.Struct {
			return errors.New("an object where this write takes none")
		}
		if err := walkMembers(dec, t); err != nil {
			return err
		}
	case json.Delim('['):
		return errors.New("an array where this write takes none")
	default:
		return nil
	}
	_, err = dec.Token()
	return err
}

func walkMembers(dec *json.Decoder, t reflect.Type) error {
	fields := memberTypes(t)
	seen := map[string]bool{}
	for dec.More() {
		key, err := dec.Token()
		if err != nil {
			return err
		}
		name, _ := key.(string)
		ft, ok := fields[name]
		switch {
		case !ok:
			return fmt.Errorf("the member %q is not one this write takes", name)
		case seen[name]:
			return errors.New("a member named twice")
		}
		seen[name] = true
		if err := walkValue(dec, ft); err != nil {
			return err
		}
	}
	return nil
}

// memberTypes maps each member name t's JSON tags spell to its field's type.
func memberTypes(t reflect.Type) map[string]reflect.Type {
	out := map[string]reflect.Type{}
	for f := range t.Fields() {
		name, _, _ := strings.Cut(f.Tag.Get("json"), ",")
		if name != "" && name != "-" {
			out[name] = f.Type
		}
	}
	return out
}

// refuse answers a refusal as one JSON sentence, printable as the commands
// print theirs: a refusal may repeat a name a request or a file chose.
func refuse(w http.ResponseWriter, status int, msg string) {
	reply(w, status, struct {
		Error string `json:"error"`
	}{keytext.Printable(msg)})
}

// reply writes v as JSON. The encoder escapes <, > and &, so no value the
// page shows is markup even to a reader that ignores the content type.
func reply(w http.ResponseWriter, status int, v any) {
	body, err := json.Marshal(v)
	if err != nil {
		status = http.StatusInternalServerError
		body = []byte(`{"error":"the answer could not be encoded"}`)
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_, _ = w.Write(append(body, '\n'))
}
