package resources

import (
	"context"
	"errors"
	"testing"

	. "github.com/onsi/gomega"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	"github.com/openshift/hypershift/control-plane-operator/hostedclusterconfigoperator/api"
	"github.com/openshift/hypershift/support/gcputil"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/utils/ptr"

	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

func TestReconcileGCPLoadBalancerServiceAnnotations(t *testing.T) {
	legacyClass := legacyRegionalExternalLoadBalancerClass
	otherClass := "example.com/other-controller"

	tests := []struct {
		name    string
		service *corev1.Service
		hcp     *hyperv1.HostedControlPlane
		want    map[string]string
	}{
		{
			name: "merges HCP labels into a legacy CCM Service",
			service: &corev1.Service{
				ObjectMeta: metav1.ObjectMeta{Name: "load-balancer", Namespace: "test", Annotations: map[string]string{
					gcputil.LBResourceLabelsAnnotation: "team=payments",
				}},
				Spec: corev1.ServiceSpec{Type: corev1.ServiceTypeLoadBalancer, LoadBalancerClass: &legacyClass},
			},
			hcp: hcpWithGCPLabels("env", "prod"),
			want: map[string]string{
				gcputil.LBResourceLabelsAnnotation:        "env=prod,team=payments",
				gcputil.ManagedLBResourceLabelsAnnotation: "env",
			},
		},
		{
			name: "removes only withdrawn HCP labels",
			service: &corev1.Service{
				ObjectMeta: metav1.ObjectMeta{Name: "load-balancer", Namespace: "test", Annotations: map[string]string{
					gcputil.LBResourceLabelsAnnotation:        "env=prod,team=payments",
					gcputil.ManagedLBResourceLabelsAnnotation: "env",
				}},
				Spec: corev1.ServiceSpec{Type: corev1.ServiceTypeLoadBalancer, LoadBalancerClass: &legacyClass},
			},
			hcp: hcpWithGCPLabels(),
			want: map[string]string{
				gcputil.LBResourceLabelsAnnotation: "team=payments",
			},
		},
		{
			name: "does not manage a Service owned by another controller",
			service: &corev1.Service{
				ObjectMeta: metav1.ObjectMeta{Name: "load-balancer", Namespace: "test"},
				Spec:       corev1.ServiceSpec{Type: corev1.ServiceTypeLoadBalancer, LoadBalancerClass: &otherClass},
			},
			hcp:  hcpWithGCPLabels("env", "prod"),
			want: nil,
		},
		{
			name: "merges multiple labels and resolves an HCP owned key conflict",
			service: &corev1.Service{
				ObjectMeta: metav1.ObjectMeta{Name: "load-balancer", Namespace: "test", Annotations: map[string]string{
					gcputil.LBResourceLabelsAnnotation:        "env=service,team=payments,zone=east",
					gcputil.ManagedLBResourceLabelsAnnotation: "env",
				}},
				Spec: corev1.ServiceSpec{Type: corev1.ServiceTypeLoadBalancer, LoadBalancerClass: &legacyClass},
			},
			hcp: hcpWithGCPResourceLabels(map[string]string{"env": "prod", "region": "us-central1"}),
			want: map[string]string{
				gcputil.LBResourceLabelsAnnotation:        "env=prod,region=us-central1,team=payments,zone=east",
				gcputil.ManagedLBResourceLabelsAnnotation: "env,region",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			g := NewWithT(t)
			guestClient := fake.NewClientBuilder().WithScheme(api.Scheme).WithObjects(tt.service).Build()
			r := &reconciler{client: guestClient}

			g.Expect(r.reconcileGCPLoadBalancerServiceAnnotations(t.Context(), tt.hcp)).To(BeEmpty())

			actual := &corev1.Service{}
			g.Expect(guestClient.Get(t.Context(), client.ObjectKeyFromObject(tt.service), actual)).To(Succeed())
			g.Expect(actual.Annotations).To(Equal(tt.want))
		})
	}
}

func TestIsLegacyGCPLoadBalancerService(t *testing.T) {
	legacyExternal := legacyRegionalExternalLoadBalancerClass
	legacyInternal := legacyRegionalInternalLoadBalancerClass
	otherClass := regionalExternalLoadBalancerClass

	tests := []struct {
		name    string
		service *corev1.Service
		want    bool
	}{
		{name: "default class LoadBalancer", service: &corev1.Service{Spec: corev1.ServiceSpec{Type: corev1.ServiceTypeLoadBalancer}}, want: true},
		{name: "legacy external class", service: &corev1.Service{Spec: corev1.ServiceSpec{Type: corev1.ServiceTypeLoadBalancer, LoadBalancerClass: &legacyExternal}}, want: true},
		{name: "legacy internal class", service: &corev1.Service{Spec: corev1.ServiceSpec{Type: corev1.ServiceTypeLoadBalancer, LoadBalancerClass: &legacyInternal}}, want: true},
		{name: "unrelated class", service: &corev1.Service{Spec: corev1.ServiceSpec{Type: corev1.ServiceTypeLoadBalancer, LoadBalancerClass: &otherClass}}, want: false},
		{name: "RBS annotation", service: &corev1.Service{ObjectMeta: metav1.ObjectMeta{Annotations: map[string]string{l4RBSAnnotation: l4RBSEnabled}}, Spec: corev1.ServiceSpec{Type: corev1.ServiceTypeLoadBalancer}}, want: false},
		{name: "RBS v2 finalizer", service: &corev1.Service{ObjectMeta: metav1.ObjectMeta{Finalizers: []string{netLBFinalizerV2}}, Spec: corev1.ServiceSpec{Type: corev1.ServiceTypeLoadBalancer}}, want: false},
		{name: "RBS v3 finalizer", service: &corev1.Service{ObjectMeta: metav1.ObjectMeta{Finalizers: []string{netLBFinalizerV3}}, Spec: corev1.ServiceSpec{Type: corev1.ServiceTypeLoadBalancer}}, want: false},
		{name: "ClusterIP Service", service: &corev1.Service{Spec: corev1.ServiceSpec{Type: corev1.ServiceTypeClusterIP}}, want: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			NewWithT(t).Expect(isLegacyGCPLoadBalancerService(tt.service)).To(Equal(tt.want))
		})
	}
}

