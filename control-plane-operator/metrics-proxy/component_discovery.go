package metricsproxy

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"sync"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"

	crclient "sigs.k8s.io/controller-runtime/pkg/client"

	prometheusoperatorv1 "github.com/prometheus-operator/prometheus-operator/pkg/apis/monitoring/v1"
)

// ComponentConfig describes how to scrape a single control plane component,
// including the per-component TLS configuration derived from its ServiceMonitor.
type ComponentConfig struct {
	ServiceName   string
	MetricsPort   int32
	MetricsPath   string
	MetricsScheme string
	TLSServerName string
	TLSConfig     *tls.Config
}

// ComponentDiscoverer discovers scrape targets by reading ServiceMonitors and
// resolving the associated Services in the HCP namespace. It also reads the
// TLS configuration (CA, client cert, client key) referenced by each
// ServiceMonitor to build per-component TLS configs. Results are cached
// and refreshed periodically in the background.
type ComponentDiscoverer struct {
	crClient  crclient.Reader
	k8sClient kubernetes.Interface
	namespace string

	mu         sync.RWMutex
	components map[string]ComponentConfig
}

func NewComponentDiscoverer(crClient crclient.Reader, k8sClient kubernetes.Interface, namespace string) *ComponentDiscoverer {
	return &ComponentDiscoverer{
		crClient:   crClient,
		k8sClient:  k8sClient,
		namespace:  namespace,
		components: make(map[string]ComponentConfig),
	}
}

// Start runs an initial discovery synchronously, then refreshes periodically
// in the background. Periodic refresh is needed because the metrics-proxy may
// start before all ServiceMonitors are created by the CPO.
func (d *ComponentDiscoverer) Start(ctx context.Context) {
	d.refresh(ctx)

	go func() {
		ticker := time.NewTicker(30 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				d.refresh(ctx)
			}
		}
	}()
}

// GetComponent returns the config for a named component, if discovered.
func (d *ComponentDiscoverer) GetComponent(name string) (ComponentConfig, bool) {
	d.mu.RLock()
	defer d.mu.RUnlock()
	c, ok := d.components[name]
	return c, ok
}

// GetComponentNames returns all discovered component names.
func (d *ComponentDiscoverer) GetComponentNames() []string {
	d.mu.RLock()
	defer d.mu.RUnlock()
	names := make([]string, 0, len(d.components))
	for name := range d.components {
		names = append(names, name)
	}
	return names
}

// refresh lists ServiceMonitors and resolves their Services to build an
// up-to-date component config map. On error, the previous cache is retained.
func (d *ComponentDiscoverer) refresh(ctx context.Context) {
	var smList prometheusoperatorv1.ServiceMonitorList
	if err := d.crClient.List(ctx, &smList, crclient.InNamespace(d.namespace)); err != nil {
		return
	}

	components := make(map[string]ComponentConfig)
	for i := range smList.Items {
		sm := &smList.Items[i]
		if len(sm.Spec.Endpoints) == 0 {
			continue
		}

		ep := sm.Spec.Endpoints[0]

		serviceName, err := d.findServiceForMonitor(ctx, sm.Spec.Selector)
		if err != nil || serviceName == "" {
			continue
		}

		port, err := d.resolvePort(ctx, serviceName, ep)
		if err != nil || port == 0 {
			continue
		}

		scheme := "https"
		if ep.Scheme != nil {
			scheme = string(*ep.Scheme)
		}

		// TLSServerName must match a DNS SAN in the component's serving certificate.
		// The proxy scrapes pods by IP for per-pod attribution, but certs are issued
		// for the Service DNS name. This value comes directly from the ServiceMonitor's
		// tlsConfig.serverName field, which already has the correct value.
		var tlsServerName string
		if ep.TLSConfig != nil && ep.TLSConfig.ServerName != nil {
			tlsServerName = *ep.TLSConfig.ServerName
		}

		// Build a per-component TLS config from the ServiceMonitor's tlsConfig.
		// Each component may use a different CA and client certificate (e.g. etcd
		// uses etcd-ca + etcd-metrics-client-tls, while most others use root-ca +
		// metrics-client). Reading these from the ServiceMonitor ensures we always
		// use the correct credentials for each component.
		tlsConfig, err := d.buildTLSConfig(ctx, ep.TLSConfig)
		if err != nil {
			continue
		}

		path := "/metrics"
		if ep.Path != "" {
			path = ep.Path
		}

		components[sm.Name] = ComponentConfig{
			ServiceName:   serviceName,
			MetricsPort:   port,
			MetricsPath:   path,
			MetricsScheme: scheme,
			TLSServerName: tlsServerName,
			TLSConfig:     tlsConfig,
		}
	}

	d.mu.Lock()
	d.components = components
	d.mu.Unlock()
}

