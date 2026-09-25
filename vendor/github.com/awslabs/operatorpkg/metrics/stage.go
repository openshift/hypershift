package metrics

// Stage describes the API stability of a metric, mirroring the Kubernetes
// metric stability levels. Declaring it lets a metrics documentation generator
// surface which metrics are safe to depend on and which may still change.
type Stage string

const (
	// Alpha marks an experimental metric. Any aspect — its name, dimensions,
	// values, or the metric itself — may change or be removed without notice.
	Alpha Stage = "alpha"
	// Beta marks a metric that is fairly stable but not yet guaranteed. Additive
	// changes are expected, and breaking changes to its dimensions (renaming or
	// removing a dimension) are still permitted before it is promoted to GA.
	Beta Stage = "beta"
	// GA marks a stable metric that is safe to depend on. Only additive changes
	// are allowed: new dimensions may still be added, but existing dimensions are
	// not renamed or removed except through the usual deprecation process.
	GA Stage = "ga"
)
