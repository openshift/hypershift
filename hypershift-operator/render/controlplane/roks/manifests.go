package roks

import (
	"bytes"
	"path"
	"strings"
	"text/template"

	assets "openshift.io/hypershift/hypershift-operator/assets/controlplane/roks"
	"openshift.io/hypershift/hypershift-operator/releaseinfo"
)

// RenderClusterManifests renders manifests for a hosted control plane cluster
func RenderClusterManifests(params *ClusterParams, releaseImageInfo *releaseinfo.ReleaseImageInfo, pullSecretFile, outputDir string, externalOauth, includeRegistry bool) error {
	releaseInfo, err := releaseImageInfo.ReleaseInfo(params.OriginReleasePrefix)
	if err != nil {
		return err
	}
	includeMetrics := len(params.ROKSMetricsImage) > 0
	ctx := newClusterManifestContext(releaseInfo.Images, releaseInfo.Versions, params, outputDir)
	ctx.setupManifests(externalOauth, includeRegistry, includeMetrics)
	return ctx.renderManifests()
}

type clusterManifestContext struct {
	*renderContext
	userManifestFiles []string
	userManifests     map[string]string
}

func newClusterManifestContext(images, versions map[string]string, params interface{}, outputDir string) *clusterManifestContext {
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
		"randomString":      randomString,
		"includeData":       includeDataFunc(),
		"trimTrailingSpace": trimTrailingSpace,
	})
	return ctx
}

func (c *clusterManifestContext) setupManifests(externalOauth, includeRegistry, includeMetrics bool) {
	c.kubeAPIServer()
	c.kubeControllerManager()
	c.kubeScheduler()
	c.clusterBootstrap()
	c.openshiftAPIServer()
	c.openshiftControllerManager()
	if externalOauth {
		c.oauthOpenshiftServer()
	}
	c.clusterVersionOperator()
	if includeRegistry {
		c.registry()
	}
	if includeMetrics {
		c.roksMetrics()
	}
	c.userManifestsBootstrapper()
	c.controlPlaneOperator()
}

func (c *clusterManifestContext) oauthOpenshiftServer() {
	c.addManifestFiles(
		"oauth-openshift/oauth-browser-client.yaml",
		"oauth-openshift/oauth-challenging-client.yaml",
		"oauth-openshift/oauth-server-config-configmap.yaml",
		"oauth-openshift/oauth-server-deployment.yaml",
		"oauth-openshift/oauth-server-service.yaml",
		"oauth-openshift/v4-0-config-system-branding.yaml",
		"oauth-openshift/oauth-server-sessionsecret-secret.yaml",
	)
}

func (c *clusterManifestContext) kubeAPIServer() {
	c.addManifestFiles(
		"kube-apiserver/kube-apiserver-deployment.yaml",
		"kube-apiserver/kube-apiserver-service.yaml",
		"kube-apiserver/kube-apiserver-config-configmap.yaml",
		"kube-apiserver/kube-apiserver-oauth-metadata-configmap.yaml",
	)
}

func (c *clusterManifestContext) kubeControllerManager() {
	c.addManifestFiles(
		"kube-controller-manager/kube-controller-manager-deployment.yaml",
		"kube-controller-manager/kube-controller-manager-config-configmap.yaml",
	)
}

func (c *clusterManifestContext) kubeScheduler() {
	c.addManifestFiles(
		"kube-scheduler/kube-scheduler-deployment.yaml",
		"kube-scheduler/kube-scheduler-config-configmap.yaml",
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

func (c *clusterManifestContext) openshiftAPIServer() {
	c.addManifestFiles(
		"openshift-apiserver/openshift-apiserver-deployment.yaml",
		"openshift-apiserver/openshift-apiserver-service.yaml",
		"openshift-apiserver/openshift-apiserver-config-configmap.yaml",
	)
	c.addUserManifestFiles(
		"openshift-apiserver/openshift-apiserver-user-service.yaml",
		"openshift-apiserver/openshift-apiserver-user-endpoint.yaml",
	)
	apiServices := &bytes.Buffer{}
	for _, apiService := range []string{
		"v1.apps.openshift.io",
		"v1.authorization.openshift.io",
		"v1.build.openshift.io",
		"v1.image.openshift.io",
		"v1.oauth.openshift.io",
		"v1.project.openshift.io",
		"v1.quota.openshift.io",
		"v1.route.openshift.io",
		"v1.security.openshift.io",
		"v1.template.openshift.io",
		"v1.user.openshift.io"} {

		params := map[string]string{
			"APIService":                 apiService,
			"APIServiceGroup":            trimFirstSegment(apiService),
			"OpenshiftAPIServerCABundle": c.params.(*ClusterParams).OpenshiftAPIServerCABundle,
		}
		entry, err := c.substituteParams(params, "openshift-apiserver/service-template.yaml")
		if err != nil {
			panic(err.Error())
		}
		apiServices.WriteString(entry)
	}
	c.addUserManifest("openshift-apiserver-apiservices.yaml", apiServices.String())
}

func (c *clusterManifestContext) openshiftControllerManager() {
	c.addManifestFiles(
		"openshift-controller-manager/openshift-controller-manager-deployment.yaml",
		"openshift-controller-manager/openshift-controller-manager-config-configmap.yaml",
		"openshift-controller-manager/cluster-policy-controller-deployment.yaml",
	)
	c.addUserManifestFiles(
		"openshift-controller-manager/00-openshift-controller-manager-namespace.yaml",
		"openshift-controller-manager/openshift-controller-manager-service-ca.yaml",
	)
}

func (c *clusterManifestContext) controlPlaneOperator() {
	c.addManifestFiles(
		"control-plane-operator/cp-operator-deployment.yaml",
	)
}

func (c *clusterManifestContext) clusterVersionOperator() {
	c.addManifestFiles(
		"cluster-version-operator/cluster-version-operator-deployment.yaml",
	)
}

func (c *clusterManifestContext) roksMetrics() {
	c.addUserManifestFiles(
		"roks-metrics/roks-metrics-00-namespace.yaml",
		"roks-metrics/roks-metrics-deployment.yaml",
		"roks-metrics/roks-metrics-rbac.yaml",
		"roks-metrics/roks-metrics-service.yaml",
		"roks-metrics/roks-metrics-serviceaccount.yaml",
		"roks-metrics/roks-metrics-servicemonitor.yaml",
		"roks-metrics/roks-metrics-push-gateway-deployment.yaml",
		"roks-metrics/roks-metrics-push-gateway-service.yaml",
		"roks-metrics/roks-metrics-push-gateway-servicemonitor.yaml",
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
