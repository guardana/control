package holdjournal

import (
	"errors"
	"fmt"
)

// JudgeDirectory refuses dir where Open and OpenReadOnly would refuse it for
// the directory itself: anything but a directory, a mode a group or the world
// may write, and an owner other than this process's account. It takes no
// lock, reads no entry and creates nothing, so a command that only inspects
// may call it while a plane serves.
func JudgeDirectory(dir string) error {
	root, _, err := openDir(dir)
	if err != nil {
		return fmt.Errorf("%s: %w", dir, err)
	}
	if err := root.Close(); err != nil {
		return errors.Join(fmt.Errorf("%s: %w", dir, ErrNotAJournal), err)
	}
	return nil
}
