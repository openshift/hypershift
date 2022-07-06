package uwmtelemetry

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strings"

	"github.com/go-logr/logr"
	monv1 "github.com/prometheus-operator/prometheus-operator/pkg/apis/monitoring/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/yaml"

	"github.com/openshift/hypershift/hypershift-operator/controllers/manifests/monitoring"
	"github.com/openshift/hypershift/support/upsert"
)

const (
	telemetryRemoteWriteURL = "https://infogw.api.openshift.com/metrics/v1/receive"
)

type UWMTelemetryReconciler struct {
	client.Client
	upsert.CreateOrUpdateProvider
}

func (r *UWMTelemetryReconciler) SetupWithManager(mgr ctrl.Manager) error {
	_, err := ctrl.NewControllerManagedBy(mgr).
		For(&corev1.Namespace{}).
		Build(r)
	if err != nil {
		return fmt.Errorf("failed setting up with a controller manager: %w", err)
	}
	return nil
}

func (r *UWMTelemetryReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	log, err := logr.FromContext(ctx)
	if err != nil {
		return ctrl.Result{}, fmt.Errorf("no logger found: %w", err)
	}
	log.Info("reconciling UWM telemetry")

	monitoringNamespace := monitoring.MonitoringNamespace()
	if err = r.Get(ctx, client.ObjectKeyFromObject(monitoringNamespace), monitoringNamespace); err != nil {
		if apierrors.IsNotFound(err) {
			log.Info("The openshift-monitoring namespace does not exist. Nothing to do")
			return ctrl.Result{}, nil
		}
		return ctrl.Result{}, fmt.Errorf("failed to get monitoring namespace: %w", err)
	}

	if err := r.reconcileMonitoringConfig(ctx); err != nil {
		return ctrl.Result{}, err
	}

	uwmNamespace := monitoring.UserWorkloadMonitoringNamespace()
	if err = r.Get(ctx, client.ObjectKeyFromObject(uwmNamespace), uwmNamespace); err != nil {
		if apierrors.IsNotFound(err) {
			log.Info("The openshift-user-workload-monitoring namespace does not exist yet.")
			return ctrl.Result{}, nil
		}
	}

	clusterVersion := monitoring.ClusterVersion()
	if err = r.Get(ctx, client.ObjectKeyFromObject(clusterVersion), clusterVersion); err != nil {
		return ctrl.Result{}, fmt.Errorf("failed to get clusterversion resource: %w", err)
	}

	if err := r.reconcileTelemetryRemoteWrite(ctx, string(clusterVersion.Spec.ClusterID)); err != nil {
		return ctrl.Result{}, err
	}

	return ctrl.Result{}, nil
}

func (r *UWMTelemetryReconciler) reconcileMonitoringConfig(ctx context.Context) error {
	monitoringConfig := monitoring.MonitoringConfig()
	if _, err := r.CreateOrUpdateProvider.CreateOrUpdate(ctx, r.Client, monitoringConfig, func() error {
		return reconcileMonitoringConfigContent(monitoringConfig)
	}); err != nil {
		return fmt.Errorf("failed to reconcile monitoring config: %w", err)
	}
	return nil
}

func (r *UWMTelemetryReconciler) reconcileTelemetryRemoteWrite(ctx context.Context, clusterID string) error {
	telemeterClientSecret := monitoring.TelemeterClientSecret()
	if err := r.Get(ctx, client.ObjectKeyFromObject(telemeterClientSecret), telemeterClientSecret); err != nil {
		return fmt.Errorf("cannot get telemeter client secret: %w", err)
	}

	remoteWriteSecret := monitoring.UWMRemoteWriteSecret()
	if _, err := r.CreateOrUpdateProvider.CreateOrUpdate(ctx, r.Client, remoteWriteSecret, func() error {
		return reconcileRemoteWriteSecret(telemeterClientSecret, remoteWriteSecret, clusterID)
	}); err != nil {
		return fmt.Errorf("failed to reconcile remote write secret: %w", err)
	}

	telemetryConfigRules := monitoring.TelemetryConfigRules()
	if err := r.Get(ctx, client.ObjectKeyFromObject(telemetryConfigRules), telemetryConfigRules); err != nil {
		return fmt.Errorf("cannot get telemetry config rules: %w", err)
	}
	relabelConfig, err := telemetryConfigToRelabelConfig(telemetryConfigRules)
	if err != nil {
		return fmt.Errorf("cannot convert telemetry config to relabel config: %w", err)
	}

	uwmConfig := monitoring.UWMConfig()
	if _, err := r.CreateOrUpdateProvider.CreateOrUpdate(ctx, r.Client, uwmConfig, func() error {
		return reconcileUWMConfigContent(uwmConfig, relabelConfig)
	}); err != nil {
		return fmt.Errorf("failed to reconcile user workload monitoring config: %w", err)
	}
	return nil
}

