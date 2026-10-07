package ignitionserver

import (
	"fmt"
	"testing"

	. "github.com/onsi/gomega"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	"github.com/openshift/hypershift/control-plane-operator/hostedclusterconfigoperator/api"
	component "github.com/openshift/hypershift/support/controlplane-component"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/util/intstr"

	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

func TestIgnitionServiceReconcile(t *testing.T) {
	testCases := []struct {
		name         string
		platform     hyperv1.PlatformType
		services     []hyperv1.ServicePublishingStrategyMapping
		svc_existing *corev1.Service
		svc_out      corev1.Service
		err          error
	}{
		{
			name:     "IBM Cloud, NodePort strategy, NodePort service, expected to fill port number from strategy",
			platform: hyperv1.IBMCloudPlatform,
			services: []hyperv1.ServicePublishingStrategyMapping{
				{
					Service: hyperv1.Ignition,
					ServicePublishingStrategy: hyperv1.ServicePublishingStrategy{
						Type:     hyperv1.NodePort,
						NodePort: &hyperv1.NodePortPublishingStrategy{Port: 1125},
					},
				},
			},
			svc_existing: &corev1.Service{
				Spec: corev1.ServiceSpec{
					Type: corev1.ServiceTypeNodePort,
					Ports: []corev1.ServicePort{
						{},
					},
				},
			},
			svc_out: corev1.Service{Spec: corev1.ServiceSpec{
				Type: corev1.ServiceTypeNodePort,
				Ports: []corev1.ServicePort{
					{
						Name:       "https",
						Protocol:   corev1.ProtocolTCP,
						Port:       443,
						TargetPort: intstr.IntOrString{IntVal: 9090},
						NodePort:   1125,
					},
				},
			}},
			err: nil,
		},
		{
			name:     "IBM Cloud, Route strategy, NodePort service with existing port number, expected not to change",
			platform: hyperv1.IBMCloudPlatform,
			services: []hyperv1.ServicePublishingStrategyMapping{
				{
					Service:                   hyperv1.Ignition,
					ServicePublishingStrategy: hyperv1.ServicePublishingStrategy{Type: hyperv1.Route},
				}},
			svc_existing: &corev1.Service{
				Spec: corev1.ServiceSpec{
					Type: corev1.ServiceTypeNodePort,
					Ports: []corev1.ServicePort{
						{
							NodePort: 1125,
						},
					},
				},
			},
			svc_out: corev1.Service{Spec: corev1.ServiceSpec{
				Type: corev1.ServiceTypeNodePort,
				Ports: []corev1.ServicePort{
					{
						Name:       "https",
						Protocol:   corev1.ProtocolTCP,
						Port:       443,
						TargetPort: intstr.IntOrString{IntVal: 9090},
						NodePort:   1125,
					},
				},
			}},
			err: nil,
		},
		{
			name:     "Non-IBM Cloud, Route strategy, ClusterIP service, expected to not change",
			platform: hyperv1.AWSPlatform,
			services: []hyperv1.ServicePublishingStrategyMapping{
				{
					Service:                   hyperv1.Ignition,
					ServicePublishingStrategy: hyperv1.ServicePublishingStrategy{Type: hyperv1.Route},
				}},
			svc_out: corev1.Service{Spec: corev1.ServiceSpec{
				Type: corev1.ServiceTypeClusterIP,
				Ports: []corev1.ServicePort{
					{
						Name:       "https",
						Protocol:   corev1.ProtocolTCP,
						Port:       443,
						TargetPort: intstr.IntOrString{IntVal: 9090},
					},
				},
			}},
			err: nil,
		},
		{
			name:     "IBM Cloud, Invalid strategy",
			platform: hyperv1.IBMCloudPlatform,
			services: []hyperv1.ServicePublishingStrategyMapping{
				{
					Service:                   hyperv1.Ignition,
					ServicePublishingStrategy: hyperv1.ServicePublishingStrategy{Type: hyperv1.LoadBalancer},
				}},
			err: fmt.Errorf("invalid publishing strategy for Ignition service: LoadBalancer"),
		},
		{
			name:     "IBM Cloud, strategy missing",
			platform: hyperv1.IBMCloudPlatform,
			services: []hyperv1.ServicePublishingStrategyMapping{},
			err:      fmt.Errorf("ignition service strategy not specified"),
		},
	}
	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {

			clientBuilder := fake.NewClientBuilder().WithScheme(api.Scheme)
			if tc.svc_existing != nil {
				clientBuilder = clientBuilder.WithObjects(tc.svc_existing)
			}
			client := clientBuilder.Build()

			ctx := component.WorkloadContext{
				HCP: &hyperv1.HostedControlPlane{
					Spec: hyperv1.HostedControlPlaneSpec{
						Platform: hyperv1.PlatformSpec{Type: tc.platform},
						Services: tc.services,
					},
				},
				Client: client,
			}
			svc_out := corev1.Service{
				Spec: corev1.ServiceSpec{
					Type: corev1.ServiceTypeClusterIP,
					Ports: []corev1.ServicePort{
						{
							Name:       "https",
							Protocol:   corev1.ProtocolTCP,
							Port:       443,
							TargetPort: intstr.IntOrString{IntVal: 9090},
						},
					},
					Selector: map[string]string{"app": "ignition-server"},
				}}

			err := adaptService(ctx, &svc_out)

			g := NewWithT(t)
			if tc.err == nil {
				g.Expect(err).To(BeNil())
				g.Expect(svc_out.Spec.Type).To(Equal(tc.svc_out.Spec.Type))
				g.Expect(svc_out.Spec.Ports).To(Equal(tc.svc_out.Spec.Ports))
			} else {
				g.Expect(tc.err.Error()).To(Equal(err.Error()))
			}
		})
	}
}
