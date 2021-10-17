package pki

import (
	"github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/config"
	"github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/manifests"
	"github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/util"
	corev1 "k8s.io/api/core/v1"
)

func ReconcileRoksMetricsCertSecret(secret, ca *corev1.Secret, ownerRef config.OwnerRef) error {
	return reconcileSignedCert(secret, ca, ownerRef, "roks-metrics", []string{"openshift"}, X509DefaultUsage, X509UsageClientAuth)
}

func ReconcileRoksMetricsSecret(cm *corev1.ConfigMap, ca *corev1.Secret, ownerRef config.OwnerRef) error {
	ownerRef.ApplyTo(cm)
	secret := manifests.RoksMetricsCert("openshift-roks-metrics")
	if err := ReconcileRoksMetricsCertSecret(secret, ca, config.OwnerRef{}); err != nil {
		return err
	}
	return util.ReconcileWorkerManifest(cm, secret)
}
