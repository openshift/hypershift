package render

import (
	"path"
	"strings"
	"text/template"

	corev1 "k8s.io/api/core/v1"

	"github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/assets"
	"github.com/openshift/hypershift/support/releaseinfo"
)

func RenderClusterManifests(params *ClusterParams, image *releaseinfo.ReleaseImage, pullSecret []byte, secrets *corev1.SecretList, configMaps *corev1.ConfigMapList) (map[string][]byte, error) {
	componentVersions, err := image.ComponentVersions()
	if err != nil {
		return nil, err
	}
	ctx := newClusterManifestContext(image.ComponentImages(), componentVersions, params, pullSecret, secrets, configMaps)
	ctx.setupManifests()
	return ctx.renderManifests()
}

type clusterManifestContext struct {
	*renderContext
	userManifestFiles []string
	userManifests     map[string]string
}

func newClusterManifestContext(images, versions map[string]string, params *ClusterParams, pullSecret []byte, secrets *corev1.SecretList, configMaps *corev1.ConfigMapList) *clusterManifestContext {
	ctx := &clusterManifestContext{
		renderContext: newRenderContext(params),
		userManifests: make(map[string]string),
	}
	ctx.setFuncs(template.FuncMap{
		"version":           versionFunc(versions),
		"imageFor":          imageFunc(images),
		"base64String":      base64StringEncode,
		"indent":            indent,
		"address":           cidrAddress,
		"dns":               dnsForCidr,
		"mask":              cidrMask,
		"include":           includeFileFunc(params, ctx.renderContext),
		"dataURLEncode":     dataURLEncode(params, ctx.renderContext),
		"randomString":      randomString,
		"includeData":       includeDataFunc(),
		"trimTrailingSpace": trimTrailingSpace,
		"pki":               pkiFunc(secrets, configMaps),
		"include_pki":       includePKIFunc(secrets, configMaps),
		"pullSecretBase64":  pullSecretBase64(pullSecret),
		"atleast_version":   atLeastVersionFunc(versions),
		"lessthan_version":  lessThanVersionFunc(versions),
		"ini_value":         iniValue,
	})
	return ctx
}

func (c *clusterManifestContext) setupManifests() {
	c.clusterBootstrap()
	c.userManifestsBootstrapper()
}

func (c *clusterManifestContext) clusterBootstrap() {
	manifests, err := assets.AssetDir("cluster-bootstrap")
	if err != nil {
		panic(err)
	}
	for _, m := range manifests {
		c.addUserManifestFiles("cluster-bootstrap/" + m)
	}
}

func (c *clusterManifestContext) userManifestsBootstrapper() {
	c.addManifestFiles(
		"user-manifests-bootstrapper/user-manifests-bootstrapper-rolebinding.yaml",
		"user-manifests-bootstrapper/user-manifests-bootstrapper-job.yaml",
	)
	for _, file := range c.userManifestFiles {
		data, err := c.substituteParams(c.params, file)
		if err != nil {
			panic(err.Error())
		}
		name := path.Base(file)
		params := map[string]string{
			"data": string(data),
			"name": userConfigMapName(name),
		}
		manifest, err := c.substituteParams(params, "user-manifests-bootstrapper/user-manifest-template.yaml")
		if err != nil {
			panic(err.Error())
		}
		c.addManifest("user-manifest-"+name, manifest)
	}

	for name, data := range c.userManifests {
		params := map[string]string{
			"data": data,
			"name": userConfigMapName(name),
		}
		manifest, err := c.substituteParams(params, "user-manifests-bootstrapper/user-manifest-template.yaml")
		if err != nil {
			panic(err.Error())
		}
		c.addManifest("user-manifest-"+name, manifest)
	}
}

func (c *clusterManifestContext) addUserManifestFiles(name ...string) {
	c.userManifestFiles = append(c.userManifestFiles, name...)
}

func userConfigMapName(file string) string {
	parts := strings.Split(file, ".")
	return "user-manifest-" + strings.ReplaceAll(parts[0], "_", "-")
}
