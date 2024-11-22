package manifests

import (
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"

	operatorv1 "github.com/openshift/api/operator/v1"
	routev1 "github.com/openshift/api/route/v1"
)

func IngressDefaultIngressController() *operatorv1.IngressController {
	return &operatorv1.IngressController{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "default",
			Namespace: "openshift-ingress-operator",
		},
	}
}

func IngressDefaultIngressControllerAsUnstructured() *unstructured.Unstructured {
	src := IngressDefaultIngressController()
	obj := &unstructured.Unstructured{}
	obj.SetAPIVersion(operatorv1.GroupVersion.String())
	obj.SetKind("IngressController")
	obj.SetName(src.Name)
	obj.SetNamespace(src.Namespace)
	return obj
}

func IngressDefaultIngressControllerCert() *corev1.Secret {
	return &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "default-ingress-cert",
			Namespace: "openshift-ingress",
		},
	}
}

func IngressDefaultIngressNodePortService() *corev1.Service {
	return &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "router-nodeport-default",
			Namespace: "openshift-ingress",
		},
	}
}

const IngressDefaultIngressPassthroughServiceName = "default-ingress-passthrough-service"

func IngressDefaultIngressPassthroughService(namespace string) *corev1.Service {
	return &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: namespace,
		},
	}
}

const IngressDefaultIngressPassthroughRouteName = "default-ingress-passthrough-route"

func IngressDefaultIngressPassthroughRoute(namespace string) *routev1.Route {
	return &routev1.Route{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: namespace,
		},
	}
}
