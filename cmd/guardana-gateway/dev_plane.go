package main

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"time"

	"github.com/guardana/control/internal/brand"
	"github.com/guardana/control/internal/gatewayconfig"
	"github.com/guardana/control/internal/policykey"
)

// devDrain bounds how long a stopping dev plane waits for its exporter to
// ship what the spool holds to the collector.
const devDrain = 5 * time.Second

// What a dev state directory holds, each named from the directory.
const (
	stateApprovals = "approvals"
	stateHolds     = "holds"
	stateSpool     = "spool"
	stateTrail     = "trail.jsonl"
	stateBundle    = "policy.bundle"
	statePause     = "pause.json"
	stateSettings  = "settings.txt"
)

// devState is a directory dev created for one plane, holding everything
// that plane writes.
type devState struct{ dir string }

func (s devState) path(name string) string { return filepath.Join(s.dir, name) }

// makeState creates dir, which must not exist, or a new directory under the
// system's temporary directory when dir is empty, with the directories the
// plane locks inside it, each mode 0700.
func makeState(dir string) (devState, error) {
	var err error
	if dir == "" {
		dir, err = os.MkdirTemp("", brand.Gateway+"-dev-")
	} else {
		err = os.Mkdir(dir, 0o700)
	}
	if err != nil {
		return devState{}, fmt.Errorf("--state: %w", err)
	}
	// Every path dev prints or hands to the approver is absolute, so none
	// can be read as a flag or against another working directory.
	if dir, err = filepath.Abs(dir); err != nil {
		return devState{}, err
	}
	st := devState{dir: dir}
	for _, sub := range []string{stateApprovals, stateHolds, stateSpool} {
		if err := os.Mkdir(st.path(sub), 0o700); err != nil {
			return devState{}, err
		}
	}
	return st, nil
}

// refuseState refuses a --state that exists. makeState refuses it too; this
// runs before anything is created or bound.
func refuseState(dir string) error {
	if dir == "" {
		return nil
	}
	if _, err := os.Lstat(dir); !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("--state %s: it exists, and dev lays out a new directory", dir)
	}
	return nil
}

// devPlane is one plane dev laid out and serves in its own process: the
// state, the collector and the plane on listeners dev bound itself.
type devPlane struct {
	state devState
	cfg   *gatewayconfig.Config
	plane *plane
	coll  *collector
	keyID string
	// done is closed once the plane's run returned ran; stop ends the run.
	done chan struct{}
	ran  error
	stop context.CancelFunc
}

// devInputs is what every plane of one dev run is made from.
type devInputs struct {
	config   string
	document []byte
	control  sibling
}

// newKey draws the signing key, which is never written.
func newKey() (ed25519.PrivateKey, error) {
	_, key, err := ed25519.GenerateKey(rand.Reader)
	return key, err
}

// checkDemo refuses the demo's configuration before anything is created or
// bound, holding it to the layer a plane under dir would get. With no dir it
// stands in a path nothing creates, named at random so that no other account
// can plant a link there ahead of dev.
func checkDemo(in devInputs, dir string) error {
	key, err := newKey()
	if err != nil {
		return err
	}
	defer clear(key)
	if dir == "" {
		dir = filepath.Join(os.TempDir(), brand.Gateway+"-dev-"+rand.Text())
	}
	const unbound = "127.0.0.1:0"
	_, err = resolveDemo(in.config, newLayer(devState{dir: dir}, key, unbound, unbound, unbound))
	return err
}

// checkSignable refuses a document the signer refuses, by signing it once
// under a key drawn for that and dropped, before anything is created.
func checkSignable(document []byte) error {
	key, err := newKey()
	if err != nil {
		return err
	}
	defer clear(key)
	_, _, err = policykey.SignBundle(document, key, time.Now())
	return err
}

