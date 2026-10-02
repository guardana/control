package policystate

import (
	"io/fs"
	"os"
)

// SetEffectiveUID makes uid the user a directory and its files have to belong
// to until the returned function restores the real one.
func SetEffectiveUID(uid func() int) (restore func()) {
	saved := effectiveUID
	effectiveUID = uid
	return func() { effectiveUID = saved }
}

// SetReplace makes replace the way a file is replaced whole until the
// returned function restores the real one, so a test can stop a write where a
// crash would.
func SetReplace(replace func(root *os.Root, name string, body []byte, perm fs.FileMode) error) (restore func()) {
	saved := replaceIn
	replaceIn = replace
	return func() { replaceIn = saved }
}
