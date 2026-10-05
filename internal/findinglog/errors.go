package findinglog

// Error is a refusal by this package, matched with errors.Is. They are
// constants, so no other code in the binary can reassign one and turn a
// refusal into a pass.
type Error string

// Error returns the refusal's text.
func (e Error) Error() string { return string(e) }

const (
	// ErrNoPermissionBits is a platform that keeps no permission bits, so the
	// directory and the file cannot be checked.
	ErrNoPermissionBits Error = "findinglog: this platform keeps no permission bits to check"
	// ErrNoLock is a platform with no file lock, so the log cannot be held by
	// one writer.
	ErrNoLock Error = "findinglog: this platform has no file lock, so the log cannot be held"
	// ErrPath is a directory path that is empty.
	ErrPath Error = "findinglog: no directory named"
	// ErrDirMode is a directory the group or others may enter, read or write.
	ErrDirMode Error = "findinglog: the directory gives the group or others access"
	// ErrFileMode is a log file the group or others may read or write.
	ErrFileMode Error = "findinglog: the log file gives the group or others access"
	// ErrNotRegular is a log file name that holds no regular file: a link, a
	// directory, a named pipe, a device or a socket.
	ErrNotRegular Error = "findinglog: not a regular file"
	// ErrLinks is a log file with a name besides the log's, or one whose
	// names the platform does not count.
	ErrLinks Error = "findinglog: the log file has another name"
	// ErrTooLarge is a log file past MaxLogBytes, or a write that would take
	// it past.
	ErrTooLarge Error = "findinglog: the log file is larger than an open reads"
	// ErrOwner is a directory or a log file another account owns, or one whose
	// owner the platform does not name.
	ErrOwner Error = "findinglog: the directory or the log file is not owned by this process's account"
	// ErrLocked is a log another writer holds.
	ErrLocked Error = "findinglog: the log is held by another writer"
	// ErrDamaged is a log no writer of this package leaves behind: a whole
	// line that is not one record this build reads in the form the writer
	// writes it, a line with a carriage return, bytes after the last newline
	// that cannot be the start of a line the writer writes or are longer than
	// any line it writes, one finding id and verdict carried twice, or a
	// report whose findings written miscounts the findings of its write.
	ErrDamaged Error = "findinglog: the log is damaged"
	// ErrRecord is a finding or a report the log will not write, or none given
	// where one is required. Nothing of the call is written.
	ErrRecord Error = "findinglog: a record the log will not write"
	// ErrClosed is a write to a log that is closed.
	ErrClosed Error = "findinglog: the log is closed"
	// ErrWrite is a write that did not reach the disk. The file is cut back
	// to what it held before it; when that fails too, the log is ErrFailed
	// from then on.
	ErrWrite Error = "findinglog: the write did not reach the disk"
	// ErrFailed is a write to a log that could not cut a failed write back.
	// The file may hold that write, so the log takes no more until it is
	// opened again. The open cuts the write unless its report reached the
	// file whole; a write that failed only at its sync can stay.
	ErrFailed Error = "findinglog: an earlier write could not be cut back; open the log again"
	// ErrChanged is a log file removed, renamed, replaced, cut or extended
	// under the writer. This write and every later one are refused until the
	// log is opened again.
	ErrChanged Error = "findinglog: the log file was removed, renamed, replaced, cut or extended under the writer; open the log again"
)
