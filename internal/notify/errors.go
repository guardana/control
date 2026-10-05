package notify

// Error is a refusal by this package, matched with errors.Is. They are
// constants, so no other code in the binary can reassign one and turn a
// refusal into a pass.
type Error string

// Error returns the refusal's text.
func (e Error) Error() string { return string(e) }

const (
	// ErrUnsupported is a platform with no permission bits, no file lock or
	// no process group, so the state cannot be judged or held and a program
	// cannot be killed with what it started.
	ErrUnsupported Error = "notify: this platform cannot hold the state or kill a program's group"
	// ErrOptions is an option missing or out of range.
	ErrOptions Error = "notify: an option is missing or out of range"
	// ErrStateMode is a state directory or file the group or others may
	// reach.
	ErrStateMode Error = "notify: the state gives the group or others access"
	// ErrOwner is a state directory or file another account owns, or one
	// whose owner the platform does not name.
	ErrOwner Error = "notify: the state is not owned by this process's account"
	// ErrNotInitialised is a state directory without its marker, used
	// without asking for init.
	ErrNotInitialised Error = "notify: the state directory is not initialised; init delivers every alert again"
	// ErrInitialised is init asked for over a state that has its marker.
	ErrInitialised Error = "notify: the state directory is initialised already"
	// ErrState is a state this package does not leave: a marker it did not
	// write, a missing or linked file, or a delivered list with a line it
	// does not write.
	ErrState Error = "notify: the state is damaged"
	// ErrLocked is a state another run holds.
	ErrLocked Error = "notify: the state is held by another run"
	// ErrOtherLog is a findings log whose first line is not the one the
	// state recorded.
	ErrOtherLog Error = "notify: the findings log is not the one this state delivers from"
	// ErrTooLarge is a delivered list past MaxDeliveredBytes, or a mark that
	// would take it past.
	ErrTooLarge Error = "notify: the delivered list is past its bound"
	// ErrChanged is a findings log line that is not the record read for it:
	// the file changed under the run.
	ErrChanged Error = "notify: the findings log changed under the run"
	// ErrMark is a mark that did not reach the disk. The program exited 0,
	// so the next run delivers that record again; this run stops.
	ErrMark Error = "notify: a delivery could not be marked"
	// ErrTimeout is a program still running at the timeout, killed with its
	// process group.
	ErrTimeout Error = "notify: the program did not exit within the timeout"
)
