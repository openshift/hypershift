package manifests

import (
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func RootCASecret(ns string) *corev1.Secret {
	return &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "root-ca",
			Namespace: ns,
		},
	}
}

func ClusterSignerCASecret(ns string) *corev1.Secret {
	return &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "cluster-signer-ca",
			Namespace: ns,
		},
	}
}

func CombinedCAConfigMap(ns string) *corev1.ConfigMap {
	return &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "combined-ca",
			Namespace: ns,
		},
	}
}

func EtcdClientSecret(ns string) *corev1.Secret {
	return &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "etcd-client-tls",
			Namespace: ns,
		},
	}
}

func EtcdServerSecret(ns string) *corev1.Secret {
	return &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "etcd-server-tls",
			Namespace: ns,
		},
	}
}

func EtcdPeerSecret(ns string) *corev1.Secret {
	return &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "etcd-peer-tls",
			Namespace: ns,
		},
	}
}

func KASServerCertSecret(ns string) *corev1.Secret {
	return &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "kas-server-crt",
			Namespace: ns,
		},
	}
}

func KASKubeletClientCertSecret(ns string) *corev1.Secret {
	return &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "kas-kubelet-client-crt",
			Namespace: ns,
		},
	}
}

func KASAggregatorCertSecret(ns string) *corev1.Secret {
	return &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "kas-aggregator-crt",
			Namespace: ns,
		},
	}
}

func KASAdminClientCertSecret(ns string) *corev1.Secret {
	return &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "kas-admin-client",
			Namespace: ns,
		},
	}
}

func KASMachineBootstrapClientCertSecret(ns string) *corev1.Secret {
	return &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "kas-bootstrap-client",
			Namespace: ns,
		},
	}
}

func ServiceAccountSigningKeySecret(ns string) *corev1.Secret {
	return &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "sa-signing-key",
			Namespace: ns,
		},
	}
}

func OpenShiftAPIServerCertSecret(ns string) *corev1.Secret {
	return &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "openshift-apiserver-cert",
			Namespace: ns,
		},
	}
}

func OpenShiftOAuthAPIServerCertSecret(ns string) *corev1.Secret {
	return &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "openshift-oauth-apiserver-cert",
			Namespace: ns,
		},
	}
}

func OpenShiftControllerManagerCertSecret(ns string) *corev1.Secret {
	return &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "openshift-controller-manager-cert",
			Namespace: ns,
		},
	}
}

func ClusterPolicyControllerCertSecret(ns string) *corev1.Secret {
	return &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "cluster-policy-controller-cert",
			Namespace: ns,
		},
	}
}

func KonnectivityServerSecret(ns string) *corev1.Secret {
	return &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "konnectivity-server",
			Namespace: ns,
		},
	}
}

func KonnectivityClusterSecret(ns string) *corev1.Secret {
	return &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "konnectivity-cluster",
			Namespace: ns,
		},
	}
}

func KonnectivityClientSecret(ns string) *corev1.Secret {
	return &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "konnectivity-client",
			Namespace: ns,
		},
	}
}

func KonnectivityAgentSecret(ns string) *corev1.Secret {
	return &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "konnectivity-agent",
			Namespace: ns,
		},
	}
}

func KonnectivityWorkerAgentSecret(ns string) *corev1.ConfigMap {
	return &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "user-manifest-konnectivity-agent-secret",
			Namespace: ns,
		},
	}
}

func IngressCert(ns string) *corev1.Secret {
	return &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "ingress-crt",
			Namespace: ns,
		},
	}
}

func OpenShiftOAuthServerCert(ns string) *corev1.Secret {
	return &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "oauth-server-crt",
			Namespace: ns,
		},
	}
}

func MachineConfigServerCert(ns string) *corev1.Secret {
	return &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "mcs-crt",
			Namespace: ns,
		},
	}
}

func OLMPackageServerCertSecret(ns string) *corev1.Secret {
	return &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "packageserver-cert",
			Namespace: ns,
		},
	}
}

func KASAuditWebhookConfigFile(ns string) *corev1.Secret {
	return &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "kas-audit-webhook",
			Namespace: ns,
		},
	}
}

func KASKMSConfigFile(ns string) *corev1.ConfigMap {
	return &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "kms-config",
			Namespace: ns,
		},
	}
}

func KASKMSWDEKSecret(ns string) *corev1.Secret {
	return &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "kp-wdek-secret",
			Namespace: ns,
		},
	}
}
