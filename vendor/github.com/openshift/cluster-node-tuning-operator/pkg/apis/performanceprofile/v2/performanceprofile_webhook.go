package v2

import (
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

var validatorClient client.Client

// SetupWebhookWithManager enables Webhooks - needed for version conversion
func (r *PerformanceProfile) SetupWebhookWithManager(mgr ctrl.Manager) error {
	if validatorClient == nil {
		validatorClient = mgr.GetClient()
	}
	return nil
}
