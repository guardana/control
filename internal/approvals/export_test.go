package approvals

// WithSteps and StepJudged let this package's external tests act between a
// call's judge and the file operations behind it.
var WithSteps = withSteps

const StepJudged = stepJudged
