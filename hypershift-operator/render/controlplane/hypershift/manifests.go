package hypershift

import (
	"fmt"
	"path"
	"strings"
	"text/template"

	assets "openshift.io/hypershift/hypershift-operator/assets/controlplane/hypershift"
	"openshift.io/hypershift/hypershift-operator/releaseinfo"
)

// RenderClusterManifests renders manifests for a hosted control plane cluster
func RenderClusterManifests(params *ClusterParams, releaseImageInfo *releaseinfo.ReleaseImageInfo, pullSecretFile, pkiDir, outputDir string, etcd bool, vpn bool, externalOauth bool, includeRegistry bool) error {
	releaseInfo, err := releaseImageInfo.ReleaseInfo(params.OriginReleasePrefix)
	if err != nil {
		return err
	}
	ctx := newClusterManifestContext(releaseInfo.Images, releaseInfo.Versions, params, pkiDir, outputDir, vpn, pullSecretFile)
	ctx.setupManifests(etcd, vpn, externalOauth, includeRegistry)
	return ctx.renderManifests()
}

type clusterManifestContext struct {
	*renderContext
	userManifestFiles []string
	userManifests     map[string]string
}

func newClusterManifestContext(images, versions map[string]string, params interface{}, pkiDir, outputDir string, includeVPN bool, pullSecretFile string) *clusterManifestContext {
	ctx := &clusterManifestContext{
		renderContext: newRenderContext(params, outputDir),
		userManifests: make(map[string]string),
	}
	ctx.setFuncs(template.FuncMap{
		"version":           versionFunc(versions),
		"imageFor":          imageFunc(images),
		"base64String":      base64StringEncode,
		"indent":            indent,
		"address":           cidrAddress,
		"mask":              cidrMask,
		"include":           includeFileFunc(params, ctx.renderContext),
		"includeVPN":        includeVPNFunc(includeVPN),
		"dataURLEncode":     dataURLEncode(params, ctx.renderContext),
		"randomString":      randomString,
		"includeData":       includeDataFunc(),
		"trimTrailingSpace": trimTrailingSpace,
		"pki":               pkiFunc(pkiDir),
		"include_pki":       includePKIFunc(pkiDir),
		"pullSecretBase64":  pullSecretBase64(pullSecretFile),
		"atleast_version":   atLeastVersionFunc(versions),
		"lessthan_version":  lessThanVersionFunc(versions),
	})
	return ctx
}

func (c *clusterManifestContext) setupManifests(etcd bool, vpn bool, externalOauth bool, includeRegistry bool) {
	if etcd {
		c.etcd()
	}
	c.kubeAPIServer()
	c.clusterBootstrap()
	if externalOauth {
		c.oauthOpenshiftServer()
	}
	if vpn {
		c.openVPN()
	}
	if includeRegistry {
		c.registry()
	}
	c.userManifestsBootstrapper()
	c.routerProxy()
	c.machineConfigServer()
	c.ignitionConfigs()
}

func (c *clusterManifestContext) etcd() {
	c.addManifestFiles(
		"etcd/etcd-cluster-crd.yaml",
		"etcd/etcd-cluster.yaml",
		"etcd/etcd-operator-cluster-role-binding.yaml",
		"etcd/etcd-operator-cluster-role.yaml",
		"etcd/etcd-operator.yaml",
	)

}

func (c *clusterManifestContext) oauthOpenshiftServer() {
	c.addUserManifestFiles(
		"oauth-openshift/ingress-certs-secret.yaml",
	)
}

func (c *clusterManifestContext) kubeAPIServer() {
	c.addPatch(
		"kube-apiserver-deployment.yaml",
		"kube-apiserver/kube-apiserver-deployment-patch.yaml")
	c.addManifestFiles(
		"kube-apiserver/kube-apiserver-vpnclient-config.yaml",
	)
}

func (c *clusterManifestContext) registry() {
	c.addUserManifestFiles("registry/cluster-imageregistry-config.yaml")
}

func (c *clusterManifestContext) clusterBootstrap() {
	manifests, err := assets.AssetDir("cluster-bootstrap")
	if err != nil {
		panic(err.Error())
	}
	for _, m := range manifests {
		c.addUserManifestFiles("cluster-bootstrap/" + m)
	}
}

func (c *clusterManifestContext) machineConfigServer() {
	c.addManifestFiles("machine-config-server/machine-config-server-configmap.yaml")
	c.addManifestFiles("machine-config-server/machine-config-server-deployment.yaml")
	c.addManifestFiles("machine-config-server/machine-config-server-service.yaml")
}

func (c *clusterManifestContext) openVPN() {
	c.addManifestFiles(
		"openvpn/openvpn-serviceaccount.yaml",
		"openvpn/openvpn-server-deployment.yaml",
		"openvpn/openvpn-ccd-configmap.yaml",
		"openvpn/openvpn-server-configmap.yaml",
	)
	c.addUserManifestFiles(
		"openvpn/openvpn-client-deployment.yaml",
		"openvpn/openvpn-client-configmap.yaml",
	)
}

func (c *clusterManifestContext) routerProxy() {
	c.addManifestFiles(
		"router-proxy/router-proxy-deployment.yaml",
		"router-proxy/router-proxy-configmap.yaml",
		"router-proxy/router-proxy-vpnclient-configmap.yaml",
		"router-proxy/router-proxy-http-service.yaml",
		"router-proxy/router-proxy-https-service.yaml",
	)
}

func (c *clusterManifestContext) userManifestsBootstrapper() {
	c.addManifestFiles(
		"user-manifests-bootstrapper/user-manifests-bootstrapper-pod.yaml",
	)
	for _, file := range c.userManifestFiles {
		data, err := c.substituteParams(c.params, file)
		if err != nil {
			panic(err.Error())
		}
		name := path.Base(file)
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
    ignition-config: "true"
data:
  data: |-
{{ indent 4 .content }}
`

func (c *clusterManifestContext) ignitionConfigs() {
	manifests, err := assets.AssetDir("ignition-configs")
	if err != nil {
		panic(err.Error())
	}
	for _, m := range manifests {
		content, err := c.substituteParams(c.params, "ignition-configs/"+m)
		if err != nil {
			panic(err)
		}
		name := fmt.Sprintf("ignition-config-%s", strings.TrimSuffix(m, ".yaml"))
		params := map[string]string{
			"name":    name,
			"content": content,
		}
		cm, err := c.substituteParamsInString(params, ignitionConfigTemplate)
		if err != nil {
			panic(err)
		}
		c.addManifest(name+".yaml", cm)
	}
}

func (c *clusterManifestContext) addUserManifestFiles(name ...string) {
	c.userManifestFiles = append(c.userManifestFiles, name...)
}

func (c *clusterManifestContext) addUserManifest(name, content string) {
	c.userManifests[name] = content
}

func trimFirstSegment(s string) string {
	parts := strings.Split(s, ".")
	return strings.Join(parts[1:], ".")
}

func userConfigMapName(file string) string {
	parts := strings.Split(file, ".")
	return "user-manifest-" + strings.ReplaceAll(parts[0], "_", "-")
}
