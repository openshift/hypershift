package config

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

type AdditionalLabels map[string]string

func (l AdditionalLabels) ApplyTo(podMeta *metav1.ObjectMeta) {
	if len(l) == 0 {
		return
	}
	if podMeta.Labels == nil {
		podMeta.Labels = map[string]string{}
	}
	for k, v := range l {
		podMeta.Labels[k] = v
	}
}
