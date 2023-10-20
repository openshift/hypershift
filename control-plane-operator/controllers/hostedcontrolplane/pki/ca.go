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
		for _, key := range []string{certs.OCPCASignerCertMapKey, certs.CASignerCertMapKey} {
			caBytes, ok := src.Data[key]
			if !ok {
				continue
			}
			_, err := fmt.Fprintf(combined, "%s", string(caBytes))
			if err != nil {
				return err
			}
			break // be greedy, only load one value if both are present
		}
	}
	if configMap.Data == nil {
		configMap.Data = map[string]string{}
	}
	configMap.Data[certs.CASignerCertMapKey] = combined.String()
	return nil
}

func ReconcileAggregatorClientSigner(secret *corev1.Secret, ownerRef config.OwnerRef) error {
	return reconcileSelfSignedCA(secret, ownerRef, "aggregator-signer", "openshift")
}

func ReconcileKubeControlPlaneSigner(secret *corev1.Secret, ownerRef config.OwnerRef) error {
	return reconcileSelfSignedCA(secret, ownerRef, "kube-control-plane-signer", "openshift")
}

func ReconcileKASToKubeletSigner(secret *corev1.Secret, ownerRef config.OwnerRef) error {
	return reconcileSelfSignedCA(secret, ownerRef, "kube-apiserver-to-kubelet-signer", "openshift")
}

func ReconcileAdminKubeconfigSigner(secret *corev1.Secret, ownerRef config.OwnerRef) error {
	return reconcileSelfSignedCA(secret, ownerRef, "admin-kubeconfig-signer", "openshift")
}

func ReconcileKubeCSRSigner(secret *corev1.Secret, ownerRef config.OwnerRef) error {
	return reconcileSelfSignedCA(secret, ownerRef, "kube-csr-signer", "openshift")
}

func ReconcileAggregatorClientCA(cm *corev1.ConfigMap, ownerRef config.OwnerRef, signer *corev1.Secret) error {
	return reconcileAggregateCA(cm, ownerRef, signer)
}

func ReconcileTotalClientCA(cm *corev1.ConfigMap, ownerRef config.OwnerRef, signers ...*corev1.Secret) error {
	return reconcileAggregateCA(cm, ownerRef, signers...)
}

func ReconcileKubeletClientCA(cm *corev1.ConfigMap, ownerRef config.OwnerRef, signers ...*corev1.Secret) error {
	return reconcileAggregateCA(cm, ownerRef, signers...)
}

func ReconcileRootCA(secret *corev1.Secret, ownerRef config.OwnerRef) error {
	return reconcileSelfSignedCA(secret, ownerRef, "root-ca", "openshift")
}

func ReconcileEtcdSignerSecret(secret *corev1.Secret, ownerRef config.OwnerRef) error {
	return reconcileSelfSignedCA(secret, ownerRef, "etcd-signer", "openshift")
}

func ReconcileEtcdSignerConfigMap(cm *corev1.ConfigMap, ownerRef config.OwnerRef, etcdSigner *corev1.Secret) error {
	return reconcileAggregateCA(cm, ownerRef, etcdSigner)
}

func ReconcileEtcdMetricsSignerSecret(secret *corev1.Secret, ownerref config.OwnerRef) error {
	return reconcileSelfSignedCA(secret, ownerref, "etcd-metrics-signer", "openshift")
}

func ReconcileEtcdMetricsSignerConfigMap(cm *corev1.ConfigMap, ownerRef config.OwnerRef, etcdMetricsSigner *corev1.Secret) error {
	return reconcileAggregateCA(cm, ownerRef, etcdMetricsSigner)
}

func ReconcileRootCAConfigMap(cm *corev1.ConfigMap, ownerRef config.OwnerRef, rootCA *corev1.Secret, observedDefaultIngressCert *corev1.ConfigMap) error {
	sources := []*corev1.Secret{rootCA}
	if observedDefaultIngressCert != nil {
		sources = append(sources, &corev1.Secret{
			Data: map[string][]byte{
				certs.CASignerCertMapKey: []byte(observedDefaultIngressCert.Data[certs.CASignerCertMapKey]),
			},
		})
	}
	return reconcileAggregateCA(cm, ownerRef, sources...)
}

func ReconcileKonnectivityConfigMap(cm *corev1.ConfigMap, ownerRef config.OwnerRef, konnectivityCA *corev1.Secret) error {
	return reconcileAggregateCA(cm, ownerRef, konnectivityCA)
}
