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
	case hyperv1.OpenStackPlatform:
		// TODO modify disable console https://docs.openshift.com/container-platform/4.16/web_console/disabling-web-console.html
	}
}
