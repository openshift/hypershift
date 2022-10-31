package manifests

import (
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func secretFor(ns, name string) *corev1.Secret {
	return &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: ns,
		},
	}
}

func RootCASecret(ns string) *corev1.Secret { return secretFor(ns, "root-ca") }

func ClusterSignerCASecret(ns string) *corev1.Secret { return secretFor(ns, "cluster-signer-ca") }

func CombinedCAConfigMap(ns string) *corev1.ConfigMap {
	return &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "combined-ca",
			Namespace: ns,
		},
	}
}

func AggregateClientCAConfigMap(ns string) *corev1.ConfigMap {
	return &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "aggregator-client-ca",
			Namespace: ns,
		},
	}
}

func MetricsClientCertSecret(ns string) *corev1.Secret { return secretFor(ns, "metrics-client") }

func UserCAConfigMap(ns string) *corev1.ConfigMap {
	return &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "user-ca-bundle",
			Namespace: ns,
		},
	}
}

func EtcdClientSecret(ns string) *corev1.Secret { return secretFor(ns, "etcd-client-tls") }

func EtcdServerSecret(ns string) *corev1.Secret { return secretFor(ns, "etcd-server-tls") }

func EtcdPeerSecret(ns string) *corev1.Secret { return secretFor(ns, "etcd-peer-tls") }

func KASServerCertSecret(ns string) *corev1.Secret { return secretFor(ns, "kas-server-crt") }

func KASKubeletClientCertSecret(ns string) *corev1.Secret {
	return secretFor(ns, "kas-kubelet-client-crt")
}

func AggregateClientSigner(ns string) *corev1.Secret {
	return secretFor(ns, "kas-aggregator-client-signer")
}

func KASAggregatorCertSecret(ns string) *corev1.Secret { return secretFor(ns, "kas-aggregator-crt") }

func KASAdminClientCertSecret(ns string) *corev1.Secret { return secretFor(ns, "kas-admin-client") }

func KASMachineBootstrapClientCertSecret(ns string) *corev1.Secret {
	return secretFor(ns, "kas-bootstrap-client")
}

func KCMServerCertSecret(ns string) *corev1.Secret { return secretFor(ns, "kcm-server") }

func ServiceAccountSigningKeySecret(ns string) *corev1.Secret { return secretFor(ns, "sa-signing-key") }

func OpenShiftAPIServerCertSecret(ns string) *corev1.Secret {
	return secretFor(ns, "openshift-apiserver-cert")
}

func OpenShiftOAuthAPIServerCertSecret(ns string) *corev1.Secret {
	return secretFor(ns, "openshift-oauth-apiserver-cert")
}

func OpenshiftAuthenticatorCertSecret(ns string) *corev1.Secret {
	return secretFor(ns, "openshift-authenticator-cert")
}

func OpenShiftControllerManagerCertSecret(ns string) *corev1.Secret {
	return secretFor(ns, "openshift-controller-manager-cert")
}

func OpenShiftRouteControllerManagerCertSecret(ns string) *corev1.Secret {
	return secretFor(ns, "openshift-route-controller-manager-cert")
}

func ClusterPolicyControllerCertSecret(ns string) *corev1.Secret {
	return secretFor(ns, "cluster-policy-controller-cert")
}

func KonnectivityServerSecret(ns string) *corev1.Secret { return secretFor(ns, "konnectivity-server") }

func KonnectivityClusterSecret(ns string) *corev1.Secret {
	return secretFor(ns, "konnectivity-cluster")
}

func KonnectivityClientSecret(ns string) *corev1.Secret { return secretFor(ns, "konnectivity-client") }

func KonnectivityAgentSecret(ns string) *corev1.Secret { return secretFor(ns, "konnectivity-agent") }

func IngressCert(ns string) *corev1.Secret { return secretFor(ns, "ingress-crt") }

func OpenShiftOAuthServerCert(ns string) *corev1.Secret { return secretFor(ns, "oauth-server-crt") }

func MachineConfigServerCert(ns string) *corev1.Secret { return secretFor(ns, "mcs-crt") }

func OLMPackageServerCertSecret(ns string) *corev1.Secret { return secretFor(ns, "packageserver-cert") }

func OLMOperatorServingCertSecret(ns string) *corev1.Secret {
	return secretFor(ns, "olm-operator-serving-cert")
}

func OLMCatalogOperatorServingCertSecret(ns string) *corev1.Secret {
	return secretFor(ns, "catalog-operator-serving-cert")
}

func KASSecretEncryptionConfigFile(ns string) *corev1.Secret {
	return secretFor(ns, "kas-secret-encryption-config")
}

func IBMCloudKASKMSWDEKSecret(ns string) *corev1.Secret { return secretFor(ns, "kp-wdek-secret") }

func ClusterVersionOperatorServerCertSecret(ns string) *corev1.Secret {
	return secretFor(ns, "cvo-server")
}

func AWSPodIdentityWebhookServingCert(ns string) *corev1.Secret {
	return secretFor(ns, "aws-pod-identity-webhook-serving-cert")
}
