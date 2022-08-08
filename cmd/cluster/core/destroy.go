package core

import (
	"context"
	"fmt"
	"time"

	"github.com/go-logr/logr"
	hyperv1 "github.com/openshift/hypershift/api/v1alpha1"
	"github.com/openshift/hypershift/cmd/util"
	v1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/sets"
	"k8s.io/apimachinery/pkg/util/wait"
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
}

type AWSPlatformDestroyOptions struct {
	AWSCredentialsFile string
	BaseDomain         string
	PreserveIAM        bool
	Region             string
}

type AzurePlatformDestroyOptions struct {
	CredentialsFile string
	Location        string
}

type PowerVSPlatformDestroyOptions struct {
	BaseDomain    string
	ResourceGroup string
	CISCRN        string
	CISDomainID   string
	Region        string
	Zone          string
	VPCRegion     string
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
	c, err := util.GetClient()
	if err != nil {
		return err
	}

	// If the hosted cluster exists, add a finalizer, delete it, and wait for
	// the cluster to be cleaned up before destroying its infrastructure.
	if hostedClusterExists && !sets.NewString(hostedCluster.Finalizers...).Has(destroyFinalizer) {
		original := hostedCluster.DeepCopy()
		controllerutil.AddFinalizer(hostedCluster, destroyFinalizer)
		if o.DestroyCloudResources {
			if hostedCluster.Annotations == nil {
				hostedCluster.Annotations = map[string]string{}
			}
			hostedCluster.Annotations[hyperv1.CleanupCloudResourcesAnnotation] = "true"
		}
		if err := c.Patch(ctx, hostedCluster, client.MergeFrom(original)); err != nil {
			if apierrors.IsNotFound(err) {
				o.Log.Info("Hosted cluster not found, skipping finalizer update", "namespace", o.Namespace, "name", o.Name)
			} else {
				return fmt.Errorf("failed to add finalizer to hosted cluster: %w", err)
			}
		} else {
			o.Log.Info("Updated finalizer for hosted cluster", "namespace", o.Namespace, "name", o.Name)
		}
		o.Log.Info("Deleting hosted cluster", "namespace", o.Namespace, "name", o.Name)
		if err := c.Delete(ctx, hostedCluster); err != nil {
			if apierrors.IsNotFound(err) {
				o.Log.Info("Hosted not found, skipping delete", "namespace", o.Namespace, "name", o.Name)
			} else {
				return fmt.Errorf("failed to delete hostedcluster: %w", err)
			}
		}
		// Wait for the hosted cluster to have only the CLI's finalizer remaining,
		// which should indicate the cluster was successfully torn down.
		clusterDeleteCtx, clusterDeleteCtxCancel := context.WithTimeout(ctx, o.ClusterGracePeriod)
		defer clusterDeleteCtxCancel()
		err := wait.PollImmediateUntil(1*time.Second, func() (bool, error) {
			if err := c.Get(clusterDeleteCtx, types.NamespacedName{Namespace: o.Namespace, Name: o.Name}, hostedCluster); err != nil {
				if apierrors.IsNotFound(err) {
					return true, nil
				}
				o.Log.Error(err, "Failed to get hosted cluster", "namespace", o.Namespace, "name", o.Name)
				return false, nil
			}
			done := len(hostedCluster.Finalizers) == 1 && hostedCluster.Finalizers[0] == destroyFinalizer
			return done, nil
		}, clusterDeleteCtx.Done())
		if err != nil {
			return fmt.Errorf("hostedcluster wasn't finalized, aborting delete: %w", err)
		}
	}

	// Destroy additional resources which are specific to the current platform
	if err := destroyPlatformSpecifics(ctx, o); err != nil {
		return err
	}

	// clean up CLI generated secrets
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

	if hostedClusterExists && sets.NewString(hostedCluster.Finalizers...).Has(destroyFinalizer) {
		original := hostedCluster.DeepCopy()
		controllerutil.RemoveFinalizer(hostedCluster, destroyFinalizer)
		if err := c.Patch(ctx, hostedCluster, client.MergeFrom(original)); err != nil {
			if !apierrors.IsNotFound(err) {
				return fmt.Errorf("failed to remove finalizer: %w", err)
			}
		} else {
			o.Log.Info("Finalized hosted cluster", "namespace", o.Namespace, "name", o.Name)
		}
	}

	o.Log.Info("Successfully destroyed cluster and infrastructure", "namespace", o.Namespace, "name", o.Name, "infraID", o.InfraID)
	return nil
}

func DestroyPlatformSpecificsNoop(_ context.Context, _ *DestroyOptions) error {
	return nil
}
