package policywatch

// SetBeforeRead makes r call f with each file's path just before a poll reads
// it, so a test can hold a poll inside a read.
func SetBeforeRead(r *Refresher, f func(path string)) { r.beforeRead = f }
