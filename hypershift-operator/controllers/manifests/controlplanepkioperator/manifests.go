package controlplanepkioperator

import (
	"fmt"

	hypershiftv1beta1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	"github.com/openshift/hypershift/hypershift-operator/controllers/manifests"

	rbacv1 "k8s.io/api/rbac/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// we require labeling these cluster-scoped resources so that the controller cleaning them up
// can find them efficiently, as we can't use namespace-based scoping for these objects
const (
	OwningHostedClusterNamespaceLabel = "hypershift.openshift.io/owner.namespace"
	OwningHostedClusterNameLabel      = "hypershift.openshift.io/owner.name"
)

func CSRApproverClusterRole(hc *hypershiftv1beta1.HostedCluster) *rbacv1.ClusterRole {
	return &rbacv1.ClusterRole{
		ObjectMeta: metav1.ObjectMeta{
			Name: fmt.Sprintf("%s-csr-approver", manifests.HostedControlPlaneNamespace(hc.Namespace, hc.Name)),
			Labels: map[string]string{
				OwningHostedClusterNamespaceLabel: hc.Namespace,
				OwningHostedClusterNameLabel:      hc.Name,
			},
		},
	}
}

func CSRSignerClusterRole(hc *hypershiftv1beta1.HostedCluster) *rbacv1.ClusterRole {
	return &rbacv1.ClusterRole{
		ObjectMeta: metav1.ObjectMeta{
			Name: fmt.Sprintf("%s-csr-signer", manifests.HostedControlPlaneNamespace(hc.Namespace, hc.Name)),
			Labels: map[string]string{
				OwningHostedClusterNamespaceLabel: hc.Namespace,
				OwningHostedClusterNameLabel:      hc.Name,
			},
		},
	}
}

func ClusterRoleBinding(hc *hypershiftv1beta1.HostedCluster, clusterRole *rbacv1.ClusterRole) *rbacv1.ClusterRoleBinding {
	return &rbacv1.ClusterRoleBinding{
		ObjectMeta: metav1.ObjectMeta{
			Name: clusterRole.Name,
			Labels: map[string]string{
				OwningHostedClusterNamespaceLabel: hc.Namespace,
				OwningHostedClusterNameLabel:      hc.Name,
			},
		},
	}
}
