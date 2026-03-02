package metricsproxy

import (
	"crypto/tls"
	"fmt"
	"os"

	"sigs.k8s.io/yaml"

	"github.com/go-logr/logr"
)

// ConfigFileReader reads component configuration from a YAML file on disk
// and builds ComponentConfig entries with TLS configurations from mounted
// certificate files. The CPOv2 framework rolls out the deployment when any
// mounted ConfigMap or Secret changes, so the config is loaded once at startup.
type ConfigFileReader struct {
	path       string
	log        logr.Logger
	components map[string]ComponentConfig
	rawConfig  *FileConfig
}

func NewConfigFileReader(path string, log logr.Logger) *ConfigFileReader {
	return &ConfigFileReader{
		path:       path,
		log:        log,
		components: make(map[string]ComponentConfig),
	}
}

// Load reads the config file and builds ComponentConfig entries.
func (r *ConfigFileReader) Load() error {
	data, err := os.ReadFile(r.path)
	if err != nil {
		return fmt.Errorf("failed to read config file %s: %w", r.path, err)
	}

	var cfg FileConfig
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return fmt.Errorf("failed to unmarshal config file: %w", err)
	}

	components := make(map[string]ComponentConfig, len(cfg.Components))
	for name, comp := range cfg.Components {
		tlsCfg, err := buildTLSConfigFromFiles(comp.CAFile, comp.CertFile, comp.KeyFile)
		if err != nil {
			r.log.Error(err, "failed to build TLS config, skipping component", "component", name)
			continue
		}

		components[name] = ComponentConfig{
			ServiceName:   comp.ServiceName,
			MetricsPort:   comp.MetricsPort,
			MetricsPath:   comp.MetricsPath,
			MetricsScheme: comp.MetricsScheme,
			TLSServerName: comp.TLSServerName,
			TLSConfig:     tlsCfg,
		}
	}

	r.components = components
	r.rawConfig = &cfg

	return nil
}

// GetComponent returns the config for a named component.
func (r *ConfigFileReader) GetComponent(name string) (ComponentConfig, bool) {
	c, ok := r.components[name]
	return c, ok
}

// GetComponentNames returns all loaded component names.
func (r *ConfigFileReader) GetComponentNames() []string {
	names := make([]string, 0, len(r.components))
	for name := range r.components {
		names = append(names, name)
	}
	return names
}

// EndpointResolverURL returns the endpoint-resolver URL from the config.
func (r *ConfigFileReader) EndpointResolverURL() string {
	if r.rawConfig == nil {
		return ""
	}
	return r.rawConfig.EndpointResolver.URL
}

// EndpointResolverCAFile returns the endpoint-resolver CA file path from the config.
func (r *ConfigFileReader) EndpointResolverCAFile() string {
	if r.rawConfig == nil {
		return ""
	}
	return r.rawConfig.EndpointResolver.CAFile
}

// buildTLSConfigFromFiles builds a *tls.Config from certificate file paths.
func buildTLSConfigFromFiles(caFile, certFile, keyFile string) (*tls.Config, error) {
	config := &tls.Config{MinVersion: tls.VersionTLS12}

	if caPool := loadCertPool(caFile); caPool != nil {
		config.RootCAs = caPool
	}

	if certFile != "" && keyFile != "" {
		certPEM, err := os.ReadFile(certFile)
		if err != nil {
			// Client cert may not exist yet; return config without client cert.
			return config, nil
		}
		keyPEM, err := os.ReadFile(keyFile)
		if err != nil {
			return config, nil
		}
		cert, err := tls.X509KeyPair(certPEM, keyPEM)
		if err != nil {
			return nil, fmt.Errorf("failed to parse client certificate: %w", err)
		}
		config.Certificates = []tls.Certificate{cert}
	}

	return config, nil
}
