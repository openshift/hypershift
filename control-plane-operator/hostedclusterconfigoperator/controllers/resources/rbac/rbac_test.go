package rbac

import (
	"context"
	"fmt"
	"testing"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	"github.com/openshift/hypershift/support/upsert"

	rbacv1 "k8s.io/api/rbac/v1"

	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
)

var _ manifestReconciler = manifestAndReconcile[*rbacv1.ClusterRole]{}

func collectingCreateOrUpdate(keys *[]string) upsert.CreateOrUpdateFN {
	return func(ctx context.Context, c client.Client, obj client.Object, f controllerutil.MutateFn) (controllerutil.OperationResult, error) {
		if err := f(); err != nil {
			return controllerutil.OperationResultNone, err
		}
		key := fmt.Sprintf("%s/%s", obj.GetObjectKind().GroupVersionKind().Kind, obj.GetName())
		if ns := obj.GetNamespace(); ns != "" {
			key = fmt.Sprintf("%s/%s/%s", obj.GetObjectKind().GroupVersionKind().Kind, ns, obj.GetName())
		}
		*keys = append(*keys, key)
		return controllerutil.OperationResultCreated, nil
	}
}

func failingCreateOrUpdate(failKey string) upsert.CreateOrUpdateFN {
	return func(ctx context.Context, c client.Client, obj client.Object, f controllerutil.MutateFn) (controllerutil.OperationResult, error) {
		if err := f(); err != nil {
			return controllerutil.OperationResultNone, err
		}
		if obj.GetName() == failKey {
			return controllerutil.OperationResultNone, fmt.Errorf("injected failure for %s", failKey)
		}
		return controllerutil.OperationResultCreated, nil
	}
}

func TestIngressCapabilityFiltering(t *testing.T) {
	t.Parallel()

	ingressKeys := map[string]bool{
		"ClusterRole/system:openshift:openshift-controller-manager:ingress-to-route-controller":        true,
		"Role/openshift-route-controller-manager/openshift-route-controllers":                          true,
		"ClusterRoleBinding/system:openshift:openshift-controller-manager:ingress-to-route-controller": true,
		"RoleBinding/openshift-route-controller-manager/openshift-route-controllers":                   true,
	}

	tests := []struct {
		name         string
		capabilities *hyperv1.Capabilities
		wantIngress  bool
	}{
		{
			name:         "nil capabilities includes ingress resources",
			capabilities: nil,
			wantIngress:  true,
		},
		{
			name:         "empty disabled list includes ingress resources",
			capabilities: &hyperv1.Capabilities{},
			wantIngress:  true,
		},
		{
			name: "ingress disabled skips ingress resources",
			capabilities: &hyperv1.Capabilities{
				Disabled: []hyperv1.OptionalCapability{hyperv1.IngressCapability},
			},
			wantIngress: false,
		},
		{
			name: "unrelated capability disabled still includes ingress resources",
			capabilities: &hyperv1.Capabilities{
				Disabled: []hyperv1.OptionalCapability{hyperv1.NodeTuningCapability},
			},
			wantIngress: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			var keys []string
			facts := PolicyFacts{Capabilities: tt.capabilities}
			err := Reconcile(t.Context(), nil, collectingCreateOrUpdate(&keys), facts)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}

			for _, k := range keys {
				if ingressKeys[k] && !tt.wantIngress {
					t.Errorf("ingress resource %q was reconciled but should have been skipped", k)
				}
			}
			if tt.wantIngress {
				found := 0
				for _, k := range keys {
					if ingressKeys[k] {
						found++
					}
				}
				if found != len(ingressKeys) {
					t.Errorf("expected %d ingress resources, found %d", len(ingressKeys), found)
				}
			}
		})
	}
}

func TestAROSelection(t *testing.T) {
	t.Parallel()

	aroOnlyKeys := map[string]bool{
		"ClusterRole/system:openshift:openshift-cluster-csi-drivers:azure-disk-csi-driver-node-sa":                           true,
		"ClusterRoleBinding/system:openshift:openshift-cluster-csi-drivers:azure-disk-csi-driver-node-sa":                    true,
		"ClusterRole/system:serviceaccount:openshift-cluster-csi-drivers:azure-file-csi-driver-node-sa":                      true,
		"ClusterRoleBinding/system:serviceaccount:openshift-cluster-csi-drivers:azure-file-csi-driver-node-sa":               true,
		"ClusterRole/system:serviceaccount:openshift-cloud-network-config-controller:cloud-network-config-controller":        true,
		"ClusterRoleBinding/system:serviceaccount:openshift-cloud-network-config-controller:cloud-network-config-controller": true,
	}

	tests := []struct {
		name     string
		isAroHCP bool
		wantARO  bool
	}{
		{
			name:     "non-ARO excludes Azure RBAC",
			isAroHCP: false,
			wantARO:  false,
		},
		{
			name:     "ARO includes Azure RBAC",
			isAroHCP: true,
			wantARO:  true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			var keys []string
			facts := PolicyFacts{IsAroHCP: tt.isAroHCP}
			err := Reconcile(t.Context(), nil, collectingCreateOrUpdate(&keys), facts)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}

			for _, k := range keys {
				if aroOnlyKeys[k] && !tt.wantARO {
					t.Errorf("ARO resource %q was reconciled but should not have been", k)
				}
			}
			if tt.wantARO {
				found := 0
				for _, k := range keys {
					if aroOnlyKeys[k] {
						found++
					}
				}
				if found != len(aroOnlyKeys) {
					t.Errorf("expected %d ARO resources, found %d", len(aroOnlyKeys), found)
				}
			}
		})
	}
}

