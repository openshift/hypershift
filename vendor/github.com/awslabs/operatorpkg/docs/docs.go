// Package docs holds the documentation primitives shared by operatorpkg's
// source-of-truth doc types, e.g. metrics.Label and wellknown.Annotation.
package docs

// Value documents one of the well known values a documented type can take, e.g.
// a metric dimension or an annotation.
type Value struct {
	// Name is the value. It MUST be sourced from a const, never a magic string.
	Name string
	// Help is human-readable documentation for this value: what it means and,
	// where useful, why it is used.
	Help string
}

// Stage describes the API stability of a documented type, mirroring the
// Kubernetes stability levels. Declaring it lets a documentation generator
// surface what is safe to depend on and what may still change.
type Stage string

const (
	// Alpha marks an experimental API. Any aspect of it may change or be removed
	// without notice.
	Alpha Stage = "alpha"
	// Beta marks an API that is fairly stable but not yet guaranteed. Additive
	// changes are expected, and breaking changes are still permitted before it
	// is promoted to GA.
	Beta Stage = "beta"
	// GA marks a stable API that is safe to depend on. Only additive changes are
	// allowed, except through the usual deprecation process.
	GA Stage = "ga"
)
