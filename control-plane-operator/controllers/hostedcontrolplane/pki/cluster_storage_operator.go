package pki

import (
	"github.com/openshift/hypershift/support/config"

	corev1 "k8s.io/api/core/v1"
)

const clusterStorageOperatorMetricsHostname = "cluster-storage-operator"

func ReconcileClusterStorageOperatorServingCert(secret, ca *corev1.Secret, ownerRef config.OwnerRef) error {
	dnsNames := []string{
		clusterStorageOperatorMetricsHostname,
		"localhost",
	}
	return reconcileSignedCertWithAddresses(secret, ca, ownerRef, clusterStorageOperatorMetricsHostname, []string{"openshift"}, X509UsageClientServerAuth, dnsNames, nil)
}
