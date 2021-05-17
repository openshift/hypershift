package pki

import (
	"fmt"

	corev1 "k8s.io/api/core/v1"
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

func (p *PKIParams) ReconcileEtcdClientSecret(secret, ca *corev1.Secret) error {
	return p.reconcileSignedCertWithKeys(secret, ca, "etcd-client", "kubernetes", X509DefaultUsage, X509UsageClientAuth, EtcdClientCrtKey, EtcdClientKeyKey, EtcdClientCAKey)
}

func (p *PKIParams) ReconcileEtcdServerSecret(secret, ca *corev1.Secret) error {
	dnsNames := []string{
		fmt.Sprintf("*.etcd.%s.svc", p.Namespace),
		fmt.Sprintf("etcd-client.%s.svc", p.Namespace),
		fmt.Sprintf("*.etcd.%s.svc.cluster.local", p.Namespace),
		fmt.Sprintf("etcd-client.%s.svc.cluster.local", p.Namespace),
		"etcd",
		"etcd-client",
		"localhost",
	}
	return p.reconcileSignedCertWithKeysAndAddresses(secret, ca, "etcd-server", "kubernetes", X509DefaultUsage, X509UsageClientServerAuth, EtcdServerCrtKey, EtcdServerKeyKey, EtcdServerCAKey, dnsNames, nil)
}

func (p *PKIParams) ReconcileEtcdPeerSecret(secret, ca *corev1.Secret) error {
	dnsNames := []string{
		fmt.Sprintf("*.etcd.%s.svc", p.Namespace),
		fmt.Sprintf("*.etcd.%s.svc.cluster.local", p.Namespace),
	}
	return p.reconcileSignedCertWithKeysAndAddresses(secret, ca, "etcd-peer", "kubernetes", X509DefaultUsage, X509UsageClientServerAuth, EtcdPeerCrtKey, EtcdPeerKeyKey, EtcdPeerCAKey, dnsNames, nil)
}
