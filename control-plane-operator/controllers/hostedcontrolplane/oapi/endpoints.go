package oapi

import (
	corev1 "k8s.io/api/core/v1"

	"github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/config"
	"github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/util"
)

func ReconcileWorkerEndpoints(cm *corev1.ConfigMap, ownerRef config.OwnerRef, endpoints *corev1.Endpoints, clusterIP string) error {
	if err := reconcileEndpoints(endpoints, clusterIP); err != nil {
		return err
	}
	ownerRef.ApplyTo(cm)
	util.ReconcileWorkerManifest(cm, endpoints)
	return nil
}

func reconcileEndpoints(ep *corev1.Endpoints, clusterIP string) error {
	ep.Subsets = []corev1.EndpointSubset{
		{
			Addresses: []corev1.EndpointAddress{
				{
					IP: clusterIP,
				},
			},
			Ports: []corev1.EndpointPort{
				{
					Name:     "https",
					Port:     OpenShiftServicePort,
					Protocol: corev1.ProtocolTCP,
				},
			},
		},
	}
	return nil
}
