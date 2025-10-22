package oadp

import (
	"context"
	"fmt"
	"slices"
	"strings"

	hypershiftv1beta1 "github.com/openshift/hypershift/api/hypershift/v1beta1"

	appsv1 "k8s.io/api/apps/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"

	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// ValidateOADPComponents checks if OADP operator is installed and running
func ValidateOADPComponents(ctx context.Context, c client.Client, namespace string) error {
	log := ctrl.LoggerFrom(ctx)
	// Check if OADP operator deployment exists
	deployment := &appsv1.Deployment{}
	err := c.Get(ctx, types.NamespacedName{
		Name:      "openshift-adp-controller-manager",
		Namespace: namespace,
	}, deployment)
	if err != nil {
		return fmt.Errorf("OADP operator deployment not found in namespace %s: %w", namespace, err)
	}

	// Check if deployment is ready
	if deployment.Status.ReadyReplicas == 0 {
		return fmt.Errorf("OADP operator deployment is not ready in namespace %s", namespace)
	}

	// Check if velero deployment exists and is ready
	veleroDeployment := &appsv1.Deployment{}
	err = c.Get(ctx, types.NamespacedName{
		Name:      "velero",
		Namespace: namespace,
	}, veleroDeployment)
	if err != nil {
		return fmt.Errorf("velero deployment not found in namespace %s: %w", namespace, err)
	}

	if veleroDeployment.Status.ReadyReplicas == 0 {
		return fmt.Errorf("velero deployment is not ready in namespace %s", namespace)
	}

	// Check DPA plugins configuration
	if err := CheckDPAHypershiftPlugin(ctx, c, namespace); err != nil {
		// Log as warning but don't fail validation
		log.Info("Warning: HyperShift plugin validation", "error", err.Error())
	}

	return nil
}

// VerifyDPAStatus checks if DataProtectionApplication CR exists and is ready
// Not vendored the oadp-operator API to save space in the binary
func VerifyDPAStatus(ctx context.Context, c client.Client, namespace string) error {
	// List all DPA resources in the namespace using unstructured
	dpaList := &unstructured.UnstructuredList{}
	dpaList.SetGroupVersionKind(schema.GroupVersionKind{
		Group:   "oadp.openshift.io",
		Version: "v1alpha1",
		Kind:    "DataProtectionApplicationList",
	})

	err := c.List(ctx, dpaList, client.InNamespace(namespace))
	if err != nil {
		return fmt.Errorf("failed to list DataProtectionApplication resources in namespace %s: %w", namespace, err)
	}

	if len(dpaList.Items) == 0 {
		return fmt.Errorf("no DataProtectionApplication resources found in namespace %s", namespace)
	}

	// Check if at least one DPA is in ready state
	for _, dpa := range dpaList.Items {
		// Navigate directly to status.conditions
		conditions, found, err := unstructured.NestedSlice(dpa.Object, "status", "conditions")
		if err != nil || !found {
			continue
		}

		// Look for Reconciled condition with True status
		for _, conditionInterface := range conditions {
			condition, ok := conditionInterface.(map[string]interface{})
			if !ok {
				continue
			}

			// Check if this is the Reconciled condition with True status
			if condType, _ := condition["type"].(string); condType == "Reconciled" {
				if condStatus, _ := condition["status"].(string); condStatus == string(metav1.ConditionTrue) {
					return nil // Found a ready DPA
				}
			}
		}
	}

	return fmt.Errorf("no ready DataProtectionApplication found in namespace %s", namespace)
}

// ValidateAndGetHostedClusterPlatform validates that the HostedCluster exists and returns its platform
func ValidateAndGetHostedClusterPlatform(ctx context.Context, c client.Client, hcName, hcNamespace string) (string, error) {
	// Get the HostedCluster resource using typed API
	hostedCluster := &hypershiftv1beta1.HostedCluster{}

	err := c.Get(ctx, types.NamespacedName{
		Name:      hcName,
		Namespace: hcNamespace,
	}, hostedCluster)
	if err != nil {
		return "", fmt.Errorf("HostedCluster '%s' not found in namespace '%s': %w", hcName, hcNamespace, err)
	}

	// Extract the platform from the spec
	platformSpec := hostedCluster.Spec.Platform
	if platformSpec.Type == "" {
		return "", fmt.Errorf("platform type not found in HostedCluster '%s' spec", hcName)
	}

	// Normalize platform name to uppercase for consistency
	return strings.ToUpper(string(platformSpec.Type)), nil
}

// CheckDPAHypershiftPlugin checks if the hypershift plugin is configured in DataProtectionApplication resources
func CheckDPAHypershiftPlugin(ctx context.Context, c client.Client, namespace string) error {
	log := ctrl.LoggerFrom(ctx)
	// List all DPA resources in the namespace using unstructured
	dpaList := &unstructured.UnstructuredList{}
	dpaList.SetGroupVersionKind(schema.GroupVersionKind{
		Group:   "oadp.openshift.io",
		Version: "v1alpha1",
		Kind:    "DataProtectionApplicationList",
	})

	err := c.List(ctx, dpaList, client.InNamespace(namespace))
	if err != nil {
		return fmt.Errorf("failed to list DataProtectionApplication resources: %w", err)
	}

	if len(dpaList.Items) == 0 {
		return fmt.Errorf("no DataProtectionApplication resources found")
	}

	// Check each DPA for hypershift plugin
	hypershiftPluginFound := false
	for _, dpa := range dpaList.Items {
		// Navigate to spec.configuration.velero.defaultPlugins
		plugins, found, err := unstructured.NestedStringSlice(dpa.Object, "spec", "configuration", "velero", "defaultPlugins")
		if err != nil {
			log.Info("Warning: Failed to read defaultPlugins from DPA", "dpa", dpa.GetName(), "error", err.Error())
			continue
		}
		if !found {
			log.Info("Warning: No defaultPlugins found in DPA configuration", "dpa", dpa.GetName())
			continue
		}

		// Check if hypershift plugin is in the list
		if slices.Contains(plugins, "hypershift") {
			hypershiftPluginFound = true
			log.Info("HyperShift plugin found in DPA", "dpa", dpa.GetName())
			break
		}
	}

	if !hypershiftPluginFound {
		return fmt.Errorf("HyperShift plugin not found in any DataProtectionApplication. Please add 'hypershift' to the defaultPlugins list in your DPA configuration")
	}

	return nil
}
