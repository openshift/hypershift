package ignitionserver

import (
	"testing"

	. "github.com/onsi/gomega"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	"github.com/openshift/hypershift/hypershift-operator/controllers/manifests/ignitionserver"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/util/intstr"
)

func TestReconcileIgnitionServerServiceNodePortFreshInitialization(t *testing.T) {
	tests := []struct {
		name                           string
		platformType                   hyperv1.PlatformType
		inputIgnitionServerService     *corev1.Service
		inputServicePublishingStrategy *hyperv1.ServicePublishingStrategy
	}{
		{
			name:                       "fresh service initialization",
			platformType:               hyperv1.AWSPlatform,
			inputIgnitionServerService: ignitionserver.Service("default"),
			inputServicePublishingStrategy: &hyperv1.ServicePublishingStrategy{
				Type: hyperv1.NodePort,
			},
		},
		{
			name:                       "fresh service with node port specified",
			platformType:               hyperv1.AWSPlatform,
			inputIgnitionServerService: ignitionserver.Service("default"),
			inputServicePublishingStrategy: &hyperv1.ServicePublishingStrategy{
				Type: hyperv1.NodePort,
				NodePort: &hyperv1.NodePortPublishingStrategy{
					Port: int32(30000),
				},
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			err := reconcileIgnitionServerService(test.inputIgnitionServerService, test.inputServicePublishingStrategy, test.platformType)
			g := NewGomegaWithT(t)
			g.Expect(err).NotTo(HaveOccurred())
			g.Expect(len(test.inputIgnitionServerService.Spec.Ports)).To(Equal(1))
			g.Expect(test.inputIgnitionServerService.Spec.Type).To(Equal(corev1.ServiceTypeNodePort))
			g.Expect(test.inputIgnitionServerService.Spec.Ports[0].TargetPort).To(Equal(intstr.FromInt(9090)))
			g.Expect(test.inputIgnitionServerService.Spec.Ports[0].Port).To(Equal(int32(443)))
			g.Expect(test.inputIgnitionServerService.Spec.Ports[0].Name).To(Equal("https"))
			g.Expect(test.inputIgnitionServerService.Spec.Ports[0].Protocol).To(Equal(corev1.ProtocolTCP))
			if test.inputServicePublishingStrategy.NodePort != nil && test.inputServicePublishingStrategy.NodePort.Port > 0 {
				g.Expect(test.inputIgnitionServerService.Spec.Ports[0].NodePort).To(Equal(test.inputServicePublishingStrategy.NodePort.Port))
			}
		})
	}
}

func TestReconcileIgnitionServerServiceNodePortExistingService(t *testing.T) {
	tests := []struct {
		name                           string
		platformType                   hyperv1.PlatformType
		inputIgnitionServerService     *corev1.Service
		inputServicePublishingStrategy *hyperv1.ServicePublishingStrategy
	}{
		{
			name:         "existing service keeps nodeport",
			platformType: hyperv1.AWSPlatform,
			inputIgnitionServerService: &corev1.Service{
				ObjectMeta: ignitionserver.Service("default").ObjectMeta,
				Spec: corev1.ServiceSpec{
					Ports: []corev1.ServicePort{
						{
							Name:       "https",
							Port:       443,
							TargetPort: intstr.FromInt(9090),
							Protocol:   corev1.ProtocolTCP,
							NodePort:   int32(30000),
						},
					},
				},
			},
			inputServicePublishingStrategy: &hyperv1.ServicePublishingStrategy{
				Type: hyperv1.NodePort,
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			g := NewGomegaWithT(t)
			initialNodePort := test.inputIgnitionServerService.Spec.Ports[0].NodePort
			err := reconcileIgnitionServerService(test.inputIgnitionServerService, test.inputServicePublishingStrategy, test.platformType)
			g.Expect(err).NotTo(HaveOccurred())
			g.Expect(len(test.inputIgnitionServerService.Spec.Ports)).To(Equal(1))
			g.Expect(test.inputIgnitionServerService.Spec.Type).To(Equal(corev1.ServiceTypeNodePort))
			g.Expect(test.inputIgnitionServerService.Spec.Ports[0].TargetPort).To(Equal(intstr.FromInt(9090)))
			g.Expect(test.inputIgnitionServerService.Spec.Ports[0].Port).To(Equal(int32(443)))
			g.Expect(test.inputIgnitionServerService.Spec.Ports[0].Name).To(Equal("https"))
			g.Expect(test.inputIgnitionServerService.Spec.Ports[0].Protocol).To(Equal(corev1.ProtocolTCP))
			g.Expect(test.inputIgnitionServerService.Spec.Ports[0].NodePort).To(Equal(initialNodePort))
		})
	}
}

