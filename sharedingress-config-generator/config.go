package sharedingressconfiggenerator

import (
	"context"
	_ "embed"
	"fmt"
	"io"
	"slices"
	"strings"
	"text/template"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	"github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/manifests"
	"github.com/openshift/hypershift/hypershift-operator/controllers/manifests/ignitionserver"
	"github.com/openshift/hypershift/support/config"
	"github.com/openshift/hypershift/support/util"

	routev1 "github.com/openshift/api/route/v1"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	crclient "sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"
)

const (
	routerConfigKey   = "haproxy.cfg"
	KASSVCLBPort      = 6443
	ExternalDNSLBPort = 443
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

type backendDesc struct {
	Name         string
	SVCIP        string
	SVCPort      int32
	ClusterID    string
	AllowedCIDRs string
}
type externalDNSBackendDesc struct {
	Name         string
	HostName     string
	SVCIP        string
	SVCPort      int32
	AllowedCIDRs string
}

func generateRouterConfig(ctx context.Context, client crclient.Client, w io.Writer) error {
	logger := log.FromContext(ctx)

	type templateParams struct {
		Backends            []backendDesc
		ExternalDNSBackends []externalDNSBackendDesc
	}

	hcList := &hyperv1.HostedClusterList{}
	if err := client.List(ctx, hcList); err != nil {
		return fmt.Errorf("failed to list HostedClusters: %w", err)
	}

	hostedClusters := hcList.Items
	slices.SortFunc(hostedClusters, func(a, b hyperv1.HostedCluster) int {
		hcpNamespaceA := a.Namespace + "-" + a.Name
		hcpNamespaceB := b.Namespace + "-" + b.Name
		return strings.Compare(hcpNamespaceA, hcpNamespaceB)
	})

	p := templateParams{
		Backends:            make([]backendDesc, 0, len(hostedClusters)),
		ExternalDNSBackends: make([]externalDNSBackendDesc, 0, len(hostedClusters)),
	}
	for _, hc := range hostedClusters {
		if !hc.DeletionTimestamp.IsZero() {
			continue
		}

		backends, externalDNSBackends, err := getBackendsForHostedCluster(ctx, hc, client)
		if err != nil {
			// don't return an error here, otherwise we block config generation for other HostedClusters and potentially block their KAS access.
			logger.Error(err, "failed to generate router config for hosted cluster", "hosted_cluster", crclient.ObjectKeyFromObject(&hc))
			continue
		}

		p.Backends = append(p.Backends, backends...)
		p.ExternalDNSBackends = append(p.ExternalDNSBackends, externalDNSBackends...)
	}

	if err := routerConfigTemplate.Execute(w, p); err != nil {
		return fmt.Errorf("failed to generate router config: %w", err)
	}
	return nil
}

func getBackendsForHostedCluster(ctx context.Context, hc hyperv1.HostedCluster, client crclient.Client) ([]backendDesc, []externalDNSBackendDesc, error) {
	backends := []backendDesc{}
	externalDNSBackends := []externalDNSBackendDesc{}

	var allowedCIDRs string
	if hc.Spec.Networking.APIServer != nil && hc.Spec.Networking.APIServer.AllowedCIDRBlocks != nil {
		allowedCIDRBlocks := make([]string, 0, len(hc.Spec.Networking.APIServer.AllowedCIDRBlocks))
		for _, cidr := range hc.Spec.Networking.APIServer.AllowedCIDRBlocks {
			if cidr != "" {
				allowedCIDRBlocks = append(allowedCIDRBlocks, string(cidr))
			}
		}

		allowedCIDRs = strings.Join(allowedCIDRBlocks, " ")
	}

	hcpNamespace := hc.Namespace + "-" + hc.Name
	kasService := manifests.KubeAPIServerService(hcpNamespace)
	if err := client.Get(ctx, crclient.ObjectKeyFromObject(kasService), kasService); err != nil {
		return nil, nil, fmt.Errorf("failed to get kube-apiserver service: %w", err)
	}

	backends = append(backends, backendDesc{
		Name:         kasService.Namespace + "-" + kasService.Name,
		SVCIP:        kasService.Spec.ClusterIP,
		SVCPort:      kasService.Spec.Ports[0].Port,
		ClusterID:    hc.Spec.ClusterID,
		AllowedCIDRs: allowedCIDRs,
	})

	// This enables traffic from external DNS to exposed endpoints (KAS, oauth, ignition and konnectivity).
	routeList := &routev1.RouteList{}
	if err := client.List(ctx, routeList, crclient.InNamespace(hcpNamespace), crclient.HasLabels{util.HCPRouteLabel}); err != nil {
		return nil, nil, fmt.Errorf("failed to list routes: %w", err)
	}

	routes := routeList.Items
	slices.SortFunc(routes, func(a, b routev1.Route) int {
		return strings.Compare(a.Name, b.Name)
	})

	for _, route := range routes {
		svc := &corev1.Service{
			ObjectMeta: metav1.ObjectMeta{
				Name:      route.Spec.To.Name,
				Namespace: route.Namespace,
			},
		}
		if err := client.Get(ctx, crclient.ObjectKeyFromObject(svc), svc); err != nil {
			return nil, nil, fmt.Errorf("failed to get service %s: %w", svc.Name, err)
		}

		switch route.Name {
		case manifests.KubeAPIServerExternalPublicRoute("").Name:
			externalDNSBackends = append(externalDNSBackends, externalDNSBackendDesc{
				Name:         route.Namespace + "-apiserver",
				HostName:     route.Spec.Host,
				SVCIP:        svc.Spec.ClusterIP,
				SVCPort:      config.KASSVCPort,
				AllowedCIDRs: allowedCIDRs})
		case ignitionserver.Route("").Name:
			externalDNSBackends = append(externalDNSBackends, externalDNSBackendDesc{
				Name:         route.Namespace + "-ignition",
				HostName:     route.Spec.Host,
				SVCIP:        svc.Spec.ClusterIP,
				SVCPort:      443,
				AllowedCIDRs: allowedCIDRs})
		case manifests.KonnectivityServerRoute("").Name:
			externalDNSBackends = append(externalDNSBackends, externalDNSBackendDesc{
				Name:         route.Namespace + "-konnectivity",
				HostName:     route.Spec.Host,
				SVCIP:        svc.Spec.ClusterIP,
				SVCPort:      8091,
				AllowedCIDRs: allowedCIDRs})
		case manifests.OauthServerExternalPublicRoute("").Name:
			externalDNSBackends = append(externalDNSBackends, externalDNSBackendDesc{
				Name:         route.Namespace + "-oauth",
				HostName:     route.Spec.Host,
				SVCIP:        svc.Spec.ClusterIP,
				SVCPort:      6443,
				AllowedCIDRs: allowedCIDRs})
		}
	}

	if hc.Spec.KubeAPIServerDNSName != "" {
		externalDNSBackends = append(externalDNSBackends, externalDNSBackendDesc{
			Name:         hcpNamespace + "-apiserver-custom",
			HostName:     hc.Spec.KubeAPIServerDNSName,
			SVCIP:        kasService.Spec.ClusterIP,
			SVCPort:      config.KASSVCPort,
			AllowedCIDRs: allowedCIDRs})
	}

	return backends, externalDNSBackends, nil
}
