package render

import (
	"bytes"
	"fmt"
	"path"
	"strings"
	"text/template"

	"github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/assets"
	"github.com/openshift/hypershift/control-plane-operator/releaseinfo"
)

func RenderClusterManifests(params *ClusterParams, image *releaseinfo.ReleaseImage, pullSecret []byte, pki map[string][]byte) (map[string][]byte, error) {
	componentVersions, err := image.ComponentVersions()
	if err != nil {
		return nil, err
	}
	ctx := newClusterManifestContext(image.ComponentImages(), componentVersions, params, pullSecret, pki)
	ctx.setupManifests()
	return ctx.renderManifests()
}

type clusterManifestContext struct {
	*renderContext
	userManifestFiles []string
	userManifests     map[string]string
}

func newClusterManifestContext(images, versions map[string]string, params interface{}, pullSecret []byte, pki map[string][]byte) *clusterManifestContext {
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
		"mask":              cidrMask,
		"include":           includeFileFunc(params, ctx.renderContext),
		"includeVPN":        includeVPNFunc(true),
		"dataURLEncode":     dataURLEncode(params, ctx.renderContext),
		"randomString":      randomString,
		"includeData":       includeDataFunc(),
		"trimTrailingSpace": trimTrailingSpace,
		"pki":               pkiFunc(pki),
		"include_pki":       includePKIFunc(pki),
		"pullSecretBase64":  pullSecretBase64(pullSecret),
		"atleast_version":   atLeastVersionFunc(versions),
		"lessthan_version":  lessThanVersionFunc(versions),
		"ini_value":         iniValue,
	})
	return ctx
}

func (c *clusterManifestContext) setupManifests() {
	c.hostedClusterConfigOperator()
	c.serviceAdminKubeconfig()
	c.etcd()
	c.kubeControllerManager()
	c.kubeScheduler()
	c.clusterVersionOperator()
	c.openshiftAPIServer()
	c.oauthAPIServer()
	c.openshiftControllerManager()
	c.clusterBootstrap()
	c.oauthOpenshiftServer()
	c.openVPN()
	c.registry()
	c.userManifestsBootstrapper()
	c.routerProxy()
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

func (c *clusterManifestContext) serviceAdminKubeconfig() {
	c.addManifestFiles(
		"common/service-network-admin-kubeconfig-secret.yaml",
	)
}

func (c *clusterManifestContext) etcd() {
	c.addManifestFiles(
		"etcd/etcd-cluster-crd.yaml",
		"etcd/etcd-cluster.yaml",
		"etcd/etcd-operator-cluster-role-binding.yaml",
		"etcd/etcd-operator-cluster-role.yaml",
		"etcd/etcd-operator-serviceaccount.yaml",
		"etcd/etcd-operator.yaml",
	)

	for _, secret := range []string{"etcd-client", "server", "peer"} {
		file := secret
		if file != "etcd-client" {
			file = "etcd-" + secret
		}
		params := map[string]string{
			"secret": secret,
			"file":   file,
		}
		content, err := c.substituteParams(params, "etcd/etcd-secret-template.yaml")
		if err != nil {
			panic(err.Error())
		}
		c.addManifest(file+"-tls-secret.yaml", content)
	}
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
		"oauth-openshift/oauth-server-secret.yaml",
		"oauth-openshift/oauth-server-configmap.yaml",
	)
	c.addUserManifestFiles(
		"oauth-openshift/ingress-certs-secret.yaml",
	)
}

func (c *clusterManifestContext) kubeControllerManager() {
	c.addManifestFiles(
		"kube-controller-manager/kube-controller-manager-deployment.yaml",
		"kube-controller-manager/kube-controller-manager-config-configmap.yaml",
		"kube-controller-manager/kube-controller-manager-secret.yaml",
		"kube-controller-manager/kube-controller-manager-configmap.yaml",
	)
}

func (c *clusterManifestContext) kubeScheduler() {
	c.addManifestFiles(
		"kube-scheduler/kube-scheduler-deployment.yaml",
		"kube-scheduler/kube-scheduler-config-configmap.yaml",
		"kube-scheduler/kube-scheduler-secret.yaml",
	)
}

