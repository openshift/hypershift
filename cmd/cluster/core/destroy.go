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

	agentv1 "github.com/openshift/cluster-api-provider-agent/api/v1beta1"

	appsv1 "k8s.io/api/apps/v1"
	v1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/equality"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/sets"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/client-go/util/retry"

	capaaws "sigs.k8s.io/cluster-api-provider-aws/v2/api/v1beta2"
	capzv1 "sigs.k8s.io/cluster-api-provider-azure/api/v1beta1"
	capikubevirt "sigs.k8s.io/cluster-api-provider-kubevirt/api/v1alpha1"
	capiv1 "sigs.k8s.io/cluster-api/api/core/v1beta1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"

	"github.com/go-logr/logr"
)

const (
	destroyFinalizer = "openshift.io/destroy-cluster"
)

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
	hostedClusterExists := hostedCluster != nil
	shouldDestroyPlatformSpecifics := destroyPlatformSpecifics != nil
	c, err := util.GetClientWithKubeconfig(o.Kubeconfig)
	if err != nil {
		return err
	}

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
				o.Log.Info("Grace period expired and --force is set, force-removing finalizers from all child resources",
					"namespace", o.Namespace, "name", o.Name)
				if forceErr := forceRemoveAllFinalizers(ctx, hostedCluster, o, c); forceErr != nil {
					o.Log.Error(forceErr, "Errors during force finalizer removal, continuing with platform cleanup")
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

	// clean up CLI generated secrets
	if err = deleteCLISecrets(ctx, o, c); err != nil {
		return err
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
	o.Log.Info("Deleting Secrets", "namespace", o.Namespace)
	if err := c.DeleteAllOf(ctx, &v1.Secret{}, client.InNamespace(o.Namespace), client.MatchingLabels{util.AutoInfraLabelName: o.InfraID}); err != nil {
		if apierrors.IsNotFound(err) {
			o.Log.Info("Secrets not found based on labels, skipping delete", "namespace", o.Namespace, "labels", util.AutoInfraLabelName+":"+o.InfraID)
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
		return fmt.Errorf("failed to strip finalizers from %T %s/%s: %w",
			obj, obj.GetNamespace(), obj.GetName(), err)
	}
	log.Info("Stripped finalizers", "kind", fmt.Sprintf("%T", obj), "namespace", obj.GetNamespace(), "name", obj.GetName())
	return nil
}

// stripFinalizersFromList lists objects of the given type in a namespace and strips all finalizers.
// CRD-not-found errors are silently ignored so this works on clusters without the CRD installed.
func stripFinalizersFromList(ctx context.Context, c client.Client, list client.ObjectList, namespace string, log logr.Logger) []error {
	if err := c.List(ctx, list, client.InNamespace(namespace)); err != nil {
		if !apierrors.IsNotFound(err) && !meta.IsNoMatchError(err) {
			return []error{fmt.Errorf("failed to list %T: %w", list, err)}
		}
		return nil
	}
	var errs []error
	var count int
	if err := meta.EachListItem(list, func(obj runtime.Object) error {
		count++
		if co, ok := obj.(client.Object); ok {
			if err := stripFinalizers(ctx, c, co, log); err != nil {
				errs = append(errs, err)
			}
		}
		return nil
	}); err != nil {
		errs = append(errs, fmt.Errorf("failed to iterate %T: %w", list, err))
	}
	log.Info("Processed resources", "type", fmt.Sprintf("%T", list), "namespace", namespace, "count", count)
	return errs
}

// forceRemoveAllFinalizers strips finalizers from all child resources in the
// control plane namespace and NodePools in the HC namespace, then from the
// HostedCluster itself (preserving the destroy finalizer for the normal
// removal path). Resources are processed bottom-up so that Kubernetes garbage
// collection can proceed as each layer is unblocked.
func forceRemoveAllFinalizers(ctx context.Context, hostedCluster *hyperv1.HostedCluster, o *DestroyOptions, c client.Client) error {
	cpNamespace := manifests.HostedControlPlaneNamespace(o.Namespace, o.Name)
	var errs []error

	// Bottom-up: infra machines → CAPI machines → clusters → HCP → deployments.
	// All provider-specific types are listed; stripFinalizersFromList silently
	// skips CRDs that are not installed on the current cluster.
	cpResources := []client.ObjectList{
		&capaaws.AWSMachineList{},
		&capaaws.AWSClusterList{},
		&capzv1.AzureMachineList{},
		&capzv1.AzureClusterList{},
		&agentv1.AgentMachineList{},
		&agentv1.AgentClusterList{},
		&capikubevirt.KubevirtMachineList{},
		&capikubevirt.KubevirtClusterList{},
		&capiv1.MachineList{},
		&capiv1.MachineSetList{},
		&capiv1.MachineDeploymentList{},
		&capiv1.ClusterList{},
		&hyperv1.HostedControlPlaneList{},
		&appsv1.DeploymentList{},
	}
	for _, list := range cpResources {
		errs = append(errs, stripFinalizersFromList(ctx, c, list, cpNamespace, o.Log)...)
	}

	// NodePools live in the HC namespace, not the CP namespace
	errs = append(errs, stripFinalizersFromList(ctx, c, &hyperv1.NodePoolList{}, o.Namespace, o.Log)...)

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
			o.Log.Info("Stripped non-destroy finalizers from HostedCluster", "namespace", o.Namespace, "name", o.Name)
		}
	}

	// Strip both metadata.finalizers and spec.finalizers from the control
	// plane namespace, then delete it. spec.finalizers require the finalize
	// subresource; a Terminating namespace stays stuck until both are empty.
	ns := &v1.Namespace{}
	if err := c.Get(ctx, types.NamespacedName{Name: cpNamespace}, ns); err != nil {
		if !apierrors.IsNotFound(err) {
			errs = append(errs, fmt.Errorf("failed to get namespace %s: %w", cpNamespace, err))
		}
	} else {
		if err := stripFinalizers(ctx, c, ns, o.Log); err != nil {
			errs = append(errs, err)
		}
		if len(ns.Spec.Finalizers) > 0 {
			ns.Spec.Finalizers = nil
			if err := c.SubResource("finalize").Update(ctx, ns); err != nil {
				if !apierrors.IsNotFound(err) {
					errs = append(errs, fmt.Errorf("failed to clear spec.finalizers on namespace %s: %w", cpNamespace, err))
				}
			} else {
				o.Log.Info("Cleared spec.finalizers on namespace", "namespace", cpNamespace)
			}
		}
		if err := c.Delete(ctx, ns); err != nil {
			if !apierrors.IsNotFound(err) {
				errs = append(errs, fmt.Errorf("failed to delete namespace %s: %w", cpNamespace, err))
			}
		} else {
			o.Log.Info("Deleted control plane namespace", "namespace", cpNamespace)
		}
	}

	if len(errs) > 0 {
		return fmt.Errorf("force removal encountered %d error(s): %w", len(errs), errors.Join(errs...))
	}
	o.Log.Info("Force removal of all finalizers complete", "namespace", o.Namespace, "name", o.Name)
	return nil
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
