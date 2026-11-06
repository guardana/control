package gatewayconfig

import (
	"fmt"
	"path/filepath"
)

// checkSignedPause requires pause.floor_dir under the signed authority where
// a pause file is set, and refuses it anywhere else. The floor is the
// plane's to write and the pause file the operators', so the file may not
// sit in the floor directory or under it.
func (c *Config) checkSignedPause() error {
	signed := c.Approvals.Authority == AuthoritySigned && c.Pause.File != ""
	switch {
	case !signed && c.Pause.FloorDir != "":
		return fmt.Errorf("pause.floor_dir: only a pause file under the %s authority is read above a floor, and here the authority is %s or pause.file is not set",
			AuthoritySigned, c.Approvals.Authority)
	case !signed:
		return nil
	case c.Pause.FloorDir == "":
		return fmt.Errorf("pause.floor_dir: the %s authority takes a signed pause only at a serial its floor takes, and it has no default", AuthoritySigned)
	}
	floor := c.Resolve(c.Pause.FloorDir)
	dir := filepath.Dir(c.Resolve(c.Pause.File))
	within, err := insideDir(dir, floor)
	if err == nil && !within {
		within, err = underSameDir(dir, floor)
	}
	if err != nil {
		return fmt.Errorf("pause.floor_dir: %w", err)
	}
	if within {
		return fmt.Errorf("pause.floor_dir: %q holds the pause file's directory; operators write the pause file, and the plane alone writes its floor",
			c.Pause.FloorDir)
	}
	return nil
}
