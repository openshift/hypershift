package pki

import (
	"fmt"

	"github.com/openshift/hypershift/support/config"

	corev1 "k8s.io/api/core/v1"
)

func ReconcileAzureDiskCsiDriverControllerMetricsServingCertSecret(secret, ca *corev1.Secret, ownerRef config.OwnerRef) error {
	dnsNames := []string{
		fmt.Sprintf("azure-disk-csi-driver-controller.%s.svc", secret.Namespace),
		fmt.Sprintf("azure-disk-csi-driver-controller.%s.svc.cluster.local", secret.Namespace),
		"azure-disk-csi-driver-controller",
		"localhost",
	}
	return reconcileSignedCertWithAddresses(secret, ca, ownerRef, "azure-disk-csi-driver-controller", []string{"openshift"}, X509UsageClientServerAuth, dnsNames, nil)
}
