package hypershift

import (
	"bytes"
	"fmt"
	"io/ioutil"
	"path"
	"path/filepath"
	"text/template"

	yamlpatch "github.com/krishicks/yaml-patch"
	"github.com/pkg/errors"

	assets "openshift.io/hypershift/hypershift-operator/assets/controlplane/hypershift"
)

type renderContext struct {
	outputDir     string
	params        interface{}
	funcs         template.FuncMap
	manifestFiles []string
	manifests     map[string]string
}

func newRenderContext(params interface{}, outputDir string) *renderContext {
	renderContext := &renderContext{
		params:    params,
		outputDir: outputDir,
		manifests: make(map[string]string),
	}
	return renderContext
}

func (c *renderContext) setFuncs(f template.FuncMap) {
	c.funcs = f
}

func (c *renderContext) renderManifests() error {
	for _, f := range c.manifestFiles {
		outputFile := filepath.Join(c.outputDir, path.Base(f))
		content, err := c.substituteParams(c.params, f)
		if err != nil {
			return errors.Wrapf(err, "cannot render %s", f)
		}
		ioutil.WriteFile(outputFile, []byte(content), 0644)
	}

	for name, content := range c.manifests {
		outputFile := filepath.Join(c.outputDir, name)
		ioutil.WriteFile(outputFile, []byte(content), 0644)
	}

	return nil
}

func (c *renderContext) addManifestFiles(name ...string) {
	c.manifestFiles = append(c.manifestFiles, name...)
}

func (c *renderContext) addManifest(name, content string) {
	c.manifests[name] = content
}

func (c *renderContext) substituteParams(data interface{}, fileName string) (string, error) {
	asset := assets.MustAsset(fileName)
	return c.substituteParamsInString(data, string(asset))
}

func (c *renderContext) substituteParamsInString(data interface{}, fileContent string) (string, error) {
	out := &bytes.Buffer{}
	t := template.Must(template.New("template").Funcs(c.funcs).Parse(fileContent))
	err := t.Execute(out, data)
	if err != nil {
		panic(err.Error())
	}
	return out.String(), nil
}

func mustDecodePatch(b []byte) yamlpatch.Patch {
	p, err := yamlpatch.DecodePatch(b)
	if err != nil {
		panic(fmt.Sprintf("Cannot decode patch %s: %v", string(b), err))
	}
	return p
}
