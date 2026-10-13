package karpenter

import (
	"errors"
	"fmt"
	"testing"

	. "github.com/onsi/gomega"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"

	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

func TestGetHCP(t *testing.T) {
	scheme := runtime.NewScheme()
	_ = hyperv1.AddToScheme(scheme)

	testCases := []struct {
		name          string
		namespace     string
		objects       []client.Object
		expectedError error
		expectedName  string
	}{
		{
			name:      "When HCP exists it should return the HCP",
			namespace: "test-namespace",
			objects: []client.Object{
				&hyperv1.HostedControlPlane{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "test-hcp",
						Namespace: "test-namespace",
					},
				},
			},
			expectedName: "test-hcp",
		},
		{
			name:          "When no HCP exists it should return ErrHCPNotFound",
			namespace:     "test-namespace",
			objects:       []client.Object{},
			expectedError: ErrHCPNotFound,
		},
		{
			name:      "When HCP exists in different namespace it should return ErrHCPNotFound",
			namespace: "test-namespace",
			objects: []client.Object{
				&hyperv1.HostedControlPlane{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "test-hcp",
						Namespace: "other-namespace",
					},
				},
			},
			expectedError: ErrHCPNotFound,
		},
		{
			name:      "When multiple HCPs exist it should return the first one",
			namespace: "test-namespace",
			objects: []client.Object{
				&hyperv1.HostedControlPlane{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "first-hcp",
						Namespace: "test-namespace",
					},
				},
				&hyperv1.HostedControlPlane{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "second-hcp",
						Namespace: "test-namespace",
					},
				},
			},
			expectedName: "first-hcp",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			g := NewWithT(t)

			fakeClient := fake.NewClientBuilder().
				WithScheme(scheme).
				WithObjects(tc.objects...).
				Build()

			hcp, err := GetHCP(t.Context(), fakeClient, tc.namespace)

			if tc.expectedError != nil {
				g.Expect(err).To(HaveOccurred())
				g.Expect(errors.Is(err, tc.expectedError)).To(BeTrue(), "expected error to wrap %v, got %v", tc.expectedError, err)
				g.Expect(hcp).To(BeNil())
			} else {
				g.Expect(err).NotTo(HaveOccurred())
				g.Expect(hcp).NotTo(BeNil())
				g.Expect(hcp.Name).To(Equal(tc.expectedName))
			}
		})
	}
}

func TestSupportedArchitectures(t *testing.T) {
	testCases := []struct {
		name          string
		platform      hyperv1.PlatformType
		expected      []string
		expectedError error
	}{
		{
			name:          "AWS",
			platform:      hyperv1.AWSPlatform,
			expected:      []string{hyperv1.ArchitectureAMD64, hyperv1.ArchitectureARM64},
			expectedError: nil,
		},
		{
			name:          "Azure",
			platform:      hyperv1.AzurePlatform,
			expected:      nil,
			expectedError: fmt.Errorf("unsupported platform: Azure"),
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			g := NewWithT(t)
			architectures, err := SupportedArchitectures(tc.platform)
			if tc.expectedError != nil {
				g.Expect(err).To(HaveOccurred())
				g.Expect(err).To(MatchError(tc.expectedError))
				g.Expect(architectures).To(BeNil())
				return
			}
			g.Expect(err).NotTo(HaveOccurred())
			g.Expect(architectures).To(Equal(tc.expected))
		})
	}
}

func TestArchToAMILabelKey(t *testing.T) {
	testCases := []struct {
		name     string
		arch     string
		expected string
	}{
		{
			name:     "AMD64",
			arch:     hyperv1.ArchitectureAMD64,
			expected: "hypershift.openshift.io/ami",
		},
		{
			name:     "ARM64",
			arch:     hyperv1.ArchitectureARM64,
			expected: "hypershift.openshift.io/ami-arm64",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			g := NewWithT(t)
			g.Expect(ArchToAMILabelKey(tc.arch)).To(Equal(tc.expected))
		})
	}
}
