package core

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	awsutil "github.com/openshift/hypershift/cmd/infra/aws/util"
	"github.com/openshift/hypershift/cmd/util"
	"github.com/openshift/hypershift/hypershift-operator/controllers/manifests"

	v1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/equality"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/sets"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/client-go/discovery"
	"k8s.io/client-go/util/retry"

	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"

	"github.com/go-logr/logr"
)

const (
	destroyFinalizer = "openshift.io/destroy-cluster"
)

type namespacedResourceDiscovery interface {
	ServerPreferredNamespacedResources() ([]*metav1.APIResourceList, error)
}

// DestroyPlatformSpecifics can be used to destroy platform specific resources which are unknown to hypershift
type DestroyPlatformSpecifics = func(ctx context.Context, options *DestroyOptions) error

type DestroyOptions struct {
	ClusterGracePeriod    time.Duration
	ForceDestroy          bool
	Kubeconfig            string
	Name                  string
	Namespace             string
	AWSPlatform           AWSPlatformDestroyOptions
	AzurePlatform         AzurePlatformDestroyOptions
	PowerVSPlatform       PowerVSPlatformDestroyOptions
	InfraID               string
	DestroyCloudResources bool
	Log                   logr.Logger
	CredentialSecretName  string
	RedactBaseDomain      bool
}

type AWSPlatformDestroyOptions struct {
	Credentials                  awsutil.AWSCredentialsOptions
	BaseDomain                   string
	BaseDomainPrefix             string
	PreserveIAM                  bool
	Region                       string
	PostDeleteAction             func()
	AwsInfraGracePeriod          time.Duration
	VPCOwnerCredentials          awsutil.AWSCredentialsOptions
	PrivateZonesInClusterAccount bool
}

type AzurePlatformDestroyOptions struct {
	CredentialsFile       string
	Location              string
	ResourceGroupName     string
	PreserveResourceGroup bool
	Cloud                 string
	DNSZoneRGName         string
}

type PowerVSPlatformDestroyOptions struct {
	BaseDomain             string
	ResourceGroup          string
	CISCRN                 string
	CISDomainID            string
	Region                 string
	Zone                   string
	VPCRegion              string
	VPC                    string
	CloudInstanceID        string
	CloudConnection        string
	Debug                  bool
	PER                    bool
	TransitGatewayLocation string
	TransitGateway         string
}

func GetCluster(ctx context.Context, o *DestroyOptions) (*hyperv1.HostedCluster, error) {
	c, err := util.GetClientWithKubeconfig(o.Kubeconfig)
	if err != nil {
		return nil, err
	}

	var hostedCluster hyperv1.HostedCluster
	if err := c.Get(ctx, types.NamespacedName{Namespace: o.Namespace, Name: o.Name}, &hostedCluster); err != nil {
		if apierrors.IsNotFound(err) || meta.IsNoMatchError(err) {
			o.Log.Info("Hosted cluster not found, destroying infrastructure from user input", "namespace", o.Namespace, "name", o.Name, "infraID", o.InfraID)
			return nil, nil
		}
		return nil, fmt.Errorf("failed to get hostedcluster: %w", err)
	}

	o.Log.Info("Found hosted cluster", "namespace", hostedCluster.Namespace, "name", hostedCluster.Name)
	return &hostedCluster, nil
}

func DestroyCluster(ctx context.Context, hostedCluster *hyperv1.HostedCluster, o *DestroyOptions, destroyPlatformSpecifics DestroyPlatformSpecifics) error {
	c, err := util.GetClientWithKubeconfig(o.Kubeconfig)
	if err != nil {
		return err
	}

	var resourceDiscovery namespacedResourceDiscovery
	if o.ForceDestroy && destroyPlatformSpecifics != nil {
		config, err := util.GetConfigWithKubeconfig(o.Kubeconfig)
		if err != nil {
			return fmt.Errorf("failed to create discovery config: %w", err)
		}
		resourceDiscovery, err = discovery.NewDiscoveryClientForConfig(config)
		if err != nil {
			return fmt.Errorf("failed to create discovery client: %w", err)
		}
	}

	return destroyCluster(ctx, c, resourceDiscovery, hostedCluster, o, destroyPlatformSpecifics)
}

