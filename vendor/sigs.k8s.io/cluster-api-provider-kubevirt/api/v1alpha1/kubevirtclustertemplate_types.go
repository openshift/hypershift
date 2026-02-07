/*
Copyright 2021 The Kubernetes Authors.

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

package v1alpha1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	clusterv1 "sigs.k8s.io/cluster-api/api/core/v1beta1" //nolint SA1019
)

// KubevirtClusterTemplateResource describes the data needed to create a KubevirtCluster from a template.
type KubevirtClusterTemplateResource struct {
	ObjectMeta clusterv1.ObjectMeta `json:"metadata,omitempty"`
	Spec       KubevirtClusterSpec  `json:"spec"`
}

// KubevirtClusterTemplateSpec defines the desired state of KubevirtClusterTemplate.
type KubevirtClusterTemplateSpec struct {
	Template KubevirtClusterTemplateResource `json:"template"`
}

// +kubebuilder:object:root=true
// +kubebuilder:resource:path=kubevirtclustertemplates,scope=Namespaced,categories=cluster-api,shortName=kct

// KubevirtClusterTemplate is the Schema for the kubevirtclustertemplates API.
type KubevirtClusterTemplate struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`
	// +kubebuilder:validation:XValidation:rule="self == oldSelf", message="KubevirtClusterTemplate Spec is immutable"
	Spec KubevirtClusterTemplateSpec `json:"spec,omitempty"`
}

// +kubebuilder:object:root=true

// KubevirtClusterTemplateList contains a list of KubevirtClusterTemplates.
type KubevirtClusterTemplateList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []KubevirtClusterTemplate `json:"items"`
}

func init() {
	SchemeBuilder.Register(&KubevirtClusterTemplate{}, &KubevirtClusterTemplateList{})
}
