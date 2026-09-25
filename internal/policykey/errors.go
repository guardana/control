package policykey

// Error is a refusal by this package, matched with errors.Is. They are
// constants, so no other code in the binary can reassign one and turn a
// refusal into a pass. No refusal quotes the bytes it refused: a key file's
// content never reaches an error's text.
type Error string

// Error returns the refusal's text.
func (e Error) Error() string { return string(e) }

// The key file's refusals, in the order ParsePrivate runs its checks.
const (
	ErrEmpty     Error = "policykey: the key file is empty"
	ErrPreamble  Error = "policykey: the key file holds something before its PEM block"
	ErrNotPEM    Error = "policykey: the key file holds no PEM block"
	ErrOpenSSH   Error = "policykey: an OpenSSH private key; a policy key is PKCS#8 PEM, as policy keygen writes it"
	ErrEncrypted Error = "policykey: an encrypted private key; a policy key is an unencrypted PKCS#8 key"
	ErrEC        Error = "policykey: an EC private key; a policy key is Ed25519"
	ErrRSA       Error = "policykey: an RSA private key; a policy key is Ed25519"
	ErrPublic    Error = "policykey: a public key; signing takes the private key"
	ErrBlockType Error = "policykey: the PEM block is not of type PRIVATE KEY"
	ErrHeaders   Error = "policykey: the PEM block carries headers"
	ErrTrailing  Error = "policykey: the key file holds more than white space after its PEM block"
	ErrPKCS8     Error = "policykey: the PEM block is not a PKCS#8 private key in the one encoding RFC 8410 gives an Ed25519 key"
	ErrKeyType   Error = "policykey: a PKCS#8 key of another algorithm; a policy key is Ed25519"
)

// The other refusals.
const (
	// ErrPublicLine is a policy.public_key value that is not the one spelling
	// of 32 bytes this package writes.
	ErrPublicLine Error = "policykey: want the 32 bytes of an ed25519 public key in standard base64, on one line"
	// ErrKeyFileMode is a key file whose mode gives the group or others any
	// permission at all.
	ErrKeyFileMode Error = "policykey: group or others have any permission on the key file; make it 0600 or 0400"
	// ErrKeyFileOwner is a key file another account owns.
	ErrKeyFileOwner Error = "policykey: the key file belongs to another account; a key is read only by the account that owns it"
	// ErrPlatform is a platform where a key file's mode cannot be checked.
	ErrPlatform Error = "policykey: this platform keeps no permission bits, so who can read a key file cannot be checked"
	// ErrKeyPairExists is a keygen output path that exists already.
	ErrKeyPairExists Error = "policykey: the path exists; keygen creates the directory and never writes into one"
	// ErrKeyDirMoved is a keygen directory the path no longer names once it
	// was created: it was replaced, moved or swapped for a link, or what the
	// path names holds entries or belongs to another account.
	ErrKeyDirMoved Error = "policykey: the path no longer names the directory keygen created; no key was written to what it names now"
	// ErrKeyText is a command line argument that holds key text or a control
	// character. Its text repeats nothing of the argument.
	ErrKeyText Error = "policykey: an argument holds a PEM marker, a key file's body line, a line break or another control character, " +
		"which no argument takes; the argument is not repeated here"
)
