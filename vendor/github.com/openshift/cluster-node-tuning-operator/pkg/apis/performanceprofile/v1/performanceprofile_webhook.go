package v1

import (
	ctrl "sigs.k8s.io/controller-runtime"
)

// SetupWebhookWithManager enables Webhooks - needed for version conversion
func (r *PerformanceProfile) SetupWebhookWithManager(mgr ctrl.Manager) error {
	return ctrl.NewWebhookManagedBy(mgr).
		For(r).
		Complete()
}
