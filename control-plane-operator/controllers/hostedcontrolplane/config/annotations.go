package config

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

type AdditionalAnnotations map[string]string

func (l AdditionalAnnotations) ApplyTo(podMeta *metav1.ObjectMeta) {
	if len(l) == 0 {
		return
	}
	newAnnotations := map[string]string{}
	if podMeta.Annotations != nil {
		for k, v := range podMeta.Annotations {
			newAnnotations[k] = v
		}
	}
	for k, v := range l {
		newAnnotations[k] = v
	}
	podMeta.Annotations = newAnnotations
}
