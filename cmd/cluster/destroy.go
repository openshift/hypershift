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
	PreserveIAM        bool
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
		PreserveIAM:        false,
	}

	cmd.Flags().StringVar(&opts.Namespace, "namespace", opts.Namespace, "A cluster namespace")
	cmd.Flags().StringVar(&opts.Name, "name", opts.Name, "A cluster name")
	cmd.Flags().StringVar(&opts.AWSCredentialsFile, "aws-creds", opts.AWSCredentialsFile, "Path to an AWS credentials file (required)")
	cmd.Flags().BoolVar(&opts.PreserveIAM, "preserve-iam", opts.PreserveIAM, "If true, skip deleting IAM. Otherwise destroy any default generated IAM along with other infra.")

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

		return DestroyCluster(ctx, &opts)
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

	controllerutil.AddFinalizer(&hostedCluster, destroyFinalizer)
	if err := c.Update(ctx, &hostedCluster); err != nil {
		return fmt.Errorf("failed to add finalizer, won't destroy: %w", err)
	}

	log.Info("deleting hostedcluster")
	if err := c.Delete(ctx, &hostedCluster); err != nil {
		if apierrors.IsNotFound(err) {
			log.Info("WARNING: hostedcluster was finalized before infrastructure was deleted; resources may have been leaked")
			return nil
		} else {
			return fmt.Errorf("failed to delete hostedcluster: %w", err)
		}
	}
	err = wait.PollUntil(1*time.Second, func() (bool, error) {
		if err := c.Get(ctx, types.NamespacedName{Namespace: o.Namespace, Name: o.Name}, &hostedCluster); err != nil {
			log.Error(err, "failed to get hostedcluster")
			return false, nil
		}
		done := len(hostedCluster.Finalizers) == 1 && hostedCluster.Finalizers[0] == destroyFinalizer
		return done, nil
	}, ctx.Done())
	if err != nil {
		return fmt.Errorf("error while waiting for hostedcluster to be deleted")
	}

	log.Info("destroying infrastructure", "infraID", hostedCluster.Spec.InfraID)
	destroyInfraOpts := awsinfra.DestroyInfraOptions{
		Region:             hostedCluster.Spec.Platform.AWS.Region,
		InfraID:            hostedCluster.Spec.InfraID,
		AWSCredentialsFile: o.AWSCredentialsFile,
	}
	destroyInfraOpts.Run(ctx)

	if !o.PreserveIAM {
		log.Info("destroying IAM")
		destroyOpts := awsinfra.DestroyIAMOptions{
			Region:             hostedCluster.Spec.Platform.AWS.Region,
			AWSCredentialsFile: o.AWSCredentialsFile,
			ProfileName:        awsinfra.DefaultIAMName(hostedCluster.Spec.InfraID),
		}
		err := destroyOpts.DestroyIAM()
		if err != nil {
			return fmt.Errorf("failed to destroy IAM: %w", err)
		}
	}

	controllerutil.RemoveFinalizer(&hostedCluster, destroyFinalizer)
	if err := c.Update(ctx, &hostedCluster); err != nil {
		return fmt.Errorf("failed to remove finalizer: %w", err)
	}

	log.Info("successfully destroyed cluster and infrastructure")
	return nil
}
