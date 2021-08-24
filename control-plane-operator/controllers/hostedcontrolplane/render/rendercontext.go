package render

import (
	"bytes"
	"path"
	"text/template"

	"github.com/pkg/errors"

	"github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/assets"
)

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
			return nil, errors.Wrapf(err, "cannot render %s", f)
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
