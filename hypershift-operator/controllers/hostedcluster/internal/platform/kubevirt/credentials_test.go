package kubevirt

import (
	"context"
	"errors"
	"strings"
	"testing"

	. "github.com/onsi/gomega"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	"github.com/openshift/hypershift/support/upsert"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"
)

const safeKubeconfig = `apiVersion: v1
kind: Config
current-context: infra
contexts:
- name: infra
  context: {cluster: infra, user: infra}
clusters:
- name: infra
  cluster: {server: "https://api.infra.cluster.test:6443"}
users:
- name: infra
  user: {token: private-test-token}
`

func TestReconcileCredentials(t *testing.T) {
	t.Parallel()
	t.Run("When no KubeVirt platform is configured, it should leave credentials absent", func(t *testing.T) {
		t.Parallel()
		g := NewWithT(t)
		c := fake.NewClientBuilder().Build()
		g.Expect(New(nil).ReconcileCredentials(t.Context(), c, upsert.New(false).CreateOrUpdate, &hyperv1.HostedCluster{}, "clusters-tenant")).To(Succeed())
		g.Expect(c.Get(t.Context(), client.ObjectKeyFromObject(credentialsSecret("clusters-tenant")), &corev1.Secret{})).To(Satisfy(apierrors.IsNotFound))
	})
	t.Run("When tenant credentials contain an exec plugin, it should reject them without publishing a Secret", func(t *testing.T) {
		t.Parallel()
		g := NewWithT(t)
		source := &corev1.Secret{
			ObjectMeta: metav1.ObjectMeta{Namespace: "clusters", Name: "infra-credentials"},
			Data: map[string][]byte{"kubeconfig": []byte(`apiVersion: v1
kind: Config
current-context: infra
contexts:
- name: infra
  context: {cluster: infra, user: infra}
clusters:
- name: infra
  cluster: {server: "https://api.infra.cluster.test:6443"}
users:
- name: infra
  user:
    exec:
      apiVersion: client.authentication.k8s.io/v1
      command: must-not-run
      interactiveMode: Never
`)},
		}
		c := fake.NewClientBuilder().WithObjects(source).Build()
		hc := &hyperv1.HostedCluster{
			ObjectMeta: metav1.ObjectMeta{Namespace: "clusters", Name: "tenant"},
			Spec: hyperv1.HostedClusterSpec{Platform: hyperv1.PlatformSpec{
				Type: hyperv1.KubevirtPlatform,
				Kubevirt: &hyperv1.KubevirtPlatformSpec{Credentials: &hyperv1.KubevirtPlatformCredentials{
					InfraKubeConfigSecret: &hyperv1.KubeconfigSecretRef{Name: source.Name, Key: "kubeconfig"},
				}},
			}},
		}
		err := New(nil).ReconcileCredentials(t.Context(), c, upsert.New(false).CreateOrUpdate, hc, "clusters-tenant")
		g.Expect(err).To(MatchError(ContainSubstring("exec")))
		g.Expect(c.Get(t.Context(), client.ObjectKeyFromObject(credentialsSecret("clusters-tenant")), &corev1.Secret{})).To(Satisfy(apierrors.IsNotFound))
		unchanged := &corev1.Secret{}
		g.Expect(c.Get(t.Context(), client.ObjectKeyFromObject(source), unchanged)).To(Succeed())
		g.Expect(unchanged.Data).To(Equal(source.Data))
	})
	for _, tc := range []struct {
		name                string
		key                 string
		data                map[string][]byte
		target              map[string][]byte
		want                map[string][]byte
		wantErr             string
		missingRef          bool
		missingName         bool
		missingKey          bool
		missingSrc          bool
		deleteDenied        bool
		writeDenied         bool
		replaceDuringDelete bool
	}{
		{
			name:   "When source credentials are valid, it should publish only consumer keys and replace stale target data",
			data:   map[string][]byte{"kubeconfig": []byte(safeKubeconfig), "namespace": []byte("worker-vms"), "unrelated": []byte("untrusted")},
			target: map[string][]byte{"kubeconfig": []byte("malformed"), "stale": []byte("untrusted")},
			want:   map[string][]byte{"kubeconfig": []byte(safeKubeconfig), "namespace": []byte("worker-vms")},
		},
		{
			name: "When a custom key is configured without a canonical key, it should publish validated aliases",
			key:  "custom", data: map[string][]byte{"custom": []byte(safeKubeconfig)},
			want: map[string][]byte{"custom": []byte(safeKubeconfig), "kubeconfig": []byte(safeKubeconfig)},
		},
		{
			name: "When a custom key and canonical key are present, it should validate and preserve both",
			key:  "custom", data: map[string][]byte{"custom": []byte(safeKubeconfig), "kubeconfig": []byte(safeKubeconfig)},
			want: map[string][]byte{"custom": []byte(safeKubeconfig), "kubeconfig": []byte(safeKubeconfig)},
		},
		{
			name: "When the canonical key is malformed despite a safe custom key, it should reject publication",
			key:  "custom", data: map[string][]byte{"custom": []byte(safeKubeconfig), "kubeconfig": []byte("malformed")}, wantErr: "kubeconfig",
		},
		{
			name: "When the selected key is missing, it should preserve an existing safe target",
			key:  "custom", data: map[string][]byte{"kubeconfig": []byte(safeKubeconfig)},
			target: map[string][]byte{"kubeconfig": []byte(safeKubeconfig)}, want: map[string][]byte{"kubeconfig": []byte(safeKubeconfig)}, wantErr: "custom",
		},
		{
			name:   "When a rejected source has safe historical credentials and namespace metadata, it should preserve the target",
			data:   map[string][]byte{"kubeconfig": []byte("malformed")},
			target: map[string][]byte{"kubeconfig": []byte(safeKubeconfig), "old-custom": []byte(safeKubeconfig), "namespace": []byte("worker-vms")},
			want:   map[string][]byte{"kubeconfig": []byte(safeKubeconfig), "old-custom": []byte(safeKubeconfig), "namespace": []byte("worker-vms")}, wantErr: "kubeconfig",
		},
		{
			name: "When rejected input replaces an unsafe target, it should delete the unsafe target",
			data: map[string][]byte{"kubeconfig": []byte("malformed")}, target: map[string][]byte{"kubeconfig": []byte("malformed")}, wantErr: "kubeconfig",
		},
		{
			name:       "When source credentials are missing, it should clean up an unsafe target",
			missingSrc: true, target: map[string][]byte{"kubeconfig": []byte("malformed")}, wantErr: "get secret",
		},
		{
			name: "When unsafe target cleanup is forbidden, it should return the cleanup failure",
			data: map[string][]byte{"kubeconfig": []byte("malformed")}, target: map[string][]byte{"kubeconfig": []byte("malformed")},
			want: map[string][]byte{"kubeconfig": []byte("malformed")}, wantErr: "cleanup forbidden", deleteDenied: true,
		},
		{
			name: "When target credentials become safe during cleanup, it should preserve the concurrent replacement",
			data: map[string][]byte{"kubeconfig": []byte("malformed")}, target: map[string][]byte{"kubeconfig": []byte("malformed")},
			want: map[string][]byte{"kubeconfig": []byte(safeKubeconfig)}, wantErr: "modified", replaceDuringDelete: true,
		},
		{
			name: "When credential publication fails, it should propagate the write error without altering the source",
			data: map[string][]byte{"kubeconfig": []byte(safeKubeconfig)}, writeDenied: true, wantErr: "write forbidden",
		},
		{
			name: "When the selected kubeconfig is empty, it should reject publication",
			data: map[string][]byte{"kubeconfig": {}}, wantErr: "empty",
		},
		{
			name: "When an invalid source has an empty target, it should remove the unusable target",
			data: map[string][]byte{"kubeconfig": []byte("malformed")}, target: map[string][]byte{}, wantErr: "kubeconfig",
		},
		{name: "When the credential reference is absent, it should reject without panic", missingRef: true, wantErr: "reference"},
		{name: "When the credential name is empty, it should reject without panic", missingName: true, wantErr: "name"},
		{name: "When the credential key is empty, it should reject without panic", missingKey: true, wantErr: "key"},
		{name: "When the credential key collides with namespace metadata, it should reject it", key: "namespace", data: map[string][]byte{"namespace": []byte(safeKubeconfig)}, wantErr: "namespace"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			g := NewWithT(t)
			key := tc.key
			if key == "" && !tc.missingKey {
				key = "kubeconfig"
			}
			source := &corev1.Secret{ObjectMeta: metav1.ObjectMeta{Namespace: "clusters", Name: "infra-credentials"}, Data: tc.data}
			hc := &hyperv1.HostedCluster{
				ObjectMeta: metav1.ObjectMeta{Namespace: "clusters", Name: "tenant"},
				Spec: hyperv1.HostedClusterSpec{Platform: hyperv1.PlatformSpec{
					Type: hyperv1.KubevirtPlatform,
					Kubevirt: &hyperv1.KubevirtPlatformSpec{Credentials: &hyperv1.KubevirtPlatformCredentials{
						InfraKubeConfigSecret: &hyperv1.KubeconfigSecretRef{Name: source.Name, Key: key},
					}},
				}},
			}
			if tc.missingRef {
				hc.Spec.Platform.Kubevirt.Credentials.InfraKubeConfigSecret = nil
			} else if tc.missingName {
				hc.Spec.Platform.Kubevirt.Credentials.InfraKubeConfigSecret.Name = ""
			}
			builder := fake.NewClientBuilder()
			if !tc.missingSrc {
				builder.WithObjects(source)
			}
			if tc.target != nil {
				target := credentialsSecret("clusters-tenant")
				target.Data = tc.target
				target.UID = "old-target"
				builder.WithObjects(target)
			}
			builder.WithInterceptorFuncs(interceptor.Funcs{Delete: func(ctx context.Context, c client.WithWatch, obj client.Object, opts ...client.DeleteOption) error {
				if tc.deleteDenied {
					return errors.New("cleanup forbidden")
				}
				options := (&client.DeleteOptions{}).ApplyOptions(opts)
				g.Expect(options.Preconditions).NotTo(BeNil())
				g.Expect(*options.Preconditions.UID).To(Equal(obj.GetUID()))
				g.Expect(*options.Preconditions.ResourceVersion).To(Equal(obj.GetResourceVersion()))
				if tc.replaceDuringDelete {
					fresh := &corev1.Secret{}
					g.Expect(c.Get(ctx, client.ObjectKeyFromObject(obj), fresh)).To(Succeed())
					fresh.Data = map[string][]byte{"kubeconfig": []byte(safeKubeconfig)}
					g.Expect(c.Update(ctx, fresh)).To(Succeed())
				}
				return c.Delete(ctx, obj, opts...)
			}, Create: func(ctx context.Context, c client.WithWatch, obj client.Object, opts ...client.CreateOption) error {
				if tc.writeDenied {
					return errors.New("write forbidden")
				}
				return c.Create(ctx, obj, opts...)
			}})
			c := builder.Build()
			err := New(nil).ReconcileCredentials(t.Context(), c, upsert.New(false).CreateOrUpdate, hc, "clusters-tenant")
			if tc.wantErr == "" {
				g.Expect(err).NotTo(HaveOccurred())
			} else {
				g.Expect(err).To(MatchError(ContainSubstring(tc.wantErr)))
				g.Expect(err.Error()).NotTo(ContainSubstring("private-test-token"))
			}
			target := &corev1.Secret{}
			err = c.Get(t.Context(), client.ObjectKeyFromObject(credentialsSecret("clusters-tenant")), target)
			if tc.want == nil {
				g.Expect(err).To(Satisfy(apierrors.IsNotFound))
			} else {
				g.Expect(err).NotTo(HaveOccurred())
				g.Expect(target.Data).To(Equal(tc.want))
				if tc.wantErr == "" {
					resourceVersion := target.ResourceVersion
					g.Expect(New(nil).ReconcileCredentials(t.Context(), c, upsert.New(false).CreateOrUpdate, hc, "clusters-tenant")).To(Succeed())
					g.Expect(c.Get(t.Context(), client.ObjectKeyFromObject(target), target)).To(Succeed())
					g.Expect(target.ResourceVersion).To(Equal(resourceVersion))
				}
			}
			if !tc.missingSrc {
				unchanged := &corev1.Secret{}
				g.Expect(c.Get(t.Context(), client.ObjectKeyFromObject(source), unchanged)).To(Succeed())
				g.Expect(unchanged.Data).To(Equal(source.Data))
				g.Expect(unchanged.Annotations).To(BeEmpty())
			}
		})
	}
}

