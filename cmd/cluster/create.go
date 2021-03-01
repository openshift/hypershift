package cluster

import (
	"bytes"
	"context"
	"fmt"
	"io/ioutil"
	"os"
	"path/filepath"

	"github.com/spf13/cobra"
	"k8s.io/apimachinery/pkg/types"

	hyperapi "github.com/openshift/hypershift/api"
	apifixtures "github.com/openshift/hypershift/api/fixtures"
	"github.com/openshift/hypershift/version"

	cr "sigs.k8s.io/controller-runtime"
	crclient "sigs.k8s.io/controller-runtime/pkg/client"
)

type Options struct {
	Namespace          string
	Name               string
	ReleaseImage       string
	PullSecretFile     string
	AWSCredentialsFile string
	SSHKeyFile         string
	NodePoolReplicas   int
	Render             bool
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
		Namespace:          "clusters",
		Name:               "example",
		ReleaseImage:       releaseImage,
		PullSecretFile:     "",
		AWSCredentialsFile: "",
		SSHKeyFile:         filepath.Join(os.Getenv("HOME"), ".ssh", "id_rsa.pub"),
		NodePoolReplicas:   2,
		Render:             false,
	}

	cmd.Flags().StringVar(&opts.Namespace, "namespace", opts.Namespace, "A namespace to contain the generated resources")
	cmd.Flags().StringVar(&opts.Name, "name", opts.Name, "A name for the cluster")
	cmd.Flags().StringVar(&opts.ReleaseImage, "release-image", opts.ReleaseImage, "The OCP release image for the cluster")
	cmd.Flags().StringVar(&opts.PullSecretFile, "pull-secret", opts.PullSecretFile, "Path to a pull secret (required)")
	cmd.Flags().StringVar(&opts.AWSCredentialsFile, "aws-creds", opts.AWSCredentialsFile, "Path to an AWS credentials file (required)")
	cmd.Flags().StringVar(&opts.SSHKeyFile, "ssh-key", opts.SSHKeyFile, "Path to an SSH key file")
	cmd.Flags().IntVar(&opts.NodePoolReplicas, "node-pool-replicas", opts.NodePoolReplicas, "If >0, create a default NodePool with this many replicas")
	cmd.Flags().BoolVar(&opts.Render, "render", opts.Render, "Render output as YAML to stdout instead of applying")

	cmd.MarkFlagRequired("pull-secret")
	cmd.MarkFlagRequired("aws-creds")

	cmd.RunE = func(cmd *cobra.Command, args []string) error {
		pullSecret, err := ioutil.ReadFile(opts.PullSecretFile)
		if err != nil {
			panic(err)
		}
		awsCredentials, err := ioutil.ReadFile(opts.AWSCredentialsFile)
		if err != nil {
			panic(err)
		}
		sshKey, err := ioutil.ReadFile(opts.SSHKeyFile)
		if err != nil {
			panic(err)
		}
		if len(opts.ReleaseImage) == 0 {
			return fmt.Errorf("release-image flag is required if default can not be fetched")
		}

		exampleObjects := apifixtures.ExampleOptions{
			Namespace:        opts.Namespace,
			Name:             opts.Name,
			ReleaseImage:     opts.ReleaseImage,
			PullSecret:       pullSecret,
			AWSCredentials:   awsCredentials,
			SSHKey:           sshKey,
			NodePoolReplicas: opts.NodePoolReplicas,
		}.Resources().AsObjects()

		switch {
		case opts.Render:
			render(exampleObjects)
		default:
			err := apply(context.TODO(), exampleObjects)
			if err != nil {
				panic(err)
			}
		}

		return nil
	}

	return cmd
}

func render(objects []crclient.Object) {
	for _, object := range objects {
		err := hyperapi.YamlSerializer.Encode(object, os.Stdout)
		if err != nil {
			panic(err)
		}
		fmt.Println("---")
	}
}

func apply(ctx context.Context, objects []crclient.Object) error {
	client, err := crclient.New(cr.GetConfigOrDie(), crclient.Options{Scheme: hyperapi.Scheme})
	if err != nil {
		return fmt.Errorf("failed to create kube client: %w", err)
	}
	for _, object := range objects {
		var objectBytes bytes.Buffer
		err := hyperapi.YamlSerializer.Encode(object, &objectBytes)
		if err != nil {
			return err
		}
		err = client.Patch(ctx, object, crclient.RawPatch(types.ApplyPatchType, objectBytes.Bytes()), crclient.ForceOwnership, crclient.FieldOwner("hypershift"))
		if err != nil {
			return err
		}
		fmt.Printf("applied %s %s/%s\n", object.GetObjectKind().GroupVersionKind().Kind, object.GetNamespace(), object.GetName())
	}
	return nil
}
