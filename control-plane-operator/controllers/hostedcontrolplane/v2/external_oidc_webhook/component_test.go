package extoidc

import (
	"testing"

	. "github.com/onsi/gomega"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	"github.com/openshift/hypershift/control-plane-operator/featuregates"
	"github.com/openshift/hypershift/support/api"
	component "github.com/openshift/hypershift/support/controlplane-component"
	"github.com/openshift/hypershift/support/testutil"
	"github.com/openshift/hypershift/support/upsert"

	configv1 "github.com/openshift/api/config/v1"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	fgtesting "k8s.io/component-base/featuregate/testing"

	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

func TestExternalOIDCWebhook_IsRequestServing(t *testing.T) {
	t.Parallel()
	g := NewWithT(t)
	options := &externalOIDCWebhook{}

	g.Expect(options.IsRequestServing()).To(BeFalse())
}

func TestExternalOIDCWebhook_MultiZoneSpread(t *testing.T) {
	t.Parallel()
	g := NewWithT(t)
	options := &externalOIDCWebhook{}

	g.Expect(options.MultiZoneSpread()).To(BeTrue())
}

func TestExternalOIDCWebhook_NeedsManagementKASAccess(t *testing.T) {
	t.Parallel()
	g := NewWithT(t)
	options := &externalOIDCWebhook{}

	g.Expect(options.NeedsManagementKASAccess()).To(BeFalse())
}

func TestPredicate(t *testing.T) {
	testCases := []struct {
		name            string
		authentication  *configv1.AuthenticationSpec
		featureEnabled  bool
		expectedEnabled bool
	}{
		{
			name:            "When authentication is not configured, it should return false",
			featureEnabled:  true,
			expectedEnabled: false,
		},
		{
			name: "When authentication is IntegratedOAuth, it should return false",
			authentication: &configv1.AuthenticationSpec{
				Type: configv1.AuthenticationTypeIntegratedOAuth,
			},
			featureEnabled:  true,
			expectedEnabled: false,
		},
		{
			name: "When authentication is OIDC without providers, it should return false",
			authentication: &configv1.AuthenticationSpec{
				Type: configv1.AuthenticationTypeOIDC,
			},
			featureEnabled: true,
		},
		{
			name: "When authentication is OIDC and the feature gate is disabled, it should return false",
			authentication: &configv1.AuthenticationSpec{
				Type:          configv1.AuthenticationTypeOIDC,
				OIDCProviders: []configv1.OIDCProvider{{Name: "test-provider"}},
			},
			expectedEnabled: false,
		},
		{
			name: "When authentication is OIDC and the feature gate is enabled, it should return true",
			authentication: &configv1.AuthenticationSpec{
				Type:          configv1.AuthenticationTypeOIDC,
				OIDCProviders: []configv1.OIDCProvider{{Name: "test-provider"}},
			},
			featureEnabled:  true,
			expectedEnabled: true,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			g := NewWithT(t)
			fgtesting.SetFeatureGateDuringTest(t, featuregates.Gate(), featuregates.ExternalOIDCAsWebhook, tc.featureEnabled)

			cpContext := component.WorkloadContext{
				HCP: &hyperv1.HostedControlPlane{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "test-hcp",
						Namespace: "test-ns",
					},
					Spec: hyperv1.HostedControlPlaneSpec{
						Configuration: &hyperv1.ClusterConfiguration{
							Authentication: tc.authentication,
						},
					},
				},
			}

			enabled, err := predicate(cpContext)
			g.Expect(err).ToNot(HaveOccurred())
			g.Expect(enabled).To(Equal(tc.expectedEnabled))
		})
	}
}

