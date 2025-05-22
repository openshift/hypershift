package karpenter

import (
	"github.com/openshift/hypershift/support/config"
	component "github.com/openshift/hypershift/support/controlplane-component"

	rbacv1 "k8s.io/api/rbac/v1"
)

func adaptRole(cpContext component.WorkloadContext, role *rbacv1.Role) error {
	ownerRef := config.OwnerRefFrom(cpContext.HCP)
	ownerRef.ApplyTo(role)

	return nil
}
