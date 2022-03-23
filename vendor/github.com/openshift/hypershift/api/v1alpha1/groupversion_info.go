// Package v1alpha1 contains API Schema definitions for the hypershift.openshift.io v1alpha1 API group
// +kubebuilder:object:generate=true
// +groupName=hypershift.openshift.io
package v1alpha1

import (
	runtime "k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
)

var (
	// GroupVersion is group version used to register these objects
	GroupVersion  = schema.GroupVersion{Group: "hypershift.openshift.io", Version: "v1alpha1"}
	schemeBuilder = runtime.NewSchemeBuilder(addKnownTypes)
	// Install is a function which adds this version to a scheme
	Install = schemeBuilder.AddToScheme

	// SchemeGroupVersion generated code relies on this name
	// Deprecated
	SchemeGroupVersion = GroupVersion
	// AddToScheme exists solely to keep the old generators creating valid code
	// DEPRECATED
	AddToScheme = schemeBuilder.AddToScheme
)

// Adds the list of known types to api.Scheme.
func addKnownTypes(scheme *runtime.Scheme) error {
	scheme.AddKnownTypes(GroupVersion,
		HostedCluster{},
		HostedClusterList{},
		HostedControlPlane{},
		HostedControlPlaneList{},
	)
	metav1.AddToGroupVersion(scheme, GroupVersion)
	return nil
}
