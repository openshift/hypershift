package manifests

import (
	admissionregistrationv1 "k8s.io/api/admissionregistration/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func GCPWorkloadIdentityFederationWebhook() *admissionregistrationv1.MutatingWebhookConfiguration {
	return &admissionregistrationv1.MutatingWebhookConfiguration{
		ObjectMeta: metav1.ObjectMeta{
			Name: "gcp-workload-identity-federation-webhook",
		},
	}
}

func GCPWorkloadIdentityFederationWebhookClusterRole() *rbacv1.ClusterRole {
	return &rbacv1.ClusterRole{
		ObjectMeta: metav1.ObjectMeta{
			Name: "gcp-workload-identity-federation-webhook",
		},
	}
}

func GCPWorkloadIdentityFederationWebhookClusterRoleBinding() *rbacv1.ClusterRoleBinding {
	return &rbacv1.ClusterRoleBinding{
		ObjectMeta: metav1.ObjectMeta{
			Name: "gcp-workload-identity-federation-webhook",
		},
	}
}
