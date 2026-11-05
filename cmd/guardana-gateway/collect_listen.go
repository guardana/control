package main

import (
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"path"
	"strings"
)

// collectListener binds what --listen names: a loopback address, or with
// unix:/// a socket at an absolute, clean path where nothing is yet. The
// socket's mode lets every account connect, as every account can reach a
// loopback port: the credential decides who writes, and the plane checks
// that this account answers before it sends the credential.
func collectListener(ctx context.Context, listen string) (net.Listener, error) {
	socket, ok := strings.CutPrefix(listen, "unix://")
	if !ok {
		if err := loopbackOnly(listen); err != nil {
			return nil, err
		}
		return (&net.ListenConfig{}).Listen(ctx, "tcp", listen)
	}
	if !path.IsAbs(socket) || path.Clean(socket) != socket {
		return nil, fmt.Errorf("--listen unix:// is followed by an absolute, clean socket path")
	}
	ln, err := (&net.ListenConfig{}).Listen(ctx, "unix", socket)
	if err != nil {
		return nil, fmt.Errorf("--listen: %w", err)
	}
	if err := os.Chmod(socket, 0o666); err != nil { //nolint:gosec // G302: the credential and the plane's peer check decide, not the mode
		return nil, errors.Join(fmt.Errorf("--listen: %w", err), ln.Close())
	}
	return ln, nil
}
