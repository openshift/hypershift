/*
KAL is a linter for Kubernetes API types, that implements API conventions and best practices.

This package provides a GolangCI-Lint plugin that can be used to build a custom linter for Kubernetes API types.
The custom golangci-lint binary can be built by checking out the KAL repository and running `make build-golangci`.
This will generate a custom golangci-lint binary in the `bin` directory.

The custom golangci-lint binary can be run with the `run` command, and the KAL linters can be enabled by setting the `kal` linter in the `.golangci.yml` configuration file.

Example `.golangci.yml` configuration file:

	linters-settings:
	custom:
	  kal:
	  type: "module"
	  description: KAL is the Kube-API-Linter and lints Kube like APIs based on API conventions and best practices.
	  settings:
	    linters:
	      enabled: []
	      disabled: []
	    lintersConfig:
	      jsonTags:
	        jsonTagRegex: ""
	      optionalOrRequired:
	        preferredOptionalMarker: ""
	        preferredRequiredMarker: ""
	linters:
	  disable-all: true
	  enable:
	    - kal

New linters can be added in the [github.com/JoelSpeed/kal/pkg/analysis] package.
*/
package kal
