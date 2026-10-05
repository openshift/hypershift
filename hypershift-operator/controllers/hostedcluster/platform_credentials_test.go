package hostedcluster

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	. "github.com/onsi/gomega"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	platformkubevirt "github.com/openshift/hypershift/hypershift-operator/controllers/hostedcluster/internal/platform/kubevirt"
	"github.com/openshift/hypershift/pkg/manifests"
	"github.com/openshift/hypershift/support/api"
	"github.com/openshift/hypershift/support/upsert"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"

	"github.com/go-logr/logr"
)

const externalInfraKubeconfig = `apiVersion: v1
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

func credentialHostedCluster() *hyperv1.HostedCluster {
	return &hyperv1.HostedCluster{
		ObjectMeta: metav1.ObjectMeta{Namespace: "clusters", Name: "tenant", Generation: 4},
		Spec: hyperv1.HostedClusterSpec{Platform: hyperv1.PlatformSpec{
			Type: hyperv1.KubevirtPlatform,
			Kubevirt: &hyperv1.KubevirtPlatformSpec{Credentials: &hyperv1.KubevirtPlatformCredentials{
				InfraKubeConfigSecret: &hyperv1.KubeconfigSecretRef{Name: "infra", Key: "kubeconfig"},
			}},
		}},
	}
}

func TestReconcile(t *testing.T) {
	t.Run("When Managed HSM is unsupported across reconciles, it should never persist configuration success before rejecting it", func(t *testing.T) {
		testManagedHSMBeforeInitialStatus(t, false)
	})
	t.Run("When the Managed HSM release is unavailable, it should leave validation incomplete and block reconciliation", func(t *testing.T) {
		testManagedHSMBeforeInitialStatus(t, true)
	})
	for _, credentialPatch := range []bool{false, true} {
		name := "When the reconciliation condition retries after a concurrent status write, it should not replay pending status"
		if credentialPatch {
			name = "When the credential condition retries after a concurrent status write, it should not replay pending status"
		}
		t.Run(name, func(t *testing.T) {
			g := NewWithT(t)
			hc := credentialHostedCluster()
			hc.Spec.Platform = hyperv1.PlatformSpec{Type: hyperv1.NonePlatform}
			patches := 0
			c := fake.NewClientBuilder().WithScheme(api.Scheme).WithStatusSubresource(hc).WithObjects(hc).WithInterceptorFuncs(interceptor.Funcs{
				SubResourcePatch: func(ctx context.Context, c client.Client, subresource string, obj client.Object, patch client.Patch, opts ...client.SubResourcePatchOption) error {
					patches++
					if patches == 1 {
						fresh := &hyperv1.HostedCluster{}
						g.Expect(c.Get(ctx, client.ObjectKeyFromObject(hc), fresh)).To(Succeed())
						fresh.Status.CustomKubeconfig = &corev1.LocalObjectReference{Name: "concurrent-kubeconfig"}
						meta.SetStatusCondition(&fresh.Status.Conditions, metav1.Condition{Type: string(hyperv1.ValidHostedClusterConfiguration), Status: metav1.ConditionTrue, Reason: "Concurrent"})
						g.Expect(c.Status().Update(ctx, fresh)).To(Succeed())
						return apierrors.NewConflict(hyperv1.SchemeGroupVersion.WithResource("hostedclusters").GroupResource(), hc.Name, errors.New("concurrent status"))
					}
					return c.SubResource(subresource).Patch(ctx, obj, patch, opts...)
				},
			}).Build()
			r := &HostedClusterReconciler{Client: c, now: metav1.Now}
			var active *hyperv1.HostedCluster
			r.overwriteReconcile = func(ctx context.Context, _ ctrl.Request, _ logr.Logger, snapshot *hyperv1.HostedCluster) (ctrl.Result, error) {
				active = snapshot
				snapshot.Status.CustomKubeconfig = &corev1.LocalObjectReference{Name: "pending-kubeconfig"}
				meta.SetStatusCondition(&snapshot.Status.Conditions, metav1.Condition{Type: string(hyperv1.ValidHostedClusterConfiguration), Status: metav1.ConditionFalse, Reason: "Pending"})
				if credentialPatch {
					return ctrl.Result{}, r.patchPlatformCredentialsCondition(ctx, snapshot, nil)
				}
				return ctrl.Result{}, nil
			}
			_, err := r.Reconcile(t.Context(), ctrl.Request{NamespacedName: client.ObjectKeyFromObject(hc)})
			g.Expect(err).NotTo(HaveOccurred())
			g.Expect(c.Get(t.Context(), client.ObjectKeyFromObject(hc), hc)).To(Succeed())
			g.Expect(hc.Status.CustomKubeconfig).To(Equal(&corev1.LocalObjectReference{Name: "concurrent-kubeconfig"}))
			g.Expect(meta.FindStatusCondition(hc.Status.Conditions, string(hyperv1.ValidHostedClusterConfiguration)).Reason).To(Equal("Concurrent"))
			g.Expect(active.Status.CustomKubeconfig).To(Equal(&corev1.LocalObjectReference{Name: "pending-kubeconfig"}))
			g.Expect(meta.FindStatusCondition(active.Status.Conditions, string(hyperv1.ValidHostedClusterConfiguration)).Reason).To(Equal("Pending"))
			g.Expect(patches).To(BeNumerically(">=", 2))
		})
	}
	t.Run("When reconciliation updates its own spec generation, it should requeue without reporting a stale status-patch error", func(t *testing.T) {
		g := NewWithT(t)
		hc := credentialHostedCluster()
		hc.Spec.Platform = hyperv1.PlatformSpec{Type: hyperv1.NonePlatform}
		meta.SetStatusCondition(&hc.Status.Conditions, metav1.Condition{Type: string(hyperv1.ReconciliationSucceeded), Status: metav1.ConditionTrue, Reason: "Previous", ObservedGeneration: hc.Generation})
		c := fake.NewClientBuilder().WithScheme(api.Scheme).WithStatusSubresource(hc).WithObjects(hc).Build()
		r := &HostedClusterReconciler{Client: c, now: metav1.Now}
		r.overwriteReconcile = func(ctx context.Context, _ ctrl.Request, _ logr.Logger, snapshot *hyperv1.HostedCluster) (ctrl.Result, error) {
			snapshot.Spec.InfraID = "defaulted-infra"
			// The fake client does not increment generation like the API server.
			snapshot.Generation++
			return ctrl.Result{}, c.Update(ctx, snapshot)
		}
		result, err := r.Reconcile(t.Context(), ctrl.Request{NamespacedName: client.ObjectKeyFromObject(hc)})
		g.Expect(err).NotTo(HaveOccurred())
		g.Expect(result.RequeueAfter).To(Equal(time.Second))
		g.Expect(c.Get(t.Context(), client.ObjectKeyFromObject(hc), hc)).To(Succeed())
		g.Expect(hc.Generation).To(Equal(int64(5)))
		g.Expect(meta.FindStatusCondition(hc.Status.Conditions, string(hyperv1.ReconciliationSucceeded)).ObservedGeneration).To(Equal(int64(4)))
	})
	t.Run("When another writer changes the spec during successful reconciliation, it should still reject stale status", func(t *testing.T) {
		g := NewWithT(t)
		hc := credentialHostedCluster()
		hc.Spec.Platform = hyperv1.PlatformSpec{Type: hyperv1.NonePlatform}
		c := fake.NewClientBuilder().WithScheme(api.Scheme).WithStatusSubresource(hc).WithObjects(hc).Build()
		r := &HostedClusterReconciler{Client: c, now: metav1.Now,
			overwriteReconcile: func(ctx context.Context, _ ctrl.Request, _ logr.Logger, snapshot *hyperv1.HostedCluster) (ctrl.Result, error) {
				fresh := snapshot.DeepCopy()
				fresh.Spec.InfraID = "concurrent-infra"
				fresh.Generation++
				return ctrl.Result{}, c.Update(ctx, fresh)
			},
		}
		_, err := r.Reconcile(t.Context(), ctrl.Request{NamespacedName: client.ObjectKeyFromObject(hc)})
		g.Expect(err).To(MatchError(ContainSubstring("HostedCluster changed during reconciliation")))
		g.Expect(c.Get(t.Context(), client.ObjectKeyFromObject(hc), hc)).To(Succeed())
		g.Expect(meta.FindStatusCondition(hc.Status.Conditions, string(hyperv1.ReconciliationSucceeded))).To(BeNil())
	})
	t.Run("When successful finalizer removal deletes the cluster, it should complete without a NotFound error or requeue", func(t *testing.T) {
		g := NewWithT(t)
		hc := credentialHostedCluster()
		hc.Spec.Platform = hyperv1.PlatformSpec{Type: hyperv1.NonePlatform}
		now := metav1.Now()
		hc.DeletionTimestamp = &now
		hc.Finalizers = []string{HostedClusterFinalizer}
		c := fake.NewClientBuilder().WithScheme(api.Scheme).WithStatusSubresource(hc).WithObjects(hc).Build()
		r := &HostedClusterReconciler{Client: c, now: metav1.Now,
			overwriteReconcile: func(ctx context.Context, _ ctrl.Request, _ logr.Logger, snapshot *hyperv1.HostedCluster) (ctrl.Result, error) {
				snapshot.Finalizers = nil
				return ctrl.Result{}, c.Update(ctx, snapshot)
			},
		}
		result, err := r.Reconcile(t.Context(), ctrl.Request{NamespacedName: client.ObjectKeyFromObject(hc)})
		g.Expect(err).NotTo(HaveOccurred())
		g.Expect(result).To(Equal(ctrl.Result{}))
		g.Expect(c.Get(t.Context(), client.ObjectKeyFromObject(hc), &hyperv1.HostedCluster{})).To(Satisfy(apierrors.IsNotFound))
	})
	t.Run("When a status fetch fails for an unrelated reason, it should still report that error", func(t *testing.T) {
		g := NewWithT(t)
		hc := credentialHostedCluster()
		hc.Spec.Platform = hyperv1.PlatformSpec{Type: hyperv1.NonePlatform}
		reads := 0
		c := fake.NewClientBuilder().WithScheme(api.Scheme).WithStatusSubresource(hc).WithObjects(hc).WithInterceptorFuncs(interceptor.Funcs{
			Get: func(ctx context.Context, c client.WithWatch, key client.ObjectKey, obj client.Object, opts ...client.GetOption) error {
				if _, ok := obj.(*hyperv1.HostedCluster); ok {
					reads++
					if reads > 1 {
						return errors.New("status fetch denied")
					}
				}
				return c.Get(ctx, key, obj, opts...)
			},
		}).Build()
		r := &HostedClusterReconciler{Client: c, now: metav1.Now,
			overwriteReconcile: func(context.Context, ctrl.Request, logr.Logger, *hyperv1.HostedCluster) (ctrl.Result, error) {
				return ctrl.Result{}, nil
			},
		}
		_, err := r.Reconcile(t.Context(), ctrl.Request{NamespacedName: client.ObjectKeyFromObject(hc)})
		g.Expect(err).To(MatchError(ContainSubstring("status fetch denied")))
	})
	t.Run("When the cluster disappears after a reconciliation failure, it should preserve the original error", func(t *testing.T) {
		g := NewWithT(t)
		hc := credentialHostedCluster()
		hc.Spec.Platform = hyperv1.PlatformSpec{Type: hyperv1.NonePlatform}
		c := fake.NewClientBuilder().WithScheme(api.Scheme).WithStatusSubresource(hc).WithObjects(hc).Build()
		r := &HostedClusterReconciler{Client: c, now: metav1.Now,
			overwriteReconcile: func(ctx context.Context, _ ctrl.Request, _ logr.Logger, snapshot *hyperv1.HostedCluster) (ctrl.Result, error) {
				g.Expect(c.Delete(ctx, snapshot)).To(Succeed())
				return ctrl.Result{}, errors.New("original reconciliation failure")
			},
		}
		_, err := r.Reconcile(t.Context(), ctrl.Request{NamespacedName: client.ObjectKeyFromObject(hc)})
		g.Expect(err).To(MatchError("original reconciliation failure"))
	})
	t.Run("When external credentials are invalid, it should clean up unsafe targets and report rejection before any reconciliation callback", func(t *testing.T) {
		g := NewWithT(t)
		hc := credentialHostedCluster()
		source := &corev1.Secret{ObjectMeta: metav1.ObjectMeta{Namespace: hc.Namespace, Name: "infra"}, Data: map[string][]byte{"kubeconfig": []byte("malformed")}}
		target := &corev1.Secret{ObjectMeta: metav1.ObjectMeta{Namespace: manifests.HostedControlPlaneNamespace(hc.Namespace, hc.Name), Name: hyperv1.KubeVirtInfraCredentialsSecretName}, Data: map[string][]byte{"kubeconfig": []byte("malformed")}}
		c := fake.NewClientBuilder().WithScheme(api.Scheme).WithStatusSubresource(hc).WithObjects(hc, source, target).Build()
		called := false
		r := &HostedClusterReconciler{
			Client: c, now: metav1.Now,
			overwriteReconcile: func(context.Context, ctrl.Request, logr.Logger, *hyperv1.HostedCluster) (ctrl.Result, error) {
				called = true
				return ctrl.Result{}, nil
			},
		}
		_, err := r.Reconcile(t.Context(), ctrl.Request{NamespacedName: client.ObjectKeyFromObject(hc)})
		g.Expect(err).To(MatchError(ContainSubstring("kubeconfig")))
		g.Expect(called).To(BeFalse())
		g.Expect(c.Get(t.Context(), client.ObjectKeyFromObject(target), &corev1.Secret{})).To(Satisfy(apierrors.IsNotFound))
		g.Expect(c.Get(t.Context(), client.ObjectKeyFromObject(hc), hc)).To(Succeed())
		condition := meta.FindStatusCondition(hc.Status.Conditions, string(hyperv1.PlatformCredentialsFound))
		g.Expect(condition).NotTo(BeNil())
		g.Expect(condition.Status).To(Equal(metav1.ConditionFalse))
		g.Expect(condition.ObservedGeneration).To(Equal(int64(4)))
		g.Expect(condition.Message).NotTo(ContainSubstring("private-test-token"))
	})
	t.Run("When ingress status refresh imports a sizing condition, it should preserve the sizing controller's later update", func(t *testing.T) {
		g := NewWithT(t)
		hc := credentialHostedCluster()
		hc.Spec.Platform = hyperv1.PlatformSpec{Type: hyperv1.NonePlatform}
		meta.SetStatusCondition(&hc.Status.Conditions, metav1.Condition{Type: string(hyperv1.IngressDefaultCertificateSynced), Status: metav1.ConditionTrue, Reason: "Old"})
		c := fake.NewClientBuilder().WithScheme(api.Scheme).WithStatusSubresource(hc).WithObjects(hc).Build()
		r := &HostedClusterReconciler{Client: c, now: metav1.Now}
		r.overwriteReconcile = func(ctx context.Context, _ ctrl.Request, _ logr.Logger, snapshot *hyperv1.HostedCluster) (ctrl.Result, error) {
			fresh := &hyperv1.HostedCluster{}
			g.Expect(c.Get(ctx, client.ObjectKeyFromObject(hc), fresh)).To(Succeed())
			meta.SetStatusCondition(&fresh.Status.Conditions, metav1.Condition{Type: hyperv1.ClusterSizeComputed, Status: metav1.ConditionTrue, Reason: "First"})
			g.Expect(c.Status().Update(ctx, fresh)).To(Succeed())
			g.Expect(r.reconcileIngressDefaultCertSync(ctx, snapshot, upsert.New(false).CreateOrUpdate, manifests.HostedControlPlaneNamespace(hc.Namespace, hc.Name))).To(Succeed())
			g.Expect(c.Get(ctx, client.ObjectKeyFromObject(hc), fresh)).To(Succeed())
			meta.SetStatusCondition(&fresh.Status.Conditions, metav1.Condition{Type: hyperv1.ClusterSizeComputed, Status: metav1.ConditionFalse, Reason: "Latest"})
			g.Expect(c.Status().Update(ctx, fresh)).To(Succeed())
			return ctrl.Result{}, nil
		}
		_, err := r.Reconcile(t.Context(), ctrl.Request{NamespacedName: client.ObjectKeyFromObject(hc)})
		g.Expect(err).NotTo(HaveOccurred())
		g.Expect(c.Get(t.Context(), client.ObjectKeyFromObject(hc), hc)).To(Succeed())
		g.Expect(meta.FindStatusCondition(hc.Status.Conditions, hyperv1.ClusterSizeComputed).Reason).To(Equal("Latest"))
		g.Expect(meta.FindStatusCondition(hc.Status.Conditions, string(hyperv1.IngressDefaultCertificateSynced))).To(BeNil())
	})
	t.Run("When a corrected source has an unsafe target before a shared prerequisite blocks reconciliation, it should remove the unsafe target", func(t *testing.T) {
		g := NewWithT(t)
		hc := credentialHostedCluster()
		source := &corev1.Secret{ObjectMeta: metav1.ObjectMeta{Namespace: hc.Namespace, Name: "infra"}, Data: map[string][]byte{"kubeconfig": []byte(externalInfraKubeconfig)}}
		unsafe := strings.ReplaceAll(externalInfraKubeconfig, "user: {token: private-test-token}", "user: {auth-provider: {name: oidc}}")
		target := &corev1.Secret{ObjectMeta: metav1.ObjectMeta{Namespace: manifests.HostedControlPlaneNamespace(hc.Namespace, hc.Name), Name: hyperv1.KubeVirtInfraCredentialsSecretName}, Data: map[string][]byte{"kubeconfig": []byte(unsafe)}}
		c := fake.NewClientBuilder().WithScheme(api.Scheme).WithStatusSubresource(hc).WithObjects(hc, source, target).Build()
		r := &HostedClusterReconciler{Client: c, now: metav1.Now,
			overwriteReconcile: func(context.Context, ctrl.Request, logr.Logger, *hyperv1.HostedCluster) (ctrl.Result, error) {
				return ctrl.Result{}, errors.New("prerequisite blocked")
			},
		}
		_, err := r.Reconcile(t.Context(), ctrl.Request{NamespacedName: client.ObjectKeyFromObject(hc)})
		g.Expect(err).To(MatchError(ContainSubstring("prerequisite blocked")))
		g.Expect(c.Get(t.Context(), client.ObjectKeyFromObject(target), &corev1.Secret{})).To(Satisfy(apierrors.IsNotFound))
	})
	for _, tc := range []struct {
		name string
		safe bool
		deny bool
	}{
		{name: "When a deleting cluster has an unsafe target and no source, it should clean the target and allow non-consuming teardown"},
		{name: "When a deleting cluster has a safe target and no source, it should retain it and allow non-consuming teardown", safe: true},
		{name: "When unsafe target cleanup fails during deletion, it should report the cleanup failure", deny: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			g := NewWithT(t)
			hc := credentialHostedCluster()
			hc.DeletionTimestamp = &metav1.Time{Time: metav1.Now().Time}
			hc.Finalizers = []string{HostedClusterFinalizer}
			data := []byte("malformed")
			if tc.safe {
				data = []byte(externalInfraKubeconfig)
			}
			target := &corev1.Secret{ObjectMeta: metav1.ObjectMeta{Namespace: manifests.HostedControlPlaneNamespace(hc.Namespace, hc.Name), Name: hyperv1.KubeVirtInfraCredentialsSecretName}, Data: map[string][]byte{"kubeconfig": data}}
			c := fake.NewClientBuilder().WithScheme(api.Scheme).WithStatusSubresource(hc).WithObjects(hc, target).
				WithInterceptorFuncs(interceptor.Funcs{Delete: func(ctx context.Context, c client.WithWatch, obj client.Object, opts ...client.DeleteOption) error {
					if tc.deny {
						return errors.New("target cleanup denied")
					}
					return c.Delete(ctx, obj, opts...)
				}}).Build()
			called := false
			r := &HostedClusterReconciler{Client: c, now: metav1.Now,
				overwriteReconcile: func(context.Context, ctrl.Request, logr.Logger, *hyperv1.HostedCluster) (ctrl.Result, error) {
					called = true
					return ctrl.Result{}, nil
				},
			}
			_, err := r.Reconcile(t.Context(), ctrl.Request{NamespacedName: client.ObjectKeyFromObject(hc)})
			if tc.deny {
				g.Expect(err).To(MatchError(ContainSubstring("cleanup denied")))
				g.Expect(called).To(BeFalse())
			} else {
				g.Expect(err).NotTo(HaveOccurred())
				g.Expect(called).To(BeTrue())
			}
			err = c.Get(t.Context(), client.ObjectKeyFromObject(target), target)
			if tc.safe || tc.deny {
				g.Expect(err).NotTo(HaveOccurred())
				g.Expect(target.Data).To(HaveKeyWithValue("kubeconfig", data))
			} else {
				g.Expect(err).To(Satisfy(apierrors.IsNotFound))
			}
		})
	}
	t.Run("When credentials are rejected for an older generation, it should not relabel the error or replace the active spec snapshot", func(t *testing.T) {
		g := NewWithT(t)
		hc := credentialHostedCluster()
		hc.Spec.Platform = hyperv1.PlatformSpec{Type: hyperv1.NonePlatform}
		c := fake.NewClientBuilder().WithScheme(api.Scheme).WithStatusSubresource(hc).WithObjects(hc).Build()
		r := &HostedClusterReconciler{Client: c, now: metav1.Now}
		r.overwriteReconcile = func(ctx context.Context, _ ctrl.Request, _ logr.Logger, snapshot *hyperv1.HostedCluster) (ctrl.Result, error) {
			fresh := &hyperv1.HostedCluster{}
			g.Expect(c.Get(ctx, client.ObjectKeyFromObject(hc), fresh)).To(Succeed())
			fresh.Generation++
			fresh.Spec.Platform.Type = hyperv1.AzurePlatform
			g.Expect(c.Update(ctx, fresh)).To(Succeed())
			err := r.patchPlatformCredentialsCondition(ctx, snapshot, errors.New("generation-four rejection"))
			g.Expect(snapshot.Generation).To(Equal(int64(4)))
			g.Expect(snapshot.Spec.Platform.Type).To(Equal(hyperv1.NonePlatform))
			return ctrl.Result{}, err
		}
		_, err := r.Reconcile(t.Context(), ctrl.Request{NamespacedName: client.ObjectKeyFromObject(hc)})
		g.Expect(err).To(MatchError(ContainSubstring("changed")))
		g.Expect(c.Get(t.Context(), client.ObjectKeyFromObject(hc), hc)).To(Succeed())
		g.Expect(meta.FindStatusCondition(hc.Status.Conditions, string(hyperv1.PlatformCredentialsFound))).To(BeNil())
		g.Expect(meta.FindStatusCondition(hc.Status.Conditions, string(hyperv1.ReconciliationSucceeded))).To(BeNil())
	})
	t.Run("When a credential patch observes concurrent status, it should not replay that imported condition over a later update", func(t *testing.T) {
		g := NewWithT(t)
		hc := credentialHostedCluster()
		hc.Spec.Platform = hyperv1.PlatformSpec{Type: hyperv1.NonePlatform}
		c := fake.NewClientBuilder().WithScheme(api.Scheme).WithStatusSubresource(hc).WithObjects(hc).Build()
		r := &HostedClusterReconciler{Client: c, now: metav1.Now}
		r.overwriteReconcile = func(ctx context.Context, _ ctrl.Request, _ logr.Logger, snapshot *hyperv1.HostedCluster) (ctrl.Result, error) {
			fresh := &hyperv1.HostedCluster{}
			g.Expect(c.Get(ctx, client.ObjectKeyFromObject(hc), fresh)).To(Succeed())
			meta.SetStatusCondition(&fresh.Status.Conditions, metav1.Condition{Type: "ConcurrentCondition", Status: metav1.ConditionTrue, Reason: "First"})
			g.Expect(c.Status().Update(ctx, fresh)).To(Succeed())
			g.Expect(r.patchPlatformCredentialsCondition(ctx, snapshot, nil)).To(Succeed())
			g.Expect(c.Get(ctx, client.ObjectKeyFromObject(hc), fresh)).To(Succeed())
			meta.SetStatusCondition(&fresh.Status.Conditions, metav1.Condition{Type: "ConcurrentCondition", Status: metav1.ConditionFalse, Reason: "Latest"})
			g.Expect(c.Status().Update(ctx, fresh)).To(Succeed())
			return ctrl.Result{}, nil
		}
		_, err := r.Reconcile(t.Context(), ctrl.Request{NamespacedName: client.ObjectKeyFromObject(hc)})
		g.Expect(err).NotTo(HaveOccurred())
		g.Expect(c.Get(t.Context(), client.ObjectKeyFromObject(hc), hc)).To(Succeed())
		g.Expect(meta.FindStatusCondition(hc.Status.Conditions, "ConcurrentCondition").Reason).To(Equal("Latest"))
	})
}

func TestReconcilePlatformCredentialsWithStatus(t *testing.T) {
	t.Run("When rejected credentials are corrected, it should publish credentials and recover status while preserving concurrent conditions", func(t *testing.T) {
		g := NewWithT(t)
		hc := credentialHostedCluster()
		source := &corev1.Secret{ObjectMeta: metav1.ObjectMeta{Namespace: hc.Namespace, Name: "infra"}, Data: map[string][]byte{"kubeconfig": []byte("malformed")}}
		patches := 0
		c := fake.NewClientBuilder().WithScheme(api.Scheme).WithStatusSubresource(hc).WithObjects(hc, source).
			WithInterceptorFuncs(interceptor.Funcs{SubResourcePatch: func(ctx context.Context, c client.Client, subresource string, obj client.Object, patch client.Patch, opts ...client.SubResourcePatchOption) error {
				patches++
				if patches == 1 {
					fresh := &hyperv1.HostedCluster{}
					g.Expect(c.Get(ctx, client.ObjectKeyFromObject(hc), fresh)).To(Succeed())
					meta.SetStatusCondition(&fresh.Status.Conditions, metav1.Condition{Type: "ConcurrentCondition", Status: metav1.ConditionTrue, Reason: "Preserved"})
					g.Expect(c.Status().Update(ctx, fresh)).To(Succeed())
					return apierrors.NewConflict(hyperv1.SchemeGroupVersion.WithResource("hostedclusters").GroupResource(), hc.Name, errors.New("concurrent update"))
				}
				return c.SubResource(subresource).Patch(ctx, obj, patch, opts...)
			}}).Build()
		r := &HostedClusterReconciler{Client: c}
		namespace := manifests.HostedControlPlaneNamespace(hc.Namespace, hc.Name)
		p := platformkubevirt.New(nil)
		g.Expect(r.reconcilePlatformCredentialsWithStatus(t.Context(), hc, upsert.New(false).CreateOrUpdate, namespace, p)).To(HaveOccurred())
		g.Expect(c.Get(t.Context(), client.ObjectKeyFromObject(hc), hc)).To(Succeed())
		g.Expect(meta.IsStatusConditionFalse(hc.Status.Conditions, string(hyperv1.PlatformCredentialsFound))).To(BeTrue())
		g.Expect(c.Get(t.Context(), client.ObjectKeyFromObject(source), source)).To(Succeed())
		source.Data["kubeconfig"] = []byte(externalInfraKubeconfig)
		g.Expect(c.Update(t.Context(), source)).To(Succeed())
		g.Expect(r.reconcilePlatformCredentialsWithStatus(t.Context(), hc, upsert.New(false).CreateOrUpdate, namespace, p)).To(Succeed())
		g.Expect(c.Get(t.Context(), client.ObjectKeyFromObject(hc), hc)).To(Succeed())
		g.Expect(meta.IsStatusConditionTrue(hc.Status.Conditions, string(hyperv1.PlatformCredentialsFound))).To(BeTrue())
		g.Expect(meta.IsStatusConditionTrue(hc.Status.Conditions, "ConcurrentCondition")).To(BeTrue())
		g.Expect(patches).To(BeNumerically(">=", 3))
		target := &corev1.Secret{}
		g.Expect(c.Get(t.Context(), client.ObjectKey{Namespace: namespace, Name: hyperv1.KubeVirtInfraCredentialsSecretName}, target)).To(Succeed())
		g.Expect(target.Data).To(HaveKeyWithValue("kubeconfig", []byte(externalInfraKubeconfig)))
	})
}

func TestValidateKubevirtCredentials(t *testing.T) {
	for _, tc := range []struct {
		name     string
		platform hyperv1.PlatformType
		invalid  bool
		local    bool
	}{
		{name: "When external credentials are valid, it should not mark them published before reconciliation", platform: hyperv1.KubevirtPlatform},
		{name: "When external credentials are invalid, it should report rejection", platform: hyperv1.KubevirtPlatform, invalid: true},
		{name: "When external credentials are disabled, it should preserve local infrastructure behavior", platform: hyperv1.KubevirtPlatform, local: true},
		{name: "When another platform is selected, it should leave its credential handling unchanged", platform: hyperv1.NonePlatform, invalid: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			g := NewWithT(t)
			hc := credentialHostedCluster()
			hc.Spec.Platform.Type = tc.platform
			if tc.local {
				hc.Spec.Platform.Kubevirt.Credentials = nil
			}
			data := externalInfraKubeconfig
			if tc.invalid {
				data = "malformed"
			}
			source := &corev1.Secret{ObjectMeta: metav1.ObjectMeta{Namespace: hc.Namespace, Name: "infra"}, Data: map[string][]byte{"kubeconfig": []byte(data)}}
			c := fake.NewClientBuilder().WithScheme(api.Scheme).WithStatusSubresource(hc).WithObjects(hc, source).Build()
			r := &HostedClusterReconciler{Client: c}
			err := r.validateKubevirtCredentials(t.Context(), hc)
			if tc.invalid && tc.platform == hyperv1.KubevirtPlatform {
				g.Expect(err).To(HaveOccurred())
				g.Expect(meta.IsStatusConditionFalse(hc.Status.Conditions, string(hyperv1.PlatformCredentialsFound))).To(BeTrue())
			} else {
				g.Expect(err).NotTo(HaveOccurred())
				g.Expect(meta.FindStatusCondition(hc.Status.Conditions, string(hyperv1.PlatformCredentialsFound))).To(BeNil())
			}
			g.Expect(c.Get(t.Context(), client.ObjectKey{
				Namespace: manifests.HostedControlPlaneNamespace(hc.Namespace, hc.Name), Name: hyperv1.KubeVirtInfraCredentialsSecretName,
			}, &corev1.Secret{})).To(Satisfy(apierrors.IsNotFound))
		})
	}
}

func TestPatchPlatformCredentialsCondition(t *testing.T) {
	t.Run("When the HostedCluster generation advances during credential reconciliation, it should not publish a stale condition", func(t *testing.T) {
		g := NewWithT(t)
		hc := credentialHostedCluster()
		stale := hc.DeepCopy()
		c := fake.NewClientBuilder().WithScheme(api.Scheme).WithStatusSubresource(hc).WithObjects(hc).Build()
		hc.Generation++
		g.Expect(c.Update(t.Context(), hc)).To(Succeed())
		r := &HostedClusterReconciler{Client: c}
		g.Expect(r.patchPlatformCredentialsCondition(t.Context(), stale, errors.New("rejected credentials"))).To(MatchError(ContainSubstring("changed")))
		g.Expect(c.Get(t.Context(), client.ObjectKeyFromObject(hc), hc)).To(Succeed())
		g.Expect(meta.FindStatusCondition(hc.Status.Conditions, string(hyperv1.PlatformCredentialsFound))).To(BeNil())
	})
	t.Run("When metadata changes without a generation change, it should reject the stale snapshot rather than promote its resource version", func(t *testing.T) {
		g := NewWithT(t)
		hc := credentialHostedCluster()
		hc.Annotations = map[string]string{hyperv1.HostedClusterRestoredFromBackupAnnotation: "true"}
		c := fake.NewClientBuilder().WithScheme(api.Scheme).WithStatusSubresource(hc).WithObjects(hc).Build()
		g.Expect(c.Get(t.Context(), client.ObjectKeyFromObject(hc), hc)).To(Succeed())
		oldRV := hc.ResourceVersion
		fresh := hc.DeepCopy()
		fresh.Annotations["concurrent"] = "retained"
		fresh.Finalizers = append(fresh.Finalizers, "concurrent-finalizer")
		g.Expect(c.Update(t.Context(), fresh)).To(Succeed())
		r := &HostedClusterReconciler{Client: c}
		g.Expect(r.patchPlatformCredentialsCondition(t.Context(), hc, nil)).To(MatchError(ContainSubstring("metadata changed")))
		g.Expect(hc.ResourceVersion).To(Equal(oldRV))
		hcp := &hyperv1.HostedControlPlane{}
		meta.SetStatusCondition(&hcp.Status.Conditions, metav1.Condition{Type: string(hyperv1.HostedClusterRestoredFromBackup), Status: metav1.ConditionTrue, Reason: "Restored"})
		g.Expect(r.reconcileRestoredFromBackupWithStatus(t.Context(), hc, hcp)).To(HaveOccurred())
		g.Expect(c.Get(t.Context(), client.ObjectKeyFromObject(hc), fresh)).To(Succeed())
		g.Expect(fresh.Annotations).To(HaveKeyWithValue("concurrent", "retained"))
		g.Expect(fresh.Finalizers).To(ContainElement("concurrent-finalizer"))
	})
}
