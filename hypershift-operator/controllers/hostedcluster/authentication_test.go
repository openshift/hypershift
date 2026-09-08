package hostedcluster

import (
	"errors"
	"testing"
	"time"

	. "github.com/onsi/gomega"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	"github.com/openshift/hypershift/api/util/ipnet"
	"github.com/openshift/hypershift/hypershift-operator/controllers/manifests"
	"github.com/openshift/hypershift/hypershift-operator/controllers/manifests/controlplaneoperator"
	"github.com/openshift/hypershift/support/api"
	"github.com/openshift/hypershift/support/releaseinfo"
	"github.com/openshift/hypershift/support/releaseinfo/testutils"
	"github.com/openshift/hypershift/support/upsert"

	configv1 "github.com/openshift/api/config/v1"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	"github.com/blang/semver"
	"go.uber.org/mock/gomock"
)

func TestValidateOCPConfigurationsExternalClaimsSources(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name           string
		version        string
		featureSet     configv1.FeatureSet
		externalClaims bool
		wantError      bool
	}{
		{name: "When targeting 4.23 it should reject external claims", version: "4.23.0", featureSet: configv1.TechPreviewNoUpgrade, externalClaims: true, wantError: true},
		{name: "When targeting 4.24 it should not alias it to 5.1", version: "4.24.0", featureSet: configv1.TechPreviewNoUpgrade, externalClaims: true, wantError: true},
		{name: "When targeting 5.0 it should reject external claims", version: "5.0.9", featureSet: configv1.TechPreviewNoUpgrade, externalClaims: true, wantError: true},
		{name: "When targeting 5.1 with the gate it should accept external claims", version: "5.1.0", featureSet: configv1.TechPreviewNoUpgrade, externalClaims: true},
		{name: "When targeting a 5.1 prerelease it should accept external claims", version: "5.1.0-rc.1", featureSet: configv1.TechPreviewNoUpgrade, externalClaims: true},
		{name: "When targeting a 5.1 patch it should accept external claims", version: "5.1.3", featureSet: configv1.TechPreviewNoUpgrade, externalClaims: true},
		{name: "When targeting a later minor it should accept external claims", version: "5.2.0", featureSet: configv1.TechPreviewNoUpgrade, externalClaims: true},
		{name: "When targeting a later major it should accept external claims", version: "6.0.0", featureSet: configv1.TechPreviewNoUpgrade, externalClaims: true},
		{name: "When using the default feature set it should reject external claims", version: "5.1.0", externalClaims: true, wantError: true},
		{name: "When using an unknown feature set it should reject external claims", version: "5.1.0", featureSet: "unknown", externalClaims: true, wantError: true},
		{name: "When no external claims are configured it should accept ordinary OIDC on older releases", version: "4.22.0"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			g := NewWithT(t)
			authn := &configv1.AuthenticationSpec{
				Type: configv1.AuthenticationTypeOIDC,
				OIDCProviders: []configv1.OIDCProvider{{
					Name: "issuer",
					Issuer: configv1.TokenIssuer{
						URL:       "https://issuer.example.com",
						Audiences: []configv1.TokenAudience{"client"},
					},
					ClaimMappings: configv1.TokenClaimMappings{
						Username: configv1.UsernameClaimMapping{Claim: "sub", PrefixPolicy: configv1.NoPrefix},
					},
				}},
			}
			if tc.externalClaims {
				// Use the second provider to verify the field path identifies the offending provider.
				provider := *authn.OIDCProviders[0].DeepCopy()
				provider.Name = "external"
				provider.Issuer.URL = "https://external.example.com"
				provider.ExternalClaimsSources = []configv1.ExternalClaimsSource{{
					URL:      configv1.SourceURL{Hostname: "claims.example.com", PathExpression: "'/userinfo'"},
					Mappings: []configv1.SourcedClaimMapping{{Name: "groups", Expression: "response.groups"}},
				}}
				authn.OIDCProviders = append(authn.OIDCProviders, provider)
			}
			hc := &hyperv1.HostedCluster{Spec: hyperv1.HostedClusterSpec{
				Configuration: &hyperv1.ClusterConfiguration{Authentication: authn},
			}}
			r := &HostedClusterReconciler{FeatureSet: tc.featureSet, Client: fake.NewClientBuilder().WithScheme(api.Scheme).Build()}
			err := r.validateOCPConfigurations(t.Context(), hc, r.Client, semver.MustParse(tc.version))
			if tc.wantError {
				g.Expect(err).To(MatchError(ContainSubstring("spec.configuration.authentication.oidcProviders[1].externalClaimsSources: Forbidden: requires control plane version 5.1 or later")))
			} else {
				g.Expect(err).NotTo(HaveOccurred())
			}
		})
	}

	t.Run("When authentication is absent it should skip authentication validation", func(t *testing.T) {
		t.Parallel()
		g := NewWithT(t)
		r := &HostedClusterReconciler{Client: fake.NewClientBuilder().WithScheme(api.Scheme).Build()}
		hc := &hyperv1.HostedCluster{}
		g.Expect(r.validateOCPConfigurations(t.Context(), hc, r.Client, semver.Version{})).To(Succeed())
		hc.Spec.Configuration = &hyperv1.ClusterConfiguration{}
		g.Expect(r.validateOCPConfigurations(t.Context(), hc, r.Client, semver.Version{})).To(Succeed())
	})
}

