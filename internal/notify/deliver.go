package notify

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os/exec"
	"time"
)

// waitDelay bounds how long a delivery waits, once the program has exited or
// been killed, for a process it left holding its output open.
const waitDelay = 2 * time.Second

// deliver runs the program once with line on its standard input, and
// returns nil only when it exited 0 within the timeout, released its output
// within waitDelay and its process group was then killed.
func deliver(ctx context.Context, o Options, line []byte) error {
	ctx, cancel := context.WithTimeout(ctx, o.Timeout)
	defer cancel()
	// The program and its arguments are the operator's own command line;
	// nothing read from the findings log reaches them, only the standard
	// input.
	//nolint:gosec // G204: the operator names the program and its arguments, and no shell runs them
	cmd := exec.CommandContext(ctx, o.Program, o.Args...)
	cmd.Stdin = bytes.NewReader(line)
	cmd.Stdout, cmd.Stderr = o.Stdout, o.Stderr
	cmd.WaitDelay = waitDelay
	ownGroup(cmd)
	err := errors.Join(cmd.Run(), endGroup(cmd))
	if err != nil && errors.Is(ctx.Err(), context.DeadlineExceeded) {
		return fmt.Errorf("%w (%s): %w", ErrTimeout, o.Timeout, err)
	}
	return err
}