// buildTLSConfig reads the CA, client cert, and client key referenced by a
// ServiceMonitor's tlsConfig and builds a *tls.Config for scraping that component.
func (d *ComponentDiscoverer) buildTLSConfig(ctx context.Context, tlsCfg *prometheusoperatorv1.TLSConfig) (*tls.Config, error) {
	if tlsCfg == nil {
		return &tls.Config{MinVersion: tls.VersionTLS12}, nil
	}

	config := &tls.Config{MinVersion: tls.VersionTLS12}

	// Load CA certificate.
	caData, err := d.readSecretOrConfigMap(ctx, tlsCfg.CA)
	if err != nil {
		return nil, fmt.Errorf("failed to read CA: %w", err)
	}
	if caData != nil {
		caPool := x509.NewCertPool()
		caPool.AppendCertsFromPEM(caData)
		config.RootCAs = caPool
	}

	// Load client certificate and key for mTLS.
	certData, err := d.readSecretOrConfigMap(ctx, tlsCfg.Cert)
	if err != nil {
		return nil, fmt.Errorf("failed to read client cert: %w", err)
	}
	var keyData []byte
	if tlsCfg.KeySecret != nil {
		keyData, err = d.readSecretKey(ctx, tlsCfg.KeySecret.Name, tlsCfg.KeySecret.Key)
		if err != nil {
			return nil, fmt.Errorf("failed to read client key: %w", err)
		}
	}
	if certData != nil && keyData != nil {
		cert, err := tls.X509KeyPair(certData, keyData)
		if err != nil {
			return nil, fmt.Errorf("failed to parse client certificate: %w", err)
		}
		config.Certificates = []tls.Certificate{cert}
	}

	return config, nil
}

// readSecretOrConfigMap reads data from a SecretOrConfigMap reference.
func (d *ComponentDiscoverer) readSecretOrConfigMap(ctx context.Context, ref prometheusoperatorv1.SecretOrConfigMap) ([]byte, error) {
	if ref.Secret != nil {
		return d.readSecretKey(ctx, ref.Secret.Name, ref.Secret.Key)
	}
	if ref.ConfigMap != nil {
		return d.readConfigMapKey(ctx, ref.ConfigMap.Name, ref.ConfigMap.Key)
	}
	return nil, nil
}

func (d *ComponentDiscoverer) readSecretKey(ctx context.Context, name, key string) ([]byte, error) {
	secret, err := d.k8sClient.CoreV1().Secrets(d.namespace).Get(ctx, name, metav1.GetOptions{})
	if err != nil {
		return nil, err
	}
	data, ok := secret.Data[key]
	if !ok {
		return nil, fmt.Errorf("key %q not found in secret %s", key, name)
	}
	return data, nil
}

func (d *ComponentDiscoverer) readConfigMapKey(ctx context.Context, name, key string) ([]byte, error) {
	cm, err := d.k8sClient.CoreV1().ConfigMaps(d.namespace).Get(ctx, name, metav1.GetOptions{})
	if err != nil {
		return nil, err
	}
	data, ok := cm.Data[key]
	if !ok {
		return nil, fmt.Errorf("key %q not found in configmap %s", key, name)
	}
	return []byte(data), nil
}

// findServiceForMonitor finds the first Service matching the ServiceMonitor's selector.
func (d *ComponentDiscoverer) findServiceForMonitor(ctx context.Context, selector metav1.LabelSelector) (string, error) {
	svcList, err := d.k8sClient.CoreV1().Services(d.namespace).List(ctx, metav1.ListOptions{
		LabelSelector: metav1.FormatLabelSelector(&selector),
	})
	if err != nil {
		return "", err
	}
	if len(svcList.Items) == 0 {
		return "", nil
	}
	return svcList.Items[0].Name, nil
}

// resolvePort resolves the metrics port number from a Service based on the
// ServiceMonitor endpoint's port or targetPort reference.
func (d *ComponentDiscoverer) resolvePort(ctx context.Context, serviceName string, ep prometheusoperatorv1.Endpoint) (int32, error) {
	svc, err := d.k8sClient.CoreV1().Services(d.namespace).Get(ctx, serviceName, metav1.GetOptions{})
	if err != nil {
		return 0, err
	}

	// "port" (named Service port) takes precedence per the prometheus-operator spec.
	// We return the targetPort (pod port) because the proxy scrapes pods by IP.
	if ep.Port != "" {
		for _, p := range svc.Spec.Ports {
			if p.Name == ep.Port {
				if p.TargetPort.IntValue() > 0 {
					return int32(p.TargetPort.IntValue()), nil
				}
				return p.Port, nil
			}
		}
		return 0, fmt.Errorf("named port %q not found on service %s", ep.Port, serviceName)
	}

	if ep.TargetPort != nil {
		if ep.TargetPort.IntValue() > 0 {
			return int32(ep.TargetPort.IntValue()), nil
		}
		// Named targetPort: find the Service port whose name or targetPort matches.
		// The ServiceMonitor's targetPort refers to the pod/container port by name.
		// We match against both p.Name and p.TargetPort since either could hold
		// the container port name. We return p.TargetPort (the pod port) because
		// the proxy scrapes pods by IP, not through the Service.
		portName := ep.TargetPort.String()
		for _, p := range svc.Spec.Ports {
			if p.Name == portName || p.TargetPort.String() == portName {
				if p.TargetPort.IntValue() > 0 {
					return int32(p.TargetPort.IntValue()), nil
				}
				return p.Port, nil
			}
		}
		return 0, fmt.Errorf("targetPort %q not found on service %s", portName, serviceName)
	}

	if len(svc.Spec.Ports) > 0 {
		return svc.Spec.Ports[0].Port, nil
	}
	return 0, fmt.Errorf("no ports found on service %s", serviceName)
}