func TestReconcileAuthenticationReleaseLookupFailure(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name    string
		version string
		err     error
	}{
		{name: "When the release lookup fails it should persist unknown validation and retry", err: errors.New("registry unavailable")},
		{name: "When the release version is malformed it should persist unknown validation and retry", version: "invalid-version"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			g := NewWithT(t)
			hc := &hyperv1.HostedCluster{
				ObjectMeta: metav1.ObjectMeta{Namespace: "clusters", Name: "example", Generation: 2},
				Spec: hyperv1.HostedClusterSpec{
					Release:             hyperv1.Release{Image: "worker-release"},
					ControlPlaneRelease: &hyperv1.Release{Image: "control-plane-release"},
					PullSecret:          corev1.LocalObjectReference{Name: "pull-secret"},
					Networking: hyperv1.ClusterNetworking{
						ClusterNetwork: []hyperv1.ClusterNetworkEntry{{CIDR: *ipnet.MustParseCIDR("172.16.1.0/24")}},
					},
				},
				Status: hyperv1.HostedClusterStatus{Conditions: []metav1.Condition{{
					Type: string(hyperv1.ValidHostedClusterConfiguration), Status: metav1.ConditionTrue, ObservedGeneration: 1,
				}}},
			}
			hcp := controlplaneoperator.HostedControlPlane(manifests.HostedControlPlaneNamespace(hc.Namespace, hc.Name), hc.Name)
			hcp.Spec.ReleaseImage = "previous-release"
			originalHCP := hcp.DeepCopy()
			pullSecret := &corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{Namespace: hc.Namespace, Name: hc.Spec.PullSecret.Name},
				Data:       map[string][]byte{corev1.DockerConfigJsonKey: []byte("{}")},
			}
			c := fake.NewClientBuilder().WithScheme(api.Scheme).WithObjects(hc, hcp, pullSecret).WithStatusSubresource(hc).Build()
			provider := releaseinfo.NewMockProviderWithOpenShiftImageRegistryOverrides(gomock.NewController(t))
			var release *releaseinfo.ReleaseImage
			if tc.version != "" {
				release = testutils.InitReleaseImageOrDie(tc.version)
			}
			provider.EXPECT().Lookup(gomock.Any(), "control-plane-release", []byte("{}")).Return(release, tc.err)
			r := &HostedClusterReconciler{
				Client:           c,
				RegistryProvider: fakeReleaseProvider{releaseProvider: provider},
				createOrUpdate:   func(ctrl.Request) upsert.CreateOrUpdateFN { return ctrl.CreateOrUpdate },
				now:              func() metav1.Time { return metav1.NewTime(time.Now()) },
			}
			_, err := r.Reconcile(t.Context(), ctrl.Request{NamespacedName: client.ObjectKeyFromObject(hc)})
			g.Expect(err).To(MatchError(ContainSubstring("failed to resolve control plane release version")))
			g.Expect(c.Get(t.Context(), client.ObjectKeyFromObject(hc), hc)).To(Succeed())
			condition := meta.FindStatusCondition(hc.Status.Conditions, string(hyperv1.ValidHostedClusterConfiguration))
			g.Expect(condition).NotTo(BeNil())
			g.Expect(condition.Status).To(Equal(metav1.ConditionUnknown))
			g.Expect(condition.ObservedGeneration).To(Equal(hc.Generation))
			g.Expect(condition.Message).To(Equal(err.Error()))
			g.Expect(c.Get(t.Context(), client.ObjectKeyFromObject(hcp), hcp)).To(Succeed())
			g.Expect(hcp.Spec).To(Equal(originalHCP.Spec))
		})
	}
}
