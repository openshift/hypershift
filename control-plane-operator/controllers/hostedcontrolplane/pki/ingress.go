package pki

import (
	"fmt"

	corev1 "k8s.io/api/core/v1"
)

func (p *PKIParams) ReconcileIngressCert(secret, ca *corev1.Secret) error {
	var ingressNumericIPs, ingressHostNames []string
	if isNumericIP(p.ExternalOauthAddress) {
		ingressNumericIPs = append(ingressNumericIPs, p.ExternalOauthAddress)
	} else {
		ingressHostNames = append(ingressHostNames, p.ExternalOauthAddress)
	}
	ingressHostNames = append(ingressHostNames, fmt.Sprintf("*.%s", p.IngressSubdomain))
	return p.reconcileSignedCertWithAddresses(secret, ca, "openshift-ingress", "openshift", X509DefaultUsage, X509UsageClientServerAuth, ingressHostNames, ingressNumericIPs)
}

func (p *PKIParams) ReconcileOAuthServerCert(secret, sourceSecret, ca *corev1.Secret) error {
	secret.Data = sourceSecret.Data
	AnnotateWithCA(secret, ca)
	return nil
}
