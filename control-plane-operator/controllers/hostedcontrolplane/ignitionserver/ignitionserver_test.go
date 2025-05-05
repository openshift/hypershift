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
			reconcileIgnitionServerService(test.inputIgnitionServerService, test.inputServicePublishingStrategy, test.platformType)
			g := NewGomegaWithT(t)
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
			initialNodePort := test.inputIgnitionServerService.Spec.Ports[0].NodePort
			reconcileIgnitionServerService(test.inputIgnitionServerService, test.inputServicePublishingStrategy, test.platformType)
			g := NewGomegaWithT(t)
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
			reconcileIgnitionServerService(test.inputIgnitionServerService, test.inputServicePublishingStrategy, test.platformType)
			g := NewGomegaWithT(t)
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

func TestLookupMappedImage(t *testing.T) {

	ocpOverrides := map[string][]string{
		"quay.io/jparrill":    {"registry.hypershiftbm.lab:5000/jparrill"},
		"quay.io/karmab":      {"registry.hypershiftbm.lab:5000/karmab"},
		"quay.io/mavazque":    {"registry.hypershiftbm.lab:5000/mavazque"},
		"quay.io/ocp-release": {"registry.hypershiftbm.lab:5000/openshift/release-images"},
		"quay.io/openshift-release-dev/ocp-v4.0-art-dev":        {"registry.hypershiftbm.lab:5000/openshift/release"},
		"registry.access.redhat.com/openshift4/ose-oauth-proxy": {"registry.hypershiftbm.lab:5000/openshift4/ose-oauth-proxy"},
		"registry.redhat.io/lvms4":                              {"registry.hypershiftbm.lab:5000/lvms4"},
		"registry.redhat.io/multicluster-engine":                {"registry.hypershiftbm.lab:5000/acm-d"},
		"registry.redhat.io/openshift4":                         {"registry.hypershiftbm.lab:5000/openshift4"},
		"registry.redhat.io/rhacm2":                             {"registry.hypershiftbm.lab:5000/acm-d"},
		"registry.redhat.io/rhel8":                              {"registry.hypershiftbm.lab:5000/rhel8"},
	}

	tests := []struct {
		name   string
		in     string
		expect string
	}{
		{
			name:   "return the mirrorImage if it exists in the ocp overrides",
			in:     "quay.io/openshift-release-dev/ocp-v4.0-art-dev@sha256:34acb17e45b80267919ea6a2f0e2b21e3f87db3c1e2443d2b3ac9afc0455ae07",
			expect: "registry.hypershiftbm.lab:5000/openshift/release@sha256:34acb17e45b80267919ea6a2f0e2b21e3f87db3c1e2443d2b3ac9afc0455ae07",
		},
		{
			name:   "return the same image if not in the overrides",
			in:     "registry.ci.openshift.org/ocp/4.14-2023-09-05-120503@sha256:34acb17e45b80267919ea6a2f0e2b21e3f87db3c1e2443d2b3ac9afc0455ae07",
			expect: "registry.ci.openshift.org/ocp/4.14-2023-09-05-120503@sha256:34acb17e45b80267919ea6a2f0e2b21e3f87db3c1e2443d2b3ac9afc0455ae07",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := lookupMappedImage(ocpOverrides, tt.in)
			if err != nil {
				t.Errorf("Unexpected error: %v", err)
			}
			if got != tt.expect {
				t.Errorf("Wanted: %s, Got: %s", tt.expect, got)
			}
		})
	}
}
