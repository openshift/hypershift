package pki

import (
	"fmt"

	corev1 "k8s.io/api/core/v1"

	"github.com/openshift/hypershift/support/config"
)

const CloudCredentialOperatorMetricsHostname = "cloud-credential-operator-metrics"

func ReconcileCloudCredentialOperatorServingCertSecret(secret, ca *corev1.Secret, ownerRef config.OwnerRef) error {
	dnsNames := []string{
		CloudCredentialOperatorMetricsHostname,
		fmt.Sprintf("cloud-credential-operator-metrics.%s.svc", secret.Namespace),
		fmt.Sprintf("cloud-credential-operator-metrics.%s.svc.cluster.local", secret.Namespace),
	}
	return reconcileSignedCertWithAddresses(secret, ca, ownerRef, CloudCredentialOperatorMetricsHostname, []string{"openshift"}, X509UsageClientServerAuth, dnsNames, nil)
}
