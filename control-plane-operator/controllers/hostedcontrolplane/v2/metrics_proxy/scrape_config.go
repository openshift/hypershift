package metricsproxy

import (
	"fmt"
	"path/filepath"

	metricsproxybin "github.com/openshift/hypershift/control-plane-operator/metrics-proxy"
	component "github.com/openshift/hypershift/support/controlplane-component"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/yaml"

	"github.com/go-logr/logr"
	prometheusoperatorv1 "github.com/prometheus-operator/prometheus-operator/pkg/apis/monitoring/v1"
)

const (
	certBasePath       = "/etc/metrics-proxy/certs"
	endpointResolverCA = certBasePath + "/endpoint-resolver-ca/tls.crt"
)

func adaptScrapeConfig(cpContext component.WorkloadContext, cm *corev1.ConfigMap) error {
	log := logr.FromContextOrDiscard(cpContext)
	namespace := cpContext.HCP.Namespace

	endpointResolverURL := fmt.Sprintf("https://endpoint-resolver.%s.svc", namespace)

	cfg := metricsproxybin.FileConfig{
		EndpointResolver: metricsproxybin.EndpointResolverFileConfig{
			URL:    endpointResolverURL,
			CAFile: endpointResolverCA,
		},
		Components: make(map[string]metricsproxybin.ComponentFileConfig),
	}

	// Process ServiceMonitors.
	smList := &prometheusoperatorv1.ServiceMonitorList{}
	if err := cpContext.Client.List(cpContext, smList, client.InNamespace(namespace)); err != nil {
		return fmt.Errorf("failed to list ServiceMonitors: %w", err)
	}

	for i := range smList.Items {
		sm := &smList.Items[i]
		if len(sm.Spec.Endpoints) == 0 {
			continue
		}
		ep := sm.Spec.Endpoints[0]

		serviceName, err := findServiceForMonitor(cpContext, namespace, sm.Name, sm.Spec.Selector)
		if err != nil {
			log.V(4).Info("skipping ServiceMonitor: service not found", "serviceMonitor", sm.Name, "error", err)
			continue
		}

		portRef := ep.Port
		if portRef == "" && ep.TargetPort != nil {
			portRef = ep.TargetPort.String()
		}
		if portRef == "" {
			log.V(4).Info("skipping ServiceMonitor: no port reference", "serviceMonitor", sm.Name)
			continue
		}

		port, err := resolveServicePort(cpContext, namespace, serviceName, portRef)
		if err != nil {
			log.V(4).Info("skipping ServiceMonitor: port not resolvable", "serviceMonitor", sm.Name, "port", portRef, "error", err)
			continue
		}

		scheme := "https"
		if ep.Scheme != nil {
			scheme = ep.Scheme.String()
		}

		metricsPath := "/metrics"
		if ep.Path != "" {
			metricsPath = ep.Path
		}

		var serverName string
		if ep.TLSConfig != nil && ep.TLSConfig.ServerName != nil {
			serverName = *ep.TLSConfig.ServerName
		}

		comp := metricsproxybin.ComponentFileConfig{
			ServiceName:   serviceName,
			MetricsPort:   port,
			MetricsPath:   metricsPath,
			MetricsScheme: scheme,
			TLSServerName: serverName,
		}

		if ep.TLSConfig != nil {
			comp.CAFile = certFilePathFromSecretOrConfigMap(ep.TLSConfig.CA)
			comp.CertFile = certFilePathFromSecretOrConfigMap(ep.TLSConfig.Cert)
			if ep.TLSConfig.KeySecret != nil {
				comp.KeyFile = filepath.Join(certBasePath, ep.TLSConfig.KeySecret.Name, ep.TLSConfig.KeySecret.Key)
			}
		}

		cfg.Components[sm.Name] = comp
	}

	// Process PodMonitors.
	pmList := &prometheusoperatorv1.PodMonitorList{}
	if err := cpContext.Client.List(cpContext, pmList, client.InNamespace(namespace)); err != nil {
		return fmt.Errorf("failed to list PodMonitors: %w", err)
	}

	for i := range pmList.Items {
		pm := &pmList.Items[i]
		if len(pm.Spec.PodMetricsEndpoints) == 0 {
			continue
		}
		ep := pm.Spec.PodMetricsEndpoints[0]

		portName := ""
		if ep.Port != nil {
			portName = *ep.Port
		}
		if portName == "" {
			log.V(4).Info("skipping PodMonitor: no port name", "podMonitor", pm.Name)
			continue
		}

		// Resolve the port number from the component's Deployment.
		// The PodMonitor name matches the component name by CPOv2 convention.
		port, err := resolveDeploymentPort(cpContext, namespace, pm.Name, portName)
		if err != nil {
			log.V(4).Info("skipping PodMonitor: port not resolvable", "podMonitor", pm.Name, "port", portName, "error", err)
			continue
		}

		scheme := "https"
		if ep.Scheme != nil {
			scheme = ep.Scheme.String()
		}

		metricsPath := "/metrics"
		if ep.Path != "" {
			metricsPath = ep.Path
		}

		var serverName string
		if ep.TLSConfig != nil && ep.TLSConfig.ServerName != nil {
			serverName = *ep.TLSConfig.ServerName
		}

		// For PodMonitors, the component name is used for endpoint-resolver lookup.
		comp := metricsproxybin.ComponentFileConfig{
			ServiceName:   pm.Name,
			MetricsPort:   port,
			MetricsPath:   metricsPath,
			MetricsScheme: scheme,
			TLSServerName: serverName,
		}

		if ep.TLSConfig != nil {
			comp.CAFile = certFilePathFromSecretOrConfigMap(ep.TLSConfig.CA)
			comp.CertFile = certFilePathFromSecretOrConfigMap(ep.TLSConfig.Cert)
			if ep.TLSConfig.KeySecret != nil {
				comp.KeyFile = filepath.Join(certBasePath, ep.TLSConfig.KeySecret.Name, ep.TLSConfig.KeySecret.Key)
			}
		}

		cfg.Components[pm.Name] = comp
	}

	data, err := yaml.Marshal(cfg)
	if err != nil {
		return fmt.Errorf("failed to marshal scrape config: %w", err)
	}

	if cm.Data == nil {
		cm.Data = make(map[string]string)
	}
	cm.Data["config.yaml"] = string(data)
	return nil
}

