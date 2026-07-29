package pki

import (
	"github.com/openshift/hypershift/support/config"

	corev1 "k8s.io/api/core/v1"
)

const csiSnapshotControllerOperatorMetricsHostname = "csi-snapshot-controller-operator"

func ReconcileCSISnapshotControllerOperatorServingCert(secret, ca *corev1.Secret, ownerRef config.OwnerRef) error {
	dnsNames := []string{
		csiSnapshotControllerOperatorMetricsHostname,
		"localhost",
	}
	return reconcileSignedCertWithAddresses(secret, ca, ownerRef, csiSnapshotControllerOperatorMetricsHostname, []string{"openshift"}, X509UsageClientServerAuth, dnsNames, nil)
}
