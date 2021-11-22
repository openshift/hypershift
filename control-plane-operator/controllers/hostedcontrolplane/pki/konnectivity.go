package pki

import (
	"fmt"

	"github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/manifests"
	"github.com/openshift/hypershift/support/config"
	"github.com/openshift/hypershift/support/util"
	corev1 "k8s.io/api/core/v1"
	"sigs.k8s.io/yaml"
)

func ReconcileKonnectivityServerSecret(secret, ca *corev1.Secret, ownerRef config.OwnerRef) error {
	dnsNames := []string{
		"konnectivity-server-local",
		fmt.Sprintf("konnectivity-server-local.%s.svc", secret.Namespace),
		fmt.Sprintf("konnectivity-server-local.%s.svc.cluster.local", secret.Namespace),
	}
	return reconcileSignedCertWithAddresses(secret, ca, ownerRef, "konnectivity-server-local", []string{"kubernetes"}, X509DefaultUsage, X509UsageServerAuth, dnsNames, nil)
}

func ReconcileKonnectivityClusterSecret(secret, ca *corev1.Secret, ownerRef config.OwnerRef, externalKconnectivityAddress string) error {
	dnsNames := []string{
		"konnectivity-server",
		fmt.Sprintf("konnectivity-server.%s.svc", secret.Namespace),
		fmt.Sprintf("konnectivity-server.%s.svc.cluster.local", secret.Namespace),
	}
	ips := []string{}
	if isNumericIP(externalKconnectivityAddress) {
		ips = append(ips, externalKconnectivityAddress)
	} else {
		dnsNames = append(dnsNames, externalKconnectivityAddress)
	}
	return reconcileSignedCertWithAddresses(secret, ca, ownerRef, "konnectivity-server", []string{"kubernetes"}, X509DefaultUsage, X509UsageServerAuth, dnsNames, ips)
}

func ReconcileKonnectivityClientSecret(secret, ca *corev1.Secret, ownerRef config.OwnerRef) error {
	return reconcileSignedCert(secret, ca, ownerRef, "konnectivity-client", []string{"kubernetes"}, X509DefaultUsage, X509UsageClientAuth)
}

func ReconcileKonnectivityAgentSecret(secret, ca *corev1.Secret, ownerRef config.OwnerRef) error {
	return reconcileSignedCert(secret, ca, ownerRef, "konnectivity-agent", []string{"kubernetes"}, X509DefaultUsage, X509UsageClientAuth)
}

func ReconcileKonnectivityWorkerAgentSecret(cm *corev1.ConfigMap, ca *corev1.Secret, ownerRef config.OwnerRef) error {
	ownerRef.ApplyTo(cm)
	secret := manifests.KonnectivityAgentSecret("kube-system")
	// Ignore errors here, the configmap might be empty initially
	yaml.Unmarshal([]byte(cm.Data[util.UserDataKey]), secret)
	if err := ReconcileKonnectivityAgentSecret(secret, ca, config.OwnerRef{}); err != nil {
		return err
	}
	return util.ReconcileWorkerManifest(cm, secret)
}
