//go:build !unix

package holdjournal

// Without the flag an open of a named pipe would wait. No journal opens on
// such a platform: none of them has the file lock the directory needs.
const nonBlocking = 0
