package install

import (
	"fmt"
	"os"
	"strings"

	"github.com/spf13/cobra"
	crclient "sigs.k8s.io/controller-runtime/pkg/client"
)


var helmTemplateParams = TemplateParams{
    HyperShiftImage:          ".Values.operator.image",
    HyperShiftImageTag:       ".Values.operator.imageTag",
    Namespace:                ".Release.Namespace",
    OIDCS3Name:               ".Values.oidc.s3.name",
    OIDCS3Region:             ".Values.oidc.s3.region",
    OIDCS3CredsSecret:        ".Values.oidc.s3.credsSecret",
    OIDCS3CredsSecretKey:     ".Values.oidc.s3.credsSecretKey",
    AWSPrivateRegion:         ".Values.aws.private.region",
    AWSPrivateCredsSecret:    ".Values.aws.private.credsSecret",
    AWSPrivateCredsSecretKey: ".Values.aws.private.credsSecretKey",
    ExternalDNSCredsSecret:   ".Values.externaldns.credsSecret",
    ExternalDNSDomainFilter:  ".Values.externaldns.domainFilter",
    ExternalDNSTxtOwnerID:    ".Values.externaldns.txtOwnerId",
	ExternalDNSAzureWorkloadIdentity: ".Values.externaldns.azureWorkloadIdentity",
	ExternalDNSImage:          ".Values.externaldns.image",
	RegistryOverrides:		  "registryOverrides",
	TemplateNamespace: false,
	TemplateParamWrapper: func(name string) string {
		return fmt.Sprintf("{{ %s }}", name)
    },
}

func NewHelmRenderCommand(opts *Options) *cobra.Command {
	cmd := &cobra.Command{
		Use:          "helm",
		Short:        "Generate a Helm chart for the HyperShift Operator",
		SilenceUsage: true,
	}

	cmd.Flags().StringVar(&opts.OutputFile, "output-dir", "", "File to write the rendered manifests to. Writes to STDOUT if not specified.")

	cmd.RunE = func(cmd *cobra.Command, args []string) error {
		opts.ApplyDefaults()

		crds, manifests, err := hyperShiftOperatorTemplateManifest(opts, helmTemplateParams)
		if err != nil {
			return err
		}

		if opts.OutputFile == "" {
			opts.OutputFile = "./chart"
		}
		WriteManifestsToDir(crds, fmt.Sprintf("%s/crds", opts.OutputFile))
		WriteManifestsToDir(manifests, fmt.Sprintf("%s/templates", opts.OutputFile))
		return nil
	}

	return cmd
}

func WriteManifestsToDir(manifests []crclient.Object, dir string) error {
	err := os.MkdirAll(dir, os.ModePerm)
	if err != nil {
		return err
	}

	for _, manifest := range manifests {
		file, err := os.Create(fmt.Sprintf("%s/%s-%s.yaml", dir, strings.ToLower(manifest.GetObjectKind().GroupVersionKind().Kind), manifest.GetName()))
		if err != nil {
			return err
		}
		defer file.Close()
		err = render([]crclient.Object{manifest}, RenderFormatYaml, file)
		if err != nil {
			return err
		}
	}
	return nil
}
