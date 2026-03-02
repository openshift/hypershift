package primaryudn

import (
	"context"
	"fmt"

	configv1 "github.com/openshift/api/config/v1"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

func guestIngressDomain(ctx context.Context, c client.Client) (string, error) {
	ing := &configv1.Ingress{ObjectMeta: metav1.ObjectMeta{Name: "cluster"}}
	if err := c.Get(ctx, client.ObjectKeyFromObject(ing), ing); err != nil {
		return "", err
	}
	if ing.Spec.Domain == "" {
		return "", fmt.Errorf("guest ingress domain is empty")
	}
	return ing.Spec.Domain, nil
}

func guestRouterInternalClusterIP(ctx context.Context, c client.Client) (string, error) {
	const (
		routerNamespace = "openshift-ingress"
		routerService   = "router-internal-default"
	)
	svc := &corev1.Service{ObjectMeta: metav1.ObjectMeta{Namespace: routerNamespace, Name: routerService}}
	if err := c.Get(ctx, client.ObjectKeyFromObject(svc), svc); err != nil {
		return "", err
	}
	if svc.Spec.ClusterIP == "" || svc.Spec.ClusterIP == "None" {
		return "", fmt.Errorf("%s/%s has no ClusterIP yet", routerNamespace, routerService)
	}
	return svc.Spec.ClusterIP, nil
}

func guestDNSImage(ctx context.Context, c client.Client) (string, error) {
	ds := &appsv1.DaemonSet{ObjectMeta: metav1.ObjectMeta{Namespace: "openshift-dns", Name: "dns-default"}}
	if err := c.Get(ctx, client.ObjectKeyFromObject(ds), ds); err != nil {
		return "", err
	}
	if len(ds.Spec.Template.Spec.Containers) == 0 || ds.Spec.Template.Spec.Containers[0].Image == "" {
		return "", fmt.Errorf("openshift-dns/dns-default has no container image")
	}
	return ds.Spec.Template.Spec.Containers[0].Image, nil
}