func TestReconcileGCPLoadBalancerServiceAnnotationsErrors(t *testing.T) {
	t.Run("does not modify a Service with a malformed native annotation", func(t *testing.T) {
		g := NewWithT(t)
		legacyClass := legacyRegionalExternalLoadBalancerClass
		service := &corev1.Service{
			ObjectMeta: metav1.ObjectMeta{Name: "load-balancer", Namespace: "test", Annotations: map[string]string{gcputil.LBResourceLabelsAnnotation: "not-a-label"}},
			Spec:       corev1.ServiceSpec{Type: corev1.ServiceTypeLoadBalancer, LoadBalancerClass: &legacyClass},
		}
		guestClient := fake.NewClientBuilder().WithScheme(api.Scheme).WithObjects(service).Build()
		r := &reconciler{client: guestClient}

		g.Expect(r.reconcileGCPLoadBalancerServiceAnnotations(t.Context(), hcpWithGCPLabels("env", "prod"))).To(HaveLen(1))
		actual := &corev1.Service{}
		g.Expect(guestClient.Get(t.Context(), client.ObjectKeyFromObject(service), actual)).To(Succeed())
		g.Expect(actual.Annotations).To(Equal(service.Annotations))
	})

	t.Run("returns a list failure", func(t *testing.T) {
		g := NewWithT(t)
		r := &reconciler{client: &failingServiceClient{Client: fake.NewClientBuilder().WithScheme(api.Scheme).Build(), listErr: errors.New("list failed")}}
		g.Expect(r.reconcileGCPLoadBalancerServiceAnnotations(t.Context(), hcpWithGCPLabels("env", "prod"))).To(HaveLen(1))
	})

	t.Run("returns a patch failure", func(t *testing.T) {
		g := NewWithT(t)
		legacyClass := legacyRegionalExternalLoadBalancerClass
		service := &corev1.Service{ObjectMeta: metav1.ObjectMeta{Name: "load-balancer", Namespace: "test"}, Spec: corev1.ServiceSpec{Type: corev1.ServiceTypeLoadBalancer, LoadBalancerClass: &legacyClass}}
		r := &reconciler{client: &failingServiceClient{Client: fake.NewClientBuilder().WithScheme(api.Scheme).WithObjects(service).Build(), patchErr: errors.New("patch failed")}}
		g.Expect(r.reconcileGCPLoadBalancerServiceAnnotations(t.Context(), hcpWithGCPLabels("env", "prod"))).To(HaveLen(1))
	})
}

type failingServiceClient struct {
	client.Client
	listErr  error
	patchErr error
}

func (c *failingServiceClient) List(ctx context.Context, list client.ObjectList, opts ...client.ListOption) error {
	if c.listErr != nil {
		return c.listErr
	}
	return c.Client.List(ctx, list, opts...)
}

func (c *failingServiceClient) Patch(ctx context.Context, obj client.Object, patch client.Patch, opts ...client.PatchOption) error {
	if c.patchErr != nil {
		return c.patchErr
	}
	return c.Client.Patch(ctx, obj, patch, opts...)
}

func hcpWithGCPLabels(labels ...string) *hyperv1.HostedControlPlane {
	hcp := &hyperv1.HostedControlPlane{}
	if len(labels) == 0 {
		return hcp
	}
	hcp.Spec.Platform.GCP = &hyperv1.GCPPlatformSpec{ResourceLabels: []hyperv1.GCPResourceLabel{{Key: labels[0], Value: ptr.To(labels[1])}}}
	return hcp
}

func hcpWithGCPResourceLabels(labels map[string]string) *hyperv1.HostedControlPlane {
	hcp := &hyperv1.HostedControlPlane{Spec: hyperv1.HostedControlPlaneSpec{Platform: hyperv1.PlatformSpec{GCP: &hyperv1.GCPPlatformSpec{}}}}
	for key, value := range labels {
		hcp.Spec.Platform.GCP.ResourceLabels = append(hcp.Spec.Platform.GCP.ResourceLabels, hyperv1.GCPResourceLabel{Key: key, Value: ptr.To(value)})
	}
	return hcp
}
