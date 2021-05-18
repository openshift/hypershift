package cluster

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/aws/aws-sdk-go/service/ec2"
	"github.com/aws/aws-sdk-go/service/ec2/ec2iface"
	"github.com/aws/aws-sdk-go/service/elb"
	"github.com/aws/aws-sdk-go/service/elb/elbiface"
	"github.com/aws/aws-sdk-go/service/iam"
	"github.com/aws/aws-sdk-go/service/iam/iamiface"
	"github.com/aws/aws-sdk-go/service/route53"
	"github.com/aws/aws-sdk-go/service/route53/route53iface"
	"github.com/spf13/cobra"
	v1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/errors"
	"k8s.io/apimachinery/pkg/util/wait"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"

	hyperv1 "github.com/openshift/hypershift/api/v1alpha1"
	awsinfra "github.com/openshift/hypershift/cmd/infra/aws"
	awsutil "github.com/openshift/hypershift/cmd/infra/aws/util"
	"github.com/openshift/hypershift/cmd/util"
)

const (
	destroyFinalizer = "openshift.io/destroy-cluster"
)

type DestroyOptions struct {
	Namespace          string
	Name               string
	InfraID            string
	BaseDomain         string
	Region             string
	AWSCredentialsFile string
	PreserveIAM        bool
	ClusterGracePeriod time.Duration

	EC2Client     ec2iface.EC2API
	Route53Client route53iface.Route53API
	ELBClient     elbiface.ELBAPI
	IAMClient     iamiface.IAMAPI
}

func NewDestroyCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:          "cluster",
		Short:        "Destroys a HostedCluster and its associated infrastructure.",
		SilenceUsage: true,
	}

	opts := DestroyOptions{
		Namespace:          "clusters",
		Name:               "",
		AWSCredentialsFile: "",
		PreserveIAM:        false,
		Region:             "us-east-1",
		ClusterGracePeriod: 10 * time.Minute,
	}

	cmd.Flags().StringVar(&opts.Namespace, "namespace", opts.Namespace, "A cluster namespace")
	cmd.Flags().StringVar(&opts.Name, "name", opts.Name, "A cluster name")
	cmd.Flags().StringVar(&opts.AWSCredentialsFile, "aws-creds", opts.AWSCredentialsFile, "Path to an AWS credentials file (required)")
	cmd.Flags().BoolVar(&opts.PreserveIAM, "preserve-iam", opts.PreserveIAM, "If true, skip deleting IAM. Otherwise destroy any default generated IAM along with other infra.")
	cmd.Flags().DurationVar(&opts.ClusterGracePeriod, "cluster-grace-period", opts.ClusterGracePeriod, "How long to wait for the cluster to be deleted before forcibly destroying its infra")
	cmd.Flags().StringVar(&opts.Region, "region", opts.Region, "Cluster's region; inferred from the hosted cluster by default")
	cmd.Flags().StringVar(&opts.BaseDomain, "base-domain", opts.BaseDomain, "Cluster's base domain; inferred from the hosted cluster by default")
	cmd.Flags().StringVar(&opts.InfraID, "infra-id", opts.InfraID, "Infrastructure ID; inferred from the hosted cluster by default")

	cmd.MarkFlagRequired("name")
	cmd.MarkFlagRequired("aws-creds")

	cmd.Run = func(cmd *cobra.Command, args []string) {
		ctx, cancel := context.WithCancel(context.Background())
		sigs := make(chan os.Signal, 1)
		signal.Notify(sigs, syscall.SIGINT)
		go func() {
			<-sigs
			cancel()
		}()

		awsSession := awsutil.NewSession()
		awsConfig := awsutil.NewConfig(opts.AWSCredentialsFile, opts.Region)
		opts.EC2Client = ec2.New(awsSession, awsConfig)
		opts.ELBClient = elb.New(awsSession, awsConfig)
		opts.Route53Client = route53.New(awsSession, awsutil.NewConfig(opts.AWSCredentialsFile, "us-east-1"))
		opts.IAMClient = iam.New(awsSession, awsConfig)

		if err := DestroyCluster(ctx, &opts); err != nil {
			log.Error(err, "Failed to destroy cluster")
			os.Exit(1)
		}
	}

	return cmd
}

