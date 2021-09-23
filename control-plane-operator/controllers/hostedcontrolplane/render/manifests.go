package render

import (
	"fmt"
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
	c.hostedClusterConfigOperator()
	c.clusterBootstrap()
	c.registry()
	c.operatorLifecycleManager()
	c.userManifestsBootstrapper()
	c.machineConfigServer()
	c.ignitionConfigs()
}

func (c *clusterManifestContext) hostedClusterConfigOperator() {
	c.addManifestFiles(
		"hosted-cluster-config-operator/cp-operator-serviceaccount.yaml",
		"hosted-cluster-config-operator/cp-operator-role.yaml",
		"hosted-cluster-config-operator/cp-operator-rolebinding.yaml",
		"hosted-cluster-config-operator/cp-operator-deployment.yaml",
		"hosted-cluster-config-operator/cp-operator-configmap.yaml",
	)
}

func (c *clusterManifestContext) registry() {
	c.addUserManifestFiles("registry/cluster-imageregistry-config.yaml")
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

func (c *clusterManifestContext) machineConfigServer() {
	c.addManifestFiles(
		"machine-config-server/machine-config-server-configmap.yaml",
		"machine-config-server/machine-config-server-kubeconfig-secret.yaml",
	)
}

func (c *clusterManifestContext) userManifestsBootstrapper() {
	c.addManifestFiles(
		"user-manifests-bootstrapper/user-manifests-bootstrapper-serviceaccount.yaml",
		"user-manifests-bootstrapper/user-manifests-bootstrapper-rolebinding.yaml",
		"user-manifests-bootstrapper/user-manifests-bootstrapper-pod.yaml",
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

const ignitionConfigTemplate = `apiVersion: v1
kind: ConfigMap
metadata:
  name: {{ .name }}
  labels:
    hypershift.openshift.io/core-ignition-config: "true"
data:
  config: |-
{{ indent 4 .content }}
`

func (c *clusterManifestContext) ignitionConfigs() {
	manifests, err := assets.AssetDir("ignition-configs")
	if err != nil {
		panic(err)
	}
	for _, m := range manifests {
		content, err := c.substituteParams(c.params, "ignition-configs/"+m)
		if err != nil {
			panic(err)
		}
		name := fmt.Sprintf("ignition-config-%s", strings.TrimSuffix(m, ".yaml"))
		params := map[string]string{
			"name":    name,
			"content": string(content),
		}
		cm, err := c.substituteParamsInBytes(params, []byte(ignitionConfigTemplate))
		if err != nil {
			panic(err)
		}
		c.addManifest(name+".yaml", cm)
	}
}

func (c *clusterManifestContext) operatorLifecycleManager() {
	c.addManifestFiles(
		"olm/catalog-rollout.serviceaccount.yaml",
		"olm/catalog-rollout.role.yaml",
		"olm/catalog-rollout.rolebinding.yaml",
		"olm/catalog-metrics-service.yaml",
		"olm/olm-metrics-service.yaml",
		"olm/olm-client-ca-secret.yaml",
		"olm/olm-operator-deployment.yaml",
		"olm/catalog-operator-deployment.yaml",
		"olm/packageserver-secret.yaml",
		"olm/packageserver-deployment.yaml",
		"olm/catalog-redhat-operators.deployment.yaml",
		"olm/catalog-redhat-operators-rollout.cronjob.yaml",
		"olm/catalog-redhat-operators.service.yaml",
		"olm/catalog-certified.deployment.yaml",
		"olm/catalog-certified-rollout.cronjob.yaml",
		"olm/catalog-certified.service.yaml",
		"olm/catalog-community.deployment.yaml",
		"olm/catalog-community-rollout.cronjob.yaml",
		"olm/catalog-community.service.yaml",
		"olm/catalog-redhat-marketplace.deployment.yaml",
		"olm/catalog-redhat-marketplace-rollout.cronjob.yaml",
		"olm/catalog-redhat-marketplace.service.yaml",
	)
	c.addUserManifestFiles(
		"olm/packageserver-service-guest.yaml",
		"olm/packageserver-endpoint-guest.yaml",
		"olm/catalog-certified-operators-catalogsource-guest.yaml",
		"olm/catalog-community-operators-catalogsource-guest.yaml",
		"olm/catalog-redhat-marketplace-catalogsource-guest.yaml",
		"olm/catalog-redhat-operators-catalogsource-guest.yaml",
	)

	params := map[string]string{
		"PackageServerCABundle": c.params.(*ClusterParams).PackageServerCABundle,
	}
	entry, err := c.substituteParams(params, "olm/packageserver-apiservice-template.yaml")
	if err != nil {
		panic(err.Error())
	}
	c.addUserManifest("packageserver-apiservice.yaml", string(entry))
}

func (c *clusterManifestContext) addUserManifestFiles(name ...string) {
	c.userManifestFiles = append(c.userManifestFiles, name...)
}

func (c *clusterManifestContext) addUserManifest(name, content string) {
	c.userManifests[name] = content
}

func userConfigMapName(file string) string {
	parts := strings.Split(file, ".")
	return "user-manifest-" + strings.ReplaceAll(parts[0], "_", "-")
}
