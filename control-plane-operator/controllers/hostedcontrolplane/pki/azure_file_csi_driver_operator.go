package pki

import (
	"fmt"

	"github.com/openshift/hypershift/support/config"

	corev1 "k8s.io/api/core/v1"
)

func ReconcileAzureFileCsiDriverOperatorMetricsServingCertSecret(secret, ca *corev1.Secret, ownerRef config.OwnerRef) error {
	dnsNames := []string{
		fmt.Sprintf("azure-file-csi-driver-operator.%s.svc", secret.Namespace),
		fmt.Sprintf("azure-file-csi-driver-operator.%s.svc.cluster.local", secret.Namespace),
		"azure-file-csi-driver-operator",
		"localhost",
	}
	return reconcileSignedCertWithAddresses(secret, ca, ownerRef, "azure-file-csi-driver-operator", []string{"openshift"}, X509UsageClientServerAuth, dnsNames, nil)
}