func TestReconcileIgnitionServerServiceRoute(t *testing.T) {
	tests := []struct {
		name                           string
		platformType                   hyperv1.PlatformType
		inputIgnitionServerService     *corev1.Service
		inputServicePublishingStrategy *hyperv1.ServicePublishingStrategy
	}{
		{
			name:                       "fresh service initialization",
			platformType:               hyperv1.AWSPlatform,
			inputIgnitionServerService: ignitionserver.Service("default"),
			inputServicePublishingStrategy: &hyperv1.ServicePublishingStrategy{
				Type: hyperv1.Route,
			},
		},
		{
			name:         "existing service",
			platformType: hyperv1.AWSPlatform,
			inputIgnitionServerService: &corev1.Service{
				ObjectMeta: ignitionserver.Service("default").ObjectMeta,
				Spec: corev1.ServiceSpec{
					Ports: []corev1.ServicePort{
						{
							Name:       "https",
							Port:       443,
							TargetPort: intstr.FromInt(9090),
							Protocol:   corev1.ProtocolTCP,
						},
					},
				},
			},
			inputServicePublishingStrategy: &hyperv1.ServicePublishingStrategy{
				Type: hyperv1.Route,
			},
		},
		{
			name:         "existing service, IBM Cloud",
			platformType: hyperv1.AWSPlatform,
			inputIgnitionServerService: &corev1.Service{
				ObjectMeta: ignitionserver.Service("default").ObjectMeta,
				Spec: corev1.ServiceSpec{
					Type: corev1.ServiceTypeNodePort,
					Ports: []corev1.ServicePort{
						{
							Name:       "https",
							Port:       9090,
							TargetPort: intstr.FromInt(9090),
							Protocol:   corev1.ProtocolTCP,
							NodePort:   30000,
						},
					},
				},
			},
			inputServicePublishingStrategy: &hyperv1.ServicePublishingStrategy{
				Type: hyperv1.Route,
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			err := reconcileIgnitionServerService(test.inputIgnitionServerService, test.inputServicePublishingStrategy, test.platformType)
			g := NewGomegaWithT(t)
			g.Expect(err).NotTo(HaveOccurred())
			g.Expect(len(test.inputIgnitionServerService.Spec.Ports)).To(Equal(1))
			g.Expect(test.inputIgnitionServerService.Spec.Type).To(Equal(corev1.ServiceTypeClusterIP))
			g.Expect(test.inputIgnitionServerService.Spec.Ports[0].TargetPort).To(Equal(intstr.FromInt(9090)))
			g.Expect(test.inputIgnitionServerService.Spec.Ports[0].Port).To(Equal(int32(443)))
			g.Expect(test.inputIgnitionServerService.Spec.Ports[0].Name).To(Equal("https"))
			g.Expect(test.inputIgnitionServerService.Spec.Ports[0].Protocol).To(Equal(corev1.ProtocolTCP))
			if test.platformType == hyperv1.IBMCloudPlatform {
				g.Expect(test.inputIgnitionServerService.Spec.Type).To(Equal(corev1.ServiceTypeNodePort))
			}
		})
	}
}
