package pki

import (
	"fmt"
	"time"

	corev1 "k8s.io/api/core/v1"

	"github.com/openshift/hypershift/support/config"
)

func ReconcileMultusAdmissionControllerServingCertSecret(secret, ca *corev1.Secret, ownerRef config.OwnerRef, validity time.Duration) error {
	dnsNames := []string{
		fmt.Sprintf("multus-admission-controller.%s.svc", secret.Namespace),
		fmt.Sprintf("multus-admission-controller.%s.svc.cluster.local", secret.Namespace),
		"multus-admission-controller",
		"localhost",
	}
	return reconcileSignedCertWithAddressesAndSecretType(secret, ca, ownerRef, "multus-admission-controller", []string{"openshift"}, X509UsageClientServerAuth, dnsNames, nil, corev1.SecretTypeTLS, validity)
}
