package resources

import (
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

func hcpWithGCPLabels(labels ...string) *hyperv1.HostedControlPlane {
	hcp := &hyperv1.HostedControlPlane{}
	if len(labels) == 0 {
		return hcp
	}
	hcp.Spec.Platform.GCP = &hyperv1.GCPPlatformSpec{ResourceLabels: []hyperv1.GCPResourceLabel{{Key: labels[0], Value: ptr.To(labels[1])}}}
	return hcp
}
