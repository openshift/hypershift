package console

import (
	operatorv1 "github.com/openshift/api/operator/v1"
	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func ConsoleOperator() *operatorv1.Console {
	return &operatorv1.Console{
		ObjectMeta: metav1.ObjectMeta{
			Name: "cluster",
		},
	}
}

func ReconcileConsoleOperator(console *operatorv1.Console, platformType hyperv1.PlatformType) {
	switch platformType {
	// Ingress is not available yet for the OpenStack platform, therefore we need to disable the console operator
	case hyperv1.OpenStackPlatform:
		console.Spec.ManagementState = operatorv1.Removed
	}
}
