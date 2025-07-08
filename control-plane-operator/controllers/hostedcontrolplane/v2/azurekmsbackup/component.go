package azurekmsbackup

import (
	"fmt"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	"github.com/openshift/hypershift/support/azureutil"
	component "github.com/openshift/hypershift/support/controlplane-component"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
)

const (
	ComponentName = "azure-kms-backup"
)

var _ component.ComponentOptions = &AzureKMSBackup{}

type AzureKMSBackup struct {
}

// IsRequestServing implements controlplanecomponent.ComponentOptions.
func (k *AzureKMSBackup) IsRequestServing() bool {
	return false
}

// MultiZoneSpread implements controlplanecomponent.ComponentOptions.
func (k *AzureKMSBackup) MultiZoneSpread() bool {
	return false
}

// NeedsManagementKASAccess implements controlplanecomponent.ComponentOptions.
func (k *AzureKMSBackup) NeedsManagementKASAccess() bool {
	return false
}

func NewComponent() component.ControlPlaneComponent {
	return component.NewDeploymentComponent(ComponentName, &AzureKMSBackup{}).
		WithAdaptFunction(adaptDeployment).
		WithPredicate(isAroHCPwithKMSBackupKey).
		WithManifestAdapter(
			"service.yaml",
			component.WithAdaptFunction(adaptService),
		).
		Build()
}

func isAroHCPwithKMSBackupKey(cpContext component.WorkloadContext) (bool, error) {
	// Only for ARO HCP environments
	if !azureutil.IsAroHCP() {
		return false, nil
	}

	// Only if Azure KMS is configured with a backup key
	if cpContext.HCP.Spec.SecretEncryption != nil &&
		cpContext.HCP.Spec.SecretEncryption.KMS != nil &&
		cpContext.HCP.Spec.SecretEncryption.KMS.Azure != nil &&
		cpContext.HCP.Spec.SecretEncryption.KMS.Azure.BackupKey != nil {
		return true, nil
	}

	return false, nil
}

// adaptDeployment adapts the backup Azure KMS deployment
func adaptDeployment(cpContext component.WorkloadContext, deployment *appsv1.Deployment) error {
	if cpContext.HCP.Spec.SecretEncryption == nil ||
		cpContext.HCP.Spec.SecretEncryption.KMS == nil ||
		cpContext.HCP.Spec.SecretEncryption.KMS.Azure == nil ||
		cpContext.HCP.Spec.SecretEncryption.KMS.Azure.BackupKey == nil {
		return fmt.Errorf("azure KMS backup configuration not found")
	}

	backupKey := *cpContext.HCP.Spec.SecretEncryption.KMS.Azure.BackupKey

	// Set deployment metadata
	deployment.Name = fmt.Sprintf("azure-kms-backup-%s", cpContext.HCP.Name)
	deployment.Namespace = cpContext.HCP.Namespace

	// Configure container
	container := &deployment.Spec.Template.Spec.Containers[0]

	// Check for image override annotation first, then fall back to release image
	if image, ok := cpContext.HCP.Annotations[hyperv1.AzureKMSProviderImage]; ok {
		container.Image = image
	} else {
		container.Image = cpContext.ReleaseImageProvider.GetImage("azure-kms")
	}

	// Add KMS-specific args to the existing deployment file
	dynamicArgs := []string{
		fmt.Sprintf("--keyvault-name=%s", backupKey.KeyVaultName),
		fmt.Sprintf("--key-name=%s", backupKey.KeyName),
		fmt.Sprintf("--key-version=%s", backupKey.KeyVersion),
	}
	container.Args = append(container.Args, dynamicArgs...)

	return nil
}

// adaptService adapts the backup Azure KMS service
func adaptService(cpContext component.WorkloadContext, service *corev1.Service) error {
	service.Name = fmt.Sprintf("azure-kms-backup-%s", cpContext.HCP.Name)
	service.Namespace = cpContext.HCP.Namespace
	return nil
}
