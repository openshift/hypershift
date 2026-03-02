//go:build e2ev2 && backuprestore

package backuprestore

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/go-logr/logr"
	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	"github.com/openshift/hypershift/test/e2e/util"
	"github.com/openshift/hypershift/test/e2e/v2/internal"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/client-go/discovery"
	crclient "sigs.k8s.io/controller-runtime/pkg/client"
)

const (
	// DeletionTimeout is the default timeout for waiting for deletion
	DeletionTimeout      = 10 * time.Minute
	DeletionRetryTimeout = 5 * time.Minute
	// PollInterval is the interval for polling deletion status
	PollInterval = 5 * time.Second
)

// BreakHostedClusterPreservingMachines simulates a catastrophic failure while preserving
// machine resources. This preserves orphaned resources (like machines) by removing
// the CAPI controller early. As a result, the cloud resources (like AWS instances)
// will not be deleted.
// 1. Force delete HCP namespace (removes CAPI controller early, orphaning machines)
// 2. Remove finalizers from HostedControlPlane
// 3. Remove finalizers from orphaned resources in HCP namespace
// 4. Delete HostedCluster namespace
// 5. Remove finalizers from orphaned resources in HostedCluster namespace
// 6. Wait for control plane namespace to be fully deleted
func BreakHostedClusterPreservingMachines(testCtx *internal.TestContext, logger logr.Logger) error {
	// Step 1: Force delete HCP namespace (without waiting for completion)
	// This removes the CAPI controller early, leaving machine resources orphaned
	if err := deleteControlPlaneNamespace(testCtx, false); err != nil {
		return fmt.Errorf("failed to delete control plane namespace: %w", err)
	}

	// Step 2: Remove finalizers from HostedControlPlane
	if err := removeHCPFinalizers(testCtx, testCtx.ClusterName, testCtx.ControlPlaneNamespace); err != nil {
		return fmt.Errorf("failed to remove HCP finalizers: %w", err)
	}

	// Step 3: Remove finalizers from orphaned resources in HCP namespace
	// This allows the namespace to be fully cleaned up without waiting for controllers
	if err := removeNamespaceObjectFinalizers(testCtx, testCtx.ControlPlaneNamespace, logger); err != nil {
		return fmt.Errorf("failed to remove orphaned resource finalizers: %w", err)
	}

	// Step 4: Delete HostedCluster namespace
	if err := deleteHostedClusterNamespace(testCtx, false); err != nil {
		return fmt.Errorf("failed to delete hosted cluster namespace: %w", err)
	}

	// Step 5: Remove finalizers from orphaned resources in HostedCluster namespace
	if err := removeNamespaceObjectFinalizers(testCtx, testCtx.ClusterNamespace, logger); err != nil {
		return fmt.Errorf("failed to remove orphaned resource finalizers: %w", err)
	}

	// Step 6: Wait for control plane namespace to be fully deleted
	if err := deleteControlPlaneNamespace(testCtx, true); err != nil {
		return fmt.Errorf("failed to delete control plane namespace: %w", err)
	}

	return nil
}

// removeHCPFinalizers removes all finalizers from the HostedControlPlane to force deletion.
func removeHCPFinalizers(testCtx *internal.TestContext, hcpName, hcpNamespace string) error {
	return wait.PollUntilContextTimeout(testCtx.Context, 1*time.Second, 30*time.Second, true,
		func(ctx context.Context) (bool, error) {
			hcp := &hyperv1.HostedControlPlane{}
			err := testCtx.MgmtClient.Get(ctx, types.NamespacedName{
				Namespace: hcpNamespace,
				Name:      hcpName,
			}, hcp)

			if err != nil {
				if apierrors.IsNotFound(err) {
					return true, nil
				}
				return false, err
			}

			if len(hcp.Finalizers) == 0 {
				return true, nil
			}

			// Remove all finalizers
			hcp.Finalizers = []string{}
			if err := testCtx.MgmtClient.Update(ctx, hcp); err != nil {
				if apierrors.IsConflict(err) {
					return false, nil // Retry on conflict
				}
				return false, err
			}

			return true, nil
		})
}