func reconcileMonitoringConfigContent(cm *corev1.ConfigMap) error {
	content := map[string]interface{}{}
	if contentString, exists := cm.Data["config.yaml"]; exists {
		if err := yaml.Unmarshal([]byte(contentString), &content); err != nil {
			return fmt.Errorf("cannot parse current configuration content: %w", err)
		}
	}
	unstructured.SetNestedField(content, true, "enableUserWorkload")
	contentBytes, err := yaml.Marshal(content)
	if err != nil {
		return fmt.Errorf("cannot serialize configuration content: %w", err)
	}
	if cm.Data == nil {
		cm.Data = map[string]string{}
	}
	cm.Data["config.yaml"] = string(contentBytes)
	return nil
}

func reconcileRemoteWriteSecret(src, dest *corev1.Secret, clusterID string) error {
	compositeToken, err := json.Marshal(map[string]string{
		"cluster_id":          clusterID,
		"authorization_token": string(src.Data["token"]),
	})
	if err != nil {
		return fmt.Errorf("failed to serialize composite token: %w", err)
	}
	dest.Type = corev1.SecretTypeOpaque
	dest.Data = map[string][]byte{
		"token": []byte(base64.StdEncoding.EncodeToString(compositeToken)),
	}
	return nil
}

func reconcileUWMConfigContent(cm *corev1.ConfigMap, relabelConfig *monv1.RelabelConfig) error {
	content := map[string]interface{}{}
	if contentString, exists := cm.Data["config.yaml"]; exists {
		if err := yaml.Unmarshal([]byte(contentString), &content); err != nil {
			return fmt.Errorf("cannot parse current configuration content: %w", err)
		}
	}
	remoteWriteConfigs, found, err := unstructured.NestedSlice(content, "prometheus", "remoteWrite")
	if err != nil {
		return fmt.Errorf("cannot read remoteWrite configuration: %w", err)
	}
	if !found {
		remoteWriteConfigs = []interface{}{}
		unstructured.SetNestedSlice(content, remoteWriteConfigs, "prometheus", "remoteWrite")
	}
	foundIndex := -1
	for i, rwConfig := range remoteWriteConfigs {
		rwConfigMap, ok := rwConfig.(map[string]interface{})
		if !ok {
			continue
		}
		url, found, err := unstructured.NestedString(rwConfigMap, "url")
		if err != nil {
			return fmt.Errorf("invalid remote write config: %w", err)
		}
		if !found {
			continue
		}
		if url == telemetryRemoteWriteURL {
			foundIndex = i
			break
		}
	}

	// remote write configuration comes from:
	// https://github.com/openshift/cluster-monitoring-operator/blob/838a238342b2b1ab5c99a18bd271a7b15a1acbd1/pkg/manifests/manifests.go#L1665-L1720
	telemetryRemoteWrite := RemoteWriteSpec{
		URL: telemetryRemoteWriteURL,
		Authorization: &monv1.SafeAuthorization{
			Type: "Bearer",
			Credentials: &corev1.SecretKeySelector{
				LocalObjectReference: corev1.LocalObjectReference{
					Name: monitoring.UWMRemoteWriteSecret().Name,
				},
				Key: "token",
			},
		},
		QueueConfig: &monv1.QueueConfig{
			Capacity:          30000,
			MaxSamplesPerSend: 10000,
			BatchSendDeadline: "1m",
			MinBackoff:        "1s",
			MaxBackoff:        "256s",
		},
	}
	if relabelConfig != nil {
		telemetryRemoteWrite.WriteRelabelConfigs = []monv1.RelabelConfig{*relabelConfig}
	}
	telemetryRemoteWriteMap, err := toJSON(telemetryRemoteWrite)
	if err != nil {
		panic(err.Error())
	}

	if foundIndex != -1 {
		remoteWriteConfigs[foundIndex] = telemetryRemoteWriteMap
	} else {
		remoteWriteConfigs = append(remoteWriteConfigs, telemetryRemoteWriteMap)
	}
	unstructured.SetNestedSlice(content, remoteWriteConfigs, "prometheus", "remoteWrite")
	contentBytes, err := yaml.Marshal(content)
	if err != nil {
		return fmt.Errorf("cannot serialize configuration content: %w", err)
	}
	if cm.Data == nil {
		cm.Data = map[string]string{}
	}
	cm.Data["config.yaml"] = string(contentBytes)
	return nil
}

