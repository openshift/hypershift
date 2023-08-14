package ignitionserver

import (
	"context"
	"reflect"
	"testing"

	. "github.com/onsi/gomega"
	hyperv1 "github.com/openshift/hypershift/api/v1beta1"
	"github.com/openshift/hypershift/hypershift-operator/controllers/manifests/ignitionserver"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/util/intstr"
)

func TestReconcileIgnitionServerServiceNodePortFreshInitialization(t *testing.T) {
	tests := []struct {
		name                           string
		inputIgnitionServerService     *corev1.Service
		inputServicePublishingStrategy *hyperv1.ServicePublishingStrategy
	}{
		{
			name:                       "fresh service initialization",
			inputIgnitionServerService: ignitionserver.Service("default"),
			inputServicePublishingStrategy: &hyperv1.ServicePublishingStrategy{
				Type: hyperv1.NodePort,
			},
		},
		{
			name:                       "fresh service with node port specified",
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
			reconcileIgnitionServerService(test.inputIgnitionServerService, test.inputServicePublishingStrategy)
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
		inputIgnitionServerService     *corev1.Service
		inputServicePublishingStrategy *hyperv1.ServicePublishingStrategy
	}{
		{
			name: "existing service keeps nodeport",
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
			reconcileIgnitionServerService(test.inputIgnitionServerService, test.inputServicePublishingStrategy)
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
		inputIgnitionServerService     *corev1.Service
		inputServicePublishingStrategy *hyperv1.ServicePublishingStrategy
	}{
		{
			name:                       "fresh service initialization",
			inputIgnitionServerService: ignitionserver.Service("default"),
			inputServicePublishingStrategy: &hyperv1.ServicePublishingStrategy{
				Type: hyperv1.Route,
			},
		},
		{
			name: "existing service",
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
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			reconcileIgnitionServerService(test.inputIgnitionServerService, test.inputServicePublishingStrategy)
			g := NewGomegaWithT(t)
			g.Expect(len(test.inputIgnitionServerService.Spec.Ports)).To(Equal(1))
			g.Expect(test.inputIgnitionServerService.Spec.Type).To(Equal(corev1.ServiceTypeClusterIP))
			g.Expect(test.inputIgnitionServerService.Spec.Ports[0].TargetPort).To(Equal(intstr.FromInt(9090)))
			g.Expect(test.inputIgnitionServerService.Spec.Ports[0].Port).To(Equal(int32(443)))
			g.Expect(test.inputIgnitionServerService.Spec.Ports[0].Name).To(Equal("https"))
			g.Expect(test.inputIgnitionServerService.Spec.Ports[0].Protocol).To(Equal(corev1.ProtocolTCP))
		})
	}
}

func TestLookupDisconnectedRegistry(t *testing.T) {
	type args struct {
		ctx          context.Context
		ocpOverrides string
	}
	tests := []struct {
		name string
		args args
		want string
	}{
		{
			name: "given an wellformed openshiftRegistryOverrides string, it should return the mirrorImage pointing to the private registry",
			args: args{
				ctx:          context.TODO(),
				ocpOverrides: "quay.io/jparrill=registry.hypershiftbm.lab:5000/jparrill,quay.io/karmab=registry.hypershiftbm.lab:5000/karmab,quay.io/mavazque=registry.hypershiftbm.lab:5000/mavazque,quay.io/ocp-release=registry.hypershiftbm.lab:5000/openshift/release-images,quay.io/openshift-release-dev/ocp-v4.0-art-dev=registry.hypershiftbm.lab:5000/openshift/release,registry.access.redhat.com/openshift4/ose-oauth-proxy=registry.hypershiftbm.lab:5000/openshift4/ose-oauth-proxy,registry.redhat.io/lvms4=registry.hypershiftbm.lab:5000/lvms4,registry.redhat.io/multicluster-engine=registry.hypershiftbm.lab:5000/acm-d,registry.redhat.io/openshift4=registry.hypershiftbm.lab:5000/openshift4,registry.redhat.io/openshift4=registry.hypershiftbm.lab:5000/openshift4,registry.redhat.io/rhacm2=registry.hypershiftbm.lab:5000/acm-d,registry.redhat.io/rhel8=registry.hypershiftbm.lab:5000/rhel8",
			},
			want: "registry.hypershiftbm.lab:5000/openshift/release",
		},
		{
			name: "given an wellformed openshiftRegistryOverrides string without ocpRelease entry, it should return an empty slice",
			args: args{
				ctx:          context.TODO(),
				ocpOverrides: "quay.io/jparrill=registry.hypershiftbm.lab:5000/jparrill,quay.io/karmab=registry.hypershiftbm.lab:5000/karmab,quay.io/mavazque=registry.hypershiftbm.lab:5000/mavazque,registry.access.redhat.com/openshift4/ose-oauth-proxy=registry.hypershiftbm.lab:5000/openshift4/ose-oauth-proxy,registry.redhat.io/lvms4=registry.hypershiftbm.lab:5000/lvms4,registry.redhat.io/multicluster-engine=registry.hypershiftbm.lab:5000/acm-d,registry.redhat.io/openshift4=registry.hypershiftbm.lab:5000/openshift4,registry.redhat.io/openshift4=registry.hypershiftbm.lab:5000/openshift4,registry.redhat.io/rhacm2=registry.hypershiftbm.lab:5000/acm-d,registry.redhat.io/rhel8=registry.hypershiftbm.lab:5000/rhel8",
			},
			want: "",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := lookupDisconnectedRegistry(tt.args.ctx, tt.args.ocpOverrides); !reflect.DeepEqual(got, tt.want) {
				t.Errorf("lookupDisconnectedRegistry() = %v, want %v", got, tt.want)
			}
		})
	}
}
