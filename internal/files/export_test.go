package files

import (
	"errors"
	"io/fs"
	"os"
)

// CreateNoReplace is CreateNoReplaceIn through a handle to dir, which the
// tests take a directory's name for.
func CreateNoReplace(dir, name string, body []byte, perm fs.FileMode) error {
	root, err := os.OpenRoot(dir)
	if err != nil {
		return err
	}
	return errors.Join(CreateNoReplaceIn(root, name, body, perm), root.Close())
}

// BetweenExchanges sets what runs once ReplaceJudgedIn's new file took the
// name, for the test's length.
func BetweenExchanges(t interface{ Cleanup(func()) }, f func(root *os.Root, tmp string)) {
	saved := betweenExchanges
	betweenExchanges = f
	t.Cleanup(func() { betweenExchanges = saved })
}

// BeforeJudgedLocked sets what runs once ReplaceJudgedIn opened the judged
// file and before it waits for that file's lock, for the test's length.
func BeforeJudgedLocked(t interface{ Cleanup(func()) }, f func()) {
	saved := beforeJudgedLocked
	beforeJudgedLocked = f
	t.Cleanup(func() { beforeJudgedLocked = saved })
}