// certFilePathFromSecretOrConfigMap returns the file path for a volume-mounted
// cert from a SecretOrConfigMap reference. The convention is:
//
//	/etc/metrics-proxy/certs/<resource-name>/<key>
func certFilePathFromSecretOrConfigMap(ref prometheusoperatorv1.SecretOrConfigMap) string {
	if ref.ConfigMap != nil {
		return filepath.Join(certBasePath, ref.ConfigMap.Name, ref.ConfigMap.Key)
	}
	if ref.Secret != nil {
		return filepath.Join(certBasePath, ref.Secret.Name, ref.Secret.Key)
	}
	return ""
}

// findServiceForMonitor finds the Service for a ServiceMonitor. It first tries
// a direct lookup by the ServiceMonitor's name (which by convention matches the
// target service in HyperShift). If no service with that name exists, it falls
// back to the label selector.
func findServiceForMonitor(cpContext component.WorkloadContext, namespace, smName string, selector metav1.LabelSelector) (string, error) {
	// Try direct lookup by ServiceMonitor name first.
	svc := &corev1.Service{}
	if err := cpContext.Client.Get(cpContext, client.ObjectKey{Namespace: namespace, Name: smName}, svc); err == nil {
		return svc.Name, nil
	}

	// Fall back to label selector.
	sel, err := metav1.LabelSelectorAsSelector(&selector)
	if err != nil {
		return "", fmt.Errorf("failed to parse label selector: %w", err)
	}

	svcList := &corev1.ServiceList{}
	if err := cpContext.Client.List(cpContext, svcList, client.InNamespace(namespace), client.MatchingLabelsSelector{Selector: sel}); err != nil {
		return "", fmt.Errorf("failed to list services: %w", err)
	}

	if len(svcList.Items) == 0 {
		return "", fmt.Errorf("no service found for ServiceMonitor %s", smName)
	}

	return svcList.Items[0].Name, nil
}

// resolveServicePort reads a Service and resolves a named port to a numeric
// port value.
func resolveServicePort(cpContext component.WorkloadContext, namespace, serviceName, portName string) (int32, error) {
	svc := &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: namespace,
			Name:      serviceName,
		},
	}
	if err := cpContext.Client.Get(cpContext, client.ObjectKeyFromObject(svc), svc); err != nil {
		return 0, fmt.Errorf("failed to get service %s: %w", serviceName, err)
	}

	for _, p := range svc.Spec.Ports {
		if p.Name == portName || p.TargetPort.String() == portName {
			if p.TargetPort.IntValue() > 0 {
				return int32(p.TargetPort.IntValue()), nil
			}
			return p.Port, nil
		}
	}

	return 0, fmt.Errorf("port %q not found on service %s", portName, serviceName)
}

// resolveDeploymentPort reads a Deployment and resolves a named container port
// to a numeric port value. It searches all containers for a port matching the
// given name.
func resolveDeploymentPort(cpContext component.WorkloadContext, namespace, deploymentName, portName string) (int32, error) {
	deploy := &appsv1.Deployment{}
	if err := cpContext.Client.Get(cpContext, client.ObjectKey{Namespace: namespace, Name: deploymentName}, deploy); err != nil {
		return 0, fmt.Errorf("failed to get deployment %s: %w", deploymentName, err)
	}

	for _, container := range deploy.Spec.Template.Spec.Containers {
		for _, p := range container.Ports {
			if p.Name == portName {
				return p.ContainerPort, nil
			}
		}
	}

	return 0, fmt.Errorf("port %q not found on deployment %s", portName, deploymentName)
}
