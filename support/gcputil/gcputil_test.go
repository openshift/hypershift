package gcputil

import (
	"context"
	"fmt"
	"maps"
	"testing"

	. "github.com/onsi/gomega"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/utils/ptr"

	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

const (
	testImageRegistryGSA = "image-registry@test-project.iam.gserviceaccount.com"
	testProjectNumber    = "123456789012"
	testPoolID           = "test-pool"
	testProviderID       = "test-provider"
)

func TestResourceLabels(t *testing.T) {
	tests := []struct {
		name     string
		hcp      *hyperv1.HostedControlPlane
		expected map[string]string
	}{
		{
			name: "When HCP has GCP resource labels, it should convert them to a map",
			hcp: &hyperv1.HostedControlPlane{Spec: hyperv1.HostedControlPlaneSpec{Platform: hyperv1.PlatformSpec{
				GCP: &hyperv1.GCPPlatformSpec{ResourceLabels: []hyperv1.GCPResourceLabel{
					{Key: "environment", Value: ptr.To("test")},
					{Key: "empty-value"},
				}},
			}}},
			expected: map[string]string{"environment": "test", "empty-value": ""},
		},
		{
			name:     "When HCP has no GCP resource labels, it should return nil",
			hcp:      &hyperv1.HostedControlPlane{},
			expected: nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			NewWithT(t).Expect(ResourceLabels(tt.hcp)).To(Equal(tt.expected))
		})
	}
}

func TestMergeResourceLabels(t *testing.T) {
	tests := []struct {
		name                       string
		existing                   map[string]string
		desired                    map[string]string
		previouslyManagedLabelKeys map[string]struct{}
		expected                   map[string]string
		wantErr                    string
	}{
		{
			name:                       "When desired labels overlap existing labels, it should update only managed values",
			existing:                   map[string]string{"preserved": "value", "managed": "old"},
			desired:                    map[string]string{"managed": "new"},
			previouslyManagedLabelKeys: map[string]struct{}{"managed": {}},
			expected:                   map[string]string{"preserved": "value", "managed": "new"},
		},
		{
			name:                       "When a previously managed label is removed, it should preserve unrelated labels",
			existing:                   map[string]string{"preserved": "value", "removed": "old"},
			desired:                    map[string]string{"managed": "new"},
			previouslyManagedLabelKeys: map[string]struct{}{"removed": {}},
			expected:                   map[string]string{"preserved": "value", "managed": "new"},
		},
		{
			name:     "When existing labels are nil, it should return desired labels",
			desired:  map[string]string{"managed": "new"},
			expected: map[string]string{"managed": "new"},
		},
		{
			name: "When merged labels exceed the GCP limit, it should return an error",
			existing: func() map[string]string {
				labels := make(map[string]string, MaxResourceLabels)
				for i := 0; i < MaxResourceLabels; i++ {
					labels[fmt.Sprintf("label-%d", i)] = "value"
				}
				return labels
			}(),
			desired: map[string]string{"managed": "value"},
			wantErr: "exceed GCP limit",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			labels, err := MergeResourceLabels(tt.existing, tt.desired, tt.previouslyManagedLabelKeys)
			if tt.wantErr != "" {
				NewWithT(t).Expect(err).To(MatchError(ContainSubstring(tt.wantErr)))
				return
			}
			NewWithT(t).Expect(err).ToNot(HaveOccurred())
			NewWithT(t).Expect(maps.Equal(labels, tt.expected)).To(BeTrue())
		})
	}
}

func TestManagedResourceLabelKeys(t *testing.T) {
	keys := ManagedResourceLabelKeys(map[string]string{"example": "second,first,second"}, "example")
	NewWithT(t).Expect(keys).To(Equal(map[string]struct{}{"first": {}, "second": {}}))
}

