package policy

import (
	"bytes"
	"crypto/ed25519"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"
	"time"

	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"

	controlv1 "github.com/guardana/control/api/gen/go/guardana/control/v1"
	"github.com/guardana/control/internal/policy/bundle"
	"github.com/guardana/control/internal/policy/match"
	"github.com/guardana/control/internal/policy/rules"
)

// exampleDigest is the digest of the example's canonical form, computed
// outside Go.
const exampleDigest = "sha256:040d29038eaa67e07d4e99953eaad6456f25bebe0ad05a15412ec259e4717e15"

func when() time.Time { return time.Date(2026, time.September, 11, 12, 0, 0, 0, time.UTC) }

// fixture is the example document as its author wrote it, a key, the keyring
// that pins its public half as k1, and the example signed by it.
type fixture struct {
	raw     []byte
	private ed25519.PrivateKey
	keys    bundle.Keyring
	signed  *controlv1.PolicyBundle
}

func newFixture(t *testing.T) fixture {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("..", "..", "testdata", "policy", "documents", "example.json"))
	if err != nil {
		t.Fatal(err)
	}
	private := ed25519.NewKeyFromSeed(bytes.Repeat([]byte{1}, ed25519.SeedSize))
	signed, err := Sign(raw, private, "k1")
	if err != nil {
		t.Fatal(err)
	}
	public := ed25519.PublicKey(slices.Clone(private[ed25519.SeedSize:]))
	return fixture{raw: raw, private: private, keys: bundle.Keyring{"k1": public}, signed: signed}
}

func refuse(*rules.Document) (*match.Program, error) { return nil, errRefused }

var errRefused = errors.New("the test's compiler refuses")

// TestLoadWrapsOnlyAProgramTheCompilerReturned: a compiler that refuses, and
// one that returns neither a program nor a refusal, each leave Load with no
// snapshot and the document check's refusal. The real compiler is the twin.
func TestLoadWrapsOnlyAProgramTheCompilerReturned(t *testing.T) {
	f := newFixture(t)
	snap, err := load(f.signed, f.keys, when(), refuse)
	if snap != nil || !errors.Is(err, ErrDocument) || !errors.Is(err, errRefused) {
		t.Errorf("a compiler that refuses: (%v, %v), want no snapshot and %q wrapping its refusal", snap, err, ErrDocument)
	}
	nothing := func(*rules.Document) (*match.Program, error) { return nil, nil }
	snap, err = load(f.signed, f.keys, when(), nothing)
	if snap != nil || !errors.Is(err, ErrDocument) {
		t.Errorf("a compiler that returns nothing: (%v, %v), want no snapshot and %q", snap, err, ErrDocument)
	}
	snap, err = load(f.signed, f.keys, when(), match.Compile)
	if err != nil || snap == nil || snap.program == nil {
		t.Fatalf("the real compiler: (%v, %v), want a snapshot around its program", snap, err)
	}
}

// TestTheCompilerRunsInTheDocumentCheck: compiling is part of the check that
// canonical is a document. It runs on verified bytes only, and its refusal
// comes before the canonical-form check's.
func TestTheCompilerRunsInTheDocumentCheck(t *testing.T) {
	f := newFixture(t)
	called := false
	spy := func(doc *rules.Document) (*match.Program, error) {
		called = true
		return match.Compile(doc)
	}
	broken := proto.CloneOf(f.signed)
	broken.Signature[0] ^= 1
	if _, err := load(broken, f.keys, when(), spy); !errors.Is(err, ErrSignature) || called {
		t.Errorf("a broken signature: err %v, compiler called %v; want %q and no call", err, called, ErrSignature)
	}

	signature, err := bundle.SignBytes(f.raw, f.private)
	if err != nil {
		t.Fatal(err)
	}
	authorsSpelling := &controlv1.PolicyBundle{
		Ref:          &controlv1.PolicyBundleRef{BundleId: "payments", Version: "2026-09-10.1", Digest: bundle.Digest(f.raw)},
		Canonical:    f.raw,
		SignatureAlg: bundle.SignatureAlg, Signature: signature, KeyId: "k1", MaxStaleSeconds: 300,
	}
	if _, err := load(authorsSpelling, f.keys, when(), match.Compile); !errors.Is(err, ErrNotCanonical) {
		t.Fatalf("the premise: the author's spelling, signed, gives %v, want %q", err, ErrNotCanonical)
	}
	if _, err := load(authorsSpelling, f.keys, when(), refuse); !errors.Is(err, ErrDocument) || errors.Is(err, ErrNotCanonical) {
		t.Errorf("the author's spelling and a refusing compiler: %v, want %q alone", err, ErrDocument)
	}
}

// TestLoadReadsTheCallersMessageOnce rewrites the caller's message from inside
// Load: the compiler runs after the signature check and the parse, and before
// every check still to come. None of those writes reaches a check or the
// snapshot, because Load read the message once, on entry.
func TestLoadReadsTheCallersMessageOnce(t *testing.T) {
	f := newFixture(t)
	b := f.signed
	rewrite := func(doc *rules.Document) (*match.Program, error) {
		copy(b.Canonical, bytes.Repeat([]byte{' '}, len(b.Canonical)))
		b.Ref.BundleId, b.Ref.Version, b.Ref.Digest = "other", "other", "other"
		b.Ref.CreatedAt = timestamppb.New(when())
		b.MaxStaleSeconds = 1
		return match.Compile(doc)
	}
	snap, err := load(b, f.keys, when(), rewrite)
	if err != nil {
		t.Fatalf("a write to the caller's message during Load reached a check: %v", err)
	}
	want := &controlv1.PolicyBundleRef{BundleId: "payments", Version: "2026-09-10.1", Digest: exampleDigest}
	if !proto.Equal(snap.Ref(), want) || snap.MaxStale() != 5*time.Minute || snap.Serial() != 7 {
		t.Errorf("Ref %v, budget %v, serial %d; want %v, 5m0s, 7", snap.Ref(), snap.MaxStale(), snap.Serial(), want)
	}
}

