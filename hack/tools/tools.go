//go:build tools
// +build tools

// This package contains import references to packages required only for the
// build process.
// https://github.com/golang/go/wiki/Modules#how-can-i-track-tool-dependencies-for-a-module
package tools

import (
	_ "github.com/ahmetb/gen-crd-api-reference-docs"
	_ "github.com/golangci/golangci-lint/cmd/golangci-lint"
	_ "github.com/onsi/ginkgo/v2/ginkgo"

	// jparrill: The RHTAP tool is breaking the RHTAP builds from Feb 27th, so we're stop using it for now
	// more info here https://redhat-internal.slack.com/archives/C031USXS2FJ/p1710177462151639
	//_ "github.com/prometheus/prometheus/cmd/promtool"
	_ "github.com/openshift/api/tools"
	_ "github.com/openshift/api/tools/codegen/cmd"
	_ "go.uber.org/mock/mockgen"
	_ "gotest.tools/gotestsum"
	_ "honnef.co/go/tools/cmd/staticcheck"
	_ "k8s.io/code-generator"
	_ "sigs.k8s.io/kube-api-linter"
)
