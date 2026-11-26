package kubevirt

import (
	"context"
	"errors"
	"testing"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	"github.com/openshift/hypershift/support/upsert"

	rbacv1 "k8s.io/api/rbac/v1"
	"k8s.io/apimachinery/pkg/api/equality"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	runtime "k8s.io/apimachinery/pkg/runtime"

	capikubevirt "sigs.k8s.io/cluster-api-provider-kubevirt/api/v1alpha1"
	capiv1 "sigs.k8s.io/cluster-api/api/v1beta1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"

	"github.com/google/go-cmp/cmp"
)

func TestReconcileCAPIInfraCR(t *testing.T) {
	kubevirt := Kubevirt{}
	fakeClient := fake.NewClientBuilder().Build()
	testNamespace := "testNamespace"
	hcluster := &hyperv1.HostedCluster{
		Spec: hyperv1.HostedClusterSpec{
			InfraID: "testInfraID",
		},
	}
	expectedResult := &capikubevirt.KubevirtCluster{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: testNamespace,
			Name:      hcluster.Spec.InfraID,
			Annotations: map[string]string{
				hostedClusterAnnotation:    client.ObjectKeyFromObject(hcluster).String(),
				capiv1.ManagedByAnnotation: "external",
			},
		},
		Status: capikubevirt.KubevirtClusterStatus{
			Ready: true,
		},
	}
	testCases := []struct {
		name        string
		expectedErr error
	}{
		{
			name: "Happy flow",
		},
		{
			name:        "Expected err from func",
			expectedErr: errors.New("test error"),
		},
	}

	apiendpoint := hyperv1.APIEndpoint{}
	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			fnCallsCount := 0
			createOrUpdateFN := func(
				ctx context.Context,
				c client.Client,
				obj client.Object,
				f controllerutil.MutateFn,
			) (controllerutil.OperationResult, error) {
				fnCallsCount++
				_ = f()
				return "", tc.expectedErr
			}
			result, err := kubevirt.ReconcileCAPIInfraCR(
				t.Context(),
				fakeClient,
				createOrUpdateFN,
				hcluster,
				testNamespace,
				apiendpoint)
			if fnCallsCount != 1 {
				t.Fatalf("Expected the provided function to be called once")
			}
			if tc.expectedErr != nil {
				if err != tc.expectedErr {
					t.Fatalf("ReconcileCAPIInfraCR: Expected to fail. gotErr: %v, expectedErr: %v", err, tc.expectedErr)
				}
			} else if err != nil {
				t.Fatalf("ReconcileCAPIInfraCR: Got unexpected error: %v (expectedErr: %v)", err, tc.expectedErr)
			} else {
				if !equality.Semantic.DeepEqual(expectedResult, result) {
					t.Error(cmp.Diff(expectedResult, result))
				}

			}
		})
	}
}

func TestReconcileCredentials(t *testing.T) {
	kubevirt := Kubevirt{}
	fakeClient := fake.NewClientBuilder().Build()
	fnCallsCount := 0
	createOrUpdateFN := func(ctx context.Context, c client.Client, obj client.Object, f controllerutil.MutateFn) (controllerutil.OperationResult, error) {
		fnCallsCount++
		return "", nil
	}
	hcluster := &hyperv1.HostedCluster{}
	err := kubevirt.ReconcileCredentials(t.Context(), fakeClient, createOrUpdateFN, hcluster, "controlPlanNamespace")
	if err != nil {
		t.Fatalf("ReconcileCredentials failed: %v", err)
	}
	if fnCallsCount > 0 {
		t.Fatalf("create or update func should not be called")
	}
}

func TestReconcileSecretEncryption(t *testing.T) {
	kubevirt := Kubevirt{}
	fakeClient := fake.NewClientBuilder().Build()
	fnCallsCount := 0
	createOrUpdateFN := func(ctx context.Context, c client.Client, obj client.Object, f controllerutil.MutateFn) (controllerutil.OperationResult, error) {
		fnCallsCount++
		return "", nil
	}
	hcluster := &hyperv1.HostedCluster{}
	err := kubevirt.ReconcileSecretEncryption(t.Context(), fakeClient, createOrUpdateFN, hcluster, "controlPlanNamespace")
	if err != nil {
		t.Fatalf("ReconcileSecretEncryption failed: %v", err)
	}
	if fnCallsCount > 0 {
		t.Fatalf("create or update func should not be called")
	}
}

