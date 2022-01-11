package ibmcloud

import (
	"context"
	"fmt"

	configv1 "github.com/openshift/api/config/v1"

	corev1 "k8s.io/api/core/v1"

	hyperv1 "github.com/openshift/hypershift/api/v1alpha1"
	"github.com/openshift/hypershift/support/upsert"
	appsv1 "k8s.io/api/apps/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	capiibmv1 "sigs.k8s.io/cluster-api-provider-ibmcloud/api/v1beta1"
	capiv1 "sigs.k8s.io/cluster-api/api/v1beta1"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

type IBMCloud struct{}

func (p IBMCloud) ReconcileCAPIInfraCR(ctx context.Context, c client.Client, createOrUpdate upsert.CreateOrUpdateFN,
	hcluster *hyperv1.HostedCluster,
	controlPlaneNamespace string,
	apiEndpoint hyperv1.APIEndpoint) (client.Object, error) {
	if hcluster.Spec.Platform.IBMCloud != nil && hcluster.Spec.Platform.IBMCloud.ProviderType == configv1.IBMCloudProviderTypeUPI {
		return nil, nil
	}
	ibmCluster := &capiibmv1.IBMVPCCluster{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: controlPlaneNamespace,
			Name:      hcluster.Name,
		},
	}

	_, err := createOrUpdate(ctx, c, ibmCluster, func() error {
		ibmCluster.Annotations = map[string]string{
			capiv1.ManagedByAnnotation: "external",
		}

		// Set the values for upper level controller
		ibmCluster.Status.Ready = true
		ibmCluster.Spec.ControlPlaneEndpoint = capiv1.APIEndpoint{
			Host: apiEndpoint.Host,
			Port: apiEndpoint.Port,
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	// reconciliation strips TypeMeta. We repopulate the static values since they are necessary for
	// downstream reconciliation of the CAPI Cluster resource.
	ibmCluster.TypeMeta = metav1.TypeMeta{
		Kind:       "IBMVPCCluster",
		APIVersion: capiibmv1.GroupVersion.String(),
	}
	return ibmCluster, nil
}

func (p IBMCloud) CAPIProviderDeploymentSpec(hcluster *hyperv1.HostedCluster, tokenMinterImage string) (*appsv1.DeploymentSpec, error) {
	return nil, nil
}

func (p IBMCloud) ReconcileCredentials(ctx context.Context, c client.Client, createOrUpdate upsert.CreateOrUpdateFN,
	hcluster *hyperv1.HostedCluster,
	controlPlaneNamespace string) error {
	return nil
}

func (IBMCloud) ReconcileSecretEncryption(ctx context.Context, c client.Client, createOrUpdate upsert.CreateOrUpdateFN,
	hcluster *hyperv1.HostedCluster,
	controlPlaneNamespace string) error {
	if hcluster.Spec.SecretEncryption.KMS.IBMCloud == nil {
		return fmt.Errorf("ibm kms metadata nil")
	}
	if hcluster.Spec.SecretEncryption.KMS.IBMCloud.Auth.Type == hyperv1.IBMCloudKMSUnmanagedAuth {
		if hcluster.Spec.SecretEncryption.KMS.IBMCloud.Auth.Unmanaged == nil || len(hcluster.Spec.SecretEncryption.KMS.IBMCloud.Auth.Unmanaged.Credentials.Name) == 0 {
			return fmt.Errorf("ibm unmanaged auth credential nil")
		}
		var src corev1.Secret
		if err := c.Get(ctx, client.ObjectKey{Namespace: hcluster.GetNamespace(), Name: hcluster.Spec.SecretEncryption.KMS.IBMCloud.Auth.Unmanaged.Credentials.Name}, &src); err != nil {
			return fmt.Errorf("failed to get ibmcloud kms credentials %s: %w", hcluster.Spec.SecretEncryption.KMS.IBMCloud.Auth.Unmanaged.Credentials.Name, err)
		}
		if _, ok := src.Data[hyperv1.IBMCloudIAMAPIKeySecretKey]; !ok {
			return fmt.Errorf("no ibmcloud iam apikey field %s specified in auth secret", hyperv1.IBMCloudIAMAPIKeySecretKey)
		}
		hostedControlPlaneIBMCloudKMSAuthSecret := &corev1.Secret{
			ObjectMeta: metav1.ObjectMeta{
				Namespace: controlPlaneNamespace,
				Name:      src.Name,
			},
		}
		_, err := createOrUpdate(ctx, c, hostedControlPlaneIBMCloudKMSAuthSecret, func() error {
			if hostedControlPlaneIBMCloudKMSAuthSecret.Data == nil {
				hostedControlPlaneIBMCloudKMSAuthSecret.Data = map[string][]byte{}
			}
			hostedControlPlaneIBMCloudKMSAuthSecret.Data[hyperv1.IBMCloudIAMAPIKeySecretKey] = src.Data[hyperv1.IBMCloudIAMAPIKeySecretKey]
			hostedControlPlaneIBMCloudKMSAuthSecret.Type = corev1.SecretTypeOpaque
			return nil
		})
		if err != nil {
			return fmt.Errorf("failed reconciling aescbc backup key: %w", err)
		}
	}
	return nil
}

func (IBMCloud) CAPIProviderPolicyRules() []rbacv1.PolicyRule {
	return nil
}
