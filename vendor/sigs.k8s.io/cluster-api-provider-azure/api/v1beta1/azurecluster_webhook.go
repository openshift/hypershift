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
func (c *AzureCluster) SetupWebhookWithManager(mgr ctrl.Manager) error {
	return ctrl.NewWebhookManagedBy(mgr).
		For(c).
		Complete()
}

// +kubebuilder:webhook:verbs=create;update,path=/validate-infrastructure-cluster-x-k8s-io-v1beta1-azurecluster,mutating=false,failurePolicy=fail,matchPolicy=Equivalent,groups=infrastructure.cluster.x-k8s.io,resources=azureclusters,versions=v1beta1,name=validation.azurecluster.infrastructure.cluster.x-k8s.io,sideEffects=None,admissionReviewVersions=v1;v1beta1
// +kubebuilder:webhook:verbs=create;update,path=/mutate-infrastructure-cluster-x-k8s-io-v1beta1-azurecluster,mutating=true,failurePolicy=fail,matchPolicy=Equivalent,groups=infrastructure.cluster.x-k8s.io,resources=azureclusters,versions=v1beta1,name=default.azurecluster.infrastructure.cluster.x-k8s.io,sideEffects=None,admissionReviewVersions=v1;v1beta1

var _ webhook.Validator = &AzureCluster{}
var _ webhook.Defaulter = &AzureCluster{}

// Default implements webhook.Defaulter so a webhook will be registered for the type.
func (c *AzureCluster) Default() {
	c.setDefaults()
}

// ValidateCreate implements webhook.Validator so a webhook will be registered for the type.
func (c *AzureCluster) ValidateCreate() error {
	return c.validateCluster(nil)
}

// ValidateUpdate implements webhook.Validator so a webhook will be registered for the type.
func (c *AzureCluster) ValidateUpdate(oldRaw runtime.Object) error {
	var allErrs field.ErrorList
	old := oldRaw.(*AzureCluster)

	if !reflect.DeepEqual(c.Spec.ResourceGroup, old.Spec.ResourceGroup) {
		allErrs = append(allErrs,
			field.Invalid(field.NewPath("spec", "ResourceGroup"),
				c.Spec.ResourceGroup, "field is immutable"),
		)
	}

	if !reflect.DeepEqual(c.Spec.SubscriptionID, old.Spec.SubscriptionID) {
		allErrs = append(allErrs,
			field.Invalid(field.NewPath("spec", "SubscriptionID"),
				c.Spec.SubscriptionID, "field is immutable"),
		)
	}

	if !reflect.DeepEqual(c.Spec.Location, old.Spec.Location) {
		allErrs = append(allErrs,
			field.Invalid(field.NewPath("spec", "Location"),
				c.Spec.Location, "field is immutable"),
		)
	}

	if !reflect.DeepEqual(c.Spec.AzureEnvironment, old.Spec.AzureEnvironment) {
		// The equality failure could be because of default mismatch between v1alpha3 and v1beta1. This happens because
		// the new object `r` will have run through the default webhooks but the old object `old` would not have so.
		// This means if the old object was in v1alpha3, it would not get the new defaults set in v1beta1 resulting
		// in object inequality. To workaround this, we set the v1beta1 defaults here so that the old object also gets
		// the new defaults.
		old.setAzureEnvironmentDefault()

		// if it's still not equal, return error.
		if !reflect.DeepEqual(c.Spec.AzureEnvironment, old.Spec.AzureEnvironment) {
			allErrs = append(allErrs,
				field.Invalid(field.NewPath("spec", "AzureEnvironment"),
					c.Spec.AzureEnvironment, "field is immutable"),
			)
		}
	}

	if !reflect.DeepEqual(c.Spec.NetworkSpec.PrivateDNSZoneName, old.Spec.NetworkSpec.PrivateDNSZoneName) {
		allErrs = append(allErrs,
			field.Invalid(field.NewPath("spec", "NetworkSpec", "PrivateDNSZoneName"),
				c.Spec.NetworkSpec.PrivateDNSZoneName, "field is immutable"),
		)
	}

	// Allow enabling azure bastion but avoid disabling it.
	if old.Spec.BastionSpec.AzureBastion != nil && !reflect.DeepEqual(old.Spec.BastionSpec.AzureBastion, c.Spec.BastionSpec.AzureBastion) {
		allErrs = append(allErrs,
			field.Invalid(field.NewPath("spec", "BastionSpec", "AzureBastion"),
				c.Spec.BastionSpec.AzureBastion, "azure bastion cannot be removed from a cluster"),
		)
	}

	if !reflect.DeepEqual(c.Spec.NetworkSpec.ControlPlaneOutboundLB, old.Spec.NetworkSpec.ControlPlaneOutboundLB) {
		allErrs = append(allErrs,
			field.Invalid(field.NewPath("spec", "networkSpec", "controlPlaneOutboundLB"),
				c.Spec.NetworkSpec.ControlPlaneOutboundLB, "field is immutable"),
		)
	}

	if len(allErrs) == 0 {
		return c.validateCluster(old)
	}

	return apierrors.NewInvalid(GroupVersion.WithKind("AzureCluster").GroupKind(), c.Name, allErrs)
}

// ValidateDelete implements webhook.Validator so a webhook will be registered for the type.
func (c *AzureCluster) ValidateDelete() error {
	return nil
}
