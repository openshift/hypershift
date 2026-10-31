package gmpmonitoring

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
)

// SchemeGroupVersion is the API version of the copied GMP types.
var SchemeGroupVersion = schema.GroupVersion{Group: "monitoring.googleapis.com", Version: "v1"}

// SchemeBuilder registers only the supported monitoring data objects.
var SchemeBuilder = runtime.NewSchemeBuilder(addKnownTypes)

// AddToScheme registers GMP types without installing CRDs or starting watches.
var AddToScheme = SchemeBuilder.AddToScheme

func addKnownTypes(scheme *runtime.Scheme) error {
	scheme.AddKnownTypes(SchemeGroupVersion,
		&PodMonitoring{}, &PodMonitoringList{}, &Rules{}, &RulesList{},
	)
	metav1.AddToGroupVersion(scheme, SchemeGroupVersion)
	return nil
}
