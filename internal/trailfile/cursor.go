package trailfile

const (
	// ErrCursorMalformed is a cursor not spelled v1:<first>:<offset>:<line>,
	// each digest 64 lowercase hex digits and the offset a decimal above zero.
	ErrCursorMalformed Error = "trailfile: the cursor is not v1:<sha256>:<offset>:<sha256>"
	// ErrCursorOtherFile is a cursor whose first line is not this file's.
	ErrCursorOtherFile Error = "trailfile: the cursor is from another file"
	// ErrCursorPastEnd is a cursor past the file's last newline.
	ErrCursorPastEnd Error = "trailfile: the cursor is past the file's last newline"
	// ErrCursorOffLine is a cursor whose offset does not follow a newline.
	ErrCursorOffLine Error = "trailfile: the cursor does not follow a newline"
	// ErrCursorChanged is a cursor whose line is not the line ending there.
	ErrCursorChanged Error = "trailfile: the line before the cursor is not the one the cursor names"
)
