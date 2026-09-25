// Package probehelper is part of the dependency rule's negative fixture,
// planted at internal/probehelper. It sits outside every guarded tree and runs
// a program, so a guarded package that imports it reaches os/exec without
// importing os/exec itself.
package probehelper

import (
	"context"
	"os/exec"
)

// Run starts the named program and waits for it to exit.
func Run(ctx context.Context, name string) error {
	return exec.CommandContext(ctx, name).Run()
}
