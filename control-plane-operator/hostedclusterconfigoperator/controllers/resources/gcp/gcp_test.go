package gcp

import (
	"encoding/json"
	"testing"

	. "github.com/onsi/gomega"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	"github.com/openshift/hypershift/support/api"
	"github.com/openshift/hypershift/support/gcputil"
	"github.com/openshift/hypershift/support/upsert"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

const (
	testImageRegistryGSA = "image-registry@test-project.iam.gserviceaccount.com"
	testProjectNumber    = "123456789012"
	testPoolID           = "test-pool"
	testProviderID       = "test-provider"
)

func makeHCP() *hyperv1.HostedControlPlane {
	return &hyperv1.HostedControlPlane{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "hcp",
			Namespace: "ns",
		},
		Spec: hyperv1.HostedControlPlaneSpec{
			Platform: hyperv1.PlatformSpec{
				Type: hyperv1.GCPPlatform,
				GCP: &hyperv1.GCPPlatformSpec{
					WorkloadIdentity: hyperv1.GCPWorkloadIdentityConfig{
						ProjectNumber: testProjectNumber,
						PoolID:        testPoolID,
						ProviderID:    testProviderID,
						ServiceAccountsEmails: hyperv1.GCPServiceAccountsEmails{
							ImageRegistry: testImageRegistryGSA,
						},
					},
				},
			},
		},
	}
}

func TestSetupOperandCredentials(t *testing.T) {
	tests := []struct {
		name                   string
		disableImageRegistry   bool
		createNamespace        bool
		expectImageRegistrySec bool
	}{
		{
			name:                   "When image registry capability is enabled it should create the credential secret",
			createNamespace:        true,
			expectImageRegistrySec: true,
		},
		{
			name:                 "When image registry capability is disabled it should skip the credential secret",
			disableImageRegistry: true,
			createNamespace:      true,
		},
		{
			name:            "When target namespace does not exist it should skip without error",
			createNamespace: false,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			g := NewWithT(t)
			c := fake.NewClientBuilder().WithScheme(api.Scheme).Build()

			if tc.createNamespace {
				ns := &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: "openshift-image-registry"}}
				g.Expect(c.Create(t.Context(), ns)).To(Succeed())
			}

			hcp := makeHCP()
			if tc.disableImageRegistry {
				hcp.Spec.Capabilities = &hyperv1.Capabilities{
					Disabled: []hyperv1.OptionalCapability{hyperv1.ImageRegistryCapability},
				}
			}

			errs := SetupOperandCredentials(t.Context(), c, upsert.New(false), hcp)
			g.Expect(errs).To(BeEmpty())

			key := client.ObjectKey{Namespace: "openshift-image-registry", Name: "installer-cloud-credentials"}
			var sec corev1.Secret
			err := c.Get(t.Context(), key, &sec)

			if tc.expectImageRegistrySec {
				g.Expect(err).ToNot(HaveOccurred())
				g.Expect(sec.Data).To(HaveKey("service_account.json"))
				g.Expect(sec.Type).To(Equal(corev1.SecretTypeOpaque))

				// Validate the WIF credential JSON structure
				var cred gcputil.ExternalAccountCredential
				g.Expect(json.Unmarshal(sec.Data["service_account.json"], &cred)).To(Succeed())
				g.Expect(cred.Type).To(Equal("external_account"))
				g.Expect(cred.Audience).To(ContainSubstring(testProjectNumber))
				g.Expect(cred.Audience).To(ContainSubstring(testPoolID))
				g.Expect(cred.Audience).To(ContainSubstring(testProviderID))
				g.Expect(cred.ServiceAccountImpersonationURL).To(ContainSubstring(testImageRegistryGSA))
				g.Expect(cred.CredentialSource.File).To(Equal("/var/run/secrets/openshift/serviceaccount/token"))
			} else {
				g.Expect(apierrors.IsNotFound(err)).To(BeTrue(), "expected image registry credentials secret to be absent")
			}
		})
	}
}
