package pki

import (
	"fmt"

	"github.com/openshift/hypershift/support/config"

	corev1 "k8s.io/api/core/v1"
)

func ReconcileSchedulerServerSecret(secret, ca *corev1.Secret, ownerRef config.OwnerRef) error {
	dnsNames := []string{
		fmt.Sprintf("kube-scheduler.%s.svc", secret.Namespace),
		fmt.Sprintf("kube-scheduler.%s.svc.cluster.local", secret.Namespace),
		"kube-scheduler",
		"localhost",
	}
	return reconcileSignedCertWithAddresses(secret, ca, ownerRef, "kube-scheduler", []string{"openshift"}, X509UsageClientServerAuth, dnsNames, nil)
}