func destroyCluster(ctx context.Context, c client.Client, resourceDiscovery namespacedResourceDiscovery, hostedCluster *hyperv1.HostedCluster, o *DestroyOptions, destroyPlatformSpecifics DestroyPlatformSpecifics) error {
	var err error
	var forceCleanupErr error
	hostedClusterExists := hostedCluster != nil
	shouldDestroyPlatformSpecifics := destroyPlatformSpecifics != nil

	// If the hosted cluster exists, add a finalizer, delete it, and wait for
	// the cluster to be cleaned up before destroying its infrastructure.
	if hostedClusterExists {

		original := hostedCluster.DeepCopy()
		if shouldDestroyPlatformSpecifics {
			setFinalizer(hostedCluster, o)
		}
		if o.DestroyCloudResources {
			setDestroyCloudResourcesAnnotation(hostedCluster, o)
		}

		// if the hostedcluster is needs to be modified during deletion, patch the
		// hosted cluster before deleting it.
		if !equality.Semantic.DeepEqual(&hostedCluster, original) {
			if err := c.Patch(ctx, hostedCluster, client.MergeFrom(original)); err != nil {
				if apierrors.IsNotFound(err) {
					o.Log.Info("Hosted cluster not found, skipping client updates", "namespace", o.Namespace, "name", o.Name)
				} else if !strings.Contains(err.Error(), "no new finalizers can be added if the object is being deleted") {
					return fmt.Errorf("failed to add client finalizer to hosted cluster: %w", err)
				}
			} else {
				o.Log.Info("Updated hosted cluster", "namespace", o.Namespace, "name", o.Name)
			}
		}

		o.Log.Info("Deleting hosted cluster", "namespace", o.Namespace, "name", o.Name)
		if err = c.Delete(ctx, hostedCluster); err != nil {
			if apierrors.IsNotFound(err) {
				o.Log.Info("Hosted not found, skipping delete", "namespace", o.Namespace, "name", o.Name)
			} else {
				return fmt.Errorf("failed to delete hostedcluster: %w", err)
			}
		}

		if shouldDestroyPlatformSpecifics {
			if err = waitForRestOfFinalizers(ctx, hostedCluster, o, c); err != nil {
				if !o.ForceDestroy {
					return err
				}
				o.Log.Info("Grace period expired and --force is set, force-removing finalizers from all child resources")
				if forceCleanupErr = forceRemoveAllFinalizers(ctx, hostedCluster, o, c, resourceDiscovery); forceCleanupErr != nil {
					logCleanupError(o.Log, forceCleanupErr, "Errors during force finalizer removal, continuing with platform cleanup")
				}
			}
		}
	}

	if shouldDestroyPlatformSpecifics {
		if err = destroyPlatformSpecifics(ctx, o); err != nil {
			if err := returnOrForceLog(o, err, "Platform-specific cleanup failed with --force, continuing"); err != nil {
				return err
			}
		}
	} else if err = waitForClusterDeletion(ctx, hostedCluster, o, c); err != nil {
		return err
	}

	// Non-fatal: CLI-created secrets are labeled with DeleteWithClusterLabelName: "true"
	// and AutoInfraLabelName. The operator's reconcileCLISecrets sets the HostedCluster
	// as their ownerRef, ensuring they are garbage-collected when the HC is deleted.
	var cliSecretsErr error
	if err = deleteCLISecrets(ctx, o, c); err != nil {
		cliSecretsErr = err
		logCleanupError(o.Log, err, "Failed to delete CLI generated secrets, skipping")
	}

	if forceCleanupErr != nil {
		if cliSecretsErr != nil {
			return errors.Join(forceCleanupErr, cliSecretsErr)
		}
		return forceCleanupErr
	}

	if shouldDestroyPlatformSpecifics && hostedClusterExists {
		if err = removeFinalizer(ctx, hostedCluster, o, c); err != nil {
			if err := returnOrForceLog(o, err, "Failed to remove destroy finalizer with --force, continuing"); err != nil {
				return err
			}
		}
	}

	o.Log.Info("Successfully destroyed cluster and infrastructure", "namespace", o.Namespace, "name", o.Name, "infraID", o.InfraID)
	return nil
}

func deleteCLISecrets(ctx context.Context, o *DestroyOptions, c client.Client) error {
	o.Log.Info("Deleting CLI generated secrets")
	if err := c.DeleteAllOf(ctx, &v1.Secret{}, client.InNamespace(o.Namespace), client.MatchingLabels{util.AutoInfraLabelName: o.InfraID}); err != nil {
		if apierrors.IsNotFound(err) {
			o.Log.Info("CLI generated secrets not found based on labels, skipping delete")
		} else {
			return fmt.Errorf("failed to clean up secrets in %s namespace: %w", o.Namespace, err)
		}
	} else {
		o.Log.Info("Deleted CLI generated secrets")
	}
	return nil
}

