package canon

import (
	"fmt"
	"strings"
	"unicode/utf8"
)

// walker carries the path to the value being visited, so a refusal can name it.
type walker struct {
	path []string
}

func (w *walker) push(token string) { w.path = append(w.path, token) }
func (w *walker) pop()              { w.path = w.path[:len(w.path)-1] }

// A member name is caller content with no length limit of its own, and a
// refusal travels into logs and evidence. So a pointer keeps at most
// maxTokenBytes of each name and the names that fit in maxPointerBytes, and
// marks each cut with cutMarker: a tilde followed by anything but 0 or 1 occurs
// in no RFC 6901 pointer, so the marker never reads as part of a name. A
// pointer is therefore at most 261 bytes before quoting, the cap plus a
// separator and the marker. Refusals quote it with %q, which renders a control
// byte or DEL of a name as four bytes, so the quoted pointer is about four
// times that, and a consumer sizing a field for a refusal allows for it.
const (
	maxTokenBytes   = 64
	maxPointerBytes = 256
	cutMarker       = "~..."
)

// pointer renders the path as an RFC 6901 JSON pointer. The root is the empty
// pointer, so callers quote the result to keep it visible in a message.
func (w *walker) pointer() string {
	var b strings.Builder
	for _, token := range w.path {
		rendered := "/" + pointerToken(token)
		if b.Len()+len(rendered) > maxPointerBytes {
			b.WriteString("/" + cutMarker)
			break
		}
		b.WriteString(rendered)
	}
	return b.String()
}

// pointerToken escapes one name, cut first if it is too long: on a character
// boundary, so the message stays valid UTF-8, and before escaping, so no
// escape is split.
func pointerToken(token string) string {
	cut := len(token) > maxTokenBytes
	if cut {
		end := maxTokenBytes
		for end > 0 && !utf8.RuneStart(token[end]) {
			end--
		}
		token = token[:end]
	}
	escaped := strings.ReplaceAll(strings.ReplaceAll(token, "~", "~0"), "/", "~1")
	if cut {
		escaped += cutMarker
	}
	return escaped
}

func (w *walker) unsupported(detail string) error {
	return fmt.Errorf("%w at %q: %s", ErrUnsupportedValue, w.pointer(), detail)
}

func (w *walker) tooDeep(level int) error {
	return fmt.Errorf("%w at %q: %d containers, limit %d", ErrTooDeep, w.pointer(), level, maxDepth)
}
