package aws

import (
	"github.com/openshift/hypershift/support/util"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

const (
	Provider          = util.AWSCloudProviderName
	ProviderConfigKey = "aws.conf"
)

func AWSKMSCredsSecret(controlPlaneNamespace string) *corev1.Secret {
	return &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: controlPlaneNamespace,
			Name:      "kms-creds",
		},
	}
}