func removeFinalizer(ctx context.Context, hostedCluster *hyperv1.HostedCluster, o *DestroyOptions, c client.Client) error {
	if !sets.New[string](hostedCluster.Finalizers...).Has(destroyFinalizer) {
		return nil
	}

	if err := retry.RetryOnConflict(retry.DefaultBackoff, func() error {
		// Ensure that we have the latest hostedCluster resource
		if err := c.Get(ctx, client.ObjectKeyFromObject(hostedCluster), hostedCluster); err != nil {
			if !apierrors.IsNotFound(err) {
				return fmt.Errorf("failed to fetch latest HostedCluster: %w", err)
			}
			return nil
		}
		original := hostedCluster.DeepCopy()
		controllerutil.RemoveFinalizer(hostedCluster, destroyFinalizer)
		if err := c.Patch(ctx, hostedCluster, client.MergeFromWithOptions(original, client.MergeFromWithOptimisticLock{})); err != nil {
			if !apierrors.IsNotFound(err) {
				return err
			}
		} else {
			o.Log.Info("Finalized hosted cluster", "namespace", o.Namespace, "name", o.Name)
		}
		return nil
	}); err != nil {
		return err
	}
	return nil
}

// returnOrForceLog returns err when --force is not set; otherwise logs the
// error and returns nil so that the caller can continue best-effort cleanup.
func returnOrForceLog(o *DestroyOptions, err error, msg string) error {
	if !o.ForceDestroy {
		return err
	}
	o.Log.Error(err, msg)
	return nil
}

func logCleanupError(log logr.Logger, err error, msg string) {
	log.Info(msg, "error", safeErrorSummary(err))
}

func safeErrorSummary(err error) string {
	var apiStatus apierrors.APIStatus
	if errors.As(err, &apiStatus) {
		status := apiStatus.Status()
		if status.Reason != "" && status.Code != 0 {
			return fmt.Sprintf("%s (HTTP %d)", status.Reason, status.Code)
		}
		if status.Reason != "" {
			return string(status.Reason)
		}
		if status.Code != 0 {
			return fmt.Sprintf("HTTP %d", status.Code)
		}
	}

	errorTypes := sets.New[string]()
	var collectLeafTypes func(error)
	collectLeafTypes = func(current error) {
		if current == nil {
			return
		}
		if joined, ok := current.(interface{ Unwrap() []error }); ok {
			for _, nested := range joined.Unwrap() {
				collectLeafTypes(nested)
			}
			return
		}
		if nested := errors.Unwrap(current); nested != nil {
			collectLeafTypes(nested)
			return
		}
		errorTypes.Insert(fmt.Sprintf("%T", current))
	}
	collectLeafTypes(err)
	if errorTypes.Len() == 0 {
		return "unknown error"
	}
	return strings.Join(sets.List(errorTypes), ", ")
}

// stripFinalizers removes all finalizers from a single object via a merge patch.
func stripFinalizers(ctx context.Context, c client.Client, obj client.Object, log logr.Logger) error {
	if len(obj.GetFinalizers()) == 0 {
		return nil
	}
	original := obj.DeepCopyObject().(client.Object)
	obj.SetFinalizers(nil)
	if err := c.Patch(ctx, obj, client.MergeFrom(original)); err != nil {
		if apierrors.IsNotFound(err) {
			return nil
		}
		return fmt.Errorf("failed to strip finalizers from %s: %w", objectIdentity(obj), err)
	}
	gvk := obj.GetObjectKind().GroupVersionKind()
	resourceType := gvk.String()
	if gvk.Empty() {
		resourceType = fmt.Sprintf("%T", obj)
	}
	log.Info("Stripped finalizers", "resource", resourceType)
	return nil
}

func objectIdentity(obj client.Object) string {
	gvk := obj.GetObjectKind().GroupVersionKind()
	if gvk.Empty() {
		return fmt.Sprintf("%T", obj)
	}
	return gvk.String()
}

