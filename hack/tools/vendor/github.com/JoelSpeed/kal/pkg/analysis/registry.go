package analysis

import (
	"fmt"

	"github.com/JoelSpeed/kal/pkg/analysis/commentstart"
	"github.com/JoelSpeed/kal/pkg/analysis/conditions"
	"github.com/JoelSpeed/kal/pkg/analysis/integers"
	"github.com/JoelSpeed/kal/pkg/analysis/jsontags"
	"github.com/JoelSpeed/kal/pkg/analysis/maxlength"
	"github.com/JoelSpeed/kal/pkg/analysis/nobools"
	"github.com/JoelSpeed/kal/pkg/analysis/nophase"
	"github.com/JoelSpeed/kal/pkg/analysis/optionalorrequired"
	"github.com/JoelSpeed/kal/pkg/analysis/requiredfields"
	"github.com/JoelSpeed/kal/pkg/analysis/statussubresource"
	"github.com/JoelSpeed/kal/pkg/config"
	"golang.org/x/tools/go/analysis"

	kerrors "k8s.io/apimachinery/pkg/util/errors"
	"k8s.io/apimachinery/pkg/util/sets"
)

// AnalyzerInitializer is used to intializer analyzers.
type AnalyzerInitializer interface {
	// Name returns the name of the analyzer initialized by this intializer.
	Name() string

	// Init returns the newly initialized analyzer.
	// It will be passed the complete LintersConfig and is expected to rely only on its own configuration.
	Init(config.LintersConfig) (*analysis.Analyzer, error)

	// Default determines whether the inializer intializes an analyzer that should be
	// on by default, or not.
	Default() bool
}

// Registry is used to fetch and initialize analyzers.
type Registry interface {
	// DefaultLinters returns the names of linters that are enabled by default.
	DefaultLinters() sets.Set[string]

	// AllLinters returns the names of all registered linters.
	AllLinters() sets.Set[string]

	// InitializeLinters returns a set of newly initialized linters based on the
	// provided configuration.
	InitializeLinters(config.Linters, config.LintersConfig) ([]*analysis.Analyzer, error)
}

type registry struct {
	initializers []AnalyzerInitializer
}

// NewRegistry returns a new registry, from which analyzers can be fetched.
func NewRegistry() Registry {
	return &registry{
		initializers: []AnalyzerInitializer{
			conditions.Initializer(),
			commentstart.Initializer(),
			integers.Initializer(),
			jsontags.Initializer(),
			maxlength.Initializer(),
			nobools.Initializer(),
			nophase.Initializer(),
			optionalorrequired.Initializer(),
			requiredfields.Initializer(),
			statussubresource.Initializer(),
		},
	}
}

// DefaultLinters returns the list of linters that are registered
// as being enabled by default.
func (r *registry) DefaultLinters() sets.Set[string] {
	defaultLinters := sets.New[string]()

	for _, initializer := range r.initializers {
		if initializer.Default() {
			defaultLinters.Insert(initializer.Name())
		}
	}

	return defaultLinters
}

// AllLinters returns the list of all known linters that are known
// to the registry.
func (r *registry) AllLinters() sets.Set[string] {
	linters := sets.New[string]()

	for _, initializer := range r.initializers {
		linters.Insert(initializer.Name())
	}

	return linters
}

// InitializeLinters returns a list of initialized linters based on the provided config.
func (r *registry) InitializeLinters(cfg config.Linters, lintersCfg config.LintersConfig) ([]*analysis.Analyzer, error) {
	analyzers := []*analysis.Analyzer{}
	errs := []error{}

	enabled := sets.New(cfg.Enable...)
	disabled := sets.New(cfg.Disable...)

	allEnabled := enabled.Len() == 1 && enabled.Has(config.Wildcard)
	allDisabled := disabled.Len() == 1 && disabled.Has(config.Wildcard)

	for _, initializer := range r.initializers {
		if !disabled.Has(initializer.Name()) && (allEnabled || enabled.Has(initializer.Name()) || !allDisabled && initializer.Default()) {
			a, err := initializer.Init(lintersCfg)
			if err != nil {
				errs = append(errs, fmt.Errorf("failed to initialize linter %s: %w", initializer.Name(), err))
				continue
			}

			analyzers = append(analyzers, a)
		}
	}

	return analyzers, kerrors.NewAggregate(errs)
}
