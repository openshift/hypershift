package controlplaneautoscaler

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	autoscalingv1 "k8s.io/autoscaler/vertical-pod-autoscaler/pkg/apis/autoscaling.k8s.io/v1"
)

func KubeAPIServerVerticalPodAutoscaler(ns string) *autoscalingv1.VerticalPodAutoscaler {
	return &autoscalingv1.VerticalPodAutoscaler{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "kube-apiserver",
			Namespace: ns,
		},
	}
}
