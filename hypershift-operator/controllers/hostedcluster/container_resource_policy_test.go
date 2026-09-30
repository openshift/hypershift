package hostedcluster

import (
	"context"
	"testing"

	. "github.com/onsi/gomega"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	schedulingv1alpha1 "github.com/openshift/hypershift/api/scheduling/v1alpha1"
	"github.com/openshift/hypershift/support/api"
	controlplanecomponent "github.com/openshift/hypershift/support/controlplane-component"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/utils/ptr"

	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"

	"github.com/go-logr/logr"
)

func startupSizingConfig() *schedulingv1alpha1.ClusterSizingConfiguration {
	policy := schedulingv1alpha1.ContainerResourcePolicy{
		DefaultRequests:      schedulingv1alpha1.ContainerRequests{CPU: resource.MustParse("25m"), Memory: resource.MustParse("100Mi")},
		GoMemoryLimitPercent: 90, MemoryLimitMultiplier: 3,
	}
	large := *policy.DeepCopy()
	large.DefaultRequests.CPU = resource.MustParse("200m")
	return &schedulingv1alpha1.ClusterSizingConfiguration{
		ObjectMeta: metav1.ObjectMeta{Name: "cluster"},
		Spec: schedulingv1alpha1.ClusterSizingConfigurationSpec{Sizes: []schedulingv1alpha1.SizeConfiguration{
			{Name: "large", Criteria: schedulingv1alpha1.NodeCountCriteria{From: 11}, Effects: &schedulingv1alpha1.Effects{ContainerResourcePolicy: large}},
			{Name: "small", Criteria: schedulingv1alpha1.NodeCountCriteria{From: 0, To: ptr.To[uint32](10)}, Effects: &schedulingv1alpha1.Effects{ContainerResourcePolicy: policy}},
		}},
		Status: schedulingv1alpha1.ClusterSizingConfigurationStatus{Conditions: []metav1.Condition{{Type: schedulingv1alpha1.ClusterSizingConfigurationValidType, Status: metav1.ConditionTrue}}},
	}
}

