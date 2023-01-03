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

	TemplateParamHyperShiftImage                  = "OPERATOR_IMG"
	TemplateParamHyperShiftImageTag               = "IMAGE_TAG"
	TemplateParamNamespace                        = "NAMESPACE"
	TemplateParamOIDCS3Name                       = "OIDC_S3_NAME"
	TemplateParamOIDCS3Region                     = "OIDC_S3_REGION"
	TemplateParamOIDCS3CredsSecret                = "OIDC_S3_CREDS_SECRET"
	TemplateParamOIDCS3CredsSecretKey             = "OIDC_S3_CREDS_SECRET_KEY"
	TemplateParamAWSPrivateRegion                 = "AWS_PRIVATE_REGION"
	TemplateParamAWSPrivateRegionSecret           = "AWS_PRIVATE_REGION_SECRET"
	TemplateParamAWSPrivateRegionSecretKey        = "AWS_PRIVATE_REGION_SECRET_KEY"
	TemplateParamAWSPrivateCredsSecret            = "AWS_PRIVATE_CREDS_SECRET"
	TemplateParamAWSPrivateCredsSecretKey         = "AWS_PRIVATE_CREDS_SECRET_KEY"
	TemplateParamOperatorReplicas                 = "OPERATOR_REPLICAS"
	TemplateParamExternalDNSCredsSecret           = "EXTERNAL_DNS_CREDS_SECRET"
	TemplateParamExternalDNSDomainFilter          = "EXTERNAL_DNS_DOMAIN_FILTER"
	TemplateParamExternalDNSDomainFilterSecret    = "EXTERNAL_DNS_DOMAIN_FILTER_SECRET"
	TemplateParamExternalDNSDomainFilterSecretKey = "EXTERNAL_DNS_DOMAIN_FILTER_SECRET_KEY"
	TemplateParamExternalDNSTxtOwnerID            = "EXTERNAL_DNS_TXT_OWNER_ID"
	TemplateParamExternalDNSTxtOwnerIDSecret      = "EXTERNAL_DNS_TXT_OWNER_ID_SECRET"
	TemplateParamExternalDNSTxtOwnerIDSecretKey   = "EXTERNAL_DNS_TXT_OWNER_ID_SECRET_KEY"

	SSSTemplateParamEnvironment               = "SSS_ENVIRONMENT"
	SSSTemplateParamManagementClusterKey      = "SSS_MANAGEMENT_CLUSTER_KEY"
	SSSTemplateParamManagementClusterOperator = "SSS_MANAGEMENT_CLUSTER_OPERATOR"
	SSSTemplateParamManagementClusterValue    = "SSS_MANAGEMENT_CLUSTER_VALUE"
	SSSTemplateParamSectorKey                 = "SSS_SECTOR_KEY"
	SSSTemplateParamSectorOperator            = "SSS_SECTOR_OPERATOR"
	SSSTemplateParamSectorValue               = "SSS_SECTOR_VALUE"

	SSSTemplateParamDefaultEnvironment               = "integration"
	SSSTemplateParamDefaultManagementClusterKey      = "ext-hypershift.openshift.io/cluster-type"
	SSSTemplateParamDefaultManagementClusterOperator = "In"
	SSSTemplateParamDefaultManagementClusterValue    = "management-cluster"
	// since the ext-hypershift.openshift.io/sector label does not exist (yet?), we can use regions as sectors for now.
	// This is a default, so can be overriden with parameters
	// SSSTemplateParamDefaultSectorKey                 = "ext-hypershift.openshift.io/sector"
	SSSTemplateParamDefaultSectorKey      = "hive.openshift.io/cluster-region"
	SSSTemplateParamDefaultSectorOperator = "In"
	SSSTemplateParamDefaultSectorValue    = "us-east-1"
)

