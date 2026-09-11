package cpo

import (
	appsv1 "k8s.io/api/apps/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func EtcdStatefulSet(ns string) *appsv1.StatefulSet {
	return &appsv1.StatefulSet{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "etcd",
			Namespace: ns,
		},
	}
}
