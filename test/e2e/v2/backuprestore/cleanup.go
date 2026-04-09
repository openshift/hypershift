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
	DeletionTimeout = 10 * time.Minute
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
// 7. Wait for hosted cluster namespace to be fully deleted
func BreakHostedClusterPreservingMachines(testCtx *internal.TestContext, logger logr.Logger) error {
	// Agent platform: prepare resources to prevent agent unbinding and node reboots
	if testCtx.GetHostedCluster().Spec.Platform.Type == hyperv1.AgentPlatform {
		logger.Info("Preparing Agent platform resources before break")
		if err := prepareAgentPlatformForBreak(testCtx, logger); err != nil {
			return fmt.Errorf("failed to prepare Agent platform for break: %w", err)
		}
	}

	// Step 1: Force delete HCP namespace (without waiting for completion)
	// This removes the CAPI controller early, leaving machine resources orphaned
	if err := deleteControlPlaneNamespace(testCtx, 0); err != nil {
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

	// Step 4: Delete HostedCluster namespace (without waiting for completion)
	if err := deleteHostedClusterNamespace(testCtx, 0); err != nil {
		return fmt.Errorf("failed to delete hosted cluster namespace: %w", err)
	}

	// Step 5: Remove finalizers from orphaned resources in HostedCluster namespace
	if err := removeNamespaceObjectFinalizers(testCtx, testCtx.ClusterNamespace, logger); err != nil {
		return fmt.Errorf("failed to remove orphaned resource finalizers: %w", err)
	}

	// Step 6: Wait for control plane namespace to be fully deleted.
	// First attempt with a short timeout. If it doesn't delete within one minute,
	// some resources may have been recreated with new finalizers by controllers,
	// so we strip finalizers again and retry with the standard timeout.
	if err := deleteControlPlaneNamespace(testCtx, 1*time.Minute); err != nil {
		if !wait.Interrupted(err) {
			return fmt.Errorf("failed to delete control plane namespace after initial timeout: %w", err)
		}
		logger.Info("Control plane namespace did not delete within initial timeout, removing finalizers again and retrying")
		if err := removeNamespaceObjectFinalizers(testCtx, testCtx.ControlPlaneNamespace, logger); err != nil {
			return fmt.Errorf("failed to remove finalizers on retry: %w", err)
		}
		if err := deleteControlPlaneNamespace(testCtx, DeletionTimeout); err != nil {
			return fmt.Errorf("failed to delete control plane namespace after retry: %w", err)
		}
	}

	// Step 7: Delete hosted cluster namespace
	if err := deleteHostedClusterNamespace(testCtx, 1*time.Minute); err != nil {
		if !wait.Interrupted(err) {
			return fmt.Errorf("failed to delete hosted cluster namespace after initial timeout: %w", err)
		}
		logger.Info("Hosted cluster namespace did not delete within initial timeout, removing finalizers again and retrying")
		if err := removeNamespaceObjectFinalizers(testCtx, testCtx.ClusterNamespace, logger); err != nil {
			return fmt.Errorf("failed to remove finalizers on retry: %w", err)
		}
		if err := deleteHostedClusterNamespace(testCtx, DeletionTimeout); err != nil {
			return fmt.Errorf("failed to delete hosted cluster namespace after retry: %w", err)
		}
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
// If timeout is 0, the function returns immediately after issuing the delete.
// Otherwise, it waits up to timeout for the namespace to be fully removed.
func deleteNamespace(testCtx *internal.TestContext, namespace string, gracePeriodSeconds int64, timeout time.Duration) error {
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

	if timeout == 0 {
		return nil
	}

	// Wait for namespace deletion
	return wait.PollUntilContextTimeout(testCtx.Context, PollInterval, timeout, true, func(ctx context.Context) (bool, error) {
		err := testCtx.MgmtClient.Get(ctx, crclient.ObjectKey{Name: namespace}, nsObj)
		if err != nil {
			if apierrors.IsNotFound(err) {
				return true, nil
			}
			if apierrors.IsTooManyRequests(err) || apierrors.IsServerTimeout(err) || apierrors.IsTimeout(err) {
				return false, nil
			}
			// Client-side rate limiter returns this when the context deadline
			// is too close. Treat it as transient so the poll loop can time out
			// naturally and be caught by wait.Interrupted.
			if strings.Contains(err.Error(), "would exceed context deadline") {
				return false, nil
			}
			return false, fmt.Errorf("unexpected error checking namespace deletion: %w", err)
		}
		return false, nil
	})
}

// PauseAgentCAPIResources pauses AgentMachine and AgentCluster CRs by adding the
// cluster.x-k8s.io/paused annotation. This prevents the CAPI provider from reconciling
// these resources during backup operations.
func PauseAgentCAPIResources(testCtx *internal.TestContext, logger logr.Logger) error {
	ns := testCtx.ControlPlaneNamespace
	logger.Info("Pausing Agent CAPI resources", "namespace", ns)

	agentMachineGVK := schema.GroupVersionKind{
		Group:   "capi-provider.agent-install.openshift.io",
		Version: "v1beta1",
		Kind:    "AgentMachine",
	}
	if err := setAnnotationOnResources(testCtx, ns, agentMachineGVK, "cluster.x-k8s.io/paused", "true", logger); err != nil {
		return fmt.Errorf("failed to pause AgentMachine resources: %w", err)
	}

	agentClusterGVK := schema.GroupVersionKind{
		Group:   "capi-provider.agent-install.openshift.io",
		Version: "v1beta1",
		Kind:    "AgentCluster",
	}
	if err := setAnnotationOnResources(testCtx, ns, agentClusterGVK, "cluster.x-k8s.io/paused", "true", logger); err != nil {
		return fmt.Errorf("failed to pause AgentCluster resources: %w", err)
	}

	return nil
}

// UnpauseAgentCAPIResources removes the cluster.x-k8s.io/paused annotation from
// AgentMachine and AgentCluster CRs, allowing the CAPI provider to resume reconciliation.
func UnpauseAgentCAPIResources(testCtx *internal.TestContext, logger logr.Logger) error {
	ns := testCtx.ControlPlaneNamespace
	logger.Info("Unpausing Agent CAPI resources", "namespace", ns)

	agentMachineGVK := schema.GroupVersionKind{
		Group:   "capi-provider.agent-install.openshift.io",
		Version: "v1beta1",
		Kind:    "AgentMachine",
	}
	if err := removeAnnotationFromResources(testCtx, ns, agentMachineGVK, "cluster.x-k8s.io/paused", logger); err != nil {
		return fmt.Errorf("failed to unpause AgentMachine resources: %w", err)
	}

	agentClusterGVK := schema.GroupVersionKind{
		Group:   "capi-provider.agent-install.openshift.io",
		Version: "v1beta1",
		Kind:    "AgentCluster",
	}
	if err := removeAnnotationFromResources(testCtx, ns, agentClusterGVK, "cluster.x-k8s.io/paused", logger); err != nil {
		return fmt.Errorf("failed to unpause AgentCluster resources: %w", err)
	}

	return nil
}

// prepareAgentPlatformForBreak prepares Agent platform resources before breaking the cluster.
// It annotates Agent CRs with skip-spoke-cleanup and sets preserveOnDelete on ClusterDeployment
// to prevent agents from being unbound and hosts from being rebooted by Ironic.
func prepareAgentPlatformForBreak(testCtx *internal.TestContext, logger logr.Logger) error {
	hc := testCtx.GetHostedCluster()
	if hc.Spec.Platform.Agent == nil || hc.Spec.Platform.Agent.AgentNamespace == "" {
		return fmt.Errorf("agent namespace not set on HostedCluster")
	}

	agentNamespace := hc.Spec.Platform.Agent.AgentNamespace
	if err := annotateAgentsSkipSpokeCleanup(testCtx, agentNamespace, logger); err != nil {
		return fmt.Errorf("failed to annotate agents with skip-spoke-cleanup: %w", err)
	}

	if err := setClusterDeploymentPreserveOnDelete(testCtx, testCtx.ControlPlaneNamespace, logger); err != nil {
		return fmt.Errorf("failed to set preserveOnDelete on ClusterDeployment: %w", err)
	}

	return nil
}

// annotateAgentsSkipSpokeCleanup adds the agent.agent-install.openshift.io/skip-spoke-cleanup
// annotation to all Agent CRs in the given namespace. This prevents agents (hosts) from being
// removed from the hosted cluster as nodes during deletion.
func annotateAgentsSkipSpokeCleanup(testCtx *internal.TestContext, agentNamespace string, logger logr.Logger) error {
	agentGVK := schema.GroupVersionKind{
		Group:   "agent-install.openshift.io",
		Version: "v1beta1",
		Kind:    "Agent",
	}
	return setAnnotationOnResources(testCtx, agentNamespace, agentGVK,
		"agent.agent-install.openshift.io/skip-spoke-cleanup", "true", logger)
}

// setClusterDeploymentPreserveOnDelete sets spec.preserveOnDelete=true on all ClusterDeployment
// CRs in the given namespace. This prevents agents from being unbound when the ClusterDeployment
// is deleted, avoiding Ironic-triggered node reboots.
func setClusterDeploymentPreserveOnDelete(testCtx *internal.TestContext, namespace string, logger logr.Logger) error {
	cdGVK := schema.GroupVersionKind{
		Group:   "hive.openshift.io",
		Version: "v1",
		Kind:    "ClusterDeployment",
	}

	list := &unstructured.UnstructuredList{}
	list.SetGroupVersionKind(schema.GroupVersionKind{
		Group:   cdGVK.Group,
		Version: cdGVK.Version,
		Kind:    cdGVK.Kind + "List",
	})

	if err := testCtx.MgmtClient.List(testCtx.Context, list, crclient.InNamespace(namespace)); err != nil {
		return fmt.Errorf("failed to list ClusterDeployments in namespace %s: %w", namespace, err)
	}

	patch := []byte(`{"spec":{"preserveOnDelete":true}}`)
	for i := range list.Items {
		obj := &list.Items[i]
		logger.Info("Setting preserveOnDelete on ClusterDeployment", "name", obj.GetName(), "namespace", obj.GetNamespace())
		if err := testCtx.MgmtClient.Patch(testCtx.Context, obj, crclient.RawPatch(types.MergePatchType, patch)); err != nil {
			return fmt.Errorf("failed to patch ClusterDeployment %s: %w", obj.GetName(), err)
		}
	}

	return nil
}

// setAnnotationOnResources adds an annotation to all resources of the given GVK in the namespace.
func setAnnotationOnResources(testCtx *internal.TestContext, namespace string, gvk schema.GroupVersionKind, annotation, value string, logger logr.Logger) error {
	list := &unstructured.UnstructuredList{}
	list.SetGroupVersionKind(schema.GroupVersionKind{
		Group:   gvk.Group,
		Version: gvk.Version,
		Kind:    gvk.Kind + "List",
	})

	if err := testCtx.MgmtClient.List(testCtx.Context, list, crclient.InNamespace(namespace)); err != nil {
		return fmt.Errorf("failed to list %s in namespace %s: %w", gvk.Kind, namespace, err)
	}

	patch := []byte(fmt.Sprintf(`{"metadata":{"annotations":{%q:%q}}}`, annotation, value))
	for i := range list.Items {
		obj := &list.Items[i]
		logger.Info("Setting annotation on resource", "kind", gvk.Kind, "name", obj.GetName(), "annotation", annotation)
		if err := testCtx.MgmtClient.Patch(testCtx.Context, obj, crclient.RawPatch(types.MergePatchType, patch)); err != nil {
			return fmt.Errorf("failed to patch %s %s: %w", gvk.Kind, obj.GetName(), err)
		}
	}

	return nil
}

// removeAnnotationFromResources removes an annotation from all resources of the given GVK in the namespace.
func removeAnnotationFromResources(testCtx *internal.TestContext, namespace string, gvk schema.GroupVersionKind, annotation string, logger logr.Logger) error {
	list := &unstructured.UnstructuredList{}
	list.SetGroupVersionKind(schema.GroupVersionKind{
		Group:   gvk.Group,
		Version: gvk.Version,
		Kind:    gvk.Kind + "List",
	})

	if err := testCtx.MgmtClient.List(testCtx.Context, list, crclient.InNamespace(namespace)); err != nil {
		return fmt.Errorf("failed to list %s in namespace %s: %w", gvk.Kind, namespace, err)
	}

	patch := []byte(fmt.Sprintf(`{"metadata":{"annotations":{%q:null}}}`, annotation))
	for i := range list.Items {
		obj := &list.Items[i]
		logger.Info("Removing annotation from resource", "kind", gvk.Kind, "name", obj.GetName(), "annotation", annotation)
		if err := testCtx.MgmtClient.Patch(testCtx.Context, obj, crclient.RawPatch(types.MergePatchType, patch)); err != nil {
			return fmt.Errorf("failed to patch %s %s: %w", gvk.Kind, obj.GetName(), err)
		}
	}

	return nil
}

// deleteControlPlaneNamespace deletes the HCP namespace.
// If timeout is 0, the function returns immediately after issuing the delete.
// Otherwise, it waits up to timeout for the namespace to be fully removed.
func deleteControlPlaneNamespace(testCtx *internal.TestContext, timeout time.Duration) error {
	namespace := testCtx.ControlPlaneNamespace
	if namespace == "" {
		return fmt.Errorf("control plane namespace is not set in test context")
	}
	return deleteNamespace(testCtx, namespace, 0, timeout)
}

// deleteHostedClusterNamespace deletes the HostedCluster namespace.
// If timeout is 0, the function returns immediately after issuing the delete.
// Otherwise, it waits up to timeout for the namespace to be fully removed.
func deleteHostedClusterNamespace(testCtx *internal.TestContext, timeout time.Duration) error {
	namespace := testCtx.ClusterNamespace
	if namespace == "" {
		return fmt.Errorf("cluster namespace is not set in test context")
	}
	return deleteNamespace(testCtx, namespace, -1, timeout)
}