func stripNodePoolFinalizers(ctx context.Context, c client.Client, namespace, clusterName string, log logr.Logger) []error {
	nodePools := &hyperv1.NodePoolList{}
	if err := c.List(ctx, nodePools, client.InNamespace(namespace)); err != nil {
		if !apierrors.IsNotFound(err) && !meta.IsNoMatchError(err) {
			return []error{fmt.Errorf("failed to list %T: %w", nodePools, err)}
		}
		return nil
	}

	var errs []error
	var count int
	for i := range nodePools.Items {
		nodePool := &nodePools.Items[i]
		if nodePool.Spec.ClusterName != clusterName {
			continue
		}
		count++
		if err := stripFinalizers(ctx, c, nodePool, log); err != nil {
			errs = append(errs, err)
		}
	}
	log.Info("Processed resources", "type", fmt.Sprintf("%T", nodePools), "count", count)
	return errs
}

// cleanupNamespacedResources removes every discovered namespaced object after
// namespace deletion is requested. Discovery is used instead of a fixed type
// allowlist because extension resources can add finalizers that the destroy
// command does not know about. Normal namespace lifecycle finalization remains
// responsible for detecting anything discovery did not return.
func cleanupNamespacedResources(ctx context.Context, c client.Client, resourceDiscovery namespacedResourceDiscovery, namespace string, log logr.Logger) []error {
	if resourceDiscovery == nil {
		return []error{fmt.Errorf("namespaced resource discovery is not configured")}
	}

	apiResourceLists, err := resourceDiscovery.ServerPreferredNamespacedResources()
	var errs []error
	if err != nil && !discovery.IsGroupDiscoveryFailedError(err) {
		return []error{fmt.Errorf("failed to discover namespaced resources: %w", err)}
	}
	if err != nil {
		groupCount := 0
		var discoveryErr *discovery.ErrGroupDiscoveryFailed
		if errors.As(err, &discoveryErr) {
			groupCount = len(discoveryErr.Groups)
		}
		log.Info("Partial namespaced resource discovery failed; namespace finalization will remain blocked", "groupCount", groupCount)
		errs = append(errs, fmt.Errorf("partial namespaced resource discovery failed for %d API group(s): %w", groupCount, err))
	}

	for _, apiResourceList := range apiResourceLists {
		groupVersion, err := schema.ParseGroupVersion(apiResourceList.GroupVersion)
		if err != nil {
			errs = append(errs, fmt.Errorf("failed to parse discovered group version: %w", err))
			continue
		}

		for _, apiResource := range apiResourceList.APIResources {
			if strings.Contains(apiResource.Name, "/") || !supportsVerb(apiResource.Verbs, "list") || !supportsVerb(apiResource.Verbs, "delete") {
				continue
			}

			resourceGVK := groupVersion.WithKind(apiResource.Kind)
			if err := cleanupNamespacedResource(ctx, c, resourceGVK, apiResource, namespace, log); err != nil {
				errs = append(errs, err)
			}
		}
	}
	return errs
}

func cleanupNamespacedResource(ctx context.Context, c client.Client, resourceGVK schema.GroupVersionKind, apiResource metav1.APIResource, namespace string, log logr.Logger) error {
	list := &unstructured.UnstructuredList{}
	list.SetGroupVersionKind(resourceGVK.GroupVersion().WithKind(resourceGVK.Kind + "List"))

	if err := c.List(ctx, list, client.InNamespace(namespace)); err != nil {
		if apierrors.IsNotFound(err) || meta.IsNoMatchError(err) || apierrors.IsMethodNotSupported(err) {
			return nil
		}
		return fmt.Errorf("failed to list discovered namespaced resource %s: %w", resourceGVK, err)
	}

	for i := range list.Items {
		obj := &list.Items[i]
		if len(obj.GetFinalizers()) > 0 {
			if !supportsVerb(apiResource.Verbs, "patch") {
				return fmt.Errorf("resource %s does not support patching finalizers", resourceGVK)
			}
			if err := stripFinalizers(ctx, c, obj, log); err != nil {
				return err
			}
		}
		if err := c.Delete(ctx, obj, client.GracePeriodSeconds(0)); err != nil && !apierrors.IsNotFound(err) {
			return fmt.Errorf("failed to delete discovered namespaced resource %s: %w", resourceGVK, err)
		}
	}

	if err := wait.PollUntilContextTimeout(ctx, time.Second, 30*time.Second, true, func(ctx context.Context) (bool, error) {
		remaining := &unstructured.UnstructuredList{}
		remaining.SetGroupVersionKind(resourceGVK.GroupVersion().WithKind(resourceGVK.Kind + "List"))
		if err := c.List(ctx, remaining, client.InNamespace(namespace)); err != nil {
			if apierrors.IsNotFound(err) || meta.IsNoMatchError(err) || apierrors.IsMethodNotSupported(err) {
				return true, nil
			}
			return false, fmt.Errorf("failed to poll discovered namespaced resource %s: %w", resourceGVK, err)
		}
		return len(remaining.Items) == 0, nil
	}); err != nil {
		return fmt.Errorf("discovered namespaced resources of type %s remain: %w", resourceGVK, err)
	}

	log.Info("Deleted namespaced resources", "resource", resourceGVK.String(), "count", len(list.Items))
	return nil
}