func TestUpdateManagedResourceLabelKeys(t *testing.T) {
	scheme := runtime.NewScheme()
	NewWithT(t).Expect(hyperv1.AddToScheme(scheme)).To(Succeed())
	hcp := &hyperv1.HostedControlPlane{ObjectMeta: metav1.ObjectMeta{Namespace: "clusters-example", Name: "example"}}
	c := fake.NewClientBuilder().WithScheme(scheme).WithObjects(hcp).Build()

	NewWithT(t).Expect(UpdateManagedResourceLabelKeys(context.Background(), c, hcp, "example", map[string]string{"second": "value", "first": "value"})).To(Succeed())
	updated := &hyperv1.HostedControlPlane{}
	NewWithT(t).Expect(c.Get(context.Background(), client.ObjectKeyFromObject(hcp), updated)).To(Succeed())
	NewWithT(t).Expect(updated.Annotations).To(Equal(map[string]string{"example": "first,second"}))

	NewWithT(t).Expect(UpdateManagedResourceLabelKeys(context.Background(), c, updated, "example", nil)).To(Succeed())
	NewWithT(t).Expect(c.Get(context.Background(), client.ObjectKeyFromObject(hcp), updated)).To(Succeed())
	NewWithT(t).Expect(updated.Annotations).To(BeEmpty())
}

func TestBuildWorkloadIdentityCredentials(t *testing.T) {
	t.Parallel()
	g := NewWithT(t)

	wif := hyperv1.GCPWorkloadIdentityConfig{
		ProjectNumber: testProjectNumber,
		PoolID:        testPoolID,
		ProviderID:    testProviderID,
	}

	credentials, err := BuildWorkloadIdentityCredentials(wif, testImageRegistryGSA)
	g.Expect(err).ToNot(HaveOccurred())
	g.Expect(credentials).To(ContainSubstring(`"type":"external_account"`))
	g.Expect(credentials).To(ContainSubstring(testProjectNumber))
	g.Expect(credentials).To(ContainSubstring(testPoolID))
	g.Expect(credentials).To(ContainSubstring(testProviderID))
	g.Expect(credentials).To(ContainSubstring(testImageRegistryGSA))
	g.Expect(credentials).To(ContainSubstring("/var/run/secrets/openshift/serviceaccount/token"))
}

func TestBuildWorkloadIdentityCredentialsValidation(t *testing.T) {
	t.Parallel()
	validWIF := func() hyperv1.GCPWorkloadIdentityConfig {
		return hyperv1.GCPWorkloadIdentityConfig{
			ProjectNumber: testProjectNumber,
			PoolID:        testPoolID,
			ProviderID:    testProviderID,
		}
	}

	tests := []struct {
		name                string
		mutateWIF           func(*hyperv1.GCPWorkloadIdentityConfig)
		serviceAccountEmail string
		errorMsg            string
	}{
		{
			name:                "When all fields are valid it should succeed",
			serviceAccountEmail: testImageRegistryGSA,
		},
		{
			name:                "When project number is empty it should return an error",
			mutateWIF:           func(wif *hyperv1.GCPWorkloadIdentityConfig) { wif.ProjectNumber = "" },
			serviceAccountEmail: testImageRegistryGSA,
			errorMsg:            "project number cannot be empty",
		},
		{
			name:                "When pool ID is empty it should return an error",
			mutateWIF:           func(wif *hyperv1.GCPWorkloadIdentityConfig) { wif.PoolID = "" },
			serviceAccountEmail: testImageRegistryGSA,
			errorMsg:            "pool ID cannot be empty",
		},
		{
			name:                "When provider ID is empty it should return an error",
			mutateWIF:           func(wif *hyperv1.GCPWorkloadIdentityConfig) { wif.ProviderID = "" },
			serviceAccountEmail: testImageRegistryGSA,
			errorMsg:            "provider ID cannot be empty",
		},
		{
			name:                "When service account email is empty it should return an error",
			serviceAccountEmail: "",
			errorMsg:            "service account email cannot be empty",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			g := NewWithT(t)
			wif := validWIF()
			if tt.mutateWIF != nil {
				tt.mutateWIF(&wif)
			}
			_, err := BuildWorkloadIdentityCredentials(wif, tt.serviceAccountEmail)
			if tt.errorMsg != "" {
				g.Expect(err).To(HaveOccurred())
				g.Expect(err.Error()).To(ContainSubstring(tt.errorMsg))
			} else {
				g.Expect(err).ToNot(HaveOccurred())
			}
		})
	}
}
