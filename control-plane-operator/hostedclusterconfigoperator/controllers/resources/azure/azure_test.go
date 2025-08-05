package azure

import (
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
