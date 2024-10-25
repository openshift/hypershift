package install

import (
	"fmt"
	"io"
	"os"

	"github.com/openshift/hypershift/cmd/version"
	hyperapi "github.com/openshift/hypershift/support/api"
	"github.com/spf13/cobra"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/util/sets"
	crclient "sigs.k8s.io/controller-runtime/pkg/client"
)

type Outputs string

const (
	OutputAll       Outputs = "all"
	OutputCRDs      Outputs = "crds"
	OutputResources Outputs = "resources"
)

var (
	RenderFormatYaml = "yaml"
	RenderFormatJson = "json"
)

var openshiftTemplateParams = TemplateParams{
	HyperShiftImage:            "OPERATOR_IMG",
	HyperShiftImageTag:         "IMAGE_TAG",
	Namespace:                  "NAMESPACE",
	HypershiftOperatorReplicas: "OPERATOR_REPLICAS",
	OIDCS3Name:                 "OIDC_S3_NAME",
	OIDCS3Region:               "OIDC_S3_REGION",
	OIDCS3CredsSecret:          "OIDC_S3_CREDS_SECRET",
	OIDCS3CredsSecretKey:       "OIDC_S3_CREDS_SECRET_KEY",
	AWSPrivateRegion:           "AWS_PRIVATE_REGION",
	AWSPrivateCredsSecret:      "AWS_PRIVATE_CREDS_SECRET",
	AWSPrivateCredsSecretKey:   "AWS_PRIVATE_CREDS_SECRET_KEY",
	ExternalDNSCredsSecret:     "EXTERNAL_DNS_CREDS_SECRET",
	ExternalDNSDomainFilter:    "EXTERNAL_DNS_DOMAIN_FILTER",
	ExternalDNSTxtOwnerID:      "EXTERNAL_DNS_TXT_OWNER_ID",
	ExternalDNSImage:           "EXTERNAL_DNS_IMAGE",
	TemplateNamespace:          true,
	TemplateParamWrapper: func(name string) string {
		return fmt.Sprintf("${%s}", name)
	},
}

func NewRenderCommand(opts *Options) *cobra.Command {
	cmd := &cobra.Command{
		Use:          "render",
		Short:        "Render HyperShift Operator manifests to stdout",
		SilenceUsage: true,
	}

	cmd.Flags().BoolVar(&opts.Template, "template", false, "Render resources and crds as an OpenShift template instead of plain manifests")
	cmd.Flags().StringVar(&opts.Format, "format", RenderFormatYaml, fmt.Sprintf("Output format for the manifests, supports %s and %s", RenderFormatYaml, RenderFormatJson))
	cmd.Flags().StringVar(&opts.OutputTypes, "outputs", string(OutputAll), fmt.Sprintf("Which manifests to output, one of %s, %s, or %s. Output CRDs separately to allow applying them first and waiting for them to be established.", OutputAll, OutputCRDs, OutputResources))
	cmd.Flags().StringVar(&opts.OutputFile, "output-file", "", "File to write the rendered manifests to. Writes to STDOUT if not specified.")
	cmd.MarkFlagsMutuallyExclusive("template", "outputs")

	cmd.RunE = func(cmd *cobra.Command, args []string) error {
		opts.ApplyDefaults()

		var err error
		if err = opts.ValidateRender(); err != nil {
			return err
		}

		var crds []crclient.Object
		var objects []crclient.Object

		if opts.Template {
			templateObject, err := openshiftTemplate(opts)
			if err != nil {
				return err
			}
			objects = []crclient.Object{templateObject}
		} else {
			crds, objects, err = hyperShiftOperatorManifests(*opts)
			if err != nil {
				return err
			}
		}

		var objectsToRender []crclient.Object
		switch Outputs(opts.OutputTypes) {
		case OutputAll:
			objectsToRender = append(crds, objects...)
		case OutputCRDs:
			objectsToRender = crds
		case OutputResources:
			objectsToRender = objects
		}
		var out io.Writer
		if opts.OutputFile != "" {
			file, err := os.Create(opts.OutputFile)
			if err != nil {
				return err
			}
			defer file.Close()
			out = file
		} else {
			out = cmd.OutOrStdout()
		}

		err = render(objectsToRender, opts.Format, out)
		if err != nil {
			return err
		}
		return nil
	}

	return cmd
}

func (o *Options) ValidateRender() error {
	if err := o.Validate(); err != nil {
		return err
	}

	if o.Format != RenderFormatYaml && o.Format != RenderFormatJson {
		return fmt.Errorf("--format must be %s or %s", RenderFormatYaml, RenderFormatJson)
	}

	outputs := sets.New(OutputAll, OutputCRDs, OutputResources)
	if !outputs.Has(Outputs(o.OutputTypes)) {
		return fmt.Errorf("--outputs must be one of %v", outputs.UnsortedList())
	}

	return nil
}

