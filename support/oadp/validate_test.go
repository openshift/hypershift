package oadp

import (
	"bytes"
	"context"
	"os"
	"strings"
	"testing"

	hypershiftv1beta1 "github.com/openshift/hypershift/api/hypershift/v1beta1"

	appsv1 "k8s.io/api/apps/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"

	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	velerov1 "github.com/vmware-tanzu/velero/pkg/apis/velero/v1"
)

func TestRenderVeleroResource(t *testing.T) {
	tests := []struct {
		name            string
		resource        interface{}
		expectError     bool
		errorMsg        string
		expectedContent []string // For successful cases, strings that should be in YAML output
	}{
		{
			name: "valid backup resource with detailed content",
			resource: &velerov1.Backup{
				TypeMeta: metav1.TypeMeta{
					APIVersion: "velero.io/v1",
					Kind:       "Backup",
				},
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-backup",
					Namespace: "velero",
				},
				Spec: velerov1.BackupSpec{
					IncludedNamespaces: []string{"default", "kube-system"},
					TTL:                metav1.Duration{Duration: 0},
				},
			},
			expectError: false,
			expectedContent: []string{
				"apiVersion: velero.io/v1",
				"kind: Backup",
				"name: test-backup",
				"namespace: velero",
				"default",
				"kube-system",
			},
		},
		{
			name: "valid restore resource",
			resource: &velerov1.Restore{
				TypeMeta: metav1.TypeMeta{
					APIVersion: "velero.io/v1",
					Kind:       "Restore",
				},
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-restore",
					Namespace: "velero",
				},
			},
			expectError: false,
			expectedContent: []string{
				"apiVersion: velero.io/v1",
				"kind: Restore",
				"name: test-restore",
				"namespace: velero",
			},
		},
		{
			name: "valid schedule resource",
			resource: &velerov1.Schedule{
				TypeMeta: metav1.TypeMeta{
					APIVersion: "velero.io/v1",
					Kind:       "Schedule",
				},
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-schedule",
					Namespace: "velero",
				},
			},
			expectError: false,
			expectedContent: []string{
				"apiVersion: velero.io/v1",
				"kind: Schedule",
				"name: test-schedule",
				"namespace: velero",
			},
		},
		{
			name:        "nil backup resource",
			resource:    (*velerov1.Backup)(nil),
			expectError: true,
			errorMsg:    "backup resource is nil",
		},
		{
			name:        "nil restore resource",
			resource:    (*velerov1.Restore)(nil),
			expectError: true,
			errorMsg:    "restore resource is nil",
		},
		{
			name:        "nil schedule resource",
			resource:    (*velerov1.Schedule)(nil),
			expectError: true,
			errorMsg:    "schedule resource is nil",
		},
		{
			name:        "unsupported resource type",
			resource:    "invalid-resource",
			expectError: true,
			errorMsg:    "unsupported resource type: string",
		},
		{
			name:        "nil interface",
			resource:    nil,
			expectError: true,
			errorMsg:    "unsupported resource type: <nil>",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Capture stdout
			oldStdout := os.Stdout
			r, w, _ := os.Pipe()
			os.Stdout = w

			// Run the function
			err := RenderVeleroResource(tt.resource)

			// Restore stdout
			w.Close()
			os.Stdout = oldStdout

			// Read captured output
			var buf bytes.Buffer
			_, _ = buf.ReadFrom(r)
			output := buf.String()

			// Check error expectations
			if tt.expectError {
				if err == nil {
					t.Errorf("expected error but got none")
				} else if !strings.Contains(err.Error(), tt.errorMsg) {
					t.Errorf("expected error to contain %q, got %q", tt.errorMsg, err.Error())
				}
				// For error cases, output should be empty
				if len(output) > 0 {
					t.Errorf("expected no output for error case, got: %s", output)
				}
			} else {
				if err != nil {
					t.Errorf("unexpected error: %v", err)
				}
				// For successful cases, check that YAML was output
				if len(output) == 0 {
					t.Errorf("expected YAML output but got empty string")
				}
				// Verify it contains basic YAML markers
				if !strings.Contains(output, "apiVersion") || !strings.Contains(output, "kind") {
					t.Errorf("output doesn't look like valid YAML: %s", output)
				}
				// Check specific expected content if provided
				for _, expected := range tt.expectedContent {
					if !strings.Contains(output, expected) {
						t.Errorf("expected output to contain %q, got:\n%s", expected, output)
					}
				}
			}
		})
	}
}

