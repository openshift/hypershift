package pki

import (
	"fmt"

	corev1 "k8s.io/api/core/v1"

	"github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/manifests"
	"github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/util"
)

func (p *PKIParams) ReconcileVPNServerCertSecret(secret, ca *corev1.Secret) error {
	dnsNames := []string{
		"openvpn-server",
		fmt.Sprintf("%s.%s.svc", manifests.VPNService(p.Namespace).Name, p.Namespace),
		fmt.Sprintf("%s.%s.svc.cluster.local", manifests.VPNService(p.Namespace).Name, p.Namespace),
		p.ExternalOpenVPNAddress,
	}
	return p.reconcileSignedCertWithAddresses(secret, ca, "vpn-server", "kubernetes", X509DefaultUsage, X509UsageServerAuth, dnsNames, nil)
}

func (p *PKIParams) ReconcileVPNKubeAPIServerClientSecret(secret, ca *corev1.Secret) error {
	return p.reconcileSignedCert(secret, ca, "kube-apiserver", "kubernetes", X509DefaultUsage, X509UsageClientAuth)
}

func (p *PKIParams) ReconcileVPNClientSecret(secret, ca *corev1.Secret) error {
	return p.reconcileSignedCert(secret, ca, "worker", "kubernetes", X509DefaultUsage, X509UsageClientAuth)
}

func (p *PKIParams) ReconcileVPNWorkerClientSecret(cm *corev1.ConfigMap, ca *corev1.Secret) error {
	util.EnsureOwnerRef(cm, p.OwnerReference)
	secret := manifests.VPNClientSecret()
	if err := p.ReconcileVPNClientSecret(secret, ca); err != nil {
		return err
	}
	return util.ReconcileWorkerManifest(cm, secret)
}