func openshiftTemplate(opts *Options) (crclient.Object, error) {
	templateParameters := []map[string]string{}
	templateParameters = append(
		templateParameters,
		map[string]string{"name": openshiftTemplateParams.HyperShiftImage, "value": fmt.Sprintf("%s:%s", version.HypershiftImageBase, version.HypershiftImageTag)},
		map[string]string{"name": openshiftTemplateParams.HypershiftOperatorReplicas, "value": string(opts.HyperShiftOperatorReplicas)},
		map[string]string{"name": openshiftTemplateParams.Namespace, "value": opts.Namespace},
	)

	// oidc S3 parameter
	if opts.OIDCStorageProviderS3BucketName != "" {
		templateParameters = append(
			templateParameters,
			map[string]string{"name": openshiftTemplateParams.OIDCS3Name},
			map[string]string{"name": openshiftTemplateParams.OIDCS3Region},
			map[string]string{"name": openshiftTemplateParams.OIDCS3CredsSecret, "value": opts.OIDCStorageProviderS3CredentialsSecret},
			map[string]string{"name": openshiftTemplateParams.OIDCS3CredsSecretKey, "value": opts.OIDCStorageProviderS3CredentialsSecretKey},
		)
	}

	// aws private credentials
	if opts.AWSPrivateCredentialsSecret != "" {
		templateParameters = append(
			templateParameters,
			map[string]string{"name": openshiftTemplateParams.AWSPrivateRegion, "value": opts.AWSPrivateRegion},
			map[string]string{"name": openshiftTemplateParams.AWSPrivateCredsSecret, "value": opts.AWSPrivateCredentialsSecret},
			map[string]string{"name": openshiftTemplateParams.AWSPrivateCredsSecretKey, "value": opts.AWSPrivateCredentialsSecretKey},
		)
	}

	// external DNS
	if opts.ExternalDNSProvider != "" && opts.ExternalDNSDomainFilter != "" && opts.ExternalDNSCredentialsSecret != "" {
		templateParameters = append(
			templateParameters,
			map[string]string{"name": openshiftTemplateParams.ExternalDNSDomainFilter, "value": opts.ExternalDNSDomainFilter},
			map[string]string{"name": openshiftTemplateParams.ExternalDNSCredsSecret, "value": opts.ExternalDNSCredentialsSecret},
		)
		if opts.ExternalDNSTxtOwnerId != "" {
			templateParameters = append(
				templateParameters,
				map[string]string{"name": openshiftTemplateParams.ExternalDNSTxtOwnerID, "value": opts.ExternalDNSTxtOwnerId},
			)
		}
	}

	// create manifests
	crds, objects, err := hyperShiftOperatorTemplateManifest(opts, openshiftTemplateParams)
	objects = append(objects, crds...)
	if err != nil {
		return nil, err
	}

	// patch those manifests, where the template parameter placeholder was not injectable with opts (e.g. type mistmatch)
	patches := []ObjectPatch{
		{Kind: "Deployment", Name: "operator", Path: []string{"spec", "replicas"}, Value: openshiftTemplateParams.TemplateParamWrapper(openshiftTemplateParams.HypershiftOperatorReplicas)},
	}
	patchedObjects, err := applyPatchesToObjects(objects, patches)
	if err != nil {
		return nil, err
	}

	// wrap into template
	template := &unstructured.Unstructured{
		Object: map[string]interface{}{
			"kind":       "Template",
			"apiVersion": "v1",
			"metadata": map[string]interface{}{
				"name": "hypershift-operator-template",
			},
			"objects":    patchedObjects,
			"parameters": templateParameters,
		},
	}
	return template, nil
}

func applyPatchesToObjects(objects []crclient.Object, patches []ObjectPatch) ([]crclient.Object, error) {
	patchedObjects := make([]crclient.Object, len(objects))
	for i, obj := range objects {
		content, err := runtime.DefaultUnstructuredConverter.ToUnstructured(obj)
		if err != nil {
			return nil, err
		}
		patchedObject := &unstructured.Unstructured{Object: content}
		for _, p := range patches {
			if p.CanBeAppliedTo(patchedObject) {
				unstructured.SetNestedField(patchedObject.Object, p.Value, p.Path...)
			}
		}
		patchedObjects[i] = patchedObject
	}
	return patchedObjects, nil
}

func render(objects []crclient.Object, format string, out io.Writer) error {
	switch format {
	case RenderFormatYaml:
		for i, object := range objects {
			err := hyperapi.YamlSerializer.Encode(object, out)
			if err != nil {
				return err
			}
			if i < len(objects)-1 {
				fmt.Fprintln(out, "---")
			}
		}
		return nil
	case RenderFormatJson:
		if len(objects) == 1 {
			err := hyperapi.JsonSerializer.Encode(objects[0], out)
			if err != nil {
				return err
			}
		} else if len(objects) > 1 {
			list := &unstructured.Unstructured{
				Object: map[string]interface{}{
					"kind":       "List",
					"apiVersion": "v1",
					"metadata":   map[string]interface{}{},
					"items":      objects,
				},
			}
			err := hyperapi.JsonSerializer.Encode(list, out)
			if err != nil {
				return err
			}
		}
		return nil
	default:
		return fmt.Errorf("unexpected format %s", format)
	}
}

type ObjectPatch struct {
	Kind  string
	Name  string
	Path  []string
	Value string
}

func (p *ObjectPatch) CanBeAppliedTo(obj crclient.Object) bool {
	if p.Kind != "" && p.Kind != obj.GetObjectKind().GroupVersionKind().Kind {
		return false
	}
	if p.Name != "" && p.Name != obj.GetName() {
		return false
	}
	return true
}
