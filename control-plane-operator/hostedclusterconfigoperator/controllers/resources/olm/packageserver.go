package olm

import (
	corev1 "k8s.io/api/core/v1"
	"k8s.io/utils/ptr"

	apiregistrationv1 "k8s.io/kube-aggregator/pkg/apis/apiregistration/v1"
)

func ReconcilePackageServerAPIService(apiService *apiregistrationv1.APIService, ca *corev1.Secret) {
	apiService.Spec = apiregistrationv1.APIServiceSpec{
		CABundle:             ca.Data["ca.crt"],
		Group:                "packages.operators.coreos.com",
		GroupPriorityMinimum: 2000,
		Service: &apiregistrationv1.ServiceReference{
			Name:      "packageserver",
			Namespace: "default",
			Port:      ptr.To[int32](443),
		},
		Version:         "v1",
		VersionPriority: 15,
	}
}

func ReconcilePackageServerService(service *corev1.Service) {
	service.Spec.Ports = []corev1.ServicePort{
		{
			Name: "https",
			Port: 443,
		},
	}
}

func ReconcilePackageServerEndpoints(ep *corev1.Endpoints, serviceIP string) {
	ep.Subsets = []corev1.EndpointSubset{
		{
			Addresses: []corev1.EndpointAddress{
				{
					IP: serviceIP,
				},
			},
			Ports: []corev1.EndpointPort{
				{
					Name:     "https",
					Port:     443,
					Protocol: corev1.ProtocolTCP,
				},
			},
		},
	}
}
