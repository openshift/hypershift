package config

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

type AdditionalAnnotations map[string]string

func (l AdditionalAnnotations) ApplyTo(podMeta *metav1.ObjectMeta) {
	if len(l) == 0 {
		return
	}
	if podMeta.Annotations == nil {
		podMeta.Annotations = map[string]string{}
	}
	for k, v := range l {
		podMeta.Annotations[k] = v
	}
}