func TestValidateCredentials(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name       string
		local      bool
		invalid    bool
		target     map[string][]byte
		inspectErr bool
	}{
		{name: "When a custom source is valid, it should return validated aliases without publishing credentials"},
		{name: "When external credentials are disabled, it should return no publication data", local: true},
		{name: "When a source is corrected but its target is unsafe, it should remove the old target before publication", target: map[string][]byte{"kubeconfig": []byte("malformed")}},
		{name: "When external credentials are removed, it should still remove an unsafe old target", local: true, target: map[string][]byte{"kubeconfig": []byte("malformed")}},
		{name: "When rejected input has a target without a stored canonical key, it should remove that unusable target", invalid: true, target: map[string][]byte{"custom": []byte(safeKubeconfig)}},
		{name: "When target inspection fails on rejected input, it should retain both errors and leave the target untouched", invalid: true, target: map[string][]byte{"kubeconfig": []byte("malformed")}, inspectErr: true},
		{name: "When valid current keys accompany an unsafe historical key, it should remove the target before publication", target: map[string][]byte{"kubeconfig": []byte(safeKubeconfig), "custom": []byte(safeKubeconfig), "old-custom": []byte(strings.ReplaceAll(safeKubeconfig, "user: {token: private-test-token}", "user: {auth-provider: {name: oidc}}"))}},
		{name: "When a rejected source has valid current target keys and an unparsable historical key, it should remove the target", invalid: true, target: map[string][]byte{"kubeconfig": []byte(safeKubeconfig), "custom": []byte(safeKubeconfig), "old-custom": []byte("malformed")}},
		{name: "When external credentials are removed but a historical key is unsafe, it should still remove the target", local: true, target: map[string][]byte{"kubeconfig": []byte(safeKubeconfig), "old-custom": []byte("malformed")}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			g := NewWithT(t)
			hc := &hyperv1.HostedCluster{ObjectMeta: metav1.ObjectMeta{Namespace: "clusters", Name: "tenant"}, Spec: hyperv1.HostedClusterSpec{Platform: hyperv1.PlatformSpec{
				Type:     hyperv1.KubevirtPlatform,
				Kubevirt: &hyperv1.KubevirtPlatformSpec{Credentials: &hyperv1.KubevirtPlatformCredentials{InfraKubeConfigSecret: &hyperv1.KubeconfigSecretRef{Name: "infra", Key: "custom"}}},
			}}}
			if tc.local {
				hc.Spec.Platform.Kubevirt.Credentials = nil
			}
			bytes := []byte(safeKubeconfig)
			if tc.invalid {
				bytes = []byte("malformed")
			}
			source := &corev1.Secret{ObjectMeta: metav1.ObjectMeta{Namespace: hc.Namespace, Name: "infra"}, Data: map[string][]byte{"custom": bytes}}
			builder := fake.NewClientBuilder().WithObjects(source)
			target := credentialsSecret("clusters-tenant")
			if tc.target != nil {
				target.Data = tc.target
				builder.WithObjects(target)
			}
			failedInspection := false
			c := builder.WithInterceptorFuncs(interceptor.Funcs{Get: func(ctx context.Context, c client.WithWatch, key client.ObjectKey, obj client.Object, opts ...client.GetOption) error {
				if tc.inspectErr && key == client.ObjectKeyFromObject(target) && !failedInspection {
					failedInspection = true
					return errors.New("target inspection denied")
				}
				return c.Get(ctx, key, obj, opts...)
			}}).Build()
			data, err := New(nil).ValidateCredentials(t.Context(), c, hc, "clusters-tenant")
			if tc.invalid {
				g.Expect(err).To(MatchError(ContainSubstring("parsed")))
				g.Expect(data).To(BeNil())
				if tc.inspectErr {
					g.Expect(err).To(MatchError(ContainSubstring("inspection denied")))
				}
			} else {
				g.Expect(err).NotTo(HaveOccurred())
				if tc.local {
					g.Expect(data).To(BeNil())
				} else {
					g.Expect(data).To(Equal(map[string][]byte{"custom": []byte(safeKubeconfig), "kubeconfig": []byte(safeKubeconfig)}))
				}
			}
			err = c.Get(t.Context(), client.ObjectKeyFromObject(target), target)
			if tc.inspectErr {
				g.Expect(err).NotTo(HaveOccurred())
				g.Expect(target.Data).To(Equal(tc.target))
			} else {
				g.Expect(err).To(Satisfy(apierrors.IsNotFound))
			}
			unchanged := &corev1.Secret{}
			g.Expect(c.Get(t.Context(), client.ObjectKeyFromObject(source), unchanged)).To(Succeed())
			g.Expect(unchanged.Data).To(Equal(source.Data))
			g.Expect(unchanged.Annotations).To(BeEmpty())
		})
	}
}

