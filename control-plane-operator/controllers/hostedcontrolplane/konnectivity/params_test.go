package konnectivity

import (
	"testing"

	. "github.com/onsi/gomega"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
)

func TestNewKonnectivityServiceParams(t *testing.T) {
	t.Parallel()

	testCases := []struct {
		name     string
		hcp      *hyperv1.HostedControlPlane
		validate func(*testing.T, *KonnectivityServiceParams)
	}{
		{
			name: "When HCP is provided it should create params with owner ref",
			hcp: &hyperv1.HostedControlPlane{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-hcp",
					Namespace: "test-namespace",
					UID:       "test-uid-123",
				},
			},
			validate: func(t *testing.T, params *KonnectivityServiceParams) {
				g := NewWithT(t)
				g.Expect(params).ToNot(BeNil())
				g.Expect(params.OwnerRef).ToNot(BeNil())
				g.Expect(params.OwnerRef.Reference).ToNot(BeNil())
				g.Expect(params.OwnerRef.Reference.Name).To(Equal("test-hcp"))
				g.Expect(params.OwnerRef.Reference.UID).To(Equal(types.UID("test-uid-123")))
			},
		},
		{
			name: "When HCP has empty metadata it should still create params",
			hcp: &hyperv1.HostedControlPlane{
				ObjectMeta: metav1.ObjectMeta{},
			},
			validate: func(t *testing.T, params *KonnectivityServiceParams) {
				g := NewWithT(t)
				g.Expect(params).ToNot(BeNil())
				g.Expect(params.OwnerRef).ToNot(BeNil())
				g.Expect(params.OwnerRef.Reference).ToNot(BeNil())
				g.Expect(params.OwnerRef.Reference.Name).To(BeEmpty())
			},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			params := NewKonnectivityServiceParams(tc.hcp)
			tc.validate(t, params)
		})
	}
}
