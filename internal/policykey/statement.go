package policykey

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"syscall"
	"unicode/utf8"

	"github.com/guardana/control/internal/canon"
	"github.com/guardana/control/internal/files"
	"github.com/guardana/control/internal/policy"
	"github.com/guardana/control/internal/policy/bundle"
	"github.com/guardana/control/internal/policy/strictjson"
)

// MaxStatementFileBytes bounds a statement file, the envelope and all. It is
// the bound a statement read from a file meets first: ADR-0038 gives the body
// and the file one bound, and the base64 body of a file within it is well
// inside policy.MaxStatementBytes.
const MaxStatementFileBytes = policy.MaxStatementBytes

// The members of the DSSE envelope and of each of its signatures, each
// required.
const (
	memberPayloadType = "payloadType"
	memberPayload     = "payload"
	memberSignatures  = "signatures"
	memberKeyID       = "keyid"
	memberSig         = "sig"
)

// ParseStatement reads a statement file's bytes as a DSSE JSON envelope and
// returns its parts, checking no signature: policy.VerifyStatement does, and
// refuses another payload type or any number of signatures but one. The file
// is at most MaxStatementFileBytes, one strict JSON object whose members and
// whose signatures' members are exactly the envelope's, none named twice, and
// payload and sig are standard base64 in the one spelling they encode back
// to.
func ParseStatement(raw []byte) (policy.StatementEnvelope, error) {
	if n := len(raw); n > MaxStatementFileBytes {
		return policy.StatementEnvelope{}, fmt.Errorf("%w: %d bytes, limit %d", ErrStatementFileTooLarge, n, MaxStatementFileBytes)
	}
	if !utf8.Valid(raw) {
		return policy.StatementEnvelope{}, fmt.Errorf("%w: invalid UTF-8", ErrStatementEnvelope)
	}
	top, err := members(raw, memberPayloadType, memberPayload, memberSignatures)
	if err != nil {
		return policy.StatementEnvelope{}, err
	}
	var env policy.StatementEnvelope
	if env.PayloadType, err = text(top[memberPayloadType]); err != nil {
		return policy.StatementEnvelope{}, err
	}
	if env.Payload, err = base64Value(top[memberPayload]); err != nil {
		return policy.StatementEnvelope{}, err
	}
	if env.Signatures, err = signatures(top[memberSignatures]); err != nil {
		return policy.StatementEnvelope{}, err
	}
	// The canonical form refuses an unpaired surrogate, which the decoder
	// reads as U+FFFD without a word.
	if _, err := canon.CanonicalizeJSON(raw); err != nil {
		return policy.StatementEnvelope{}, fmt.Errorf("%w: not strict JSON", ErrStatementEnvelope)
	}
	return env, nil
}

// members reads raw as one JSON object holding exactly the members named,
// each value kept as the bytes it was written as.
func members(raw []byte, names ...string) (strictjson.Object, error) {
	o, err := strictjson.ReadObject(raw)
	switch {
	case errors.Is(err, strictjson.ErrRepeated):
		return nil, fmt.Errorf("%w: %w", ErrStatementEnvelopeRepeated, err)
	case err != nil:
		return nil, fmt.Errorf("%w: %w", ErrStatementEnvelope, err)
	}
	if err := o.Only(names...); err != nil {
		return nil, fmt.Errorf("%w: %w", ErrStatementEnvelopeMember, err)
	}
	if err := o.Require(names...); err != nil {
		return nil, fmt.Errorf("%w: %w", ErrStatementEnvelopeMember, err)
	}
	return o, nil
}

// signatures reads the array of signatures, each an object of exactly keyid
// and sig.
func signatures(raw json.RawMessage) ([]policy.StatementSignature, error) {
	if len(raw) == 0 || raw[0] != '[' {
		return nil, fmt.Errorf("%w: signatures is not an array", ErrStatementEnvelope)
	}
	var items []json.RawMessage
	if err := json.Unmarshal(raw, &items); err != nil {
		return nil, fmt.Errorf("%w: signatures is not an array", ErrStatementEnvelope)
	}
	out := make([]policy.StatementSignature, 0, len(items))
	for _, item := range items {
		sig, err := members(item, memberKeyID, memberSig)
		if err != nil {
			return nil, err
		}
		keyID, err := text(sig[memberKeyID])
		if err != nil {
			return nil, err
		}
		signature, err := base64Value(sig[memberSig])
		if err != nil {
			return nil, err
		}
		out = append(out, policy.StatementSignature{KeyID: keyID, Signature: signature})
	}
	return out, nil
}

// text reads raw as a JSON string and nothing else, null included.
func text(raw json.RawMessage) (string, error) {
	s, ok := strictjson.String(raw)
	if !ok {
		return "", fmt.Errorf("%w: a value that is not a string", ErrStatementEnvelope)
	}
	return s, nil
}

