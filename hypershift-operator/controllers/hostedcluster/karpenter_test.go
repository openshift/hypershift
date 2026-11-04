package hostedcluster

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
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
	"k8s.io/apimachinery/pkg/types"
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

type karpenterFinalizerTestCase struct {
	name            string
	hc              *hyperv1.HostedCluster
	objects         []crclient.Object
	expectFinalizer *bool
	expectError     bool
	invalidAWS      bool
	hcpReadError    error
	kasReadError    error
	patchError      error
	concurrentWrite bool
}

func TestResolveKarpenterFinalizer(t *testing.T) {
	const (
		hcNamespace = "clusters"
		hcName      = "test-cluster"
		cpNamespace = "clusters-test-cluster" // manifests.HostedControlPlaneNamespace(hcNamespace, hcName)
	)

	tests := []karpenterFinalizerTestCase{
		{
			name: "When karpenter is not enabled and HCP has no finalizer it should no-op",
			hc: &hyperv1.HostedCluster{
				ObjectMeta: metav1.ObjectMeta{Name: hcName, Namespace: hcNamespace},
				Spec:       hyperv1.HostedClusterSpec{},
			},
		},
		{
			name: "When HCP is not found it should no-op",
			hc: &hyperv1.HostedCluster{
				ObjectMeta: metav1.ObjectMeta{Name: hcName, Namespace: hcNamespace},
				Spec:       hyperv1.HostedClusterSpec{AutoNode: autoNode},
			},
		},
		{
			name: "When HCP exists without finalizer it should no-op",
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
			name: "When karpenter is enabled and KAS is available it should retain the finalizer",
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
			name: "When karpenter is enabled and KAS deployment is missing it should remove the finalizer",
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
			name: "When karpenter is enabled and KAS exists but is not available it should remove the finalizer",
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
			name: "When karpenter is enabled and KAS exists but has no Available condition it should remove the finalizer",
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
			name: "When karpenter is disabled and HCP has a stale finalizer it should remove the finalizer immediately",
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
			name: "When karpenter is disabled and KAS is still available it should remove the finalizer immediately",
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
	}

	conditionStatuses := []metav1.ConditionStatus{metav1.ConditionTrue, metav1.ConditionFalse, metav1.ConditionUnknown, ""}
	for _, oidcStatus := range conditionStatuses {
		for _, identityStatus := range conditionStatuses {
			hc := &hyperv1.HostedCluster{
				ObjectMeta: metav1.ObjectMeta{Name: hcName, Namespace: hcNamespace},
				Spec: hyperv1.HostedClusterSpec{
					AutoNode: autoNode,
					Platform: hyperv1.PlatformSpec{Type: hyperv1.AWSPlatform},
				},
			}
			for conditionType, status := range map[string]metav1.ConditionStatus{
				string(hyperv1.ValidOIDCConfiguration):   oidcStatus,
				string(hyperv1.ValidAWSIdentityProvider): identityStatus,
			} {
				if status != "" {
					hc.Status.Conditions = append(hc.Status.Conditions, metav1.Condition{Type: string(conditionType), Status: status})
				}
			}
			invalid := oidcStatus == metav1.ConditionFalse || identityStatus == metav1.ConditionFalse
			tests = append(tests, karpenterFinalizerTestCase{
				name: fmt.Sprintf("When AWS OIDC is %q and identity is %q with available KAS, it should retain the finalizer only without explicit invalidity", oidcStatus, identityStatus),
				hc:   hc, invalidAWS: invalid,
				objects:         []crclient.Object{hcpWithFinalizer(hcName, cpNamespace), kasDeployment(cpNamespace, true)},
				expectFinalizer: ptr.To(!invalid),
			})
		}
	}

	readError := errors.New("injected read failure")
	patchError := errors.New("injected patch failure")
	conflictError := apierrors.NewConflict(schema.GroupResource{Group: hyperv1.GroupVersion.Group, Resource: "hostedcontrolplanes"}, hcName, errors.New("stale resource version"))
	for _, platform := range []hyperv1.PlatformType{hyperv1.AWSPlatform, hyperv1.AzurePlatform, hyperv1.GCPPlatform, hyperv1.NonePlatform} {
		for _, credentialStatus := range []metav1.ConditionStatus{metav1.ConditionTrue, metav1.ConditionFalse, metav1.ConditionUnknown, ""} {
			hc := &hyperv1.HostedCluster{
				ObjectMeta: metav1.ObjectMeta{Name: hcName, Namespace: hcNamespace},
				Spec:       hyperv1.HostedClusterSpec{AutoNode: autoNode, Platform: hyperv1.PlatformSpec{Type: platform}},
			}
			if credentialStatus != "" {
				hc.Status.Conditions = []metav1.Condition{
					{Type: string(hyperv1.ValidOIDCConfiguration), Status: credentialStatus},
					{Type: string(hyperv1.ValidAWSIdentityProvider), Status: credentialStatus},
				}
			}
			invalid := platform == hyperv1.AWSPlatform && credentialStatus == metav1.ConditionFalse
			prefix := fmt.Sprintf("When platform is %s and credentials are %q", platform, credentialStatus)
			tests = append(tests, karpenterFinalizerTestCase{
				name: prefix + " with available KAS, it should retain the finalizer unless explicitly invalid AWS",
				hc:   hc, invalidAWS: invalid,
				objects: []crclient.Object{hcpWithFinalizer(hcName, cpNamespace), kasDeployment(cpNamespace, true)}, expectFinalizer: ptr.To(!invalid),
			})
			for _, availability := range []corev1.ConditionStatus{corev1.ConditionFalse, corev1.ConditionUnknown, "", "missing"} {
				objects := []crclient.Object{hcpWithFinalizer(hcName, cpNamespace)}
				if availability != "missing" {
					deployment := kasDeployment(cpNamespace, false)
					deployment.Status.Conditions[0].Status = availability
					if availability == "" {
						deployment.Status.Conditions = nil
					}
					objects = append(objects, deployment)
				}
				tests = append(tests, karpenterFinalizerTestCase{
					name: fmt.Sprintf("%s and KAS availability is %q, it should remove only the target finalizer", prefix, availability),
					hc:   hc, invalidAWS: invalid, objects: objects, expectFinalizer: ptr.To(false),
				})
			}
			for _, enabled := range []bool{false, true} {
				configuredHC := hc.DeepCopy()
				if !enabled {
					configuredHC.Spec.AutoNode = hyperv1.AutoNode{}
				}
				for _, kasPresent := range []bool{false, true} {
					objects := []crclient.Object{hcpWithFinalizer(hcName, cpNamespace)}
					if kasPresent {
						objects = append(objects, kasDeployment(cpNamespace, true))
					}
					tests = append(tests, karpenterFinalizerTestCase{
						name: fmt.Sprintf("%s with enabled=%t and KAS present=%t and a read failure, it should require KAS only for the normal gate", prefix, enabled, kasPresent),
						hc:   configuredHC, invalidAWS: invalid, objects: objects, kasReadError: readError,
						expectFinalizer: ptr.To(enabled && !invalid), expectError: enabled && !invalid,
					})
				}
			}
			for _, targetPresent := range []bool{false, true} {
				objects := []crclient.Object{}
				var expectFinalizer *bool
				if targetPresent {
					objects = append(objects, &hyperv1.HostedControlPlane{ObjectMeta: metav1.ObjectMeta{Name: hcName, Namespace: cpNamespace}})
					expectFinalizer = ptr.To(false)
				}
				tests = append(tests, karpenterFinalizerTestCase{
					name: fmt.Sprintf("%s with HCP present=%t and no target, it should skip KAS and patch", prefix, targetPresent),
					hc:   hc, invalidAWS: invalid, objects: objects, expectFinalizer: expectFinalizer, kasReadError: readError,
				})
			}
			tests = append(tests, karpenterFinalizerTestCase{
				name: prefix + " and an HCP read error, it should propagate the error without patching",
				hc:   hc, invalidAWS: invalid, objects: []crclient.Object{hcpWithFinalizer(hcName, cpNamespace)},
				hcpReadError: readError, expectError: true, expectFinalizer: ptr.To(true),
			})
		}
	}
	for _, removalPath := range []string{"invalid AWS", "KAS down", "AutoNode disabled"} {
		for _, failure := range []string{"patch error", "conflict", "concurrent write"} {
			hc := &hyperv1.HostedCluster{
				ObjectMeta: metav1.ObjectMeta{Name: hcName, Namespace: hcNamespace},
				Spec:       hyperv1.HostedClusterSpec{AutoNode: autoNode, Platform: hyperv1.PlatformSpec{Type: hyperv1.AWSPlatform}},
			}
			if removalPath == "invalid AWS" {
				hc.Status.Conditions = []metav1.Condition{{Type: string(hyperv1.ValidAWSIdentityProvider), Status: metav1.ConditionFalse}}
			}
			if removalPath == "AutoNode disabled" {
				hc.Spec.AutoNode = hyperv1.AutoNode{}
			}
			injectedError := patchError
			switch failure {
			case "conflict":
				injectedError = conflictError
			case "concurrent write":
				injectedError = nil
			}
			tests = append(tests, karpenterFinalizerTestCase{
				name: fmt.Sprintf("When removing through %s encounters %s, it should preserve stored finalizers and return the error", removalPath, failure),
				hc:   hc, invalidAWS: removalPath == "invalid AWS",
				objects:         []crclient.Object{hcpWithFinalizer(hcName, cpNamespace), kasDeployment(cpNamespace, removalPath != "KAS down")},
				expectFinalizer: ptr.To(true), expectError: true, patchError: injectedError, concurrentWrite: failure == "concurrent write",
			})
		}
	}

	for _, tc := range tests {
		for _, hcpDeleting := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/HCP deleting=%t", tc.name, hcpDeleting), func(t *testing.T) {
				runKarpenterFinalizerCase(t, tc, hcpDeleting)
			})
		}
	}
}

