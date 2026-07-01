package v1alpha1

import (
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"

	"github.com/openshift/oauth-apiserver/pkg/externaloidc/apis/authentication"
)

const (
	GroupName = "authentication.openshift.io"
)

var (
	schemeBuilder = runtime.NewSchemeBuilder(
		authentication.Install,
		addKnownTypes,
	)
	Install = schemeBuilder.AddToScheme

	// DEPRECATED kept for generated code
	SchemeGroupVersion = schema.GroupVersion{Group: GroupName, Version: "v1alpha1"}
	// DEPRECATED kept for generated code
	AddToScheme        = schemeBuilder.AddToScheme
	localSchemeBuilder = &schemeBuilder
)

// Resource kept for generated code
// DEPRECATED
func Resource(resource string) schema.GroupResource {
	return SchemeGroupVersion.WithResource(resource).GroupResource()
}

// Adds the list of known types to api.Scheme.
func addKnownTypes(scheme *runtime.Scheme) error {
	scheme.AddKnownTypes(SchemeGroupVersion,
		&AuthenticationConfiguration{},
	)
	return nil
}
