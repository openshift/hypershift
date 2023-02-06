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
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/util/validation/field"
	webhookutils "sigs.k8s.io/cluster-api-provider-azure/util/webhook"
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
	spec := m.Spec

	allErrs := ValidateAzureMachineSpec(spec)

	if errs := ValidateSystemAssignedIdentity(spec.Identity, "", spec.RoleAssignmentName, field.NewPath("roleAssignmentName")); len(errs) > 0 {
		allErrs = append(allErrs, errs...)
	}

	if len(allErrs) == 0 {
		return nil
	}

	return apierrors.NewInvalid(GroupVersion.WithKind("AzureMachine").GroupKind(), m.Name, allErrs)
}

// ValidateUpdate implements webhook.Validator so a webhook will be registered for the type.
func (m *AzureMachine) ValidateUpdate(oldRaw runtime.Object) error {
	var allErrs field.ErrorList
	old := oldRaw.(*AzureMachine)

	if err := webhookutils.ValidateImmutable(
		field.NewPath("Spec", "Image"),
		old.Spec.Image,
		m.Spec.Image); err != nil {
		allErrs = append(allErrs, err)
	}

	if err := webhookutils.ValidateImmutable(
		field.NewPath("Spec", "Identity"),
		old.Spec.Identity,
		m.Spec.Identity); err != nil {
		allErrs = append(allErrs, err)
	}

	if err := webhookutils.ValidateImmutable(
		field.NewPath("Spec", "UserAssignedIdentities"),
		old.Spec.UserAssignedIdentities,
		m.Spec.UserAssignedIdentities); err != nil {
		allErrs = append(allErrs, err)
	}

	if err := webhookutils.ValidateImmutable(
		field.NewPath("Spec", "RoleAssignmentName"),
		old.Spec.RoleAssignmentName,
		m.Spec.RoleAssignmentName); err != nil {
		allErrs = append(allErrs, err)
	}

	if err := webhookutils.ValidateImmutable(
		field.NewPath("Spec", "OSDisk"),
		old.Spec.OSDisk,
		m.Spec.OSDisk); err != nil {
		allErrs = append(allErrs, err)
	}

	if err := webhookutils.ValidateImmutable(
		field.NewPath("Spec", "DataDisks"),
		old.Spec.DataDisks,
		m.Spec.DataDisks); err != nil {
		allErrs = append(allErrs, err)
	}

	if err := webhookutils.ValidateImmutable(
		field.NewPath("Spec", "SSHPublicKey"),
		old.Spec.SSHPublicKey,
		m.Spec.SSHPublicKey); err != nil {
		allErrs = append(allErrs, err)
	}

	if err := webhookutils.ValidateImmutable(
		field.NewPath("Spec", "AllocatePublicIP"),
		old.Spec.AllocatePublicIP,
		m.Spec.AllocatePublicIP); err != nil {
		allErrs = append(allErrs, err)
	}

	if err := webhookutils.ValidateImmutable(
		field.NewPath("Spec", "EnableIPForwarding"),
		old.Spec.EnableIPForwarding,
		m.Spec.EnableIPForwarding); err != nil {
		allErrs = append(allErrs, err)
	}

	if err := webhookutils.ValidateImmutable(
		field.NewPath("Spec", "AcceleratedNetworking"),
		old.Spec.AcceleratedNetworking,
		m.Spec.AcceleratedNetworking); err != nil {
		allErrs = append(allErrs, err)
	}

	if err := webhookutils.ValidateImmutable(
		field.NewPath("Spec", "SpotVMOptions"),
		old.Spec.SpotVMOptions,
		m.Spec.SpotVMOptions); err != nil {
		allErrs = append(allErrs, err)
	}

	if err := webhookutils.ValidateImmutable(
		field.NewPath("Spec", "SecurityProfile"),
		old.Spec.SecurityProfile,
		m.Spec.SecurityProfile); err != nil {
		allErrs = append(allErrs, err)
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
