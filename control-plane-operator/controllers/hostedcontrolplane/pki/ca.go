package pki

import (
	"bytes"
	"fmt"

	corev1 "k8s.io/api/core/v1"

	"github.com/openshift/hypershift/support/certs"
	"github.com/openshift/hypershift/support/config"
)

func reconcileSelfSignedCA(secret *corev1.Secret, ownerRef config.OwnerRef, cn, ou string) error {
	ownerRef.ApplyTo(secret)
	secret.Type = corev1.SecretTypeOpaque
	return certs.ReconcileSelfSignedCA(secret, cn, ou)
}

func reconcileAggregateCA(configMap *corev1.ConfigMap, ownerRef config.OwnerRef, sources ...*corev1.Secret) error {
	ownerRef.ApplyTo(configMap)
	combined := &bytes.Buffer{}
	for _, src := range sources {
		ca_bytes := src.Data[certs.CASignerCertMapKey]
		fmt.Fprintf(combined, "%s", string(ca_bytes))
	}
	if configMap.Data == nil {
		configMap.Data = map[string]string{}
	}
	configMap.Data[certs.CASignerCertMapKey] = combined.String()
	return nil
}

func ReconcileRootCA(secret *corev1.Secret, ownerRef config.OwnerRef) error {
	return reconcileSelfSignedCA(secret, ownerRef, "root-ca", "openshift")
}

func ReconcileClusterSignerCA(secret *corev1.Secret, ownerRef config.OwnerRef) error {
	return reconcileSelfSignedCA(secret, ownerRef, "cluster-signer", "openshift")
}

func ReconcileCombinedCA(cm *corev1.ConfigMap, ownerRef config.OwnerRef, rootCA, signerCA *corev1.Secret) error {
	return reconcileAggregateCA(cm, ownerRef, rootCA, signerCA)
}
