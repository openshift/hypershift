package install

import (
	"fmt"
	"os"
	"strings"

	"github.com/openshift/hypershift/pkg/version"
	"github.com/spf13/cobra"
	"gopkg.in/yaml.v2"
	crclient "sigs.k8s.io/controller-runtime/pkg/client"
)

var helmTemplateParams = TemplateParams{
	Namespace:                ".Release.Namespace",
	HyperShiftImage:          ".Values.image",
	HyperShiftImageTag:       ".Values.imagetag",
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
	ExternalDNSImage:         ".Values.externaldns.image",
	TemplateNamespace:        false,
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

	cmd.Flags().StringVar(&opts.OutputFile, "output-dir", "", "Directory to write the rendered helm chart to")

	cmd.RunE = func(cmd *cobra.Command, args []string) error {
		opts.ApplyDefaults()

		crds, manifests, err := hyperShiftOperatorTemplateManifest(opts, helmTemplateParams)
		if err != nil {
			return err
		}

		if opts.OutputFile == "" {
			opts.OutputFile = "./chart"
		}
		err = writeManifestsToDir(crds, fmt.Sprintf("%s/crds", opts.OutputFile))
		if err != nil {
			return err
		}
		err = writeManifestsToDir(manifests, fmt.Sprintf("%s/templates", opts.OutputFile))
		if err != nil {
			return err
		}
		err = WriteChartYaml(opts.OutputFile)
		if err != nil {
			return err
		}
		err = WriteValuesFile(opts.OutputFile)
		if err != nil {
			return err
		}
		return nil
	}

	return cmd
}

func WriteChartYaml(dir string) error {
	data := map[string]interface{}{
		"apiVersion":  "v2",
		"name":        "hypershift-operator",
		"description": "A Helm chart for the HyperShift Operator",
		"type":        "application",
		"version":     "0.1.0",
		"appVersion":  version.GetRevision(),
	}
	return writeYamlFile(fmt.Sprintf("%s/Chart.yaml", dir), data)
}

func WriteValuesFile(dir string) error {
	data := map[string]interface{}{
		"image":             "",
		"imagetag":          "",
		"registryOverrides": "",
		"azure": map[string]interface{}{
			"keyVault": map[string]interface{}{
				"clientId": "",
			},
		},
		"oidc": map[string]interface{}{
			"s3": map[string]interface{}{
				"name":           "",
				"region":         "",
				"credsSecret":    "",
				"credsSecretKey": "",
			},
		},
		"aws": map[string]interface{}{
			"private": map[string]interface{}{
				"region":         "",
				"credsSecret":    "",
				"credsSecretKey": "",
			},
		},
	}
	return writeYamlFile(fmt.Sprintf("%s/values.yaml", dir), data)
}

func writeYamlFile(path string, data map[string]interface{}) error {
	yamlData, err := yaml.Marshal(data)
	if err != nil {
		return err
	}
	file, err := os.Create(path)
	if err != nil {
		return err
	}
	defer file.Close()
	_, err = file.Write(yamlData)
	if err != nil {
		return err
	}
	return nil
}

func writeManifestsToDir(manifests []crclient.Object, dir string) error {
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
