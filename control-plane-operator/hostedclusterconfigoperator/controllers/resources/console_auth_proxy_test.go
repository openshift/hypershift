package resources

import (
	"context"
	"fmt"
	"testing"

	. "github.com/onsi/gomega"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	"github.com/openshift/hypershift/control-plane-operator/hostedclusterconfigoperator/api"
	"github.com/openshift/hypershift/control-plane-operator/hostedclusterconfigoperator/controllers/resources/manifests"
	"github.com/openshift/hypershift/support/proxy"

	operatorv1 "github.com/openshift/api/operator/v1"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"
)

func TestReconcileConsoleAuthProxy(t *testing.T) {
	const (
		hcpNamespace = "clusters-test"
		proxyCAName  = "auth-proxy-ca"
		hcpInfraID   = "test-infra-id"
	)

	newHCP := func() *hyperv1.HostedControlPlane {
		return &hyperv1.HostedControlPlane{
			ObjectMeta: metav1.ObjectMeta{Name: "test-hcp", Namespace: hcpNamespace},
			Spec: hyperv1.HostedControlPlaneSpec{
				InfraID: hcpInfraID,
				OperatorConfiguration: &hyperv1.OperatorConfiguration{
					Authentication: hyperv1.AuthenticationOperatorSpec{
						Proxy: operatorv1.AuthenticationProxyConfig{
							HTTPSProxy: "https://auth.example.com:8443",
							NoProxy:    []string{".svc"},
							TrustedCA:  operatorv1.AuthenticationConfigMapReference{Name: proxyCAName},
						},
					},
				},
			},
		}
	}

	newSourceCA := func() *corev1.ConfigMap {
		return &corev1.ConfigMap{
			ObjectMeta: metav1.ObjectMeta{Name: proxy.AuthProxyTrustedCAConfigMapName, Namespace: hcpNamespace},
			Data:       map[string]string{"ca-bundle.crt": "-----BEGIN CERTIFICATE-----\n..."},
		}
	}

	// newOwnedCA returns the guest CA ConfigMap as this control plane would
	// have created it: the infra-id ownership marker is what cleanup checks.
	newOwnedCA := func() *corev1.ConfigMap {
		caCM := manifests.ConsoleAuthProxyTrustedCAConfigMap()
		caCM.Labels = map[string]string{hyperv1.InfraIDLabel: hcpInfraID}
		caCM.Data = map[string]string{"ca-bundle.crt": "-----BEGIN CERTIFICATE-----\n..."}
		return caCM
	}

	t.Run("When the override is set, it should persist the field and copy the CA bundle", func(t *testing.T) {
		g := NewWithT(t)
		ctx := t.Context()

		// The guest client and the uncached client share one backing store,
		// as both point at the same guest API server in production.
		guestClient := fake.NewClientBuilder().WithScheme(api.Scheme).Build()
		cpClient := fake.NewClientBuilder().WithScheme(api.Scheme).
			WithObjects(newHCP(), newSourceCA()).Build()

		r := &reconciler{
			client:                 guestClient,
			uncachedClient:         guestClient,
			cpClient:               cpClient,
			CreateOrUpdateProvider: &simpleCreateOrUpdater{},
		}

		g.Expect(r.reconcileConsoleAuthProxy(ctx, newHCP())).To(Succeed())

		console := manifests.ConsoleOperatorConfig()
		g.Expect(guestClient.Get(ctx, client.ObjectKeyFromObject(console), console)).To(Succeed())
		g.Expect(console.Spec.AuthProxy.HTTPSProxy).To(Equal("https://auth.example.com:8443"))
		g.Expect(console.Spec.AuthProxy.TrustedCA.Name).To(Equal("console-auth-proxy-trusted-ca"))

		caCM := manifests.ConsoleAuthProxyTrustedCAConfigMap()
		g.Expect(guestClient.Get(ctx, client.ObjectKeyFromObject(caCM), caCM)).To(Succeed())
		g.Expect(caCM.Data).To(HaveKeyWithValue("ca-bundle.crt", "-----BEGIN CERTIFICATE-----\n..."))
		g.Expect(caCM.Labels).To(HaveKeyWithValue(hyperv1.InfraIDLabel, hcpInfraID))
	})

	t.Run("When the guest name is taken by an object this control plane did not create, it should refuse to overwrite it", func(t *testing.T) {
		g := NewWithT(t)
		ctx := t.Context()

		foreignCA := manifests.ConsoleAuthProxyTrustedCAConfigMap()
		foreignCA.Labels = map[string]string{hyperv1.InfraIDLabel: "another-infra-id"}
		foreignCA.Data = map[string]string{"ca-bundle.crt": "foreign bundle"}
		guestClient := fake.NewClientBuilder().WithScheme(api.Scheme).
			WithObjects(foreignCA).Build()
		cpClient := fake.NewClientBuilder().WithScheme(api.Scheme).
			WithObjects(newHCP(), newSourceCA()).Build()

		r := &reconciler{
			client:                 guestClient,
			uncachedClient:         guestClient,
			cpClient:               cpClient,
			CreateOrUpdateProvider: &simpleCreateOrUpdater{},
		}

		err := r.reconcileConsoleAuthProxy(ctx, newHCP())
		g.Expect(err).To(HaveOccurred())
		g.Expect(err.Error()).To(ContainSubstring("not owned by this control plane"))

		caCM := manifests.ConsoleAuthProxyTrustedCAConfigMap()
		g.Expect(guestClient.Get(ctx, client.ObjectKeyFromObject(caCM), caCM)).To(Succeed())
		g.Expect(caCM.Data).To(HaveKeyWithValue("ca-bundle.crt", "foreign bundle"))
	})

	t.Run("When the guest API server prunes spec.authProxy, it should report an error", func(t *testing.T) {
		g := NewWithT(t)
		ctx := t.Context()

		// Simulate a guest Console CRD that does not serve spec.authProxy:
		// every write loses the field, exactly like server-side field
		// pruning of an unknown field.
		pruningClient := fake.NewClientBuilder().WithScheme(api.Scheme).
			WithInterceptorFuncs(interceptor.Funcs{
				Create: func(ctx context.Context, c client.WithWatch, obj client.Object, opts ...client.CreateOption) error {
					if console, ok := obj.(*operatorv1.Console); ok {
						pruned := console.DeepCopy()
						pruned.Spec.AuthProxy = operatorv1.ConsoleAuthProxyConfig{}
						return c.Create(ctx, pruned, opts...)
					}
					return c.Create(ctx, obj, opts...)
				},
				Update: func(ctx context.Context, c client.WithWatch, obj client.Object, opts ...client.UpdateOption) error {
					if console, ok := obj.(*operatorv1.Console); ok {
						pruned := console.DeepCopy()
						pruned.Spec.AuthProxy = operatorv1.ConsoleAuthProxyConfig{}
						return c.Update(ctx, pruned, opts...)
					}
					return c.Update(ctx, obj, opts...)
				},
			}).Build()
		cpClient := fake.NewClientBuilder().WithScheme(api.Scheme).
			WithObjects(newHCP(), newSourceCA()).Build()

		r := &reconciler{
			client:                 pruningClient,
			uncachedClient:         pruningClient,
			cpClient:               cpClient,
			CreateOrUpdateProvider: &simpleCreateOrUpdater{},
		}

		err := r.reconcileConsoleAuthProxy(ctx, newHCP())
		g.Expect(err).To(HaveOccurred())
		g.Expect(err.Error()).To(ContainSubstring("spec.authProxy did not persist"))
	})

	t.Run("When the override is unset and the guest does not serve the Console kind, it should succeed", func(t *testing.T) {
		g := NewWithT(t)
		ctx := t.Context()

		// Simulate a guest whose Console CRD is not served, for example the
		// Console capability off: every Console read fails with a no-match
		// error. The unset path must treat it like an absent CR.
		noMatchClient := fake.NewClientBuilder().WithScheme(api.Scheme).
			WithInterceptorFuncs(interceptor.Funcs{
				Get: func(ctx context.Context, c client.WithWatch, key client.ObjectKey, obj client.Object, opts ...client.GetOption) error {
					if _, ok := obj.(*operatorv1.Console); ok {
						return &meta.NoKindMatchError{
							GroupKind:        operatorv1.GroupVersion.WithKind("Console").GroupKind(),
							SearchedVersions: []string{operatorv1.GroupVersion.Version},
						}
					}
					return c.Get(ctx, key, obj, opts...)
				},
			}).Build()
		cpClient := fake.NewClientBuilder().WithScheme(api.Scheme).
			WithObjects(newHCP()).Build()

		r := &reconciler{
			client:                 noMatchClient,
			uncachedClient:         noMatchClient,
			cpClient:               cpClient,
			CreateOrUpdateProvider: &simpleCreateOrUpdater{},
		}

		hcp := newHCP()
		hcp.Spec.OperatorConfiguration = nil
		g.Expect(r.reconcileConsoleAuthProxy(ctx, hcp)).To(Succeed())
	})

	t.Run("When the override is unset, it should clear the field and remove the CA bundle", func(t *testing.T) {
		g := NewWithT(t)
		ctx := t.Context()

		guestClient := fake.NewClientBuilder().WithScheme(api.Scheme).
			WithObjects(
				&operatorv1.Console{
					ObjectMeta: metav1.ObjectMeta{Name: "cluster"},
					Spec: operatorv1.ConsoleSpec{
						AuthProxy: operatorv1.ConsoleAuthProxyConfig{
							HTTPSProxy: "https://stale.example.com:8443",
						},
					},
				},
				newOwnedCA(),
			).Build()
		cpClient := fake.NewClientBuilder().WithScheme(api.Scheme).
			WithObjects(newHCP()).Build()

		r := &reconciler{
			client:                 guestClient,
			uncachedClient:         guestClient,
			cpClient:               cpClient,
			CreateOrUpdateProvider: &simpleCreateOrUpdater{},
		}

		hcp := newHCP()
		hcp.Spec.OperatorConfiguration.Authentication.Proxy = operatorv1.AuthenticationProxyConfig{}
		g.Expect(r.reconcileConsoleAuthProxy(ctx, hcp)).To(Succeed())

		console := manifests.ConsoleOperatorConfig()
		g.Expect(guestClient.Get(ctx, client.ObjectKeyFromObject(console), console)).To(Succeed())
		g.Expect(console.Spec.AuthProxy).To(Equal(operatorv1.ConsoleAuthProxyConfig{}))

		caCM := manifests.ConsoleAuthProxyTrustedCAConfigMap()
		err := guestClient.Get(ctx, client.ObjectKeyFromObject(caCM), caCM)
		g.Expect(err).To(HaveOccurred())
		g.Expect(err.Error()).To(ContainSubstring("not found"))
	})

	t.Run("When the override is unset and the CA bundle is not owned by this control plane, it should leave it in place", func(t *testing.T) {
		g := NewWithT(t)
		ctx := t.Context()

		foreignCA := manifests.ConsoleAuthProxyTrustedCAConfigMap()
		foreignCA.Data = map[string]string{"ca-bundle.crt": "foreign bundle"}
		guestClient := fake.NewClientBuilder().WithScheme(api.Scheme).
			WithObjects(foreignCA).Build()
		cpClient := fake.NewClientBuilder().WithScheme(api.Scheme).
			WithObjects(newHCP()).Build()

		r := &reconciler{
			client:                 guestClient,
			uncachedClient:         guestClient,
			cpClient:               cpClient,
			CreateOrUpdateProvider: &simpleCreateOrUpdater{},
		}

		hcp := newHCP()
		hcp.Spec.OperatorConfiguration.Authentication.Proxy = operatorv1.AuthenticationProxyConfig{}
		g.Expect(r.reconcileConsoleAuthProxy(ctx, hcp)).To(Succeed())

		caCM := manifests.ConsoleAuthProxyTrustedCAConfigMap()
		g.Expect(guestClient.Get(ctx, client.ObjectKeyFromObject(caCM), caCM)).To(Succeed())
		g.Expect(caCM.Data).To(HaveKeyWithValue("ca-bundle.crt", "foreign bundle"))
	})

	t.Run("When the override switches to system trust and the Console write fails, it should keep the CA bundle", func(t *testing.T) {
		g := NewWithT(t)
		ctx := t.Context()

		// Every Console update fails: the stored spec.authProxy keeps
		// referencing the bundle, so the bundle must survive the reconcile.
		failingClient := fake.NewClientBuilder().WithScheme(api.Scheme).
			WithObjects(
				&operatorv1.Console{
					ObjectMeta: metav1.ObjectMeta{Name: "cluster"},
					Spec: operatorv1.ConsoleSpec{
						AuthProxy: operatorv1.ConsoleAuthProxyConfig{
							HTTPSProxy: "https://stale.example.com:8443",
							TrustedCA:  operatorv1.ConsoleAuthProxyTrustedCAConfigMapReference{Name: "console-auth-proxy-trusted-ca"},
						},
					},
				},
				newOwnedCA(),
			).
			WithInterceptorFuncs(interceptor.Funcs{
				Update: func(ctx context.Context, c client.WithWatch, obj client.Object, opts ...client.UpdateOption) error {
					if _, ok := obj.(*operatorv1.Console); ok {
						return fmt.Errorf("injected console write failure")
					}
					return c.Update(ctx, obj, opts...)
				},
			}).Build()
		cpClient := fake.NewClientBuilder().WithScheme(api.Scheme).
			WithObjects(newHCP()).Build()

		r := &reconciler{
			client:                 failingClient,
			uncachedClient:         failingClient,
			cpClient:               cpClient,
			CreateOrUpdateProvider: &simpleCreateOrUpdater{},
		}

		hcp := newHCP()
		hcp.Spec.OperatorConfiguration.Authentication.Proxy = operatorv1.AuthenticationProxyConfig{HTTPSProxy: "https://auth.example.com:8443"}
		err := r.reconcileConsoleAuthProxy(ctx, hcp)
		g.Expect(err).To(HaveOccurred())
		g.Expect(err.Error()).To(ContainSubstring("injected console write failure"))

		// The bundle the stored spec.authProxy still references must survive.
		caCM := manifests.ConsoleAuthProxyTrustedCAConfigMap()
		g.Expect(failingClient.Get(ctx, client.ObjectKeyFromObject(caCM), caCM)).To(Succeed())
	})
}
