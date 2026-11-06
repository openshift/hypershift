package pki

import (
	"fmt"

	"github.com/openshift/hypershift/support/config"
	etcdutil "github.com/openshift/hypershift/support/etcd"

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
	}
	// Add the exact per-member discovery names in addition to the wildcards.
	// etcd verifies a peer by reverse-resolving the dialer's pod IP and matching
	// the result against these SANs (client/pkg/transport/listener_tls.go,
	// checkCertSAN -> isHostInDNS). On OpenShift, a pod IP reverse-resolves to
	// both its etcd-discovery PTR and an <dashed-ip>.etcd-client PTR, returned in
	// non-deterministic order; when the etcd-client name comes first the wildcard
	// match fails and the peer connection is rejected intermittently. isHostInDNS
	// falls back to a forward lookup, but only over the exact (non-wildcard) SAN
	// names. Listing etcd-<ordinal>.etcd-discovery lets that fallback forward-
	// resolve the member's stable headless name to the dialer IP and accept the
	// connection regardless of reverse-DNS ordering.
	//
	// replicas is the resolved member count from the same authority the etcd
	// StatefulSet sizes itself with (etcd.Replicas -> component.DefaultReplicas),
	// so the SAN set and the actual members can never drift.
	for i := int32(0); i < replicas; i++ {
		dnsNames = append(dnsNames,
			fmt.Sprintf("etcd-%d.etcd-discovery.%s.svc", i, secret.Namespace),
			fmt.Sprintf("etcd-%d.etcd-discovery.%s.svc.cluster.local", i, secret.Namespace),
		)
	}
	ips := []string{"127.0.0.1", "::1"}

	return reconcileSignedCertWithKeysAndAddresses(secret, ca, ownerRef, "etcd-discovery", []string{"kubernetes"}, X509UsageClientServerAuth, EtcdPeerCrtKey, EtcdPeerKeyKey, "", dnsNames, ips, "")
}

func ReconcileEtcdShardServerSecret(secret, ca *corev1.Secret, ownerRef config.OwnerRef, shardName string) error {
	clientService := etcdutil.ClientServiceName(shardName)
	discoveryService := etcdutil.DiscoveryServiceName(shardName)
	dnsNames := []string{
		fmt.Sprintf("%s.%s.svc", clientService, secret.Namespace),
		fmt.Sprintf("%s.%s.svc.cluster.local", clientService, secret.Namespace),
		fmt.Sprintf("*.%s.%s.svc", discoveryService, secret.Namespace),
		fmt.Sprintf("*.%s.%s.svc.cluster.local", discoveryService, secret.Namespace),
		clientService,
		"localhost",
	}

	return reconcileSignedCertWithKeysAndAddresses(secret, ca, ownerRef, "etcd-server", []string{"kubernetes"}, X509UsageClientServerAuth, EtcdServerCrtKey, EtcdServerKeyKey, "", dnsNames, nil, "")
}

func ReconcileEtcdShardPeerSecret(secret, ca *corev1.Secret, ownerRef config.OwnerRef, shardName string, replicas int32) error {
	discoveryService := etcdutil.DiscoveryServiceName(shardName)
	dnsNames := []string{
		fmt.Sprintf("*.%s.%s.svc", discoveryService, secret.Namespace),
		fmt.Sprintf("*.%s.%s.svc.cluster.local", discoveryService, secret.Namespace),
	}
	// Add the exact per-member discovery names in addition to the wildcards so
	// etcd's peer-TLS forward-lookup fallback (isHostInDNS) can confirm a dialer
	// when reverse DNS returns the ambiguous client-service PTR first. See
	// ReconcileEtcdPeerSecret for the full rationale. Shard members are named
	// <shardName>-<ordinal>; replicas is the shard's own ManagedEtcdShardSpec
	// .Replicas, the same value its StatefulSet is sized with.
	for i := int32(0); i < replicas; i++ {
		dnsNames = append(dnsNames,
			fmt.Sprintf("%s-%d.%s.%s.svc", shardName, i, discoveryService, secret.Namespace),
			fmt.Sprintf("%s-%d.%s.%s.svc.cluster.local", shardName, i, discoveryService, secret.Namespace),
		)
	}
	ips := []string{"127.0.0.1", "::1"}

	return reconcileSignedCertWithKeysAndAddresses(secret, ca, ownerRef, discoveryService, []string{"kubernetes"}, X509UsageClientServerAuth, EtcdPeerCrtKey, EtcdPeerKeyKey, "", dnsNames, ips, "")
}
