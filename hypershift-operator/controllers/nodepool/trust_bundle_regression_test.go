package nodepool

import (
	"fmt"
	"testing"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	"github.com/openshift/hypershift/support/releaseinfo"
	"github.com/openshift/hypershift/support/thirdparty/library-go/pkg/image/dockerv1client"
	"github.com/openshift/hypershift/support/util"
	fakeimagemetadataprovider "github.com/openshift/hypershift/support/util/fakeimagemetadataprovider"

	configv1 "github.com/openshift/api/config/v1"
	"github.com/openshift/api/image/docker10"
	imageapi "github.com/openshift/api/image/v1"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

func migrationConfigFixture() *ConfigGenerator {
	return &ConfigGenerator{
		hostedCluster:         &hyperv1.HostedCluster{ObjectMeta: metav1.ObjectMeta{Name: "hc", Namespace: "clusters"}, Spec: hyperv1.HostedClusterSpec{AdditionalTrustBundle: &corev1.LocalObjectReference{Name: "ca"}}},
		nodePool:              &hyperv1.NodePool{ObjectMeta: metav1.ObjectMeta{Name: "workers", Namespace: "clusters"}, Spec: hyperv1.NodePoolSpec{Management: hyperv1.NodePoolManagement{UpgradeType: hyperv1.UpgradeTypeReplace}}},
		controlplaneNamespace: "clusters-hc",
		rolloutConfig: &rolloutConfig{
			releaseImage:              &releaseinfo.ReleaseImage{ImageStream: &imageapi.ImageStream{ObjectMeta: metav1.ObjectMeta{Name: "4.21.10"}}},
			additionalTrustBundleHash: util.HashSimple("additional"),
			proxyTrustedCAHash:        util.HashSimple("proxy"),
		},
	}
}

func TestRenderedTrustBundleHash(t *testing.T) {
	for _, tc := range []struct {
		name, additional, proxy string
		missing, changed        bool
	}{
		{"When both bundles exist, it should hash additional followed by proxy", "additional", "proxy", false, false},
		{"When no bundles exist, it should hash the empty bundle", "", "", false, false},
		{"When a bundle is missing, it should return an error", "additional", "proxy", true, false},
		{"When source content changes between reads, it should reject the inconsistent snapshot", "additional", "proxy", false, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			hc := &hyperv1.HostedCluster{ObjectMeta: metav1.ObjectMeta{Namespace: "clusters"}}
			var objects []client.Object
			var ah, ph string
			if tc.additional != "" {
				hc.Spec.AdditionalTrustBundle = &corev1.LocalObjectReference{Name: "additional"}
				ah = util.HashSimple(tc.additional)
				objects = append(objects, &corev1.ConfigMap{ObjectMeta: metav1.ObjectMeta{Name: "additional", Namespace: hc.Namespace}, Data: map[string]string{"ca-bundle.crt": tc.additional}})
			}
			if tc.proxy != "" {
				hc.Spec.Configuration = &hyperv1.ClusterConfiguration{Proxy: &configv1.ProxySpec{TrustedCA: configv1.ConfigMapNameReference{Name: "proxy"}}}
				ph = util.HashSimple(tc.proxy)
				if !tc.missing {
					objects = append(objects, &corev1.ConfigMap{ObjectMeta: metav1.ObjectMeta{Name: "proxy", Namespace: hc.Namespace}, Data: map[string]string{"ca-bundle.crt": tc.proxy}})
				}
			}
			if tc.changed {
				ph = util.HashSimple("old")
			}
			c := fake.NewClientBuilder().WithObjects(objects...).Build()
			hash, err := renderedTrustBundleHash(t.Context(), c, hc, ah, ph)
			if tc.missing || tc.changed {
				if err == nil {
					t.Fatal("expected error")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if hash != util.HashSimple(tc.additional+tc.proxy) {
				t.Fatal("unexpected aggregate hash")
			}
		})
	}
}

func testHashAtVersionContentEquivalentReferences(t *testing.T) {
	cg := migrationConfigFixture()
	before := cg.Hash()
	cg.hostedCluster.Spec.AdditionalTrustBundle.Name = "identical-ca-different-name"
	if cg.Hash() != before {
		t.Fatal("identical content under another name should not roll nodes")
	}
}

func TestDetectCPOCapabilities(t *testing.T) {
	for _, tc := range []struct {
		name                                  string
		enabled, missingSecret, metadataError bool
	}{
		{"When the selected image advertises validation, it should enable the capability", true, false, false},
		{"When the selected image lacks validation, it should leave the capability disabled", false, false, false},
		{"When the pull secret is missing, it should return an error", false, true, false},
		{"When metadata lookup fails, it should return an error", false, false, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			hc := &hyperv1.HostedCluster{ObjectMeta: metav1.ObjectMeta{Namespace: "clusters", Annotations: map[string]string{hyperv1.ControlPlaneOperatorImageAnnotation: "quay.io/hypershift/hypershift-operator:latest"}}, Spec: hyperv1.HostedClusterSpec{PullSecret: corev1.LocalObjectReference{Name: "pull-secret"}}}
			var objects []client.Object
			if !tc.missingSecret {
				objects = append(objects, &corev1.Secret{ObjectMeta: metav1.ObjectMeta{Name: "pull-secret", Namespace: "clusters"}, Data: map[string][]byte{corev1.DockerConfigJsonKey: []byte(`{"auths":{}}`)}})
			}
			labels := map[string]string{}
			if tc.enabled {
				labels["io.openshift.hypershift.control-plane-operator-manages.rendered-trust-bundle-validation"] = "true"
			}
			provider := &fakeimagemetadataprovider.FakeRegistryClientImageMetadataProvider{Result: &dockerv1client.DockerImageConfig{Config: &docker10.DockerConfig{Labels: labels}}}
			if tc.metadataError {
				provider.Err = fmt.Errorf("metadata unavailable")
			}
			caps, err := DetectCPOCapabilities(t.Context(), fake.NewClientBuilder().WithObjects(objects...).Build(), hc, nil, provider, "")
			if tc.missingSecret || tc.metadataError {
				if err == nil {
					t.Fatal("expected error")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if caps.RenderedTrustBundleValidation != tc.enabled {
				t.Fatal("incorrect capability")
			}
		})
	}
}

func TestReconcileTokenSecret(t *testing.T) {
	t.Run("When the proxy bundle changes, it should update the token hash", testReconcileTokenSecretProxyTrustedCAHash)
	cg := migrationConfigFixture()
	cg.renderedTrustBundleHash = util.HashSimple("updated-additional" + "proxy")
	token := &Token{ConfigGenerator: cg, additionalTrustBundleHash: []byte(util.HashSimple("updated-additional")), proxyTrustedCAHash: []byte(cg.proxyTrustedCAHash)}
	secret := &corev1.Secret{ObjectMeta: metav1.ObjectMeta{Name: "token-workers-legacy", Namespace: cg.controlplaneNamespace}, Data: map[string][]byte{TokenSecretTokenKey: []byte("retained-token"), TokenSecretAdditionalTrustBundleKey: []byte(util.HashSimple("old-additional"))}}
	if err := token.reconcileTokenSecret(secret); err != nil {
		t.Fatal(err)
	}
	if string(secret.Data[TokenSecretTokenKey]) != "retained-token" {
		t.Fatal("migration rotated the bootstrap identity")
	}
	if string(secret.Data[TokenSecretAdditionalTrustBundleKey]) != string(token.additionalTrustBundleHash) {
		t.Fatal("retained token kept a stale additional trust hash")
	}
	if string(secret.Data[TokenSecretRenderedTrustBundleHashKey]) != cg.renderedTrustBundleHash {
		t.Fatal("retained token is missing rendered bundle validation")
	}
}
