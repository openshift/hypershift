package controlplanecomponent

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"testing"

	. "github.com/onsi/gomega"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	schedulingv1alpha1 "github.com/openshift/hypershift/api/scheduling/v1alpha1"
	"github.com/openshift/hypershift/support/podspec"
	"github.com/openshift/hypershift/support/testutil"
	"github.com/openshift/hypershift/support/upsert"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/utils/ptr"

	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

func resourcePolicyForTest() schedulingv1alpha1.ContainerResourcePolicy {
	return schedulingv1alpha1.ContainerResourcePolicy{
		DefaultRequests: schedulingv1alpha1.ContainerRequests{
			CPU: resource.MustParse("25m"), Memory: resource.MustParse("101"),
		},
		GoMemoryLimitPercent:  90,
		MemoryLimitMultiplier: 3,
		Containers: []schedulingv1alpha1.ContainerResources{{
			Workload: "kube-apiserver", Container: "kube-apiserver",
			Requests:             schedulingv1alpha1.ContainerRequests{CPU: resource.MustParse("200m"), Memory: resource.MustParse("1Gi")},
			GoMemoryLimitPercent: ptr.To[int32](80), GoMaxProcs: 2,
		}},
	}
}

func policyAnnotationsForTest(t *testing.T, policy schedulingv1alpha1.ContainerResourcePolicy) map[string]string {
	t.Helper()
	encoded, err := json.Marshal(policy)
	if err != nil {
		t.Fatal(err)
	}
	return map[string]string{hyperv1.ContainerResourcePolicyAnnotation: string(encoded)}
}