func TestCAPIProviderPolicyRules(t *testing.T) {
	t.Run("When building the platform policy it should include the expected rules", func(t *testing.T) {
		kubevirt := Kubevirt{}

		expectedRules := []rbacv1.PolicyRule{
			{
				APIGroups: []string{""},
				Resources: []string{"services"},
				Verbs:     []string{rbacv1.VerbAll},
			},
			{
				APIGroups: []string{"kubevirt.io"},
				Resources: []string{"virtualmachineinstances", "virtualmachines"},
				Verbs:     []string{rbacv1.VerbAll},
			},
			{
				APIGroups: []string{"kubevirt.io"},
				Resources: []string{"virtualmachineinstancemigrations"},
				Verbs:     []string{"create"},
			},
			{
				APIGroups: []string{"cdi.kubevirt.io"},
				Resources: []string{"datavolumes"},
				Verbs:     []string{"get", "list", "watch"},
			},
		}

		rules := kubevirt.CAPIProviderPolicyRules()
		if !equality.Semantic.DeepEqual(expectedRules, rules) {
			t.Error(cmp.Diff(expectedRules, rules))
		}
	})

	t.Run("When upgrading an existing Role it should add VMIM creation", testCAPIProviderPolicyRulesUpdateExistingRole)
}

func testCAPIProviderPolicyRulesUpdateExistingRole(t *testing.T) {
	scheme := runtime.NewScheme()
	if err := rbacv1.AddToScheme(scheme); err != nil {
		t.Fatalf("failed to add RBAC scheme: %v", err)
	}
	fakeClient := fake.NewClientBuilder().WithScheme(scheme).Build()

	role := func(rules []rbacv1.PolicyRule) *rbacv1.Role {
		return &rbacv1.Role{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "capi-provider",
				Namespace: "test-namespace",
			},
			Rules: rules,
		}
	}

	oldRules := []rbacv1.PolicyRule{
		{
			APIGroups: []string{""},
			Resources: []string{"events", "secrets", "configmaps"},
			Verbs:     []string{rbacv1.VerbAll},
		},
		{
			APIGroups: []string{""},
			Resources: []string{"services"},
			Verbs:     []string{rbacv1.VerbAll},
		},
		{
			APIGroups: []string{"kubevirt.io"},
			Resources: []string{"virtualmachineinstances", "virtualmachines"},
			Verbs:     []string{rbacv1.VerbAll},
		},
		{
			APIGroups: []string{"cdi.kubevirt.io"},
			Resources: []string{"datavolumes"},
			Verbs:     []string{"get", "list", "watch"},
		},
	}

	provider := upsert.NewApplyProvider(false)
	if result, err := provider.ApplyManifest(t.Context(), fakeClient, role(oldRules)); err != nil {
		t.Fatalf("failed to create pre-upgrade Role: %v", err)
	} else if result != controllerutil.OperationResultCreated {
		t.Fatalf("expected pre-upgrade Role creation, got %s", result)
	}

	oldRole := &rbacv1.Role{}
	if err := fakeClient.Get(t.Context(), client.ObjectKeyFromObject(role(nil)), oldRole); err != nil {
		t.Fatalf("failed to get pre-upgrade Role: %v", err)
	}
	oldHash := oldRole.Annotations[upsert.DesiredStateHashAnnotation]
	if oldHash == "" {
		t.Fatal("pre-upgrade Role does not have a desired-state hash")
	}

	newRules := append([]rbacv1.PolicyRule{oldRules[0]}, Kubevirt{}.CAPIProviderPolicyRules()...)
	result, err := provider.ApplyManifest(t.Context(), fakeClient, role(newRules))
	if err != nil {
		t.Fatalf("failed to update Role: %v", err)
	}
	if result != controllerutil.OperationResultUpdated {
		t.Fatalf("expected Role update, got %s", result)
	}

	updatedRole := &rbacv1.Role{}
	if err := fakeClient.Get(t.Context(), client.ObjectKeyFromObject(role(nil)), updatedRole); err != nil {
		t.Fatalf("failed to get updated Role: %v", err)
	}
	newHash := updatedRole.Annotations[upsert.DesiredStateHashAnnotation]
	if newHash == "" {
		t.Fatal("updated Role does not have a desired-state hash")
	}
	if newHash == oldHash {
		t.Fatalf("expected desired-state hash to change during upgrade: old=%s new=%s", oldHash, newHash)
	}

	hasVMIMRule := false
	for _, rule := range updatedRole.Rules {
		if equality.Semantic.DeepEqual(rule.APIGroups, []string{"kubevirt.io"}) &&
			equality.Semantic.DeepEqual(rule.Resources, []string{"virtualmachineinstancemigrations"}) &&
			equality.Semantic.DeepEqual(rule.Verbs, []string{"create"}) {
			hasVMIMRule = true
			break
		}
	}
	if !hasVMIMRule {
		t.Fatalf("updated Role does not grant VMIM creation: %#v", updatedRole.Rules)
	}
}