// removeNamespaceObjectFinalizers removes finalizers from all objects in the specified namespace.
// This uses the discovery API to find all resource types and removes finalizers from each.
func removeNamespaceObjectFinalizers(testCtx *internal.TestContext, namespace string, logger logr.Logger) error {
	ctx := testCtx.Context

	// Check if namespace still exists
	nsObj := &corev1.Namespace{}
	err := testCtx.MgmtClient.Get(ctx, crclient.ObjectKey{Name: namespace}, nsObj)
	if err != nil {
		if apierrors.IsNotFound(err) {
			// Namespace already deleted, nothing to do
			return nil
		}
		return fmt.Errorf("failed to get namespace %s: %w", namespace, err)
	}

	restConfig, err := util.GetConfig()
	if err != nil {
		return fmt.Errorf("failed to get REST config: %w", err)
	}

	// Create discovery client
	discoveryClient, err := discovery.NewDiscoveryClientForConfig(restConfig)
	if err != nil {
		return fmt.Errorf("failed to create discovery client: %w", err)
	}

	// Get all API resources
	apiResourceLists, err := discoveryClient.ServerPreferredNamespacedResources()
	if err != nil && !discovery.IsGroupDiscoveryFailedError(err) {
		return fmt.Errorf("failed to get API resources: %w", err)
	}

	// Iterate through all namespaced resource types
	for _, apiResourceList := range apiResourceLists {
		gv, err := schema.ParseGroupVersion(apiResourceList.GroupVersion)
		if err != nil {
			continue
		}

		for _, apiResource := range apiResourceList.APIResources {
			// Skip non-namespaced resources and subresources
			if !apiResource.Namespaced || strings.Contains(apiResource.Name, "/") {
				continue
			}

			// Skip if the resource doesn't support list, update or get
			if !supportsVerb(apiResource.Verbs, "list") ||
				!supportsVerb(apiResource.Verbs, "update") ||
				!supportsVerb(apiResource.Verbs, "get") {
				continue
			}

			// Use GVK with the correct Kind from discovery API
			gvk := schema.GroupVersionKind{
				Group:   gv.Group,
				Version: gv.Version,
				Kind:    apiResource.Kind, // Use actual Kind from discovery (e.g., "AWSMachine" not "awsmachines")
			}

			// List and remove finalizers for this resource type
			if err := removeFinalizersForResourceType(testCtx, namespace, gvk, logger); err != nil {
				// Log but don't fail on individual resource type errors
				logger.Info("Warning: failed to remove finalizers for resource type", "gvk", gvk.String(), "error", err)
			}
		}
	}

	return nil
}

// removeFinalizersForResourceType removes finalizers from all resources of a specific type in the namespace
func removeFinalizersForResourceType(testCtx *internal.TestContext, namespace string, gvk schema.GroupVersionKind, logger logr.Logger) error {
	ctx := testCtx.Context

	// Create an unstructured list for this resource type
	list := &unstructured.UnstructuredList{}
	// Use the list kind (singular kind + "List") for listing resources
	listGVK := schema.GroupVersionKind{
		Group:   gvk.Group,
		Version: gvk.Version,
		Kind:    gvk.Kind + "List",
	}
	list.SetGroupVersionKind(listGVK)

	// List all resources of this type in the namespace
	if err := testCtx.MgmtClient.List(ctx, list, crclient.InNamespace(namespace)); err != nil {
		if apierrors.IsNotFound(err) || apierrors.IsMethodNotSupported(err) {
			// Resource type not found or not supported, skip
			return nil
		}
		return err
	}

	// Remove finalizers from each object
	for i := range list.Items {
		if err := removeObjectFinalizers(testCtx, &list.Items[i]); err != nil {
			// Continue on errors for individual objects
			logger.Info("Warning: failed to remove finalizers from object",
				"kind", list.Items[i].GetKind(),
				"name", list.Items[i].GetName(),
				"error", err)
		}
	}

	return nil
}

// supportsVerb checks if a resource supports a specific verb.
func supportsVerb(verbs metav1.Verbs, verb string) bool {
	for _, v := range verbs {
		if v == verb {
			return true
		}
		if v == "*" {
			return true
		}
	}
	return false
}

