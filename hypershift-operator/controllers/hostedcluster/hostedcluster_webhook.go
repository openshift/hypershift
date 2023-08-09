package hostedcluster

import (
	"fmt"

	hyperv1 "github.com/openshift/hypershift/api/v1beta1"
	ctrl "sigs.k8s.io/controller-runtime"
)

// SetupWebhookWithManager sets up webhooks.
func SetupWebhookWithManager(mgr ctrl.Manager) error {
	err := ctrl.NewWebhookManagedBy(mgr).
		For(&hyperv1.HostedCluster{}).
		Complete()
	if err != nil {
		return fmt.Errorf("unable to register hostedcluster webhook: %w", err)
	}
	err = ctrl.NewWebhookManagedBy(mgr).
		For(&hyperv1.NodePool{}).
		Complete()
	if err != nil {
		return fmt.Errorf("unable to register nodepool webhook: %w", err)
	}
	err = ctrl.NewWebhookManagedBy(mgr).
		For(&hyperv1.HostedControlPlane{}).
		Complete()
	if err != nil {
		return fmt.Errorf("unable to register hostedcontrolplane webhook: %w", err)
	}
	return nil
}
