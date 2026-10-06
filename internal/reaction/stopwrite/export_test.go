package stopwrite

// SetJudged makes f run once the judge accepted an append and before it is
// written, until the returned function puts the writer back.
func SetJudged(f func()) func() {
	judged = f
	return func() { judged = nil }
}

// SetCarrying makes f run once a carry read the old list and before it
// writes the new one, until the returned function puts the writer back.
func SetCarrying(f func()) func() {
	carrying = f
	return func() { carrying = nil }
}