// startDevPlane lays out a new state under dir, signs the document into it
// under a key that lives only here, binds the loopback for the collector, the
// agents and the health answers, and starts the collector and then the plane
// the way run starts one. Whatever it made is released when it refuses; the
// directory stays, as the record of what was tried.
func startDevPlane(ctx context.Context, in devInputs, dir string, log io.Writer) (*devPlane, error) {
	st, err := makeState(dir)
	if err != nil {
		return nil, err
	}
	d := &devPlane{state: st}
	key, err := newKey()
	if err != nil {
		return nil, err
	}
	defer clear(key)
	if err := d.sign(in.document, key); err != nil {
		return nil, err
	}
	if _, err := in.control.run(ctx, "pause", "init", st.path(statePause)); err != nil {
		return nil, fmt.Errorf("creating the pause file: %w", err)
	}
	listeners, err := bindLoopback(ctx, 3)
	if err != nil {
		return nil, err
	}
	addr := func(i int) string { return listeners[i].Addr().String() }
	d.cfg, err = resolveDemo(in.config, newLayer(st, key, addr(1), addr(2), addr(0)))
	if err == nil {
		err = writeSettings(st.path(stateSettings), d.cfg)
	}
	if err != nil {
		return nil, errors.Join(err, closeAll(listeners))
	}
	if d.coll, err = startCollector(listeners[0], st.path(stateTrail), log); err != nil {
		return nil, errors.Join(err, closeAll(listeners[1:]))
	}
	if d.plane, err = startPlane(ctx, d.cfg, log); err != nil {
		return nil, errors.Join(err, closeAll(listeners[1:]), d.coll.stop())
	}
	d.plane.settle(ctx, log)
	runCtx, stop := context.WithCancel(ctx)
	d.done, d.stop = make(chan struct{}), stop
	handed := map[string]net.Listener{addr(1): listeners[1], addr(2): listeners[2]}
	go func() {
		defer close(d.done)
		d.ran = d.plane.run(runCtx, log, func(a string) (net.Listener, error) {
			ln, ok := handed[a]
			if !ok {
				return nil, fmt.Errorf("dev bound nothing at %s", a)
			}
			delete(handed, a)
			return ln, nil
		}, devDrain)
		// A run that stopped before it took every listener leaves the rest
		// to be closed here.
		for _, ln := range handed {
			_ = ln.Close()
		}
	}()
	return d, nil
}

// sign signs the document under key into the state's bundle file.
func (d *devPlane) sign(document []byte, key ed25519.PrivateKey) error {
	b, _, err := policykey.SignBundle(document, key, time.Now())
	if err != nil {
		return fmt.Errorf("--policy: %w", err)
	}
	d.keyID = b.GetKeyId()
	return policykey.WriteBundle(d.state.path(stateBundle), b)
}

// bindLoopback binds n ports the system picks on 127.0.0.1, so no port is
// chosen and then bound by someone else first.
func bindLoopback(ctx context.Context, n int) ([]net.Listener, error) {
	var out []net.Listener
	for range n {
		ln, err := (&net.ListenConfig{}).Listen(ctx, "tcp4", "127.0.0.1:0")
		if err != nil {
			return nil, errors.Join(err, closeAll(out))
		}
		out = append(out, ln)
	}
	return out, nil
}

func closeAll(listeners []net.Listener) error {
	var errs []error
	for _, ln := range listeners {
		errs = append(errs, ln.Close())
	}
	return errors.Join(errs...)
}

// halt stops the plane's listeners and gives its exporter devDrain to ship
// what the spool holds, then stops the collector and releases the plane. It
// returns what is left unacknowledged in the spool, which the trail file
// lacks, and whether a part had stopped with an error.
func (d *devPlane) halt() (left int64, failed error) {
	d.stop()
	<-d.done
	failed = d.ran
	st, err := d.plane.spool.Stats()
	left = st.Unacknowledged
	return left, errors.Join(failed, err, d.coll.stop(), d.plane.close())
}