func TestApplyContainerResourcePolicy(t *testing.T) {
	for _, tt := range []struct {
		name   string
		policy schedulingv1alpha1.CPULimitPolicy
	}{
		{name: "When CPU limit policy is omitted, it should remove CPU limits"},
		{name: "When CPU limit policy is None, it should remove CPU limits", policy: schedulingv1alpha1.CPULimitPolicyNone},
		{name: "When CPU limit policy is EqualsRequest, it should set CPU limits to requests", policy: schedulingv1alpha1.CPULimitPolicyEqualsRequest},
	} {
		t.Run(tt.name, func(t *testing.T) {
			g := NewWithT(t)
			policy := resourcePolicyForTest()
			policy.CPULimitPolicy = tt.policy
			container := corev1.Container{Name: "main", Resources: corev1.ResourceRequirements{Limits: corev1.ResourceList{corev1.ResourceCPU: resource.MustParse("1")}}}
			template := &corev1.PodTemplateSpec{Spec: corev1.PodSpec{Containers: []corev1.Container{container}, InitContainers: []corev1.Container{container}}}
			g.Expect(ApplyContainerResourcePolicy("other", template, policyAnnotationsForTest(t, policy))).To(Succeed())
			for _, got := range []corev1.Container{template.Spec.Containers[0], template.Spec.InitContainers[0]} {
				if tt.policy == schedulingv1alpha1.CPULimitPolicyEqualsRequest {
					g.Expect(got.Resources.Limits.Cpu().Cmp(*got.Resources.Requests.Cpu())).To(BeZero())
				} else {
					g.Expect(got.Resources.Limits).NotTo(HaveKey(corev1.ResourceCPU))
				}
			}
		})
	}
	for _, tt := range []struct{ memory, limit string }{
		{"64Mi", "71Mi"}, {"1Gi", "1127Mi"}, {"10Mi", "11Mi"}, {"1", "1Mi"}, {"10485760.1", "12Mi"},
	} {
		t.Run("When 110 percent is requested for "+tt.memory+", it should round up and constrain every container", func(t *testing.T) {
			g := NewWithT(t)
			policy := resourcePolicyForTest()
			policy.MemoryLimitMultiplier, policy.MemoryLimitPercent, policy.CPULimitPolicy = 0, 110, schedulingv1alpha1.CPULimitPolicyEqualsRequest
			policy.DefaultRequests.Memory = resource.MustParse(tt.memory)
			policy.Containers = []schedulingv1alpha1.ContainerResources{{Workload: "other", Container: "non-go", Requests: policy.DefaultRequests, GoMemoryLimitPercent: ptr.To[int32](0)}}
			template := &corev1.PodTemplateSpec{Spec: corev1.PodSpec{
				Containers:     []corev1.Container{{Name: "main", Env: []corev1.EnvVar{{Name: "GOMAXPROCS", Value: "8"}}}, {Name: "non-go"}},
				InitContainers: []corev1.Container{{Name: "init"}, {Name: "restartable", RestartPolicy: ptr.To(corev1.ContainerRestartPolicyAlways)}},
			}}
			g.Expect(ApplyContainerResourcePolicy("other", template, policyAnnotationsForTest(t, policy))).To(Succeed())
			for _, containers := range [][]corev1.Container{template.Spec.Containers, template.Spec.InitContainers} {
				for _, c := range containers {
					g.Expect(c.Resources.Limits.Cpu().Cmp(*c.Resources.Requests.Cpu())).To(BeZero())
					g.Expect(c.Resources.Limits.Memory().Cmp(resource.MustParse(tt.limit))).To(BeZero())
					g.Expect(c.Resources.Limits.Memory().Cmp(*c.Resources.Requests.Memory())).To(BeNumerically(">=", 0))
				}
			}
			g.Expect(template.Spec.Containers[0].Env).To(ContainElement(corev1.EnvVar{Name: "GOMAXPROCS", Value: "8"}))
			g.Expect(template.Spec.Containers[1].Env).To(BeEmpty())
			g.Expect(template.Spec.InitContainers[0].Env).NotTo(ContainElement(HaveField("Name", "GOMAXPROCS")))
		})
	}
	t.Run("When policy matches regular and init containers, it should override resources and deduplicate runtime settings", func(t *testing.T) {
		g := NewWithT(t)
		policy := resourcePolicyForTest()
		policy.Containers = append(policy.Containers, schedulingv1alpha1.ContainerResources{
			Workload: "kube-apiserver", Container: "non-go", Requests: policy.DefaultRequests, GoMemoryLimitPercent: ptr.To[int32](0),
		})
		container := corev1.Container{
			Name: "kube-apiserver",
			Resources: corev1.ResourceRequirements{
				Requests: corev1.ResourceList{corev1.ResourceCPU: resource.MustParse("1"), aroSwiftNICResource: resource.MustParse("1")},
				Limits:   corev1.ResourceList{corev1.ResourceCPU: resource.MustParse("2"), aroSwiftNICResource: resource.MustParse("1"), corev1.ResourceEphemeralStorage: resource.MustParse("1Gi")},
			},
			Env: []corev1.EnvVar{{Name: "GOMEMLIMIT", Value: "legacy"}, {Name: "GOMEMLIMIT", ValueFrom: &corev1.EnvVarSource{}}, {Name: "GOMAXPROCS", Value: "8"}, {Name: "GOMAXPROCS", Value: "16"}, {Name: "OTHER", Value: "preserved"}},
		}
		template := &corev1.PodTemplateSpec{Spec: corev1.PodSpec{
			Containers:     []corev1.Container{container, {Name: "sidecar", Env: []corev1.EnvVar{{Name: "GOMAXPROCS", Value: "7"}}}, {Name: "non-go", Env: []corev1.EnvVar{{Name: "GOMEMLIMIT", Value: "legacy"}}}},
			InitContainers: []corev1.Container{container},
		}}
		annotations := policyAnnotationsForTest(t, policy)
		g.Expect(ApplyContainerResourcePolicy("kube-apiserver", template, annotations)).To(Succeed())
		for _, got := range []corev1.Container{template.Spec.Containers[0], template.Spec.InitContainers[0]} {
			g.Expect(got.Resources.Requests.Cpu().String()).To(Equal("200m"))
			g.Expect(got.Resources.Requests.Memory().String()).To(Equal("1Gi"))
			g.Expect(got.Resources.Limits.Memory().String()).To(Equal("3Gi"))
			g.Expect(got.Resources.Limits).NotTo(HaveKey(corev1.ResourceCPU))
			g.Expect(got.Resources.Requests[aroSwiftNICResource]).To(Equal(resource.MustParse("1")))
			g.Expect(got.Resources.Limits[aroSwiftNICResource]).To(Equal(resource.MustParse("1")))
			g.Expect(got.Resources.Limits[corev1.ResourceEphemeralStorage]).To(Equal(resource.MustParse("1Gi")))
			g.Expect(got.Env).To(ConsistOf(corev1.EnvVar{Name: "OTHER", Value: "preserved"}, corev1.EnvVar{Name: "GOMEMLIMIT", Value: "858993459"}, corev1.EnvVar{Name: "GOMAXPROCS", Value: "2"}))
		}
		g.Expect(template.Spec.Containers[1].Resources.Requests.Cpu().String()).To(Equal("25m"))
		g.Expect(template.Spec.Containers[1].Env).To(ConsistOf(corev1.EnvVar{Name: "GOMAXPROCS", Value: "7"}, corev1.EnvVar{Name: "GOMEMLIMIT", Value: "90"}))
		g.Expect(template.Spec.Containers[2].Env).To(BeEmpty())
		g.Expect(template.Annotations[containerResourcePolicyHashAnnotation]).To(Equal(fmt.Sprintf("%x", sha256.Sum256([]byte(annotations[hyperv1.ContainerResourcePolicyAnnotation])))))
		projected := template.DeepCopy()
		g.Expect(ApplyContainerResourcePolicy("kube-apiserver", template, annotations)).To(Succeed())
		g.Expect(template).To(Equal(projected), "identical policy must not keep changing the pod template")
	})

	t.Run("When a matching entry leaves GoMaxProcs unset, it should preserve the manifest default", func(t *testing.T) {
		g := NewWithT(t)
		policy := resourcePolicyForTest()
		policy.Containers[0].GoMaxProcs = 0
		template := &corev1.PodTemplateSpec{Spec: corev1.PodSpec{Containers: []corev1.Container{{Name: "kube-apiserver", Env: []corev1.EnvVar{{Name: "GOMAXPROCS", Value: "8"}}}}}}
		g.Expect(ApplyContainerResourcePolicy("kube-apiserver", template, policyAnnotationsForTest(t, policy))).To(Succeed())
		g.Expect(template.Spec.Containers[0].Env).To(ContainElement(corev1.EnvVar{Name: "GOMAXPROCS", Value: "8"}))
	})

	t.Run("When the workload name differs, it should use explicit defaults rather than another workload's entry", func(t *testing.T) {
		g := NewWithT(t)
		template := &corev1.PodTemplateSpec{Spec: corev1.PodSpec{Containers: []corev1.Container{{Name: "kube-apiserver"}}}}
		g.Expect(ApplyContainerResourcePolicy("other", template, policyAnnotationsForTest(t, resourcePolicyForTest()))).To(Succeed())
		g.Expect(template.Spec.Containers[0].Resources.Requests.Cpu().String()).To(Equal("25m"))
	})

	t.Run("When memory approaches int64 limits, it should compute the percentage without overflow", func(t *testing.T) {
		g := NewWithT(t)
		policy := resourcePolicyForTest()
		policy.DefaultRequests.Memory = resource.MustParse("9223372036854775807")
		policy.MemoryLimitMultiplier = 1
		template := &corev1.PodTemplateSpec{Spec: corev1.PodSpec{Containers: []corev1.Container{{Name: "sidecar"}}}}
		g.Expect(ApplyContainerResourcePolicy("other", template, policyAnnotationsForTest(t, policy))).To(Succeed())
		g.Expect(template.Spec.Containers[0].Env).To(ContainElement(corev1.EnvVar{Name: "GOMEMLIMIT", Value: "8301034833169298226"}))
	})

	t.Run("When no annotation is present, it should preserve manifest resources and environment", func(t *testing.T) {
		g := NewWithT(t)
		template := &corev1.PodTemplateSpec{Spec: corev1.PodSpec{Containers: []corev1.Container{{Name: "kube-apiserver", Env: []corev1.EnvVar{{Name: "GOMEMLIMIT", Value: "legacy"}}}}}}
		before := template.DeepCopy()
		g.Expect(ApplyContainerResourcePolicy("kube-apiserver", template, nil)).To(Succeed())
		g.Expect(template).To(Equal(before))
	})

	tests := []struct {
		name   string
		change func(*schedulingv1alpha1.ContainerResourcePolicy)
		raw    *string
	}{
		{name: "When JSON is malformed, it should reject the policy atomically", raw: ptr.To("{")},
		{name: "When the annotation is empty, it should reject the policy atomically", raw: ptr.To("")},
		{name: "When the policy is null, it should reject the policy atomically", raw: ptr.To("null")},
		{name: "When parameters are missing, it should reject the policy atomically", raw: ptr.To("{}")},
		{name: "When CPU limit policy is unknown, it should reject the policy atomically", change: func(p *schedulingv1alpha1.ContainerResourcePolicy) { p.CPULimitPolicy = "Unknown" }},
		{name: "When default CPU is missing, it should reject the policy atomically", change: func(p *schedulingv1alpha1.ContainerResourcePolicy) { p.DefaultRequests.CPU = resource.Quantity{} }},
		{name: "When default memory is negative, it should reject the policy atomically", change: func(p *schedulingv1alpha1.ContainerResourcePolicy) {
			p.DefaultRequests.Memory = resource.MustParse("-1Gi")
		}},
		{name: "When the multiplier is missing, it should reject the policy atomically", change: func(p *schedulingv1alpha1.ContainerResourcePolicy) { p.MemoryLimitMultiplier = 0 }},
		{name: "When both memory modes are set, it should reject the policy atomically", change: func(p *schedulingv1alpha1.ContainerResourcePolicy) { p.MemoryLimitPercent = 110 }},
		{name: "When the memory percentage is below request, it should reject the policy atomically", change: func(p *schedulingv1alpha1.ContainerResourcePolicy) {
			p.MemoryLimitMultiplier, p.MemoryLimitPercent = 0, 99
		}},
		{name: "When the memory percentage exceeds the maximum, it should reject the policy atomically", change: func(p *schedulingv1alpha1.ContainerResourcePolicy) {
			p.MemoryLimitMultiplier, p.MemoryLimitPercent = 0, 1601
		}},
		{name: "When percentage rounding overflows, it should reject the policy atomically", change: func(p *schedulingv1alpha1.ContainerResourcePolicy) {
			p.MemoryLimitMultiplier, p.MemoryLimitPercent = 0, 100
			p.DefaultRequests.Memory = resource.MustParse("9223372036854775807")
		}},
		{name: "When the percentage is missing, it should reject the policy atomically", change: func(p *schedulingv1alpha1.ContainerResourcePolicy) { p.GoMemoryLimitPercent = 0 }},
		{name: "When the percentage exceeds 100, it should reject the policy atomically", change: func(p *schedulingv1alpha1.ContainerResourcePolicy) { p.GoMemoryLimitPercent = 101 }},
		{name: "When memory limits overflow, it should reject the policy atomically", change: func(p *schedulingv1alpha1.ContainerResourcePolicy) {
			p.DefaultRequests.Memory = resource.MustParse("4Ei")
		}},
		{name: "When an unmatched entry overflows, it should reject the policy atomically", change: func(p *schedulingv1alpha1.ContainerResourcePolicy) {
			p.Containers[0].Requests.Memory = resource.MustParse("4Ei")
		}},
		{name: "When an entry has no workload, it should reject the policy atomically", change: func(p *schedulingv1alpha1.ContainerResourcePolicy) { p.Containers[0].Workload = "" }},
		{name: "When an entry is duplicated, it should reject the policy atomically", change: func(p *schedulingv1alpha1.ContainerResourcePolicy) {
			p.Containers = append(p.Containers, p.Containers[0])
		}},
		{name: "When an entry has a negative percentage, it should reject the policy atomically", change: func(p *schedulingv1alpha1.ContainerResourcePolicy) {
			p.Containers[0].GoMemoryLimitPercent = ptr.To[int32](-1)
		}},
		{name: "When an entry has negative GOMAXPROCS, it should reject the policy atomically", change: func(p *schedulingv1alpha1.ContainerResourcePolicy) { p.Containers[0].GoMaxProcs = -1 }},
		{name: "When an entry exceeds the GOMAXPROCS maximum, it should reject the policy atomically", change: func(p *schedulingv1alpha1.ContainerResourcePolicy) { p.Containers[0].GoMaxProcs = 1025 }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			g := NewWithT(t)
			policy := resourcePolicyForTest()
			if tt.change != nil {
				tt.change(&policy)
			}
			annotations := policyAnnotationsForTest(t, policy)
			if tt.raw != nil {
				annotations[hyperv1.ContainerResourcePolicyAnnotation] = *tt.raw
			}
			template := &corev1.PodTemplateSpec{ObjectMeta: metav1.ObjectMeta{Annotations: map[string]string{"keep": "me"}}, Spec: corev1.PodSpec{Containers: []corev1.Container{{Name: "sidecar"}}, InitContainers: []corev1.Container{{Name: "init"}}}}
			before := template.DeepCopy()
			g.Expect(ApplyContainerResourcePolicy("other", template, annotations)).NotTo(Succeed())
			g.Expect(template).To(Equal(before))
		})
	}
	t.Run("When the template is nil, it should return an error", func(t *testing.T) {
		NewWithT(t).Expect(ApplyContainerResourcePolicy("other", nil, nil)).NotTo(Succeed())
	})
}

