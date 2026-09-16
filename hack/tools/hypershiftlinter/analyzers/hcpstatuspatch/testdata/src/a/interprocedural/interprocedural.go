package interprocedural

import (
	"github.com/openshift/hypershift/api/hypershift/v1beta1"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

func patchWithParameter(c client.Client, hcp *v1beta1.HostedControlPlane, patch client.Patch) error {
	return c.Status().Patch(nil, hcp, patch)
}

func passPatchAsParameter(c client.Client, hcp *v1beta1.HostedControlPlane) error {
	patch := client.MergeFrom(hcp)
	return patchWithParameter(c, hcp, patch)
}
