package gatewayconfig

import (
	"fmt"
	"net/textproto"
	"strings"
)

// nameBytes are the bytes of an HTTP token, which is what a header name is.
const nameBytes = "!#$%&'*+-.^_`|~0123456789ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz"

// canonicalKey is path with a map key's leaf spelled as HTTP compares header
// names, so two spellings of one header are one key: the file cannot set it
// twice, and a variable replaces it however either spells it.
func canonicalKey(path string) (string, error) {
	for _, m := range mapFields {
		if name, ok := strings.CutPrefix(path, m.prefix); ok {
			canonical, err := headerName(m.prefix, name)
			return m.prefix + canonical, err
		}
	}
	return path, nil
}

// headerName is the canonical spelling of the leaf name under the map key
// prefix, or the reason it names no header. A leaf of digits alone is what a
// list under the key arrives as.
func headerName(prefix, name string) (string, error) {
	key := strings.TrimSuffix(prefix, ".")
	first, _, _ := strings.Cut(name, ".")
	switch {
	case name == "":
		return "", fmt.Errorf("%s names no header", key)
	case first != "" && strings.Trim(first, "0123456789") == "":
		return "", fmt.Errorf("%s takes a mapping from header names to values, not a list", key)
	case strings.Contains(name, "."):
		return "", fmt.Errorf("%s%s names no single header", prefix, name)
	case strings.Trim(name, nameBytes) != "":
		return "", fmt.Errorf("%s: %s is not a header name", key, quoteValue(name))
	case strings.Contains(name, "_"):
		return "", fmt.Errorf("%s: %s holds _, which no variable can name, since a variable's _ stands for -", key, quoteValue(name))
	}
	return textproto.CanonicalMIMEHeaderKey(name), nil
}
