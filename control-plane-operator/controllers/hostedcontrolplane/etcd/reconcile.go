package etcd

import (
	"crypto/x509"
	"crypto/x509/pkix"
	"fmt"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/utils/pointer"

	"github.com/openshift/hypershift/certs"
	"github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/pki"
	etcdv1 "github.com/openshift/hypershift/thirdparty/etcd/v1beta2"
)

// Etcd secret keys
const (
	ClientCrtKey = "etcd-client.crt"
	ClientKeyKey = "etcd-client.key"
	ClientCAKey  = "etcd-client-ca.crt"

	ServerCrtKey = "server.crt"
	ServerKeyKey = "server.key"
	ServerCAKey  = "server-ca.crt"

	PeerCrtKey = "peer.crt"
	PeerKeyKey = "peer.key"
	PeerCAKey  = "peer-ca.crt"
)

func ReconcileOperatorRole(role *rbacv1.Role) error {
	role.Rules = []rbacv1.PolicyRule{
		{
			APIGroups: []string{
				etcdv1.SchemeGroupVersion.Group,
			},
			Resources: []string{
				"etcdclusters",
				"etcdbackups",
				"etcdrestores",
			},
			Verbs: []string{
				"*",
			},
		},
		{
			APIGroups: []string{
				corev1.SchemeGroupVersion.Group,
			},
			Resources: []string{
				"pods",
				"services",
				"endpoints",
				"persistentvolumeclaims",
				"events",
			},
			Verbs: []string{
				"*",
			},
		},
		{
			APIGroups: []string{
				appsv1.SchemeGroupVersion.Group,
			},
			Resources: []string{
				"deployments",
			},
			Verbs: []string{
				"*",
			},
		},
		{
			APIGroups: []string{
				corev1.SchemeGroupVersion.Group,
			},
			Resources: []string{
				"secrets",
			},
			Verbs: []string{
				"get",
			},
		},
	}
	return nil
}

func ReconcileOperatorRoleBinding(roleBinding *rbacv1.RoleBinding) error {
	serviceAccount := OperatorServiceAccount(roleBinding.Namespace)
	roleBinding.RoleRef = rbacv1.RoleRef{
		APIGroup: rbacv1.SchemeGroupVersion.Group,
		Kind:     "Role",
		Name:     "etcd-operator",
	}
	roleBinding.Subjects = []rbacv1.Subject{
		{
			Kind:      "ServiceAccount",
			APIGroup:  corev1.SchemeGroupVersion.Group,
			Namespace: serviceAccount.Namespace,
			Name:      serviceAccount.Name,
		},
	}
	return nil
}

var etcdOperatorDeploymentLabels = map[string]string{
	"name": "etcd-operator",
}

func ReconcileOperatorDeployment(deployment *appsv1.Deployment, operatorImage string) error {
	serviceAccount := OperatorServiceAccount(deployment.Namespace)
	deployment.Spec = appsv1.DeploymentSpec{
		Replicas: pointer.Int32Ptr(1),
		Selector: &metav1.LabelSelector{
			MatchLabels: etcdOperatorDeploymentLabels,
		},
		Template: corev1.PodTemplateSpec{
			ObjectMeta: metav1.ObjectMeta{
				Labels: etcdOperatorDeploymentLabels,
			},
			Spec: corev1.PodSpec{
				ServiceAccountName: serviceAccount.Name,
				Containers: []corev1.Container{
					{
						Name:  "etcd-operator",
						Image: operatorImage,
						Command: []string{
							"etcd-operator",
						},
						Args: []string{
							"-create-crd=false",
						},
						Env: []corev1.EnvVar{
							{
								Name: "MY_POD_NAMESPACE",
								ValueFrom: &corev1.EnvVarSource{
									FieldRef: &corev1.ObjectFieldSelector{
										FieldPath: "metadata.namespace",
									},
								},
							},
							{
								Name: "MY_POD_NAME",
								ValueFrom: &corev1.EnvVarSource{
									FieldRef: &corev1.ObjectFieldSelector{
										FieldPath: "metadata.name",
									},
								},
							},
						},
					},
				},
			},
		},
	}
	return nil
}

func ReconcileCluster(cluster *etcdv1.EtcdCluster, size int, version string) error {
	peerSecret := PeerSecret(cluster.Namespace)
	serverSecret := ServerSecret(cluster.Namespace)
	clientSecret := ClientSecret(cluster.Namespace)

	cluster.Spec = etcdv1.ClusterSpec{
		Size:    size,
		Version: version,
		TLS: &etcdv1.TLSPolicy{
			Static: &etcdv1.StaticTLS{
				Member: &etcdv1.MemberSecret{
					PeerSecret:   peerSecret.Name,
					ServerSecret: serverSecret.Name,
				},
				OperatorSecret: clientSecret.Name,
			},
		},
	}
	return nil
}

