package config

// GolangCIConfig is the complete configuration for the KAL
// linter when built as an integration into golangci-lint.
type GolangCIConfig struct {
	// Linters allows the user to configure which linters should,
	// and should not be enabled.
	Linters Linters `mapstructure:"linters"`

	// LintersConfig contains configuration for individual linters.
	LintersConfig LintersConfig `mapstructure:"lintersConfig"`
}
