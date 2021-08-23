package pki

import (
	"github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/config"
	corev1 "k8s.io/api/core/v1"
)

func (p *PKIParams) ReconcileOAuthServerCert(secret, ca *corev1.Secret, ownerRef config.OwnerRef, externalOAuthAddress string) error {
	oauthHostNames := []string{externalOAuthAddress}
	return reconcileSignedCertWithAddresses(secret, ca, ownerRef, "openshift-oauth", "openshift", X509DefaultUsage, X509UsageClientServerAuth, oauthHostNames, nil)
}
