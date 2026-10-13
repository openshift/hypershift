package azure

import (
	"strings"
	"testing"

	. "github.com/onsi/gomega"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	"github.com/openshift/hypershift/support/api"
	"github.com/openshift/hypershift/support/upsert"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

func TestSetupOperandCredentials(t *testing.T) {
	makeHCP := func(managed bool) *hyperv1.HostedControlPlane {
		hcp := &hyperv1.HostedControlPlane{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "hcp",
				Namespace: "ns",
			},
			Spec: hyperv1.HostedControlPlaneSpec{
				Platform: hyperv1.PlatformSpec{
					Type: hyperv1.AzurePlatform,
					Azure: &hyperv1.AzurePlatformSpec{
						AzureAuthenticationConfig: hyperv1.AzureAuthenticationConfiguration{},
					},
				},
			},
		}
		if managed {
			hcp.Spec.Platform.Azure.AzureAuthenticationConfig = hyperv1.AzureAuthenticationConfiguration{
				AzureAuthenticationConfigType: hyperv1.AzureAuthenticationTypeManagedIdentities,
				ManagedIdentities: &hyperv1.AzureResourceManagedIdentities{
					DataPlane: hyperv1.DataPlaneManagedIdentities{
						DiskMSIClientID:          "disk-msi",
						FileMSIClientID:          "file-msi",
						ImageRegistryMSIClientID: "registry-msi",
					},
				},
			}
		} else {
			wi := func(id string) hyperv1.WorkloadIdentity {
				return hyperv1.WorkloadIdentity{ClientID: hyperv1.AzureClientID(id)}
			}
			hcp.Spec.Platform.Azure.AzureAuthenticationConfig = hyperv1.AzureAuthenticationConfiguration{
				AzureAuthenticationConfigType: hyperv1.AzureAuthenticationTypeWorkloadIdentities,
				WorkloadIdentities: &hyperv1.AzureWorkloadIdentities{
					Ingress:       wi("ingress-id"),
					Disk:          wi("disk-id"),
					File:          wi("file-id"),
					ImageRegistry: wi("registry-id"),
				},
			}
		}
		return hcp
	}

	// Base data that should be preserved in all created secrets
	baseSecretData := map[string][]byte{
		"some": []byte("data"),
	}

	tests := []struct {
		name           string
		managedAzure   bool
		disableIngress bool
		expectIngress  bool
		expectValues   map[client.ObjectKey]string
	}{
		{
			name:          "managed azure uses placeholder ingress and MSI ids",
			managedAzure:  true,
			expectIngress: true,
			expectValues: map[client.ObjectKey]string{
				{Namespace: "openshift-ingress-operator", Name: "cloud-credentials"}:         placeholderClientID,
				{Namespace: "openshift-cluster-csi-drivers", Name: "azure-disk-credentials"}: "disk-msi",
				{Namespace: "openshift-cluster-csi-drivers", Name: "azure-file-credentials"}: "file-msi",
				{Namespace: "openshift-image-registry", Name: "installer-cloud-credentials"}: "registry-msi",
			},
		},
		{
			name:          "self-managed azure uses workload identity client ids",
			managedAzure:  false,
			expectIngress: true,
			expectValues: map[client.ObjectKey]string{
				{Namespace: "openshift-ingress-operator", Name: "cloud-credentials"}:         "ingress-id",
				{Namespace: "openshift-cluster-csi-drivers", Name: "azure-disk-credentials"}: "disk-id",
				{Namespace: "openshift-cluster-csi-drivers", Name: "azure-file-credentials"}: "file-id",
				{Namespace: "openshift-image-registry", Name: "installer-cloud-credentials"}: "registry-id",
			},
		},
		{
			name:           "ingress capability disabled skips ingress secret only",
			managedAzure:   false,
			disableIngress: true,
			expectIngress:  false,
			expectValues: map[client.ObjectKey]string{
				{Namespace: "openshift-cluster-csi-drivers", Name: "azure-disk-credentials"}: "disk-id",
				{Namespace: "openshift-cluster-csi-drivers", Name: "azure-file-credentials"}: "file-id",
				{Namespace: "openshift-image-registry", Name: "installer-cloud-credentials"}: "registry-id",
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			g := NewWithT(t)
			c := fake.NewClientBuilder().WithScheme(api.Scheme).Build()

			hcp := makeHCP(tc.managedAzure)
			if tc.disableIngress {
				hcp.Spec.Capabilities = &hyperv1.Capabilities{Disabled: []hyperv1.OptionalCapability{hyperv1.IngressCapability}}
			}

			errs := SetupOperandCredentials(t.Context(), c, upsert.New(false), hcp, baseSecretData, tc.managedAzure)
			g.Expect(errs).To(BeEmpty())

			// Verify expected secrets and their azure_client_id values, and that base data is preserved
			for key, expectedID := range tc.expectValues {
				var sec corev1.Secret
				err := c.Get(t.Context(), key, &sec)
				g.Expect(err).ToNot(HaveOccurred())
				g.Expect(string(sec.Data["azure_client_id"])).To(Equal(expectedID))
				// ensure original data copied too
				g.Expect(sec.Data["some"]).To(Equal([]byte("data")))
			}

			// If ingress is expected, validate it exists; otherwise ensure it's absent
			ingressKey := client.ObjectKey{Namespace: "openshift-ingress-operator", Name: "cloud-credentials"}
			var ingressSecret corev1.Secret
			err := c.Get(t.Context(), ingressKey, &ingressSecret)

			if tc.expectIngress {
				g.Expect(err).ToNot(HaveOccurred())
			} else {
				g.Expect(err).To(HaveOccurred())
			}
		})
	}
}

