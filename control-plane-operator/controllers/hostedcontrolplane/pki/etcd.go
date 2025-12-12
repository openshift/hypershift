package pki

import (
	"fmt"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
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

func ReconcileEtcdServerSecret(secret, ca *corev1.Secret, ownerRef config.OwnerRef, shards []hyperv1.ManagedEtcdShardSpec) error {
	// Build DNS names for all etcd shard services
	dnsNames := []string{
		// Backward compatibility - old non-sharded names
		fmt.Sprintf("etcd-client.%s.svc", secret.Namespace),
		fmt.Sprintf("etcd-client.%s.svc.cluster.local", secret.Namespace),
		fmt.Sprintf("*.etcd-discovery.%s.svc", secret.Namespace),
		fmt.Sprintf("*.etcd-discovery.%s.svc.cluster.local", secret.Namespace),
		"etcd-client",
		"localhost",
	}

	// Add SANs for all shard-specific client and discovery services
	for _, shard := range shards {
		if shard.Name == "default" {
			// Default shard uses non-suffixed names (already in list above)
			continue
		}

		clientServiceName := fmt.Sprintf("etcd-client-%s", shard.Name)
		discoveryServiceName := fmt.Sprintf("etcd-discovery-%s", shard.Name)

		dnsNames = append(dnsNames,
			fmt.Sprintf("%s.%s.svc", clientServiceName, secret.Namespace),
			fmt.Sprintf("%s.%s.svc.cluster.local", clientServiceName, secret.Namespace),
			fmt.Sprintf("*.%s.%s.svc", discoveryServiceName, secret.Namespace),
			fmt.Sprintf("*.%s.%s.svc.cluster.local", discoveryServiceName, secret.Namespace),
		)
	}

	return reconcileSignedCertWithKeysAndAddresses(secret, ca, ownerRef, "etcd-server", []string{"kubernetes"}, X509UsageClientServerAuth, EtcdServerCrtKey, EtcdServerKeyKey, "", dnsNames, nil, "")
}

func ReconcileEtcdPeerSecret(secret, ca *corev1.Secret, ownerRef config.OwnerRef, shards []hyperv1.ManagedEtcdShardSpec) error {
	// Build DNS names for all etcd shard discovery services
	dnsNames := []string{
		// Backward compatibility - old non-sharded discovery wildcard
		fmt.Sprintf("*.etcd-discovery.%s.svc", secret.Namespace),
		fmt.Sprintf("*.etcd-discovery.%s.svc.cluster.local", secret.Namespace),
		"127.0.0.1",
		"::1",
	}

	// Add wildcards for all shard-specific discovery services
	for _, shard := range shards {
		if shard.Name == "default" {
			// Default shard uses non-suffixed names (already in list above)
			continue
		}

		discoveryServiceName := fmt.Sprintf("etcd-discovery-%s", shard.Name)
		dnsNames = append(dnsNames,
			fmt.Sprintf("*.%s.%s.svc", discoveryServiceName, secret.Namespace),
			fmt.Sprintf("*.%s.%s.svc.cluster.local", discoveryServiceName, secret.Namespace),
		)
	}

	return reconcileSignedCertWithKeysAndAddresses(secret, ca, ownerRef, "etcd-discovery", []string{"kubernetes"}, X509UsageClientServerAuth, EtcdPeerCrtKey, EtcdPeerKeyKey, "", dnsNames, nil, "")
}
