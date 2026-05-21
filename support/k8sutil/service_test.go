package k8sutil

import (
	"testing"

	. "github.com/onsi/gomega"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func TestExtractLoadBalancerIP(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		svc    *corev1.Service
		wantIP string
		wantOK bool
	}{
		{
			name:   "When service is nil it should return empty and false",
			svc:    nil,
			wantIP: "",
			wantOK: false,
		},
		{
			name: "When service has a valid ingress IP it should return the IP and true",
			svc: &corev1.Service{
				Status: corev1.ServiceStatus{
					LoadBalancer: corev1.LoadBalancerStatus{
						Ingress: []corev1.LoadBalancerIngress{
							{IP: "10.0.0.1"},
						},
					},
				},
			},
			wantIP: "10.0.0.1",
			wantOK: true,
		},
		{
			name: "When service has no ingress it should return empty and false",
			svc: &corev1.Service{
				Status: corev1.ServiceStatus{
					LoadBalancer: corev1.LoadBalancerStatus{
						Ingress: []corev1.LoadBalancerIngress{},
					},
				},
			},
			wantIP: "",
			wantOK: false,
		},
		{
			name: "When service has ingress with empty IP it should return empty and false",
			svc: &corev1.Service{
				Status: corev1.ServiceStatus{
					LoadBalancer: corev1.LoadBalancerStatus{
						Ingress: []corev1.LoadBalancerIngress{
							{IP: ""},
						},
					},
				},
			},
			wantIP: "",
			wantOK: false,
		},
		{
			name: "When service has multiple ingresses it should return the first IP",
			svc: &corev1.Service{
				Status: corev1.ServiceStatus{
					LoadBalancer: corev1.LoadBalancerStatus{
						Ingress: []corev1.LoadBalancerIngress{
							{IP: "10.0.0.1"},
							{IP: "10.0.0.2"},
							{IP: "10.0.0.3"},
						},
					},
				},
			},
			wantIP: "10.0.0.1",
			wantOK: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			g := NewWithT(t)
			gotIP, gotOK := ExtractLoadBalancerIP(tt.svc)
			g.Expect(gotIP).To(Equal(tt.wantIP))
			g.Expect(gotOK).To(Equal(tt.wantOK))
		})
	}
}

func TestExtractHostedControlPlaneOwnerName(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		ownerRefs []metav1.OwnerReference
		want      string
	}{
		{
			name: "When HostedControlPlane owner ref exists it should return the name",
			ownerRefs: []metav1.OwnerReference{
				{
					APIVersion: hyperv1.GroupVersion.String(),
					Kind:       "HostedControlPlane",
					Name:       "my-hcp",
				},
			},
			want: "my-hcp",
		},
		{
			name:      "When no owner refs exist it should return empty string",
			ownerRefs: []metav1.OwnerReference{},
			want:      "",
		},
		{
			name: "When owner refs exist but none is HostedControlPlane it should return empty string",
			ownerRefs: []metav1.OwnerReference{
				{
					APIVersion: "apps/v1",
					Kind:       "Deployment",
					Name:       "my-deployment",
				},
				{
					APIVersion: "v1",
					Kind:       "Service",
					Name:       "my-service",
				},
			},
			want: "",
		},
		{
			name: "When multiple owner refs exist it should return only the HCP one",
			ownerRefs: []metav1.OwnerReference{
				{
					APIVersion: "apps/v1",
					Kind:       "Deployment",
					Name:       "my-deployment",
				},
				{
					APIVersion: hyperv1.GroupVersion.String(),
					Kind:       "HostedControlPlane",
					Name:       "my-hcp",
				},
				{
					APIVersion: "v1",
					Kind:       "ConfigMap",
					Name:       "my-configmap",
				},
			},
			want: "my-hcp",
		},
		{
			name: "When owner ref has wrong APIVersion it should return empty string",
			ownerRefs: []metav1.OwnerReference{
				{
					APIVersion: "wrong.api/v1",
					Kind:       "HostedControlPlane",
					Name:       "my-hcp",
				},
			},
			want: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			g := NewWithT(t)
			g.Expect(ExtractHostedControlPlaneOwnerName(tt.ownerRefs)).To(Equal(tt.want))
		})
	}
}