func DestroyCluster(ctx context.Context, o *DestroyOptions) error {
	c := util.GetClientOrDie()

	infraID := o.InfraID
	baseDomain := o.BaseDomain
	region := o.Region

	hostedClusterExists := false
	var hostedCluster hyperv1.HostedCluster
	if err := c.Get(ctx, types.NamespacedName{Namespace: o.Namespace, Name: o.Name}, &hostedCluster); err != nil {
		if apierrors.IsNotFound(err) {
			log.Info("Hosted cluster not found, destroying infrastructure from user input", "namespace", o.Namespace, "name", o.Name, "infraID", infraID, "region", region, "baseDomain", baseDomain)
		} else {
			return fmt.Errorf("failed to get hostedcluster: %w", err)
		}
	} else {
		infraID = hostedCluster.Spec.InfraID
		baseDomain = hostedCluster.Spec.DNS.BaseDomain
		region = hostedCluster.Spec.Platform.AWS.Region
		hostedClusterExists = true
		log.Info("Found hosted cluster", "namespace", hostedCluster.Namespace, "name", hostedCluster.Name, "infraID", infraID, "region", region, "baseDomain", baseDomain)
	}

	var inputErrors []error
	if len(infraID) == 0 {
		inputErrors = append(inputErrors, fmt.Errorf("infrastructure ID is required"))
	}
	if len(baseDomain) == 0 {
		inputErrors = append(inputErrors, fmt.Errorf("base domain is required"))
	}
	if len(region) == 0 {
		inputErrors = append(inputErrors, fmt.Errorf("region is required"))
	}
	if err := errors.NewAggregate(inputErrors); err != nil {
		return fmt.Errorf("required inputs are missing: %w", err)
	}

	// If the hosted cluster exists, add a finalizer, delete it, and wait for
	// the cluster to be cleaned up before destroying its infrastructure.
	if hostedClusterExists {
		controllerutil.AddFinalizer(&hostedCluster, destroyFinalizer)
		if err := c.Update(ctx, &hostedCluster); err != nil {
			if apierrors.IsNotFound(err) {
				log.Info("Hosted cluster not found, skipping finalizer update", "namespace", o.Namespace, "name", o.Name)
			} else {
				return fmt.Errorf("failed to add finalizer to hosted cluster: %w", err)
			}
		} else {
			log.Info("Updated finalizer for hosted cluster", "namespace", o.Namespace, "name", o.Name)
		}
		log.Info("Deleting hosted cluster", "namespace", o.Namespace, "name", o.Name)
		if err := c.Delete(ctx, &hostedCluster); err != nil {
			if apierrors.IsNotFound(err) {
				log.Info("Hosted not found, skipping delete", "namespace", o.Namespace, "name", o.Name)
			} else {
				return fmt.Errorf("failed to delete hostedcluster: %w", err)
			}
		}
		// Wait for the hosted cluster to have only the CLI's finalizer remaining,
		// which should indicate the cluster was successfully torn down.
		clusterDeleteCtx, clusterDeleteCtxCancel := context.WithTimeout(ctx, o.ClusterGracePeriod)
		defer clusterDeleteCtxCancel()
		err := wait.PollUntil(1*time.Second, func() (bool, error) {
			if err := c.Get(clusterDeleteCtx, types.NamespacedName{Namespace: o.Namespace, Name: o.Name}, &hostedCluster); err != nil {
				if apierrors.IsNotFound(err) {
					return true, nil
				}
				log.Error(err, "Failed to get hosted cluster", "namespace", o.Namespace, "name", o.Name)
				return false, nil
			}
			done := len(hostedCluster.Finalizers) == 1 && hostedCluster.Finalizers[0] == destroyFinalizer
			return done, nil
		}, clusterDeleteCtx.Done())
		if err != nil {
			return fmt.Errorf("hostedcluster was't finalized, aborting delete: %w", err)
		}
	}

	log.Info("Destroying infrastructure", "infraID", infraID)
	destroyInfraOpts := awsinfra.DestroyInfraOptions{
		Region:             region,
		InfraID:            infraID,
		AWSCredentialsFile: o.AWSCredentialsFile,
		Name:               o.Name,
		BaseDomain:         baseDomain,
		EC2Client:          o.EC2Client,
		Route53Client:      o.Route53Client,
		ELBClient:          o.ELBClient,
	}
	if err := destroyInfraOpts.Run(ctx); err != nil {
		return fmt.Errorf("failed to destroy infrastructure: %w", err)
	}

	if !o.PreserveIAM {
		log.Info("Destroying IAM", "infraID", infraID)
		destroyOpts := awsinfra.DestroyIAMOptions{
			Region:             region,
			AWSCredentialsFile: o.AWSCredentialsFile,
			InfraID:            infraID,
			IAMClient:          o.IAMClient,
		}
		if err := destroyOpts.Run(ctx); err != nil {
			return fmt.Errorf("failed to destroy IAM: %w", err)
		}
	}

	//clean up CLI generated secrets
	log.Info("Deleting Secrets", "namespace", o.Namespace)
	if err := c.DeleteAllOf(ctx, &v1.Secret{}, client.InNamespace(o.Namespace), client.MatchingLabels{util.AutoInfraLabelName: infraID}); err != nil {
		if apierrors.IsNotFound(err) {
			log.Info("Secrets not found based on labels, skipping delete", "namespace", o.Namespace, "labels", util.AutoInfraLabelName+infraID)
		} else {
			return fmt.Errorf("failed to clean up secrets in %s namespace: %w", o.Namespace, err)
		}
	} else {
		log.Info("Deleted CLI generated secrets")
	}

	if hostedClusterExists {
		controllerutil.RemoveFinalizer(&hostedCluster, destroyFinalizer)
		if err := c.Update(ctx, &hostedCluster); err != nil {
			if !apierrors.IsNotFound(err) {
				return fmt.Errorf("failed to remove finalizer: %w", err)
			}
		} else {
			log.Info("Finalized hosted cluster", "namespace", o.Namespace, "name", o.Name)
		}
	}

	log.Info("Successfully destroyed cluster and infrastructure", "namespace", o.Namespace, "name", o.Name, "infraID", infraID)
	return nil
}
