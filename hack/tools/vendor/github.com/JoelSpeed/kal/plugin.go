package kal

import (
	"fmt"

	kalanalysis "github.com/JoelSpeed/kal/pkg/analysis"
	"github.com/JoelSpeed/kal/pkg/config"
	"github.com/JoelSpeed/kal/pkg/validation"
	"github.com/golangci/plugin-module-register/register"
	"golang.org/x/tools/go/analysis"
	"k8s.io/apimachinery/pkg/util/validation/field"
)

func init() {
	register.Plugin("kal", New)
}

// New creates a new golangci-lint plugin based on the KAL analyzers.
func New(settings any) (register.LinterPlugin, error) {
	s, err := register.DecodeSettings[config.GolangCIConfig](settings)
	if err != nil {
		return nil, fmt.Errorf("error decoding settings: %w", err)
	}

	return &GolangCIPlugin{config: s}, nil
}

// GolangCIPlugin constructs a new plugin for the golangci-lint
// plugin pattern.
// This allows golangci-lint to build a version of itself, containing
// all of the anaylzers included in KAL.
type GolangCIPlugin struct {
	config config.GolangCIConfig
}

// BuildAnalyzers returns all of the analyzers to run, based on the configuration.
func (f *GolangCIPlugin) BuildAnalyzers() ([]*analysis.Analyzer, error) {
	if err := validation.ValidateGolangCIConfig(f.config, field.NewPath("")); err != nil {
		return nil, fmt.Errorf("error in KAL configuration: %w", err)
	}

	registry := kalanalysis.NewRegistry()

	analyzers, err := registry.InitializeLinters(f.config.Linters, f.config.LintersConfig)
	if err != nil {
		return nil, fmt.Errorf("error initializing analyzers: %w", err)
	}

	return analyzers, nil
}

// GetLoadMode implements the golangci-lint plugin interface.
func (f *GolangCIPlugin) GetLoadMode() string {
	return register.LoadModeTypesInfo
}
