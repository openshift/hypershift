package machineapprover

import (
	"context"
	"fmt"
	"testing"

	. "github.com/onsi/gomega"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	"github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/manifests"
	component "github.com/openshift/hypershift/support/controlplane-component"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"

	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

func TestIsRequestServing(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		expected bool
	}{
		{
			name:     "When called, it should return false",
			expected: false,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			g := NewWithT(t)

			approver := &machineApprover{}
			result := approver.IsRequestServing()

			g.Expect(result).To(Equal(tc.expected))
		})
	}
}

func TestMultiZoneSpread(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		expected bool
	}{
		{
			name:     "When called, it should return false",
			expected: false,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			g := NewWithT(t)

			approver := &machineApprover{}
			result := approver.MultiZoneSpread()

			g.Expect(result).To(Equal(tc.expected))
		})
	}
}

func TestNeedsManagementKASAccess(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		expected bool
	}{
		{
			name:     "When called, it should return true",
			expected: true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			g := NewWithT(t)

			approver := &machineApprover{}
			result := approver.NeedsManagementKASAccess()

			g.Expect(result).To(Equal(tc.expected))
		})
	}
}

func TestPredicate(t *testing.T) {
	t.Parallel()

	scheme := runtime.NewScheme()
	_ = corev1.AddToScheme(scheme)
	_ = hyperv1.AddToScheme(scheme)

	tests := []struct {
		name          string
		hcp           *hyperv1.HostedControlPlane
		objects       []client.Object
		expectEnabled bool
		expectError   bool
	}{
		{
			name: "When DisableMachineManagement annotation exists, it should return false",
			hcp: &hyperv1.HostedControlPlane{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-hcp",
					Namespace: "test-namespace",
					Annotations: map[string]string{
						hyperv1.DisableMachineManagement: "true",
					},
				},
				Status: hyperv1.HostedControlPlaneStatus{
					KubeConfig: &hyperv1.KubeconfigSecretRef{Name: "kubeconfig", Key: "kubeconfig"},
				},
			},
			expectEnabled: false,
			expectError:   false,
		},
		{
			name: "When KubeConfig is nil, it should return false",
			hcp: &hyperv1.HostedControlPlane{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-hcp",
					Namespace: "test-namespace",
				},
				Status: hyperv1.HostedControlPlaneStatus{
					KubeConfig: nil,
				},
			},
			expectEnabled: false,
			expectError:   false,
		},
		{
			name: "When kubeconfig secret is not found, it should return false",
			hcp: &hyperv1.HostedControlPlane{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-hcp",
					Namespace: "test-namespace",
				},
				Status: hyperv1.HostedControlPlaneStatus{
					KubeConfig: &hyperv1.KubeconfigSecretRef{Name: "kubeconfig", Key: "kubeconfig"},
				},
			},
			objects:       []client.Object{},
			expectEnabled: false,
			expectError:   false,
		},
		{
			name: "When kubeconfig secret exists, it should return true",
			hcp: &hyperv1.HostedControlPlane{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-hcp",
					Namespace: "test-namespace",
				},
				Status: hyperv1.HostedControlPlaneStatus{
					KubeConfig: &hyperv1.KubeconfigSecretRef{Name: "kubeconfig", Key: "kubeconfig"},
				},
			},
			objects: []client.Object{
				&corev1.Secret{
					ObjectMeta: metav1.ObjectMeta{
						Name:      manifests.KASServiceKubeconfigSecret("test-namespace").Name,
						Namespace: "test-namespace",
					},
				},
			},
			expectEnabled: true,
			expectError:   false,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			g := NewWithT(t)

			fakeClient := fake.NewClientBuilder().
				WithScheme(scheme).
				WithObjects(tc.objects...).
				Build()

			cpContext := component.WorkloadContext{
				Context: context.TODO(),
				HCP:     tc.hcp,
				Client:  fakeClient,
			}

			enabled, err := predicate(cpContext)

			if tc.expectError {
				g.Expect(err).To(HaveOccurred())
			} else {
				g.Expect(err).ToNot(HaveOccurred())
			}
			g.Expect(enabled).To(Equal(tc.expectEnabled))
		})
	}
}

func TestPredicateError(t *testing.T) {
	t.Parallel()

	scheme := runtime.NewScheme()
	_ = corev1.AddToScheme(scheme)
	_ = hyperv1.AddToScheme(scheme)

	tests := []struct {
		name   string
		hcp    *hyperv1.HostedControlPlane
		client client.Client
	}{
		{
			name: "When client Get fails with non-NotFound error, it should return error",
			hcp: &hyperv1.HostedControlPlane{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-hcp",
					Namespace: "test-namespace",
				},
				Status: hyperv1.HostedControlPlaneStatus{
					KubeConfig: &hyperv1.KubeconfigSecretRef{Name: "kubeconfig", Key: "kubeconfig"},
				},
			},
			client: &fakeClientWithError{},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			g := NewWithT(t)

			cpContext := component.WorkloadContext{
				Context: context.TODO(),
				HCP:     tc.hcp,
				Client:  tc.client,
			}

			enabled, err := predicate(cpContext)

			g.Expect(err).To(HaveOccurred())
			g.Expect(err.Error()).To(ContainSubstring("failed to get hosted controlplane kubeconfig secret"))
			g.Expect(enabled).To(BeFalse())
		})
	}
}

// fakeClientWithError is a fake client that returns a non-NotFound error
type fakeClientWithError struct {
	client.Client
}

func (f *fakeClientWithError) Get(ctx context.Context, key client.ObjectKey, obj client.Object, opts ...client.GetOption) error {
	return apierrors.NewInternalError(fmt.Errorf("test error"))
}
