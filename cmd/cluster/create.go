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
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	utilrand "k8s.io/apimachinery/pkg/util/rand"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"

	hyperapi "github.com/openshift/hypershift/api"
	apifixtures "github.com/openshift/hypershift/api/fixtures"
	hyperv1 "github.com/openshift/hypershift/api/v1alpha1"
	awsinfra "github.com/openshift/hypershift/cmd/infra/aws"
	"github.com/openshift/hypershift/version"

	"github.com/openshift/hypershift/cmd/util"
	crclient "sigs.k8s.io/controller-runtime/pkg/client"
)

// NoopReconcile is just a default mutation function that does nothing.
var NoopReconcile controllerutil.MutateFn = func() error { return nil }

type Options struct {
	Namespace              string
	Name                   string
	ReleaseImage           string
	PullSecretFile         string
	AWSCredentialsFile     string
	SSHKeyFile             string
	NodePoolReplicas       int
	Render                 bool
	InfraID                string
	InfrastructureJSON     string
	InstanceType           string
	Region                 string
	BaseDomain             string
	Overwrite              bool
	PreserveInfraOnFailure bool
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
		Namespace:              "clusters",
		Name:                   "example",
		ReleaseImage:           releaseImage,
		PullSecretFile:         "",
		AWSCredentialsFile:     "",
		SSHKeyFile:             "",
		NodePoolReplicas:       2,
		Render:                 false,
		InfrastructureJSON:     "",
		Region:                 "us-east-1",
		InfraID:                "",
		InstanceType:           "m4.large",
		Overwrite:              false,
		PreserveInfraOnFailure: false,
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
	cmd.Flags().StringVar(&opts.Region, "region", opts.Region, "Region to use for AWS infrastructure.")
	cmd.Flags().StringVar(&opts.InfraID, "infra-id", opts.InfraID, "Infrastructure ID to use for AWS resources.")
	cmd.Flags().StringVar(&opts.InstanceType, "instance-type", opts.InstanceType, "Instance type for AWS instances.")
	cmd.Flags().StringVar(&opts.BaseDomain, "base-domain", opts.BaseDomain, "The base domain for the cluster")
	cmd.Flags().BoolVar(&opts.Overwrite, "overwrite", opts.Overwrite, "If an existing cluster exists, overwrite it")
	cmd.Flags().BoolVar(&opts.PreserveInfraOnFailure, "preserve-infra-on-failure", opts.PreserveInfraOnFailure, "Preserve infrastructure if creation fails and is rolled back")

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
	if len(opts.ReleaseImage) == 0 {
		return fmt.Errorf("release-image flag is required if default can not be fetched")
	}

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

	client := util.GetClientOrDie()

	// If creating the cluster directly, fail early unless overwrite is specified
	// as updates aren't really part of the design intent and may not work.
	if !opts.Render {
		existingCluster := &hyperv1.HostedCluster{
			ObjectMeta: metav1.ObjectMeta{
				Namespace: opts.Namespace,
				Name:      opts.Name,
			},
		}
		err := client.Get(ctx, crclient.ObjectKeyFromObject(existingCluster), existingCluster)
		if err == nil && !opts.Overwrite {
			return fmt.Errorf("hostedcluster already exists")
		}
	}

	// Load or create infrastructure for the cluster
	var infra *awsinfra.CreateInfraOutput
	if len(opts.InfrastructureJSON) > 0 {
		// Load the specified infra spec from disk
		rawInfra, err := ioutil.ReadFile(opts.InfrastructureJSON)
		if err != nil {
			return fmt.Errorf("failed to read infra json file: %w", err)
		}
		infra = &awsinfra.CreateInfraOutput{}
		if err = json.Unmarshal(rawInfra, infra); err != nil {
			return fmt.Errorf("failed to load infra json: %w", err)
		}
	} else {
		// No infra was provided, so create it from scratch
		if len(opts.BaseDomain) == 0 {
			return fmt.Errorf("base domain is required")
		}
		infraID := opts.InfraID
		if len(infraID) == 0 {
			infraID = fmt.Sprintf("%s-%s", opts.Name, utilrand.String(5))
		}
		subdomain := fmt.Sprintf("%s-%s.%s", opts.Namespace, opts.Name, opts.BaseDomain)
		opt := awsinfra.CreateInfraOptions{
			AWSCredentialsFile: opts.AWSCredentialsFile,
			InfraID:            infraID,
			Region:             opts.Region,
			BaseDomain:         opts.BaseDomain,
			Subdomain:          subdomain,
			PreserveOnFailure:  opts.PreserveInfraOnFailure,
		}
		newInfra, err := opt.Run(ctx)
		if err != nil {
			return fmt.Errorf("failed to create infra: %w", err)
		}
		infra = newInfra
	}

	exampleObjects := apifixtures.ExampleOptions{
		Namespace:        opts.Namespace,
		Name:             opts.Name,
		ReleaseImage:     opts.ReleaseImage,
		PullSecret:       pullSecret,
		AWSCredentials:   awsCredentials,
		SigningKey:       infra.ServiceAccountSigningKey,
		IssuerURL:        infra.OIDCIssuerURL,
		SSHKey:           sshKey,
		NodePoolReplicas: opts.NodePoolReplicas,
		InfraID:          infra.InfraID,
		ComputeCIDR:      infra.ComputeCIDR,
		BaseDomain:       infra.Subdomain,
		PublicZoneID:     infra.SubdomainPublicZoneID,
		PrivateZoneID:    infra.SubdomainPrivateZoneID,
		AWS: apifixtures.ExampleAWSOptions{
			Region:          infra.Region,
			Zone:            infra.Zone,
			VPCID:           infra.VPCID,
			SubnetID:        infra.PrivateSubnetID,
			SecurityGroupID: infra.WorkerSecurityGroupID,
			InstanceProfile: infra.WorkerInstanceProfileID,
			InstanceType:    opts.InstanceType,
			Roles: []hyperv1.AWSRoleCredentials{
				{
					ARN:       infra.OIDCIngressRoleArn,
					Namespace: "openshift-ingress-operator",
					Name:      "cloud-credentials",
				},
				{
					ARN:       infra.OIDCImageRegistryRoleArn,
					Namespace: "openshift-image-registry",
					Name:      "installer-cloud-credentials",
				},
				{
					ARN:       infra.OIDCCSIDriverRoleArn,
					Namespace: "openshift-cluster-csi-drivers",
					Name:      "ebs-cloud-credentials",
				},
			},
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
		for _, object := range exampleObjects {
			key := crclient.ObjectKeyFromObject(object)
			if err := client.Patch(ctx, object, crclient.Apply, crclient.ForceOwnership, crclient.FieldOwner("hypershift-cli")); err != nil {
				return fmt.Errorf("failed to apply object %q: %w", key, err)
			}
			log.Info("Applied Kube resource", "kind", object.GetObjectKind().GroupVersionKind().Kind, "namespace", key.Namespace, "name", key.Name)
		}
		return nil
	}

	return nil
}