func TestValidateContainerResourcePolicy(t *testing.T) {
	t.Run("When the policy is valid, it should index entries by workload and container", func(t *testing.T) {
		g := NewWithT(t)
		policy := resourcePolicyForTest()
		entries, err := validateContainerResourcePolicy(policy)
		g.Expect(err).NotTo(HaveOccurred())
		g.Expect(entries).To(Equal(map[containerKey]schedulingv1alpha1.ContainerResources{
			{"kube-apiserver", "kube-apiserver"}: policy.Containers[0],
		}))
	})
	t.Run("When the policy is empty, it should reject it before calculating limits", func(t *testing.T) {
		g := NewWithT(t)
		entries, err := validateContainerResourcePolicy(schedulingv1alpha1.ContainerResourcePolicy{})
		g.Expect(err).To(HaveOccurred())
		g.Expect(entries).To(BeNil())
	})
}

func TestMemoryLimitForRequest(t *testing.T) {
	for _, tt := range []struct {
		name       string
		memory     string
		multiplier int32
		percent    int32
		limit      string
	}{
		{name: "When a multiplier is set, it should scale the request exactly", memory: "64Mi", multiplier: 3, limit: "192Mi"},
		{name: "When a percentage is set, it should round up to MiB", memory: "64Mi", percent: 110, limit: "71Mi"},
		{name: "When multiplication overflows, it should reject the request", memory: "4Ei", multiplier: 3},
		{name: "When percentage rounding overflows, it should reject the request", memory: "9223372036854775807", percent: 100},
	} {
		t.Run(tt.name, func(t *testing.T) {
			g := NewWithT(t)
			limit, err := memoryLimitForRequest(schedulingv1alpha1.ContainerResourcePolicy{
				MemoryLimitMultiplier: tt.multiplier, MemoryLimitPercent: tt.percent,
			}, resource.MustParse(tt.memory))
			if tt.limit == "" {
				g.Expect(err).To(HaveOccurred())
				return
			}
			g.Expect(err).NotTo(HaveOccurred())
			g.Expect(limit.Cmp(resource.MustParse(tt.limit))).To(BeZero())
		})
	}
}

