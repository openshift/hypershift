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

func ReconcileEtcdPeerSecret(secret, ca *corev1.Secret, ownerRef config.OwnerRef, replicas int32) error {
	dnsNames := []string{
		fmt.Sprintf("*.etcd-discovery.%s.svc", secret.Namespace),
		fmt.Sprintf("*.etcd-discovery.%s.svc.cluster.local", secret.Namespace),
		"127.0.0.1",
		"::1",
	}
	// Add the exact per-member discovery names in addition to the wildcards. etcd
	// verifies a peer by reverse-resolving the dialer's pod IP and matching the result
	// against these SANs; on OpenShift a pod IP can reverse-resolve to the etcd-client
	// PTR first, which no wildcard matches, so isHostInDNS falls back to a forward lookup,
	// but only over exact (non-wildcard) names. Listing etcd-<ordinal>.etcd-discovery lets
	// that fallback accept the peer regardless of reverse-DNS ordering. replicas is the same
	// count the StatefulSet is sized with, so the SAN set and the members cannot drift.
	for i := int32(0); i < replicas; i++ {
		dnsNames = append(dnsNames,
			fmt.Sprintf("etcd-%d.etcd-discovery.%s.svc", i, secret.Namespace),
			fmt.Sprintf("etcd-%d.etcd-discovery.%s.svc.cluster.local", i, secret.Namespace),
		)
	}

	return reconcileSignedCertWithKeysAndAddresses(secret, ca, ownerRef, "etcd-discovery", []string{"kubernetes"}, X509UsageClientServerAuth, EtcdPeerCrtKey, EtcdPeerKeyKey, "", dnsNames, nil, "")
}
