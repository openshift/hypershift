package cluster

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/spf13/cobra"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/wait"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"

	hyperapi "github.com/openshift/hypershift/api"
	hyperv1 "github.com/openshift/hypershift/api/v1alpha1"
	awsinfra "github.com/openshift/hypershift/cmd/infra/aws"

	ctrl "sigs.k8s.io/controller-runtime"
	crclient "sigs.k8s.io/controller-runtime/pkg/client"
)

const (
	destroyFinalizer = "openshift.io/destroy-cluster"
)

type DestroyOptions struct {
	Namespace          string
	Name               string
	AWSCredentialsFile string
	ClusterGracePeriod time.Duration
}

func NewDestroyCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "cluster",
		Short: "Destroys a HostedCluster and its associated infrastructure.",
	}

	opts := DestroyOptions{
		Namespace:          "clusters",
		Name:               "",
		AWSCredentialsFile: "",
		ClusterGracePeriod: 15 * time.Minute,
	}

	cmd.Flags().StringVar(&opts.Namespace, "namespace", opts.Namespace, "A cluster namespace")
	cmd.Flags().StringVar(&opts.Name, "name", opts.Name, "A cluster name")
	cmd.Flags().StringVar(&opts.AWSCredentialsFile, "aws-creds", opts.AWSCredentialsFile, "Path to an AWS credentials file (required)")
	cmd.Flags().DurationVar(&opts.ClusterGracePeriod, "cluster-grace-period", opts.ClusterGracePeriod, "How long to wait for the cluster to be deleted before forcibly destroying its infra")

	cmd.MarkFlagRequired("name")
	cmd.MarkFlagRequired("aws-creds")

	cmd.RunE = func(cmd *cobra.Command, args []string) error {
		ctx, cancel := context.WithCancel(context.Background())
		sigs := make(chan os.Signal, 1)
		signal.Notify(sigs, syscall.SIGINT)
		go func() {
			<-sigs
			cancel()
		}()

		t := time.NewTicker(5 * time.Second)
		for {
			select {
			case <-ctx.Done():
				log.Info("Cluster deletion was cancelled. If the HostedCluster resource " +
					"still exists, you can retry this command. Otherwise run the `delete infra` " +
					"command to clean up the infrastructure for the cluster using its infrastructure ID.")
				return nil
			case <-t.C:
				if err := DestroyCluster(ctx, &opts); err != nil {
					log.Error(err, "failed to destroy cluster, will retry")
				} else {
					log.Info("Successfully destroyed cluster")
					return nil
				}
			}
		}
	}

	return cmd
}

func DestroyCluster(ctx context.Context, o *DestroyOptions) error {
	c, err := crclient.New(ctrl.GetConfigOrDie(), crclient.Options{Scheme: hyperapi.Scheme})
	if err != nil {
		return fmt.Errorf("failed to create kube client: %w", err)
	}

	var hostedCluster hyperv1.HostedCluster
	if err := c.Get(ctx, types.NamespacedName{Namespace: o.Namespace, Name: o.Name}, &hostedCluster); err != nil {
		log.Info("hostedcluster not found, nothing to do", "namespace", o.Namespace, "name", o.Name)
		return nil
	}

	log.Info("Destroying cluster", "name", hostedCluster.Name, "infraID", hostedCluster.Spec.InfraID)

	controllerutil.AddFinalizer(&hostedCluster, destroyFinalizer)
	if err := c.Update(ctx, &hostedCluster); err != nil {
		return fmt.Errorf("failed to add finalizer, won't destroy: %w", err)
	}

	// Cluster deletion will be subject to a timeout so that it's possible to
	// try and tear down infra even if the cluster never finalizes; this is an
	// attempt to reduce resource leakage in such cases.
	if err := c.Delete(ctx, &hostedCluster); err != nil {
		if apierrors.IsNotFound(err) {
			log.Info("WARNING: hostedcluster was finalized before infrastructure was deleted; resources may have been leaked")
			return nil
		} else {
			return fmt.Errorf("failed to delete hostedcluster: %w", err)
		}
	}
	clusterDeleteCtx, clusterDeleteCtxCancel := context.WithTimeout(ctx, o.ClusterGracePeriod)
	defer clusterDeleteCtxCancel()
	err = wait.PollUntil(1*time.Second, func() (bool, error) {
		if err := c.Get(clusterDeleteCtx, types.NamespacedName{Namespace: o.Namespace, Name: o.Name}, &hostedCluster); err != nil {
			if apierrors.IsNotFound(err) {
				return true, nil
			}
			log.Error(err, "failed to get hostedcluster")
			return false, nil
		}
		done := len(hostedCluster.Finalizers) == 1 && hostedCluster.Finalizers[0] == destroyFinalizer
		return done, nil
	}, clusterDeleteCtx.Done())
	if err != nil {
		return fmt.Errorf("hostedcluster was never finalized: %w", err)
	}

	log.Info("Destroying infrastructure", "id", hostedCluster.Spec.InfraID)
	destroyInfraOpts := awsinfra.DestroyInfraOptions{
		AWSCredentialsFile: o.AWSCredentialsFile,
		Region:             hostedCluster.Spec.Platform.AWS.Region,
		InfraID:            hostedCluster.Spec.InfraID,
	}
	err = destroyInfraOpts.DestroyInfra(ctx)
	if err != nil {
		return fmt.Errorf("failed to destroy infrastructure: %w", err)
	}

	controllerutil.RemoveFinalizer(&hostedCluster, destroyFinalizer)
	if err := c.Update(ctx, &hostedCluster); err != nil {
		return fmt.Errorf("failed to remove finalizer: %w", err)
	}

	log.Info("Destroyed cluster and infrastructure", "name", hostedCluster.Name, "infraID", hostedCluster.Spec.InfraID)
	return nil
}