func supportsVerb(verbs metav1.Verbs, verb string) bool {
	for _, supportedVerb := range verbs {
		if supportedVerb == verb || supportedVerb == "*" {
			return true
		}
	}
	return false
}

// forceRemoveAllFinalizers strips finalizers from NodePools in the HC namespace,
// removes all objects from the control-plane namespace, then from the HostedCluster
// itself (preserving the destroy finalizer for the normal removal path).
func forceRemoveAllFinalizers(ctx context.Context, hostedCluster *hyperv1.HostedCluster, o *DestroyOptions, c client.Client, resourceDiscovery namespacedResourceDiscovery) error {
	cpNamespace := manifests.HostedControlPlaneNamespace(o.Namespace, o.Name)
	var errs []error
	namespaceTerminating, err := markNamespaceTerminating(ctx, c, cpNamespace)
	if err != nil {
		errs = append(errs, err)
	}

	// Remove and wait for every discovered namespaced resource after requesting
	// namespace deletion. The delete request above blocks new writes while the
	// sweep runs. The namespace lifecycle controller remains responsible for
	// clearing spec.finalizers after it confirms that all content is gone.
	errs = append(errs, cleanupNamespacedResources(ctx, c, resourceDiscovery, cpNamespace, o.Log)...)

	// NodePools live in the HC namespace, not the CP namespace
	errs = append(errs, stripNodePoolFinalizers(ctx, c, o.Namespace, o.Name, o.Log)...)

	// Strip all HostedCluster finalizers except the destroy finalizer, which
	// is removed by the normal removeFinalizer path after platform cleanup.
	if err := c.Get(ctx, client.ObjectKeyFromObject(hostedCluster), hostedCluster); err != nil {
		if !apierrors.IsNotFound(err) {
			errs = append(errs, fmt.Errorf("failed to refresh HostedCluster: %w", err))
		}
	} else if len(hostedCluster.Finalizers) > 0 {
		original := hostedCluster.DeepCopy()
		var kept []string
		for _, f := range hostedCluster.Finalizers {
			if f == destroyFinalizer {
				kept = append(kept, f)
			}
		}
		hostedCluster.SetFinalizers(kept)
		if err := c.Patch(ctx, hostedCluster, client.MergeFrom(original)); err != nil {
			if !apierrors.IsNotFound(err) {
				errs = append(errs, fmt.Errorf("failed to strip non-destroy finalizers from HostedCluster: %w", err))
			}
		} else {
			o.Log.Info("Stripped non-destroy finalizers from HostedCluster")
		}
	}

	if len(errs) > 0 {
		return fmt.Errorf("force removal encountered %d error(s): %w", len(errs), errors.Join(errs...))
	}
	if !namespaceTerminating {
		return nil
	}

	// Strip only metadata.finalizers from the control plane namespace. Never
	// clear spec.finalizers: the namespace lifecycle controller uses its content
	// finalizer to prevent undiscovered resources from being orphaned.
	ns := &v1.Namespace{}
	if err := c.Get(ctx, types.NamespacedName{Name: cpNamespace}, ns); err != nil {
		if !apierrors.IsNotFound(err) {
			errs = append(errs, fmt.Errorf("failed to get control plane namespace: %w", err))
		}
	} else {
		if err := stripFinalizers(ctx, c, ns, o.Log); err != nil {
			errs = append(errs, err)
		}
		if err := c.Delete(ctx, ns); err != nil {
			if !apierrors.IsNotFound(err) {
				errs = append(errs, fmt.Errorf("failed to request control plane namespace deletion: %w", err))
			}
		} else {
			o.Log.Info("Requested control plane namespace deletion")
		}
	}

	if len(errs) > 0 {
		return fmt.Errorf("force removal encountered %d error(s): %w", len(errs), errors.Join(errs...))
	}
	o.Log.Info("Force finalizer cleanup complete; namespace deletion requested")
	return nil
}