//nolint:hypershiftlinter // testfuncstructure: verifies CA-driven rollout through the webhook factory and shared component reconciliation
func TestManagementCABundleRollout(t *testing.T) {
	fgtesting.SetFeatureGateDuringTest(t, featuregates.Gate(), featuregates.ExternalOIDCAsWebhook, true)
	for _, name := range []string{"HTTP_PROXY", "HTTPS_PROXY", "NO_PROXY"} {
		t.Setenv(name, "")
	}

	testCases := []struct {
		name           string
		initialBundle  string
		updatedBundle  string
		expectedChange bool
	}{
		{
			name: "When the optional management CA bundle is absent, it should reconcile with a stable pod template",
		},
		{
			name:          "When the management CA bundle is unchanged, it should retain the pod template hash",
			initialBundle: "initial management CA",
			updatedBundle: "initial management CA",
		},
		{
			name:           "When the management CA bundle is rotated, it should change the pod template hash",
			initialBundle:  "initial management CA",
			updatedBundle:  "replacement management CA",
			expectedChange: true,
		},
		{
			name:           "When the optional management CA bundle is created, it should change the pod template hash",
			updatedBundle:  "initial management CA",
			expectedChange: true,
		},
		{
			name:           "When the optional management CA bundle is deleted, it should change the pod template hash",
			initialBundle:  "initial management CA",
			expectedChange: true,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			g := NewWithT(t)
			hcp := &hyperv1.HostedControlPlane{
				ObjectMeta: metav1.ObjectMeta{Name: "test-hcp", Namespace: "test-ns"},
				Spec: hyperv1.HostedControlPlaneSpec{
					ReleaseImage: "quay.io/openshift-release-dev/ocp-release:4.22.0-x86_64",
					Platform:     hyperv1.PlatformSpec{Type: hyperv1.AWSPlatform},
					Configuration: &hyperv1.ClusterConfiguration{
						Authentication: &configv1.AuthenticationSpec{
							Type: configv1.AuthenticationTypeOIDC,
							OIDCProviders: []configv1.OIDCProvider{{
								Name:   "test-provider",
								Issuer: configv1.TokenIssuer{URL: "https://issuer.example.com"},
								ClaimMappings: configv1.TokenClaimMappings{
									Username: configv1.UsernameClaimMapping{Claim: "sub", PrefixPolicy: configv1.NoPrefix},
								},
							}},
						},
					},
				},
			}
			cl := fake.NewClientBuilder().WithScheme(api.Scheme).Build()
			bundle := &corev1.ConfigMap{
				ObjectMeta: metav1.ObjectMeta{Name: "openshift-config-managed-trusted-ca-bundle", Namespace: hcp.Namespace},
				Data:       map[string]string{"ca-bundle.crt": tc.initialBundle},
			}
			if tc.initialBundle != "" {
				g.Expect(cl.Create(t.Context(), bundle)).To(Succeed())
			}
			imageProvider := testutil.FakeImageProvider()
			cpContext := component.ControlPlaneContext{
				Context:                  t.Context(),
				Client:                   cl,
				HCP:                      hcp,
				ApplyProvider:            upsert.NewApplyProvider(false),
				ReleaseImageProvider:     imageProvider,
				UserReleaseImageProvider: imageProvider,
				// Keep serving certificates stable so only management trust affects the hash.
				SkipCertificateSigning: true,
			}
			webhook := NewComponent()
			g.Expect(webhook.Reconcile(cpContext)).To(Succeed())
			key := client.ObjectKey{Name: ComponentName, Namespace: hcp.Namespace}
			before := &appsv1.Deployment{}
			g.Expect(cl.Get(t.Context(), key, before)).To(Succeed())
			const hashAnnotation = "component.hypershift.openshift.io/config-hash"
			beforeHash := before.Spec.Template.Annotations[hashAnnotation]
			g.Expect(beforeHash).NotTo(BeEmpty())

			if tc.updatedBundle == "" {
				if tc.initialBundle != "" {
					g.Expect(cl.Delete(t.Context(), bundle)).To(Succeed())
				}
				g.Expect(apierrors.IsNotFound(cl.Get(t.Context(), client.ObjectKeyFromObject(bundle), &corev1.ConfigMap{}))).To(BeTrue())
			} else {
				bundle.Data["ca-bundle.crt"] = tc.updatedBundle
				if tc.initialBundle == "" {
					g.Expect(cl.Create(t.Context(), bundle)).To(Succeed())
				} else {
					g.Expect(cl.Update(t.Context(), bundle)).To(Succeed())
				}
			}

			g.Expect(webhook.Reconcile(cpContext)).To(Succeed())
			after := &appsv1.Deployment{}
			g.Expect(cl.Get(t.Context(), key, after)).To(Succeed())
			afterHash := after.Spec.Template.Annotations[hashAnnotation]
			if tc.expectedChange {
				g.Expect(afterHash).NotTo(Equal(beforeHash))
			} else {
				g.Expect(afterHash).To(Equal(beforeHash))
			}
			// A bundle change should change only the pod template's resource hash.
			template := after.Spec.Template.DeepCopy()
			template.Annotations[hashAnnotation] = beforeHash
			g.Expect(*template).To(Equal(before.Spec.Template))

			g.Expect(webhook.Reconcile(cpContext)).To(Succeed())
			stable := &appsv1.Deployment{}
			g.Expect(cl.Get(t.Context(), key, stable)).To(Succeed())
			g.Expect(stable.Spec.Template).To(Equal(after.Spec.Template))
		})
	}
}
