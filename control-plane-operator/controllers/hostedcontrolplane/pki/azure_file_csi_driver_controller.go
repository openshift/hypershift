package pki

import (
	"fmt"

	corev1 "k8s.io/api/core/v1"

	"github.com/openshift/hypershift/support/config"
)

func ReconcileAzureFileCsiDriverControllerMetricsServingCertSecret(secret, ca *corev1.Secret, ownerRef config.OwnerRef) error {
	dnsNames := []string{
		fmt.Sprintf("azure-file-csi-driver-controller.%s.svc", secret.Namespace),
		fmt.Sprintf("azure-file-csi-driver-controller.%s.svc.cluster.local", secret.Namespace),
		"azure-file-csi-driver-controller",
		"localhost",
	}
	return reconcileSignedCertWithAddresses(secret, ca, ownerRef, "azure-file-csi-driver-controller", []string{"openshift"}, X509UsageClientServerAuth, dnsNames, nil)
}
