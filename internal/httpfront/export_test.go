package httpfront

// What the external tests in package httpfront_test call of
// this package's internals: the chain without the mux, and the in-flight cap
// on its own. Compiled into tests only.

var (
	Chain              = chain
	InflightMiddleware = inflightMiddleware
)
