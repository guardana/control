// The dependency rule's negative fixture. scripts/check-imports-probe.sh
// copies this directory into every package of every guarded tree of a staged
// copy of the repository, with the package clause rewritten to that package's
// name, and into one new package per tree, and fails unless each mechanism of
// the rule refuses what it holds. This file dials an address read from the
// environment, reads a file, runs a program through a helper outside the
// guarded trees, imports an adapter and a module no guarded tree uses, reads
// the wall clock, and carries an inline exception the rule does not take.

package ioprobe

import (
	"context"
	"io/ioutil"
	"net"
	"os"
	"time"

	"golang.org/x/sync/errgroup"

	"github.com/guardana/control/adapters/probeadapter"
	"github.com/guardana/control/internal/probehelper"
)

// DependencyProbeDial connects to the address named by PROBE_ADDR and says
// when it did.
func DependencyProbeDial(ctx context.Context) (net.Conn, time.Time, error) {
	var conn net.Conn
	group, ctx := errgroup.WithContext(ctx)
	group.Go(func() error {
		var dialer net.Dialer
		c, err := dialer.DialContext(ctx, "tcp", os.Getenv("PROBE_ADDR"))
		conn = c
		return err
	})
	err := group.Wait()
	return conn, time.Now(), err
}

// DependencyProbeRead runs the adapter's program, then reads name from disk.
func DependencyProbeRead(ctx context.Context, name string) ([]byte, error) {
	if err := probehelper.Run(ctx, probeadapter.Program); err != nil {
		return nil, err
	}
	return ioutil.ReadFile(name)
}

// DependencyProbeExcused reads the clock under an inline exception.
func DependencyProbeExcused() time.Time {
	return time.Now() //nolint:forbidigo // the exception the gate refuses
}
