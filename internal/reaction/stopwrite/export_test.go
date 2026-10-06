package stopwrite

// SetJudged makes f run once the judge accepted an append and before it is
// written, until the returned function puts the writer back.
func SetJudged(f func()) func() {
	judged = f
	return func() { judged = nil }
}
