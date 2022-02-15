/*
Copyright 2021 The Kubernetes Authors.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package v1beta1

import (
	"reflect"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/util/validation/field"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/webhook"
)

// AzureMachineTemplateImmutableMsg ...
const AzureMachineTemplateImmutableMsg = "AzureMachineTemplate spec.template.spec field is immutable. Please create new resource instead. ref doc: https://cluster-api.sigs.k8s.io/tasks/change-machine-template.html"

// SetupWebhookWithManager sets up and registers the webhook with the manager.
func (r *AzureMachineTemplate) SetupWebhookWithManager(mgr ctrl.Manager) error {
	return ctrl.NewWebhookManagedBy(mgr).
		For(r).
		Complete()
}

// +kubebuilder:webhook:verbs=create;update,path=/mutate-infrastructure-cluster-x-k8s-io-v1beta1-azuremachinetemplate,mutating=true,failurePolicy=fail,matchPolicy=Equivalent,groups=infrastructure.cluster.x-k8s.io,resources=azuremachinetemplates,versions=v1beta1,name=default.azuremachinetemplate.infrastructure.cluster.x-k8s.io,sideEffects=None,admissionReviewVersions=v1;v1beta1
// +kubebuilder:webhook:verbs=create;update,path=/validate-infrastructure-cluster-x-k8s-io-v1beta1-azuremachinetemplate,mutating=false,failurePolicy=fail,matchPolicy=Equivalent,groups=infrastructure.cluster.x-k8s.io,resources=azuremachinetemplates,versions=v1beta1,name=validation.azuremachinetemplate.infrastructure.cluster.x-k8s.io,sideEffects=None,admissionReviewVersions=v1;v1beta1

var _ webhook.Defaulter = &AzureMachineTemplate{}
var _ webhook.Validator = &AzureMachineTemplate{}

// ValidateCreate implements webhook.Validator so a webhook will be registered for the type.
func (r *AzureMachineTemplate) ValidateCreate() error {
	spec := r.Spec.Template.Spec

	if allErrs := ValidateAzureMachineSpec(spec); len(allErrs) > 0 {
		return apierrors.NewInvalid(GroupVersion.WithKind("AzureMachineTemplate").GroupKind(), r.Name, allErrs)
	}
	return nil
}

// ValidateUpdate implements webhook.Validator so a webhook will be registered for the type.
func (r *AzureMachineTemplate) ValidateUpdate(oldRaw runtime.Object) error {
	var allErrs field.ErrorList
	old := oldRaw.(*AzureMachineTemplate)

	if !reflect.DeepEqual(r.Spec.Template.Spec, old.Spec.Template.Spec) {
		// The equality failure could be because of default mismatch between v1alpha3 and v1beta1. This happens because
		// the new object `r` will have run through the default webhooks but the old object `old` would not have so.
		// This means if the old object was in v1alpha3, it would not get the new defaults set in v1beta1 resulting
		// in object inequality. To workaround this, we set the v1beta1 defaults here so that the old object also gets
		// the new defaults.

		// We need to set ssh key explicitly, otherwise Default() will create a new one.
		if old.Spec.Template.Spec.SSHPublicKey == "" {
			old.Spec.Template.Spec.SSHPublicKey = r.Spec.Template.Spec.SSHPublicKey
		}

		old.Default()

		// if it's still not equal, return error.
		if !reflect.DeepEqual(r.Spec.Template.Spec, old.Spec.Template.Spec) {
			allErrs = append(allErrs,
				field.Invalid(field.NewPath("AzureMachineTemplate", "spec", "template", "spec"), r, AzureMachineTemplateImmutableMsg),
			)
		}
	}

	if len(allErrs) == 0 {
		return nil
	}
	return apierrors.NewInvalid(GroupVersion.WithKind("AzureMachineTemplate").GroupKind(), r.Name, allErrs)
}

// ValidateDelete implements webhook.Validator so a webhook will be registered for the type.
func (r *AzureMachineTemplate) ValidateDelete() error {
	return nil
}

// Default implements webhookutil.defaulter so a webhook will be registered for the type.
func (r *AzureMachineTemplate) Default() {
	r.Spec.Template.Spec.SetDefaults()
}
