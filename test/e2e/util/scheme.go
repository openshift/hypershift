package util

import (
	hyperapi "github.com/openshift/hypershift/support/api"

	awskarpenterapis "github.com/aws/karpenter-provider-aws/pkg/apis"
	awskarpenterv1 "github.com/aws/karpenter-provider-aws/pkg/apis/v1"

	"k8s.io/apimachinery/pkg/runtime"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"

	capikubevirt "sigs.k8s.io/cluster-api-provider-kubevirt/api/v1alpha1"

	operatorsv1 "github.com/operator-framework/api/pkg/operators/v1"
	operatorsv1alpha1 "github.com/operator-framework/api/pkg/operators/v1alpha1"
)

var (
	// scheme used by client in e2e test suite.
	// This scheme was born out of the requirement of certain
	// GVKs in the testing environment that are not required by
	// the client used by a running HyperShift instance.
	scheme = hyperapi.Scheme
)

// Scheme returns the scheme used by e2e test clients
// Exported for use by framework package
func Scheme() *runtime.Scheme {
	return scheme
}

func init() {
	_ = operatorsv1.AddToScheme(scheme)
	_ = operatorsv1alpha1.AddToScheme(scheme)
	_ = capikubevirt.AddToScheme(scheme)

	awsKarpanterGroupVersion := schema.GroupVersion{Group: awskarpenterapis.Group, Version: "v1"}
	metav1.AddToGroupVersion(scheme, awsKarpanterGroupVersion)
	scheme.AddKnownTypes(awsKarpanterGroupVersion, &awskarpenterv1.EC2NodeClass{})
	scheme.AddKnownTypes(awsKarpanterGroupVersion, &awskarpenterv1.EC2NodeClassList{})
}
