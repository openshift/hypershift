package rbac

import (
	"context"
	"fmt"
	"reflect"
	"strings"
	"testing"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	hccomanifests "github.com/openshift/hypershift/control-plane-operator/hostedclusterconfigoperator/controllers/resources/manifests"
	"github.com/openshift/hypershift/support/upsert"

	rbacv1 "k8s.io/api/rbac/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/sets"

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

func expectedIngressRBACResourceKeys() sets.Set[string] {
	return sets.New(
		rbacResourceKey(hccomanifests.IngressToRouteControllerClusterRole()),
		rbacResourceKey(hccomanifests.IngressToRouteControllerRole()),
		rbacResourceKey(hccomanifests.IngressToRouteControllerClusterRoleBinding()),
		rbacResourceKey(hccomanifests.IngressToRouteControllerRoleBinding()),
	)
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

func TestManifestAndReconcileGetKey(t *testing.T) {
	tests := []struct {
		name string
		got  string
		want string
	}{
		{
			name: "When the manifest is cluster-scoped, it should use name and kind",
			got: (manifestAndReconcile[*rbacv1.ClusterRole]{manifest: func() *rbacv1.ClusterRole {
				return &rbacv1.ClusterRole{
					TypeMeta: metav1.TypeMeta{Kind: "ClusterRole"},
					ObjectMeta: metav1.ObjectMeta{
						Name: "example",
					},
				}
			}}).getKey(),
			want: "example/ClusterRole",
		},
		{
			name: "When the manifest is namespaced, it should use namespace, name, and kind",
			got: (manifestAndReconcile[*rbacv1.Role]{manifest: func() *rbacv1.Role {
				return &rbacv1.Role{
					TypeMeta: metav1.TypeMeta{Kind: "Role"},
					ObjectMeta: metav1.ObjectMeta{
						Namespace: "example-ns",
						Name:      "example",
					},
				}
			}}).getKey(),
			want: "example-ns/example/Role",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if tc.got != tc.want {
				t.Fatalf("unexpected manifest key: got %q, want %q", tc.got, tc.want)
			}
		})
	}

	wantCapabilities := map[string]hyperv1.OptionalCapability{
		"system:openshift:openshift-controller-manager:ingress-to-route-controller/ClusterRole":        hyperv1.IngressCapability,
		"openshift-route-controller-manager/openshift-route-controllers/Role":                          hyperv1.IngressCapability,
		"system:openshift:openshift-controller-manager:ingress-to-route-controller/ClusterRoleBinding": hyperv1.IngressCapability,
		"openshift-route-controller-manager/openshift-route-controllers/RoleBinding":                   hyperv1.IngressCapability,
	}
	if !reflect.DeepEqual(RbacCapabilityMap, wantCapabilities) {
		t.Fatalf("unexpected RBAC capability map: got %#v, want %#v", RbacCapabilityMap, wantCapabilities)
	}
}

func TestReconcile(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name        string
		params      ReconcileParams
		failures    map[int]error
		want        []string
		wantErrors  []string
		wantIngress sets.Set[string]
	}{
		{
			name:        "When reconciling a non-ARO HCP, it should reconcile the base catalog in order",
			params:      ReconcileParams{IngressEnabled: true},
			want:        expectedRBACResourceKeys(false),
			wantIngress: expectedIngressRBACResourceKeys(),
		},
		{
			name:        "When Ingress is disabled, it should preserve existing RBAC reconciliation behavior",
			params:      ReconcileParams{IngressEnabled: false},
			want:        expectedRBACResourceKeys(false),
			wantIngress: expectedIngressRBACResourceKeys(),
		},
		{
			name: "When the HCP is ARO, it should append ARO-only resources after the base catalog",
			params: ReconcileParams{
				IngressEnabled: true,
				IsAROHCP:       true,
			},
			want: expectedRBACResourceKeys(true),
		},
		{
			name:       "When multiple resources fail, it should aggregate errors and continue reconciling later resources",
			params:     ReconcileParams{IngressEnabled: true},
			failures:   map[int]error{0: fmt.Errorf("first failure"), 2: fmt.Errorf("third failure")},
			want:       expectedRBACResourceKeys(false),
			wantErrors: []string{"first failure", "third failure"},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			var calls []string
			err := Reconcile(t.Context(), nil, recordingCreateOrUpdate(&calls, tc.failures), tc.params)
			if len(tc.wantErrors) == 0 && err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			for _, wantError := range tc.wantErrors {
				if err == nil || !strings.Contains(err.Error(), wantError) {
					t.Fatalf("expected error containing %q, got %v", wantError, err)
				}
			}
			if !reflect.DeepEqual(calls, tc.want) {
				t.Fatalf("unexpected reconciliation order:\n got: %v\nwant: %v", calls, tc.want)
			}
			for key := range tc.wantIngress {
				if !sets.New(calls...).Has(key) {
					t.Errorf("expected ingress resource %q to be reconciled", key)
				}
			}
		})
	}
}

func TestReconcileIngressToRouteControllerClusterRole(t *testing.T) {
	t.Run("When reconciling, it should preserve the ingress controller policy rules", func(t *testing.T) {
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
	t.Run("When reconciling, it should preserve autoupdate, role reference, and subject", func(t *testing.T) {
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
	t.Run("When reconciling, it should set the correct policy rules", func(t *testing.T) {
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
	t.Run("When reconciling, it should set the correct role ref and subjects", func(t *testing.T) {
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
