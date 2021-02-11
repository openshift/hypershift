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

	hyperapi "openshift.io/hypershift/api"
	apifixtures "openshift.io/hypershift/api/fixtures"
	"openshift.io/hypershift/version"

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
	Render             bool
}

func NewCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "cluster",
		Short: "Creates basic functional HostedCluster resources",
	}

	var opts Options

	cmd.Flags().StringVar(&opts.Namespace, "namespace", "clusters", "A namespace to contain the generated resources")
	cmd.Flags().StringVar(&opts.Name, "name", "example", "A name for the cluster")
	cmd.Flags().StringVar(&opts.ReleaseImage, "release-image", "", "The OCP release image for the cluster")
	cmd.Flags().StringVar(&opts.PullSecretFile, "pull-secret", "", "Path to a pull secret")
	cmd.Flags().StringVar(&opts.AWSCredentialsFile, "aws-creds", "", "Path to an AWS credentials file")
	cmd.Flags().StringVar(&opts.SSHKeyFile, "ssh-key", filepath.Join(os.Getenv("HOME"), ".ssh", "id_rsa.pub"), "Path to an SSH key file")
	cmd.Flags().BoolVar(&opts.Render, "render", false, "Render output as YAML to stdout instead of applying")

	cmd.Run = func(cmd *cobra.Command, args []string) {
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
			defaultVersion, err := version.LookupDefaultOCPVersion()
			if err != nil {
				panic(err)
			}
			opts.ReleaseImage = defaultVersion.PullSpec
			fmt.Printf("using default OCP version %s\n", opts.ReleaseImage)
		}

		exampleObjects := apifixtures.ExampleOptions{
			Namespace:      opts.Namespace,
			Name:           opts.Name,
			ReleaseImage:   opts.ReleaseImage,
			PullSecret:     pullSecret,
			AWSCredentials: awsCredentials,
			SSHKey:         sshKey,
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
