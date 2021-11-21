// Copyright 2018 The Cluster Monitoring Operator Authors
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package manifests

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"

	configv1 "github.com/openshift/api/config/v1"
	monv1 "github.com/prometheus-operator/prometheus-operator/pkg/apis/monitoring/v1"
	v1 "k8s.io/api/core/v1"
	k8syaml "k8s.io/apimachinery/pkg/util/yaml"
	auditv1 "k8s.io/apiserver/pkg/apis/audit/v1"
)

const (
	DefaultRetentionValue = "15d"
)

type Config struct {
	Images      *Images `json:"-"`
	RemoteWrite bool    `json:"-"`

	ClusterMonitoringConfiguration *ClusterMonitoringConfiguration `json:"-"`
	UserWorkloadConfiguration      *UserWorkloadConfiguration      `json:"-"`
}

func (c Config) IsStorageConfigured() bool {
	if c.ClusterMonitoringConfiguration == nil {
		return false
	}

	prometheusK8sConfig := c.ClusterMonitoringConfiguration.PrometheusK8sConfig
	if prometheusK8sConfig == nil {
		return false
	}

	return prometheusK8sConfig.VolumeClaimTemplate != nil
}

// GetPrometheusUWAdditionalAlertmanagerConfigs returns the alertmanager configurations for
// the User Workload Monitoring Prometheus instance.
// If no additional configurations are specified, GetPrometheusUWAdditionalAlertmanagerConfigs returns nil.
func (c Config) GetPrometheusUWAdditionalAlertmanagerConfigs() []AdditionalAlertmanagerConfig {
	if c.UserWorkloadConfiguration == nil {
		return nil
	}

	if c.UserWorkloadConfiguration.Prometheus == nil {
		return nil
	}

	alertmanagerConfigs := c.UserWorkloadConfiguration.Prometheus.AlertmanagerConfigs
	if len(alertmanagerConfigs) == 0 {
		return nil
	}

	return alertmanagerConfigs
}

// GetThanosRulerAlertmanagerConfigs returns the alertmanager configurations for
// the User Workload Monitoring Thanos Ruler instance.
// If no additional configurations are specified, GetThanosRulerAlertmanagerConfigs returns nil.
func (c Config) GetThanosRulerAlertmanagerConfigs() []AdditionalAlertmanagerConfig {
	if c.UserWorkloadConfiguration == nil {
		return nil
	}

	if c.UserWorkloadConfiguration.ThanosRuler == nil {
		return nil
	}

	alertmanagerConfigs := c.UserWorkloadConfiguration.ThanosRuler.AlertmanagersConfigs
	if len(alertmanagerConfigs) == 0 {
		return nil
	}

	return alertmanagerConfigs
}

type ClusterMonitoringConfiguration struct {
	PrometheusOperatorConfig *PrometheusOperatorConfig    `json:"prometheusOperator"`
	PrometheusK8sConfig      *PrometheusK8sConfig         `json:"prometheusK8s"`
	AlertmanagerMainConfig   *AlertmanagerMainConfig      `json:"alertmanagerMain"`
	KubeStateMetricsConfig   *KubeStateMetricsConfig      `json:"kubeStateMetrics"`
	OpenShiftMetricsConfig   *OpenShiftStateMetricsConfig `json:"openshiftStateMetrics"`
	GrafanaConfig            *GrafanaConfig               `json:"grafana"`
	EtcdConfig               *EtcdConfig                  `json:"-"`
	HTTPConfig               *HTTPConfig                  `json:"http"`
	TelemeterClientConfig    *TelemeterClientConfig       `json:"telemeterClient"`
	K8sPrometheusAdapter     *K8sPrometheusAdapter        `json:"k8sPrometheusAdapter"`
	ThanosQuerierConfig      *ThanosQuerierConfig         `json:"thanosQuerier"`
	UserWorkloadEnabled      *bool                        `json:"enableUserWorkload"`
}

type Images struct {
	K8sPrometheusAdapter     string
	PromLabelProxy           string
	PrometheusOperator       string
	PrometheusConfigReloader string
	Prometheus               string
	Alertmanager             string
	Grafana                  string
	OauthProxy               string
	NodeExporter             string
	KubeStateMetrics         string
	OpenShiftStateMetrics    string
	KubeRbacProxy            string
	TelemeterClient          string
	Thanos                   string
}

type HTTPConfig struct {
	HTTPProxy  string `json:"httpProxy"`
	HTTPSProxy string `json:"httpsProxy"`
	NoProxy    string `json:"noProxy"`
}

