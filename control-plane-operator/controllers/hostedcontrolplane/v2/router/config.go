package router

import (
	"bytes"
	_ "embed"
	"fmt"
	"sort"
	"text/template"

	"github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/manifests"
	"github.com/openshift/hypershift/hypershift-operator/controllers/manifests/ignitionserver"
	"github.com/openshift/hypershift/support/config"
	component "github.com/openshift/hypershift/support/controlplane-component"
	"github.com/openshift/hypershift/support/util"

	routev1 "github.com/openshift/api/route/v1"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"sigs.k8s.io/controller-runtime/pkg/client"
)

const (
	routerConfigKey = "haproxy.cfg"
)

//go:embed router_config.template
var routerConfigTemplateStr string
var routerConfigTemplate *template.Template

func init() {
	var err error
	routerConfigTemplate, err = template.New("router-config").Parse(routerConfigTemplateStr)
	if err != nil {
		panic(err.Error())
	}
}

func adaptConfig(cpContext component.WorkloadContext, cm *corev1.ConfigMap) error {
	routeList := &routev1.RouteList{}
	if err := cpContext.Client.List(cpContext, routeList, client.InNamespace(cpContext.HCP.Namespace)); err != nil {
		return fmt.Errorf("failed to list routes: %w", err)
	}

	// reconcile the router's configuration
	svcsNameToIP := make(map[string]string)
	for _, route := range routeList.Items {
		svc := &corev1.Service{
			TypeMeta: metav1.TypeMeta{},
			ObjectMeta: metav1.ObjectMeta{
				Name:      route.Spec.To.Name,
				Namespace: cpContext.HCP.Namespace,
			},
		}
		if err := cpContext.Client.Get(cpContext, client.ObjectKeyFromObject(svc), svc); err != nil {
			return err
		}

		svcsNameToIP[route.Spec.To.Name] = svc.Spec.ClusterIP
	}

	routerConfig, err := generateRouterConfig(routeList, svcsNameToIP)
	if err != nil {
		return err
	}
	cm.Data[routerConfigKey] = routerConfig
	return nil
}

type byRouteName []routev1.Route

func (r byRouteName) Len() int           { return len(r) }
func (r byRouteName) Swap(i, j int)      { r[i], r[j] = r[j], r[i] }
func (r byRouteName) Less(i, j int) bool { return r[i].Name < r[j].Name }

func generateRouterConfig(routeList *routev1.RouteList, svcsNameToIP map[string]string) (string, error) {
	type backendDesc struct {
		Name                 string
		HostName             string
		DestinationServiceIP string
		DestinationPort      int32
	}
	type templateParams struct {
		HasKubeAPI              bool
		KASSVCPort              int32
		KASDestinationServiceIP string
		Backends                []backendDesc
	}
	p := templateParams{}
	sort.Sort(byRouteName(routeList.Items))
	for _, route := range routeList.Items {
		if _, hasHCPLabel := route.Labels[util.HCPRouteLabel]; !hasHCPLabel {
			// If the hypershift.openshift.io/hosted-control-plane label is not present,
			// then it means the route should be fulfilled by the management cluster's router.
			continue
		}
		switch route.Name {
		case manifests.KubeAPIServerInternalRoute("").Name,
			manifests.KubeAPIServerExternalPublicRoute("").Name,
			manifests.KubeAPIServerExternalPrivateRoute("").Name:
			p.HasKubeAPI = true
			p.KASDestinationServiceIP = svcsNameToIP["kube-apiserver"]
			continue
		case ignitionserver.Route("").Name:
			p.Backends = append(p.Backends, backendDesc{Name: "ignition", HostName: route.Spec.Host, DestinationServiceIP: svcsNameToIP[route.Spec.To.Name], DestinationPort: 443})
		case manifests.KonnectivityServerRoute("").Name:
			p.Backends = append(p.Backends, backendDesc{Name: "konnectivity", HostName: route.Spec.Host, DestinationServiceIP: svcsNameToIP[route.Spec.To.Name], DestinationPort: 8091})
		case manifests.OauthServerExternalPrivateRoute("").Name:
			p.Backends = append(p.Backends, backendDesc{Name: "oauth_private", HostName: route.Spec.Host, DestinationServiceIP: svcsNameToIP[route.Spec.To.Name], DestinationPort: 6443})
		case manifests.OauthServerExternalPublicRoute("").Name:
			p.Backends = append(p.Backends, backendDesc{Name: "oauth", HostName: route.Spec.Host, DestinationServiceIP: svcsNameToIP[route.Spec.To.Name], DestinationPort: 6443})
		case manifests.OauthServerInternalRoute("").Name:
			p.Backends = append(p.Backends, backendDesc{Name: "oauth_internal", HostName: route.Spec.Host, DestinationServiceIP: svcsNameToIP[route.Spec.To.Name], DestinationPort: 6443})
		case manifests.MetricsForwarderRoute("").Name:
			p.Backends = append(p.Backends, backendDesc{Name: "metrics_forwarder", HostName: route.Spec.Host, DestinationServiceIP: svcsNameToIP[route.Spec.To.Name], DestinationPort: route.Spec.Port.TargetPort.IntVal})
		}
	}
	if p.HasKubeAPI {
		p.KASSVCPort = config.KASSVCPort
	}
	out := &bytes.Buffer{}
	if err := routerConfigTemplate.Execute(out, p); err != nil {
		return "", fmt.Errorf("failed to generate router config: %w", err)
	}
	return out.String(), nil
}
