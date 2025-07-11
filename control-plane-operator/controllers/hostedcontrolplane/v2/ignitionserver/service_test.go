package ignitionserver

import (
	"fmt"
	"testing"

	. "github.com/onsi/gomega"

	"github.com/openshift/hypershift/api/hypershift/v1beta1"
	component "github.com/openshift/hypershift/support/controlplane-component"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/util/intstr"
)

func TestIgnitionServiceReconcile(t *testing.T) {
	// Define common inputs

	testCases := []struct {
		name     string
		platform v1beta1.PlatformType
		services []v1beta1.ServicePublishingStrategyMapping
		svc_in   corev1.Service
		svc_out  corev1.Service
		err      error
	}{
		{
			name:     "IBM Cloud, NodePort strategy, NodePort service, expected to fill port number from strategy",
			platform: v1beta1.IBMCloudPlatform,
			services: []v1beta1.ServicePublishingStrategyMapping{
				{
					Service: v1beta1.Ignition,
					ServicePublishingStrategy: v1beta1.ServicePublishingStrategy{
						Type:     v1beta1.NodePort,
						NodePort: &v1beta1.NodePortPublishingStrategy{Port: 1125},
					},
				},
			},
			svc_in: corev1.Service{Spec: corev1.ServiceSpec{
				Type: corev1.ServiceTypeNodePort,
				Ports: []corev1.ServicePort{
					{
						Protocol:   corev1.ProtocolTCP,
						Port:       6443,
						TargetPort: intstr.IntOrString{IntVal: 6443},
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
			name:     "IBM Cloud, Route strategy, NodePort service with existing port number, expected not to change",
			platform: v1beta1.IBMCloudPlatform,
			services: []v1beta1.ServicePublishingStrategyMapping{
				{
					Service:                   v1beta1.Ignition,
					ServicePublishingStrategy: v1beta1.ServicePublishingStrategy{Type: v1beta1.Route},
				}},
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
			name:     "Non-IBM Cloud, Route strategy, ClusterIP service, expected to not change",
			platform: v1beta1.AWSPlatform,
			services: []v1beta1.ServicePublishingStrategyMapping{
				{
					Service:                   v1beta1.Ignition,
					ServicePublishingStrategy: v1beta1.ServicePublishingStrategy{Type: v1beta1.Route},
				}},
			svc_in: corev1.Service{Spec: corev1.ServiceSpec{
				Type: corev1.ServiceTypeClusterIP,
				Ports: []corev1.ServicePort{
					{
						Protocol:   corev1.ProtocolTCP,
						Port:       6443,
						TargetPort: intstr.IntOrString{IntVal: 6443},
					},
				},
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
			name:     "IBM Cloud, Invalid strategy",
			platform: v1beta1.IBMCloudPlatform,
			services: []v1beta1.ServicePublishingStrategyMapping{
				{
					Service:                   v1beta1.Ignition,
					ServicePublishingStrategy: v1beta1.ServicePublishingStrategy{Type: v1beta1.LoadBalancer},
				}},
			err: fmt.Errorf("invalid publishing strategy for Ignition service: LoadBalancer"),
		},
		{
			name:     "IBM Cloud, strategy missing",
			platform: v1beta1.IBMCloudPlatform,
			services: []v1beta1.ServicePublishingStrategyMapping{},
			err:      fmt.Errorf("ignition service strategy not specified"),
		},
	}
	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			ctx := component.WorkloadContext{
				HCP: &v1beta1.HostedControlPlane{
					Spec: v1beta1.HostedControlPlaneSpec{
						Platform: v1beta1.PlatformSpec{Type: tc.platform},
						Services: tc.services,
					},
				},
			}
			err := adaptService(ctx, &tc.svc_in)

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
