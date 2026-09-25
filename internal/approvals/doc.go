// Package approvals is the durable approval record: a directory of files where
// the plane writes what it holds, an approver process answers it, and the plane
// reads the answer back (ADR-0016). Write access to the directory is the
// approval authority; approver_id on a record is an unauthenticated claim, and
// nothing here treats it as an identity.
//
// It grants nothing on its own. It reports which of its records are still
// there, and the plane compares every answer against its own record, field by
// field, before anything runs.
//
// Two handles open one directory and the compiler keeps them apart. OpenPlane
// takes the directory's exclusive lock and has no method that answers an
// approval; OpenApprover has Answer, takes no lock, and files an answer
// whether or not the plane lock is held, saying which it found, since an
// answer nobody consumes still records that a person decided. The zero value
// of either handle approves nothing: every method on it fails closed.
//
// A handle opens its directory once, as an os.Root, and reaches every file
// through it, the plane's lock included, so a directory put at the name later
// is never read or written. It judges that directory again at every call: the
// same owner, and no write bit for the group or the world; and it refuses a
// name that no longer names it. A directory that changed is refused with
// ErrDirectoryChanged.
//
// # The names a record takes
//
// One record is one file, and a state transition is a link to the next state's
// name that fails with EEXIST when another writer won, so nothing in the
// request path waits on another process's lock. The states are totally
// ordered:
//
//	held < answered < not-resumed < consumed
//
// A reader takes the furthest-along name present, and the earlier names are
// unlinked after the new one is linked, so a crash between the two leaves two
// names that the order resolves rather than a guess. Consumed is furthest
// because a record read as consumed refuses a later execution, which is the
// closed direction; not-resumed only makes the next identical call hold anew.
//
// # What a record is held to on the way back
//
// A record read from the directory is held to what an answer is held to on the
// way in: the approver id and the reason to their bounds and to no control
// character, the decision to the window between the request and the expiry, and
// the expiry to the longest window this plane could have minted. A writer of
// the directory picks its own bytes, so a bound enforced only on the way in is
// no bound at all; what the last one buys is not that a record cannot be forged
// but that a forged one expires like any other.
//
// An entry that is not a regular file is refused wherever it is met, by name or
// in a listing. A named pipe is what makes that worth saying: the read of one
// never returns, and it would be holding the directory's only mutex.
//
// Beside each record sits a projection for a human to read and for an approver
// to list. It carries no free text, the plane never reads it back as truth, and
// a record whose projection is missing or corrupt still works.
package approvals