func (c *clusterManifestContext) openshiftAPIServer() {
	c.addManifestFiles(
		"openshift-apiserver/openshift-apiserver-deployment.yaml",
		"openshift-apiserver/openshift-apiserver-service.yaml",
		"openshift-apiserver/openshift-apiserver-config-configmap.yaml",
		"openshift-apiserver/openshift-apiserver-secret.yaml",
		"openshift-apiserver/openshift-apiserver-configmap.yaml",
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
		"v1.project.openshift.io",
		"v1.quota.openshift.io",
		"v1.route.openshift.io",
		"v1.security.openshift.io",
		"v1.template.openshift.io"} {
		params := map[string]string{
			"APIService":                 apiService,
			"APIServiceGroup":            trimFirstSegment(apiService),
			"OpenshiftAPIServerCABundle": c.params.(*ClusterParams).OpenshiftAPIServerCABundle,
		}
		entry, err := c.substituteParams(params, "openshift-apiserver/service-template.yaml")
		if err != nil {
			panic(err.Error())
		}
		apiServices.Write(entry)
	}
	c.addUserManifest("openshift-apiserver-apiservices.yaml", apiServices.String())
}

func (c *clusterManifestContext) oauthAPIServer() {
	c.addManifestFiles(
		"oauth-apiserver/oauth-apiserver-deployment.yaml",
		"oauth-apiserver/oauth-apiserver-auditpolicy.yaml",
		"oauth-apiserver/oauth-apiserver-secret.yaml",
		"oauth-apiserver/oauth-apiserver-configmap.yaml",
	)
	c.addUserManifestFiles(
		"oauth-apiserver/oauth-apiserver-user-service.yaml",
		"oauth-apiserver/oauth-apiserver-user-endpoint.yaml",
	)
	apiServices := &bytes.Buffer{}
	for _, apiService := range []string{
		"v1.oauth.openshift.io",
		"v1.user.openshift.io"} {

		params := map[string]string{
			"APIService":             apiService,
			"APIServiceGroup":        trimFirstSegment(apiService),
			"OauthAPIServerCABundle": c.params.(*ClusterParams).OauthAPIServerCABundle,
		}
		entry, err := c.substituteParams(params, "oauth-apiserver/service-template.yaml")
		if err != nil {
			panic(err.Error())
		}
		apiServices.Write(entry)
	}
	c.addUserManifest("oauth-apiserver-apiservices.yaml", apiServices.String())
}

func (c *clusterManifestContext) openshiftControllerManager() {
	c.addManifestFiles(
		"openshift-controller-manager/openshift-controller-manager-deployment.yaml",
		"openshift-controller-manager/openshift-controller-manager-config-configmap.yaml",
		"openshift-controller-manager/cluster-policy-controller-deployment.yaml",
		"openshift-controller-manager/openshift-controller-manager-secret.yaml",
		"openshift-controller-manager/openshift-controller-manager-configmap.yaml",
	)
	c.addUserManifestFiles(
		"openshift-controller-manager/00-openshift-controller-manager-namespace.yaml",
		"openshift-controller-manager/openshift-controller-manager-service-ca.yaml",
	)
}

func (c *clusterManifestContext) clusterVersionOperator() {
	c.addManifestFiles(
		"cluster-version-operator/cluster-version-operator-deployment.yaml",
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
		"machine-config-server/machine-config-server-serviceaccount.yaml",
		"machine-config-server/machine-config-server-rolebinding.yaml",
		"machine-config-server/machine-config-server-deployment.yaml",
		"machine-config-server/machine-config-server-service.yaml",
		"machine-config-server/machine-config-server-secret.yaml",
		"machine-config-server/machine-config-server-kubeconfig-secret.yaml",
	)
}

func (c *clusterManifestContext) openVPN() {
	c.addManifestFiles(
		"openvpn/openvpn-serviceaccount.yaml",
		"openvpn/openvpn-server-deployment.yaml",
		"openvpn/openvpn-ccd-configmap.yaml",
		"openvpn/openvpn-server-configmap.yaml",
		"openvpn/openvpn-server-secret.yaml",
		"openvpn/openvpn-client-secret.yaml",
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
		"router-proxy/router-proxy-vpnclient-secret.yaml",
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
    ignition-config: "true"
data:
  data: |-
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
