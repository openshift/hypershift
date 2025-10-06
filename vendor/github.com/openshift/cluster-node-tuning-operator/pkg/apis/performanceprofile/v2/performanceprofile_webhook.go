package v2

import (
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/webhook"
)

var _ webhook.CustomValidator = &PerformanceProfile{}

// we need this variable only because our validate methods should have access to the client
var validatorClient client.Client

// SetupWebhookWithManager enables Webhooks - needed for version conversion
func (r *PerformanceProfile) SetupWebhookWithManager(mgr ctrl.Manager) error {
	if validatorClient == nil {
		validatorClient = mgr.GetClient()
	}

	return ctrl.NewWebhookManagedBy(mgr).
		For(r).
		WithValidator(r).
		Complete()
}
