package auditlogpersistence

import (
	"encoding/json"
	"testing"

	. "github.com/onsi/gomega"

	auditlogpersistencev1alpha1 "github.com/openshift/hypershift/api/auditlogpersistence/v1alpha1"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/utils/ptr"

	"github.com/go-logr/logr"
)

func TestMutateConfigMap(t *testing.T) {
	tests := []struct {
		name           string
		configMap      *corev1.ConfigMap
		config         *auditlogpersistencev1alpha1.AuditLogPersistenceConfig
		expectedError  bool
		validateResult func(*WithT, *corev1.ConfigMap)
	}{
		{
			name: "ConfigMap with nil Data initializes Data map and sets audit log values",
			configMap: &corev1.ConfigMap{
				ObjectMeta: metav1.ObjectMeta{
					Name:      kasConfigMapName,
					Namespace: "test-ns",
				},
				Data: map[string]string{
					kubeAPIServerConfigKey: `{
						"apiVersion": "v1",
						"kind": "KubeAPIServerConfig"
					}`,
				},
			},
			config: &auditlogpersistencev1alpha1.AuditLogPersistenceConfig{
				Spec: auditlogpersistencev1alpha1.AuditLogPersistenceConfigSpec{
					Enabled: true,
					AuditLog: auditlogpersistencev1alpha1.AuditLogConfig{
						MaxSize:   ptr.To(int32(200)),
						MaxBackup: ptr.To(int32(10)),
					},
				},
			},
			expectedError: false,
			validateResult: func(g *WithT, cm *corev1.ConfigMap) {
				g.Expect(cm.Data).ToNot(BeNil())

				// Verify config.json exists and contains the audit log settings
				configJSON, exists := cm.Data[kubeAPIServerConfigKey]
				g.Expect(exists).To(BeTrue())
				g.Expect(configJSON).ToNot(BeEmpty())

				var result map[string]interface{}
				err := json.Unmarshal([]byte(configJSON), &result)
				g.Expect(err).ToNot(HaveOccurred())

				apiServerArgs, exists, err := unstructured.NestedMap(result, "apiServerArguments")
				g.Expect(err).ToNot(HaveOccurred())
				g.Expect(exists).To(BeTrue())
				g.Expect(apiServerArgs).ToNot(BeNil())

				// Verify audit-log-maxsize was set correctly
				maxSize, exists, err := unstructured.NestedStringSlice(apiServerArgs, "audit-log-maxsize")
				g.Expect(err).ToNot(HaveOccurred())
				g.Expect(exists).To(BeTrue())
				g.Expect(maxSize).To(Equal([]string{"200"}))

				// Verify audit-log-maxbackup was set correctly
				maxBackup, exists, err := unstructured.NestedStringSlice(apiServerArgs, "audit-log-maxbackup")
				g.Expect(err).ToNot(HaveOccurred())
				g.Expect(exists).To(BeTrue())
				g.Expect(maxBackup).To(Equal([]string{"10"}))
			},
		},
		{
			name: "ConfigMap without config.json key returns early",
			configMap: &corev1.ConfigMap{
				ObjectMeta: metav1.ObjectMeta{
					Name:      kasConfigMapName,
					Namespace: "test-ns",
				},
				Data: map[string]string{
					"other-key": "value",
				},
			},
			config: &auditlogpersistencev1alpha1.AuditLogPersistenceConfig{
				Spec: auditlogpersistencev1alpha1.AuditLogPersistenceConfigSpec{
					Enabled: true,
					AuditLog: auditlogpersistencev1alpha1.AuditLogConfig{
						MaxSize:   ptr.To(int32(200)),
						MaxBackup: ptr.To(int32(10)),
					},
				},
			},
			expectedError: false,
			validateResult: func(g *WithT, cm *corev1.ConfigMap) {
				g.Expect(cm.Data["other-key"]).To(Equal("value"))
				_, found := cm.Data[kubeAPIServerConfigKey]
				g.Expect(found).To(BeFalse())
			},
		},
		{
			name: "ConfigMap with empty config.json returns error",
			configMap: &corev1.ConfigMap{
				ObjectMeta: metav1.ObjectMeta{
					Name:      kasConfigMapName,
					Namespace: "test-ns",
				},
				Data: map[string]string{
					kubeAPIServerConfigKey: "",
				},
			},
			config: &auditlogpersistencev1alpha1.AuditLogPersistenceConfig{
				Spec: auditlogpersistencev1alpha1.AuditLogPersistenceConfigSpec{
					Enabled: true,
					AuditLog: auditlogpersistencev1alpha1.AuditLogConfig{
						MaxSize:   ptr.To(int32(200)),
						MaxBackup: ptr.To(int32(10)),
					},
				},
			},
			expectedError: true,
		},
		{
			name: "ConfigMap with invalid JSON returns error",
			configMap: &corev1.ConfigMap{
				ObjectMeta: metav1.ObjectMeta{
					Name:      kasConfigMapName,
					Namespace: "test-ns",
				},
				Data: map[string]string{
					kubeAPIServerConfigKey: `{invalid json}`,
				},
			},
			config: &auditlogpersistencev1alpha1.AuditLogPersistenceConfig{
				Spec: auditlogpersistencev1alpha1.AuditLogPersistenceConfigSpec{
					Enabled: true,
					AuditLog: auditlogpersistencev1alpha1.AuditLogConfig{
						MaxSize:   ptr.To(int32(200)),
						MaxBackup: ptr.To(int32(10)),
					},
				},
			},
			expectedError: true,
		},
		{
			name: "ConfigMap with valid JSON but no apiServerArguments creates it",
			configMap: &corev1.ConfigMap{
				ObjectMeta: metav1.ObjectMeta{
					Name:      kasConfigMapName,
					Namespace: "test-ns",
				},
				Data: map[string]string{
					kubeAPIServerConfigKey: `{"apiVersion":"v1","kind":"KubeAPIServerConfig"}`,
				},
			},
			config: &auditlogpersistencev1alpha1.AuditLogPersistenceConfig{
				Spec: auditlogpersistencev1alpha1.AuditLogPersistenceConfigSpec{
					Enabled: true,
					AuditLog: auditlogpersistencev1alpha1.AuditLogConfig{
						MaxSize:   ptr.To(int32(200)),
						MaxBackup: ptr.To(int32(10)),
					},
				},
			},
			expectedError: false,
			validateResult: func(g *WithT, cm *corev1.ConfigMap) {
				var result map[string]interface{}
				err := json.Unmarshal([]byte(cm.Data[kubeAPIServerConfigKey]), &result)
				g.Expect(err).ToNot(HaveOccurred())

				apiServerArgs, exists, err := unstructured.NestedMap(result, "apiServerArguments")
				g.Expect(err).ToNot(HaveOccurred())
				g.Expect(exists).To(BeTrue())
				g.Expect(apiServerArgs).ToNot(BeNil())

				maxSize, exists, err := unstructured.NestedStringSlice(apiServerArgs, "audit-log-maxsize")
				g.Expect(err).ToNot(HaveOccurred())
				g.Expect(exists).To(BeTrue())
				g.Expect(maxSize).To(Equal([]string{"200"}))

				maxBackup, exists, err := unstructured.NestedStringSlice(apiServerArgs, "audit-log-maxbackup")
				g.Expect(err).ToNot(HaveOccurred())
				g.Expect(exists).To(BeTrue())
				g.Expect(maxBackup).To(Equal([]string{"10"}))
			},
		},
		{
			name: "ConfigMap with existing apiServerArguments updates audit log settings",
			configMap: &corev1.ConfigMap{
				ObjectMeta: metav1.ObjectMeta{
					Name:      kasConfigMapName,
					Namespace: "test-ns",
				},
				Data: map[string]string{
					kubeAPIServerConfigKey: `{
						"apiVersion": "v1",
						"kind": "KubeAPIServerConfig",
						"apiServerArguments": {
							"other-arg": ["value1", "value2"],
							"audit-log-maxsize": ["100"],
							"audit-log-maxbackup": ["5"]
						}
					}`,
				},
			},
			config: &auditlogpersistencev1alpha1.AuditLogPersistenceConfig{
				Spec: auditlogpersistencev1alpha1.AuditLogPersistenceConfigSpec{
					Enabled: true,
					AuditLog: auditlogpersistencev1alpha1.AuditLogConfig{
						MaxSize:   ptr.To(int32(200)),
						MaxBackup: ptr.To(int32(10)),
					},
				},
			},
			expectedError: false,
			validateResult: func(g *WithT, cm *corev1.ConfigMap) {
				var result map[string]interface{}
				err := json.Unmarshal([]byte(cm.Data[kubeAPIServerConfigKey]), &result)
				g.Expect(err).ToNot(HaveOccurred())

				apiServerArgs, exists, err := unstructured.NestedMap(result, "apiServerArguments")
				g.Expect(err).ToNot(HaveOccurred())
				g.Expect(exists).To(BeTrue())

				// Verify audit log settings were updated
				maxSize, exists, err := unstructured.NestedStringSlice(apiServerArgs, "audit-log-maxsize")
				g.Expect(err).ToNot(HaveOccurred())
				g.Expect(exists).To(BeTrue())
				g.Expect(maxSize).To(Equal([]string{"200"}))

				maxBackup, exists, err := unstructured.NestedStringSlice(apiServerArgs, "audit-log-maxbackup")
				g.Expect(err).ToNot(HaveOccurred())
				g.Expect(exists).To(BeTrue())
				g.Expect(maxBackup).To(Equal([]string{"10"}))

				// Verify other args are preserved
				otherArg, exists, err := unstructured.NestedStringSlice(apiServerArgs, "other-arg")
				g.Expect(err).ToNot(HaveOccurred())
				g.Expect(exists).To(BeTrue())
				g.Expect(otherArg).To(Equal([]string{"value1", "value2"}))
			},
		},
		{
			name: "Only MaxSize is set when MaxBackup is zero",
			configMap: &corev1.ConfigMap{
				ObjectMeta: metav1.ObjectMeta{
					Name:      kasConfigMapName,
					Namespace: "test-ns",
				},
				Data: map[string]string{
					kubeAPIServerConfigKey: `{
						"apiVersion": "v1",
						"kind": "KubeAPIServerConfig"
					}`,
				},
			},
			config: &auditlogpersistencev1alpha1.AuditLogPersistenceConfig{
				Spec: auditlogpersistencev1alpha1.AuditLogPersistenceConfigSpec{
					Enabled: true,
					AuditLog: auditlogpersistencev1alpha1.AuditLogConfig{
						MaxSize:   ptr.To(int32(200)),
						MaxBackup: nil,
					},
				},
			},
			expectedError: false,
			validateResult: func(g *WithT, cm *corev1.ConfigMap) {
				var result map[string]interface{}
				err := json.Unmarshal([]byte(cm.Data[kubeAPIServerConfigKey]), &result)
				g.Expect(err).ToNot(HaveOccurred())

				apiServerArgs, exists, err := unstructured.NestedMap(result, "apiServerArguments")
				g.Expect(err).ToNot(HaveOccurred())
				g.Expect(exists).To(BeTrue())

				maxSize, exists, err := unstructured.NestedStringSlice(apiServerArgs, "audit-log-maxsize")
				g.Expect(err).ToNot(HaveOccurred())
				g.Expect(exists).To(BeTrue())
				g.Expect(maxSize).To(Equal([]string{"200"}))

				// MaxBackup should not be set
				_, exists, err = unstructured.NestedStringSlice(apiServerArgs, "audit-log-maxbackup")
				g.Expect(err).ToNot(HaveOccurred())
				g.Expect(exists).To(BeFalse())
			},
		},
		{
			name: "Only MaxBackup is set when MaxSize is empty",
			configMap: &corev1.ConfigMap{
				ObjectMeta: metav1.ObjectMeta{
					Name:      kasConfigMapName,
					Namespace: "test-ns",
				},
				Data: map[string]string{
					kubeAPIServerConfigKey: `{
						"apiVersion": "v1",
						"kind": "KubeAPIServerConfig"
					}`,
				},
			},
			config: &auditlogpersistencev1alpha1.AuditLogPersistenceConfig{
				Spec: auditlogpersistencev1alpha1.AuditLogPersistenceConfigSpec{
					Enabled: true,
					AuditLog: auditlogpersistencev1alpha1.AuditLogConfig{
						MaxSize:   nil,
						MaxBackup: ptr.To(int32(10)),
					},
				},
			},
			expectedError: false,
			validateResult: func(g *WithT, cm *corev1.ConfigMap) {
				var result map[string]interface{}
				err := json.Unmarshal([]byte(cm.Data[kubeAPIServerConfigKey]), &result)
				g.Expect(err).ToNot(HaveOccurred())

				apiServerArgs, exists, err := unstructured.NestedMap(result, "apiServerArguments")
				g.Expect(err).ToNot(HaveOccurred())
				g.Expect(exists).To(BeTrue())

				// MaxSize should not be set
				_, exists, err = unstructured.NestedStringSlice(apiServerArgs, "audit-log-maxsize")
				g.Expect(err).ToNot(HaveOccurred())
				g.Expect(exists).To(BeFalse())

				maxBackup, exists, err := unstructured.NestedStringSlice(apiServerArgs, "audit-log-maxbackup")
				g.Expect(err).ToNot(HaveOccurred())
				g.Expect(exists).To(BeTrue())
				g.Expect(maxBackup).To(Equal([]string{"10"}))
			},
		},
		{
			name: "Complex config with nested structures preserves other fields",
			configMap: &corev1.ConfigMap{
				ObjectMeta: metav1.ObjectMeta{
					Name:      kasConfigMapName,
					Namespace: "test-ns",
				},
				Data: map[string]string{
					kubeAPIServerConfigKey: `{
						"apiVersion": "v1",
						"kind": "KubeAPIServerConfig",
						"servingInfo": {
							"bindAddress": "0.0.0.0:6443"
						},
						"apiServerArguments": {
							"feature-gates": ["Feature1=true", "Feature2=false"]
						}
					}`,
				},
			},
			config: &auditlogpersistencev1alpha1.AuditLogPersistenceConfig{
				Spec: auditlogpersistencev1alpha1.AuditLogPersistenceConfigSpec{
					Enabled: true,
					AuditLog: auditlogpersistencev1alpha1.AuditLogConfig{
						MaxSize:   ptr.To(int32(200)),
						MaxBackup: ptr.To(int32(10)),
					},
				},
			},
			expectedError: false,
			validateResult: func(g *WithT, cm *corev1.ConfigMap) {
				var result map[string]interface{}
				err := json.Unmarshal([]byte(cm.Data[kubeAPIServerConfigKey]), &result)
				g.Expect(err).ToNot(HaveOccurred())

				// Verify servingInfo is preserved
				servingInfo, exists, err := unstructured.NestedMap(result, "servingInfo")
				g.Expect(err).ToNot(HaveOccurred())
				g.Expect(exists).To(BeTrue())
				g.Expect(servingInfo["bindAddress"]).To(Equal("0.0.0.0:6443"))

				apiServerArgs, exists, err := unstructured.NestedMap(result, "apiServerArguments")
				g.Expect(err).ToNot(HaveOccurred())
				g.Expect(exists).To(BeTrue())

				// Verify feature-gates are preserved
				featureGates, exists, err := unstructured.NestedStringSlice(apiServerArgs, "feature-gates")
				g.Expect(err).ToNot(HaveOccurred())
				g.Expect(exists).To(BeTrue())
				g.Expect(featureGates).To(Equal([]string{"Feature1=true", "Feature2=false"}))

				// Verify audit log settings were added
				maxSize, exists, err := unstructured.NestedStringSlice(apiServerArgs, "audit-log-maxsize")
				g.Expect(err).ToNot(HaveOccurred())
				g.Expect(exists).To(BeTrue())
				g.Expect(maxSize).To(Equal([]string{"200"}))

				maxBackup, exists, err := unstructured.NestedStringSlice(apiServerArgs, "audit-log-maxbackup")
				g.Expect(err).ToNot(HaveOccurred())
				g.Expect(exists).To(BeTrue())
				g.Expect(maxBackup).To(Equal([]string{"10"}))
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			g := NewWithT(t)

			handler := &ConfigMapWebhookHandler{
				log:     logr.Discard(),
				client:  nil,
				decoder: nil,
			}

			err := handler.mutateConfigMap(tt.configMap, &tt.config.Spec)

			if tt.expectedError {
				g.Expect(err).To(HaveOccurred())
			} else {
				g.Expect(err).ToNot(HaveOccurred())
				if tt.validateResult != nil {
					tt.validateResult(g, tt.configMap)
				}
			}
		})
	}
}
