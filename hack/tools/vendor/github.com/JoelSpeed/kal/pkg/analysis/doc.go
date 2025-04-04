/*
analysis providers a linter registry and a set of linters that can be used to analyze Go code.
The linters in this package are focused on Kubernetes API types and implmement API conventions
and best practices.

To use the linters provided by KAL, initialise an instance of the Registry and then initialize
the linters within, by passing the required configuration.

Example:

	registry := analysis.NewRegistry()

	// Initialize the linters
	linters, err := registry.InitLinters(
		config.Linters{
			Enabled: []string{
				"commentstart"
				"jsontags",
				"optionalorrequired",
			},
			Disabled: []string{
				...
			},
		},
		config.LintersConfig{
			JSONTags: config.JSONTagsConfig{
				JSONTagRegex: `^[-_a-zA-Z0-9]+$`,
			},
			OptionalOrRequired: config.OptionalOrRequiredConfig{
				PreferredOptionalMarker: optionalorrequired.OptionalMarker,
				PreferredRequiredMarker: optionalorrequired.RequiredMarker,
			},
		},
	)

The provided list of analyzers can be used with `multichecker.Main()` from the `golang.org/x/tools/go/analysis/multichecker` package,
or as part of a custom analysis pipeline, eg via the golangci-lint plugin system.

Linters provided by KAL:
  - [commentstart]: Linter to ensure that comments start with the serialized version of the field name.
  - [jsontags]: Linter to ensure that JSON tags are present on struct fields, and that they match a given regex.
  - [optionalorrequired]: Linter to ensure that all fields are marked as either optional or required.

When adding new linters, ensure that they are added to the registry in the `NewRegistry` function.
Linters should not depend on other linters, unless that linter has no configuration and is always enabled,
see the helpers package.

Any common, or shared functionality to extract data from types, should be added as a helper function in the helpers package.
The available helpers are:
  - extractjsontags: Extracts JSON tags from struct fields and returns the information in a structured format.
  - markers: Extracts marker information from types and returns the information in a structured format.
*/
package analysis
