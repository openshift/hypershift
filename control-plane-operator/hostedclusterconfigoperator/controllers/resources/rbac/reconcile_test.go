package rbac

import (
	"context"
	"fmt"
	"reflect"
	"strings"
	"testing"

	hccomanifests "github.com/openshift/hypershift/control-plane-operator/hostedclusterconfigoperator/controllers/resources/manifests"
	"github.com/openshift/hypershift/support/upsert"

	rbacv1 "k8s.io/api/rbac/v1"

	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
)

func rbacResourceKey(obj client.Object) string {
	return fmt.Sprintf("%T:%s/%s", obj, obj.GetNamespace(), obj.GetName())
}

func recordingCreateOrUpdate(calls *[]string, failures map[int]error) upsert.CreateOrUpdateFN {
	return func(_ context.Context, _ client.Client, obj client.Object, mutate controllerutil.MutateFn) (controllerutil.OperationResult, error) {
		*calls = append(*calls, rbacResourceKey(obj))
		if err := mutate(); err != nil {
			return controllerutil.OperationResultNone, err
		}
		if err, ok := failures[len(*calls)-1]; ok {
			return controllerutil.OperationResultNone, err
		}
		return controllerutil.OperationResultCreated, nil
	}
}

func expectedRBACResourceKeys(isAROHCP bool) []string {
	keys := []string{
		rbacResourceKey(hccomanifests.CSRApproverClusterRole()),
		rbacResourceKey(hccomanifests.IngressToRouteControllerClusterRole()),
		rbacResourceKey(hccomanifests.NamespaceSecurityAllocationControllerClusterRole()),
		rbacResourceKey(hccomanifests.IngressToRouteControllerRole()),
		rbacResourceKey(hccomanifests.CSRApproverClusterRoleBinding()),
		rbacResourceKey(hccomanifests.IngressToRouteControllerClusterRoleBinding()),
		rbacResourceKey(hccomanifests.NamespaceSecurityAllocationControllerClusterRoleBinding()),
		rbacResourceKey(hccomanifests.NodeBootstrapperClusterRoleBinding()),
		rbacResourceKey(hccomanifests.CSRRenewalClusterRoleBinding()),
		rbacResourceKey(hccomanifests.MetricsClientClusterRoleBinding()),
		rbacResourceKey(hccomanifests.MetricsResourcesClusterRole()),
		rbacResourceKey(hccomanifests.MetricsResourcesClusterRoleBinding()),
		rbacResourceKey(hccomanifests.IngressToRouteControllerRoleBinding()),
		rbacResourceKey(hccomanifests.AuthenticatedReaderForAuthenticatedUserRolebinding()),
		rbacResourceKey(hccomanifests.KCMLeaderElectionRole()),
		rbacResourceKey(hccomanifests.KCMLeaderElectionRoleBinding()),
		rbacResourceKey(hccomanifests.ImageTriggerControllerClusterRole()),
		rbacResourceKey(hccomanifests.ImageTriggerControllerClusterRoleBinding()),
		rbacResourceKey(hccomanifests.PodSecurityAdmissionLabelSyncerControllerClusterRole()),
		rbacResourceKey(hccomanifests.PodSecurityAdmissionLabelSyncerControllerRoleBinding()),
		rbacResourceKey(hccomanifests.PriviligedNamespacesPSALabelSyncerClusterRole()),
		rbacResourceKey(hccomanifests.PriviligedNamespacesPSALabelSyncerClusterRoleBinding()),
		rbacResourceKey(hccomanifests.DeployerClusterRole()),
		rbacResourceKey(hccomanifests.DeployerClusterRoleBinding()),
		rbacResourceKey(hccomanifests.UserOAuthClusterRole()),
		rbacResourceKey(hccomanifests.UserOAuthClusterRoleBinding()),
		rbacResourceKey(hccomanifests.KASConnectionCheckerRole()),
		rbacResourceKey(hccomanifests.KASConnectionCheckerRoleBinding()),
	}
	if isAROHCP {
		keys = append(keys,
			rbacResourceKey(hccomanifests.AzureDiskCSIDriverNodeServiceAccountRole()),
			rbacResourceKey(hccomanifests.AzureDiskCSIDriverNodeServiceAccountRoleBinding()),
			rbacResourceKey(hccomanifests.AzureFileCSIDriverNodeServiceAccountRole()),
			rbacResourceKey(hccomanifests.AzureFileCSIDriverNodeServiceAccountRoleBinding()),
			rbacResourceKey(hccomanifests.CloudNetworkConfigControllerServiceAccountRole()),
			rbacResourceKey(hccomanifests.CloudNetworkConfigControllerServiceAccountRoleBinding()),
		)
	}
	return keys
}

func TestReconcile(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name   string
		params ReconcileParams
		want   []string
	}{
		{
			name: "When ingress is enabled for a non-ARO HCP, it should reconcile the base catalog in order",
			params: ReconcileParams{
				IngressEnabled: true,
			},
			want: expectedRBACResourceKeys(false),
		},
		{
			name: "When ingress is disabled, it should omit all ingress resources while preserving order",
			params: ReconcileParams{
				IngressEnabled: false,
			},
			want: func() []string {
				all := expectedRBACResourceKeys(false)
				var want []string
				for i, key := range all {
					if i == 1 || i == 3 || i == 5 || i == 12 {
						continue
					}
					want = append(want, key)
				}
				return want
			}(),
		},
		{
			name: "When the HCP is ARO, it should append ARO-only resources after the base catalog",
			params: ReconcileParams{
				IngressEnabled: true,
				IsAROHCP:       true,
			},
			want: expectedRBACResourceKeys(true),
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			var calls []string
			if err := Reconcile(t.Context(), nil, recordingCreateOrUpdate(&calls, nil), tc.params); err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if !reflect.DeepEqual(calls, tc.want) {
				t.Fatalf("unexpected reconciliation order:\n got: %v\nwant: %v", calls, tc.want)
			}
		})
	}
}

