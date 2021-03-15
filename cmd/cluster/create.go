package cluster

import (
	"context"
	"encoding/json"
	"fmt"
	"io/ioutil"
	"os"
	"os/signal"
	"syscall"

	"github.com/spf13/cobra"
	utilrand "k8s.io/apimachinery/pkg/util/rand"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"

	hyperapi "github.com/openshift/hypershift/api"
	apifixtures "github.com/openshift/hypershift/api/fixtures"
	awsinfra "github.com/openshift/hypershift/cmd/infra/aws"
	"github.com/openshift/hypershift/version"

	cr "sigs.k8s.io/controller-runtime"
	crclient "sigs.k8s.io/controller-runtime/pkg/client"
)

// NoopReconcile is just a default mutation function that does nothing.
var NoopReconcile controllerutil.MutateFn = func() error { return nil }

type Options struct {
	Namespace                              string
	Name                                   string
	ReleaseImage                           string
	PullSecretFile                         string
	AWSCredentialsFile                     string
	SSHKeyFile                             string
	NodePoolReplicas                       int
	Render                                 bool
	InfraID                                string
	InfrastructureJSON                     string
	WorkerInstanceProfile                  string
	InstanceType                           string
	Region                                 string
	ControlPlaneServiceTypeNodePortAddress string
	ControlPlaneServiceType                string
}

func NewCreateCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "cluster",
		Short: "Creates basic functional HostedCluster resources",
	}

	var releaseImage string
	defaultVersion, err := version.LookupDefaultOCPVersion()
	if err != nil {
		fmt.Println("WARN: Unable to lookup default OCP version with error:", err)
		fmt.Println("WARN: The 'release-image' flag is required in this case.")
		releaseImage = ""
	} else {
		releaseImage = defaultVersion.PullSpec
	}

	opts := Options{
		Namespace:                              "clusters",
		Name:                                   "example",
		ReleaseImage:                           releaseImage,
		PullSecretFile:                         "",
		AWSCredentialsFile:                     "",
		SSHKeyFile:                             "",
		NodePoolReplicas:                       2,
		Render:                                 false,
		InfrastructureJSON:                     "",
		WorkerInstanceProfile:                  "",
		Region:                                 "us-east-1",
		InfraID:                                "",
		InstanceType:                           "m4.large",
		ControlPlaneServiceType:                "",
		ControlPlaneServiceTypeNodePortAddress: "",
	}

	cmd.Flags().StringVar(&opts.Namespace, "namespace", opts.Namespace, "A namespace to contain the generated resources")
	cmd.Flags().StringVar(&opts.Name, "name", opts.Name, "A name for the cluster")
	cmd.Flags().StringVar(&opts.ReleaseImage, "release-image", opts.ReleaseImage, "The OCP release image for the cluster")
	cmd.Flags().StringVar(&opts.PullSecretFile, "pull-secret", opts.PullSecretFile, "Path to a pull secret (required)")
	cmd.Flags().StringVar(&opts.AWSCredentialsFile, "aws-creds", opts.AWSCredentialsFile, "Path to an AWS credentials file (required)")
	cmd.Flags().StringVar(&opts.SSHKeyFile, "ssh-key", opts.SSHKeyFile, "Path to an SSH key file")
	cmd.Flags().IntVar(&opts.NodePoolReplicas, "node-pool-replicas", opts.NodePoolReplicas, "If >0, create a default NodePool with this many replicas")
	cmd.Flags().BoolVar(&opts.Render, "render", opts.Render, "Render output as YAML to stdout instead of applying")
	cmd.Flags().StringVar(&opts.InfrastructureJSON, "infra-json", opts.InfrastructureJSON, "Path to file containing infrastructure information for the cluster. If not specified, infrastructure will be created")
	cmd.Flags().StringVar(&opts.WorkerInstanceProfile, "instance-profile", opts.WorkerInstanceProfile, "Name of the AWS instance profile to use for workers.")
	cmd.Flags().StringVar(&opts.Region, "region", opts.Region, "Region to use for AWS infrastructure.")
	cmd.Flags().StringVar(&opts.InfraID, "infra-id", opts.InfraID, "Infrastructure ID to use for AWS resources.")
	cmd.Flags().StringVar(&opts.InstanceType, "instance-type", opts.InstanceType, "Instance type for AWS instances.")
	cmd.Flags().StringVar(&opts.ControlPlaneServiceTypeNodePortAddress, "controlplane-servicetype-nodeport-address", opts.ControlPlaneServiceTypeNodePortAddress, "Address that will expose node port traffic of the controller cluster.")
	cmd.Flags().StringVar(&opts.ControlPlaneServiceType, "controlplane-servicetype", opts.ControlPlaneServiceType, "Strategy used for exposing control plane services. Currently supports NodePort for nodePorts otherwise defaults to using LoadBalancer services.")

	cmd.MarkFlagRequired("pull-secret")
	cmd.MarkFlagRequired("aws-creds")

	cmd.RunE = func(cmd *cobra.Command, args []string) error {
		ctx, cancel := context.WithCancel(context.Background())
		sigs := make(chan os.Signal, 1)
		signal.Notify(sigs, syscall.SIGINT)
		go func() {
			<-sigs
			cancel()
		}()
		return CreateCluster(ctx, opts)
	}

	return cmd
}

