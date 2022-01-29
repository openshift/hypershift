package install

import (
	"fmt"
	"io"
	"strings"

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
		if err := opts.ValidateRender(); err != nil {
			return err
		}

		objects, err := hyperShiftOperatorManifests(*opts)
		if err != nil {
			return err
		}

		if opts.Template {
			templateObject, err := wrapManifestsInTemplate(objects, opts)
			if err != nil {
				return err
			}
			objects = []crclient.Object{templateObject}
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

func wrapManifestsInTemplate(objects []crclient.Object, opts *Options) (crclient.Object, error) {
	// patch objects
	patches := []ObjectPatch{
		{Kind: "Deployment", Name: "operator", Path: []string{"spec"}, Field: "replicas", Value: "${OPERATOR_REPLICAS}"},
		{Kind: "Deployment", Name: "operator", Path: []string{"spec", "template", "spec", "containers", "name=operator"}, Field: "image", Value: "${OPERATOR_IMG}:${IMAGE_TAG}"},
		{Namespace: opts.Namespace, Path: []string{"metadata"}, Field: "namespace", Value: "${NAMESPACE}"},
	}
	templateParameters := []map[string]string{
		{"name": "OPERATOR_REPLICAS", "value": "1"},
		{"name": "OPERATOR_IMG", "value": version.HypershiftImageBase},
		{"name": "IMAGE_TAG", "value": version.HypershiftImageTag},
		{"name": "NAMESPACE", "value": opts.Namespace},
	}
	if opts.OIDCStorageProviderS3BucketName != "" {
		patches = append(
			patches,
			ObjectPatch{Kind: "Secret", Name: "hypershift-operator-oidc-provider-s3-credentials", Path: []string{"stringData"}, Field: "bucket", Value: "${OIDC_S3_BUCKET}"},
			ObjectPatch{Kind: "Secret", Name: "hypershift-operator-oidc-provider-s3-credentials", Path: []string{"stringData"}, Field: "region", Value: "${OIDC_S3_REGION}"},
			ObjectPatch{Kind: "Secret", Name: "hypershift-operator-oidc-provider-s3-credentials", Path: []string{"stringData"}, Field: "credentials", Value: "[default]\naws_access_key_id = ${OIDC_S3_ACCESS_KEY_ID}\naws_secret_access_key = ${OIDC_S3_SECRET_ACCESS_KEY}\n"},
			ObjectPatch{Kind: "ConfigMap", Name: "oidc-storage-provider-s3-config", Path: []string{"data"}, Field: "name", Value: "${OIDC_S3_BUCKET}"},
			ObjectPatch{Kind: "ConfigMap", Name: "oidc-storage-provider-s3-config", Path: []string{"data"}, Field: "region", Value: "${OIDC_S3_REGION}"},
		)
		templateParameters = append(
			templateParameters,
			map[string]string{"name": "OIDC_S3_BUCKET", "value": opts.OIDCStorageProviderS3BucketName},
			map[string]string{"name": "OIDC_S3_REGION", "value": opts.OIDCStorageProviderS3Region},
			map[string]string{"name": "OIDC_S3_ACCESS_KEY_ID"},
			map[string]string{"name": "OIDC_S3_SECRET_ACCESS_KEY"},
		)
	}
	patchedObjects := make([]crclient.Object, len(objects))
	for i, obj := range objects {
		patched, err := patchObject(obj, patches)
		if err != nil {
			return nil, err
		}
		patchedObjects[i] = patched
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

func patchObject(object crclient.Object, patches []ObjectPatch) (crclient.Object, error) {
	content, err := runtime.DefaultUnstructuredConverter.ToUnstructured(object)
	if err != nil {
		return nil, err
	}
	u := &unstructured.Unstructured{Object: content}
	for _, k := range patches {
		if k.CanBeAppliedTo(object) {
			nested, found := resolveNestedMap(u.Object, k.Path)
			if !found {
				return nil, fmt.Errorf("can't resolve patch path %s", k.Path)
			}
			nested[k.Field] = k.Value
		}
	}
	return u, nil
}

func resolveNestedMap(obj map[string]interface{}, path []string) (map[string]interface{}, bool) {
	var val interface{} = obj

	for _, field := range path {
		if val == nil {
			return nil, false
		}
		if m, ok := val.(map[string]interface{}); ok {
			// current location is a map, so go one step down
			val, ok = m[field]
			if !ok {
				return nil, false
			}
		} else if list, ok := val.([]interface{}); ok {
			// current location is a list, so find the element that
			// matches field=value
			f, v, _ := splitFilterPathElement(field)
			foundInList := false
			for _, e := range list {
				if (e.(map[string]interface{}))[f] == v {
					foundInList = true
					val = e
				}
			}
			if !foundInList {
				return nil, false
			}
		} else {
			return nil, false
		}
	}
	if nestedMap, ok := val.(map[string]interface{}); ok {
		return nestedMap, true
	} else {
		return nil, true
	}
}

func splitFilterPathElement(p string) (string, string, error) {
	parts := strings.SplitN(p, "=", 2)
	if len(parts) == 1 {
		return "", "", fmt.Errorf("filter must match field=value format")
	}
	return parts[0], parts[1], nil
}

func render(objects []crclient.Object, format string, out io.Writer) error {
	switch format {
	case RenderFormatYaml:
		for i, object := range objects {
			err := hyperapi.YamlSerializer.Encode(object, out)
			if err != nil {
				return err
			}
			if i > 0 {
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
	Kind      string
	Name      string
	Namespace string
	Path      []string
	Field     string
	Value     string
}

func (p *ObjectPatch) CanBeAppliedTo(obj crclient.Object) bool {
	if p.Kind != "" && p.Kind != obj.GetObjectKind().GroupVersionKind().Kind {
		return false
	}
	if p.Name != "" && p.Name != obj.GetName() {
		return false
	}
	if p.Namespace != "" && p.Namespace != obj.GetNamespace() {
		return false
	}
	return true
}