func TestValidateOADPComponents(t *testing.T) {
	scheme := runtime.NewScheme()
	_ = appsv1.AddToScheme(scheme)

	tests := []struct {
		name        string
		namespace   string
		objects     []client.Object
		expectError bool
		errorMsg    string
	}{
		{
			name:        "OADP operator deployment not found",
			namespace:   "openshift-adp",
			objects:     []client.Object{},
			expectError: true,
			errorMsg:    "OADP operator deployment not found",
		},
		{
			name:      "OADP operator deployment not ready",
			namespace: "openshift-adp",
			objects: []client.Object{
				&appsv1.Deployment{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "openshift-adp-controller-manager",
						Namespace: "openshift-adp",
					},
					Status: appsv1.DeploymentStatus{
						ReadyReplicas: 0,
					},
				},
			},
			expectError: true,
			errorMsg:    "OADP operator deployment is not ready",
		},
		{
			name:      "Velero deployment not found",
			namespace: "openshift-adp",
			objects: []client.Object{
				&appsv1.Deployment{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "openshift-adp-controller-manager",
						Namespace: "openshift-adp",
					},
					Status: appsv1.DeploymentStatus{
						ReadyReplicas: 1,
					},
				},
			},
			expectError: true,
			errorMsg:    "velero deployment not found",
		},
		{
			name:      "Velero deployment not ready",
			namespace: "openshift-adp",
			objects: []client.Object{
				&appsv1.Deployment{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "openshift-adp-controller-manager",
						Namespace: "openshift-adp",
					},
					Status: appsv1.DeploymentStatus{
						ReadyReplicas: 1,
					},
				},
				&appsv1.Deployment{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "velero",
						Namespace: "openshift-adp",
					},
					Status: appsv1.DeploymentStatus{
						ReadyReplicas: 0,
					},
				},
			},
			expectError: true,
			errorMsg:    "velero deployment is not ready",
		},
		{
			name:      "All deployments ready",
			namespace: "openshift-adp",
			objects: []client.Object{
				&appsv1.Deployment{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "openshift-adp-controller-manager",
						Namespace: "openshift-adp",
					},
					Status: appsv1.DeploymentStatus{
						ReadyReplicas: 1,
					},
				},
				&appsv1.Deployment{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "velero",
						Namespace: "openshift-adp",
					},
					Status: appsv1.DeploymentStatus{
						ReadyReplicas: 1,
					},
				},
			},
			expectError: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fakeClient := fake.NewClientBuilder().
				WithScheme(scheme).
				WithObjects(tt.objects...).
				Build()

			ctx := context.Background()
			err := ValidateOADPComponents(ctx, fakeClient, tt.namespace)

			if tt.expectError {
				if err == nil {
					t.Errorf("expected error but got none")
				} else if !strings.Contains(err.Error(), tt.errorMsg) {
					t.Errorf("expected error to contain %q, got %q", tt.errorMsg, err.Error())
				}
			} else {
				if err != nil {
					t.Errorf("unexpected error: %v", err)
				}
			}
		})
	}
}

