package primaryudn

import (
	"context"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
)

const (
	oauthBridgeNamespace = "openshift-authentication"
	oauthBridgeName      = "oauth-bridge"
	oauthListenPort      = 6443
)

func ensureGuestOAuthBridge(ctx context.Context, c client.Client, oauthUDNIP string) (serviceClusterIP string, err error) {
	svc := &corev1.Service{ObjectMeta: metav1.ObjectMeta{Namespace: oauthBridgeNamespace, Name: oauthBridgeName}}
	if _, err := controllerutil.CreateOrUpdate(ctx, c, svc, func() error {
		svc.Spec.Ports = []corev1.ServicePort{{
			Name:       "https",
			Port:       443,
			TargetPort: intstr.FromInt(oauthListenPort),
			Protocol:   corev1.ProtocolTCP,
		}}
		// Selectorless service; endpoints are managed separately.
		svc.Spec.Selector = nil
		return nil
	}); err != nil {
		return "", err
	}

	ep := &corev1.Endpoints{ObjectMeta: metav1.ObjectMeta{Namespace: oauthBridgeNamespace, Name: oauthBridgeName}}
	if _, err := controllerutil.CreateOrUpdate(ctx, c, ep, func() error {
		ep.Subsets = []corev1.EndpointSubset{{
			Addresses: []corev1.EndpointAddress{{IP: oauthUDNIP}},
			Ports:     []corev1.EndpointPort{{Name: "https", Port: oauthListenPort, Protocol: corev1.ProtocolTCP}},
		}}
		return nil
	}); err != nil {
		return "", err
	}

	// Re-get service to observe allocated ClusterIP.
	if err := c.Get(ctx, client.ObjectKeyFromObject(svc), svc); err != nil {
		return "", err
	}
	if svc.Spec.ClusterIP == "" || svc.Spec.ClusterIP == "None" {
		return "", nil
	}
	return svc.Spec.ClusterIP, nil
}