func TestReconcileAggregatesErrors(t *testing.T) {
	t.Parallel()
	var calls []string
	want := expectedRBACResourceKeys(false)
	err := Reconcile(t.Context(), nil, recordingCreateOrUpdate(&calls, map[int]error{
		0: fmt.Errorf("first failure"),
		2: fmt.Errorf("third failure"),
	}), ReconcileParams{IngressEnabled: true})
	if err == nil {
		t.Fatal("expected aggregate error")
	}
	if !strings.Contains(err.Error(), "first failure") || !strings.Contains(err.Error(), "third failure") {
		t.Fatalf("aggregate error did not contain all failures: %v", err)
	}
	if !reflect.DeepEqual(calls, want) {
		t.Fatalf("later resources were not attempted after failures:\n got: %v\nwant: %v", calls, want)
	}
}

func TestReconcileIngressToRouteControllerClusterRole(t *testing.T) {
	t.Run("When reconciling it should preserve the ingress controller policy rules", func(t *testing.T) {
		role := &rbacv1.ClusterRole{}
		if err := ReconcileIngressToRouteControllerClusterRole(role); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(role.Rules) != 6 {
			t.Fatalf("expected 6 rules, got %d", len(role.Rules))
		}
		if got := role.Rules[0]; !reflect.DeepEqual(got, rbacv1.PolicyRule{
			APIGroups: []string{""},
			Resources: []string{"secrets", "services"},
			Verbs:     []string{"get", "list", "watch"},
		}) {
			t.Errorf("unexpected secrets/services rule: %#v", got)
		}
		if got := role.Rules[3]; !reflect.DeepEqual(got, rbacv1.PolicyRule{
			APIGroups: []string{"route.openshift.io"},
			Resources: []string{"routes"},
			Verbs:     []string{"create", "delete", "patch", "update", "get", "list", "watch"},
		}) {
			t.Errorf("unexpected routes rule: %#v", got)
		}
	})
}

func TestReconcileCSRApproverClusterRoleBinding(t *testing.T) {
	t.Run("When reconciling it should preserve autoupdate, role reference, and subject", func(t *testing.T) {
		binding := &rbacv1.ClusterRoleBinding{}
		if err := ReconcileCSRApproverClusterRoleBinding(binding); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if binding.Annotations["rbac.authorization.kubernetes.io/autoupdate"] != "true" {
			t.Errorf("expected autoupdate annotation, got %v", binding.Annotations)
		}
		if binding.RoleRef != (rbacv1.RoleRef{
			APIGroup: rbacv1.SchemeGroupVersion.Group,
			Kind:     "ClusterRole",
			Name:     hccomanifests.CSRApproverClusterRoleBinding().Name,
		}) {
			t.Errorf("unexpected role reference: %#v", binding.RoleRef)
		}
		if !reflect.DeepEqual(binding.Subjects, []rbacv1.Subject{{
			Kind:      "ServiceAccount",
			Name:      "cluster-csr-approver-controller",
			Namespace: "openshift-infra",
		}}) {
			t.Errorf("unexpected subjects: %#v", binding.Subjects)
		}
	})
}

func TestReconcileMetricsResourcesClusterRole(t *testing.T) {
	t.Run("When reconciling it should set the correct policy rules", func(t *testing.T) {
		role := &rbacv1.ClusterRole{}
		if err := ReconcileMetricsResourcesClusterRole(role); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(role.Rules) != 1 {
			t.Fatalf("expected 1 rule, got %d", len(role.Rules))
		}
		rule := role.Rules[0]
		if len(rule.NonResourceURLs) != 1 || rule.NonResourceURLs[0] != "/metrics/resources" {
			t.Errorf("expected NonResourceURLs [\"/metrics/resources\"], got %v", rule.NonResourceURLs)
		}
		if len(rule.Verbs) != 1 || rule.Verbs[0] != "get" {
			t.Errorf("expected Verbs [\"get\"], got %v", rule.Verbs)
		}
	})
}

func TestReconcileMetricsResourcesClusterRoleBinding(t *testing.T) {
	t.Run("When reconciling it should set the correct role ref and subjects", func(t *testing.T) {
		binding := &rbacv1.ClusterRoleBinding{}
		if err := ReconcileMetricsResourcesClusterRoleBinding(binding); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if binding.RoleRef.Kind != "ClusterRole" {
			t.Errorf("expected RoleRef.Kind ClusterRole, got %s", binding.RoleRef.Kind)
		}
		if binding.RoleRef.Name != "hypershift-metrics-resources-reader" {
			t.Errorf("expected RoleRef.Name hypershift-metrics-resources-reader, got %s", binding.RoleRef.Name)
		}
		if len(binding.Subjects) != 1 {
			t.Fatalf("expected 1 subject, got %d", len(binding.Subjects))
		}
		subject := binding.Subjects[0]
		if subject.Kind != "User" {
			t.Errorf("expected subject Kind User, got %s", subject.Kind)
		}
		if subject.Name != "system:serviceaccount:hypershift:prometheus" {
			t.Errorf("expected subject Name system:serviceaccount:hypershift:prometheus, got %s", subject.Name)
		}
	})
}