func TestVerifyDPAStatus(t *testing.T) {
	scheme := runtime.NewScheme()

	tests := []struct {
		name        string
		namespace   string
		objects     []client.Object
		expectError bool
		errorMsg    string
	}{
		{
			name:        "No DPA resources found",
			namespace:   "openshift-adp",
			objects:     []client.Object{},
			expectError: true,
			errorMsg:    "no DataProtectionApplication resources found",
		},
		{
			name:      "DPA with Reconciled=True condition",
			namespace: "openshift-adp",
			objects: []client.Object{
				createDPAWithCondition("test-dpa", "openshift-adp", "Reconciled", "True"),
			},
			expectError: false,
		},
		{
			name:      "DPA with Reconciled=False condition",
			namespace: "openshift-adp",
			objects: []client.Object{
				createDPAWithCondition("test-dpa", "openshift-adp", "Reconciled", "False"),
			},
			expectError: true,
			errorMsg:    "no ready DataProtectionApplication found",
		},
		{
			name:      "DPA with different condition type",
			namespace: "openshift-adp",
			objects: []client.Object{
				createDPAWithCondition("test-dpa", "openshift-adp", "Available", "True"),
			},
			expectError: true,
			errorMsg:    "no ready DataProtectionApplication found",
		},
		{
			name:      "Multiple DPAs, one ready",
			namespace: "openshift-adp",
			objects: []client.Object{
				createDPAWithCondition("test-dpa-1", "openshift-adp", "Reconciled", "False"),
				createDPAWithCondition("test-dpa-2", "openshift-adp", "Reconciled", "True"),
			},
			expectError: false,
		},
		{
			name:      "DPA without status",
			namespace: "openshift-adp",
			objects: []client.Object{
				createDPAWithoutStatus("test-dpa", "openshift-adp"),
			},
			expectError: true,
			errorMsg:    "no ready DataProtectionApplication found",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fakeClient := fake.NewClientBuilder().
				WithScheme(scheme).
				WithObjects(tt.objects...).
				Build()

			ctx := context.Background()
			err := VerifyDPAStatus(ctx, fakeClient, tt.namespace)

			if tt.expectError {
				if err == nil {
					t.Errorf("expected error but got none")
				} else if !strings.Contains(err.Error(), tt.errorMsg) {
					t.Errorf("expected error to contain %q, got %q", tt.errorMsg, err.Error())
				}
			} else {
				if err != nil {
					t.Errorf("unexpected error: %v", err)
				}
			}
		})
	}
}

func TestCheckDPAHypershiftPlugin(t *testing.T) {
	scheme := runtime.NewScheme()

	tests := []struct {
		name        string
		namespace   string
		objects     []client.Object
		expectError bool
		errorMsg    string
	}{
		{
			name:        "No DPA resources found",
			namespace:   "openshift-adp",
			objects:     []client.Object{},
			expectError: true,
			errorMsg:    "no DataProtectionApplication resources found",
		},
		{
			name:      "DPA with hypershift plugin",
			namespace: "openshift-adp",
			objects: []client.Object{
				createDPAWithPlugins("test-dpa", "openshift-adp", []string{"openshift", "aws", "hypershift"}),
			},
			expectError: false,
		},
		{
			name:      "DPA without hypershift plugin",
			namespace: "openshift-adp",
			objects: []client.Object{
				createDPAWithPlugins("test-dpa", "openshift-adp", []string{"openshift", "aws"}),
			},
			expectError: true,
			errorMsg:    "HyperShift plugin not found",
		},
		{
			name:      "Multiple DPAs, one with hypershift plugin",
			namespace: "openshift-adp",
			objects: []client.Object{
				createDPAWithPlugins("test-dpa-1", "openshift-adp", []string{"openshift", "aws"}),
				createDPAWithPlugins("test-dpa-2", "openshift-adp", []string{"openshift", "hypershift"}),
			},
			expectError: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fakeClient := fake.NewClientBuilder().
				WithScheme(scheme).
				WithObjects(tt.objects...).
				Build()

			ctx := context.Background()
			err := CheckDPAHypershiftPlugin(ctx, fakeClient, tt.namespace)

			if tt.expectError {
				if err == nil {
					t.Errorf("expected error but got none")
				} else if !strings.Contains(err.Error(), tt.errorMsg) {
					t.Errorf("expected error to contain %q, got %q", tt.errorMsg, err.Error())
				}
			} else {
				if err != nil {
					t.Errorf("unexpected error: %v", err)
				}
			}
		})
	}
}

