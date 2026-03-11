package hostedcluster

import (
	"context"
	"testing"

	. "github.com/onsi/gomega"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	"github.com/openshift/hypershift/support/api"
	karpenterutil "github.com/openshift/hypershift/support/karpenter"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/utils/ptr"

	crclient "sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
)

var autoNode = &hyperv1.AutoNode{
	Provisioner: hyperv1.ProvisionerConfig{
		Name: hyperv1.ProvisionerKarpenter,
		Karpenter: &hyperv1.KarpenterConfig{
			Platform: hyperv1.AWSPlatform,
		},
	},
}

func TestResolveKarpenterFinalizer(t *testing.T) {
	const (
		hcNamespace = "clusters"
		hcName      = "test-cluster"
		cpNamespace = "clusters-test-cluster" // manifests.HostedControlPlaneNamespace(hcNamespace, hcName)
	)

	tests := []struct {
		name            string
		hc              *hyperv1.HostedCluster
		objects         []crclient.Object
		expectFinalizer *bool // nil = don't check (HCP doesn't exist), true/false = check
		expectError     bool
	}{
		{
			name: "karpenter not enabled, no-op",
			hc: &hyperv1.HostedCluster{
				ObjectMeta: metav1.ObjectMeta{Name: hcName, Namespace: hcNamespace},
				Spec:       hyperv1.HostedClusterSpec{},
			},
		},
		{
			name: "HCP not found, no-op",
			hc: &hyperv1.HostedCluster{
				ObjectMeta: metav1.ObjectMeta{Name: hcName, Namespace: hcNamespace},
				Spec:       hyperv1.HostedClusterSpec{AutoNode: autoNode},
			},
		},
		{
			name: "HCP exists without finalizer, no-op",
			hc: &hyperv1.HostedCluster{
				ObjectMeta: metav1.ObjectMeta{Name: hcName, Namespace: hcNamespace},
				Spec:       hyperv1.HostedClusterSpec{AutoNode: autoNode},
			},
			objects: []crclient.Object{
				&hyperv1.HostedControlPlane{
					ObjectMeta: metav1.ObjectMeta{Name: hcName, Namespace: cpNamespace},
				},
			},
			expectFinalizer: ptr.To(false),
		},
		{
			name: "KAS available, finalizer retained",
			hc: &hyperv1.HostedCluster{
				ObjectMeta: metav1.ObjectMeta{Name: hcName, Namespace: hcNamespace},
				Spec:       hyperv1.HostedClusterSpec{AutoNode: autoNode},
			},
			objects: []crclient.Object{
				hcpWithFinalizer(hcName, cpNamespace),
				kasDeployment(cpNamespace, true),
			},
			expectFinalizer: ptr.To(true),
		},
		{
			name: "KAS deployment missing, finalizer removed",
			hc: &hyperv1.HostedCluster{
				ObjectMeta: metav1.ObjectMeta{Name: hcName, Namespace: hcNamespace},
				Spec:       hyperv1.HostedClusterSpec{AutoNode: autoNode},
			},
			objects: []crclient.Object{
				hcpWithFinalizer(hcName, cpNamespace),
			},
			expectFinalizer: ptr.To(false),
		},
		{
			name: "KAS exists but not available, finalizer removed",
			hc: &hyperv1.HostedCluster{
				ObjectMeta: metav1.ObjectMeta{Name: hcName, Namespace: hcNamespace},
				Spec:       hyperv1.HostedClusterSpec{AutoNode: autoNode},
			},
			objects: []crclient.Object{
				hcpWithFinalizer(hcName, cpNamespace),
				kasDeployment(cpNamespace, false),
			},
			expectFinalizer: ptr.To(false),
		},
		{
			name: "KAS exists but no Available condition, finalizer removed",
			hc: &hyperv1.HostedCluster{
				ObjectMeta: metav1.ObjectMeta{Name: hcName, Namespace: hcNamespace},
				Spec:       hyperv1.HostedClusterSpec{AutoNode: autoNode},
			},
			objects: []crclient.Object{
				hcpWithFinalizer(hcName, cpNamespace),
				&appsv1.Deployment{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "kube-apiserver",
						Namespace: cpNamespace,
					},
				},
			},
			expectFinalizer: ptr.To(false),
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			g := NewWithT(t)
			ctx := context.Background()

			fakeClient := fake.NewClientBuilder().
				WithScheme(api.Scheme).
				WithObjects(tc.objects...).
				Build()

			r := &HostedClusterReconciler{
				Client: fakeClient,
			}

			err := r.resolveKarpenterFinalizer(ctx, tc.hc)
			if tc.expectError {
				g.Expect(err).To(HaveOccurred())
				return
			}
			g.Expect(err).ToNot(HaveOccurred())

			if tc.expectFinalizer != nil {
				hcp := &hyperv1.HostedControlPlane{}
				err := fakeClient.Get(ctx, crclient.ObjectKey{Namespace: cpNamespace, Name: hcName}, hcp)
				g.Expect(err).ToNot(HaveOccurred())
				g.Expect(controllerutil.ContainsFinalizer(hcp, karpenterutil.KarpenterFinalizer)).To(Equal(*tc.expectFinalizer),
					"finalizer presence mismatch")
			}
		})
	}
}

func TestIsKASAvailable(t *testing.T) {
	const cpNamespace = "test-namespace"

	tests := []struct {
		name        string
		objects     []crclient.Object
		expected    bool
		expectError bool
	}{
		{
			name:     "deployment missing",
			expected: false,
		},
		{
			name: "deployment exists, Available=True",
			objects: []crclient.Object{
				kasDeployment(cpNamespace, true),
			},
			expected: true,
		},
		{
			name: "deployment exists, Available=False",
			objects: []crclient.Object{
				kasDeployment(cpNamespace, false),
			},
			expected: false,
		},
		{
			name: "deployment exists, no Available condition",
			objects: []crclient.Object{
				&appsv1.Deployment{
					ObjectMeta: metav1.ObjectMeta{Name: "kube-apiserver", Namespace: cpNamespace},
				},
			},
			expected: false,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			g := NewWithT(t)

			fakeClient := fake.NewClientBuilder().
				WithScheme(api.Scheme).
				WithObjects(tc.objects...).
				Build()

			available, err := isKASAvailable(context.Background(), cpNamespace, fakeClient)
			if tc.expectError {
				g.Expect(err).To(HaveOccurred())
			} else {
				g.Expect(err).ToNot(HaveOccurred())
				g.Expect(available).To(Equal(tc.expected))
			}
		})
	}
}

func hcpWithFinalizer(name, namespace string) *hyperv1.HostedControlPlane {
	hcp := &hyperv1.HostedControlPlane{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: namespace,
		},
	}
	controllerutil.AddFinalizer(hcp, karpenterutil.KarpenterFinalizer)
	return hcp
}

func kasDeployment(namespace string, available bool) *appsv1.Deployment {
	status := corev1.ConditionFalse
	if available {
		status = corev1.ConditionTrue
	}
	return &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "kube-apiserver",
			Namespace: namespace,
		},
		Status: appsv1.DeploymentStatus{
			Conditions: []appsv1.DeploymentCondition{
				{
					Type:   appsv1.DeploymentAvailable,
					Status: status,
				},
			},
		},
	}
}
