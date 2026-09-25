// Package trailfile keeps the evidence a collector accepts in one file of
// evidence lines, and reads such a file back as trails, one per request.
//
// The writer is the only process writing its file, under an exclusive lock,
// and reports a batch appended only once the file is synced, so a sender may
// release what it reported. A crash can still leave a batch the sender will
// send again, so the file is at least once; the reader collapses a line that
// repeats one it read and refuses one event id carrying two different lines.
package trailfile