func runKarpenterFinalizerCase(t *testing.T, tc karpenterFinalizerTestCase, hcpDeleting bool) {
	t.Helper()
	const (
		hcName      = "test-cluster"
		cpNamespace = "clusters-test-cluster"
	)
	g := NewWithT(t)
	ctx := context.Background()
	hc := tc.hc.DeepCopy()
	now := metav1.Now()
	hc.DeletionTimestamp = &now
	hc.Finalizers = []string{"test.hypershift.openshift.io/hostedcluster"}
	objects := []crclient.Object{hc}
	var expectedHCP *hyperv1.HostedControlPlane
	for _, object := range tc.objects {
		copyObject := object.DeepCopyObject().(crclient.Object)
		if hcp, ok := copyObject.(*hyperv1.HostedControlPlane); ok {
			hcp.Finalizers = append(hcp.Finalizers, "test.hypershift.openshift.io/unrelated")
			hcp.Annotations = map[string]string{"test.hypershift.openshift.io/preserved": "value"}
			if hcpDeleting {
				hcp.DeletionTimestamp = &now
			}
			expectedHCP = hcp.DeepCopy()
		}
		objects = append(objects, copyObject)
	}
	backingClient := fake.NewClientBuilder().
		WithScheme(api.Scheme).
		WithObjects(objects...).
		Build()
	g.Expect(backingClient.Get(ctx, crclient.ObjectKeyFromObject(hc), hc)).To(Succeed())
	if expectedHCP != nil {
		g.Expect(backingClient.Get(ctx, crclient.ObjectKeyFromObject(expectedHCP), expectedHCP)).To(Succeed())
	}
	expectedHC := hc.DeepCopy()
	kasReads, patches := 0, 0
	testClient := interceptor.NewClient(backingClient, interceptor.Funcs{
		Get: func(ctx context.Context, delegate crclient.WithWatch, key crclient.ObjectKey, object crclient.Object, opts ...crclient.GetOption) error {
			switch object.(type) {
			case *hyperv1.HostedControlPlane:
				if tc.hcpReadError != nil {
					return tc.hcpReadError
				}
			case *appsv1.Deployment:
				kasReads++
				if tc.kasReadError != nil {
					return tc.kasReadError
				}
			}
			return delegate.Get(ctx, key, object, opts...)
		},
		Patch: func(ctx context.Context, delegate crclient.WithWatch, object crclient.Object, patch crclient.Patch, opts ...crclient.PatchOption) error {
			patches++
			g.Expect(object).To(BeAssignableToTypeOf(&hyperv1.HostedControlPlane{}))
			g.Expect(patch.Type()).To(Equal(types.MergePatchType))
			data, err := patch.Data(object)
			g.Expect(err).ToNot(HaveOccurred())
			var patchBody map[string]map[string]json.RawMessage
			g.Expect(json.Unmarshal(data, &patchBody)).To(Succeed())
			g.Expect(patchBody).To(HaveLen(1))
			g.Expect(patchBody["metadata"]).To(HaveLen(2))
			var version string
			g.Expect(json.Unmarshal(patchBody["metadata"]["resourceVersion"], &version)).To(Succeed())
			g.Expect(version).ToNot(BeEmpty())
			current := &hyperv1.HostedControlPlane{}
			g.Expect(delegate.Get(ctx, crclient.ObjectKeyFromObject(object), current)).To(Succeed())
			g.Expect(version).To(Equal(current.ResourceVersion))
			var finalizers []string
			g.Expect(json.Unmarshal(patchBody["metadata"]["finalizers"], &finalizers)).To(Succeed())
			expectedFinalizers := current.DeepCopy()
			controllerutil.RemoveFinalizer(expectedFinalizers, karpenterutil.KarpenterFinalizer)
			g.Expect(finalizers).To(ConsistOf(expectedFinalizers.Finalizers))
			if tc.concurrentWrite {
				controllerutil.AddFinalizer(current, "test.hypershift.openshift.io/concurrent")
				current.Annotations["test.hypershift.openshift.io/concurrent"] = fmt.Sprint(patches)
				g.Expect(delegate.Update(ctx, current)).To(Succeed())
				expectedHCP = current.DeepCopy()
			}
			if tc.patchError != nil {
				return tc.patchError
			}
			return delegate.Patch(ctx, object, patch, opts...)
		},
	})
	r := &HostedClusterReconciler{Client: testClient}
	for attempt := 0; attempt < 3; attempt++ {
		g.Expect(backingClient.Get(ctx, crclient.ObjectKeyFromObject(hc), hc)).To(Succeed())
		needsKAS := expectedHCP != nil && controllerutil.ContainsFinalizer(expectedHCP, karpenterutil.KarpenterFinalizer) && tc.hcpReadError == nil && karpenterutil.IsKarpenterEnabled(hc.Spec.AutoNode) && !tc.invalidAWS
		previousKASReads, previousPatches := kasReads, patches
		err := r.resolveKarpenterFinalizer(ctx, hc)
		assertKarpenterFinalizerError(t, tc, err)
		if needsKAS {
			g.Expect(kasReads - previousKASReads).To(Equal(1))
		} else {
			g.Expect(kasReads).To(Equal(previousKASReads))
		}
		shouldPatch := expectedHCP != nil && controllerutil.ContainsFinalizer(expectedHCP, karpenterutil.KarpenterFinalizer) && tc.hcpReadError == nil && (!needsKAS || tc.kasReadError == nil) && (tc.expectFinalizer != nil && !*tc.expectFinalizer || tc.patchError != nil || tc.concurrentWrite)
		if shouldPatch {
			g.Expect(patches - previousPatches).To(Equal(1))
		} else {
			g.Expect(patches).To(Equal(previousPatches))
		}
		storedHC := &hyperv1.HostedCluster{}
		g.Expect(backingClient.Get(ctx, crclient.ObjectKeyFromObject(hc), storedHC)).To(Succeed())
		g.Expect(storedHC).To(Equal(expectedHC))
		hcp := &hyperv1.HostedControlPlane{}
		err = backingClient.Get(ctx, crclient.ObjectKey{Namespace: cpNamespace, Name: hcName}, hcp)
		if expectedHCP == nil {
			g.Expect(apierrors.IsNotFound(err)).To(BeTrue())
			continue
		}
		g.Expect(err).ToNot(HaveOccurred())
		if tc.expectFinalizer != nil {
			g.Expect(controllerutil.ContainsFinalizer(hcp, karpenterutil.KarpenterFinalizer)).To(Equal(*tc.expectFinalizer))
			if !*tc.expectFinalizer {
				controllerutil.RemoveFinalizer(expectedHCP, karpenterutil.KarpenterFinalizer)
			}
		}
		expectedHCP.ResourceVersion = hcp.ResourceVersion
		g.Expect(hcp).To(Equal(expectedHCP))
	}
}

func assertKarpenterFinalizerError(t *testing.T, tc karpenterFinalizerTestCase, err error) {
	t.Helper()
	g := NewWithT(t)
	if !tc.expectError {
		g.Expect(err).ToNot(HaveOccurred())
		return
	}
	g.Expect(err).To(HaveOccurred())
	if tc.concurrentWrite || apierrors.IsConflict(tc.patchError) {
		g.Expect(apierrors.IsConflict(err)).To(BeTrue())
		return
	}
	expectedError := tc.hcpReadError
	if expectedError == nil {
		expectedError = tc.kasReadError
	}
	if expectedError == nil {
		expectedError = tc.patchError
	}
	g.Expect(errors.Is(err, expectedError)).To(BeTrue())
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
