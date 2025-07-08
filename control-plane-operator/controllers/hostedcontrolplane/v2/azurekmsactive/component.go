package azurekmsactive

import (
	"fmt"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	"github.com/openshift/hypershift/support/azureutil"
	component "github.com/openshift/hypershift/support/controlplane-component"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
)

const (
	ComponentName = "azure-kms-active"
)

var _ component.ComponentOptions = &AzureKMSActive{}

type AzureKMSActive struct {
}

// IsRequestServing implements controlplanecomponent.ComponentOptions.
func (k *AzureKMSActive) IsRequestServing() bool {
	return false
}

// MultiZoneSpread implements controlplanecomponent.ComponentOptions.
func (k *AzureKMSActive) MultiZoneSpread() bool {
	return false
}

// NeedsManagementKASAccess implements controlplanecomponent.ComponentOptions.
func (k *AzureKMSActive) NeedsManagementKASAccess() bool {
	return false
}

func NewComponent() component.ControlPlaneComponent {
	return component.NewDeploymentComponent(ComponentName, &AzureKMSActive{}).
		WithAdaptFunction(adaptDeployment).
		WithPredicate(isAroHCPwithKMSActiveKey).
		WithManifestAdapter(
			"service.yaml",
			component.WithAdaptFunction(adaptService),
		).
		Build()
}

func isAroHCPwithKMSActiveKey(cpContext component.WorkloadContext) (bool, error) {
	// Only for ARO HCP environments
	if !azureutil.IsAroHCP() {
		return false, nil
	}

	// Only if Azure KMS is configured with an active key
	if cpContext.HCP.Spec.SecretEncryption != nil &&
		cpContext.HCP.Spec.SecretEncryption.KMS != nil &&
		cpContext.HCP.Spec.SecretEncryption.KMS.Azure != nil &&
		cpContext.HCP.Spec.SecretEncryption.KMS.Azure.ActiveKey.KeyName != "" &&
		cpContext.HCP.Spec.SecretEncryption.KMS.Azure.ActiveKey.KeyVaultName != "" &&
		cpContext.HCP.Spec.SecretEncryption.KMS.Azure.ActiveKey.KeyVersion != "" {
		return true, nil
	}

	return false, nil
}

// adaptDeployment adapts the active Azure KMS deployment
func adaptDeployment(cpContext component.WorkloadContext, deployment *appsv1.Deployment) error {
	if cpContext.HCP.Spec.SecretEncryption == nil ||
		cpContext.HCP.Spec.SecretEncryption.KMS == nil ||
		cpContext.HCP.Spec.SecretEncryption.KMS.Azure == nil {
		return fmt.Errorf("azure KMS configuration not found")
	}

	activeKey := cpContext.HCP.Spec.SecretEncryption.KMS.Azure.ActiveKey

	// Set deployment metadata
	deployment.Name = fmt.Sprintf("azure-kms-active-%s", cpContext.HCP.Name)
	deployment.Namespace = cpContext.HCP.Namespace

	// Configure container
	container := &deployment.Spec.Template.Spec.Containers[0]

	// Check for image override annotation first, then fall back to release image
	if image, ok := cpContext.HCP.Annotations[hyperv1.AzureKMSProviderImage]; ok {
		container.Image = image
	} else {
		container.Image = cpContext.ReleaseImageProvider.GetImage("azure-kms")
	}

	// Add only the dynamic KMS-specific args to the existing static args
	dynamicArgs := []string{
		fmt.Sprintf("--keyvault-name=%s", activeKey.KeyVaultName),
		fmt.Sprintf("--key-name=%s", activeKey.KeyName),
		fmt.Sprintf("--key-version=%s", activeKey.KeyVersion),
	}
	container.Args = append(container.Args, dynamicArgs...)

	return nil
}

// adaptService adapts the active Azure KMS service
func adaptService(cpContext component.WorkloadContext, service *corev1.Service) error {
	service.Name = fmt.Sprintf("azure-kms-active-%s", cpContext.HCP.Name)
	service.Namespace = cpContext.HCP.Namespace
	return nil
}
