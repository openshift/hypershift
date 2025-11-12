package gcpprivateserviceconnect

import (
	"testing"

	. "github.com/onsi/gomega"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func TestControllerName(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{
			name:     "private-router service",
			input:    "private-router",
			expected: "private-router-observer",
		},
		{
			name:     "custom service name",
			input:    "my-service",
			expected: "my-service-observer",
		},
		{
			name:     "empty service name",
			input:    "",
			expected: "-observer",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			g := NewGomegaWithT(t)
			result := ControllerName(tt.input)
			g.Expect(result).To(Equal(tt.expected))
		})
	}
}

func TestGetConsumerAcceptList(t *testing.T) {
	tests := []struct {
		name     string
		hcp      *hyperv1.HostedControlPlane
		expected []string
	}{
		{
			name: "valid GCP platform with project",
			hcp: &hyperv1.HostedControlPlane{
				Spec: hyperv1.HostedControlPlaneSpec{
					Platform: hyperv1.PlatformSpec{
						Type: hyperv1.GCPPlatform,
						GCP: &hyperv1.GCPPlatformSpec{
							Project: "my-gcp-project",
						},
					},
				},
			},
			expected: []string{"my-gcp-project"},
		},
		{
			name: "project with numeric project ID",
			hcp: &hyperv1.HostedControlPlane{
				Spec: hyperv1.HostedControlPlaneSpec{
					Platform: hyperv1.PlatformSpec{
						Type: hyperv1.GCPPlatform,
						GCP: &hyperv1.GCPPlatformSpec{
							Project: "123456789012",
						},
					},
				},
			},
			expected: []string{"123456789012"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			g := NewGomegaWithT(t)
			result := getConsumerAcceptList(tt.hcp)
			g.Expect(result).To(Equal(tt.expected))
		})
	}
}

func TestServiceLoadBalancerValidation(t *testing.T) {
	tests := []struct {
		name                 string
		service              *corev1.Service
		expectIsInternalType bool
		expectHasValidIP     bool
		expectLoadBalancerIP string
	}{
		{
			name: "valid Internal LoadBalancer with IP",
			service: &corev1.Service{
				ObjectMeta: metav1.ObjectMeta{
					Name: "private-router",
					Annotations: map[string]string{
						gcpLoadBalancerTypeAnnotation: gcpInternalLoadBalancerType,
					},
				},
				Status: corev1.ServiceStatus{
					LoadBalancer: corev1.LoadBalancerStatus{
						Ingress: []corev1.LoadBalancerIngress{
							{IP: "10.128.15.229"},
						},
					},
				},
			},
			expectIsInternalType: true,
			expectHasValidIP:     true,
			expectLoadBalancerIP: "10.128.15.229",
		},
		{
			name: "External LoadBalancer - should be rejected",
			service: &corev1.Service{
				ObjectMeta: metav1.ObjectMeta{
					Name: "public-router",
					Annotations: map[string]string{
						gcpLoadBalancerTypeAnnotation: "External",
					},
				},
				Status: corev1.ServiceStatus{
					LoadBalancer: corev1.LoadBalancerStatus{
						Ingress: []corev1.LoadBalancerIngress{
							{IP: "35.1.2.3"},
						},
					},
				},
			},
			expectIsInternalType: false,
			expectHasValidIP:     true, // Has IP, but should be rejected due to type
		},
		{
			name: "Internal LoadBalancer without IP yet",
			service: &corev1.Service{
				ObjectMeta: metav1.ObjectMeta{
					Name: "private-router",
					Annotations: map[string]string{
						gcpLoadBalancerTypeAnnotation: gcpInternalLoadBalancerType,
					},
				},
				Status: corev1.ServiceStatus{
					LoadBalancer: corev1.LoadBalancerStatus{
						Ingress: []corev1.LoadBalancerIngress{},
					},
				},
			},
			expectIsInternalType: true,
			expectHasValidIP:     false,
		},
		{
			name: "Internal LoadBalancer with empty IP",
			service: &corev1.Service{
				ObjectMeta: metav1.ObjectMeta{
					Name: "private-router",
					Annotations: map[string]string{
						gcpLoadBalancerTypeAnnotation: gcpInternalLoadBalancerType,
					},
				},
				Status: corev1.ServiceStatus{
					LoadBalancer: corev1.LoadBalancerStatus{
						Ingress: []corev1.LoadBalancerIngress{
							{IP: ""},
						},
					},
				},
			},
			expectIsInternalType: true,
			expectHasValidIP:     false,
		},
		{
			name: "Service without LoadBalancer type annotation",
			service: &corev1.Service{
				ObjectMeta: metav1.ObjectMeta{
					Name: "router",
				},
				Status: corev1.ServiceStatus{
					LoadBalancer: corev1.LoadBalancerStatus{
						Ingress: []corev1.LoadBalancerIngress{
							{IP: "10.128.15.229"},
						},
					},
				},
			},
			expectIsInternalType: false,
			expectHasValidIP:     true, // Has IP, but should be rejected due to missing annotation
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			g := NewGomegaWithT(t)

			// Test LoadBalancer type validation using actual implementation
			isInternalType := isInternalLoadBalancer(tt.service)
			g.Expect(isInternalType).To(Equal(tt.expectIsInternalType))

			// Test IP extraction using actual implementation
			ip, hasValidIP := extractLoadBalancerIP(tt.service)
			g.Expect(hasValidIP).To(Equal(tt.expectHasValidIP))

			if tt.expectLoadBalancerIP != "" {
				g.Expect(ip).To(Equal(tt.expectLoadBalancerIP))
			}
		})
	}
}

func TestServiceOwnerReferenceValidation(t *testing.T) {
	validHCPOwnerRef := metav1.OwnerReference{
		APIVersion: hyperv1.GroupVersion.String(),
		Kind:       "HostedControlPlane",
		Name:       "test-hcp",
		UID:        "test-uid",
	}

	invalidOwnerRef := metav1.OwnerReference{
		APIVersion: "apps/v1",
		Kind:       "Deployment",
		Name:       "some-deployment",
		UID:        "other-uid",
	}

	tests := []struct {
		name           string
		ownerRefs      []metav1.OwnerReference
		expectHCPName  string
		expectHasOwner bool
	}{
		{
			name:           "valid HostedControlPlane owner reference",
			ownerRefs:      []metav1.OwnerReference{validHCPOwnerRef},
			expectHCPName:  "test-hcp",
			expectHasOwner: true,
		},
		{
			name:           "multiple owners with valid HCP",
			ownerRefs:      []metav1.OwnerReference{invalidOwnerRef, validHCPOwnerRef},
			expectHCPName:  "test-hcp",
			expectHasOwner: true,
		},
		{
			name:           "no HostedControlPlane owner reference",
			ownerRefs:      []metav1.OwnerReference{invalidOwnerRef},
			expectHCPName:  "",
			expectHasOwner: false,
		},
		{
			name:           "no owner references",
			ownerRefs:      []metav1.OwnerReference{},
			expectHCPName:  "",
			expectHasOwner: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			g := NewGomegaWithT(t)

			// Simulate the owner reference lookup logic from the controller
			var hcpName string
			for _, ownerRef := range tt.ownerRefs {
				if ownerRef.Kind == "HostedControlPlane" && ownerRef.APIVersion == hyperv1.GroupVersion.String() {
					hcpName = ownerRef.Name
					break
				}
			}

			g.Expect(hcpName).To(Equal(tt.expectHCPName))
			g.Expect(hcpName != "").To(Equal(tt.expectHasOwner))
		})
	}
}