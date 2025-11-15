package backup

// This file contains comprehensive tests for all validation functions used in the backup creation process.
// These tests ensure that OADP components, DataProtectionApplications, and HostedClusters are properly
// validated before attempting to create backups. The tests use fake Kubernetes clients to simulate
// various cluster states and error conditions.

import (
	"context"
	"strings"
	"testing"

	hypershiftv1beta1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	"github.com/openshift/hypershift/support/oadp"

	appsv1 "k8s.io/api/apps/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"

	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

// TestVerifyDPAStatus tests the verifyDPAStatus function which checks if DataProtectionApplication
// resources exist and are in a ready state (Reconciled condition = True).
// This test validates the optimized implementation that directly navigates to status.conditions
// and efficiently searches for the Reconciled condition.
func TestVerifyDPAStatus(t *testing.T) {
	scheme := runtime.NewScheme()

	tests := []struct {
		name      string
		namespace string
		objects   []client.Object
		wantErr   bool
		errMsg    string
	}{
		{
			name:      "No DPA resources found",
			namespace: "test-namespace",
			objects:   []client.Object{},
			wantErr:   true,
			errMsg:    "no DataProtectionApplication resources found",
		},
		{
			name:      "DPA with Reconciled=True condition",
			namespace: "test-namespace",
			objects: []client.Object{
				createDPAWithCondition("test-dpa", "test-namespace", "Reconciled", "True"),
			},
			wantErr: false,
		},
		{
			name:      "DPA with Reconciled=False condition",
			namespace: "test-namespace",
			objects: []client.Object{
				createDPAWithCondition("test-dpa", "test-namespace", "Reconciled", "False"),
			},
			wantErr: true,
			errMsg:  "no ready DataProtectionApplication found",
		},
		{
			name:      "DPA with different condition type",
			namespace: "test-namespace",
			objects: []client.Object{
				createDPAWithCondition("test-dpa", "test-namespace", "Available", "True"),
			},
			wantErr: true,
			errMsg:  "no ready DataProtectionApplication found",
		},
		{
			name:      "Multiple DPAs, one ready",
			namespace: "test-namespace",
			objects: []client.Object{
				createDPAWithCondition("test-dpa-1", "test-namespace", "Reconciled", "False"),
				createDPAWithCondition("test-dpa-2", "test-namespace", "Reconciled", "True"),
			},
			wantErr: false,
		},
		{
			name:      "DPA without status",
			namespace: "test-namespace",
			objects: []client.Object{
				createDPAWithoutStatus("test-dpa", "test-namespace"),
			},
			wantErr: true,
			errMsg:  "no ready DataProtectionApplication found",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fakeClient := fake.NewClientBuilder().
				WithScheme(scheme).
				WithObjects(tt.objects...).
				Build()

			ctx := ctrl.LoggerInto(context.Background(), ctrl.Log)
			err := oadp.VerifyDPAStatus(ctx, fakeClient, tt.namespace)

			if tt.wantErr {
				if err == nil {
					t.Errorf("verifyDPAStatus() expected error but got none")
					return
				}
				if tt.errMsg != "" && !containsString(err.Error(), tt.errMsg) {
					t.Errorf("verifyDPAStatus() error = %v, wantErr to contain %v", err.Error(), tt.errMsg)
				}
			} else {
				if err != nil {
					t.Errorf("verifyDPAStatus() unexpected error = %v", err)
				}
			}
		})
	}
}