func NewRenderCommand(opts *Options) *cobra.Command {
	cmd := &cobra.Command{
		Use:          "render",
		Short:        "Render HyperShift Operator manifests to stdout",
		SilenceUsage: true,
	}

	cmd.Flags().BoolVar(&opts.Template, "template", false, "Render as Openshift template instead of plain manifests")
	cmd.Flags().BoolVar(&opts.SSSTemplate, "sss-template", false, "Render as a Hive SelectorSyncSet Openshift template instead of plain manifests")
	cmd.Flags().StringVar(&opts.Format, "format", RenderFormatYaml, fmt.Sprintf("Output format for the manifests, supports %s and %s", RenderFormatYaml, RenderFormatJson))

	cmd.RunE = func(cmd *cobra.Command, args []string) error {
		opts.ApplyDefaults()

		var err error
		if err = opts.ValidateRender(); err != nil {
			return err
		}

		var objects []crclient.Object

		if opts.SSSTemplate {
			templateObject, err := hyperShiftOperatorSSSTemplateManifest(opts)
			if err != nil {
				return err
			}
			objects = []crclient.Object{templateObject}
		} else if opts.Template {
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

func hyperShiftOperatorTemplateObjects(opts *Options) ([]crclient.Object, []map[string]interface{}, error) {
	templateParameters := []map[string]interface{}{}

	// image parameter
	templateParameters = append(
		templateParameters,
		map[string]interface{}{"name": TemplateParamHyperShiftImage, "value": version.HypershiftImageBase, "required": true},
		map[string]interface{}{"name": TemplateParamHyperShiftImageTag, "value": version.HypershiftImageTag, "required": true},
	)
	opts.HyperShiftImage = fmt.Sprintf("${%s}:${%s}", TemplateParamHyperShiftImage, TemplateParamHyperShiftImageTag)

	// namespace parameter
	templateParameters = append(
		templateParameters,
		map[string]interface{}{"name": TemplateParamNamespace, "value": opts.Namespace, "required": true},
	)
	opts.Namespace = fmt.Sprintf("${%s}", TemplateParamNamespace)

	// oidc S3 parameter
	if opts.OIDCStorageProviderS3BucketName != "" {
		templateParameters = append(
			templateParameters,
			map[string]interface{}{"name": TemplateParamOIDCS3Name, "required": true},
			map[string]interface{}{"name": TemplateParamOIDCS3Region, "required": true},
			map[string]interface{}{"name": TemplateParamOIDCS3CredsSecret, "value": opts.OIDCStorageProviderS3CredentialsSecret, "required": true},
			map[string]interface{}{"name": TemplateParamOIDCS3CredsSecretKey, "value": opts.OIDCStorageProviderS3CredentialsSecretKey, "required": true},
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
			map[string]interface{}{"name": TemplateParamAWSPrivateCredsSecret, "value": opts.AWSPrivateCredentialsSecret, "required": true},
			map[string]interface{}{"name": TemplateParamAWSPrivateCredsSecretKey, "value": opts.AWSPrivateCredentialsSecretKey, "required": true},
		)
		opts.AWSPrivateCredentialsSecret = fmt.Sprintf("${%s}", TemplateParamAWSPrivateCredsSecret)
		opts.AWSPrivateCredentialsSecretKey = fmt.Sprintf("${%s}", TemplateParamAWSPrivateCredsSecretKey)
		if opts.AWSPrivateRegion != "" {
			templateParameters = append(
				templateParameters,
				map[string]interface{}{"name": TemplateParamAWSPrivateRegion, "value": opts.AWSPrivateRegion, "required": true},
			)
			opts.AWSPrivateRegion = fmt.Sprintf("${%s}", TemplateParamAWSPrivateRegion)
		}
		if opts.AWSPrivateRegion != "" {
			templateParameters = append(
				templateParameters,
				map[string]interface{}{"name": TemplateParamAWSPrivateRegion, "value": opts.AWSPrivateRegion, "required": true},
			)
			opts.AWSPrivateRegion = fmt.Sprintf("${%s}", TemplateParamAWSPrivateRegion)
		}
		if opts.AWSPrivateRegionSecret != "" {
			templateParameters = append(
				templateParameters,
				map[string]interface{}{"name": TemplateParamAWSPrivateRegionSecret, "value": opts.AWSPrivateRegionSecret, "required": true},
				map[string]interface{}{"name": TemplateParamAWSPrivateRegionSecretKey, "value": opts.AWSPrivateRegionSecretKey, "required": true},
			)
			opts.AWSPrivateRegionSecret = fmt.Sprintf("${%s}", TemplateParamAWSPrivateRegionSecret)
			opts.AWSPrivateRegionSecretKey = fmt.Sprintf("${%s}", TemplateParamAWSPrivateRegionSecretKey)
		}
	}

	// external DNS
	if opts.ExternalDNSProvider != "" && (opts.ExternalDNSDomainFilter != "" || opts.ExternalDNSDomainFilterSecret != "") && opts.ExternalDNSCredentialsSecret != "" {
		templateParameters = append(
			templateParameters,
			map[string]interface{}{"name": TemplateParamExternalDNSCredsSecret, "value": opts.ExternalDNSCredentialsSecret, "required": true},
		)
		opts.ExternalDNSCredentialsSecret = fmt.Sprintf("${%s}", TemplateParamExternalDNSCredsSecret)

		if opts.ExternalDNSDomainFilter != "" {
			templateParameters = append(
				templateParameters,
				map[string]interface{}{"name": TemplateParamExternalDNSDomainFilter, "value": opts.ExternalDNSDomainFilter, "required": true},
			)
			opts.ExternalDNSDomainFilter = fmt.Sprintf("${%s}", TemplateParamExternalDNSDomainFilter)
		}
		if opts.ExternalDNSDomainFilterSecret != "" {
			templateParameters = append(
				templateParameters,
				map[string]interface{}{"name": TemplateParamExternalDNSDomainFilterSecret, "value": opts.ExternalDNSDomainFilterSecret, "required": true},
				map[string]interface{}{"name": TemplateParamExternalDNSDomainFilterSecretKey, "value": opts.ExternalDNSDomainFilterSecretKey, "required": true},
			)
			opts.ExternalDNSDomainFilterSecret = fmt.Sprintf("${%s}", TemplateParamExternalDNSDomainFilterSecret)
			opts.ExternalDNSDomainFilterSecretKey = fmt.Sprintf("${%s}", TemplateParamExternalDNSDomainFilterSecretKey)
		}

		if opts.ExternalDNSTxtOwnerId != "" {
			templateParameters = append(
				templateParameters,
				map[string]interface{}{"name": TemplateParamExternalDNSTxtOwnerID, "value": opts.ExternalDNSTxtOwnerId, "required": true},
			)
			opts.ExternalDNSTxtOwnerId = fmt.Sprintf("${%s}", TemplateParamExternalDNSTxtOwnerID)
		}
		if opts.ExternalDNSTxtOwnerIdSecret != "" {
			templateParameters = append(
				templateParameters,
				map[string]interface{}{"name": TemplateParamExternalDNSTxtOwnerIDSecret, "value": opts.ExternalDNSTxtOwnerIdSecret, "required": true},
				map[string]interface{}{"name": TemplateParamExternalDNSTxtOwnerIDSecretKey, "value": opts.ExternalDNSTxtOwnerIdSecretKey, "required": true},
			)
			opts.ExternalDNSTxtOwnerIdSecret = fmt.Sprintf("${%s}", TemplateParamExternalDNSTxtOwnerIDSecret)
			opts.ExternalDNSTxtOwnerIdSecretKey = fmt.Sprintf("${%s}", TemplateParamExternalDNSTxtOwnerIDSecretKey)
		}
	}

	// create manifests
	objects, err := hyperShiftOperatorManifests(*opts)
	if err != nil {
		return nil, nil, err
	}

	// patch those manifests, where the template parameter placeholder was not injectable with opts (e.g. type mistmatch)
	patches := []ObjectPatch{
		{Kind: "Deployment", Name: "operator", Path: []string{"spec", "replicas"}, Value: fmt.Sprintf("${{%s}}", TemplateParamOperatorReplicas)},
	}
	templateParameters = append(
		templateParameters,
		map[string]interface{}{"name": TemplateParamOperatorReplicas, "value": "1", "required": true},
	)
	patchedObjects, err := applyPatchesToObjects(objects, patches)
	if err != nil {
		return nil, nil, err
	}

	return patchedObjects, templateParameters, nil
}

func hyperShiftOperatorTemplateManifest(opts *Options) (crclient.Object, error) {
	objects, templateParameters, err := hyperShiftOperatorTemplateObjects(opts)
	if err != nil {
		return nil, err
	}
	// wrap into template
	template := &unstructured.Unstructured{
		Object: map[string]interface{}{
			"kind":       "Template",
			"apiVersion": "v1",
			"metadata": map[string]string{
				"name": "hypershift-operator-template",
			},
			"objects":    objects,
			"parameters": templateParameters,
		},
	}
	return template, nil
}

func hyperShiftOperatorSSSTemplateManifest(opts *Options) (crclient.Object, error) {
	objects, templateParameters, err := hyperShiftOperatorTemplateObjects(opts)
	if err != nil {
		return nil, err
	}

	// wrap into sss
	sss := map[string]interface{}{
		"kind":       "SelectorSyncSet",
		"apiVersion": "hive.openshift.io/v1",
		"metadata": map[string]interface{}{
			"name": fmt.Sprintf("hypershift-operator-${%s}-${%s}", SSSTemplateParamEnvironment, SSSTemplateParamSectorValue),
			"annotations": map[string]string{
				"kubernetes.io/description": "Deploys hypershift-operator on the selected environment/sector management clusters",
			},
		},
		"spec": map[string]interface{}{
			"clusterDeploymentSelector": map[string]interface{}{
				"matchLabels": map[string]interface{}{
					"api.openshift.com/managed": "true",
				},
				"matchExpressions": []map[string]interface{}{{
					"key":      "api.openshift.com/fedramp",
					"operator": "NotIn",
					"values":   "true",
				}, {
					"key":      "api.openshift.com/environment",
					"operator": "In",
					"values":   []string{fmt.Sprintf("${%s}", SSSTemplateParamEnvironment)},
				}, {
					"key":      fmt.Sprintf("${%s}", SSSTemplateParamManagementClusterKey),
					"operator": fmt.Sprintf("${%s}", SSSTemplateParamManagementClusterOperator),
					"values":   []string{fmt.Sprintf("${%s}", SSSTemplateParamManagementClusterValue)},
				}, {
					"key":      fmt.Sprintf("${%s}", SSSTemplateParamSectorKey),
					"operator": fmt.Sprintf("${%s}", SSSTemplateParamSectorOperator),
					"values":   []string{fmt.Sprintf("${%s}", SSSTemplateParamSectorValue)},
				}},
			},
			"resourceApplyMode": "Sync",
			"resources":         objects,
		},
	}

	templateParameters = append(
		templateParameters,
		map[string]interface{}{"name": SSSTemplateParamEnvironment, "value": SSSTemplateParamDefaultEnvironment, "required": true},
		map[string]interface{}{"name": SSSTemplateParamManagementClusterKey, "value": SSSTemplateParamDefaultManagementClusterKey, "required": true},
		map[string]interface{}{"name": SSSTemplateParamManagementClusterOperator, "value": SSSTemplateParamDefaultManagementClusterOperator, "required": true},
		map[string]interface{}{"name": SSSTemplateParamManagementClusterValue, "value": SSSTemplateParamDefaultManagementClusterValue, "required": true},
		map[string]interface{}{"name": SSSTemplateParamSectorKey, "value": SSSTemplateParamDefaultSectorKey, "required": true},
		map[string]interface{}{"name": SSSTemplateParamSectorOperator, "value": SSSTemplateParamDefaultSectorOperator, "required": true},
		map[string]interface{}{"name": SSSTemplateParamSectorValue, "value": SSSTemplateParamDefaultSectorValue, "required": true},
	)

	// wrap into template
	template := &unstructured.Unstructured{
		Object: map[string]interface{}{
			"kind":       "Template",
			"apiVersion": "v1",
			"metadata": map[string]interface{}{
				"name": "hypershift-operator-sss-template",
			},
			"objects":    []map[string]interface{}{sss},
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