// base64Value reads raw as a string of standard base64. The decoder skips
// line breaks and ignores stray padding bits, so a value is taken only when
// it encodes back to itself.
func base64Value(raw json.RawMessage) ([]byte, error) {
	s, err := text(raw)
	if err != nil {
		return nil, err
	}
	decoded, err := base64.StdEncoding.DecodeString(s)
	if err != nil || base64.StdEncoding.EncodeToString(decoded) != s {
		return nil, ErrStatementBase64
	}
	return decoded, nil
}

// envelopeJSON is the file's shape, members in the order DSSE lists them.
type envelopeJSON struct {
	PayloadType string          `json:"payloadType"`
	Payload     string          `json:"payload"`
	Signatures  []signatureJSON `json:"signatures"`
}

type signatureJSON struct {
	KeyID string `json:"keyid"`
	Sig   string `json:"sig"`
}

// MarshalStatement is the statement file of env, ending in a newline. It
// refuses a file over MaxStatementFileBytes, or one ParseStatement would not
// read back, so nothing it returns is refused by a reader for its form.
func MarshalStatement(env policy.StatementEnvelope) ([]byte, error) {
	out := envelopeJSON{
		PayloadType: env.PayloadType,
		Payload:     base64.StdEncoding.EncodeToString(env.Payload),
		Signatures:  make([]signatureJSON, 0, len(env.Signatures)),
	}
	for _, s := range env.Signatures {
		out.Signatures = append(out.Signatures, signatureJSON{KeyID: s.KeyID, Sig: base64.StdEncoding.EncodeToString(s.Signature)})
	}
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(out); err != nil {
		return nil, fmt.Errorf("policykey: encoding the statement: %w", err)
	}
	raw := buf.Bytes()
	if _, err := ParseStatement(raw); err != nil {
		return nil, err
	}
	return raw, nil
}

// IsStatement reports whether raw is a statement file: ParseStatement reads
// it and its payload type is the statement's. It checks no signature; it
// tells a statement from another file, a key or a bundle among them.
func IsStatement(raw []byte) bool {
	env, err := ParseStatement(raw)
	return err == nil && env.PayloadType == bundle.StatementPayloadType
}

// ReadStatement reads the statement file at path, a regular file of at most
// MaxStatementFileBytes, and parses it.
func ReadStatement(path string) (policy.StatementEnvelope, error) {
	raw, err := files.ReadRegular(filepath.Clean(path), MaxStatementFileBytes, 0)
	if err != nil {
		return policy.StatementEnvelope{}, fmt.Errorf("the statement file: %w", err)
	}
	return ParseStatement(raw)
}

// WriteStatement writes env to path with mode 0644, a statement being public,
// through a temporary file and a rename, so a reader sees the old file or the
// new. It replaces only a path that is absent or already a regular file
// holding a freshness statement, and refuses anything else there, a key, a
// bundle, a directory and a link among them, with ErrStatementOut. An
// envelope of another payload type is policy.ErrStatementPayloadType.
func WriteStatement(path string, env policy.StatementEnvelope) error {
	if env.PayloadType != bundle.StatementPayloadType {
		return policy.ErrStatementPayloadType
	}
	raw, err := MarshalStatement(env)
	if err != nil {
		return err
	}
	path = filepath.Clean(path)
	root, err := os.OpenRoot(filepath.Dir(path))
	if err != nil {
		return err
	}
	name := filepath.Base(path)
	err = checkStatementOut(root, name)
	if err == nil {
		err = files.ReplaceIn(root, name, raw, 0o644)
	}
	return errors.Join(err, root.Close())
}

// checkStatementOut refuses a name in root that holds anything but a
// statement. It judges through the handle the replace goes through, so a
// directory swapped in after the judgement receives nothing; the file judged
// is the one opened, which neither follows a link nor waits on a pipe. A file
// put under the name between the judgement and the rename is not seen: a
// rename cannot compare what it replaces.
func checkStatementOut(root *os.Root, name string) error {
	info, err := root.Lstat(name)
	switch {
	case errors.Is(err, fs.ErrNotExist):
		return nil
	case err != nil:
		return err
	case !info.Mode().IsRegular():
		return fmt.Errorf("%s: %w", name, ErrStatementOut)
	}
	f, err := root.OpenFile(name, os.O_RDONLY|syscall.O_NONBLOCK, 0)
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }()
	held, err := f.Stat()
	if err != nil {
		return err
	}
	if !held.Mode().IsRegular() || !os.SameFile(info, held) {
		return fmt.Errorf("%s: %w: it changed while it was judged", name, ErrStatementOut)
	}
	existing, err := io.ReadAll(io.LimitReader(f, MaxStatementFileBytes+1))
	if err != nil {
		return err
	}
	if !IsStatement(existing) {
		return fmt.Errorf("%s: %w", name, ErrStatementOut)
	}
	return nil
}
