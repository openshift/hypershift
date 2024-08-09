package controlplanecomponent

import (
	"github.com/openshift/hypershift/hypershift-operator/controllers/manifests/controlplaneoperator"
	"github.com/openshift/hypershift/support/util"

	corev1 "k8s.io/api/core/v1"
	rbacv1 "k8s.io/api/rbac/v1"
)

type RBACReconciler interface {
	reconcileServiceAccount(cpContext ControlPlaneContext, serviceAccount *corev1.ServiceAccount) error
	reconcileRole(cpContext ControlPlaneContext, role *rbacv1.Role) error
	reconcileRoleBinding(cpContext ControlPlaneContext, roleBinding *rbacv1.RoleBinding, role *rbacv1.Role, serviceAccount *corev1.ServiceAccount) error
}

var _ RBACReconciler = &DefaultRBACReconciler{}

// default implenetation of RBACReconciler which is sufficient to be used in most cases.
type DefaultRBACReconciler struct {
	RoleRules []rbacv1.PolicyRule
}

func NewRBACReconciler(roleRules []rbacv1.PolicyRule) RBACReconciler {
	return &DefaultRBACReconciler{
		RoleRules: roleRules,
	}
}

// reconcileServiceAccount implements RBACReconciler.
func (d *DefaultRBACReconciler) reconcileServiceAccount(cpContext ControlPlaneContext, serviceAccount *corev1.ServiceAccount) error {
	util.EnsurePullSecret(serviceAccount, controlplaneoperator.PullSecret("").Name)
	return nil
}

// reconcileRole implements RBACReconciler.
func (d *DefaultRBACReconciler) reconcileRole(cpContext ControlPlaneContext, role *rbacv1.Role) error {
	role.Rules = d.RoleRules
	return nil
}

// reconcileRoleBinding implements RBACReconciler.
func (d *DefaultRBACReconciler) reconcileRoleBinding(cpContext ControlPlaneContext, roleBinding *rbacv1.RoleBinding, role *rbacv1.Role, serviceAccount *corev1.ServiceAccount) error {
	roleBinding.RoleRef = rbacv1.RoleRef{
		APIGroup: "rbac.authorization.k8s.io",
		Kind:     "Role",
		Name:     role.Name,
	}
	roleBinding.Subjects = []rbacv1.Subject{
		{
			Kind:      "ServiceAccount",
			Name:      serviceAccount.Name,
			Namespace: serviceAccount.Namespace,
		},
	}

	return nil
}
