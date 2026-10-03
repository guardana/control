package holdjournal

import "io/fs"

// SetOwnerWanted makes uid name the account each file or directory the
// journal judges has to belong to, until the returned function restores the
// real one.
func SetOwnerWanted(uid func(fs.FileInfo) int) (restore func()) {
	saved := ownerWanted
	ownerWanted = uid
	return func() { ownerWanted = saved }
}