func markNamespaceTerminating(ctx context.Context, c client.Client, namespace string) (bool, error) {
	ns := &v1.Namespace{}
	if err := c.Get(ctx, types.NamespacedName{Name: namespace}, ns); err != nil {
		if apierrors.IsNotFound(err) {
			return false, nil
		}
		return false, fmt.Errorf("failed to get control plane namespace before deletion: %w", err)
	}
	if ns.DeletionTimestamp != nil {
		return true, nil
	}
	if err := c.Delete(ctx, ns); err != nil && !apierrors.IsNotFound(err) {
		return false, fmt.Errorf("failed to mark control plane namespace for deletion: %w", err)
	}
	return true, nil
}

// waitForRestOfFinalizers waits for the hosted cluster to have only the CLI's finalizer remaining,
// which should indicate the cluster was successfully torn down.
func waitForRestOfFinalizers(ctx context.Context, hostedCluster *hyperv1.HostedCluster, o *DestroyOptions, c client.Client) error {
	clusterDeleteCtx, clusterDeleteCtxCancel := context.WithTimeout(ctx, o.ClusterGracePeriod)
	defer clusterDeleteCtxCancel()

	err := wait.PollUntilContextCancel(clusterDeleteCtx, 1*time.Second, true, func(ctx context.Context) (bool, error) {
		if err := c.Get(ctx, types.NamespacedName{Namespace: o.Namespace, Name: o.Name}, hostedCluster); err != nil {
			if apierrors.IsNotFound(err) {
				return true, nil
			}
			o.Log.Error(err, "Failed to get hosted cluster", "namespace", o.Namespace, "name", o.Name)
			return false, nil
		}
		done := len(hostedCluster.Finalizers) == 1 && hostedCluster.Finalizers[0] == destroyFinalizer
		return done, nil
	})
	if err != nil {
		return fmt.Errorf("hostedcluster wasn't finalized, aborting delete: %w", err)
	}
	return nil
}

func setDestroyCloudResourcesAnnotation(hostedCluster *hyperv1.HostedCluster, o *DestroyOptions) {
	if hostedCluster.Annotations == nil {
		hostedCluster.Annotations = map[string]string{}
	}
	hostedCluster.Annotations[hyperv1.CleanupCloudResourcesAnnotation] = "true"
	o.Log.Info("Marking cleanup of cloud resources for hosted cluster", "namespace", hostedCluster.Namespace, "name", hostedCluster.Name)
}

func setFinalizer(hostedCluster *hyperv1.HostedCluster, o *DestroyOptions) {
	if sets.New[string](hostedCluster.Finalizers...).Has(destroyFinalizer) {
		return
	}
	if hostedCluster.DeletionTimestamp == nil {
		controllerutil.AddFinalizer(hostedCluster, destroyFinalizer)
	}
	o.Log.Info("Setting client finalizer for hosted cluster", "namespace", hostedCluster.Namespace, "name", hostedCluster.Name)
}

func waitForClusterDeletion(ctx context.Context, hostedCluster *hyperv1.HostedCluster, o *DestroyOptions, c client.Client) error {
	clusterDeleteCtx, clusterDeleteCtxCancel := context.WithTimeout(ctx, o.ClusterGracePeriod)
	defer clusterDeleteCtxCancel()

	err := wait.PollUntilContextCancel(clusterDeleteCtx, 1*time.Second, true, func(ctx context.Context) (bool, error) {
		if err := c.Get(ctx, types.NamespacedName{Namespace: o.Namespace, Name: o.Name}, hostedCluster); err != nil {
			if apierrors.IsNotFound(err) {
				return true, nil
			}
			o.Log.Error(err, "Failed to get hosted cluster", "namespace", o.Namespace, "name", o.Name)
			return false, nil
		}

		// don't wait for grace period. Nothing happens after grace period in the controller, but it's only
		// for debug. So it's safe to continue in case of grace period.
		if _, ok := hostedCluster.Annotations[hyperv1.HCDestroyGracePeriodAnnotation]; ok {
			if meta.FindStatusCondition(hostedCluster.Status.Conditions, string(hyperv1.HostedClusterDestroyed)) != nil {
				return true, nil
			}
		}

		return false, nil
	})

	if err != nil {
		o.Log.Error(err, "HostedCluster deletion failed", "namespace", o.Namespace, "name", o.Name)
		return err
	}

	return nil
}
