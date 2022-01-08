package olm

import (
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/util/intstr"
	"k8s.io/utils/pointer"

	prometheusoperatorv1 "github.com/prometheus-operator/prometheus-operator/pkg/apis/monitoring/v1"
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
			Port:      pointer.Int32(443),
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

func ReconcileOLMAlertRules(rule *prometheusoperatorv1.PrometheusRule) {
	if rule.Labels == nil {
		rule.Labels = map[string]string{}
	}
	rule.Labels["prometheus"] = "alert-rules"
	rule.Labels["role"] = "alert-rules"
	rule.Spec.Groups = []prometheusoperatorv1.RuleGroup{
		{
			Name: "olm.csv_abnormal.rules",
			Rules: []prometheusoperatorv1.Rule{
				{
					Alert: "CsvAbnormalFailedOver2Min",
					Expr:  intstr.FromString(`csv_abnormal{phase=~"^Failed$"}`),
					For:   "2m",
					Labels: map[string]string{
						"severity":  "warning",
						"namespace": "{{ $labels.namespace }}",
					},
					Annotations: map[string]string{
						"message": "Failed to install Operator {{ $labels.name }} version {{ $labels.version }}. Reason-{{ $labels.reason }}",
					},
				},
				{
					Alert: "CsvAbnormalOver30Min",
					Expr:  intstr.FromString(`csv_abnormal{phase=~"(^Replacing$|^Pending$|^Deleting$|^Unknown$)"}`),
					For:   "30m",
					Labels: map[string]string{
						"severity":  "warning",
						"namespace": "{{ $labels.namespace }}",
					},
					Annotations: map[string]string{
						"message": "Failed to install Operator {{ $labels.name }} version {{ $labels.version }}. Phase-{{ $labels.phase }} Reason-{{ $labels.reason }}",
					},
				},
			},
		},
	}
}