func TestCatalogOrder(t *testing.T) {
	t.Parallel()
	var keys []string
	facts := PolicyFacts{}
	err := Reconcile(t.Context(), nil, collectingCreateOrUpdate(&keys), facts)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(keys) == 0 {
		t.Fatal("expected non-empty catalog")
	}

	expectedFirst := "ClusterRole/system:openshift:controller:cluster-csr-approver-controller"
	if keys[0] != expectedFirst {
		t.Errorf("expected first key %q, got %q", expectedFirst, keys[0])
	}

	expectedLast := "RoleBinding/openshift-config-managed/kas-connection-checker"
	if keys[len(keys)-1] != expectedLast {
		t.Errorf("expected last key %q, got %q", expectedLast, keys[len(keys)-1])
	}
}

func TestAggregateErrors(t *testing.T) {
	t.Parallel()

	failOn := "system:openshift:controller:cluster-csr-approver-controller"
	facts := PolicyFacts{}
	err := Reconcile(t.Context(), nil, failingCreateOrUpdate(failOn), facts)
	if err == nil {
		t.Fatal("expected aggregate error, got nil")
	}

	var keys []string
	_ = Reconcile(t.Context(), nil, collectingCreateOrUpdate(&keys), facts)
	totalExpected := len(keys)

	var successKeys []string
	countingFail := func(ctx context.Context, c client.Client, obj client.Object, f controllerutil.MutateFn) (controllerutil.OperationResult, error) {
		if err := f(); err != nil {
			return controllerutil.OperationResultNone, err
		}
		if obj.GetName() == failOn {
			return controllerutil.OperationResultNone, fmt.Errorf("injected failure for %s", failOn)
		}
		key := fmt.Sprintf("%s/%s", obj.GetObjectKind().GroupVersionKind().Kind, obj.GetName())
		successKeys = append(successKeys, key)
		return controllerutil.OperationResultCreated, nil
	}

	_ = Reconcile(t.Context(), nil, countingFail, facts)

	if len(successKeys) != totalExpected-1 {
		t.Errorf("expected %d successful reconciliations (all except the failed one), got %d", totalExpected-1, len(successKeys))
	}
}

func TestPolicyOutcomes(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		reconcile func(obj client.Object) error
		obj       client.Object
		validate  func(t *testing.T, obj client.Object)
	}{
		{
			name: "CSRApproverClusterRole sets expected rules",
			obj:  &rbacv1.ClusterRole{},
			reconcile: func(obj client.Object) error {
				return ReconcileCSRApproverClusterRole(obj.(*rbacv1.ClusterRole))
			},
			validate: func(t *testing.T, obj client.Object) {
				cr := obj.(*rbacv1.ClusterRole)
				if len(cr.Rules) != 4 {
					t.Errorf("expected 4 rules, got %d", len(cr.Rules))
				}
			},
		},
		{
			name: "IngressToRouteControllerClusterRole sets expected rules",
			obj:  &rbacv1.ClusterRole{},
			reconcile: func(obj client.Object) error {
				return ReconcileIngressToRouteControllerClusterRole(obj.(*rbacv1.ClusterRole))
			},
			validate: func(t *testing.T, obj client.Object) {
				cr := obj.(*rbacv1.ClusterRole)
				if len(cr.Rules) != 6 {
					t.Errorf("expected 6 rules, got %d", len(cr.Rules))
				}
			},
		},
		{
			name: "NodeBootstrapperClusterRoleBinding sets node-bootstrapper roleref",
			obj:  &rbacv1.ClusterRoleBinding{},
			reconcile: func(obj client.Object) error {
				return ReconcileNodeBootstrapperClusterRoleBinding(obj.(*rbacv1.ClusterRoleBinding))
			},
			validate: func(t *testing.T, obj client.Object) {
				crb := obj.(*rbacv1.ClusterRoleBinding)
				if crb.RoleRef.Name != "system:node-bootstrapper" {
					t.Errorf("expected roleref system:node-bootstrapper, got %s", crb.RoleRef.Name)
				}
			},
		},
		{
			name: "DeployerClusterRole sets autoupdate annotation",
			obj:  &rbacv1.ClusterRole{},
			reconcile: func(obj client.Object) error {
				return ReconcileDeployerClusterRole(obj.(*rbacv1.ClusterRole))
			},
			validate: func(t *testing.T, obj client.Object) {
				cr := obj.(*rbacv1.ClusterRole)
				if cr.Annotations["rbac.authorization.kubernetes.io/autoupdate"] != "true" {
					t.Error("expected autoupdate annotation to be true")
				}
			},
		},
		{
			name: "GenericMetricsClusterRoleBinding parameterizes subject",
			obj:  &rbacv1.ClusterRoleBinding{},
			reconcile: func(obj client.Object) error {
				return ReconcileGenericMetricsClusterRoleBinding("test-cn")(obj.(*rbacv1.ClusterRoleBinding))
			},
			validate: func(t *testing.T, obj client.Object) {
				crb := obj.(*rbacv1.ClusterRoleBinding)
				if len(crb.Subjects) != 1 || crb.Subjects[0].Name != "test-cn" {
					t.Errorf("expected subject name test-cn, got %v", crb.Subjects)
				}
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if err := tt.reconcile(tt.obj); err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			tt.validate(t, tt.obj)
		})
	}
}

func TestBaseCatalogCount(t *testing.T) {
	t.Parallel()
	base := catalog(PolicyFacts{})
	aro := catalog(PolicyFacts{IsAroHCP: true})

	if len(base) == 0 {
		t.Fatal("base catalog is empty")
	}
	if len(aro) != len(base)+6 {
		t.Errorf("expected ARO catalog to have 6 more entries than base (%d), got %d", len(base), len(aro))
	}
}
