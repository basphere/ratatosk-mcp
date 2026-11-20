package metrics

// Exported for the tests still in package main while the packages are split;
// removed when those tests move to package metrics_test (export_test.go).

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
