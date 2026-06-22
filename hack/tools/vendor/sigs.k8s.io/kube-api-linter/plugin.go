/*
Copyright 2025 The Kubernetes Authors.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

	http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

// Package kubeapilinter is a golangci-lint plugin for the Kube API Linter (KAL).
// It is built as a module to be used with golangci-lint.
// See https://golangci-lint.run/plugins/module-plugins/ for more information.
package kubeapilinter

import (
	pluginbase "sigs.k8s.io/kube-api-linter/pkg/plugin/base"

	// Import the default linters.
	// DO NOT ADD DIRECTLY TO THIS FILE.
	_ "sigs.k8s.io/kube-api-linter/pkg/registration"
)

// New is the entrypoint for the plugin.
// We import the base plugin here so that custom implementations do not need to import
// this file, but can easily create their own plugin with their own custom set of analyzers.
//
//nolint:gochecknoglobals
var New = pluginbase.New