// TestARefreshKeepsTheProgram: the same digest again makes a new snapshot
// around the program already compiled for it, and leaves the old snapshot as
// it was. A replacement brings its own program.
func TestARefreshKeepsTheProgram(t *testing.T) {
	f := newFixture(t)
	var h Holder
	if err := h.Install(f.signed, f.keys, when()); err != nil {
		t.Fatal(err)
	}
	first := h.Current()
	if err := h.Install(f.signed, f.keys, when().Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	second := h.Current()
	if second == first || second.program != first.program {
		t.Fatalf("refresh: new snapshot %v, same program %v; want both", second != first, second.program == first.program)
	}
	if !first.confirmedAt.Equal(when()) || !second.confirmedAt.Equal(when().Add(time.Minute)) {
		t.Fatalf("confirmed at %v then %v", first.confirmedAt, second.confirmedAt)
	}
	higher, err := Sign([]byte(strings.Replace(string(f.raw), `"serial": 7`, `"serial": 8`, 1)), f.private, "k1")
	if err != nil {
		t.Fatal(err)
	}
	if err := h.Install(higher, f.keys, when().Add(2*time.Minute)); err != nil {
		t.Fatal(err)
	}
	if h.Current().program == first.program {
		t.Fatal("a replacement kept the program of the bundle it replaced")
	}
}

// TestASecondInstallWaitsForTheFirst holds one install inside Load and starts
// a second. Serialized, the second cannot finish while the first is held, and
// once the first is let go the two land in the order they took the lock. The
// wait is a bounded run of yields, not a timer: long enough for an install
// nothing holds back to finish many times over, and unable to fail on a
// holder that serializes, since there the second install is blocked however
// long the loop runs.
func TestASecondInstallWaitsForTheFirst(t *testing.T) {
	f := newFixture(t)
	higher, err := Sign([]byte(strings.Replace(string(f.raw), `"serial": 7`, `"serial": 8`, 1)), f.private, "k1")
	if err != nil {
		t.Fatal(err)
	}
	var h Holder
	entered, release := make(chan struct{}), make(chan struct{})
	hold := func(doc *rules.Document) (*match.Program, error) {
		close(entered)
		<-release
		return match.Compile(doc)
	}
	first, second := make(chan error, 1), make(chan error, 1)
	go func() { first <- h.install(f.signed, f.keys, when(), hold) }()
	select {
	case <-entered:
	case err := <-first:
		t.Fatalf("the first Install returned (%v) before it reached the compiler", err)
	}
	go func() { second <- h.Install(higher, f.keys, when()) }()
	for range 100_000 {
		select {
		case err := <-second:
			t.Fatalf("a second Install returned (%v) while the first was still inside Load; serial now %d", err, h.Current().Serial())
		default:
			runtime.Gosched()
		}
	}
	if h.Current() != nil {
		t.Fatalf("something was installed while the first Install was held: serial %d", h.Current().Serial())
	}
	close(release)
	if err := <-first; err != nil {
		t.Fatalf("the first Install: %v", err)
	}
	if err := <-second; err != nil {
		t.Fatalf("the second Install: %v", err)
	}
	if got := h.Current().Serial(); got != 8 {
		t.Fatalf("serial %d after both installs, want 8", got)
	}
}

// TestPolicyDoesNotReachTheRegistry is the import-graph half of "a verdict
// never comes from a reason code": no production dependency of this package,
// direct or not, is the registry. go list -deps without -test does not read the
// test files, which may import it.
func TestPolicyDoesNotReachTheRegistry(t *testing.T) {
	deps := goList(t, "-deps", ".")
	// The listing is real only if it holds what this package does import.
	for _, dep := range []string{
		"github.com/guardana/control/internal/policy",
		"github.com/guardana/control/internal/policy/bundle",
		"github.com/guardana/control/internal/policy/match",
		"github.com/guardana/control/internal/policy/rules",
	} {
		if !slices.Contains(deps, dep) {
			t.Fatalf("go list -deps does not list %s, so it did not list this package's dependencies: %q", dep, deps)
		}
	}
	if slices.Contains(deps, "github.com/guardana/control/internal/policy/reasons") {
		t.Error("a production file of this package reaches internal/policy/reasons")
	}
}

func goList(t *testing.T, args ...string) []string {
	t.Helper()
	// The program is the fixed string "go" and every argument is a literal in
	// this file; gosec cannot see that through the variadic call.
	//nolint:gosec // G204: no external input reaches this argument list.
	cmd := exec.CommandContext(t.Context(), "go", append([]string{"list"}, args...)...)
	var stdout, stderr strings.Builder
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	if err := cmd.Run(); err != nil {
		t.Fatalf("go list %s: %v\n%s", strings.Join(args, " "), err, stderr.String())
	}
	return strings.Fields(stdout.String())
}
