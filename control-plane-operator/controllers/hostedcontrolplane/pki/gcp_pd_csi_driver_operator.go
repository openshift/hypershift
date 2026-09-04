package pki

import (
	"fmt"

	"github.com/openshift/hypershift/support/config"

	corev1 "k8s.io/api/core/v1"
)

func ReconcileGCPPDCsiDriverOperatorMetricsServingCertSecret(secret, ca *corev1.Secret, ownerRef config.OwnerRef) error {
	dnsNames := []string{
		fmt.Sprintf("gcp-pd-csi-driver-operator-metrics.%s.svc", secret.Namespace),
		fmt.Sprintf("gcp-pd-csi-driver-operator-metrics.%s.svc.cluster.local", secret.Namespace),
		"gcp-pd-csi-driver-operator-metrics",
		"localhost",
	}
	return reconcileSignedCertWithAddresses(secret, ca, ownerRef, "gcp-pd-csi-driver-operator-metrics", []string{"openshift"}, X509UsageClientServerAuth, dnsNames, nil)
}