// TestValidateOADPComponents tests the validateOADPComponents function which verifies that
// OADP operator and Velero deployments are installed and ready in the cluster.
// This test covers various failure scenarios including missing deployments and
// deployments that are not ready (ReadyReplicas = 0).
func TestValidateOADPComponents(t *testing.T) {
	scheme := runtime.NewScheme()
	_ = appsv1.AddToScheme(scheme)

	tests := []struct {
		name      string
		namespace string
		objects   []client.Object
		wantErr   bool
		errMsg    string
	}{
		{
			name:      "OADP operator deployment not found",
			namespace: "openshift-adp",
			objects:   []client.Object{},
			wantErr:   true,
			errMsg:    "OADP operator deployment not found",
		},
		{
			name:      "OADP operator deployment not ready",
			namespace: "openshift-adp",
			objects: []client.Object{
				createDeployment("openshift-adp-controller-manager", "openshift-adp", 0, 1),
			},
			wantErr: true,
			errMsg:  "OADP operator deployment is not ready",
		},
		{
			name:      "Velero deployment not found",
			namespace: "openshift-adp",
			objects: []client.Object{
				createDeployment("openshift-adp-controller-manager", "openshift-adp", 1, 1),
			},
			wantErr: true,
			errMsg:  "velero deployment not found in namespace openshift-adp",
		},
		{
			name:      "Velero deployment not ready",
			namespace: "openshift-adp",
			objects: []client.Object{
				createDeployment("openshift-adp-controller-manager", "openshift-adp", 1, 1),
				createDeployment("velero", "openshift-adp", 0, 1),
			},
			wantErr: true,
			errMsg:  "velero deployment is not ready in namespace openshift-adp",
		},
		{
			name:      "All deployments ready",
			namespace: "openshift-adp",
			objects: []client.Object{
				createDeployment("openshift-adp-controller-manager", "openshift-adp", 1, 1),
				createDeployment("velero", "openshift-adp", 1, 1),
				createDPAWithHypershiftPlugin("test-dpa", "openshift-adp"),
			},
			wantErr: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fakeClient := fake.NewClientBuilder().
				WithScheme(scheme).
				WithObjects(tt.objects...).
				Build()

			ctx := ctrl.LoggerInto(context.Background(), ctrl.Log)
			err := oadp.ValidateOADPComponents(ctx, fakeClient, tt.namespace)

			if tt.wantErr {
				if err == nil {
					t.Errorf("validateOADPComponents() expected error but got none")
					return
				}
				if tt.errMsg != "" && !containsString(err.Error(), tt.errMsg) {
					t.Errorf("validateOADPComponents() error = %v, wantErr to contain %v", err.Error(), tt.errMsg)
				}
			} else {
				if err != nil {
					t.Errorf("validateOADPComponents() unexpected error = %v", err)
				}
			}
		})
	}
}

// TestCheckDPAHypershiftPlugin tests the checkDPAHypershiftPlugin function which validates
// that the 'hypershift' plugin is included in the DataProtectionApplication's defaultPlugins list.
// This test ensures that the HyperShift-specific plugin is properly configured for backing up
// HyperShift resources, which is critical for complete backup functionality.
func TestCheckDPAHypershiftPlugin(t *testing.T) {
	scheme := runtime.NewScheme()

	tests := []struct {
		name      string
		namespace string
		objects   []client.Object
		wantErr   bool
		errMsg    string
	}{
		{
			name:      "No DPA resources found",
			namespace: "test-namespace",
			objects:   []client.Object{},
			wantErr:   true,
			errMsg:    "no DataProtectionApplication resources found",
		},
		{
			name:      "DPA with hypershift plugin",
			namespace: "test-namespace",
			objects: []client.Object{
				createDPAWithHypershiftPlugin("test-dpa", "test-namespace"),
			},
			wantErr: false,
		},
		{
			name:      "DPA without hypershift plugin",
			namespace: "test-namespace",
			objects: []client.Object{
				createDPAWithoutHypershiftPlugin("test-dpa", "test-namespace"),
			},
			wantErr: true,
			errMsg:  "HyperShift plugin not found",
		},
		{
			name:      "Multiple DPAs, one with hypershift plugin",
			namespace: "test-namespace",
			objects: []client.Object{
				createDPAWithoutHypershiftPlugin("test-dpa-1", "test-namespace"),
				createDPAWithHypershiftPlugin("test-dpa-2", "test-namespace"),
			},
			wantErr: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fakeClient := fake.NewClientBuilder().
				WithScheme(scheme).
				WithObjects(tt.objects...).
				Build()

			ctx := ctrl.LoggerInto(context.Background(), ctrl.Log)
			err := oadp.CheckDPAHypershiftPlugin(ctx, fakeClient, tt.namespace)

			if tt.wantErr {
				if err == nil {
					t.Errorf("checkDPAHypershiftPlugin() expected error but got none")
					return
				}
				if tt.errMsg != "" && !containsString(err.Error(), tt.errMsg) {
					t.Errorf("checkDPAHypershiftPlugin() error = %v, wantErr to contain %v", err.Error(), tt.errMsg)
				}
			} else {
				if err != nil {
					t.Errorf("checkDPAHypershiftPlugin() unexpected error = %v", err)
				}
			}
		})
	}
}