func TestValidateAndGetHostedClusterPlatform(t *testing.T) {
	scheme := runtime.NewScheme()
	_ = hypershiftv1beta1.AddToScheme(scheme)

	tests := []struct {
		name        string
		hcName      string
		hcNamespace string
		objects     []client.Object
		expected    string
		expectError bool
		errorMsg    string
	}{
		{
			name:        "HostedCluster not found",
			hcName:      "test-cluster",
			hcNamespace: "clusters",
			objects:     []client.Object{},
			expectError: true,
			errorMsg:    "not found",
		},
		{
			name:        "AWS platform",
			hcName:      "test-cluster",
			hcNamespace: "clusters",
			objects: []client.Object{
				createHostedClusterWithPlatform("test-cluster", "clusters", hypershiftv1beta1.AWSPlatform),
			},
			expected:    "AWS",
			expectError: false,
		},
		{
			name:        "Agent platform (lowercase)",
			hcName:      "test-cluster",
			hcNamespace: "clusters",
			objects: []client.Object{
				createHostedClusterWithPlatform("test-cluster", "clusters", hypershiftv1beta1.AgentPlatform),
			},
			expected:    "AGENT",
			expectError: false,
		},
		{
			name:        "KubeVirt platform",
			hcName:      "test-cluster",
			hcNamespace: "clusters",
			objects: []client.Object{
				createHostedClusterWithPlatform("test-cluster", "clusters", hypershiftv1beta1.KubevirtPlatform),
			},
			expected:    "KUBEVIRT",
			expectError: false,
		},
		{
			name:        "HostedCluster without platform",
			hcName:      "test-cluster",
			hcNamespace: "clusters",
			objects: []client.Object{
				createHostedClusterWithoutPlatform("test-cluster", "clusters"),
			},
			expectError: true,
			errorMsg:    "platform type not found",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fakeClient := fake.NewClientBuilder().
				WithScheme(scheme).
				WithObjects(tt.objects...).
				Build()

			ctx := context.Background()
			platform, err := ValidateAndGetHostedClusterPlatform(ctx, fakeClient, tt.hcName, tt.hcNamespace)

			if tt.expectError {
				if err == nil {
					t.Errorf("expected error but got none")
				} else if !strings.Contains(err.Error(), tt.errorMsg) {
					t.Errorf("expected error to contain %q, got %q", tt.errorMsg, err.Error())
				}
			} else {
				if err != nil {
					t.Errorf("unexpected error: %v", err)
				}
				if platform != tt.expected {
					t.Errorf("expected platform %q, got %q", tt.expected, platform)
				}
			}
		})
	}
}

// Helper functions to create test objects

func createDPAWithCondition(name, namespace, conditionType, status string) *unstructured.Unstructured {
	dpa := &unstructured.Unstructured{}
	dpa.SetGroupVersionKind(schema.GroupVersionKind{
		Group:   "oadp.openshift.io",
		Version: "v1alpha1",
		Kind:    "DataProtectionApplication",
	})
	dpa.SetName(name)
	dpa.SetNamespace(namespace)

	conditions := []interface{}{
		map[string]interface{}{
			"type":   conditionType,
			"status": status,
		},
	}

	_ = unstructured.SetNestedSlice(dpa.Object, conditions, "status", "conditions")
	return dpa
}

func createDPAWithoutStatus(name, namespace string) *unstructured.Unstructured {
	dpa := &unstructured.Unstructured{}
	dpa.SetGroupVersionKind(schema.GroupVersionKind{
		Group:   "oadp.openshift.io",
		Version: "v1alpha1",
		Kind:    "DataProtectionApplication",
	})
	dpa.SetName(name)
	dpa.SetNamespace(namespace)
	return dpa
}

func createDPAWithPlugins(name, namespace string, plugins []string) *unstructured.Unstructured {
	dpa := &unstructured.Unstructured{}
	dpa.SetGroupVersionKind(schema.GroupVersionKind{
		Group:   "oadp.openshift.io",
		Version: "v1alpha1",
		Kind:    "DataProtectionApplication",
	})
	dpa.SetName(name)
	dpa.SetNamespace(namespace)

	pluginInterfaces := make([]interface{}, len(plugins))
	for i, plugin := range plugins {
		pluginInterfaces[i] = plugin
	}

	_ = unstructured.SetNestedSlice(dpa.Object, pluginInterfaces, "spec", "configuration", "velero", "defaultPlugins")
	return dpa
}

func createHostedClusterWithPlatform(name, namespace string, platformType hypershiftv1beta1.PlatformType) *hypershiftv1beta1.HostedCluster {
	return &hypershiftv1beta1.HostedCluster{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: namespace,
		},
		Spec: hypershiftv1beta1.HostedClusterSpec{
			Platform: hypershiftv1beta1.PlatformSpec{
				Type: platformType,
			},
		},
	}
}

func createHostedClusterWithoutPlatform(name, namespace string) *hypershiftv1beta1.HostedCluster {
	return &hypershiftv1beta1.HostedCluster{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: namespace,
		},
		Spec: hypershiftv1beta1.HostedClusterSpec{
			// Platform field is empty
		},
	}
}
