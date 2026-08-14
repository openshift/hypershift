package framework

import (
	hypershiftsupport "github.com/openshift/hypershift/support/api"

	"k8s.io/apimachinery/pkg/runtime"

	capikubevirt "sigs.k8s.io/cluster-api-provider-kubevirt/api/v1alpha1"

	operatorsv1 "github.com/operator-framework/api/pkg/operators/v1"
	operatorsv1alpha1 "github.com/operator-framework/api/pkg/operators/v1alpha1"
)

var (
	scheme = hypershiftsupport.Scheme
)

func init() {
	for _, add := range []func(s *runtime.Scheme) error{
		operatorsv1.AddToScheme,
		operatorsv1alpha1.AddToScheme,
		capikubevirt.AddToScheme,
	} {
		if err := add(scheme); err != nil {
			panic(err)
		}
	}
}
