package controlplanepkioperator

import (
	hypershiftv1beta1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	"github.com/openshift/hypershift/control-plane-pki-operator/certificates"

	corev1 "k8s.io/api/core/v1"
	rbacv1 "k8s.io/api/rbac/v1"
)

func ReconcileCSRApproverClusterRole(clusterRole *rbacv1.ClusterRole, hc *hypershiftv1beta1.HostedCluster, signers ...certificates.SignerClass) error {
	var signerNames []string
	for _, signer := range signers {
		signerNames = append(signerNames, certificates.SignerNameForHC(hc, signer))
	}
	clusterRole.Rules = []rbacv1.PolicyRule{
		{
			APIGroups: []string{"certificates.k8s.io"},
			Resources: []string{"certificatesigningrequests"},
			Verbs:     []string{"get", "list", "watch"},
		},
		{
			APIGroups: []string{"certificates.k8s.io"},
			Resources: []string{"certificatesigningrequests/approval"},
			Verbs:     []string{"update"},
		},
		{
			APIGroups:     []string{"certificates.k8s.io"},
			Resources:     []string{"signers"},
			Verbs:         []string{"approve"},
			ResourceNames: signerNames,
		},
	}

	return nil
}

func ReconcileCSRSignerClusterRole(clusterRole *rbacv1.ClusterRole, hc *hypershiftv1beta1.HostedCluster, signers ...certificates.SignerClass) error {
	var signerNames []string
	for _, signer := range signers {
		signerNames = append(signerNames, certificates.SignerNameForHC(hc, signer))
	}
	clusterRole.Rules = []rbacv1.PolicyRule{
		{
			APIGroups: []string{"certificates.k8s.io"},
			Resources: []string{"certificatesigningrequests"},
			Verbs:     []string{"get", "list", "watch"},
		},
		{
			APIGroups: []string{"certificates.k8s.io"},
			Resources: []string{"certificatesigningrequests/status"},
			Verbs:     []string{"patch"},
		},
		{
			APIGroups:     []string{"certificates.k8s.io"},
			Resources:     []string{"signers"},
			Verbs:         []string{"sign"},
			ResourceNames: signerNames,
		},
	}

	return nil
}

func ReconcileClusterRoleBinding(clusterRoleBinding *rbacv1.ClusterRoleBinding, clusterRole *rbacv1.ClusterRole, serviceAccount *corev1.ServiceAccount) error {
	clusterRoleBinding.RoleRef = rbacv1.RoleRef{
		APIGroup: "rbac.authorization.k8s.io",
		Kind:     "ClusterRole",
		Name:     clusterRole.Name,
	}
	clusterRoleBinding.Subjects = []rbacv1.Subject{
		{
			Kind:      "ServiceAccount",
			Name:      serviceAccount.Name,
			Namespace: serviceAccount.Namespace,
		},
	}
	return nil
}
