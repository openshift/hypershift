package hostedcluster

import (
	"context"
	"testing"

	. "github.com/onsi/gomega"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	karpenterv2 "github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/v2/karpenter"
	karpenteroperatorv2 "github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/v2/karpenteroperator"
	"github.com/openshift/hypershift/support/api"
	karpenterutil "github.com/openshift/hypershift/support/karpenter"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/equality"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/utils/ptr"

	crclient "sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
)

var autoNode = hyperv1.AutoNode{
	Provisioner: hyperv1.ProvisionerConfig{
		Name: hyperv1.ProvisionerKarpenter,
		Karpenter: hyperv1.KarpenterConfig{
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

func TestReconcileAutoNodeEnabledCondition(t *testing.T) {
	hcpNamespace := "clusters-test"

	karpenterEnabledAutoNode := hyperv1.AutoNode{
		Provisioner: hyperv1.ProvisionerConfig{
			Name: hyperv1.ProvisionerKarpenter,
			Karpenter: hyperv1.KarpenterConfig{
				Platform: hyperv1.AWSPlatform,
			},
		},
	}

	rolloutCompleteTrue := metav1.Condition{
		Type:   string(hyperv1.ControlPlaneComponentRolloutComplete),
		Status: metav1.ConditionTrue,
		Reason: hyperv1.AsExpectedReason,
	}
	rolloutCompleteFalse := metav1.Condition{
		Type:    string(hyperv1.ControlPlaneComponentRolloutComplete),
		Status:  metav1.ConditionFalse,
		Reason:  "WaitingForRollout",
		Message: "deployment not yet ready",
	}

	tests := map[string]struct {
		autoNode    hyperv1.AutoNode
		components  []hyperv1.ControlPlaneComponent
		deployments []appsv1.Deployment
		want        metav1.Condition
	}{
		"When karpenter is enabled and components not yet created it should report progressing": {
			autoNode:   karpenterEnabledAutoNode,
			components: nil,
			want: metav1.Condition{
				Type:   string(hyperv1.AutoNodeEnabled),
				Status: metav1.ConditionFalse,
				Reason: hyperv1.AutoNodeProgressingReason,
			},
		},
		"When karpenter is enabled and only one component exists it should report progressing": {
			autoNode: karpenterEnabledAutoNode,
			components: []hyperv1.ControlPlaneComponent{
				{
					ObjectMeta: metav1.ObjectMeta{Name: karpenteroperatorv2.ComponentName, Namespace: hcpNamespace},
					Status:     hyperv1.ControlPlaneComponentStatus{Conditions: []metav1.Condition{rolloutCompleteTrue}},
				},
			},
			want: metav1.Condition{
				Type:   string(hyperv1.AutoNodeEnabled),
				Status: metav1.ConditionFalse,
				Reason: hyperv1.AutoNodeProgressingReason,
			},
		},
		"When karpenter is enabled and one component is not rolled out it should report progressing": {
			autoNode: karpenterEnabledAutoNode,
			components: []hyperv1.ControlPlaneComponent{
				{
					ObjectMeta: metav1.ObjectMeta{Name: karpenteroperatorv2.ComponentName, Namespace: hcpNamespace},
					Status:     hyperv1.ControlPlaneComponentStatus{Conditions: []metav1.Condition{rolloutCompleteTrue}},
				},
				{
					ObjectMeta: metav1.ObjectMeta{Name: karpenterv2.ComponentName, Namespace: hcpNamespace},
					Status:     hyperv1.ControlPlaneComponentStatus{Conditions: []metav1.Condition{rolloutCompleteFalse}},
				},
			},
			want: metav1.Condition{
				Type:   string(hyperv1.AutoNodeEnabled),
				Status: metav1.ConditionFalse,
				Reason: hyperv1.AutoNodeProgressingReason,
			},
		},
		"When karpenter is enabled and both components are rolled out it should report ready": {
			autoNode: karpenterEnabledAutoNode,
			components: []hyperv1.ControlPlaneComponent{
				{
					ObjectMeta: metav1.ObjectMeta{Name: karpenteroperatorv2.ComponentName, Namespace: hcpNamespace},
					Status:     hyperv1.ControlPlaneComponentStatus{Conditions: []metav1.Condition{rolloutCompleteTrue}},
				},
				{
					ObjectMeta: metav1.ObjectMeta{Name: karpenterv2.ComponentName, Namespace: hcpNamespace},
					Status:     hyperv1.ControlPlaneComponentStatus{Conditions: []metav1.Condition{rolloutCompleteTrue}},
				},
			},
			want: metav1.Condition{
				Type:   string(hyperv1.AutoNodeEnabled),
				Status: metav1.ConditionTrue,
				Reason: hyperv1.AsExpectedReason,
			},
		},
		"When karpenter is disabled and deployments are still present it should report progressing": {
			autoNode: hyperv1.AutoNode{},
			deployments: []appsv1.Deployment{
				{ObjectMeta: metav1.ObjectMeta{Name: karpenterv2.ComponentName, Namespace: hcpNamespace}},
				{ObjectMeta: metav1.ObjectMeta{Name: karpenteroperatorv2.ComponentName, Namespace: hcpNamespace}},
			},
			want: metav1.Condition{
				Type:   string(hyperv1.AutoNodeEnabled),
				Status: metav1.ConditionFalse,
				Reason: hyperv1.AutoNodeProgressingReason,
			},
		},
		"When karpenter is disabled and only the karpenter deployment remains it should report progressing": {
			autoNode: hyperv1.AutoNode{},
			deployments: []appsv1.Deployment{
				{ObjectMeta: metav1.ObjectMeta{Name: karpenterv2.ComponentName, Namespace: hcpNamespace}},
			},
			want: metav1.Condition{
				Type:   string(hyperv1.AutoNodeEnabled),
				Status: metav1.ConditionFalse,
				Reason: hyperv1.AutoNodeProgressingReason,
			},
		},
		"When karpenter is disabled and CPC CRs remain but deployments are gone it should report not configured": {
			// CPC CRs are deleted before pods terminate; once Deployments are gone teardown is complete.
			autoNode: hyperv1.AutoNode{},
			components: []hyperv1.ControlPlaneComponent{
				{
					ObjectMeta: metav1.ObjectMeta{Name: karpenteroperatorv2.ComponentName, Namespace: hcpNamespace},
					Status:     hyperv1.ControlPlaneComponentStatus{Conditions: []metav1.Condition{rolloutCompleteTrue}},
				},
			},
			deployments: nil,
			want: metav1.Condition{
				Type:   string(hyperv1.AutoNodeEnabled),
				Status: metav1.ConditionFalse,
				Reason: hyperv1.AutoNodeNotConfiguredReason,
			},
		},
		"When karpenter is disabled and no deployments are present it should report not configured": {
			autoNode: hyperv1.AutoNode{},
			want: metav1.Condition{
				Type:   string(hyperv1.AutoNodeEnabled),
				Status: metav1.ConditionFalse,
				Reason: hyperv1.AutoNodeNotConfiguredReason,
			},
		},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			components := make([]hyperv1.ControlPlaneComponent, len(tc.components))
			copy(components, tc.components)

			builder := fake.NewClientBuilder().WithScheme(api.Scheme)
			for i := range components {
				builder = builder.WithStatusSubresource(&components[i])
				builder = builder.WithObjects(&components[i])
			}
			for i := range tc.deployments {
				builder = builder.WithObjects(&tc.deployments[i])
			}
			fakeClient := builder.Build()

			// Patch component status (WithObjects only sets spec; status requires explicit update).
			for i := range components {
				if err := fakeClient.Status().Update(context.Background(), &components[i]); err != nil {
					t.Fatalf("failed to update component status: %v", err)
				}
			}

			r := &HostedClusterReconciler{Client: fakeClient}
			hcluster := &hyperv1.HostedCluster{
				Spec: hyperv1.HostedClusterSpec{
					AutoNode: tc.autoNode,
				},
			}

			got := r.reconcileAutoNodeEnabledCondition(context.Background(), hcluster, hcpNamespace)
			got.ObservedGeneration = 0
			got.Message = ""
			got.LastTransitionTime = metav1.Time{}

			if !equality.Semantic.DeepEqual(tc.want, got) {
				t.Errorf("expected %+v, got %+v", tc.want, got)
			}
		})
	}
}
