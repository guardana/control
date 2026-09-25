package policykey

import (
	"bytes"
	"crypto/ecdsa"
	"crypto/ed25519"
	"crypto/rsa"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"fmt"
	"io/fs"
	"os"

	"github.com/guardana/control/internal/files"
	"github.com/guardana/control/internal/keytext"
)

// MaxKeyFileBytes bounds a key file. A PKCS#8 Ed25519 key in PEM is 119
// bytes; the bound leaves room for white space and refuses anything that is
// plainly something else before it is read.
const MaxKeyFileBytes = 4096

// effectiveUID names the account a key file must belong to. It is a variable
// so a test can name another account and see a file of its own refused, which
// otherwise takes root to arrange.
var effectiveUID = os.Geteuid

// privateBlock is the one PEM type a key file may hold.
const privateBlock = "PRIVATE KEY"

// PKCS8Prefix is the DER RFC 8410 puts before the 32-byte seed of every
// Ed25519 key in PKCS#8.
const PKCS8Prefix = keytext.PKCS8Prefix

// namedBlocks are the block types a stranger is likely to try, each refused
// with a sentence of its own.
var namedBlocks = map[string]Error{
	"OPENSSH PRIVATE KEY":   ErrOpenSSH,
	"ENCRYPTED PRIVATE KEY": ErrEncrypted,
	"EC PRIVATE KEY":        ErrEC,
	"RSA PRIVATE KEY":       ErrRSA,
	"PUBLIC KEY":            ErrPublic,
	"RSA PUBLIC KEY":        ErrPublic,
}

// MarshalPrivate is the key file: one PEM block of type PRIVATE KEY holding
// the key in PKCS#8, as RFC 8410 encodes an Ed25519 key.
func MarshalPrivate(key ed25519.PrivateKey) ([]byte, error) {
	if len(key) != ed25519.PrivateKeySize {
		return nil, fmt.Errorf("policykey: a private key of %d bytes, want %d", len(key), ed25519.PrivateKeySize)
	}
	der, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		return nil, fmt.Errorf("policykey: encoding the private key: %w", err)
	}
	return pem.EncodeToMemory(&pem.Block{Type: privateBlock, Bytes: der}), nil
}

// ParsePrivate reads a key file's bytes: exactly one PEM block of type
// PRIVATE KEY, with no headers, nothing before it and only white space after
// it, holding an Ed25519 key in PKCS#8. Every refusal is one of this package's
// constants, returned bare, so none can carry a byte of what it refused.
func ParsePrivate(raw []byte) (ed25519.PrivateKey, error) {
	if len(raw) == 0 {
		return nil, ErrEmpty
	}
	// pem.Decode skips whatever precedes a block, and passes over a block it
	// cannot decode to reach a later one; a file that opens with anything but
	// the block, or whose decoded block is not its first, is refused here.
	if !bytes.HasPrefix(raw, []byte("-----BEGIN ")) {
		return nil, ErrPreamble
	}
	block, rest := pem.Decode(raw)
	if block == nil {
		return nil, ErrNotPEM
	}
	defer clear(block.Bytes)
	if consumed := raw[:len(raw)-len(rest)]; bytes.Count(consumed, []byte("-----BEGIN ")) != 1 {
		return nil, ErrPreamble
	}
	if err := checkBlock(block, rest); err != nil {
		return nil, err
	}
	return parsePKCS8(block.Bytes)
}

// checkBlock refuses a block of another type, one with headers, and anything
// but white space after it.
func checkBlock(block *pem.Block, rest []byte) error {
	if block.Type != privateBlock {
		if named, ok := namedBlocks[block.Type]; ok {
			return named
		}
		return ErrBlockType
	}
	if len(block.Headers) != 0 {
		return ErrHeaders
	}
	if len(bytes.TrimLeft(rest, " \t\r\n")) != 0 {
		return ErrTrailing
	}
	return nil
}

// parsePKCS8 reads the block's payload as an Ed25519 key in the one encoding
// MarshalPrivate writes, naming the other key types a stranger may hand it.
func parsePKCS8(der []byte) (ed25519.PrivateKey, error) {
	parsed, err := x509.ParsePKCS8PrivateKey(der)
	if err != nil {
		return nil, ErrPKCS8
	}
	switch key := parsed.(type) {
	case ed25519.PrivateKey:
		// The parser ignores bytes after the key's own SEQUENCE; one key has
		// one file, so the payload must be the encoding MarshalPrivate writes.
		again, err := x509.MarshalPKCS8PrivateKey(key)
		same := err == nil && bytes.Equal(again, der)
		clear(again)
		if !same {
			clear(key)
			return nil, ErrPKCS8
		}
		return key, nil
	case *ecdsa.PrivateKey:
		return nil, ErrEC
	case *rsa.PrivateKey:
		return nil, ErrRSA
	default:
		return nil, ErrKeyType
	}
}

// ReadPrivate reads and parses the key file at path. The file is judged from
// its opened descriptor: a regular file the effective user owns, at most
// MaxKeyFileBytes, with no permission bit for the group or for others. A
// refusal names the check and neither the path nor the content, because a
// path argument may itself be key text; the caller names its flag.
func ReadPrivate(path string) (ed25519.PrivateKey, error) {
	if err := CheckPlatform(); err != nil {
		return nil, err
	}
	raw, err := files.ReadOwned(path, MaxKeyFileBytes, 0o077, effectiveUID())
	defer clear(raw)
	if err != nil {
		return nil, keyFileRefusal(err)
	}
	return ParsePrivate(raw)
}

// keyFileRefusal is err without the path: an error naming the file it was
// given carries only its cause.
func keyFileRefusal(err error) error {
	var pathErr *fs.PathError
	switch {
	case errors.Is(err, files.ErrMode):
		return fmt.Errorf("%w (%w)", ErrKeyFileMode, err)
	case errors.Is(err, files.ErrOwner):
		return fmt.Errorf("%w (%w)", ErrKeyFileOwner, err)
	case errors.As(err, &pathErr):
		return fmt.Errorf("the key file: %w", pathErr.Err)
	}
	return fmt.Errorf("the key file: %w", err)
}
