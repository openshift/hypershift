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
/*
This package is used as an internal registration of linters.

It should not be imported by those seeking to create a custom version of KAL.

Instead, use blank imports in your own registry invocation.
*/
package registration

import (
	_ "sigs.k8s.io/kube-api-linter/pkg/analysis/commentstart"
	_ "sigs.k8s.io/kube-api-linter/pkg/analysis/conditions"
	_ "sigs.k8s.io/kube-api-linter/pkg/analysis/conflictingmarkers"
	_ "sigs.k8s.io/kube-api-linter/pkg/analysis/duplicatemarkers"
	_ "sigs.k8s.io/kube-api-linter/pkg/analysis/integers"
	_ "sigs.k8s.io/kube-api-linter/pkg/analysis/jsontags"
	_ "sigs.k8s.io/kube-api-linter/pkg/analysis/maxlength"
	_ "sigs.k8s.io/kube-api-linter/pkg/analysis/nobools"
	_ "sigs.k8s.io/kube-api-linter/pkg/analysis/nofloats"
	_ "sigs.k8s.io/kube-api-linter/pkg/analysis/nomaps"
	_ "sigs.k8s.io/kube-api-linter/pkg/analysis/nophase"
	_ "sigs.k8s.io/kube-api-linter/pkg/analysis/notimestamp"
	_ "sigs.k8s.io/kube-api-linter/pkg/analysis/optionalfields"
	_ "sigs.k8s.io/kube-api-linter/pkg/analysis/optionalorrequired"
	_ "sigs.k8s.io/kube-api-linter/pkg/analysis/requiredfields"
	_ "sigs.k8s.io/kube-api-linter/pkg/analysis/ssatags"
	_ "sigs.k8s.io/kube-api-linter/pkg/analysis/statusoptional"
	_ "sigs.k8s.io/kube-api-linter/pkg/analysis/statussubresource"
	_ "sigs.k8s.io/kube-api-linter/pkg/analysis/uniquemarkers"
)