func ReconcileClientSecret(secret, ca *corev1.Secret) error {
	if !pki.ValidCA(ca) {
		return fmt.Errorf("Invalid CA signer secret %s", ca.Name)
	}
	expectedKeys := []string{ClientCrtKey, ClientKeyKey, ClientCAKey}
	secret.Type = corev1.SecretTypeOpaque
	if !pki.SignedSecretUpToDate(secret, ca, expectedKeys) {
		cfg := &certs.CertCfg{
			Subject:      pkix.Name{CommonName: "etcd-client", Organization: []string{"kubernetes"}},
			KeyUsages:    x509.KeyUsageKeyEncipherment | x509.KeyUsageDigitalSignature,
			ExtKeyUsages: []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth},
			Validity:     certs.ValidityOneYear,
		}
		certBytes, keyBytes, caBytes, err := pki.SignCertificate(cfg, ca)
		if err != nil {
			return fmt.Errorf("error signing secret: %w", err)
		}
		if secret.Data == nil {
			secret.Data = map[string][]byte{}
		}
		secret.Data[ClientCrtKey] = certBytes
		secret.Data[ClientKeyKey] = keyBytes
		secret.Data[ClientCAKey] = caBytes
		pki.AnnotateWithCA(secret, ca)
	}
	return nil
}

func ReconcileServerSecret(secret, ca *corev1.Secret) error {
	if !pki.ValidCA(ca) {
		return fmt.Errorf("Invalid CA signer secret %s", ca.Name)
	}
	secret.Type = corev1.SecretTypeOpaque
	expectedKeys := []string{ServerCrtKey, ServerKeyKey, ServerCAKey}
	if !pki.SignedSecretUpToDate(secret, ca, expectedKeys) {
		dnsNames := []string{
			fmt.Sprintf("*.etcd.%s.svc", secret.Namespace),
			fmt.Sprintf("etcd-client.%s.svc", secret.Namespace),
			fmt.Sprintf("*.etcd.%s.svc.cluster.local", secret.Namespace),
			fmt.Sprintf("etcd-client.%s.svc.cluster.local", secret.Namespace),
			"etcd",
			"etcd-client",
			"localhost",
		}
		cfg := &certs.CertCfg{
			Subject:      pkix.Name{CommonName: "etcd-server", Organization: []string{"kubernetes"}},
			KeyUsages:    x509.KeyUsageKeyEncipherment | x509.KeyUsageDigitalSignature,
			ExtKeyUsages: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth, x509.ExtKeyUsageClientAuth},
			Validity:     certs.ValidityOneYear,
			DNSNames:     dnsNames,
		}
		certBytes, keyBytes, caBytes, err := pki.SignCertificate(cfg, ca)
		if err != nil {
			return fmt.Errorf("error signing secret: %w", err)
		}
		if secret.Data == nil {
			secret.Data = map[string][]byte{}
		}
		secret.Data[ServerCrtKey] = certBytes
		secret.Data[ServerKeyKey] = keyBytes
		secret.Data[ServerCAKey] = caBytes
		pki.AnnotateWithCA(secret, ca)
	}
	return nil
}

func ReconcilePeerSecret(secret, ca *corev1.Secret) error {
	if !pki.ValidCA(ca) {
		return fmt.Errorf("Invalid CA signer secret %s", ca.Name)
	}
	secret.Type = corev1.SecretTypeOpaque
	expectedKeys := []string{PeerCrtKey, PeerKeyKey, PeerCAKey}
	if !pki.SignedSecretUpToDate(secret, ca, expectedKeys) {
		dnsNames := []string{
			fmt.Sprintf("*.etcd.%s.svc", secret.Namespace),
			fmt.Sprintf("*.etcd.%s.svc.cluster.local", secret.Namespace),
		}
		cfg := &certs.CertCfg{
			Subject:      pkix.Name{CommonName: "etcd-peer", Organization: []string{"kubernetes"}},
			KeyUsages:    x509.KeyUsageKeyEncipherment | x509.KeyUsageDigitalSignature,
			ExtKeyUsages: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth, x509.ExtKeyUsageClientAuth},
			Validity:     certs.ValidityOneYear,
			DNSNames:     dnsNames,
		}
		certBytes, keyBytes, caBytes, err := pki.SignCertificate(cfg, ca)
		if err != nil {
			return fmt.Errorf("error signing secret: %w", err)
		}
		if secret.Data == nil {
			secret.Data = map[string][]byte{}
		}
		secret.Data[PeerCrtKey] = certBytes
		secret.Data[PeerKeyKey] = keyBytes
		secret.Data[PeerCAKey] = caBytes
		pki.AnnotateWithCA(secret, ca)
	}
	return nil
}
