package cluster

import (
	"context"
	"encoding/json"
	"fmt"
	"io/ioutil"
	"os"
	"os/signal"
	"strings"
	"syscall"

	"github.com/alknopfler/hypershift/api/v1alpha1"

	hyperapi "github.com/alknopfler/hypershift/api"
	apifixtures "github.com/alknopfler/hypershift/api/fixtures"
	awsinfra "github.com/alknopfler/hypershift/cmd/infra/aws"
	"github.com/alknopfler/hypershift/version"
	"github.com/spf13/cobra"
	utilrand "k8s.io/apimachinery/pkg/util/rand"

	"github.com/alknopfler/hypershift/cmd/util"
	crclient "sigs.k8s.io/controller-runtime/pkg/client"
)

type Options struct {
	Namespace          string
	Name               string
	ReleaseImage       string
	PullSecretFile     string
	AWSCredentialsFile string
	SSHKeyFile         string
	NodePoolReplicas   int32
	Render             bool
	InfraID            string
	InfrastructureJSON string
	IAMJSON            string
	InstanceType       string
	Region             string
	BaseDomain         string
	IssuerURL          string
	PublicZoneID       string
	PrivateZoneID      string
	Annotations        []string
	NetworkType        string
	FIPS               bool
	Platform           string
}

func NewCreateCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:          "cluster",
		Short:        "Creates basic functional HostedCluster resources",
		SilenceUsage: true,
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
		Namespace:          "clusters",
		Name:               "example",
		ReleaseImage:       releaseImage,
		PullSecretFile:     "",
		BaseDomain:         "example.com",
		AWSCredentialsFile: "",
		SSHKeyFile:         "",
		NodePoolReplicas:   2,
		Render:             false,
		InfrastructureJSON: "",
		Region:             "us-east-1",
		InfraID:            fmt.Sprintf("%s-%s", "example", utilrand.String(5)),
		InstanceType:       "m4.large",
		Annotations:        []string{},
		NetworkType:        string(v1alpha1.OpenShiftSDN),
		FIPS:               false,
		Platform:           "aws",
	}

	cmd.Flags().StringVar(&opts.Namespace, "namespace", opts.Namespace, "A namespace to contain the generated resources")
	cmd.Flags().StringVar(&opts.Name, "name", opts.Name, "A name for the cluster")
	cmd.Flags().StringVar(&opts.ReleaseImage, "release-image", opts.ReleaseImage, "The OCP release image for the cluster")
	cmd.Flags().StringVar(&opts.PullSecretFile, "pull-secret", opts.PullSecretFile, "Path to a pull secret (required)")
	cmd.Flags().StringVar(&opts.AWSCredentialsFile, "aws-creds", opts.AWSCredentialsFile, "Path to an AWS credentials file (required)")
	cmd.Flags().StringVar(&opts.SSHKeyFile, "ssh-key", opts.SSHKeyFile, "Path to an SSH key file")
	cmd.Flags().Int32Var(&opts.NodePoolReplicas, "node-pool-replicas", opts.NodePoolReplicas, "If >-1, create a default NodePool with this many replicas")
	cmd.Flags().BoolVar(&opts.Render, "render", opts.Render, "Render output as YAML to stdout instead of applying")
	cmd.Flags().StringVar(&opts.InfrastructureJSON, "infra-json", opts.InfrastructureJSON, "Path to file containing infrastructure information for the cluster. If not specified, infrastructure will be created")
	cmd.Flags().StringVar(&opts.IAMJSON, "iam-json", opts.IAMJSON, "Path to file containing IAM information for the cluster. If not specified, IAM will be created")
	cmd.Flags().StringVar(&opts.Region, "region", opts.Region, "Region to use for AWS infrastructure.")
	cmd.Flags().StringVar(&opts.InfraID, "infra-id", opts.InfraID, "Infrastructure ID to use for AWS resources.")
	cmd.Flags().StringVar(&opts.InstanceType, "instance-type", opts.InstanceType, "Instance type for AWS instances.")
	cmd.Flags().StringVar(&opts.BaseDomain, "base-domain", opts.BaseDomain, "The ingress base domain for the cluster")
	cmd.Flags().StringArrayVar(&opts.Annotations, "annotations", opts.Annotations, "Annotations to apply to the hostedcluster (key=value). Can be specified multiple times.")
	cmd.Flags().StringVar(&opts.NetworkType, "network-type", opts.NetworkType, "Enum specifying the cluster SDN provider. Supports either Calico or OpenshiftSDN.")
	cmd.Flags().BoolVar(&opts.FIPS, "fips", opts.FIPS, "Enables FIPS mode for nodes in the cluster")
	cmd.Flags().StringVar(&opts.Platform, "platform", opts.Platform, "The cluster platform. Supports either aws or none.")

	cmd.MarkFlagRequired("pull-secret")
	cmd.MarkFlagRequired("ssh-key")
	//cmd.MarkFlagRequired("aws-creds")
	cmd.MarkFlagRequired("platform")

	cmd.Run = func(cmd *cobra.Command, args []string) {
		ctx, cancel := context.WithCancel(context.Background())
		sigs := make(chan os.Signal, 1)
		signal.Notify(sigs, syscall.SIGINT)
		go func() {
			<-sigs
			cancel()
		}()

		if err := CreateCluster(ctx, opts); err != nil {
			log.Error(err, "Failed to create cluster")
			os.Exit(1)
		}
	}

	return cmd
}

