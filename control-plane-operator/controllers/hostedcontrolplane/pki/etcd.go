package pki

import (
	"fmt"

	"github.com/openshift/hypershift/support/config"

	corev1 "k8s.io/api/core/v1"
)

// Etcd secret keys
const (
	EtcdClientCrtKey = "etcd-client.crt"
	EtcdClientKeyKey = "etcd-client.key"

	EtcdServerCrtKey = "server.crt"
	EtcdServerKeyKey = "server.key"

	EtcdPeerCrtKey = "peer.crt"
	EtcdPeerKeyKey = "peer.key"
)

func ReconcileEtcdClientSecret(secret, ca *corev1.Secret, ownerRef config.OwnerRef) error {
	return reconcileSignedCertWithKeys(secret, ca, ownerRef, "etcd-client", []string{"kubernetes"}, X509UsageClientAuth, EtcdClientCrtKey, EtcdClientKeyKey, "")
}

func ReconcileEtcdMetricsClientSecret(secret, ca *corev1.Secret, ownerRef config.OwnerRef) error {
	return reconcileSignedCertWithKeys(secret, ca, ownerRef, "etcd-metrics-client", []string{"kubernetes"}, X509UsageClientAuth, EtcdClientCrtKey, EtcdClientKeyKey, "")
}

func ReconcileEtcdServerSecret(secret, ca *corev1.Secret, ownerRef config.OwnerRef) error {
	dnsNames := []string{
		fmt.Sprintf("etcd-client.%s.svc", secret.Namespace),
		fmt.Sprintf("etcd-client.%s.svc.cluster.local", secret.Namespace),
		fmt.Sprintf("*.etcd-discovery.%s.svc", secret.Namespace),
		fmt.Sprintf("*.etcd-discovery.%s.svc.cluster.local", secret.Namespace),
		"etcd-client",
		"localhost",
	}

	return reconcileSignedCertWithKeysAndAddresses(secret, ca, ownerRef, "etcd-server", []string{"kubernetes"}, X509UsageClientServerAuth, EtcdServerCrtKey, EtcdServerKeyKey, "", dnsNames, nil, "")
}

func ReconcileEtcdPeerSecret(secret, ca *corev1.Secret, ownerRef config.OwnerRef) error {
	dnsNames := []string{
		fmt.Sprintf("*.etcd-discovery.%s.svc", secret.Namespace),
		fmt.Sprintf("*.etcd-discovery.%s.svc.cluster.local", secret.Namespace),
		"127.0.0.1",
		"::1",
	}

	return reconcileSignedCertWithKeysAndAddresses(secret, ca, ownerRef, "etcd-discovery", []string{"kubernetes"}, X509UsageClientServerAuth, EtcdPeerCrtKey, EtcdPeerKeyKey, "", dnsNames, nil, "")
}
