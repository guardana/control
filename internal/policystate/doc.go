// Package policystate keeps the serial floor of ADR-0038 on disk, one file per
// bundle id, so that a restart cannot roll a plane's policy back. It
// implements policy.FloorStore, which the policy package declares and calls.
//
// The operator's code and the plane's are apart, and the compiler keeps them
// so. Init and Reset are the operator's: Init makes a directory a floor
// directory and gives a bundle id its file holding no serial yet, and refuses
// a file that exists or one removed after it was made; Reset alone lowers a
// floor, recording the reason and the value it replaced. Open is the plane's: its Store reads a floor and
// raises one, and has no method that creates a file or lowers a floor, so a
// binary that calls only Open links neither.
//
// A store opens its directory once, as an os.Root, and reaches every file
// through it. What the directory holds, every entry against the names below,
// is judged when Open, Init and Reset open it. Every call of a store judges
// the directory itself again, the same owner, no access for the group or the
// world, and still at the name it was opened under, and reads the marker
// again. Every call, a read as much as a raise, holds the directory's
// exclusive lock: a raise reads the file, raises the value it read and writes
// it under the lock, so a floor another process raised is the one a raise
// compares against, and readers never hold the lock among themselves long
// enough to keep a raise out.
//
// # The names under the directory
//
//	floors.meta              the marker: the kind and the bundle ids
//	<sha256 hex>.floor.json  the floor of the listed id whose hash names it
//	.tmp-<26 base32>         what a crash during a write leaves; never read
//
// Any other name, and any entry that is not a regular file whatever its name,
// is foreign and refuses the directory. A file is only ever replaced whole:
// written to a temporary name, forced to disk, renamed, and the directory
// forced to disk after. Directory and files are their owner's alone.
//
// # The formats
//
// Both files are one strict JSON object: an absent, unknown or repeated
// member is refused, never defaulted, and a schema_version of major 1 is
// read, any other refused. The marker holds schema_version; kind, "plane" or
// "signer"; and bundle_ids, every id Init ever gave a file here, sorted, one
// to MaxBundleIDs of them. A floor file holds schema_version; bundle_id;
// serial, digest, issued_at and latest_issued_at, all null while the floor
// holds no serial yet; reset_reason and reset_from, both null until the
// operator resets the floor, then the reason given and the floor it replaced
// as an object of the same four members, or null when the reset found the
// file missing. Times are issuedAt's one spelling,
// YYYY-MM-DDTHH:MM:SSZ.
//
// Nothing here reads a clock: every time is the caller's.
package policystate
