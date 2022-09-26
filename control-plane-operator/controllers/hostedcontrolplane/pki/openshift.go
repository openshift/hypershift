package pki

import (
	"fmt"

	corev1 "k8s.io/api/core/v1"

	"github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/manifests"
	"github.com/openshift/hypershift/support/config"
)

func ReconcileOpenShiftAPIServerCertSecret(secret, ca *corev1.Secret, ownerRef config.OwnerRef) error {
	dnsNames := []string{
		"openshift-apiserver",
		fmt.Sprintf("openshift-apiserver.%s.svc", secret.Namespace),
		fmt.Sprintf("openshift-apiserver.%s.svc.cluster.local", secret.Namespace),
		"openshift-apiserver.default.svc",
		"openshift-apiserver.default.svc.cluster.local",
	}
	return reconcileSignedCertWithAddresses(secret, ca, ownerRef, "openshift-apiserver", []string{"openshift"}, X509UsageClientServerAuth, dnsNames, nil)
}

func ReconcileOpenShiftOAuthAPIServerCertSecret(secret, ca *corev1.Secret, ownerRef config.OwnerRef) error {
	dnsNames := []string{
		"openshift-oauth-apiserver",
		fmt.Sprintf("openshift-oauth-apiserver.%s.svc", secret.Namespace),
		fmt.Sprintf("openshift-oauth-apiserver.%s.svc.cluster.local", secret.Namespace),
		"openshift-oauth-apiserver.default.svc",
		"openshift-oauth-apiserver.default.svc.cluster.local",
	}
	return reconcileSignedCertWithAddresses(secret, ca, ownerRef, "openshift-oauth-apiserver", []string{"openshift"}, X509UsageClientServerAuth, dnsNames, nil)
}

func ReconcileOpenShiftAuthenticatorCertSecret(secret, ca *corev1.Secret, ownerRef config.OwnerRef) error {
	return reconcileSignedCertWithAddresses(secret, ca, ownerRef, "system:serviceaccount:openshift-oauth-apiserver:openshift-authenticator", []string{"openshift"}, X509UsageClientAuth, nil, nil)
}

func ReconcileOpenShiftControllerManagerCertSecret(secret, ca *corev1.Secret, ownerRef config.OwnerRef) error {
	dnsNames := []string{
		"openshift-controller-manager",
		fmt.Sprintf("openshift-controller-manager.%s.svc", secret.Namespace),
		fmt.Sprintf("openshift-controller-manager.%s.svc.cluster.local", secret.Namespace),
	}
	return reconcileSignedCertWithAddresses(secret, ca, ownerRef, "openshift-controller-manager", []string{"openshift"}, X509UsageClientServerAuth, dnsNames, nil)
}

func ReconcileClusterPolicyControllerCertSecret(secret, ca *corev1.Secret, ownerRef config.OwnerRef) error {
	dnsNames := []string{
		"cluster-policy-controller",
		fmt.Sprintf("openshift-controller-manager.%s.svc", secret.Namespace),
		fmt.Sprintf("openshift-controller-manager.%s.svc.cluster.local", secret.Namespace),
	}
	return reconcileSignedCertWithAddresses(secret, ca, ownerRef, "cluster-policy-controller", []string{"openshift"}, X509UsageClientServerAuth, dnsNames, nil)
}

func ReconcileOpenShiftRouteControllerManagerCertSecret(secret, ca *corev1.Secret, ownerRef config.OwnerRef) error {
	svcName := manifests.OpenShiftRouteControllerManagerService("").Name
	dnsNames := []string{
		svcName,
		fmt.Sprintf("%s.%s.svc", svcName, secret.Namespace),
		fmt.Sprintf("%s.%s.svc.cluster.local", svcName, secret.Namespace),
	}
	return reconcileSignedCertWithAddresses(secret, ca, ownerRef, svcName, []string{"openshift"}, X509UsageClientServerAuth, dnsNames, nil)
}
