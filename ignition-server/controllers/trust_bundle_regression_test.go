package controllers

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/openshift/hypershift/support/util"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/yaml"
)

// Only the external MCO process is replaced. Readiness and cache reconciliation
// use the production code, including propagation of the expected bundle identity.
type validatingBundleProvider struct {
	provider *LocalIgnitionProvider
	calls    int
}

func (p *validatingBundleProvider) GetPayload(ctx context.Context, _, _, _, _, hcHash, _, _, bundleHash string) ([]byte, error) {
	p.calls++
	cm, err := p.provider.fetchAndValidateMCSConfig(ctx, hcHash, bundleHash)
	if err != nil {
		return nil, err
	}
	bundle := &corev1.ConfigMap{}
	if err := yaml.Unmarshal([]byte(cm.Data["user-ca-bundle-config.yaml"]), bundle); err != nil {
		return nil, err
	}
	return []byte(bundle.Data["ca-bundle.crt"]), nil
}

func TestCachedPayloadStillValid(t *testing.T) {
	for _, tc := range []struct {
		name         string
		renderedHash string
		want         bool
	}{
		{"When the rendered bundle matches, it should reuse the payload", "expected", true},
		{"When the rendered bundle differs, it should reject the payload", "old", false},
		{"When the cached payload predates validation, it should reject the payload", "", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			value := CacheValue{CloudConfigHash: "cloud", AdditionalTrustBundleHash: "additional", ProxyTrustedCAHash: "proxy", RenderedTrustBundleHash: tc.renderedHash}
			if got := cachedPayloadStillValid(value, "cloud", "additional", "proxy", "expected"); got != tc.want {
				t.Fatalf("cache valid=%v, want %v", got, tc.want)
			}
		})
	}
}

func testReconcileRenderedTrustBundle(t *testing.T) {
	for _, target := range []string{"new-proxy-ca", ""} {
		t.Run(fmt.Sprintf("When the proxy bundle becomes %q before rendering, it should wait and cache only matching bytes", target), func(t *testing.T) {
			compressed, err := util.CompressAndEncode([]byte("config"))
			if err != nil {
				t.Fatal(err)
			}
			mcs := &corev1.ConfigMap{ObjectMeta: metav1.ObjectMeta{Name: "machine-config-server", Namespace: "cp"}, Data: map[string]string{"configuration-hash": "unchanged", "user-ca-bundle-config.yaml": `{"data":{"ca-bundle.crt":"old-proxy-ca"}}`}}
			managed := &corev1.ConfigMap{ObjectMeta: metav1.ObjectMeta{Name: managedTrustBundleName, Namespace: "cp"}, Data: map[string]string{"ca-bundle.crt": "old-proxy-ca"}}
			secret := &corev1.Secret{ObjectMeta: metav1.ObjectMeta{Name: "token-workers-new", Namespace: "cp", CreationTimestamp: metav1.Now(), Annotations: map[string]string{TokenSecretAnnotation: "true", TokenSecretTokenGenerationTime: time.Now().Format(time.RFC3339Nano)}}, Data: map[string][]byte{
				TokenSecretTokenKey: []byte("new-token"), TokenSecretReleaseKey: []byte("release"), TokenSecretConfigKey: compressed.Bytes(), TokenSecretHCConfigurationHashKey: []byte("unchanged"), TokenSecretRenderedTrustBundleHashKey: []byte(util.HashSimple(target)),
			}}
			c := fake.NewClientBuilder().WithObjects(mcs, managed, secret).Build()
			p := &validatingBundleProvider{provider: &LocalIgnitionProvider{Client: c, Namespace: "cp"}}
			r := &TokenSecretReconciler{Client: c, IgnitionProvider: p, PayloadStore: NewPayloadStore()}
			req := ctrl.Request{NamespacedName: client.ObjectKeyFromObject(secret)}
			if _, err := r.Reconcile(t.Context(), req); err == nil {
				t.Fatal("stale rendering must be rejected")
			}
			if _, found := r.PayloadStore.Get("new-token"); found {
				t.Fatal("stale payload was cached")
			}
			mcs.Data["user-ca-bundle-config.yaml"] = fmt.Sprintf(`{"data":{"ca-bundle.crt":%q}}`, target)
			managed.Data["ca-bundle.crt"] = target
			if err := c.Update(t.Context(), mcs); err != nil {
				t.Fatal(err)
			}
			if err := c.Update(t.Context(), managed); err != nil {
				t.Fatal(err)
			}
			if _, err := r.Reconcile(t.Context(), req); err != nil {
				t.Fatal(err)
			}
			value, found := r.PayloadStore.Get("new-token")
			if !found || string(value.Payload) != target {
				t.Fatalf("unexpected cached payload: %q", value.Payload)
			}
			if _, err := r.Reconcile(t.Context(), req); err != nil {
				t.Fatal(err)
			}
			if p.calls != 2 {
				t.Fatalf("expected rejection then successful rendering; got %d calls", p.calls)
			}
		})
	}
}
