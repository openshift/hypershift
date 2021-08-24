package pki

import (
	"fmt"

	corev1 "k8s.io/api/core/v1"

	"github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/config"
)

// Etcd secret keys
const (
	EtcdClientCrtKey = "etcd-client.crt"
	EtcdClientKeyKey = "etcd-client.key"
	EtcdClientCAKey  = "etcd-client-ca.crt"

	EtcdServerCrtKey = "server.crt"
	EtcdServerKeyKey = "server.key"
	EtcdServerCAKey  = "server-ca.crt"

	EtcdPeerCrtKey = "peer.crt"
	EtcdPeerKeyKey = "peer.key"
	EtcdPeerCAKey  = "peer-ca.crt"
)

func ReconcileEtcdClientSecret(secret, ca *corev1.Secret, ownerRef config.OwnerRef) error {
	return reconcileSignedCertWithKeys(secret, ca, ownerRef, "etcd-client", "kubernetes", X509DefaultUsage, X509UsageClientAuth, EtcdClientCrtKey, EtcdClientKeyKey, EtcdClientCAKey)
}

func ReconcileEtcdServerSecret(secret, ca *corev1.Secret, ownerRef config.OwnerRef) error {
	dnsNames := []string{
		fmt.Sprintf("*.etcd.%s.svc", secret.Namespace),
		fmt.Sprintf("etcd-client.%s.svc", secret.Namespace),
		fmt.Sprintf("*.etcd.%s.svc.cluster.local", secret.Namespace),
		fmt.Sprintf("etcd-client.%s.svc.cluster.local", secret.Namespace),
		"etcd",
		"etcd-client",
		"localhost",
	}
	return reconcileSignedCertWithKeysAndAddresses(secret, ca, ownerRef, "etcd-server", "kubernetes", X509DefaultUsage, X509UsageClientServerAuth, EtcdServerCrtKey, EtcdServerKeyKey, EtcdServerCAKey, dnsNames, nil)
}

func ReconcileEtcdPeerSecret(secret, ca *corev1.Secret, ownerRef config.OwnerRef) error {
	dnsNames := []string{
		fmt.Sprintf("*.etcd.%s.svc", secret.Namespace),
		fmt.Sprintf("*.etcd.%s.svc.cluster.local", secret.Namespace),
	}
	return reconcileSignedCertWithKeysAndAddresses(secret, ca, ownerRef, "etcd-peer", "kubernetes", X509DefaultUsage, X509UsageClientServerAuth, EtcdPeerCrtKey, EtcdPeerKeyKey, EtcdPeerCAKey, dnsNames, nil)
}
