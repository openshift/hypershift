package manifests

import (
	batchv1 "k8s.io/api/batch/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func FeatureGateGenerationJob(ns string) *batchv1.Job {
	return &batchv1.Job{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "featuregate-generator",
			Namespace: ns,
		},
	}
}
