package holdjournal

// Error is a refusal by this package, matched with errors.Is. They are
// constants, so no other code in the binary can reassign one and turn a
// refusal into a pass.
type Error string

// Error returns the refusal's text.
func (e Error) Error() string { return string(e) }

// The four refusals the gateway's HoldJournal names, under this package's own
// vocabulary. Package documentation carries the translation.
const (
	// ErrEntry is an entry that cannot be recorded: one missing what closing
	// its trail needs, one whose parts disagree, or one in another state.
	ErrEntry Error = "holdjournal: an entry is recorded held, with its ids, its position, its binding, its approval and its expiry"
	// ErrRecorded is a second entry under one request id.
	ErrRecorded Error = "holdjournal: this request has an entry already"
	// ErrNoEntry is a request the journal holds no entry for.
	ErrNoEntry Error = "holdjournal: the journal holds no entry for this request"
	// ErrFlip is a flip the journal refuses: out of a state that is not held,
	// or into one that is neither resuming nor closing.
	ErrFlip Error = "holdjournal: an entry leaves held once, for resuming or for closing"
)

// The vocabulary of the directory and of the handle.
const (
	// ErrClosed is an operation on a journal nobody opened, or one already
	// closed. The zero value answers it.
	ErrClosed Error = "holdjournal: the journal is not open"
	// ErrReadOnly is a write through a handle that took no lock. A reader
	// shares the directory with the plane and never writes in it.
	ErrReadOnly Error = "holdjournal: this handle is read-only"
	// ErrLocked is an Open of a directory another plane holds.
	ErrLocked Error = "holdjournal: the directory is held by another plane"
	// ErrNoLock is a platform with no file lock. The journal refuses to open
	// rather than let two planes write one directory.
	ErrNoLock Error = "holdjournal: this platform has no file lock, so the directory cannot be held"
	// ErrPermissions is a directory a group or the world may write.
	ErrPermissions Error = "holdjournal: the directory is group- or world-writable"
	// ErrNotAJournal is a directory that is not a hold journal: no marker and
	// not empty, or a marker this build cannot read. It is never read as an
	// empty journal, because an empty journal says every hold was closed.
	ErrNotAJournal Error = "holdjournal: the directory is not a hold journal"
	// ErrForeignFile is a file under the directory this package did not
	// write: a name it gives no entry, or anything that is not a regular
	// file whatever it is named.
	ErrForeignFile Error = "holdjournal: a file under the directory is not the journal's"
	// ErrTooManyEntries is a directory holding as many entries as the bound
	// allows. A hold is refused; a listing says it is incomplete.
	ErrTooManyEntries Error = "holdjournal: the directory holds as many entries as the bound allows"
	// ErrInvalidOption is an option outside its range.
	ErrInvalidOption Error = "holdjournal: invalid option"
)

// The vocabulary of one entry on disk. Each way of failing is its own
// sentinel: an operator acts differently on a truncated file, a checksum that
// does not match and a field this build cannot name.
const (
	// ErrEntryTooLarge is an entry whose body is over the bound, in either
	// direction.
	ErrEntryTooLarge Error = "holdjournal: the entry is larger than the bound allows"
	// ErrTruncated is a file too short for the entry it claims.
	ErrTruncated Error = "holdjournal: the entry is truncated"
	// ErrCorrupt is an entry whose framing does not check: a wrong magic, a
	// checksum that does not match the body, or bytes past the body's end.
	ErrCorrupt Error = "holdjournal: the entry does not check"
	// ErrUnknownField is an entry carrying a field this build cannot name.
	// Reading it as if the field were absent would drop in silence what a
	// writer thought mattered.
	ErrUnknownField Error = "holdjournal: the entry carries an unknown field"
	// ErrFieldType is an entry whose field holds a value of another type.
	ErrFieldType Error = "holdjournal: a field of the entry holds the wrong type"
	// ErrSchemaVersion is an entry whose schema version has a major this
	// build does not understand, or none at all.
	ErrSchemaVersion Error = "holdjournal: the entry's schema version is not one this build reads"
	// ErrMalformed is an entry body that is not one JSON object of one entry,
	// or one whose state or expiry this build cannot read.
	ErrMalformed Error = "holdjournal: the entry is malformed"
	// ErrNameMismatch is an entry whose file name disagrees with the request
	// id inside it.
	ErrNameMismatch Error = "holdjournal: the entry's file name disagrees with the entry"
	// ErrRequestName is a request id that cannot name a file here: empty,
	// over MaxRequestIDBytes, or one that does not come back identical
	// through the name encoding.
	ErrRequestName Error = "holdjournal: the request id cannot name a file"
)
