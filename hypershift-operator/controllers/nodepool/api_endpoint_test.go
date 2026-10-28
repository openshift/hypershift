package nodepool

import (
	"context"
	"testing"

	. "github.com/onsi/gomega"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	haproxy "github.com/openshift/hypershift/hypershift-operator/controllers/nodepool/apiserver-haproxy"
	"github.com/openshift/hypershift/support/api"
	"github.com/openshift/hypershift/support/config"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"
)

func testConfigGeneratorEndpointSnapshot(t *testing.T) {
	t.Helper()
	t.Run("When the kubeconfig changes after HAProxy rendering, it should track the endpoint actually rendered without rereading the Secret", func(t *testing.T) {
		g := NewWithT(t)
		f := newRolloutFixture(t, hyperv1.UpgradeTypeReplace)
		hc := &hyperv1.HostedCluster{
			Spec:   hyperv1.HostedClusterSpec{Platform: hyperv1.PlatformSpec{Type: hyperv1.AWSPlatform, AWS: &hyperv1.AWSPlatformSpec{EndpointAccess: hyperv1.Public}}},
			Status: hyperv1.HostedClusterStatus{KubeConfig: &corev1.LocalObjectReference{Name: "changing-kubeconfig"}},
		}
		c := fake.NewClientBuilder().WithScheme(api.Scheme).WithObjects(coreConfigMaps[:2]...).WithInterceptorFuncs(interceptor.Funcs{
			Get: func(ctx context.Context, c client.WithWatch, key client.ObjectKey, obj client.Object, opts ...client.GetOption) error {
				if _, ok := obj.(*corev1.Secret); ok {
					return apierrors.NewServiceUnavailable("the kubeconfig must not be read a second time")
				}
				return c.Get(ctx, key, obj, opts...)
			},
		}).Build()
		payload := haproxy.Config{Raw: "captured-haproxy-payload", Endpoint: "private=false,server=https://api.hc.hypershift.local:6443"}
		cg, err := NewConfigGenerator(t.Context(), c, hc, f.capi.nodePool, f.capi.releaseImage, payload, "test-test", StreamRHEL9)
		g.Expect(err).NotTo(HaveOccurred())
		g.Expect(cg.endpointConfig).To(Equal(payload.Endpoint))
		g.Expect(cg.endpointConfigKnown).To(BeTrue())
		oldHash := cg.RolloutHash()
		payload.Endpoint = "private=false,server=https://api.public.example.com:6443"
		cg, err = NewConfigGenerator(t.Context(), c, hc, f.capi.nodePool, f.capi.releaseImage, payload, "test-test", StreamRHEL9)
		g.Expect(err).NotTo(HaveOccurred())
		g.Expect(cg.RolloutHash()).NotTo(Equal(oldHash), "the later public endpoint must request a real rollout")
	})
}

func testAPIEndpointCutover(t *testing.T) {
	t.Helper()
	t.Run("When an endpoint rollout is in progress, it should report cutover only after replacement completes", func(t *testing.T) {
		g := NewWithT(t)
		f := newRolloutFixture(t, hyperv1.UpgradeTypeReplace)
		c := f.capi
		c.endpointConfigKnown = true
		c.endpointConfig = "private=true"
		c.nodePool.Annotations[config.NodePoolAPIEndpointAnnotation] = c.endpointConfig
		c.nodePool.Annotations[nodePoolAnnotationCurrentRolloutConfig] = c.RolloutHashWithoutVersion()
		const publicEndpoint = "private=false,server=https://api.public.example.com:6443"
		c.endpointConfig = publicEndpoint
		c.mcoRawConfig = "user-config-A + public-haproxy-A"
		g.Expect(f.reconcile(t)).To(BeTrue())
		g.Expect(IsAPIEndpointApplied(c.nodePool, publicEndpoint)).To(BeFalse())
		g.Expect(c.nodePool.Annotations).To(HaveKeyWithValue(config.NodePoolAPIEndpointAnnotation, "private=true"))

		// Preserve endpoint identity when a management image change leaves the in-flight payload unchanged.
		c.mcoRawConfig = "user-config-A + public-haproxy-B"
		g.Expect(f.reconcile(t)).To(BeFalse())
		g.Expect(IsAPIEndpointApplied(c.nodePool, publicEndpoint)).To(BeFalse())
		f.complete(t)
		g.Expect(IsAPIEndpointApplied(c.nodePool, publicEndpoint)).To(BeTrue())
	})
}

func testAPIEndpointAdoption(t *testing.T) {
	t.Helper()
	for _, known := range []bool{true, false} {
		name := "When the adopted payload has no endpoint proof, it should retain the private endpoint across reconciles"
		if known {
			name = "When an existing payload matches the resolved endpoint, it should track its endpoint without replacing nodes"
		}
		t.Run(name, func(t *testing.T) {
			g := NewWithT(t)
			f := newRolloutFixture(t, hyperv1.UpgradeTypeReplace)
			c := f.capi
			c.endpointConfigKnown = true
			const endpoint = "private=false,server=https://api.public.example.com:6443"
			c.endpointConfig = endpoint
			c.nodePool.Annotations[nodePoolAnnotationCurrentRolloutConfig] = c.RolloutHashWithoutVersion()
			if !known {
				// Older operators could write this marker without delivering the payload; it is not proof.
				f.md.Annotations = map[string]string{nodePoolAnnotationTargetRolloutConfig: c.RolloutHash()}
				c.mcoRawConfig = "user-config-A + haproxy-B"
			}
			for range 2 {
				g.Expect(f.reconcile(t)).To(BeFalse())
				f.complete(t)
				g.Expect(IsAPIEndpointApplied(c.nodePool, endpoint)).To(Equal(known))
			}
		})
	}
}

func TestIsAPIEndpointApplied(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name        string
		current     string
		target      string
		inProgress  string
		inPlace     bool
		wantApplied bool
	}{
		{name: "When endpoint tracking is missing, it should not authorize cleanup", target: "public"},
		{name: "When the completed endpoint is private, it should not authorize public cleanup", current: "private", target: "public"},
		{name: "When another rollout is in progress, it should not authorize cleanup from stale completion metadata", current: "public", target: "public", inProgress: "new-config"},
		{name: "When the public endpoint rollout has completed, it should authorize cleanup", current: "public", target: "public", wantApplied: true},
		{name: "When an InPlace update completes, it should retain the private endpoint needed by existing kubelets", current: "public", target: "public", inPlace: true},
		{name: "When no endpoint is requested, it should not authorize cleanup"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			g := NewWithT(t)
			np := &hyperv1.NodePool{ObjectMeta: metav1.ObjectMeta{Annotations: map[string]string{
				config.NodePoolAPIEndpointAnnotation:      tt.current,
				nodePoolAnnotationInProgressRolloutConfig: tt.inProgress,
			}}}
			if tt.inPlace {
				np.Spec.Management.UpgradeType = hyperv1.UpgradeTypeInPlace
			}
			g.Expect(IsAPIEndpointApplied(np, tt.target)).To(Equal(tt.wantApplied))
		})
	}
}
