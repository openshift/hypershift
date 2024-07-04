package pki

import (
	"time"

	"github.com/openshift/hypershift/support/config"
	corev1 "k8s.io/api/core/v1"
)

func ReconcileAWSPodIdentityWebhookServingCert(secret, ca *corev1.Secret, ownerRef config.OwnerRef, validity time.Duration) error {
	return reconcileSignedCertWithAddresses(secret, ca, ownerRef, "127.0.0.1", nil, X509UsageClientServerAuth, nil, []string{"127.0.0.1"}, validity)
}