func TestReconcileAzureCloudCredentials(t *testing.T) {
	// Test the core functionality using real upsert provider like other tests in the codebase
	g := NewWithT(t)
	c := fake.NewClientBuilder().WithScheme(api.Scheme).Build()

	secretData := map[string][]byte{
		"base_key": []byte("base_value"),
	}
	azureClientIDs := clientIDs{
		ingress:       "ingress-id",
		azureDisk:     "disk-id",
		azureFile:     "file-id",
		imageRegistry: "registry-id",
	}

	// Test all capabilities enabled
	hcp := &hyperv1.HostedControlPlane{
		Spec: hyperv1.HostedControlPlaneSpec{
			Capabilities: &hyperv1.Capabilities{},
		},
	}
	errs := reconcileAzureCloudCredentials(t.Context(), c, upsert.New(false), hcp, secretData, azureClientIDs)
	g.Expect(errs).To(BeEmpty())

	// Verify all 4 secrets were created with correct data
	expectedSecrets := map[string]map[string]string{
		"openshift-ingress-operator/cloud-credentials":         {"azure_client_id": "ingress-id"},
		"openshift-cluster-csi-drivers/azure-disk-credentials": {"azure_client_id": "disk-id"},
		"openshift-cluster-csi-drivers/azure-file-credentials": {"azure_client_id": "file-id"},
		"openshift-image-registry/installer-cloud-credentials": {"azure_client_id": "registry-id"},
	}

	for secretKey, expectedData := range expectedSecrets {
		parts := strings.Split(secretKey, "/")
		key := client.ObjectKey{Namespace: parts[0], Name: parts[1]}
		var secret corev1.Secret
		err := c.Get(t.Context(), key, &secret)
		g.Expect(err).ToNot(HaveOccurred())
		for dataKey, expectedValue := range expectedData {
			g.Expect(string(secret.Data[dataKey])).To(Equal(expectedValue))
		}
		g.Expect(secret.Data["base_key"]).To(Equal([]byte("base_value")))
	}

	// Test with ingress capability disabled
	c2 := fake.NewClientBuilder().WithScheme(api.Scheme).Build()
	hcp2 := &hyperv1.HostedControlPlane{
		Spec: hyperv1.HostedControlPlaneSpec{
			Capabilities: &hyperv1.Capabilities{
				Disabled: []hyperv1.OptionalCapability{hyperv1.IngressCapability},
			},
		},
	}
	errs = reconcileAzureCloudCredentials(t.Context(), c2, upsert.New(false), hcp2, secretData, azureClientIDs)
	g.Expect(errs).To(BeEmpty())

	// Verify ingress secret was not created
	ingressKey := client.ObjectKey{Namespace: "openshift-ingress-operator", Name: "cloud-credentials"}
	var ingressSecret corev1.Secret
	err := c2.Get(t.Context(), ingressKey, &ingressSecret)
	g.Expect(err).To(HaveOccurred())

	// Verify other secrets were still created
	diskKey := client.ObjectKey{Namespace: "openshift-cluster-csi-drivers", Name: "azure-disk-credentials"}
	var diskSecret corev1.Secret
	err = c2.Get(t.Context(), diskKey, &diskSecret)
	g.Expect(err).ToNot(HaveOccurred())
}
