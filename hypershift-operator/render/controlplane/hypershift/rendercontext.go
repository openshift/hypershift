package hypershift

import (
	"bytes"
	"fmt"
	"io/ioutil"
	"os"
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
	patches       map[string][]string
}

func newRenderContext(params interface{}, outputDir string) *renderContext {
	renderContext := &renderContext{
		params:    params,
		outputDir: outputDir,
		manifests: make(map[string]string),
		patches:   make(map[string][]string),
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

	for name, patches := range c.patches {
		sourceFile := filepath.Join(c.outputDir, name)
		outputFile := sourceFile + ".patched"
		for _, p := range patches {
			sourceBytes, err := ioutil.ReadFile(sourceFile)
			if err != nil {
				return err
			}
			patchBytes := assets.MustAsset(p)
			patch := mustDecodePatch(patchBytes)
			out, err := patch.Apply(sourceBytes)
			if err != nil {
				return err
			}
			if err = ioutil.WriteFile(outputFile, out, 0644); err != nil {
				return err
			}
			if err = os.Remove(sourceFile); err != nil {
				return err
			}
			if err = os.Rename(outputFile, sourceFile); err != nil {
				return err
			}
		}
	}

	return nil
}

func (c *renderContext) addManifestFiles(name ...string) {
	c.manifestFiles = append(c.manifestFiles, name...)
}

func (c *renderContext) addPatch(name, patch string) {
	c.patches[name] = append(c.patches[name], patch)
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
