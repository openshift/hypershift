package primaryudn

import (
	"context"

	operatorv1 "github.com/openshift/api/operator/v1"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
)

const internalAppsDNSServerName = "internal-apps"

func ensureDNSOperatorForwarding(ctx context.Context, c client.Client, zones []string, upstream string) error {
	dns := &operatorv1.DNS{ObjectMeta: metav1.ObjectMeta{Name: "default"}}
	_, err := controllerutil.CreateOrUpdate(ctx, c, dns, func() error {
		dns.Spec.Servers = upsertDNSServer(dns.Spec.Servers, operatorv1.Server{
			Name:  internalAppsDNSServerName,
			Zones: zones,
			ForwardPlugin: operatorv1.ForwardPlugin{
				Upstreams: []string{upstream},
			},
		})
		return nil
	})
	return err
}

func upsertDNSServer(servers []operatorv1.Server, desired operatorv1.Server) []operatorv1.Server {
	out := make([]operatorv1.Server, 0, len(servers)+1)
	found := false
	for i := range servers {
		s := servers[i]
		if s.Name == desired.Name {
			out = append(out, desired)
			found = true
		} else {
			out = append(out, s)
		}
	}
	if !found {
		out = append(out, desired)
	}
	return out
}
