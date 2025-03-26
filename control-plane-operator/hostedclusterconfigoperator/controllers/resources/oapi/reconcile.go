package oapi

import (
	"fmt"

	corev1 "k8s.io/api/core/v1"
	apiregistrationv1 "k8s.io/kube-aggregator/pkg/apis/apiregistration/v1"
	"k8s.io/utils/ptr"
)

const (
	OpenShiftServicePort        = 443
	openshiftAPIServerConfigKey = "config.yaml"
	configNamespace             = "openshift-config"
	configHashAnnotation        = "openshift-apiserver.hypershift.openshift.io/config-hash"
	crdPresentAnnotation        = "openshift-apiserver.hypershift.openshift.io/rolebindingrestrictions-present"
)

func ReconcileAPIService(apiService *apiregistrationv1.APIService, svc *corev1.Service, ca *corev1.Secret, group string) {
	groupName := fmt.Sprintf("%s.openshift.io", group)
	caBundle := ca.Data["ca.crt"]
	apiService.Spec = apiregistrationv1.APIServiceSpec{
		CABundle:             caBundle,
		Group:                groupName,
		Version:              "v1",
		GroupPriorityMinimum: 9900,
		Service: &apiregistrationv1.ServiceReference{
			Name:      svc.Name,
			Namespace: svc.Namespace,
			Port:      ptr.To[int32](443),
		},
		VersionPriority: 15,
	}
}

func ReconcileEndpoints(ep *corev1.Endpoints, clusterIP string) {
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
}

func ReconcileClusterService(svc *corev1.Service) {
	svc.Spec.Ports = []corev1.ServicePort{
		{
			Name: "https",
			Port: OpenShiftServicePort,
		},
	}
}