type PrometheusOperatorConfig struct {
	LogLevel     string            `json:"logLevel"`
	NodeSelector map[string]string `json:"nodeSelector"`
	Tolerations  []v1.Toleration   `json:"tolerations"`
}

// RemoteWriteSpec is almost a 1to1 copy of monv1.RemoteWriteSpec but with the
// BearerToken field removed. In the future other fields might be added here.
type RemoteWriteSpec struct {
	// The URL of the endpoint to send samples to.
	URL string `json:"url"`
	// The name of the remote write queue, must be unique if specified. The
	// name is used in metrics and logging in order to differentiate queues.
	// Only valid in Prometheus versions 2.15.0 and newer.
	Name string `json:"name,omitempty"`
	// Timeout for requests to the remote write endpoint.
	RemoteTimeout string `json:"remoteTimeout,omitempty"`
	// Custom HTTP headers to be sent along with each remote write request.
	// Be aware that headers that are set by Prometheus itself can't be overwritten.
	// Only valid in Prometheus versions 2.25.0 and newer.
	Headers map[string]string `json:"headers,omitempty"`
	// The list of remote write relabel configurations.
	WriteRelabelConfigs []monv1.RelabelConfig `json:"writeRelabelConfigs,omitempty"`
	// BasicAuth for the URL.
	BasicAuth *monv1.BasicAuth `json:"basicAuth,omitempty"`
	// Bearer token for remote write.
	BearerTokenFile string `json:"bearerTokenFile,omitempty"`
	// TLS Config to use for remote write.
	TLSConfig *monv1.SafeTLSConfig `json:"tlsConfig,omitempty"`
	// Optional ProxyURL
	ProxyURL string `json:"proxyUrl,omitempty"`
	// QueueConfig allows tuning of the remote write queue parameters.
	QueueConfig *monv1.QueueConfig `json:"queueConfig,omitempty"`
	// MetadataConfig configures the sending of series metadata to remote storage.
	MetadataConfig *monv1.MetadataConfig `json:"metadataConfig,omitempty"`
}

type PrometheusK8sConfig struct {
	LogLevel            string                               `json:"logLevel"`
	Retention           string                               `json:"retention"`
	NodeSelector        map[string]string                    `json:"nodeSelector"`
	Tolerations         []v1.Toleration                      `json:"tolerations"`
	Resources           *v1.ResourceRequirements             `json:"resources"`
	ExternalLabels      map[string]string                    `json:"externalLabels"`
	VolumeClaimTemplate *monv1.EmbeddedPersistentVolumeClaim `json:"volumeClaimTemplate"`
	RemoteWrite         []RemoteWriteSpec                    `json:"remoteWrite"`
	TelemetryMatches    []string                             `json:"-"`
	AlertmanagerConfigs []AdditionalAlertmanagerConfig       `json:"additionalAlertmanagerConfigs"`
}

type AdditionalAlertmanagerConfig struct {
	// The URL scheme to use when talking to Alertmanagers.
	Scheme string `json:"scheme,omitempty"`
	// Path prefix to add in front of the push endpoint path.
	PathPrefix string `json:"pathPrefix,omitempty"`
	// The timeout used when sending alerts.
	Timeout *string `json:"timeout,omitempty"`
	// The api version of Alertmanager.
	APIVersion string `json:"apiVersion"`
	// TLS Config to use for alertmanager connection.
	TLSConfig TLSConfig `json:"tlsConfig,omitempty"`
	// Bearer token to use when authenticating to Alertmanager.
	BearerToken *v1.SecretKeySelector `json:"bearerToken,omitempty"`
	// List of statically configured Alertmanagers.
	StaticConfigs []string `json:"staticConfigs,omitempty"`
}

// TLSConfig configures the options for TLS connections.
type TLSConfig struct {
	// The CA cert in the Prometheus container to use for the targets.
	CA *v1.SecretKeySelector `json:"ca,omitempty"`
	// The client cert in the Prometheus container to use for the targets.
	Cert *v1.SecretKeySelector `json:"cert,omitempty"`
	// The client key in the Prometheus container to use for the targets.
	Key *v1.SecretKeySelector `json:"key,omitempty"`
	// Used to verify the hostname for the targets.
	ServerName string `json:"serverName,omitempty"`
	// Disable target certificate validation.
	InsecureSkipVerify bool `json:"insecureSkipVerify"`
}

type AlertmanagerMainConfig struct {
	Enabled             *bool                                `json:"enabled"`
	LogLevel            string                               `json:"logLevel"`
	NodeSelector        map[string]string                    `json:"nodeSelector"`
	Tolerations         []v1.Toleration                      `json:"tolerations"`
	Resources           *v1.ResourceRequirements             `json:"resources"`
	VolumeClaimTemplate *monv1.EmbeddedPersistentVolumeClaim `json:"volumeClaimTemplate"`
}

