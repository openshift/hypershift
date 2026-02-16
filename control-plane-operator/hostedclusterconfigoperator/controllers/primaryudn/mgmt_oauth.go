package primaryudn

import (
	"context"
	"encoding/json"
	"fmt"
	"net/netip"
	"strings"

	routev1 "github.com/openshift/api/route/v1"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

const (
	oauthRouteName           = "oauth"
	oauthPodLabelValue       = "oauth-openshift"
	ovnPodNetworksAnnotation = "k8s.ovn.org/pod-networks"
	ovnPrimaryRole           = "primary"
)

type podNetworksEntry struct {
	Role      string `json:"role"`
	IPAddress string `json:"ip_address"`
}

func mgmtOAuthRouteHost(ctx context.Context, c client.Client, namespace string) (string, error) {
	route := &routev1.Route{ObjectMeta: metav1.ObjectMeta{Namespace: namespace, Name: oauthRouteName}}
	if err := c.Get(ctx, client.ObjectKeyFromObject(route), route); err != nil {
		return "", err
	}
	if route.Spec.Host == "" {
		return "", fmt.Errorf("oauth route has empty host")
	}
	return route.Spec.Host, nil
}

func mgmtOAuthPrimaryUDNIP(ctx context.Context, c client.Client, namespace string) (string, error) {
	pods := &corev1.PodList{}
	if err := c.List(ctx, pods, client.InNamespace(namespace), client.MatchingLabels{"app": oauthPodLabelValue}); err != nil {
		return "", err
	}
	for i := range pods.Items {
		ip, ok := primaryUDNIPFromPodNetworks(&pods.Items[i])
		if ok {
			return ip, nil
		}
	}
	return "", fmt.Errorf("no oauth pod with primary UDN IP found")
}

func primaryUDNIPFromPodNetworks(pod *corev1.Pod) (string, bool) {
	raw := ""
	if pod.Annotations != nil {
		raw = pod.Annotations[ovnPodNetworksAnnotation]
	}
	if raw == "" {
		return "", false
	}

	m := map[string]podNetworksEntry{}
	if err := json.Unmarshal([]byte(raw), &m); err != nil {
		return "", false
	}

	for networkKey, v := range m {
		if v.Role != ovnPrimaryRole {
			continue
		}
		if networkKey == "default" {
			continue
		}
		ip := strings.SplitN(v.IPAddress, "/", 2)[0]
		if ip == "" {
			continue
		}
		if _, err := netip.ParseAddr(ip); err != nil {
			continue
		}
		return ip, true
	}
	return "", false
}
