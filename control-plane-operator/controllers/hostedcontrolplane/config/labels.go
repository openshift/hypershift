package config

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

type AdditionalLabels map[string]string

func (l AdditionalLabels) ApplyTo(podMeta *metav1.ObjectMeta) {
	if len(l) == 0 {
		return
	}
	newLabels := map[string]string{}
	if podMeta.Labels != nil {
		for k, v := range podMeta.Labels {
			newLabels[k] = v
		}
	}
	for k, v := range l {
		newLabels[k] = v
	}
	podMeta.Labels = newLabels
}