func (a AlertmanagerMainConfig) IsEnabled() bool {
	return a.Enabled == nil || *a.Enabled
}

type ThanosRulerConfig struct {
	LogLevel             string                               `json:"logLevel"`
	NodeSelector         map[string]string                    `json:"nodeSelector"`
	Tolerations          []v1.Toleration                      `json:"tolerations"`
	Resources            *v1.ResourceRequirements             `json:"resources"`
	VolumeClaimTemplate  *monv1.EmbeddedPersistentVolumeClaim `json:"volumeClaimTemplate"`
	AlertmanagersConfigs []AdditionalAlertmanagerConfig       `json:"additionalAlertmanagerConfigs"`
}

type ThanosQuerierConfig struct {
	LogLevel     string                   `json:"logLevel"`
	NodeSelector map[string]string        `json:"nodeSelector"`
	Tolerations  []v1.Toleration          `json:"tolerations"`
	Resources    *v1.ResourceRequirements `json:"resources"`
}

type GrafanaConfig struct {
	Enabled      *bool             `json:"enabled"`
	NodeSelector map[string]string `json:"nodeSelector"`
	Tolerations  []v1.Toleration   `json:"tolerations"`
}

// IsEnabled returns the underlying value of the `Enabled` boolean pointer.  It
// defaults to TRUE if the pointer is nil because Grafana should be enabled by
// default.
func (g *GrafanaConfig) IsEnabled() bool {
	if g.Enabled == nil {
		return true
	}
	return *g.Enabled
}

type KubeStateMetricsConfig struct {
	NodeSelector map[string]string `json:"nodeSelector"`
	Tolerations  []v1.Toleration   `json:"tolerations"`
}

type OpenShiftStateMetricsConfig struct {
	NodeSelector map[string]string `json:"nodeSelector"`
	Tolerations  []v1.Toleration   `json:"tolerations"`
}

type K8sPrometheusAdapter struct {
	NodeSelector map[string]string `json:"nodeSelector"`
	Tolerations  []v1.Toleration   `json:"tolerations"`
	Audit        *Audit            `json:"audit"`
}

type Audit struct {
	Profile auditv1.Level `json:"profile"`
}

type EtcdConfig struct {
	Enabled *bool `json:"-"`
}

// IsEnabled returns the underlying value of the `Enabled` boolean pointer.
// It defaults to false if the pointer is nil.
func (e *EtcdConfig) IsEnabled() bool {
	if e.Enabled == nil {
		return false
	}
	return *e.Enabled
}

type TelemeterClientConfig struct {
	ClusterID          string            `json:"clusterID"`
	Enabled            *bool             `json:"enabled"`
	TelemeterServerURL string            `json:"telemeterServerURL"`
	Token              string            `json:"token"`
	NodeSelector       map[string]string `json:"nodeSelector"`
	Tolerations        []v1.Toleration   `json:"tolerations"`
}

func (cfg *TelemeterClientConfig) IsEnabled() bool {
	if cfg == nil {
		return false
	}

	if (cfg.Enabled != nil && *cfg.Enabled == false) ||
		cfg.ClusterID == "" ||
		cfg.Token == "" {
		return false
	}

	return true
}

func NewConfig(content io.Reader) (*Config, error) {
	c := Config{}
	cmc := ClusterMonitoringConfiguration{}
	err := k8syaml.NewYAMLOrJSONDecoder(content, 4096).Decode(&cmc)
	if err != nil {
		return nil, err
	}
	c.ClusterMonitoringConfiguration = &cmc
	res := &c
	res.applyDefaults()
	c.UserWorkloadConfiguration = NewDefaultUserWorkloadMonitoringConfig()

	return res, nil
}