func CreateCluster(ctx context.Context, opts Options) error {
	if len(opts.ReleaseImage) == 0 {
		return fmt.Errorf("release image is required")
	}

	annotations := map[string]string{}
	for _, s := range opts.Annotations {
		pair := strings.SplitN(s, "=", 2)
		if len(pair) != 2 {
			return fmt.Errorf("invalid annotation: %s", s)
		}
		k, v := pair[0], pair[1]
		annotations[k] = v
	}

	pullSecret, err := ioutil.ReadFile(opts.PullSecretFile)
	if err != nil {
		return fmt.Errorf("failed to read pull secret file: %w", err)
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

	// Load or create infrastructure for the cluster
	if opts.Platform == "aws" {
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
		if opts.BaseDomain == "" {
			if infra != nil {
				opts.BaseDomain = infra.BaseDomain
			} else {
				return fmt.Errorf("base-domain flag is required if infra-json is not provided")
			}
		}
		if infra == nil {
			opt := awsinfra.CreateInfraOptions{
				Region:             opts.Region,
				InfraID:            opts.InfraID,
				AWSCredentialsFile: opts.AWSCredentialsFile,
				Name:               opts.Name,
				BaseDomain:         opts.BaseDomain,
			}
			infra, err = opt.CreateInfra(ctx)
			if err != nil {
				return fmt.Errorf("failed to create infra: %w", err)
			}
		}

		var iamInfo *awsinfra.CreateIAMOutput
		if len(opts.IAMJSON) > 0 {
			rawIAM, err := ioutil.ReadFile(opts.IAMJSON)
			if err != nil {
				return fmt.Errorf("failed to read iam json file: %w", err)
			}
			iamInfo = &awsinfra.CreateIAMOutput{}
			if err = json.Unmarshal(rawIAM, iamInfo); err != nil {
				return fmt.Errorf("failed to load infra json: %w", err)
			}
		} else {
			opt := awsinfra.CreateIAMOptions{
				Region:             opts.Region,
				AWSCredentialsFile: opts.AWSCredentialsFile,
				InfraID:            infra.InfraID,
				IssuerURL:          opts.IssuerURL,
			}
			iamInfo, err = opt.CreateIAM(ctx, client)
			if err != nil {
				return fmt.Errorf("failed to create iam: %w", err)
			}
		}

		exampleObjects := apifixtures.ExampleOptions{
			Namespace:        opts.Namespace,
			Name:             infra.Name,
			Annotations:      annotations,
			ReleaseImage:     opts.ReleaseImage,
			PullSecret:       pullSecret,
			SigningKey:       iamInfo.ServiceAccountSigningKey,
			IssuerURL:        iamInfo.IssuerURL,
			SSHKey:           sshKey,
			NodePoolReplicas: opts.NodePoolReplicas,
			InfraID:          infra.InfraID,
			ComputeCIDR:      infra.ComputeCIDR,
			BaseDomain:       infra.BaseDomain,
			PublicZoneID:     infra.PublicZoneID,
			PrivateZoneID:    infra.PrivateZoneID,
			NetworkType:      v1alpha1.NetworkType(opts.NetworkType),
			FIPS:             opts.FIPS,
			PlatformType:     opts.Platform,
			AWS: apifixtures.ExampleAWSOptions{
				Region:                                 infra.Region,
				Zone:                                   infra.Zone,
				VPCID:                                  infra.VPCID,
				SubnetID:                               infra.PrivateSubnetID,
				SecurityGroupID:                        infra.SecurityGroupID,
				InstanceProfile:                        iamInfo.ProfileName,
				InstanceType:                           opts.InstanceType,
				Roles:                                  iamInfo.Roles,
				KubeCloudControllerUserAccessKeyID:     iamInfo.KubeCloudControllerUserAccessKeyID,
				KubeCloudControllerUserAccessKeySecret: iamInfo.KubeCloudControllerUserAccessKeySecret,
				NodePoolManagementUserAccessKeyID:      iamInfo.NodePoolManagementUserAccessKeyID,
				NodePoolManagementUserAccessKeySecret:  iamInfo.NodePoolManagementUserAccessKeySecret,
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
				object.SetLabels(map[string]string{util.AutoInfraLabelName: infra.InfraID})
				if err := client.Patch(ctx, object, crclient.Apply, crclient.ForceOwnership, crclient.FieldOwner("hypershift-cli")); err != nil {
					return fmt.Errorf("failed to apply object %q: %w", key, err)
				}
				log.Info("Applied Kube resource", "kind", object.GetObjectKind().GroupVersionKind().Kind, "namespace", key.Namespace, "name", key.Name)
			}
			return nil
		}
		return nil

	} else {
		//case platform none or baremetal

		exampleObjects := apifixtures.ExampleOptions{
			Namespace:        opts.Namespace,
			Name:             opts.Name,
			Annotations:      annotations,
			ReleaseImage:     opts.ReleaseImage,
			PullSecret:       pullSecret,
			IssuerURL:        "https://kubernetes.default.svc",
			SSHKey:           sshKey,
			NodePoolReplicas: 0,
			ComputeCIDR:      "10.0.0.0/16",
			PublicZoneID:     "",
			PrivateZoneID:    "",
			BaseDomain:       opts.BaseDomain,
			NetworkType:      v1alpha1.NetworkType(opts.NetworkType),
			FIPS:             opts.FIPS,
			PlatformType:     opts.Platform,
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
				object.SetLabels(map[string]string{util.AutoInfraLabelName: opts.InfraID})
				if err := client.Patch(ctx, object, crclient.Apply, crclient.ForceOwnership, crclient.FieldOwner("hypershift-cli")); err != nil {
					return fmt.Errorf("failed to apply object %q: %w", key, err)
				}
				log.Info("Applied Kube resource", "kind", object.GetObjectKind().GroupVersionKind().Kind, "namespace", key.Namespace, "name", key.Name)
			}
			return nil
		}

		return nil
	}
}
