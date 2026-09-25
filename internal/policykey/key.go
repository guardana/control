package policykey

import (
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"strings"

	"github.com/guardana/control/internal/files"
)

// The names keygen gives the two halves inside the directory it creates.
const (
	PrivateFile = "signing.key"
	PublicFile  = "signing.pub"
)

// keyIDPrefix names the algorithm; the hex after it is a hint, never trusted.
const keyIDPrefix = "ed25519-"

// KeyID is "ed25519-" and the first 16 lowercase hex digits of SHA-256 over
// the 32 raw bytes of pub. A key of any other size has no id, and the empty id
// selects no key anywhere.
func KeyID(pub ed25519.PublicKey) string {
	if len(pub) != ed25519.PublicKeySize {
		return ""
	}
	sum := sha256.Sum256(pub)
	return keyIDPrefix + hex.EncodeToString(sum[:8])
}

// FormatPublic is the public key line: standard base64 of the 32 raw bytes.
func FormatPublic(pub ed25519.PublicKey) string {
	return base64.StdEncoding.EncodeToString(pub)
}

// ParsePublic reads a public key line. It accepts exactly the spelling
// FormatPublic writes, alone or with the one newline PublicFile ends in: the
// standard decoder skips line breaks and accepts stray padding bits, so a
// value is taken only when it re-encodes to itself.
func ParsePublic(line string) (ed25519.PublicKey, error) {
	line = strings.TrimSuffix(line, "\n")
	key, err := base64.StdEncoding.DecodeString(line)
	if err != nil || len(key) != ed25519.PublicKeySize || base64.StdEncoding.EncodeToString(key) != line {
		return nil, ErrPublicLine
	}
	return ed25519.PublicKey(key), nil
}

// ConfigLines is what keygen prints: the two members the configuration's
// policy group takes, one per line, as `name: value`.
func ConfigLines(pub ed25519.PublicKey) string {
	return "key_id: " + KeyID(pub) + "\npublic_key: " + FormatPublic(pub) + "\n"
}

// CheckPlatform refuses a platform where a key file's permission bits cannot
// be read. Both commands call it before touching a file.
func CheckPlatform() error {
	if !files.PermissionBits {
		return ErrPlatform
	}
	return nil
}
