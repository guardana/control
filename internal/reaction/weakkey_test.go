package reaction_test

import (
	"bytes"
	"crypto/ed25519"
	"encoding/base64"
	"fmt"
	"strings"
	"testing"

	"github.com/guardana/control/internal/reaction"
)

// smallOrderKeys are encodings of points of small order, built byte by byte:
// the identity (y = 1), y = 0, y = p-1, and the identity with its sign bit
// set. Under each a forged signature verifies for some or every message.
func smallOrderKeys() []ed25519.PublicKey {
	identity := make([]byte, ed25519.PublicKeySize)
	identity[0] = 1
	signed := bytes.Clone(identity)
	signed[ed25519.PublicKeySize-1] |= 0x80
	pMinusOne := append(append([]byte{0xec}, bytes.Repeat([]byte{0xff}, 30)...), 0x7f)
	return []ed25519.PublicKey{identity, make([]byte, ed25519.PublicKeySize), pMinusOne, signed}
}

func routeNamingLiftKey(pub ed25519.PublicKey) []byte {
	return []byte(strings.ReplaceAll(routeTemplate, "LIFTKEY", base64.StdEncoding.EncodeToString(pub)))
}

// A route whose lift key is of small order is refused when it is read, and
// one naming a usable key is taken.
func TestParseRouteRefusesALiftKeyOfSmallOrder(t *testing.T) {
	for _, key := range smallOrderKeys() {
		_, err := reaction.ParseRoute(routeNamingLiftKey(key))
		expectOnly(t, fmt.Sprintf("lift key %x", key), err, reaction.ErrRouteValue, routeRefusals())
	}
	for _, key := range []ed25519.PublicKey{pubOf(liftKey()), pubOf(seededKey(0))} {
		r, err := reaction.ParseRoute(routeNamingLiftKey(key))
		if err != nil || !bytes.Equal(r.LiftKey(), key) {
			t.Errorf("lift key %x: %x, %v", key, r.LiftKey(), err)
		}
	}
}