func TestBootstrapContainerResourcePolicy(t *testing.T) {
	for _, tt := range []struct {
		name      string
		modify    func(*hyperv1.HostedCluster, *schedulingv1alpha1.ClusterSizingConfiguration)
		missing   bool
		wantCPU   string
		wantError string
		unchanged bool
	}{
		{name: "When a new HC has no size label, it should choose the zero-node class regardless of list order", wantCPU: "25m"},
		{name: "When constrained sizing is selected, it should publish limits before startup", modify: func(_ *hyperv1.HostedCluster, c *schedulingv1alpha1.ClusterSizingConfiguration) {
			p := &c.Spec.Sizes[1].Effects.ContainerResourcePolicy
			p.MemoryLimitMultiplier, p.MemoryLimitPercent, p.CPULimitPolicy = 0, 110, schedulingv1alpha1.CPULimitPolicyEqualsRequest
		}, wantCPU: "25m"},
		{name: "When the validator omits observed generation, it should still accept its valid condition", modify: func(_ *hyperv1.HostedCluster, c *schedulingv1alpha1.ClusterSizingConfiguration) {
			c.Generation = 1
		}, wantCPU: "25m"},
		{name: "When an override is provided, it should win over the current size label", modify: func(hc *hyperv1.HostedCluster, _ *schedulingv1alpha1.ClusterSizingConfiguration) {
			hc.Annotations[hyperv1.ClusterSizeOverrideAnnotation] = "large"
			hc.Labels = map[string]string{hyperv1.HostedClusterSizeLabel: "small"}
		}, wantCPU: "200m"},
		{name: "When a size label is present, it should use that class before the scheduler runs", modify: func(hc *hyperv1.HostedCluster, _ *schedulingv1alpha1.ClusterSizingConfiguration) {
			hc.Labels = map[string]string{hyperv1.HostedClusterSizeLabel: "large"}
		}, wantCPU: "200m"},
		{name: "When an override is invalid, it should block rather than silently use the smallest class", modify: func(hc *hyperv1.HostedCluster, _ *schedulingv1alpha1.ClusterSizingConfiguration) {
			hc.Annotations[hyperv1.ClusterSizeOverrideAnnotation] = "missing"
		}, wantError: "unknown size"},
		{name: "When the policy is invalid, it should block without patching the HC", modify: func(_ *hyperv1.HostedCluster, c *schedulingv1alpha1.ClusterSizingConfiguration) {
			c.Spec.Sizes[1].Effects.ContainerResourcePolicy.MemoryLimitMultiplier = 0
		}, wantError: "invalid startup"},
		{name: "When CPU limit policy is unknown, it should block without patching the HC", modify: func(_ *hyperv1.HostedCluster, c *schedulingv1alpha1.ClusterSizingConfiguration) {
			c.Spec.Sizes[1].Effects.ContainerResourcePolicy.CPULimitPolicy = "Unknown"
		}, wantError: "unknown cpuLimitPolicy"},
		{name: "When no zero-node class exists, it should block startup", modify: func(_ *hyperv1.HostedCluster, c *schedulingv1alpha1.ClusterSizingConfiguration) {
			c.Spec.Sizes[1].Criteria.From = 1
		}, wantError: "no size class"},
		{name: "When zero-node classes are ambiguous, it should block startup", modify: func(_ *hyperv1.HostedCluster, c *schedulingv1alpha1.ClusterSizingConfiguration) {
			c.Spec.Sizes[0].Criteria.From = 0
		}, wantError: "multiple size classes"},
		{name: "When configuration validation is pending, it should block startup", modify: func(_ *hyperv1.HostedCluster, c *schedulingv1alpha1.ClusterSizingConfiguration) {
			c.Status.Conditions = nil
		}, wantError: "waiting for valid"},
		{name: "When configuration is absent, it should leave existing annotations alone", missing: true, unchanged: true},
		{name: "When all policies are disabled, it should leave existing annotations alone", modify: func(_ *hyperv1.HostedCluster, c *schedulingv1alpha1.ClusterSizingConfiguration) {
			c.Spec.Sizes[0].Effects = nil
			c.Spec.Sizes[1].Effects = nil
		}, unchanged: true},
		{name: "When scheduling is complete, it should not compete with scheduler transitions", modify: func(hc *hyperv1.HostedCluster, _ *schedulingv1alpha1.ClusterSizingConfiguration) {
			hc.Annotations[hyperv1.HostedClusterScheduledAnnotation] = "true"
		}, unchanged: true},
		{name: "When the HC is paused, it should not publish a policy", modify: func(hc *hyperv1.HostedCluster, _ *schedulingv1alpha1.ClusterSizingConfiguration) {
			hc.Spec.PausedUntil = ptr.To("true")
		}, unchanged: true},
		{name: "When pausedUntil is invalid, it should return an error without publishing a policy", modify: func(hc *hyperv1.HostedCluster, _ *schedulingv1alpha1.ClusterSizingConfiguration) {
			hc.Spec.PausedUntil = ptr.To("invalid")
		}, wantError: "startup container resource policy: invalid pausedUntil"},
		{name: "When the HC is deleting, it should not block cleanup", modify: func(hc *hyperv1.HostedCluster, _ *schedulingv1alpha1.ClusterSizingConfiguration) {
			now := metav1.Now()
			hc.DeletionTimestamp = &now
			hc.Finalizers = []string{"test"}
		}, unchanged: true},
		{name: "When the selected class disables policy, it should remove a stale startup annotation", modify: func(_ *hyperv1.HostedCluster, c *schedulingv1alpha1.ClusterSizingConfiguration) {
			c.Spec.Sizes[1].Effects = nil
		}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			g := NewWithT(t)
			hc := &hyperv1.HostedCluster{ObjectMeta: metav1.ObjectMeta{Name: "cluster", Namespace: "clusters", Annotations: map[string]string{hyperv1.ContainerResourcePolicyAnnotation: "old", "preserve": "value"}}}
			config := startupSizingConfig()
			if tt.modify != nil {
				tt.modify(hc, config)
			}
			objects := []client.Object{hc}
			if !tt.missing {
				objects = append(objects, config)
			}
			cl := fake.NewClientBuilder().WithScheme(api.Scheme).WithObjects(objects...).Build()
			g.Expect(cl.Get(t.Context(), client.ObjectKeyFromObject(hc), hc)).To(Succeed())
			before := hc.DeepCopy()
			r := &HostedClusterReconciler{Client: cl}
			err := r.bootstrapContainerResourcePolicy(t.Context(), hc)
			if tt.wantError != "" {
				g.Expect(err).To(MatchError(ContainSubstring(tt.wantError)))
				g.Expect(hc).To(Equal(before))
			} else {
				g.Expect(err).NotTo(HaveOccurred())
			}
			stored := &hyperv1.HostedCluster{}
			g.Expect(cl.Get(t.Context(), client.ObjectKeyFromObject(hc), stored)).To(Succeed())
			g.Expect(stored).To(Equal(hc), "published and local policy must agree in the same reconcile")
			if tt.unchanged {
				g.Expect(hc).To(Equal(before))
			}
			g.Expect(hc.Labels).To(Equal(before.Labels))
			g.Expect(hc.Status).To(Equal(before.Status))
			g.Expect(hc.Annotations["preserve"]).To(Equal("value"))
			g.Expect(hc.Annotations[hyperv1.HostedClusterScheduledAnnotation]).To(Equal(before.Annotations[hyperv1.HostedClusterScheduledAnnotation]))
			if tt.wantCPU != "" {
				template := &corev1.PodTemplateSpec{Spec: corev1.PodSpec{Containers: []corev1.Container{{Name: "control-plane-operator"}}}}
				g.Expect(controlplanecomponent.ApplyContainerResourcePolicy("control-plane-operator", template, hc.Annotations)).To(Succeed())
				g.Expect(template.Spec.Containers[0].Resources.Requests.Cpu().String()).To(Equal(tt.wantCPU))
				if config.Spec.Sizes[1].Effects.ContainerResourcePolicy.CPULimitPolicy == schedulingv1alpha1.CPULimitPolicyEqualsRequest {
					g.Expect(template.Spec.Containers[0].Resources.Limits.Cpu().String()).To(Equal(tt.wantCPU))
					g.Expect(template.Spec.Containers[0].Resources.Limits.Memory().Cmp(resource.MustParse("110Mi"))).To(BeZero())
				}
				resourceVersion := hc.ResourceVersion
				g.Expect(r.bootstrapContainerResourcePolicy(t.Context(), hc)).To(Succeed())
				g.Expect(hc.ResourceVersion).To(Equal(resourceVersion), "bootstrap must be idempotent")
			} else if !tt.unchanged && tt.wantError == "" {
				g.Expect(hc.Annotations).NotTo(HaveKey(hyperv1.ContainerResourcePolicyAnnotation))
			}
		})
	}

	t.Run("When patching fails, it should retain the local HC and stop startup", func(t *testing.T) {
		g := NewWithT(t)
		hc := &hyperv1.HostedCluster{ObjectMeta: metav1.ObjectMeta{Name: "cluster", Namespace: "clusters"}}
		cl := fake.NewClientBuilder().WithScheme(api.Scheme).WithObjects(hc, startupSizingConfig()).WithInterceptorFuncs(interceptor.Funcs{
			Patch: func(context.Context, client.WithWatch, client.Object, client.Patch, ...client.PatchOption) error {
				return context.DeadlineExceeded
			},
		}).Build()
		before := hc.DeepCopy()
		g.Expect((&HostedClusterReconciler{Client: cl}).bootstrapContainerResourcePolicy(t.Context(), hc)).To(MatchError(ContainSubstring("publishing startup")))
		g.Expect(hc).To(Equal(before))
	})

	t.Run("When configuration lookup fails, it should block startup without changing the HC", func(t *testing.T) {
		g := NewWithT(t)
		hc := &hyperv1.HostedCluster{ObjectMeta: metav1.ObjectMeta{Name: "cluster", Namespace: "clusters"}}
		cl := fake.NewClientBuilder().WithScheme(api.Scheme).WithInterceptorFuncs(interceptor.Funcs{
			Get: func(context.Context, client.WithWatch, client.ObjectKey, client.Object, ...client.GetOption) error {
				return context.DeadlineExceeded
			},
		}).Build()
		before := hc.DeepCopy()
		g.Expect((&HostedClusterReconciler{Client: cl}).bootstrapContainerResourcePolicy(t.Context(), hc)).To(MatchError(ContainSubstring("reading startup")))
		g.Expect(hc).To(Equal(before))
	})
}

