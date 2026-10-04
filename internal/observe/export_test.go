package observe

import "bytes"

// AcceptMinor adds a minor to the table for one test, so a test can tell the
// writer's version from the reader's table; the returned func removes it.
func AcceptMinor(minor string) func() {
	minors[minor] = true
	return func() { delete(minors, minor) }
}

// EscapeLine exposes the line escape to a sweep over every rune, which would
// take minutes through MarshalLine.
func EscapeLine(line []byte) []byte {
	var b bytes.Buffer
	escapeLine(&b, line)
	return b.Bytes()
}