func TestReconcileWorkload(t *testing.T) {
	t.Run("When policy changes and is removed, it should roll the template and restore generated defaults without losing custom resources", func(t *testing.T) {
		g := NewWithT(t)
		scheme := runtime.NewScheme()
		g.Expect(corev1.AddToScheme(scheme)).To(Succeed())
		g.Expect(appsv1.AddToScheme(scheme)).To(Succeed())
		workload := &controlPlaneWorkload[*appsv1.Deployment]{
			name: testComponentName, workloadProvider: &deploymentProvider{}, ComponentOptions: &testComponent{},
			konnectivityContainerOpts: &KonnectivityContainerOptions{Mode: HTTPS},
			tokenMinterContainerOpts:  &TokenMinterContainerOptions{TokenType: KubeAPIServerToken, ServiceAccountName: "test", ServiceAccountNameSpace: "test"},
			availabilityProberOpts:    &podspec.AvailabilityProberOpts{},
			adapt: func(_ WorkloadContext, deployment *appsv1.Deployment) error {
				deployment.Spec.Template.Spec.Containers[0].Env = append(deployment.Spec.Template.Spec.Containers[0].Env,
					corev1.EnvVar{Name: "GOMEMLIMIT", Value: "legacy-kas"}, corev1.EnvVar{Name: "GOMAXPROCS", Value: "8"})
				deployment.Spec.Template.Spec.InitContainers = []corev1.Container{{Name: "init", Image: "test-component"}}
				return nil
			},
		}
		policy := resourcePolicyForTest()
		policy.Containers[0].Workload, policy.Containers[0].Container = testComponentName, testComponentName
		hcp := &hyperv1.HostedControlPlane{ObjectMeta: metav1.ObjectMeta{Namespace: testComponentNamespace, Annotations: policyAnnotationsForTest(t, policy)}}
		hcp.Annotations[hyperv1.ResourceRequestOverrideAnnotationPrefix+"/"+testComponentName+"."+testComponentName] = "cpu=75m,memory=64Mi,aro.openshift.io/swift-nic=1"
		ctx := ControlPlaneContext{Context: t.Context(), HCP: hcp, OmitOwnerReference: true, ApplyProvider: upsert.NewApplyProvider(false), ReleaseImageProvider: testutil.FakeImageProvider(), Client: fake.NewClientBuilder().WithScheme(scheme).Build()}
		get := func() *appsv1.Deployment {
			deployment := &appsv1.Deployment{}
			g.Expect(ctx.Client.Get(ctx, client.ObjectKey{Name: testComponentName, Namespace: testComponentNamespace}, deployment)).To(Succeed())
			return deployment
		}
		g.Expect(workload.reconcileWorkload(ctx)).To(Succeed())
		first := get()
		g.Expect(first.Spec.Template.Spec.Containers[0].Resources.Requests.Cpu().String()).To(Equal("200m"))
		g.Expect(first.Spec.Template.Spec.InitContainers[0].Resources.Requests.Cpu().String()).To(Equal("25m"))
		g.Expect(first.Spec.Template.Spec.Containers).To(HaveLen(3), "konnectivity and token-minter sidecars should be present")
		g.Expect(first.Spec.Template.Spec.InitContainers).To(HaveLen(2), "availability prober should be injected before the manifest init container")
		for _, container := range first.Spec.Template.Spec.Containers[1:] {
			g.Expect(container.Resources.Requests.Cpu().String()).To(Equal("25m"), "injected %s must receive explicit defaults", container.Name)
			g.Expect(container.Resources.Requests.Memory().Value()).To(Equal(int64(101)))
		}
		g.Expect(first.Spec.Template.Spec.Containers[0].Env).To(ContainElement(corev1.EnvVar{Name: "GOMAXPROCS", Value: "2"}))
		first.Spec.Template.Spec.Containers[0].Resources.Limits[corev1.ResourceEphemeralStorage] = resource.MustParse("2Gi")
		g.Expect(ctx.Client.Update(ctx, first)).To(Succeed())
		pending := &corev1.Pod{ObjectMeta: metav1.ObjectMeta{Name: "pending", Namespace: testComponentNamespace, Annotations: first.Spec.Template.Annotations}, Spec: first.Spec.Template.Spec, Status: corev1.PodStatus{Phase: corev1.PodPending}}
		g.Expect(ctx.Client.Create(ctx, pending)).To(Succeed())

		policy.Containers = nil
		policy.MemoryLimitMultiplier, policy.MemoryLimitPercent, policy.CPULimitPolicy = 0, 110, schedulingv1alpha1.CPULimitPolicyEqualsRequest
		hcp.Annotations[hyperv1.ContainerResourcePolicyAnnotation] = policyAnnotationsForTest(t, policy)[hyperv1.ContainerResourcePolicyAnnotation]
		g.Expect(workload.reconcileWorkload(ctx)).To(Succeed())
		second := get()
		g.Expect(second.Spec.Template.Spec.Containers[0].Resources.Requests.Cpu().String()).To(Equal("25m"))
		for _, containers := range [][]corev1.Container{second.Spec.Template.Spec.Containers, second.Spec.Template.Spec.InitContainers} {
			for _, c := range containers {
				g.Expect(c.Resources.Limits.Cpu().String()).To(Equal("25m"), "all injected containers must receive limits")
				g.Expect(c.Resources.Limits.Memory().String()).To(Equal("1Mi"))
			}
		}
		g.Expect(second.Spec.Template.Spec.Containers[0].Env).To(ContainElement(corev1.EnvVar{Name: "GOMAXPROCS", Value: "8"}))
		g.Expect(second.Spec.Template.Annotations[containerResourcePolicyHashAnnotation]).NotTo(Equal(first.Spec.Template.Annotations[containerResourcePolicyHashAnnotation]))
		oldPod := &corev1.Pod{}
		g.Expect(ctx.Client.Get(ctx, client.ObjectKeyFromObject(pending), oldPod)).To(Succeed())
		g.Expect(oldPod.Annotations[containerResourcePolicyHashAnnotation]).To(Equal(first.Spec.Template.Annotations[containerResourcePolicyHashAnnotation]), "pending pod retains its actual policy until the workload controller replaces it")

		delete(hcp.Annotations, hyperv1.ContainerResourcePolicyAnnotation)
		g.Expect(workload.reconcileWorkload(ctx)).To(Succeed())
		third := get()
		container := third.Spec.Template.Spec.Containers[0]
		g.Expect(container.Resources.Requests.Cpu().String()).To(Equal("75m"))
		g.Expect(container.Resources.Requests.Memory().String()).To(Equal("64Mi"))
		g.Expect(container.Resources.Limits).NotTo(HaveKey(corev1.ResourceMemory))
		g.Expect(container.Resources.Limits).NotTo(HaveKey(corev1.ResourceCPU))
		g.Expect(container.Resources.Limits[corev1.ResourceEphemeralStorage]).To(Equal(resource.MustParse("2Gi")))
		g.Expect(container.Resources.Limits[aroSwiftNICResource]).To(Equal(resource.MustParse("1")))
		g.Expect(container.Env).To(ContainElement(corev1.EnvVar{Name: "GOMEMLIMIT", Value: "legacy-kas"}))
		g.Expect(container.Env).To(ContainElement(corev1.EnvVar{Name: "GOMAXPROCS", Value: "8"}))
		g.Expect(third.Spec.Template.Spec.InitContainers[1].Resources.Requests).To(BeEmpty())
		g.Expect(third.Spec.Template.Annotations).NotTo(HaveKey(containerResourcePolicyHashAnnotation))
	})
}

func TestUpdate(t *testing.T) {
	t.Run("When a policy is invalid, it should fail before applying associated manifests", func(t *testing.T) {
		workload := &controlPlaneWorkload[*appsv1.Deployment]{name: testComponentName}
		// No client or apply provider: validation must fail before either is used.
		NewWithT(t).Expect(workload.update(ControlPlaneContext{HCP: &hyperv1.HostedControlPlane{ObjectMeta: metav1.ObjectMeta{Annotations: map[string]string{hyperv1.ContainerResourcePolicyAnnotation: "{}"}}}})).NotTo(Succeed())
	})
}
