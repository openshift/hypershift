package auditlogpersistence

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"

	auditlogpersistencev1alpha1 "github.com/openshift/hypershift/api/auditlogpersistence/v1alpha1"

	admissionv1 "k8s.io/api/admission/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/types"

	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/webhook/admission"

	"github.com/go-logr/logr"
)

const (
	kasConfigMapName       = "kas-config"
	kubeAPIServerConfigKey = "config.json"
)

type ConfigMapWebhookHandler struct {
	log     logr.Logger
	client  client.Client
	decoder admission.Decoder
}

var _ admission.Handler = &ConfigMapWebhookHandler{}

func NewConfigMapWebhookHandler(log logr.Logger, c client.Client, decoder admission.Decoder) *ConfigMapWebhookHandler {
	return &ConfigMapWebhookHandler{
		log:     log.WithName("audit-log-persistence-configmap-webhook"),
		client:  c,
		decoder: decoder,
	}
}

func (h *ConfigMapWebhookHandler) Handle(ctx context.Context, req admission.Request) admission.Response {
	// Only handle ConfigMap resources
	if req.Kind.Group != "" || req.Kind.Kind != "ConfigMap" {
		return admission.Allowed("")
	}

	// Only handle CREATE and UPDATE operations
	if req.Operation != admissionv1.Create && req.Operation != admissionv1.Update {
		return admission.Allowed("")
	}

	// Check if this is the kas-config ConfigMap
	if req.Name != kasConfigMapName {
		return admission.Allowed("")
	}

	// Check if namespace is a control plane namespace
	ns := &corev1.Namespace{}
	if err := h.client.Get(ctx, types.NamespacedName{Name: req.Namespace}, ns); err != nil {
		if apierrors.IsNotFound(err) {
			return admission.Allowed("")
		}
		h.log.Error(err, "Failed to get namespace", "namespace", req.Namespace)
		return admission.Errored(http.StatusInternalServerError, fmt.Errorf("failed to get namespace %s: %w", req.Namespace, err))
	}

	if ns.Labels == nil || ns.Labels[controlPlaneNamespaceLabel] != "true" {
		return admission.Allowed("")
	}

	// Decode the ConfigMap
	configMap := &corev1.ConfigMap{}
	if err := h.decoder.Decode(req, configMap); err != nil {
		h.log.Error(err, "Failed to decode ConfigMap")
		return admission.Errored(http.StatusBadRequest, err)
	}

	// Get the AuditLogPersistenceConfig
	config := &auditlogpersistencev1alpha1.AuditLogPersistenceConfig{}
	if err := h.client.Get(ctx, types.NamespacedName{Name: "cluster"}, config); err != nil {
		if apierrors.IsNotFound(err) {
			return admission.Allowed("")
		}
		h.log.Error(err, "Failed to get AuditLogPersistenceConfig")
		return admission.Errored(http.StatusInternalServerError, fmt.Errorf("failed to get AuditLogPersistenceConfig: %w", err))
	}

	// Apply defaults to a copy of the spec to avoid modifying the original
	spec := config.Spec.DeepCopy()
	ApplyDefaults(spec)

	// Check if feature is enabled
	if !IsEnabled(spec) {
		return admission.Allowed("")
	}

	// Mutate the ConfigMap
	mutated := configMap.DeepCopy()
	if err := h.mutateConfigMap(mutated, spec); err != nil {
		h.log.Error(err, "Failed to mutate ConfigMap")
		return admission.Errored(http.StatusInternalServerError, fmt.Errorf("failed to mutate ConfigMap: %w", err))
	}

	mutatedRaw, err := json.Marshal(mutated)
	if err != nil {
		h.log.Error(err, "Failed to marshal mutated ConfigMap")
		return admission.Errored(http.StatusInternalServerError, fmt.Errorf("failed to marshal mutated ConfigMap: %w", err))
	}

	maxSizeVal := int32(0)
	if spec.AuditLog.MaxSize != nil {
		maxSizeVal = *spec.AuditLog.MaxSize
	}
	maxBackupVal := int32(0)
	if spec.AuditLog.MaxBackup != nil {
		maxBackupVal = *spec.AuditLog.MaxBackup
	}
	h.log.Info("Successfully mutated ConfigMap for audit log persistence", "configmap", configMap.Name, "namespace", configMap.Namespace, "maxSize", maxSizeVal, "maxBackup", maxBackupVal)
	return admission.PatchResponseFromRaw(req.Object.Raw, mutatedRaw)
}

func (h *ConfigMapWebhookHandler) mutateConfigMap(configMap *corev1.ConfigMap, spec *auditlogpersistencev1alpha1.AuditLogPersistenceConfigSpec) error {
	if configMap.Data == nil {
		configMap.Data = make(map[string]string)
	}

	configJSON, exists := configMap.Data[kubeAPIServerConfigKey]
	if !exists {
		return nil
	}

	// Parse the JSON config into unstructured map
	var kasConfigMap map[string]interface{}
	if err := json.Unmarshal([]byte(configJSON), &kasConfigMap); err != nil {
		h.log.Error(err, "Failed to unmarshal kube-apiserver config")
		return fmt.Errorf("failed to unmarshal kube-apiserver config: %w", err)
	}

	// Ensure apiServerArguments exists
	apiServerArgs, exists, err := unstructured.NestedMap(kasConfigMap, "apiServerArguments")
	if err != nil {
		h.log.Error(err, "Failed to get apiServerArguments")
		return fmt.Errorf("failed to get apiServerArguments: %w", err)
	}
	if !exists || apiServerArgs == nil {
		apiServerArgs = make(map[string]interface{})
	}

	// Update audit-log-maxsize and audit-log-maxbackup
	// apiServerArguments values are arrays of strings
	if spec.AuditLog.MaxSize != nil && *spec.AuditLog.MaxSize > 0 {
		maxSizeStr := fmt.Sprintf("%d", *spec.AuditLog.MaxSize)
		apiServerArgs["audit-log-maxsize"] = []interface{}{maxSizeStr}
	}

	if spec.AuditLog.MaxBackup != nil && *spec.AuditLog.MaxBackup > 0 {
		maxBackupStr := fmt.Sprintf("%d", *spec.AuditLog.MaxBackup)
		apiServerArgs["audit-log-maxbackup"] = []interface{}{maxBackupStr}
	}

	// Set the updated apiServerArguments back
	if err := unstructured.SetNestedField(kasConfigMap, apiServerArgs, "apiServerArguments"); err != nil {
		h.log.Error(err, "Failed to set apiServerArguments")
		return fmt.Errorf("failed to set apiServerArguments: %w", err)
	}

	// Serialize back to JSON
	updatedConfigJSON, err := json.Marshal(kasConfigMap)
	if err != nil {
		h.log.Error(err, "Failed to marshal updated kube-apiserver config")
		return fmt.Errorf("failed to marshal updated kube-apiserver config: %w", err)
	}

	configMap.Data[kubeAPIServerConfigKey] = string(updatedConfigJSON)
	return nil
}
