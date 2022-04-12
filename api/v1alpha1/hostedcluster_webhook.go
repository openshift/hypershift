package v1alpha1

import (
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	runtime "k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/util/validation/field"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/webhook"
)

var log = ctrl.Log.WithName("hostedcluster")

func (r *HostedCluster) SetupWebhookWithManager(mgr ctrl.Manager) error {
	return ctrl.NewWebhookManagedBy(mgr).
		For(r).
		Complete()
}

var _ webhook.Validator = &HostedCluster{}

// ValidateCreate implements webhook.Validator so a webhook will be registered for the type
// NOTE: add CREATE in the ValidatingWebhookConfiguration for this to work
func (r *HostedCluster) ValidateCreate() error {
	log.Info("HostedCluster validate create", "name", r.Name)
	return nil
}

// ValidateUpdate implements webhook.Validator so a webhook will be registered for the type
func (r *HostedCluster) ValidateUpdate(old runtime.Object) error {
	log.Info("HostedCluster validate update", "name", r.Name)
	var allErrs field.ErrorList

	// TODO: Do immutablity enforcement here

	if len(allErrs) == 0 {
		return nil
	}
	return apierrors.NewInvalid(
		schema.GroupKind{Group: GroupVersion.Group, Kind: "HostedCluster"},
		r.Name, allErrs)
}

// ValidateDelete implements webhook.Validator so a webhook will be registered for the type
// NOTE: add DELETE in the ValidatingWebhookConfiguration for this to work
func (r *HostedCluster) ValidateDelete() error {
	log.Info("HostedCluster validate delete", "name", r.Name)
	return nil
}
