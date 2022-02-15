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

// SetupWebhookWithManager sets up and registers the webhook with the manager.
func (m *AzureMachine) SetupWebhookWithManager(mgr ctrl.Manager) error {
	return ctrl.NewWebhookManagedBy(mgr).
		For(m).
		Complete()
}

// +kubebuilder:webhook:verbs=create;update,path=/validate-infrastructure-cluster-x-k8s-io-v1beta1-azuremachine,mutating=false,failurePolicy=fail,matchPolicy=Equivalent,groups=infrastructure.cluster.x-k8s.io,resources=azuremachines,versions=v1beta1,name=validation.azuremachine.infrastructure.cluster.x-k8s.io,sideEffects=None,admissionReviewVersions=v1;v1beta1
// +kubebuilder:webhook:verbs=create;update,path=/mutate-infrastructure-cluster-x-k8s-io-v1beta1-azuremachine,mutating=true,failurePolicy=fail,matchPolicy=Equivalent,groups=infrastructure.cluster.x-k8s.io,resources=azuremachines,versions=v1beta1,name=default.azuremachine.infrastructure.cluster.x-k8s.io,sideEffects=None,admissionReviewVersions=v1;v1beta1

var _ webhook.Validator = &AzureMachine{}

// ValidateCreate implements webhook.Validator so a webhook will be registered for the type.
func (m *AzureMachine) ValidateCreate() error {
	if allErrs := ValidateAzureMachineSpec(m.Spec); len(allErrs) > 0 {
		return apierrors.NewInvalid(GroupVersion.WithKind("AzureMachine").GroupKind(), m.Name, allErrs)
	}

	return nil
}

// ValidateUpdate implements webhook.Validator so a webhook will be registered for the type.
func (m *AzureMachine) ValidateUpdate(oldRaw runtime.Object) error {
	var allErrs field.ErrorList
	old := oldRaw.(*AzureMachine)

	if !reflect.DeepEqual(m.Spec.Image, old.Spec.Image) {
		allErrs = append(allErrs,
			field.Invalid(field.NewPath("spec", "image"),
				m.Spec.Image, "field is immutable"),
		)
	}

	if !reflect.DeepEqual(m.Spec.Identity, old.Spec.Identity) {
		allErrs = append(allErrs,
			field.Invalid(field.NewPath("spec", "identity"),
				m.Spec.Identity, "field is immutable"),
		)
	}

	if !reflect.DeepEqual(m.Spec.UserAssignedIdentities, old.Spec.UserAssignedIdentities) {
		allErrs = append(allErrs,
			field.Invalid(field.NewPath("spec", "userAssignedIdentities"),
				m.Spec.UserAssignedIdentities, "field is immutable"),
		)
	}

	if !reflect.DeepEqual(m.Spec.RoleAssignmentName, old.Spec.RoleAssignmentName) {
		allErrs = append(allErrs,
			field.Invalid(field.NewPath("spec", "roleAssignmentName"),
				m.Spec.RoleAssignmentName, "field is immutable"),
		)
	}

	if !reflect.DeepEqual(m.Spec.OSDisk, old.Spec.OSDisk) {
		allErrs = append(allErrs,
			field.Invalid(field.NewPath("spec", "osDisk"),
				m.Spec.OSDisk, "field is immutable"),
		)
	}

	if !reflect.DeepEqual(m.Spec.DataDisks, old.Spec.DataDisks) {
		allErrs = append(allErrs,
			field.Invalid(field.NewPath("spec", "dataDisks"),
				m.Spec.DataDisks, "field is immutable"),
		)
	}

	if !reflect.DeepEqual(m.Spec.SSHPublicKey, old.Spec.SSHPublicKey) {
		allErrs = append(allErrs,
			field.Invalid(field.NewPath("spec", "sshPublicKey"),
				m.Spec.SSHPublicKey, "field is immutable"),
		)
	}

	if !reflect.DeepEqual(m.Spec.AllocatePublicIP, old.Spec.AllocatePublicIP) {
		allErrs = append(allErrs,
			field.Invalid(field.NewPath("spec", "allocatePublicIP"),
				m.Spec.AllocatePublicIP, "field is immutable"),
		)
	}

	if !reflect.DeepEqual(m.Spec.EnableIPForwarding, old.Spec.EnableIPForwarding) {
		allErrs = append(allErrs,
			field.Invalid(field.NewPath("spec", "enableIPForwarding"),
				m.Spec.EnableIPForwarding, "field is immutable"),
		)
	}

	if !reflect.DeepEqual(m.Spec.AcceleratedNetworking, old.Spec.AcceleratedNetworking) {
		allErrs = append(allErrs,
			field.Invalid(field.NewPath("spec", "acceleratedNetworking"),
				m.Spec.AcceleratedNetworking, "field is immutable"),
		)
	}

	if !reflect.DeepEqual(m.Spec.SpotVMOptions, old.Spec.SpotVMOptions) {
		allErrs = append(allErrs,
			field.Invalid(field.NewPath("spec", "spotVMOptions"),
				m.Spec.SpotVMOptions, "field is immutable"),
		)
	}

	if !reflect.DeepEqual(m.Spec.SecurityProfile, old.Spec.SecurityProfile) {
		allErrs = append(allErrs,
			field.Invalid(field.NewPath("spec", "securityProfile"),
				m.Spec.SecurityProfile, "field is immutable"),
		)
	}

	if len(allErrs) == 0 {
		return nil
	}
	return apierrors.NewInvalid(GroupVersion.WithKind("AzureMachine").GroupKind(), m.Name, allErrs)
}

// ValidateDelete implements webhook.Validator so a webhook will be registered for the type.
func (m *AzureMachine) ValidateDelete() error {
	return nil
}

// Default implements webhookutil.defaulter so a webhook will be registered for the type.
func (m *AzureMachine) Default() {
	m.Spec.SetDefaults()
}
