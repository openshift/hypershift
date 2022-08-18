package install

import (
	"fmt"
	"io"

	hyperapi "github.com/openshift/hypershift/api"
	"github.com/openshift/hypershift/cmd/version"
	"github.com/spf13/cobra"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	crclient "sigs.k8s.io/controller-runtime/pkg/client"
)

var (
	RenderFormatYaml = "yaml"
	RenderFormatJson = "json"

	TemplateParamHyperShiftImage          = "OPERATOR_IMG"
	TemplateParamHyperShiftImageTag       = "IMAGE_TAG"
	TemplateParamNamespace                = "NAMESPACE"
	TemplateParamOIDCS3Name               = "OIDC_S3_NAME"
	TemplateParamOIDCS3Region             = "OIDC_S3_REGION"
	TemplateParamOIDCS3CredsSecret        = "OIDC_S3_CREDS_SECRET"
	TemplateParamOIDCS3CredsSecretKey     = "OIDC_S3_CREDS_SECRET_KEY"
	TemplateParamAWSPrivateRegion         = "AWS_PRIVATE_REGION"
	TemplateParamAWSPrivateCredsSecret    = "AWS_PRIVATE_CREDS_SECRET"
	TemplateParamAWSPrivateCredsSecretKey = "AWS_PRIVATE_CREDS_SECRET_KEY"
	TemplateParamOperatorReplicas         = "OPERATOR_REPLICAS"
	TemplateParamExternalDNSCredsSecret   = "EXTERNAL_DNS_CREDS_SECRET"
	TemplateParamExternalDNSDomainFilter  = "EXTERNAL_DNS_DOMAIN_FILTER"
	TemplateParamExternalDNSTxtOwnerID    = "EXTERNAL_DNS_TXT_OWNER_ID"
)

func NewRenderCommand(opts *Options) *cobra.Command {
	cmd := &cobra.Command{
		Use:          "render",
		Short:        "Render HyperShift Operator manifests to stdout",
		SilenceUsage: true,
	}

	cmd.Flags().BoolVar(&opts.Template, "template", false, "Render as Openshift template instead of plain manifests")
	cmd.Flags().StringVar(&opts.Format, "format", RenderFormatYaml, fmt.Sprintf("Output format for the manifests, supports %s and %s", RenderFormatYaml, RenderFormatJson))

	cmd.RunE = func(cmd *cobra.Command, args []string) error {
		opts.ApplyDefaults()

		var err error
		if err = opts.ValidateRender(); err != nil {
			return err
		}

		var objects []crclient.Object

		if opts.Template {
			templateObject, err := hyperShiftOperatorTemplateManifest(opts)
			if err != nil {
				return err
			}
			objects = []crclient.Object{templateObject}
		} else {
			objects, err = hyperShiftOperatorManifests(*opts)
			if err != nil {
				return err
			}
		}

		err = render(objects, opts.Format, cmd.OutOrStdout())
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

	return nil
}

func hyperShiftOperatorTemplateManifest(opts *Options) (crclient.Object, error) {
	templateParameters := []map[string]string{}

	// image parameter
	templateParameters = append(
		templateParameters,
		map[string]string{"name": TemplateParamHyperShiftImage, "value": version.HypershiftImageBase},
		map[string]string{"name": TemplateParamHyperShiftImageTag, "value": version.HypershiftImageTag},
	)
	opts.HyperShiftImage = fmt.Sprintf("${%s}:${%s}", TemplateParamHyperShiftImage, TemplateParamHyperShiftImageTag)

	// namespace parameter
	templateParameters = append(
		templateParameters,
		map[string]string{"name": TemplateParamNamespace, "value": opts.Namespace},
	)
	opts.Namespace = fmt.Sprintf("${%s}", TemplateParamNamespace)

	// oidc S3 parameter
	if opts.OIDCStorageProviderS3BucketName != "" {
		templateParameters = append(
			templateParameters,
			map[string]string{"name": TemplateParamOIDCS3Name},
			map[string]string{"name": TemplateParamOIDCS3Region},
			map[string]string{"name": TemplateParamOIDCS3CredsSecret, "value": opts.OIDCStorageProviderS3CredentialsSecret},
			map[string]string{"name": TemplateParamOIDCS3CredsSecretKey, "value": opts.OIDCStorageProviderS3CredentialsSecretKey},
		)
		opts.OIDCStorageProviderS3BucketName = fmt.Sprintf("${%s}", TemplateParamOIDCS3Name)
		opts.OIDCStorageProviderS3Region = fmt.Sprintf("${%s}", TemplateParamOIDCS3Region)
		opts.OIDCStorageProviderS3CredentialsSecret = fmt.Sprintf("${%s}", TemplateParamOIDCS3CredsSecret)
		opts.OIDCStorageProviderS3CredentialsSecretKey = fmt.Sprintf("${%s}", TemplateParamOIDCS3CredsSecretKey)
	}

	// aws private credentials
	if opts.AWSPrivateCredentialsSecret != "" {
		templateParameters = append(
			templateParameters,
			map[string]string{"name": TemplateParamAWSPrivateRegion, "value": opts.AWSPrivateRegion},
			map[string]string{"name": TemplateParamAWSPrivateCredsSecret, "value": opts.AWSPrivateCredentialsSecret},
			map[string]string{"name": TemplateParamAWSPrivateCredsSecretKey, "value": opts.AWSPrivateCredentialsSecretKey},
		)
		opts.AWSPrivateRegion = fmt.Sprintf("${%s}", TemplateParamAWSPrivateRegion)
		opts.AWSPrivateCredentialsSecret = fmt.Sprintf("${%s}", TemplateParamAWSPrivateCredsSecret)
		opts.AWSPrivateCredentialsSecretKey = fmt.Sprintf("${%s}", TemplateParamAWSPrivateCredsSecretKey)
	}

	// external DNS
	if opts.ExternalDNSProvider != "" && opts.ExternalDNSDomainFilter != "" && opts.ExternalDNSCredentialsSecret != "" {
		templateParameters = append(
			templateParameters,
			map[string]string{"name": TemplateParamExternalDNSDomainFilter, "value": opts.ExternalDNSDomainFilter},
			map[string]string{"name": TemplateParamExternalDNSCredsSecret, "value": opts.ExternalDNSCredentialsSecret},
		)
		opts.ExternalDNSDomainFilter = fmt.Sprintf("${%s}", TemplateParamExternalDNSDomainFilter)
		opts.ExternalDNSCredentialsSecret = fmt.Sprintf("${%s}", TemplateParamExternalDNSCredsSecret)

		if opts.ExternalDNSTxtOwnerId != "" {
			templateParameters = append(
				templateParameters,
				map[string]string{"name": TemplateParamExternalDNSTxtOwnerID, "value": opts.ExternalDNSTxtOwnerId},
			)
			opts.ExternalDNSTxtOwnerId = fmt.Sprintf("${%s}", TemplateParamExternalDNSTxtOwnerID)
		}

	}

	// create manifests
	objects, err := hyperShiftOperatorManifests(*opts)
	if err != nil {
		return nil, err
	}

	// patch those manifests, where the template parameter placeholder was not injectable with opts (e.g. type mistmatch)
	patches := []ObjectPatch{
		{Kind: "Deployment", Name: "operator", Path: []string{"spec", "replicas"}, Value: fmt.Sprintf("${{%s}}", TemplateParamOperatorReplicas)},
	}
	templateParameters = append(
		templateParameters,
		map[string]string{"name": TemplateParamOperatorReplicas, "value": "1"},
	)
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
