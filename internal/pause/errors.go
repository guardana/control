package pause

// Error is a refusal by this package, matched with errors.Is. They are
// constants, so no other code in the binary can reassign one and turn a
// refusal into a pass.
type Error string

// Error returns the refusal's text.
func (e Error) Error() string { return string(e) }

// The refusals of the reader. Each is returned wrapped, beside what it
// refers to, and each maps to one Cause.
const (
	// ErrMissing is a pause file that does not exist.
	ErrMissing Error = "pause: the pause file does not exist"
	// ErrLink is a pause file that is a symbolic link.
	ErrLink Error = "pause: the pause file is a symbolic link"
	// ErrNotRegular is a pause file that is not a regular file.
	ErrNotRegular Error = "pause: the pause file is not a regular file"
	// ErrFileMode is a pause file the group or others may write.
	ErrFileMode Error = "pause: the group or others may write the pause file"
	// ErrDirMode is a directory holding the pause file that the group or
	// others may write, and so replace the file in.
	ErrDirMode Error = "pause: the group or others may write the pause file's directory"
	// ErrFileOwner is a pause file another account than this process's
	// effective user owns, and so may rewrite whatever its mode says.
	ErrFileOwner Error = "pause: the pause file is owned by another account"
	// ErrDirOwner is a directory holding the pause file that another account
	// owns, and so may replace the file in.
	ErrDirOwner Error = "pause: the pause file's directory is owned by another account"
	// ErrDirLink is a directory holding the pause file whose own name is a
	// symbolic link, which a writer of its parent could point anywhere.
	ErrDirLink Error = "pause: the pause file's directory is a symbolic link"
	// ErrDirChanged is a directory holding the pause file that another took
	// the place of while it was being opened.
	ErrDirChanged Error = "pause: the pause file's directory changed while it was opened"
	// ErrFileChanged is a pause file that another took the place of between
	// the check that its name is no link and the open, which follows one.
	ErrFileChanged Error = "pause: the pause file changed while it was opened"
	// ErrTooLarge is a pause file over MaxFileBytes.
	ErrTooLarge Error = "pause: the pause file is over its size bound"
	// ErrMalformed is a document this build refuses: not strict JSON, an
	// unknown or repeated member, a missing one, a value out of its bound or
	// its set, or two entries under one id.
	ErrMalformed Error = "pause: the pause file is malformed"
	// ErrVersion is a document of a schema version this build does not read.
	ErrVersion Error = "pause: the pause file is of a schema version this build does not read"
	// ErrNoPermissionBits is a platform that keeps no permission bits, so
	// nothing can say who may write the file.
	ErrNoPermissionBits Error = "pause: this platform keeps no permission bits to check"
)

// The refusals of the writer.
const (
	// ErrExists is an Init over a pause file that exists already.
	ErrExists Error = "pause: a pause file exists already"
	// ErrLocked is a writer that could not take the lock before its context
	// ended.
	ErrLocked Error = "pause: another writer holds the pause file's lock"
	// ErrNoLock is a platform with no file lock, where two writers could
	// lose each other's entries.
	ErrNoLock Error = "pause: this platform has no file lock for the pause file"
	// ErrDuplicateID is an entry added under an id the document holds.
	ErrDuplicateID Error = "pause: an entry with this id exists already"
	// ErrNoEntry is a removal of an id the document does not hold.
	ErrNoEntry Error = "pause: no entry with this id"
	// ErrOptions is a poller asked for with no path, no clock or an interval
	// that is not positive.
	ErrOptions Error = "pause: a poller needs a path, a clock and a positive interval"
	// ErrNotServable is a pause file a poller refuses to start on: whatever
	// made its first read unknown.
	ErrNotServable Error = "pause: the pause file cannot be served"
)