func (c *Config) applyDefaults() {
	if c.Images == nil {
		c.Images = &Images{}
	}
	if c.ClusterMonitoringConfiguration == nil {
		c.ClusterMonitoringConfiguration = &ClusterMonitoringConfiguration{}
	}
	if c.ClusterMonitoringConfiguration.PrometheusOperatorConfig == nil {
		c.ClusterMonitoringConfiguration.PrometheusOperatorConfig = &PrometheusOperatorConfig{}
	}
	if c.ClusterMonitoringConfiguration.PrometheusK8sConfig == nil {
		c.ClusterMonitoringConfiguration.PrometheusK8sConfig = &PrometheusK8sConfig{}
	}
	if c.ClusterMonitoringConfiguration.PrometheusK8sConfig.Retention == "" {
		c.ClusterMonitoringConfiguration.PrometheusK8sConfig.Retention = DefaultRetentionValue
	}
	if c.ClusterMonitoringConfiguration.AlertmanagerMainConfig == nil {
		c.ClusterMonitoringConfiguration.AlertmanagerMainConfig = &AlertmanagerMainConfig{}
	}
	if c.ClusterMonitoringConfiguration.UserWorkloadEnabled == nil {
		disable := false
		c.ClusterMonitoringConfiguration.UserWorkloadEnabled = &disable
	}
	if c.ClusterMonitoringConfiguration.ThanosQuerierConfig == nil {
		c.ClusterMonitoringConfiguration.ThanosQuerierConfig = &ThanosQuerierConfig{}
	}
	if c.ClusterMonitoringConfiguration.GrafanaConfig == nil {
		c.ClusterMonitoringConfiguration.GrafanaConfig = &GrafanaConfig{}
	}
	if c.ClusterMonitoringConfiguration.KubeStateMetricsConfig == nil {
		c.ClusterMonitoringConfiguration.KubeStateMetricsConfig = &KubeStateMetricsConfig{}
	}
	if c.ClusterMonitoringConfiguration.OpenShiftMetricsConfig == nil {
		c.ClusterMonitoringConfiguration.OpenShiftMetricsConfig = &OpenShiftStateMetricsConfig{}
	}
	if c.ClusterMonitoringConfiguration.HTTPConfig == nil {
		c.ClusterMonitoringConfiguration.HTTPConfig = &HTTPConfig{}
	}
	if c.ClusterMonitoringConfiguration.TelemeterClientConfig == nil {
		c.ClusterMonitoringConfiguration.TelemeterClientConfig = &TelemeterClientConfig{
			TelemeterServerURL: "https://infogw.api.openshift.com/",
		}
	}

	if c.ClusterMonitoringConfiguration.K8sPrometheusAdapter == nil {
		c.ClusterMonitoringConfiguration.K8sPrometheusAdapter = &K8sPrometheusAdapter{}
	}
	if c.ClusterMonitoringConfiguration.K8sPrometheusAdapter.Audit == nil {
		c.ClusterMonitoringConfiguration.K8sPrometheusAdapter.Audit = &Audit{}
	}
	if c.ClusterMonitoringConfiguration.K8sPrometheusAdapter.Audit.Profile == "" {
		c.ClusterMonitoringConfiguration.K8sPrometheusAdapter.Audit.Profile = auditv1.LevelMetadata
	}

	if c.ClusterMonitoringConfiguration.EtcdConfig == nil {
		c.ClusterMonitoringConfiguration.EtcdConfig = &EtcdConfig{}
	}
}

func (c *Config) SetImages(images map[string]string) {
	c.Images.PrometheusOperator = images["prometheus-operator"]
	c.Images.PrometheusConfigReloader = images["prometheus-config-reloader"]
	c.Images.Prometheus = images["prometheus"]
	c.Images.Alertmanager = images["alertmanager"]
	c.Images.Grafana = images["grafana"]
	c.Images.OauthProxy = images["oauth-proxy"]
	c.Images.NodeExporter = images["node-exporter"]
	c.Images.KubeStateMetrics = images["kube-state-metrics"]
	c.Images.KubeRbacProxy = images["kube-rbac-proxy"]
	c.Images.TelemeterClient = images["telemeter-client"]
	c.Images.PromLabelProxy = images["prom-label-proxy"]
	c.Images.K8sPrometheusAdapter = images["k8s-prometheus-adapter"]
	c.Images.OpenShiftStateMetrics = images["openshift-state-metrics"]
	c.Images.Thanos = images["thanos"]
}

func (c *Config) SetTelemetryMatches(matches []string) {
	c.ClusterMonitoringConfiguration.PrometheusK8sConfig.TelemetryMatches = matches
}

func (c *Config) SetRemoteWrite(rw bool) {
	c.RemoteWrite = rw
	if c.RemoteWrite && c.ClusterMonitoringConfiguration.TelemeterClientConfig.TelemeterServerURL == "https://infogw.api.openshift.com/" {
		c.ClusterMonitoringConfiguration.TelemeterClientConfig.TelemeterServerURL = "https://infogw.api.openshift.com/metrics/v1/receive"
	}
}

func (c *Config) LoadClusterID(load func() (*configv1.ClusterVersion, error)) error {
	if c.ClusterMonitoringConfiguration.TelemeterClientConfig.ClusterID != "" {
		return nil
	}

	cv, err := load()
	if err != nil {
		return fmt.Errorf("error loading cluster version: %v", err)
	}

	c.ClusterMonitoringConfiguration.TelemeterClientConfig.ClusterID = string(cv.Spec.ClusterID)
	return nil
}