func TestDeleteCredentials(t *testing.T) {
	t.Parallel()
	t.Run("When unsafe credential cleanup is denied, it should propagate the failure and retain the target Secret", func(t *testing.T) {
		t.Parallel()
		g := NewWithT(t)
		target := credentialsSecret("clusters-tenant")
		target.Data = map[string][]byte{"kubeconfig": []byte("malformed")}
		cleanupErr := errors.New("credential cleanup denied")
		c := fake.NewClientBuilder().WithObjects(target).WithInterceptorFuncs(interceptor.Funcs{
			Delete: func(context.Context, client.WithWatch, client.Object, ...client.DeleteOption) error {
				return cleanupErr
			},
		}).Build()
		err := New(nil).DeleteCredentials(t.Context(), c, &hyperv1.HostedCluster{}, target.Namespace)
		g.Expect(errors.Is(err, cleanupErr)).To(BeTrue())
		stored := &corev1.Secret{}
		g.Expect(c.Get(t.Context(), client.ObjectKeyFromObject(target), stored)).To(Succeed())
		g.Expect(stored.Data).To(Equal(target.Data))
	})
	t.Run("When teardown has no source but a safe target, it should retain usable credentials without fetching the source", func(t *testing.T) {
		t.Parallel()
		g := NewWithT(t)
		target := credentialsSecret("clusters-tenant")
		target.Data = map[string][]byte{"kubeconfig": []byte(safeKubeconfig)}
		c := fake.NewClientBuilder().WithObjects(target).Build()
		g.Expect(New(nil).DeleteCredentials(t.Context(), c, &hyperv1.HostedCluster{}, target.Namespace)).To(Succeed())
		g.Expect(c.Get(t.Context(), client.ObjectKeyFromObject(target), target)).To(Succeed())
		g.Expect(target.Data).To(HaveKeyWithValue("kubeconfig", []byte(safeKubeconfig)))
	})
	t.Run("When a deleting cluster's source is corrected, it should restore only safe canonical credentials for live consumers", func(t *testing.T) {
		t.Parallel()
		g := NewWithT(t)
		hc := &hyperv1.HostedCluster{ObjectMeta: metav1.ObjectMeta{Namespace: "clusters", Name: "tenant"}, Spec: hyperv1.HostedClusterSpec{Platform: hyperv1.PlatformSpec{
			Type:     hyperv1.KubevirtPlatform,
			Kubevirt: &hyperv1.KubevirtPlatformSpec{Credentials: &hyperv1.KubevirtPlatformCredentials{InfraKubeConfigSecret: &hyperv1.KubeconfigSecretRef{Name: "infra", Key: "kubeconfig"}}},
		}}}
		source := &corev1.Secret{ObjectMeta: metav1.ObjectMeta{Namespace: hc.Namespace, Name: "infra"}, Data: map[string][]byte{"kubeconfig": []byte("malformed")}}
		target := credentialsSecret("clusters-tenant")
		target.Data = map[string][]byte{"kubeconfig": []byte("malformed")}
		namespace := &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: target.Namespace}}
		c := fake.NewClientBuilder().WithObjects(source, target, namespace).Build()
		p := New(nil)
		g.Expect(p.DeleteCredentials(t.Context(), c, hc, target.Namespace)).To(Succeed())
		g.Expect(c.Get(t.Context(), client.ObjectKeyFromObject(target), &corev1.Secret{})).To(Satisfy(apierrors.IsNotFound))
		g.Expect(c.Get(t.Context(), client.ObjectKeyFromObject(source), source)).To(Succeed())
		source.Data = map[string][]byte{"kubeconfig": []byte(safeKubeconfig), "unrelated": []byte("untrusted")}
		g.Expect(c.Update(t.Context(), source)).To(Succeed())
		g.Expect(p.DeleteCredentials(t.Context(), c, hc, target.Namespace)).To(Succeed())
		g.Expect(c.Get(t.Context(), client.ObjectKeyFromObject(target), target)).To(Succeed())
		g.Expect(target.Data).To(Equal(map[string][]byte{"kubeconfig": []byte(safeKubeconfig)}))
		g.Expect(c.Delete(t.Context(), namespace)).To(Succeed())
		g.Expect(c.Delete(t.Context(), target)).To(Succeed())
		g.Expect(p.DeleteCredentials(t.Context(), c, hc, target.Namespace)).To(Succeed())
		g.Expect(c.Get(t.Context(), client.ObjectKeyFromObject(target), &corev1.Secret{})).To(Satisfy(apierrors.IsNotFound))
	})
	t.Run("When the control plane namespace is terminating, it should not publish credentials or recreate resources", func(t *testing.T) {
		t.Parallel()
		g := NewWithT(t)
		hc := &hyperv1.HostedCluster{ObjectMeta: metav1.ObjectMeta{Namespace: "clusters", Name: "tenant"}, Spec: hyperv1.HostedClusterSpec{Platform: hyperv1.PlatformSpec{
			Type:     hyperv1.KubevirtPlatform,
			Kubevirt: &hyperv1.KubevirtPlatformSpec{Credentials: &hyperv1.KubevirtPlatformCredentials{InfraKubeConfigSecret: &hyperv1.KubeconfigSecretRef{Name: "infra", Key: "kubeconfig"}}},
		}}}
		source := &corev1.Secret{ObjectMeta: metav1.ObjectMeta{Namespace: hc.Namespace, Name: "infra"}, Data: map[string][]byte{"kubeconfig": []byte(safeKubeconfig)}}
		now := metav1.Now()
		ns := &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: "clusters-tenant", DeletionTimestamp: &now, Finalizers: []string{"retained-for-test"}}}
		writes := 0
		c := fake.NewClientBuilder().WithObjects(source, ns).WithInterceptorFuncs(interceptor.Funcs{
			Create: func(context.Context, client.WithWatch, client.Object, ...client.CreateOption) error {
				writes++
				return errors.New("unexpected create")
			},
			Update: func(context.Context, client.WithWatch, client.Object, ...client.UpdateOption) error {
				writes++
				return errors.New("unexpected update")
			},
		}).Build()
		g.Expect(New(nil).DeleteCredentials(t.Context(), c, hc, ns.Name)).To(Succeed())
		g.Expect(writes).To(BeZero())
		g.Expect(c.Get(t.Context(), client.ObjectKeyFromObject(credentialsSecret(ns.Name)), &corev1.Secret{})).To(Satisfy(apierrors.IsNotFound))
	})
}

