package kas

import (
	"fmt"
	"testing"

	. "github.com/onsi/gomega"

	"github.com/openshift/hypershift/api/hypershift/v1beta1"
	"github.com/openshift/hypershift/support/config"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	corev1 "k8s.io/api/core/v1"
	v1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"
)

func TestReconcileService(t *testing.T) {

	testCases := []struct {
		name          string
		platform      v1beta1.PlatformType
		strategy      v1beta1.ServicePublishingStrategy
		apiServerPort int
		svc_in        corev1.Service
		svc_out       corev1.Service
		err           error
	}{
		{
			name:          "IBM Cloud, NodePort strategy, NodePort service, expected to fill port number from strategy",
			platform:      v1beta1.IBMCloudPlatform,
			strategy:      v1beta1.ServicePublishingStrategy{Type: v1beta1.NodePort, NodePort: &v1beta1.NodePortPublishingStrategy{Port: 31125}},
			apiServerPort: 1125,
			svc_in: corev1.Service{Spec: corev1.ServiceSpec{
				Type: corev1.ServiceTypeNodePort,
				Ports: []corev1.ServicePort{
					{
						Protocol: corev1.ProtocolTCP,
						Port:     1125,
					},
				},
			}},
			svc_out: corev1.Service{Spec: corev1.ServiceSpec{
				Type: corev1.ServiceTypeNodePort,
				Ports: []corev1.ServicePort{
					{
						Protocol:   corev1.ProtocolTCP,
						Port:       1125,
						TargetPort: intstr.IntOrString{Type: intstr.String, StrVal: "client"},
						NodePort:   31125,
					},
				},
			}},
			err: nil,
		},
		{
			name:          "IBM Cloud, Route strategy, NodePort service with existing port number, expected not to change",
			platform:      v1beta1.IBMCloudPlatform,
			strategy:      v1beta1.ServicePublishingStrategy{Type: v1beta1.Route},
			apiServerPort: 1125,
			svc_in: corev1.Service{Spec: corev1.ServiceSpec{
				Type: corev1.ServiceTypeNodePort,
				Ports: []corev1.ServicePort{
					{
						Protocol: corev1.ProtocolTCP,
						Port:     1125,
						NodePort: 1125,
					},
				},
			}},
			svc_out: corev1.Service{Spec: corev1.ServiceSpec{
				Type: corev1.ServiceTypeNodePort,
				Ports: []corev1.ServicePort{
					{
						Protocol:   corev1.ProtocolTCP,
						Port:       1125,
						TargetPort: intstr.IntOrString{Type: intstr.String, StrVal: "client"},
						NodePort:   1125,
					},
				},
			}},
			err: nil,
		},
		{
			name:          "Non-IBM Cloud, Route strategy, ClusterIP service, expected to fill port value only",
			platform:      v1beta1.AWSPlatform,
			strategy:      v1beta1.ServicePublishingStrategy{Type: v1beta1.Route},
			apiServerPort: 1125,
			svc_in: corev1.Service{Spec: corev1.ServiceSpec{
				Type: corev1.ServiceTypeClusterIP,
			}},
			svc_out: corev1.Service{Spec: corev1.ServiceSpec{
				Type: corev1.ServiceTypeClusterIP,
				Ports: []corev1.ServicePort{
					{
						Protocol:   corev1.ProtocolTCP,
						Port:       1125,
						TargetPort: intstr.IntOrString{Type: intstr.String, StrVal: "client"},
					},
				},
			}},
			err: nil,
		},
		{
			name:     "Invalid strategy",
			strategy: v1beta1.ServicePublishingStrategy{Type: v1beta1.None},
			err:      fmt.Errorf("invalid publishing strategy for Kube API server service: None"),
		},
	}
	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			hcp := hyperv1.HostedControlPlane{Spec: hyperv1.HostedControlPlaneSpec{Platform: hyperv1.PlatformSpec{Type: tc.platform}}}

			err := ReconcileService(&tc.svc_in, &tc.strategy, &v1.OwnerReference{}, tc.apiServerPort, []string{}, &hcp)

			g := NewWithT(t)
			if tc.err == nil {
				g.Expect(err).To(BeNil())
				g.Expect(tc.svc_in.Spec.Type).To(Equal(tc.svc_out.Spec.Type))
				g.Expect(tc.svc_in.Spec.Ports).To(Equal(tc.svc_out.Spec.Ports))
			} else {
				g.Expect(tc.err.Error()).To(Equal(err.Error()))
			}
		})
	}
}

