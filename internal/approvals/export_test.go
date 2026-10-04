package approvals

// WithSteps, StepJudged and StepLinked let this package's external tests act
// between a call's judge and the file operations behind it, and between a
// link and the re-read behind it.
var WithSteps = withSteps

const (
	StepJudged = stepJudged
	StepLinked = stepLinked
)

// SetEffectiveUID makes uid the account a directory has to belong to until
// the returned function restores the real one.
func SetEffectiveUID(uid func() int) (restore func()) {
	saved := effectiveUID
	effectiveUID = uid
	return func() { effectiveUID = saved }
}