func (c *Config) LoadToken(load func() (*v1.Secret, error)) error {
	if c.ClusterMonitoringConfiguration.TelemeterClientConfig.Token != "" {
		return nil
	}

	secret, err := load()
	if err != nil {
		return fmt.Errorf("error loading secret: %v", err)
	}

	if secret.Type != v1.SecretTypeDockerConfigJson {
		return fmt.Errorf("error expecting secret type %s got %s", v1.SecretTypeDockerConfigJson, secret.Type)
	}

	ps := struct {
		Auths struct {
			COC struct {
				Auth string `json:"auth"`
			} `json:"cloud.openshift.com"`
		} `json:"auths"`
	}{}

	if err := json.Unmarshal(secret.Data[v1.DockerConfigJsonKey], &ps); err != nil {
		return fmt.Errorf("unmarshaling pull secret failed: %v", err)
	}

	c.ClusterMonitoringConfiguration.TelemeterClientConfig.Token = ps.Auths.COC.Auth
	return nil
}

// HTTPProxy implements the ProxyReader interface.
func (c *Config) HTTPProxy() string {
	return c.ClusterMonitoringConfiguration.HTTPConfig.HTTPProxy
}

// HTTPSProxy implements the ProxyReader interface.
func (c *Config) HTTPSProxy() string {
	return c.ClusterMonitoringConfiguration.HTTPConfig.HTTPSProxy
}

// NoProxy implements the ProxyReader interface.
func (c *Config) NoProxy() string {
	return c.ClusterMonitoringConfiguration.HTTPConfig.NoProxy
}

func NewConfigFromString(content string) (*Config, error) {
	if content == "" {
		return NewDefaultConfig(), nil
	}

	return NewConfig(bytes.NewBuffer([]byte(content)))
}

func NewDefaultConfig() *Config {
	c := &Config{}
	cmc := ClusterMonitoringConfiguration{}
	c.ClusterMonitoringConfiguration = &cmc
	c.UserWorkloadConfiguration = NewDefaultUserWorkloadMonitoringConfig()
	c.applyDefaults()
	return c
}

type UserWorkloadConfiguration struct {
	PrometheusOperator *PrometheusOperatorConfig   `json:"prometheusOperator"`
	Prometheus         *PrometheusRestrictedConfig `json:"prometheus"`
	ThanosRuler        *ThanosRulerConfig          `json:"thanosRuler"`
}

type PrometheusRestrictedConfig struct {
	LogLevel            string                               `json:"logLevel"`
	Retention           string                               `json:"retention"`
	NodeSelector        map[string]string                    `json:"nodeSelector"`
	Tolerations         []v1.Toleration                      `json:"tolerations"`
	Resources           *v1.ResourceRequirements             `json:"resources"`
	ExternalLabels      map[string]string                    `json:"externalLabels"`
	VolumeClaimTemplate *monv1.EmbeddedPersistentVolumeClaim `json:"volumeClaimTemplate"`
	RemoteWrite         []RemoteWriteSpec                    `json:"remoteWrite"`
	EnforcedSampleLimit *uint64                              `json:"enforcedSampleLimit"`
	EnforcedTargetLimit *uint64                              `json:"enforcedTargetLimit"`
	AlertmanagerConfigs []AdditionalAlertmanagerConfig       `json:"additionalAlertmanagerConfigs"`
}

func (u *UserWorkloadConfiguration) applyDefaults() {
	if u.PrometheusOperator == nil {
		u.PrometheusOperator = &PrometheusOperatorConfig{}
	}
	if u.Prometheus == nil {
		u.Prometheus = &PrometheusRestrictedConfig{}
	}
	if u.ThanosRuler == nil {
		u.ThanosRuler = &ThanosRulerConfig{}
	}
}

func NewUserConfigFromString(content string) (*UserWorkloadConfiguration, error) {
	if content == "" {
		return NewDefaultUserWorkloadMonitoringConfig(), nil
	}
	u := &UserWorkloadConfiguration{}
	err := k8syaml.NewYAMLOrJSONDecoder(bytes.NewBuffer([]byte(content)), 100).Decode(&u)
	if err != nil {
		return nil, err
	}

	u.applyDefaults()

	return u, nil
}

func NewDefaultUserWorkloadMonitoringConfig() *UserWorkloadConfiguration {
	u := &UserWorkloadConfiguration{}
	u.applyDefaults()
	return u
}