func toJSON(o interface{}) (map[string]interface{}, error) {
	tempBytes, err := json.Marshal(o)
	if err != nil {
		return nil, err
	}
	result := map[string]interface{}{}
	if err := json.Unmarshal(tempBytes, &result); err != nil {
		return nil, err
	}
	return result, nil
}

var (
	metricSelectorPattern = regexp.MustCompile(`__name__="([^"]+)"`)
)

func telemetryConfigToRelabelConfig(cm *corev1.ConfigMap) (*monv1.RelabelConfig, error) {
	contentStr, ok := cm.Data["metrics.yaml"]
	if !ok {
		return nil, fmt.Errorf("telemetry config %s/%s does not include expected key: metrics.yaml", cm.Namespace, cm.Name)
	}
	content := map[string]interface{}{}
	if err := yaml.Unmarshal([]byte(contentStr), &content); err != nil {
		return nil, fmt.Errorf("unable to parse metrics selectors: %w", err)
	}

	selectors, exists, err := unstructured.NestedStringSlice(content, "matches")
	if !exists || err != nil {
		if !exists {
			err = errors.New("'matches' not found")
		}
		return nil, fmt.Errorf("cannot extract selectors: %w", err)
	}

	var metricsToInclude []string
	for _, sel := range selectors {
		if match := metricSelectorPattern.FindStringSubmatch(sel); len(match) > 1 {
			metricsToInclude = append(metricsToInclude, match[1])
		}
	}
	return &monv1.RelabelConfig{
		Action:       "keep",
		SourceLabels: []monv1.LabelName{"__name__"},
		Regex:        "(" + strings.Join(metricsToInclude, "|") + ")",
	}, nil

}

// RemoteWriteSpec is used for serializing the remote write configuration
// Copied from:
// https://github.com/openshift/cluster-monitoring-operator/blob/838a238342b2b1ab5c99a18bd271a7b15a1acbd1/pkg/manifests/config.go#L153-L188
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
	// Authorization section for remote write
	Authorization *monv1.SafeAuthorization `json:"authorization,omitempty"`
	// Sigv4 allows to configures AWS's Signature Verification 4
	Sigv4 *monv1.Sigv4 `json:"sigv4,omitempty"`
	// TLS Config to use for remote write.
	TLSConfig *monv1.SafeTLSConfig `json:"tlsConfig,omitempty"`
	// Optional ProxyURL
	ProxyURL string `json:"proxyUrl,omitempty"`
	// QueueConfig allows tuning of the remote write queue parameters.
	QueueConfig *monv1.QueueConfig `json:"queueConfig,omitempty"`
	// MetadataConfig configures the sending of series metadata to remote storage.
	MetadataConfig *monv1.MetadataConfig `json:"metadataConfig,omitempty"`
	// OAuth2 configures OAuth2 authentication for remote write.
	OAuth2 *monv1.OAuth2 `json:"oauth2,omitempty"`
}
