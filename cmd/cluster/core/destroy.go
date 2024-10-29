package core

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/go-logr/logr"
	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	awsutil "github.com/openshift/hypershift/cmd/infra/aws/util"
	"github.com/openshift/hypershift/cmd/util"
	v1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/equality"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/sets"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/client-go/util/retry"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
)

const (
	destroyFinalizer = "openshift.io/destroy-cluster"
)

// DestroyPlatformSpecifics can be used to destroy platform specific resources which are unknown to hypershift
type DestroyPlatformSpecifics = func(ctx context.Context, options *DestroyOptions) error

type DestroyOptions struct {
	ClusterGracePeriod    time.Duration
	Name                  string
	Namespace             string
	AWSPlatform           AWSPlatformDestroyOptions
	AzurePlatform         AzurePlatformDestroyOptions
	PowerVSPlatform       PowerVSPlatformDestroyOptions
	InfraID               string
	DestroyCloudResources bool
	Log                   logr.Logger
	CredentialSecretName  string
	TechPreviewEnabled    bool
}

type AWSPlatformDestroyOptions struct {
	Credentials         awsutil.AWSCredentialsOptions
	BaseDomain          string
	BaseDomainPrefix    string
	PreserveIAM         bool
	Region              string
	PostDeleteAction    func()
	AwsInfraGracePeriod time.Duration
	VPCOwnerCredentials awsutil.AWSCredentialsOptions
}

type AzurePlatformDestroyOptions struct {
	CredentialsFile   string
	Location          string
	ResourceGroupName string
	ControlPlaneMIs   hyperv1.AzureResourceManagedIdentities
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
	c, err := util.GetClient()
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
	c, err := util.GetClient()
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
				return err
			}
		}
	}

	if shouldDestroyPlatformSpecifics {
		// Destroy additional resources which are specific to the current platform
		if err = destroyPlatformSpecifics(ctx, o); err != nil {
			return err
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
			return err
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
