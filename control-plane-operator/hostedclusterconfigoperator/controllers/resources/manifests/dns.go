package manifests

import (
	appsv1 "k8s.io/api/apps/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// DNSOperatorDeployment returns a stub deployment, with name and namespace, for
// the DNS operator.
func DNSOperatorDeployment() *appsv1.Deployment {
	return &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "dns-operator",
			Namespace: "openshift-dns-operator",
		},
	}
}
