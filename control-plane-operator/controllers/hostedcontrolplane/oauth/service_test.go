package oauth

import (
	"fmt"
	"testing"

	. "github.com/onsi/gomega"

	"github.com/openshift/hypershift/api/hypershift/v1beta1"
	"github.com/openshift/hypershift/support/config"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/util/intstr"
)

func TestOauthServiceReconcile(t *testing.T) {
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
						Port:       6443,
						TargetPort: intstr.IntOrString{IntVal: 6443},
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
						Port:       6443,
						TargetPort: intstr.IntOrString{IntVal: 6443},
						NodePort:   1125,
					},
				},
			}},
			svc_out: corev1.Service{Spec: corev1.ServiceSpec{
				Type: corev1.ServiceTypeNodePort,
				Ports: []corev1.ServicePort{
					{
						Protocol:   corev1.ProtocolTCP,
						Port:       6443,
						TargetPort: intstr.IntOrString{IntVal: 6443},
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
						Port:       6443,
						TargetPort: intstr.IntOrString{IntVal: 6443},
					},
				},
			}},
			err: nil,
		},
		{
			name:     "Invalid strategy",
			strategy: v1beta1.ServicePublishingStrategy{Type: v1beta1.LoadBalancer},
			err:      fmt.Errorf("invalid publishing strategy for OAuth service: LoadBalancer"),
		},
	}
	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			err := ReconcileService(&tc.svc_in, config.OwnerRef{}, &tc.strategy, tc.platform)

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
