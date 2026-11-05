package hostedcluster

import (
	"context"
	"errors"
	"testing"

	. "github.com/onsi/gomega"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	karpenterv2 "github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/v2/karpenter"
	karpenteroperatorv2 "github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/v2/karpenteroperator"
	"github.com/openshift/hypershift/hypershift-operator/featuregate"
	"github.com/openshift/hypershift/support/api"
	controlplanecomponent "github.com/openshift/hypershift/support/controlplane-component"
	karpenterutil "github.com/openshift/hypershift/support/karpenter"

	configv1 "github.com/openshift/api/config/v1"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/equality"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/utils/ptr"

	crclient "sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"
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

	now := metav1.Now()
	readError := errors.New("injected read failure")
	patchError := errors.New("injected patch failure")
	conflictError := apierrors.NewConflict(schema.GroupResource{Group: hyperv1.GroupVersion.Group, Resource: "hostedcontrolplanes"}, hcName, errors.New("stale resource version"))

	tests := []struct {
		name            string
		hc              *hyperv1.HostedCluster
		objects         []crclient.Object
		expectFinalizer *bool // nil = don't check (HCP doesn't exist), true/false = check
		expectedError   error
		interceptors    interceptor.Funcs
	}{
		{
			name: "When karpenter is disabled and HCP is absent, it should no-op",
			hc: &hyperv1.HostedCluster{
				ObjectMeta: metav1.ObjectMeta{Name: hcName, Namespace: hcNamespace},
				Spec:       hyperv1.HostedClusterSpec{},
			},
		},
		{
			name: "When HCP is not found, it should no-op",
			hc: &hyperv1.HostedCluster{
				ObjectMeta: metav1.ObjectMeta{Name: hcName, Namespace: hcNamespace},
				Spec:       hyperv1.HostedClusterSpec{AutoNode: autoNode},
			},
		},
		{
			name: "When HCP exists without finalizer, it should no-op",
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
			name: "When AWS conditions are missing and KAS is available, it should retain the finalizer",
			hc: &hyperv1.HostedCluster{
				ObjectMeta: metav1.ObjectMeta{Name: hcName, Namespace: hcNamespace},
				Spec:       hyperv1.HostedClusterSpec{AutoNode: autoNode, Platform: hyperv1.PlatformSpec{Type: hyperv1.AWSPlatform}},
			},
			objects: []crclient.Object{
				hcpWithFinalizer(hcName, cpNamespace),
				kasDeployment(cpNamespace, true),
			},
			expectFinalizer: ptr.To(true),
		},
		{
			name: "When karpenter is enabled and KAS deployment is missing, it should remove the finalizer",
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
			name: "When karpenter is enabled and KAS exists but is not available, it should remove the finalizer",
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
			name: "When karpenter is enabled and KAS exists but has no Available condition, it should remove the finalizer",
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
		{
			name: "When karpenter is disabled and HCP has a stale finalizer, it should remove the finalizer immediately",
			hc: &hyperv1.HostedCluster{
				ObjectMeta: metav1.ObjectMeta{Name: hcName, Namespace: hcNamespace},
				Spec:       hyperv1.HostedClusterSpec{},
			},
			objects: []crclient.Object{
				hcpWithFinalizer(hcName, cpNamespace),
			},
			expectFinalizer: ptr.To(false),
		},
		{
			name: "When karpenter is disabled and KAS is still available, it should remove the finalizer immediately",
			hc: &hyperv1.HostedCluster{
				ObjectMeta: metav1.ObjectMeta{Name: hcName, Namespace: hcNamespace},
				Spec:       hyperv1.HostedClusterSpec{},
			},
			objects: []crclient.Object{
				hcpWithFinalizer(hcName, cpNamespace),
				kasDeployment(cpNamespace, true),
			},
			expectFinalizer: ptr.To(false),
		},
		{
			name: "When AWS OIDC is invalid, it should remove the finalizer",
			hc: &hyperv1.HostedCluster{
				ObjectMeta: metav1.ObjectMeta{Name: hcName, Namespace: hcNamespace},
				Spec:       hyperv1.HostedClusterSpec{AutoNode: autoNode, Platform: hyperv1.PlatformSpec{Type: hyperv1.AWSPlatform}},
				Status: hyperv1.HostedClusterStatus{
					Conditions: []metav1.Condition{
						{Type: string(hyperv1.ValidOIDCConfiguration), Status: metav1.ConditionFalse},
						{Type: string(hyperv1.ValidAWSIdentityProvider), Status: metav1.ConditionTrue},
					},
				},
			},
			objects:         []crclient.Object{hcpWithFinalizer(hcName, cpNamespace), kasDeployment(cpNamespace, true)},
			expectFinalizer: ptr.To(false),
		},
		{
			name: "When AWS identity is invalid, it should remove the finalizer",
			hc: &hyperv1.HostedCluster{
				ObjectMeta: metav1.ObjectMeta{Name: hcName, Namespace: hcNamespace},
				Spec:       hyperv1.HostedClusterSpec{AutoNode: autoNode, Platform: hyperv1.PlatformSpec{Type: hyperv1.AWSPlatform}},
				Status: hyperv1.HostedClusterStatus{
					Conditions: []metav1.Condition{
						{Type: string(hyperv1.ValidOIDCConfiguration), Status: metav1.ConditionTrue},
						{Type: string(hyperv1.ValidAWSIdentityProvider), Status: metav1.ConditionFalse},
					},
				},
			},
			objects:         []crclient.Object{hcpWithFinalizer(hcName, cpNamespace), kasDeployment(cpNamespace, true)},
			expectFinalizer: ptr.To(false),
		},
		{
			name: "When both AWS credentials are invalid on a deleting HCP, it should remove the finalizer",
			hc: &hyperv1.HostedCluster{
				ObjectMeta: metav1.ObjectMeta{Name: hcName, Namespace: hcNamespace},
				Spec:       hyperv1.HostedClusterSpec{AutoNode: autoNode, Platform: hyperv1.PlatformSpec{Type: hyperv1.AWSPlatform}},
				Status: hyperv1.HostedClusterStatus{
					Conditions: []metav1.Condition{
						{Type: string(hyperv1.ValidOIDCConfiguration), Status: metav1.ConditionFalse},
						{Type: string(hyperv1.ValidAWSIdentityProvider), Status: metav1.ConditionFalse},
					},
				},
			},
			objects: []crclient.Object{
				&hyperv1.HostedControlPlane{ObjectMeta: metav1.ObjectMeta{
					Name: hcName, Namespace: cpNamespace,
					DeletionTimestamp: &now,
					Finalizers:        []string{karpenterutil.KarpenterFinalizer},
				}},
				kasDeployment(cpNamespace, true),
			},
			expectFinalizer: ptr.To(false),
		},
		{
			name: "When AWS credentials are valid, it should retain the finalizer",
			hc: &hyperv1.HostedCluster{
				ObjectMeta: metav1.ObjectMeta{Name: hcName, Namespace: hcNamespace},
				Spec:       hyperv1.HostedClusterSpec{AutoNode: autoNode, Platform: hyperv1.PlatformSpec{Type: hyperv1.AWSPlatform}},
				Status: hyperv1.HostedClusterStatus{
					Conditions: []metav1.Condition{
						{Type: string(hyperv1.ValidOIDCConfiguration), Status: metav1.ConditionTrue},
						{Type: string(hyperv1.ValidAWSIdentityProvider), Status: metav1.ConditionTrue},
					},
				},
			},
			objects:         []crclient.Object{hcpWithFinalizer(hcName, cpNamespace), kasDeployment(cpNamespace, true)},
			expectFinalizer: ptr.To(true),
		},
		{
			name: "When AWS OIDC is unknown and identity is missing, it should retain the finalizer",
			hc: &hyperv1.HostedCluster{
				ObjectMeta: metav1.ObjectMeta{Name: hcName, Namespace: hcNamespace},
				Spec:       hyperv1.HostedClusterSpec{AutoNode: autoNode, Platform: hyperv1.PlatformSpec{Type: hyperv1.AWSPlatform}},
				Status: hyperv1.HostedClusterStatus{
					Conditions: []metav1.Condition{
						{Type: string(hyperv1.ValidOIDCConfiguration), Status: metav1.ConditionUnknown},
					},
				},
			},
			objects:         []crclient.Object{hcpWithFinalizer(hcName, cpNamespace), kasDeployment(cpNamespace, true)},
			expectFinalizer: ptr.To(true),
		},
		{
			name: "When AWS OIDC is missing and identity is invalid, it should remove the finalizer",
			hc: &hyperv1.HostedCluster{
				ObjectMeta: metav1.ObjectMeta{Name: hcName, Namespace: hcNamespace},
				Spec:       hyperv1.HostedClusterSpec{AutoNode: autoNode, Platform: hyperv1.PlatformSpec{Type: hyperv1.AWSPlatform}},
				Status: hyperv1.HostedClusterStatus{
					Conditions: []metav1.Condition{
						{Type: string(hyperv1.ValidAWSIdentityProvider), Status: metav1.ConditionFalse},
					},
				},
			},
			objects:         []crclient.Object{hcpWithFinalizer(hcName, cpNamespace), kasDeployment(cpNamespace, true)},
			expectFinalizer: ptr.To(false),
		},
		{
			name: "When AWS conditions are invalid on Azure, it should retain the finalizer",
			hc: &hyperv1.HostedCluster{
				ObjectMeta: metav1.ObjectMeta{Name: hcName, Namespace: hcNamespace},
				Spec:       hyperv1.HostedClusterSpec{AutoNode: autoNode, Platform: hyperv1.PlatformSpec{Type: hyperv1.AzurePlatform}},
				Status: hyperv1.HostedClusterStatus{
					Conditions: []metav1.Condition{
						{Type: string(hyperv1.ValidOIDCConfiguration), Status: metav1.ConditionFalse},
						{Type: string(hyperv1.ValidAWSIdentityProvider), Status: metav1.ConditionFalse},
					},
				},
			},
			objects:         []crclient.Object{hcpWithFinalizer(hcName, cpNamespace), kasDeployment(cpNamespace, true)},
			expectFinalizer: ptr.To(true),
		},
		{
			name: "When a deleting HCP has valid AWS credentials, it should retain the finalizer",
			hc: &hyperv1.HostedCluster{
				ObjectMeta: metav1.ObjectMeta{Name: hcName, Namespace: hcNamespace},
				Spec:       hyperv1.HostedClusterSpec{AutoNode: autoNode, Platform: hyperv1.PlatformSpec{Type: hyperv1.AWSPlatform}},
				Status: hyperv1.HostedClusterStatus{
					Conditions: []metav1.Condition{
						{Type: string(hyperv1.ValidOIDCConfiguration), Status: metav1.ConditionTrue},
						{Type: string(hyperv1.ValidAWSIdentityProvider), Status: metav1.ConditionTrue},
					},
				},
			},
			objects: []crclient.Object{
				&hyperv1.HostedControlPlane{ObjectMeta: metav1.ObjectMeta{
					Name: hcName, Namespace: cpNamespace,
					DeletionTimestamp: &now,
					Finalizers:        []string{karpenterutil.KarpenterFinalizer},
				}},
				kasDeployment(cpNamespace, true),
			},
			expectFinalizer: ptr.To(true),
		},
		{
			name: "When reading the HCP fails, it should return the read error",
			hc: &hyperv1.HostedCluster{
				ObjectMeta: metav1.ObjectMeta{Name: hcName, Namespace: hcNamespace},
				Spec:       hyperv1.HostedClusterSpec{AutoNode: autoNode, Platform: hyperv1.PlatformSpec{Type: hyperv1.AWSPlatform}},
			},
			objects:         []crclient.Object{hcpWithFinalizer(hcName, cpNamespace), kasDeployment(cpNamespace, true)},
			expectFinalizer: ptr.To(true),
			expectedError:   readError,
			interceptors: interceptor.Funcs{Get: func(ctx context.Context, delegate crclient.WithWatch, key crclient.ObjectKey, object crclient.Object, opts ...crclient.GetOption) error {
				if _, matches := object.(*hyperv1.HostedControlPlane); matches {
					return readError
				}
				return delegate.Get(ctx, key, object, opts...)
			}},
		},
		{
			name: "When reading KAS fails, it should retain the finalizer and return the read error",
			hc: &hyperv1.HostedCluster{
				ObjectMeta: metav1.ObjectMeta{Name: hcName, Namespace: hcNamespace},
				Spec:       hyperv1.HostedClusterSpec{AutoNode: autoNode, Platform: hyperv1.PlatformSpec{Type: hyperv1.AWSPlatform}},
			},
			objects:         []crclient.Object{hcpWithFinalizer(hcName, cpNamespace), kasDeployment(cpNamespace, true)},
			expectFinalizer: ptr.To(true),
			expectedError:   readError,
			interceptors: interceptor.Funcs{Get: func(ctx context.Context, delegate crclient.WithWatch, key crclient.ObjectKey, object crclient.Object, opts ...crclient.GetOption) error {
				if _, matches := object.(*appsv1.Deployment); matches {
					return readError
				}
				return delegate.Get(ctx, key, object, opts...)
			}},
		},
		{
			name: "When removing an invalid AWS finalizer fails, it should retain it and return the patch error",
			hc: &hyperv1.HostedCluster{
				ObjectMeta: metav1.ObjectMeta{Name: hcName, Namespace: hcNamespace},
				Spec:       hyperv1.HostedClusterSpec{AutoNode: autoNode, Platform: hyperv1.PlatformSpec{Type: hyperv1.AWSPlatform}},
				Status: hyperv1.HostedClusterStatus{
					Conditions: []metav1.Condition{
						{Type: string(hyperv1.ValidOIDCConfiguration), Status: metav1.ConditionFalse},
					},
				},
			},
			objects:         []crclient.Object{hcpWithFinalizer(hcName, cpNamespace), kasDeployment(cpNamespace, true)},
			expectFinalizer: ptr.To(true),
			expectedError:   patchError,
			interceptors: interceptor.Funcs{Patch: func(context.Context, crclient.WithWatch, crclient.Object, crclient.Patch, ...crclient.PatchOption) error {
				return patchError
			}},
		},
		{
			name: "When removing an invalid AWS finalizer conflicts, it should retain it and return the conflict",
			hc: &hyperv1.HostedCluster{
				ObjectMeta: metav1.ObjectMeta{Name: hcName, Namespace: hcNamespace},
				Spec:       hyperv1.HostedClusterSpec{AutoNode: autoNode, Platform: hyperv1.PlatformSpec{Type: hyperv1.AWSPlatform}},
				Status: hyperv1.HostedClusterStatus{
					Conditions: []metav1.Condition{
						{Type: string(hyperv1.ValidAWSIdentityProvider), Status: metav1.ConditionFalse},
					},
				},
			},
			objects:         []crclient.Object{hcpWithFinalizer(hcName, cpNamespace), kasDeployment(cpNamespace, true)},
			expectFinalizer: ptr.To(true),
			expectedError:   conflictError,
			interceptors: interceptor.Funcs{Patch: func(context.Context, crclient.WithWatch, crclient.Object, crclient.Patch, ...crclient.PatchOption) error {
				return conflictError
			}},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			g := NewWithT(t)
			ctx := context.Background()

			for _, object := range tc.objects {
				if hcp, ok := object.(*hyperv1.HostedControlPlane); ok {
					hcp.Finalizers = append(hcp.Finalizers, "test.hypershift.openshift.io/unrelated")
				}
			}
			fakeClient := fake.NewClientBuilder().
				WithScheme(api.Scheme).
				WithObjects(tc.objects...).
				Build()

			r := &HostedClusterReconciler{
				Client: interceptor.NewClient(fakeClient, tc.interceptors),
			}

			for attempt := 0; attempt < 2; attempt++ {
				err := r.resolveKarpenterFinalizer(ctx, tc.hc)
				if tc.expectedError != nil {
					g.Expect(err).To(MatchError(tc.expectedError))
				} else {
					g.Expect(err).ToNot(HaveOccurred())
				}
				if tc.expectFinalizer != nil {
					hcp := &hyperv1.HostedControlPlane{}
					err := fakeClient.Get(ctx, crclient.ObjectKey{Namespace: cpNamespace, Name: hcName}, hcp)
					g.Expect(err).ToNot(HaveOccurred())
					g.Expect(controllerutil.ContainsFinalizer(hcp, karpenterutil.KarpenterFinalizer)).To(Equal(*tc.expectFinalizer),
						"finalizer presence mismatch")
					g.Expect(hcp.Finalizers).To(ContainElement("test.hypershift.openshift.io/unrelated"))
				}
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
			name:     "When deployment is missing, it should return false",
			expected: false,
		},
		{
			name: "When deployment exists with Available=True, it should return true",
			objects: []crclient.Object{
				kasDeployment(cpNamespace, true),
			},
			expected: true,
		},
		{
			name: "When deployment exists with Available=False, it should return false",
			objects: []crclient.Object{
				kasDeployment(cpNamespace, false),
			},
			expected: false,
		},
		{
			name: "When deployment exists with no Available condition, it should return false",
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
		autoNode       hyperv1.AutoNode
		components     []hyperv1.ControlPlaneComponent
		deployments    []appsv1.Deployment
		standalone     bool
		want           metav1.Condition
		wantProgessing bool
	}{
		"When karpenter is enabled and components not yet created, it should report progressing": {
			autoNode:       karpenterEnabledAutoNode,
			components:     nil,
			wantProgessing: true,
			want: metav1.Condition{
				Type:   string(hyperv1.AutoNodeEnabled),
				Status: metav1.ConditionFalse,
				Reason: hyperv1.AutoNodeProgressingReason,
			},
		},
		"When karpenter is enabled and only one component exists, it should report progressing": {
			autoNode: karpenterEnabledAutoNode,
			components: []hyperv1.ControlPlaneComponent{
				{
					ObjectMeta: metav1.ObjectMeta{Name: karpenteroperatorv2.ComponentName, Namespace: hcpNamespace},
					Status:     hyperv1.ControlPlaneComponentStatus{Conditions: []metav1.Condition{rolloutCompleteTrue}},
				},
			},
			wantProgessing: true,
			want: metav1.Condition{
				Type:   string(hyperv1.AutoNodeEnabled),
				Status: metav1.ConditionFalse,
				Reason: hyperv1.AutoNodeProgressingReason,
			},
		},
		"When karpenter is enabled and one component is not rolled out, it should report progressing": {
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
			wantProgessing: true,
			want: metav1.Condition{
				Type:   string(hyperv1.AutoNodeEnabled),
				Status: metav1.ConditionFalse,
				Reason: hyperv1.AutoNodeProgressingReason,
			},
		},
		"When karpenter is enabled and both components are rolled out, it should report ready": {
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
		"When karpenter is disabled and deployments are still present, it should report progressing": {
			autoNode: hyperv1.AutoNode{},
			deployments: []appsv1.Deployment{
				{ObjectMeta: metav1.ObjectMeta{Name: karpenterv2.ComponentName, Namespace: hcpNamespace}},
				{ObjectMeta: metav1.ObjectMeta{Name: karpenteroperatorv2.ComponentName, Namespace: hcpNamespace}},
			},
			wantProgessing: true,
			want: metav1.Condition{
				Type:   string(hyperv1.AutoNodeEnabled),
				Status: metav1.ConditionFalse,
				Reason: hyperv1.AutoNodeProgressingReason,
			},
		},
		"When karpenter is disabled and only the karpenter deployment remains, it should report progressing": {
			autoNode: hyperv1.AutoNode{},
			deployments: []appsv1.Deployment{
				{ObjectMeta: metav1.ObjectMeta{Name: karpenterv2.ComponentName, Namespace: hcpNamespace}},
			},
			wantProgessing: true,
			want: metav1.Condition{
				Type:   string(hyperv1.AutoNodeEnabled),
				Status: metav1.ConditionFalse,
				Reason: hyperv1.AutoNodeProgressingReason,
			},
		},
		"When karpenter is disabled and CPC CRs remain but deployments are gone, it should report not configured": {
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
		"When karpenter is disabled and no deployments are present, it should report not configured": {
			autoNode: hyperv1.AutoNode{},
			want: metav1.Condition{
				Type:   string(hyperv1.AutoNodeEnabled),
				Status: metav1.ConditionFalse,
				Reason: hyperv1.AutoNodeNotConfiguredReason,
			},
		},
		"When standalone karpenter is enabled and its component is rolled out, it should report ready": {
			autoNode: karpenterEnabledAutoNode,
			components: []hyperv1.ControlPlaneComponent{
				{
					ObjectMeta: metav1.ObjectMeta{Name: karpenteroperatorv2.ComponentName, Namespace: hcpNamespace},
					Status:     hyperv1.ControlPlaneComponentStatus{Conditions: []metav1.Condition{rolloutCompleteTrue}},
				},
			},
			standalone: true,
			want: metav1.Condition{
				Type:   string(hyperv1.AutoNodeEnabled),
				Status: metav1.ConditionTrue,
				Reason: hyperv1.AsExpectedReason,
			},
		},
		"When standalone karpenter is enabled and its component is not rolled out, it should report progressing": {
			autoNode: karpenterEnabledAutoNode,
			components: []hyperv1.ControlPlaneComponent{
				{
					ObjectMeta: metav1.ObjectMeta{Name: karpenteroperatorv2.ComponentName, Namespace: hcpNamespace},
					Status:     hyperv1.ControlPlaneComponentStatus{Conditions: []metav1.Condition{rolloutCompleteFalse}},
				},
			},
			standalone:     true,
			wantProgessing: true,
			want: metav1.Condition{
				Type:   string(hyperv1.AutoNodeEnabled),
				Status: metav1.ConditionFalse,
				Reason: hyperv1.AutoNodeProgressingReason,
			},
		},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			if tc.standalone {
				previousFeatureSet := featuregate.FeatureSet()
				featuregate.ConfigureFeatureSet(string(configv1.TechPreviewNoUpgrade))
				t.Cleanup(func() {
					featuregate.ConfigureFeatureSet(string(previousFeatureSet))
				})
				t.Setenv(karpenterutil.EnableStandaloneKarpenterOperatorEnvVar, "1")
			}

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

			got, progressing := r.reconcileAutoNodeEnabledCondition(context.Background(), hcluster, hcpNamespace)
			got.ObservedGeneration = 0
			got.Message = ""
			got.LastTransitionTime = metav1.Time{}

			if !equality.Semantic.DeepEqual(tc.want, got) {
				t.Errorf("condition: expected %+v, got %+v", tc.want, got)
			}
			if progressing != tc.wantProgessing {
				t.Errorf("progressing: expected %v, got %v", tc.wantProgessing, progressing)
			}
		})
	}
}

func TestReconcileKarpenterOperator(t *testing.T) {
	tests := []struct {
		name          string
		staleAutoNode hyperv1.AutoNodeStatus
		wantAutoNode  hyperv1.AutoNodeStatus
	}{
		{
			name:          "When karpenter is disabled and AutoNode status is stale, it should clear the status",
			staleAutoNode: hyperv1.AutoNodeStatus{NodeCount: ptr.To[int32](5)},
			wantAutoNode:  hyperv1.AutoNodeStatus{},
		},
		{
			name:          "When karpenter is disabled and AutoNode status is already empty, it should no-op",
			staleAutoNode: hyperv1.AutoNodeStatus{},
			wantAutoNode:  hyperv1.AutoNodeStatus{},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			g := NewWithT(t)
			ctx := context.Background()

			hcp := &hyperv1.HostedControlPlane{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-hcp",
					Namespace: "test-ns",
				},
			}

			fakeClient := fake.NewClientBuilder().
				WithScheme(api.Scheme).
				WithObjects(hcp).
				WithStatusSubresource(hcp).
				Build()

			// Set stale AutoNode status after creation (WithObjects doesn't persist status).
			hcp.Status.AutoNode = tc.staleAutoNode
			g.Expect(fakeClient.Status().Update(ctx, hcp)).To(Succeed())

			r := &HostedClusterReconciler{
				Client: fakeClient,
			}

			hcluster := &hyperv1.HostedCluster{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-cluster",
					Namespace: "clusters",
				},
				// Spec.AutoNode is zero value — karpenter disabled.
			}

			cpContext := controlplanecomponent.ControlPlaneContext{
				Context: ctx,
				Client:  fakeClient,
				HCP:     hcp,
			}

			err := r.reconcileKarpenterOperator(cpContext, hcluster, "image", "image")
			g.Expect(err).ToNot(HaveOccurred())

			updated := &hyperv1.HostedControlPlane{}
			g.Expect(fakeClient.Get(ctx, crclient.ObjectKeyFromObject(hcp), updated)).To(Succeed())
			g.Expect(updated.Status.AutoNode).To(Equal(tc.wantAutoNode),
				"stale AutoNode status should be cleared when karpenter is disabled")
		})
	}
}
