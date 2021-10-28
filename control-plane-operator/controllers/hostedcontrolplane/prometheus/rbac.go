package prometheus

import (
	rbacv1 "k8s.io/api/rbac/v1"

	"github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/config"
	"github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/manifests"
)

func ReconcileRoleBinding(rb *rbacv1.RoleBinding, ownerRef config.OwnerRef) error {
	ownerRef.ApplyTo(rb)
	rb.RoleRef = rbacv1.RoleRef{
		Kind:     "ClusterRole",
		APIGroup: rbacv1.SchemeGroupVersion.Group,
		Name:     "edit",
	}
	rb.Subjects = []rbacv1.Subject{
		{
			Kind:      "ServiceAccount",
			Name:      manifests.PrometheusServiceAccount(rb.Namespace).Name,
			Namespace: rb.Namespace,
		},
	}
	return nil
}