// removeObjectFinalizers removes all finalizers from a given object.
// Uses retry logic to handle resource version conflicts.
func removeObjectFinalizers(testCtx *internal.TestContext, obj crclient.Object) error {
	// Skip if object has no finalizers
	if len(obj.GetFinalizers()) == 0 {
		return nil
	}

	return wait.PollUntilContextTimeout(testCtx.Context, 1*time.Second, 30*time.Second, true,
		func(ctx context.Context) (bool, error) {
			// Get the latest version of the object
			key := crclient.ObjectKeyFromObject(obj)
			if err := testCtx.MgmtClient.Get(ctx, key, obj); err != nil {
				if apierrors.IsNotFound(err) {
					// Object already deleted
					return true, nil
				}
				return false, err
			}

			// Check if finalizers are already removed
			if len(obj.GetFinalizers()) == 0 {
				return true, nil
			}

			// Remove all finalizers
			obj.SetFinalizers([]string{})
			if err := testCtx.MgmtClient.Update(ctx, obj); err != nil {
				if apierrors.IsConflict(err) {
					// Retry on conflict
					return false, nil
				}
				if apierrors.IsNotFound(err) {
					// Object was deleted during update
					return true, nil
				}
				return false, err
			}

			return true, nil
		})
}

// deleteNamespace deletes a namespace with optional grace period and wait.
// Set gracePeriodSeconds to 0 for immediate termination.
// Set waitForDeletion to true if you want to wait for the namespace to be fully removed.
func deleteNamespace(testCtx *internal.TestContext, namespace string, gracePeriodSeconds int64, waitForDeletion bool) error {
	if namespace == "" {
		return fmt.Errorf("namespace is not set")
	}

	// Get the namespace first
	nsObj := &corev1.Namespace{}
	err := testCtx.MgmtClient.Get(testCtx.Context, crclient.ObjectKey{Name: namespace}, nsObj)
	if err != nil {
		if apierrors.IsNotFound(err) {
			return nil // Already deleted
		}
		return fmt.Errorf("failed to get namespace %s: %w", namespace, err)
	}

	// Delete the namespace
	deleteOpts := []crclient.DeleteOption{}
	if gracePeriodSeconds >= 0 {
		deleteOpts = append(deleteOpts, crclient.GracePeriodSeconds(gracePeriodSeconds))
	}

	err = testCtx.MgmtClient.Delete(testCtx.Context, nsObj, deleteOpts...)
	if err != nil && !apierrors.IsNotFound(err) {
		return fmt.Errorf("failed to delete namespace %s: %w", namespace, err)
	}

	if !waitForDeletion {
		return nil
	}

	// Wait for namespace deletion
	return wait.PollUntilContextTimeout(testCtx.Context, PollInterval, DeletionTimeout, true, func(ctx context.Context) (bool, error) {
		err := testCtx.MgmtClient.Get(ctx, crclient.ObjectKey{Name: namespace}, nsObj)
		if err != nil {
			if apierrors.IsNotFound(err) {
				return true, nil
			}
			if apierrors.IsTooManyRequests(err) || apierrors.IsServerTimeout(err) || apierrors.IsTimeout(err) {
				return false, nil
			}
			return false, fmt.Errorf("unexpected error checking namespace deletion: %w", err)
		}
		return false, nil
	})
}

// deleteControlPlaneNamespace deletes the HCP namespace.
// Set waitForDeletion to true if you want to wait for the namespace to be fully removed.
func deleteControlPlaneNamespace(testCtx *internal.TestContext, waitForDeletion bool) error {
	namespace := testCtx.ControlPlaneNamespace
	if namespace == "" {
		return fmt.Errorf("control plane namespace is not set in test context")
	}
	return deleteNamespace(testCtx, namespace, 0, waitForDeletion)
}

// deleteHostedClusterNamespace deletes the HostedCluster namespace.
// Set waitForDeletion to true if you want to wait for the namespace to be fully removed.
func deleteHostedClusterNamespace(testCtx *internal.TestContext, waitForDeletion bool) error {
	namespace := testCtx.ClusterNamespace
	if namespace == "" {
		return fmt.Errorf("cluster namespace is not set in test context")
	}
	return deleteNamespace(testCtx, namespace, -1, waitForDeletion)
}
