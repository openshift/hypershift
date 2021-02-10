package cluster

import (
	"fmt"
	"io/ioutil"
	"os"
	"path/filepath"

	"github.com/spf13/cobra"

	hyperapi "openshift.io/hypershift/api"
	apifixtures "openshift.io/hypershift/api/fixtures"
)

type Options struct {
	Namespace          string
	Name               string
	ReleaseImage       string
	PullSecretFile     string
	AWSCredentialsFile string
	SSHKeyFile         string
}

func NewCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "cluster",
		Short: "Creates basic functional HostedCluster resources",
	}

	var opts Options

	cmd.Flags().StringVar(&opts.Namespace, "namespace", "clusters", "A namespace to contain the generated resources")
	cmd.Flags().StringVar(&opts.Name, "name", "example", "A name for the cluster")
	cmd.Flags().StringVar(&opts.ReleaseImage, "release-image", hyperapi.OCPReleaseImage, "The OCP release image for the cluster")
	cmd.Flags().StringVar(&opts.PullSecretFile, "pull-secret", "", "Path to a pull secret")
	cmd.Flags().StringVar(&opts.AWSCredentialsFile, "aws-creds", "", "Path to an AWS credentials file")
	cmd.Flags().StringVar(&opts.SSHKeyFile, "ssh-key", filepath.Join(os.Getenv("HOME"), ".ssh", "id_rsa.pub"), "Path to an SSH key file")

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

		example := apifixtures.ExampleOptions{
			Namespace:      opts.Namespace,
			Name:           opts.Name,
			ReleaseImage:   opts.ReleaseImage,
			PullSecret:     pullSecret,
			AWSCredentials: awsCredentials,
			SSHKey:         sshKey,
		}.Resources()

		for _, object := range example.AsObjects() {
			err := hyperapi.YamlSerializer.Encode(object, os.Stdout)
			if err != nil {
				panic(err)
			}
			fmt.Println("---")
		}
	}

	return cmd
}
