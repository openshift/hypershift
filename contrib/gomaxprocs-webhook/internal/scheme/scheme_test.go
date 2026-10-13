package scheme

import (
	appsv1 "k8s.io/api/apps/v1"
	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
)

// Basic compile-time style check that required GVKs are registered
func _() {
	s := New()
	_ = s.Recognizes(corev1.SchemeGroupVersion.WithKind("Pod"))
	_ = s.Recognizes(appsv1.SchemeGroupVersion.WithKind("ReplicaSet"))
	_ = s.Recognizes(appsv1.SchemeGroupVersion.WithKind("Deployment"))
	_ = s.Recognizes(batchv1.SchemeGroupVersion.WithKind("Job"))
	_ = s.Recognizes(batchv1.SchemeGroupVersion.WithKind("CronJob"))
}
