//go:build !(linux || darwin)

package gatewayconfig

import "os"

// readOnlyMount cannot read a mount's flags here, so no directory others may
// write is taken for one on a read-only mount.
func readOnlyMount(*os.File) (bool, error) { return false, nil }
