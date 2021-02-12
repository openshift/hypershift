package hostedcluster

import (
	"bytes"
	"context"
	"fmt"
	"path"
	"text/template"

	"github.com/go-logr/logr"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/errors"
	"k8s.io/apimachinery/pkg/util/sets"
	"k8s.io/apimachinery/pkg/util/yaml"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"openshift.io/hypershift/hypershift-operator/controllers/hostedcluster/assets"
)

type ClusterParams struct {
	Namespace                 string `json:"namespace"`
	ControlPlaneOperatorImage string `json:"controlPlaneOperatorImage"`
}

var (
	excludeManifests = sets.NewString(
		"openshift-apiserver-service.yaml",
		"v4-0-config-system-branding.yaml",
		"oauth-server-service.yaml",
		"kube-apiserver-service.yaml",
	)
)

// renderControlPlaneManifests renders manifests for a hosted cluster
func renderControlPlaneManifests(params *ClusterParams) (map[string][]byte, error) {
	ctx := &clusterManifestContext{
		renderContext: newRenderContext(params),
		userManifests: make(map[string]string),
	}
	ctx.capi()
	ctx.autoscaler()
	ctx.controlPlaneOperator()
	return ctx.renderManifests()
}

type clusterManifestContext struct {
	*renderContext
	userManifestFiles []string
	userManifests     map[string]string
}

func (c *clusterManifestContext) controlPlaneOperator() {
	c.addManifestFiles(
		"control-plane-operator/cp-operator-serviceaccount.yaml",
		"control-plane-operator/cp-operator-role.yaml",
		"control-plane-operator/cp-operator-rolebinding.yaml",
		"control-plane-operator/cp-operator-deployment.yaml",
	)
}

func (c *clusterManifestContext) capi() {
	c.addManifestFiles(
		"capi/capa-manager-serviceaccount.yaml",
		"capi/capa-manager-clusterrole.yaml",
		"capi/capa-manager-clusterrolebinding.yaml",
		"capi/capa-manager-deployment.yaml",
		"capi/manager-serviceaccount.yaml",
		"capi/manager-clusterrole.yaml",
		"capi/manager-clusterrolebinding.yaml",
		"capi/manager-deployment.yaml",
	)
}

func (c *clusterManifestContext) autoscaler() {
	c.addManifestFiles(
		"autoscaler/serviceaccount.yaml",
		"autoscaler/role.yaml",
		"autoscaler/rolebinding.yaml",
		"autoscaler/deployment.yaml",
	)
}

func (c *clusterManifestContext) addUserManifestFiles(name ...string) {
	c.userManifestFiles = append(c.userManifestFiles, name...)
}

func (c *clusterManifestContext) addUserManifest(name, content string) {
	c.userManifests[name] = content
}

type renderContext struct {
	params        interface{}
	funcs         template.FuncMap
	manifestFiles []string
	manifests     map[string][]byte
}

func newRenderContext(params interface{}) *renderContext {
	renderContext := &renderContext{
		params:    params,
		manifests: make(map[string][]byte),
	}
	return renderContext
}

func (c *renderContext) setFuncs(f template.FuncMap) {
	c.funcs = f
}

func (c *renderContext) renderManifests() (map[string][]byte, error) {
	result := make(map[string][]byte, len(c.manifestFiles)+len(c.manifests))
	for name, b := range c.manifests {
		result[name] = b
	}
	for _, f := range c.manifestFiles {
		content, err := c.substituteParams(c.params, f)
		if err != nil {
			return nil, fmt.Errorf("cannot render %s: %w", f, err)
		}
		result[path.Base(f)] = content
	}
	return result, nil
}

func (c *renderContext) addManifestFiles(name ...string) {
	c.manifestFiles = append(c.manifestFiles, name...)
}

func (c *renderContext) addManifest(name string, content []byte) {
	c.manifests[name] = content
}

func (c *renderContext) substituteParams(data interface{}, fileName string) ([]byte, error) {
	return c.substituteParamsInBytes(data, assets.MustAsset(fileName))
}

func (c *renderContext) substituteParamsInBytes(data interface{}, content []byte) ([]byte, error) {
	out := &bytes.Buffer{}
	t := template.Must(template.New("template").Funcs(c.funcs).Parse(string(content)))
	err := t.Execute(out, data)
	if err != nil {
		return nil, err
	}
	return out.Bytes(), nil
}

func applyManifests(ctx context.Context, c client.Client, log logr.Logger, namespace string, manifests map[string][]byte) error {
	// Use server side apply for manifestss
	applyErrors := []error{}
	for manifestName, manifestBytes := range manifests {
		if excludeManifests.Has(manifestName) {
			continue
		}
		obj := &unstructured.Unstructured{}
		if err := yaml.NewYAMLOrJSONDecoder(bytes.NewReader(manifestBytes), 100).Decode(obj); err != nil {
			applyErrors = append(applyErrors, fmt.Errorf("failed to decode manifest %s: %w", manifestName, err))
		}
		obj.SetNamespace(namespace)
		err := c.Patch(ctx, obj, client.RawPatch(types.ApplyPatchType, manifestBytes), client.ForceOwnership, client.FieldOwner("hypershift-operator"))
		if err != nil {
			applyErrors = append(applyErrors, fmt.Errorf("failed to apply manifest %s: %w", manifestName, err))
		} else {
			log.Info("applied manifest", "manifest", manifestName)
		}
	}
	if errs := errors.NewAggregate(applyErrors); errs != nil {
		return fmt.Errorf("failed to apply some manifests: %w", errs)
	}
	return nil
}