func TestKonnectivityServiceReconcile(t *testing.T) {
	// Define common inputs

	testCases := []struct {
		name     string
		platform v1beta1.PlatformType
		strategy v1beta1.ServicePublishingStrategy
		svc_in   corev1.Service
		svc_out  corev1.Service
		err      error
	}{
		{
			name:     "IBM Cloud, NodePort strategy, NodePort service, expected to fill port number from strategy",
			platform: v1beta1.IBMCloudPlatform,
			strategy: v1beta1.ServicePublishingStrategy{Type: v1beta1.NodePort, NodePort: &v1beta1.NodePortPublishingStrategy{Port: 1125}},
			svc_in: corev1.Service{Spec: corev1.ServiceSpec{
				Type: corev1.ServiceTypeNodePort,
			}},
			svc_out: corev1.Service{Spec: corev1.ServiceSpec{
				Type: corev1.ServiceTypeNodePort,
				Ports: []corev1.ServicePort{
					{
						Protocol:   corev1.ProtocolTCP,
						Port:       8091,
						TargetPort: intstr.IntOrString{IntVal: 8091},
						NodePort:   1125,
					},
				},
			}},
			err: nil,
		},
		{
			name:     "IBM Cloud, Route strategy, NodePort service with existing port number, expected not to change",
			platform: v1beta1.IBMCloudPlatform,
			strategy: v1beta1.ServicePublishingStrategy{Type: v1beta1.Route},
			svc_in: corev1.Service{Spec: corev1.ServiceSpec{
				Type: corev1.ServiceTypeNodePort,
				Ports: []corev1.ServicePort{
					{
						Protocol:   corev1.ProtocolTCP,
						Port:       8091,
						TargetPort: intstr.IntOrString{IntVal: 8091},
						NodePort:   1125,
					},
				},
			}},
			svc_out: corev1.Service{Spec: corev1.ServiceSpec{
				Type: corev1.ServiceTypeNodePort,
				Ports: []corev1.ServicePort{
					{
						Protocol:   corev1.ProtocolTCP,
						Port:       8091,
						TargetPort: intstr.IntOrString{IntVal: 8091},
						NodePort:   1125,
					},
				},
			}},
			err: nil,
		},
		{
			name:     "Non-IBM Cloud, Route strategy, ClusterIP service, expected to fill port value only",
			platform: v1beta1.AWSPlatform,
			strategy: v1beta1.ServicePublishingStrategy{Type: v1beta1.Route},
			svc_in: corev1.Service{Spec: corev1.ServiceSpec{
				Type: corev1.ServiceTypeClusterIP,
			}},
			svc_out: corev1.Service{Spec: corev1.ServiceSpec{
				Type: corev1.ServiceTypeClusterIP,
				Ports: []corev1.ServicePort{
					{
						Protocol:   corev1.ProtocolTCP,
						Port:       8091,
						TargetPort: intstr.IntOrString{IntVal: 8091},
					},
				},
			}},
			err: nil,
		},
		{
			name:     "Invalid strategy",
			strategy: v1beta1.ServicePublishingStrategy{Type: v1beta1.None},
			err:      fmt.Errorf("invalid publishing strategy for Konnectivity service: None"),
		},
	}
	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			hcp := hyperv1.HostedControlPlane{Spec: hyperv1.HostedControlPlaneSpec{Platform: hyperv1.PlatformSpec{Type: tc.platform}}}

			err := ReconcileKonnectivityServerService(&tc.svc_in, config.OwnerRef{}, &tc.strategy, &hcp)

			g := NewWithT(t)
			if tc.err == nil {
				g.Expect(err).To(BeNil())
				g.Expect(tc.svc_in.Spec.Type).To(Equal(tc.svc_out.Spec.Type))
				g.Expect(tc.svc_in.Spec.Ports).To(Equal(tc.svc_out.Spec.Ports))
			} else {
				g.Expect(tc.err.Error()).To(Equal(err.Error()))
			}
		})
	}
}
