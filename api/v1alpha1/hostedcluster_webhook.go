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
func (r *HostedCluster) ValidateCreate() error {
	log.Info("validate create", "name", r.Name)
	return r.validateHostedCluster()
}

// ValidateUpdate implements webhook.Validator so a webhook will be registered for the type
func (r *HostedCluster) ValidateUpdate(old runtime.Object) error {
	log.Info("validate update", "name", r.Name)
	return r.validateHostedCluster()
}

// ValidateDelete implements webhook.Validator so a webhook will be registered for the type
func (r *HostedCluster) ValidateDelete() error {
	log.Info("validate delete", "name", r.Name)
	return nil
}

func (r *HostedCluster) validateHostedCluster() error {
	var allErrs field.ErrorList
	if err := r.validateFIPS(); err != nil {
		allErrs = append(allErrs, err)
	}
	if len(allErrs) == 0 {
		return nil
	}

	return apierrors.NewInvalid(
		schema.GroupKind{Group: "hypershift.openshift.io", Kind: "HostedCluster"},
		r.Name, allErrs)
}

func (r *HostedCluster) validateFIPS() *field.Error {
	return nil
}
