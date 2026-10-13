package oapi

import (
	"testing"

	. "github.com/onsi/gomega"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	"github.com/openshift/hypershift/support/config"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"
)

func testOwnerRef() config.OwnerRef {
	return config.OwnerRef{
		Reference: &metav1.OwnerReference{
			APIVersion: hyperv1.GroupVersion.String(),
			Kind:       "HostedControlPlane",
			Name:       "test-hcp",
			UID:        "test-uid",
		},
	}
}

func TestReconcileOpenShiftAPIService(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name             string
		svc              *corev1.Service
		expectTargetPort intstr.IntOrString
		expectPort       int32
		expectType       corev1.ServiceType
		expectSelector   map[string]string
		expectLabels     map[string]string
		presetSelector   bool
	}{
		{
			name:             "When service has no existing ports, it should create a port with target port 8443",
			svc:              &corev1.Service{},
			expectTargetPort: intstr.FromInt(OpenShiftAPIServerPort),
			expectPort:       int32(OpenShiftServicePort),
			expectType:       corev1.ServiceTypeClusterIP,
			expectSelector:   openshiftAPIServerLabels(),
			expectLabels:     openshiftAPIServerLabels(),
		},
		{
			name: "When called, it should set the service type to ClusterIP",
			svc: &corev1.Service{
				Spec: corev1.ServiceSpec{
					Type: corev1.ServiceTypeNodePort,
				},
			},
			expectTargetPort: intstr.FromInt(OpenShiftAPIServerPort),
			expectPort:       int32(OpenShiftServicePort),
			expectType:       corev1.ServiceTypeClusterIP,
			expectSelector:   openshiftAPIServerLabels(),
			expectLabels:     openshiftAPIServerLabels(),
		},
		{
			name:             "When called, it should set the service port to 443",
			svc:              &corev1.Service{},
			expectTargetPort: intstr.FromInt(OpenShiftAPIServerPort),
			expectPort:       443,
			expectType:       corev1.ServiceTypeClusterIP,
			expectSelector:   openshiftAPIServerLabels(),
			expectLabels:     openshiftAPIServerLabels(),
		},
		{
			name:             "When service has no selector, it should set the openshift-apiserver selector",
			svc:              &corev1.Service{},
			expectTargetPort: intstr.FromInt(OpenShiftAPIServerPort),
			expectPort:       int32(OpenShiftServicePort),
			expectType:       corev1.ServiceTypeClusterIP,
			expectSelector:   openshiftAPIServerLabels(),
			expectLabels:     openshiftAPIServerLabels(),
		},
		{
			name: "When service already has a selector, it should preserve the existing selector",
			svc: &corev1.Service{
				Spec: corev1.ServiceSpec{
					Selector: map[string]string{"custom": "selector"},
				},
			},
			expectTargetPort: intstr.FromInt(OpenShiftAPIServerPort),
			expectPort:       int32(OpenShiftServicePort),
			expectType:       corev1.ServiceTypeClusterIP,
			expectSelector:   map[string]string{"custom": "selector"},
			expectLabels:     openshiftAPIServerLabels(),
			presetSelector:   true,
		},
		{
			name:             "When called, it should set labels to openshift-apiserver labels",
			svc:              &corev1.Service{},
			expectTargetPort: intstr.FromInt(OpenShiftAPIServerPort),
			expectPort:       int32(OpenShiftServicePort),
			expectType:       corev1.ServiceTypeClusterIP,
			expectSelector:   openshiftAPIServerLabels(),
			expectLabels:     openshiftAPIServerLabels(),
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			g := NewWithT(t)
			ownerRef := testOwnerRef()

			err := ReconcileOpenShiftAPIService(tt.svc, ownerRef)
			g.Expect(err).ToNot(HaveOccurred())
			g.Expect(tt.svc.Spec.Type).To(Equal(tt.expectType))
			g.Expect(tt.svc.Labels).To(Equal(tt.expectLabels))
			g.Expect(tt.svc.Spec.Selector).To(Equal(tt.expectSelector))
			g.Expect(tt.svc.Spec.Ports).To(HaveLen(1))
			g.Expect(tt.svc.Spec.Ports[0].Name).To(Equal("https"))
			g.Expect(tt.svc.Spec.Ports[0].Port).To(Equal(tt.expectPort))
			g.Expect(tt.svc.Spec.Ports[0].Protocol).To(Equal(corev1.ProtocolTCP))
			g.Expect(tt.svc.Spec.Ports[0].TargetPort).To(Equal(tt.expectTargetPort))
		})
	}
}

func TestReconcileOAuthAPIService(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name             string
		svc              *corev1.Service
		expectSelector   map[string]string
		expectTargetPort intstr.IntOrString
	}{
		{
			name:             "When service has no selector, it should set the oauth-apiserver selector",
			svc:              &corev1.Service{},
			expectSelector:   oauthAPIServerLabels,
			expectTargetPort: intstr.FromInt(OpenShiftAPIServerPort),
		},
		{
			name:             "When called, it should set target port to 8443",
			svc:              &corev1.Service{},
			expectSelector:   oauthAPIServerLabels,
			expectTargetPort: intstr.FromInt(8443),
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			g := NewWithT(t)
			ownerRef := testOwnerRef()

			err := ReconcileOAuthAPIService(tt.svc, ownerRef)
			g.Expect(err).ToNot(HaveOccurred())
			g.Expect(tt.svc.Spec.Selector).To(Equal(tt.expectSelector))
			g.Expect(tt.svc.Spec.Ports).To(HaveLen(1))
			g.Expect(tt.svc.Spec.Ports[0].TargetPort).To(Equal(tt.expectTargetPort))
			g.Expect(tt.svc.Spec.Ports[0].Port).To(Equal(int32(OpenShiftServicePort)))
			g.Expect(tt.svc.Labels).To(Equal(openshiftAPIServerLabels()))
		})
	}
}

func TestReconcileOLMPackageServerService(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name             string
		svc              *corev1.Service
		expectSelector   map[string]string
		expectTargetPort intstr.IntOrString
	}{
		{
			name:             "When service has no selector, it should set the packageserver selector",
			svc:              &corev1.Service{},
			expectSelector:   olmPackageServerLabels,
			expectTargetPort: intstr.FromInt(OLMPackageServerPort),
		},
		{
			name:             "When called, it should set target port to 5443",
			svc:              &corev1.Service{},
			expectSelector:   olmPackageServerLabels,
			expectTargetPort: intstr.FromInt(5443),
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			g := NewWithT(t)
			ownerRef := testOwnerRef()

			err := ReconcileOLMPackageServerService(tt.svc, ownerRef)
			g.Expect(err).ToNot(HaveOccurred())
			g.Expect(tt.svc.Spec.Selector).To(Equal(tt.expectSelector))
			g.Expect(tt.svc.Spec.Ports).To(HaveLen(1))
			g.Expect(tt.svc.Spec.Ports[0].TargetPort).To(Equal(tt.expectTargetPort))
			g.Expect(tt.svc.Spec.Ports[0].Port).To(Equal(int32(OpenShiftServicePort)))
			g.Expect(tt.svc.Labels).To(Equal(openshiftAPIServerLabels()))
		})
	}
}
