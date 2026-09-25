package trailfile

// Error is a refusal by this package, matched with errors.Is. They are
// constants, so no other code in the binary can reassign one and turn a
// refusal into a pass.
type Error string

// Error returns the refusal's text.
func (e Error) Error() string { return string(e) }

const (
	// ErrNoPermissionBits is a platform that keeps no permission bits, so the
	// file and its directory cannot be checked.
	ErrNoPermissionBits Error = "trailfile: this platform keeps no permission bits to check"
	// ErrNoLock is a platform with no file lock, so the file cannot be held by
	// one writer.
	ErrNoLock Error = "trailfile: this platform has no file lock, so the file cannot be held"
	// ErrDirMode is a directory the group or others may write.
	ErrDirMode Error = "trailfile: the directory is group- or world-writable"
	// ErrFileMode is a file the group or others may write.
	ErrFileMode Error = "trailfile: the file is group- or world-writable"
	// ErrNotRegular is a path that is not a regular file.
	ErrNotRegular Error = "trailfile: not a regular file"
	// ErrLocked is a file another writer holds.
	ErrLocked Error = "trailfile: the file is held by another writer"
	// ErrDamaged is a file no writer of this package leaves behind: a whole
	// line that is not one event, bytes after the last newline that cannot be
	// the start of a line the codec writes or are longer than any line it
	// writes, or one event id carrying two different lines.
	ErrDamaged Error = "trailfile: the file is damaged"
	// ErrClosed is an append to a writer that is closed.
	ErrClosed Error = "trailfile: the writer is closed"
	// ErrWrite is an append that did not reach the disk. The file is cut back
	// to what it held before it, so nothing of it stays.
	ErrWrite Error = "trailfile: the append did not reach the disk"
	// ErrFailed is an append to a writer that could not cut a failed append
	// back, and it carries why the cut failed. The file may hold part of that
	// append, so the writer takes no more until the file is opened again,
	// which cuts a torn last line.
	ErrFailed Error = "trailfile: an earlier append could not be cut back; open the file again"
	// ErrChanged is a file removed, renamed, replaced, cut or extended under
	// the writer: its path no longer names the regular file the writer holds,
	// or that file is not the length the writer left it. What the writer
	// appends would no longer reach a reader of the path, so this append and
	// every later one are refused, each carrying the change, until the path is
	// opened again. Bytes rewritten in place are not detected.
	ErrChanged Error = "trailfile: the file was removed, renamed, replaced, cut or extended under the writer; open the path again"
	// ErrConflict is an append carrying an event id that the file, or the
	// append itself, already holds with other content. Nothing of the append
	// is written, and it is refused for good.
	ErrConflict Error = "trailfile: an event id the file holds arrived with other content"
	// ErrEvent is an event the evidence codec will not write.
	ErrEvent Error = "trailfile: an event the codec will not write"
	// ErrLimit is a negative limit, which is not read as none.
	ErrLimit Error = "trailfile: invalid limit"
)