func CreateCluster(ctx context.Context, opts Options) error {
	pullSecret, err := ioutil.ReadFile(opts.PullSecretFile)
	if err != nil {
		return fmt.Errorf("failed to read pull secret file: %w", err)
	}
	awsCredentials, err := ioutil.ReadFile(opts.AWSCredentialsFile)
	if err != nil {
		return fmt.Errorf("failed to read aws credentials: %w", err)
	}
	var sshKey []byte
	if len(opts.SSHKeyFile) > 0 {
		key, err := ioutil.ReadFile(opts.SSHKeyFile)
		if err != nil {
			return fmt.Errorf("failed to read ssh key file: %w", err)
		}
		sshKey = key
	}
	if len(opts.ReleaseImage) == 0 {
		return fmt.Errorf("release-image flag is required if default can not be fetched")
	}
	var infra *awsinfra.CreateInfraOutput
	if len(opts.InfrastructureJSON) > 0 {
		rawInfra, err := ioutil.ReadFile(opts.InfrastructureJSON)
		if err != nil {
			return fmt.Errorf("failed to read infra json file: %w", err)
		}
		infra = &awsinfra.CreateInfraOutput{}
		if err = json.Unmarshal(rawInfra, infra); err != nil {
			return fmt.Errorf("failed to load infra json: %w", err)
		}
	}
	if infra == nil {
		infraID := opts.InfraID
		if len(infraID) == 0 {
			infraID = fmt.Sprintf("%s-%s", opts.Name, utilrand.String(5))
		}
		opt := awsinfra.CreateInfraOptions{
			Region:             opts.Region,
			InfraID:            infraID,
			AWSCredentialsFile: opts.AWSCredentialsFile,
		}
		infra, err = opt.CreateInfra()
		if err != nil {
			return fmt.Errorf("failed to create infra: %w", err)
		}
	}

	instanceProfile := opts.WorkerInstanceProfile
	if len(instanceProfile) == 0 {
		instanceProfile = awsinfra.DefaultIAMName(infra.InfraID)
		opt := awsinfra.CreateIAMOptions{
			Region:             opts.Region,
			AWSCredentialsFile: opts.AWSCredentialsFile,
			ProfileName:        instanceProfile,
		}
		err := opt.CreateIAM()
		if err != nil {
			return fmt.Errorf("failed to create iam: %w", err)
		}
	}

	exampleObjects := apifixtures.ExampleOptions{
		Namespace:                              opts.Namespace,
		Name:                                   opts.Name,
		ReleaseImage:                           opts.ReleaseImage,
		PullSecret:                             pullSecret,
		AWSCredentials:                         awsCredentials,
		SSHKey:                                 sshKey,
		NodePoolReplicas:                       opts.NodePoolReplicas,
		InfraID:                                infra.InfraID,
		ComputeCIDR:                            infra.ComputeCIDR,
		ControlPlaneServiceTypeNodePortAddress: opts.ControlPlaneServiceTypeNodePortAddress,
		ControlPlaneServiceType:                opts.ControlPlaneServiceType,
		AWS: apifixtures.ExampleAWSOptions{
			Region:          infra.Region,
			Zone:            infra.Zone,
			VPCID:           infra.VPCID,
			SubnetID:        infra.PrivateSubnetID,
			SecurityGroupID: infra.SecurityGroupID,
			InstanceProfile: instanceProfile,
			InstanceType:    opts.InstanceType,
		},
	}.Resources().AsObjects()

	switch {
	case opts.Render:
		for _, object := range exampleObjects {
			err := hyperapi.YamlSerializer.Encode(object, os.Stdout)
			if err != nil {
				return fmt.Errorf("failed to encode objects: %w", err)
			}
			fmt.Println("---")
		}
	default:
		client, err := crclient.New(cr.GetConfigOrDie(), crclient.Options{Scheme: hyperapi.Scheme})
		if err != nil {
			return fmt.Errorf("failed to create kube client: %w", err)
		}
		for _, object := range exampleObjects {
			key := crclient.ObjectKeyFromObject(object)
			_, err = controllerutil.CreateOrUpdate(ctx, client, object, NoopReconcile)
			if err != nil {
				return fmt.Errorf("failed to create object %q: %w", key, err)
			}
			log.Info("applied resource", "key", key)
		}
		return nil
	}

	return nil
}
