package manifests

import (
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func AWSEBSCsiDriverOperatorMetricsService(namespace string) *corev1.Service {
	return &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "aws-ebs-csi-driver-operator-metrics",
			Namespace: namespace,
		},
		Spec: corev1.ServiceSpec{
			ClusterIP: "None",
		},
	}
}
