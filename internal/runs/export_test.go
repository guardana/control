package runs

// SetEffectiveUID makes uid the user a directory has to belong to until the
// returned function restores the real one.
func SetEffectiveUID(uid func() int) (restore func()) {
	saved := effectiveUID
	effectiveUID = uid
	return func() { effectiveUID = saved }
}
