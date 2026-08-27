package good

import (
	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// Seeding fixture status via Status().Update() in a unit test is fine — there's
// no concurrent writer to race with in a synchronous test.
func seedFixtureStatus(c client.Client, hcp *hyperv1.HostedControlPlane) error {
	return c.Status().Update(nil, hcp)
}
