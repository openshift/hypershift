package metrics

import "github.com/awslabs/operatorpkg/docs"

// Stage describes the API stability of a metric. Alpha metrics may change in any
// way; Beta metrics may still rename or remove dimensions; GA metrics only add
// dimensions, except through the usual deprecation process.
type Stage = docs.Stage

const (
	Alpha = docs.Alpha
	Beta  = docs.Beta
	GA    = docs.GA
)