func TestReconcile(t *testing.T) {
	for _, invalid := range []bool{false, true} {
		name := "When initial policy is valid, it should publish it before the first HCP and workload reconciliation"
		if invalid {
			name = "When initial policy is invalid, it should report failure without entering workload reconciliation"
		}
		t.Run(name, func(t *testing.T) {
			g := NewWithT(t)
			hc := &hyperv1.HostedCluster{ObjectMeta: metav1.ObjectMeta{Name: "cluster", Namespace: "clusters"}}
			config := startupSizingConfig()
			if invalid {
				config.Spec.Sizes[1].Effects.ContainerResourcePolicy.MemoryLimitMultiplier = 0
			}
			cl := fake.NewClientBuilder().WithScheme(api.Scheme).WithObjects(hc, config).WithStatusSubresource(hc).Build()
			called := false
			r := &HostedClusterReconciler{Client: cl, now: metav1.Now, overwriteReconcile: func(ctx context.Context, _ ctrl.Request, _ logr.Logger, local *hyperv1.HostedCluster) (ctrl.Result, error) {
				called = true
				stored := &hyperv1.HostedCluster{}
				g.Expect(cl.Get(ctx, client.ObjectKeyFromObject(local), stored)).To(Succeed())
				g.Expect(stored.Annotations).To(Equal(local.Annotations))
				hcp := &hyperv1.HostedControlPlane{}
				g.Expect(reconcileHostedControlPlane(hcp, local, false, false, func() (map[string]string, error) { return nil, nil })).To(Succeed())
				g.Expect(hcp.Annotations[hyperv1.ContainerResourcePolicyAnnotation]).NotTo(BeEmpty())
				template := &corev1.PodTemplateSpec{Spec: corev1.PodSpec{Containers: []corev1.Container{{Name: "control-plane-operator"}}}}
				g.Expect(controlplanecomponent.ApplyContainerResourcePolicy("control-plane-operator", template, hcp.Annotations)).To(Succeed())
				g.Expect(template.Spec.Containers[0].Resources.Requests.Cpu().String()).To(Equal("25m"))
				return ctrl.Result{}, nil
			}}
			_, err := r.Reconcile(t.Context(), ctrl.Request{NamespacedName: client.ObjectKeyFromObject(hc)})
			g.Expect(called).To(Equal(!invalid))
			if invalid {
				g.Expect(err).To(HaveOccurred())
				g.Expect(cl.Get(t.Context(), client.ObjectKeyFromObject(hc), hc)).To(Succeed())
				g.Expect(meta.FindStatusCondition(hc.Status.Conditions, string(hyperv1.ReconciliationSucceeded)).Status).To(Equal(metav1.ConditionFalse))
			} else {
				g.Expect(err).NotTo(HaveOccurred())
			}
		})
	}
}
