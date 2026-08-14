package pki

import (
	"fmt"

	"github.com/openshift/hypershift/support/config"

	corev1 "k8s.io/api/core/v1"
)

func ReconcileAWSEBSCsiDriverOperatorMetricsServingCertSecret(secret, ca *corev1.Secret, ownerRef config.OwnerRef) error {
	dnsNames := []string{
		fmt.Sprintf("aws-ebs-csi-driver-operator-metrics.%s.svc", secret.Namespace),
		fmt.Sprintf("aws-ebs-csi-driver-operator-metrics.%s.svc.cluster.local", secret.Namespace),
		"aws-ebs-csi-driver-operator-metrics",
		"localhost",
	}
	return reconcileSignedCertWithAddresses(secret, ca, ownerRef, "aws-ebs-csi-driver-operator-metrics", []string{"openshift"}, X509UsageClientServerAuth, dnsNames, nil)
}