// TestValidateAndGetHostedClusterPlatform tests the validateAndGetHostedClusterPlatform function
// which retrieves and validates the platform type from a HostedCluster resource.
// This test verifies:
// - HostedCluster existence validation
// - Platform type extraction from spec.platform.type
// - Platform name normalization to uppercase
// - Error handling for missing HostedClusters or missing platform information
func TestValidateAndGetHostedClusterPlatform(t *testing.T) {
	scheme := runtime.NewScheme()
	_ = hypershiftv1beta1.AddToScheme(scheme)

	tests := []struct {
		name        string
		hcName      string
		hcNamespace string
		objects     []client.Object
		want        string
		wantErr     bool
		errMsg      string
	}{
		{
			name:        "HostedCluster not found",
			hcName:      "test-cluster",
			hcNamespace: "test-namespace",
			objects:     []client.Object{},
			wantErr:     true,
			errMsg:      "HostedCluster 'test-cluster' not found",
		},
		{
			name:        "AWS platform",
			hcName:      "test-cluster",
			hcNamespace: "test-namespace",
			objects: []client.Object{
				createHostedClusterWithPlatform("test-cluster", "test-namespace", "AWS"),
			},
			want:    "AWS",
			wantErr: false,
		},
		{
			name:        "Agent platform (lowercase)",
			hcName:      "test-cluster",
			hcNamespace: "test-namespace",
			objects: []client.Object{
				createHostedClusterWithPlatform("test-cluster", "test-namespace", "agent"),
			},
			want:    "AGENT",
			wantErr: false,
		},
		{
			name:        "KubeVirt platform",
			hcName:      "test-cluster",
			hcNamespace: "test-namespace",
			objects: []client.Object{
				createHostedClusterWithPlatform("test-cluster", "test-namespace", "KubeVirt"),
			},
			want:    "KUBEVIRT",
			wantErr: false,
		},
		{
			name:        "HostedCluster without platform",
			hcName:      "test-cluster",
			hcNamespace: "test-namespace",
			objects: []client.Object{
				createHostedClusterWithoutPlatform("test-cluster", "test-namespace"),
			},
			wantErr: true,
			errMsg:  "platform type not found",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fakeClient := fake.NewClientBuilder().
				WithScheme(scheme).
				WithObjects(tt.objects...).
				Build()

			ctx := context.Background()
			got, err := oadp.ValidateAndGetHostedClusterPlatform(ctx, fakeClient, tt.hcName, tt.hcNamespace)

			if tt.wantErr {
				if err == nil {
					t.Errorf("validateAndGetHostedClusterPlatform() expected error but got none")
					return
				}
				if tt.errMsg != "" && !containsString(err.Error(), tt.errMsg) {
					t.Errorf("validateAndGetHostedClusterPlatform() error = %v, wantErr to contain %v", err.Error(), tt.errMsg)
				}
			} else {
				if err != nil {
					t.Errorf("validateAndGetHostedClusterPlatform() unexpected error = %v", err)
					return
				}
				if got != tt.want {
					t.Errorf("validateAndGetHostedClusterPlatform() = %v, want %v", got, tt.want)
				}
			}
		})
	}
}

// Helper functions to create test objects for mocking Kubernetes resources

