package util

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// HasAnnotationWithValue checks if a Kubernetes object has a specific annotation with a given value.
func HasAnnotationWithValue(obj metav1.Object, key, expectedValue string) bool {
	annotations := obj.GetAnnotations()
	val, ok := annotations[key]
	return ok && val == expectedValue
}
