package hostedcontrolplane

import (
	"context"
	"fmt"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	"github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/manifests"
	"github.com/openshift/hypershift/support/config"
	"github.com/openshift/hypershift/support/upsert"

	rbacv1 "k8s.io/api/rbac/v1"
)

// reconcileGMPCollectorAccess grants Google's managed collector read access to
// the TLS Secrets the staged PodMonitoring assets reference.
//
// GMP TLS configuration accepts only Secret references, so the CA ConfigMaps the
// CoreOS monitors trust are mirrored into CA-only Secrets by reconcilePKI. The
// collector runs under its own ServiceAccount outside the control-plane
// namespace, and a PodMonitoring cannot present credentials without this read
// access: Phase 0 confirmed scraping fails without it.
//
// The binding is built in Go rather than as a component asset because the
// component framework rewrites ServiceAccount subject namespaces to the
// control-plane namespace, which would bind the Role to a ServiceAccount that
// does not exist there.
func (r *HostedControlPlaneReconciler) reconcileGMPCollectorAccess(ctx context.Context, hcp *hyperv1.HostedControlPlane, createOrUpdate upsert.CreateOrUpdateFN) error {
	ownerRef := config.OwnerRefFrom(hcp)

	role := manifests.GMPCollectorRole(hcp.Namespace)
	if _, err := createOrUpdate(ctx, r, role, func() error {
		ownerRef.ApplyTo(role)
		role.Rules = []rbacv1.PolicyRule{
			{
				APIGroups:     []string{""},
				Resources:     []string{"secrets"},
				ResourceNames: manifests.GMPCollectorSecretNames,
				Verbs:         []string{"get", "list", "watch"},
			},
		}
		return nil
	}); err != nil {
		return fmt.Errorf("failed to reconcile GMP collector role: %w", err)
	}

	binding := manifests.GMPCollectorRoleBinding(hcp.Namespace)
	if _, err := createOrUpdate(ctx, r, binding, func() error {
		ownerRef.ApplyTo(binding)
		binding.RoleRef = rbacv1.RoleRef{
			APIGroup: rbacv1.GroupName,
			Kind:     "Role",
			Name:     role.Name,
		}
		binding.Subjects = []rbacv1.Subject{
			{
				Kind:      rbacv1.ServiceAccountKind,
				Name:      manifests.GMPCollectorServiceAccountName,
				Namespace: manifests.GMPCollectorNamespace,
			},
		}
		return nil
	}); err != nil {
		return fmt.Errorf("failed to reconcile GMP collector role binding: %w", err)
	}

	return nil
}
