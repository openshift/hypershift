package pki

import (
	"fmt"

	corev1 "k8s.io/api/core/v1"

	"github.com/openshift/hypershift/support/config"
)

// This is the expected DNS name for the server name in the ServiceMonitor the cluster network operator lays down.
// https://github.com/openshift/cluster-network-operator/blob/a1283bfaf7bf0a90c82cf72d8da97035a7b7020e/bindata/network/ovn-kubernetes/managed/monitor-control-plane.yaml#L35
const ovnkubeControlPlaneServingCertName = "ovn-kubernetes-control-plane"

func ReconcileOVNControlPlaneMetricsServingCertSecret(secret, ca *corev1.Secret, ownerRef config.OwnerRef) error {
	dnsNames := []string{
		fmt.Sprintf("%s.%s.svc", ovnkubeControlPlaneServingCertName, secret.Namespace),
		fmt.Sprintf("%s.%s.svc.cluster.local", ovnkubeControlPlaneServingCertName, secret.Namespace),
		ovnkubeControlPlaneServingCertName,
		"localhost",
	}
	return reconcileSignedCertWithAddressesAndSecretType(secret, ca, ownerRef, ovnkubeControlPlaneServingCertName, []string{"openshift"}, X509UsageClientServerAuth, dnsNames, nil, corev1.SecretTypeTLS)
}
