package vpn

import (
	corev1 "k8s.io/api/core/v1"

	"github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/common"
	"github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/util"
)

func (p *VPNParams) ReconcileVPNServiceAccount(sa *corev1.ServiceAccount) error {
	util.EnsureOwnerRef(sa, p.OwnerReference)
	// Ensure that image pull secrets include the cluster's pull secret
	util.EnsurePullSecret(sa, common.PullSecret(sa.Namespace).Name)
	return nil
}
