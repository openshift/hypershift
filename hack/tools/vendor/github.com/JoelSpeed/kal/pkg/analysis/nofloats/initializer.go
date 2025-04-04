package nofloats

import (
	"github.com/JoelSpeed/kal/pkg/config"
	"golang.org/x/tools/go/analysis"
)

// Initializer returns the AnalyzerInitializer for this
// Analyzer so that it can be added to the registry.
func Initializer() initializer {
	return initializer{}
}

// intializer implements the AnalyzerInitializer interface.
type initializer struct{}

// Name returns the name of the Analyzer.
func (initializer) Name() string {
	return name
}

// Init returns the intialized Analyzer.
func (initializer) Init(cfg config.LintersConfig) (*analysis.Analyzer, error) {
	return Analyzer, nil
}

// Default determines whether this Analyzer is on by default, or not.
func (initializer) Default() bool {
	// Floats avoidance in the Kube conventions is a must for spec fields.
	// Doesn't hurt to enforce it widely, uses outside of spec fields should be
	// evaluated on an individual basis to determine if it is reasonable to override.
	return true
}