func TestReconcileDeletionCredentials(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name              string
		mutate            func(*hyperv1.HostedCluster)
		missingSource     bool
		invalidSource     bool
		missingNamespace  bool
		terminating       bool
		denyWrite         bool
		denyInspection    bool
		wantCredentialErr string
		wantErr           string
		published         bool
	}{
		{name: "When teardown credentials are valid and the namespace is live, it should report successful publication", published: true},
		{name: "When external credentials are disabled, it should not publish or report rejection", mutate: func(hc *hyperv1.HostedCluster) { hc.Spec.Platform.Kubevirt.Credentials = nil }},
		{name: "When the credential reference is missing, it should report non-blocking rejection", mutate: func(hc *hyperv1.HostedCluster) { hc.Spec.Platform.Kubevirt.Credentials.InfraKubeConfigSecret = nil }, wantCredentialErr: "reference"},
		{name: "When the credential name is empty, it should report non-blocking rejection", mutate: func(hc *hyperv1.HostedCluster) { hc.Spec.Platform.Kubevirt.Credentials.InfraKubeConfigSecret.Name = "" }, wantCredentialErr: "name"},
		{name: "When the credential key is empty, it should report non-blocking rejection", mutate: func(hc *hyperv1.HostedCluster) { hc.Spec.Platform.Kubevirt.Credentials.InfraKubeConfigSecret.Key = "" }, wantCredentialErr: "key"},
		{name: "When the source Secret is absent, it should report non-blocking rejection", missingSource: true, wantCredentialErr: "source"},
		{name: "When the source is invalid, it should report non-blocking rejection without publication", invalidSource: true, wantCredentialErr: "parsed"},
		{name: "When the namespace is absent, it should skip publication without claiming success", missingNamespace: true},
		{name: "When the namespace is terminating, it should skip publication without claiming success", terminating: true},
		{name: "When target publication fails, it should propagate the failure without claiming success", denyWrite: true, wantErr: "publication denied"},
		{name: "When target inspection fails, it should propagate the failure without claiming success", denyInspection: true, wantErr: "inspection denied"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			g := NewWithT(t)
			hc := &hyperv1.HostedCluster{ObjectMeta: metav1.ObjectMeta{Namespace: "clusters", Name: "tenant"}, Spec: hyperv1.HostedClusterSpec{Platform: hyperv1.PlatformSpec{
				Type: hyperv1.KubevirtPlatform, Kubevirt: &hyperv1.KubevirtPlatformSpec{Credentials: &hyperv1.KubevirtPlatformCredentials{InfraKubeConfigSecret: &hyperv1.KubeconfigSecretRef{Name: "infra", Key: "kubeconfig"}}},
			}}}
			if tc.mutate != nil {
				tc.mutate(hc)
			}
			source := &corev1.Secret{ObjectMeta: metav1.ObjectMeta{Namespace: hc.Namespace, Name: "infra"}, Data: map[string][]byte{"kubeconfig": []byte(safeKubeconfig), "extra": []byte("untrusted")}}
			if tc.invalidSource {
				source.Data["kubeconfig"] = []byte("users: [private-test-token")
			}
			target := credentialsSecret("clusters-tenant")
			ns := &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: target.Namespace}}
			if tc.terminating {
				now := metav1.Now()
				ns.DeletionTimestamp = &now
				ns.Finalizers = []string{"retained-for-test"}
			}
			builder := fake.NewClientBuilder()
			if !tc.missingSource {
				builder.WithObjects(source)
			}
			if !tc.missingNamespace {
				builder.WithObjects(ns)
			}
			c := builder.WithInterceptorFuncs(interceptor.Funcs{
				Get: func(ctx context.Context, c client.WithWatch, key client.ObjectKey, obj client.Object, opts ...client.GetOption) error {
					if tc.denyInspection && key == client.ObjectKeyFromObject(target) {
						return errors.New("inspection denied")
					}
					return c.Get(ctx, key, obj, opts...)
				},
				Create: func(ctx context.Context, c client.WithWatch, obj client.Object, opts ...client.CreateOption) error {
					if tc.denyWrite {
						return errors.New("publication denied")
					}
					return c.Create(ctx, obj, opts...)
				},
			}).Build()
			result, err := New(nil).ReconcileDeletionCredentials(t.Context(), c, hc, ns.Name)
			if tc.wantErr == "" {
				g.Expect(err).NotTo(HaveOccurred())
			} else {
				g.Expect(err).To(MatchError(ContainSubstring(tc.wantErr)))
			}
			if tc.wantCredentialErr == "" {
				g.Expect(result.CredentialError).NotTo(HaveOccurred())
			} else {
				g.Expect(result.CredentialError).To(MatchError(ContainSubstring(tc.wantCredentialErr)))
				g.Expect(result.CredentialError.Error()).NotTo(ContainSubstring("private-test-token"))
			}
			g.Expect(result.Published).To(Equal(tc.published))
			if !tc.denyInspection {
				err = c.Get(t.Context(), client.ObjectKeyFromObject(target), target)
				if tc.published {
					g.Expect(err).NotTo(HaveOccurred())
					g.Expect(target.Data).To(Equal(map[string][]byte{"kubeconfig": []byte(safeKubeconfig)}))
				} else {
					g.Expect(err).To(Satisfy(apierrors.IsNotFound))
				}
			}
			if !tc.missingSource {
				unchanged := &corev1.Secret{}
				g.Expect(c.Get(t.Context(), client.ObjectKeyFromObject(source), unchanged)).To(Succeed())
				g.Expect(unchanged.Data).To(Equal(source.Data))
			}
		})
	}
}