// createDPAWithCondition creates a mock DataProtectionApplication with a specific condition
func createDPAWithCondition(name, namespace, conditionType, conditionStatus string) *unstructured.Unstructured {
	dpa := &unstructured.Unstructured{}
	dpa.SetGroupVersionKind(schema.GroupVersionKind{
		Group:   "oadp.openshift.io",
		Version: "v1alpha1",
		Kind:    "DataProtectionApplication",
	})
	dpa.SetName(name)
	dpa.SetNamespace(namespace)

	status := map[string]interface{}{
		"conditions": []interface{}{
			map[string]interface{}{
				"type":   conditionType,
				"status": conditionStatus,
			},
		},
	}
	dpa.Object["status"] = status

	return dpa
}

// createDPAWithoutStatus creates a mock DataProtectionApplication without status field
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

// createDPAWithHypershiftPlugin creates a mock DataProtectionApplication with hypershift plugin configured
func createDPAWithHypershiftPlugin(name, namespace string) *unstructured.Unstructured {
	dpa := &unstructured.Unstructured{}
	dpa.SetGroupVersionKind(schema.GroupVersionKind{
		Group:   "oadp.openshift.io",
		Version: "v1alpha1",
		Kind:    "DataProtectionApplication",
	})
	dpa.SetName(name)
	dpa.SetNamespace(namespace)

	spec := map[string]interface{}{
		"configuration": map[string]interface{}{
			"velero": map[string]interface{}{
				"defaultPlugins": []interface{}{
					"openshift",
					"aws",
					"csi",
					"hypershift",
				},
			},
		},
	}
	dpa.Object["spec"] = spec

	return dpa
}

// createDPAWithoutHypershiftPlugin creates a mock DataProtectionApplication without hypershift plugin
func createDPAWithoutHypershiftPlugin(name, namespace string) *unstructured.Unstructured {
	dpa := &unstructured.Unstructured{}
	dpa.SetGroupVersionKind(schema.GroupVersionKind{
		Group:   "oadp.openshift.io",
		Version: "v1alpha1",
		Kind:    "DataProtectionApplication",
	})
	dpa.SetName(name)
	dpa.SetNamespace(namespace)

	spec := map[string]interface{}{
		"configuration": map[string]interface{}{
			"velero": map[string]interface{}{
				"defaultPlugins": []interface{}{
					"openshift",
					"aws",
					"csi",
				},
			},
		},
	}
	dpa.Object["spec"] = spec

	return dpa
}

// createHostedClusterWithPlatform creates a mock HostedCluster with a specific platform type
func createHostedClusterWithPlatform(name, namespace, platform string) *hypershiftv1beta1.HostedCluster {
	hc := &hypershiftv1beta1.HostedCluster{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: namespace,
		},
		Spec: hypershiftv1beta1.HostedClusterSpec{
			Platform: hypershiftv1beta1.PlatformSpec{
				Type: hypershiftv1beta1.PlatformType(platform),
			},
		},
	}

	return hc
}

// createHostedClusterWithoutPlatform creates a mock HostedCluster without platform information
func createHostedClusterWithoutPlatform(name, namespace string) *hypershiftv1beta1.HostedCluster {
	hc := &hypershiftv1beta1.HostedCluster{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: namespace,
		},
		Spec: hypershiftv1beta1.HostedClusterSpec{
			Platform: hypershiftv1beta1.PlatformSpec{
				Type: "", // Empty platform type
			},
		},
	}

	return hc
}

// createDeployment creates a mock Kubernetes Deployment with specified replica counts
func createDeployment(name, namespace string, readyReplicas, replicas int32) *appsv1.Deployment {
	return &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: namespace,
		},
		Spec: appsv1.DeploymentSpec{
			Replicas: &replicas,
		},
		Status: appsv1.DeploymentStatus{
			ReadyReplicas: readyReplicas,
		},
	}
}

// containsString is a simple helper function that checks if haystack contains needle
func containsString(haystack, needle string) bool {
	return strings.Contains(haystack, needle)
}
