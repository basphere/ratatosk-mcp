package metrics

// What the end-to-end tests in package metrics_test read of this package's
// internals: the closed label lists, the body scanner and its
// bound, and the tally's context key. Compiled into tests only.

var (
	MethodLabels     = methodLabels
	RequestOutcomes  = requestOutcomes
	UpstreamOutcomes = upstreamOutcomes
	SelfLabels       = selfLabels
	ToolNames        = toolNames
	ToolLabels       = toolLabels
	PeekLabels       = peekLabels
)

const PeekBytes = metricsPeekBytes

type TallyKey = tallyKey
