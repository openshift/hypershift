package hypershift

import (
	"text/template"
)

func RenderPKISecrets(pkiDir, outputDir string, etcd, vpn bool, externalOauth bool) {
	ctx := newPKIRenderContext(pkiDir, outputDir)
	ctx.setupManifests(etcd, vpn, externalOauth)
	ctx.renderManifests()
}

type pkiRenderContext struct {
	*renderContext
}

func newPKIRenderContext(pkiDir, outputDir string) *pkiRenderContext {
	ctx := &pkiRenderContext{
		renderContext: newRenderContext(nil, outputDir),
	}
	ctx.setFuncs(template.FuncMap{
		"pki":         pkiFunc(pkiDir),
		"include_pki": includePKIFunc(pkiDir),
	})
	return ctx
}

func (c *pkiRenderContext) setupManifests(etcd bool, vpn bool, externalOauth bool) {
	c.serviceAdminKubeconfig()
	c.kubeAPIServer(vpn)
	if etcd {
		c.etcd()
	}
	if externalOauth {
		c.oauthOpenshiftServer()
	}
	c.kubeControllerManager()
	c.openshiftAPIServer()
	c.openshiftControllerManager()
	c.controlPlaneOperator()
	if vpn {
		c.openVPN()
	}
	c.routerProxy()
	c.hypershiftOperator()
	c.machineConfigServer()
}

func (c *pkiRenderContext) etcd() {
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

func (c *pkiRenderContext) oauthOpenshiftServer() {
	c.addManifestFiles(
		"oauth-openshift/oauth-server-secret.yaml",
		"oauth-openshift/oauth-server-configmap.yaml",
	)
}

func (c *pkiRenderContext) kubeAPIServer(includeVPN bool) {
	c.addManifestFiles(
		"kube-apiserver/kube-apiserver-secret.yaml",
		"kube-apiserver/kube-apiserver-configmap.yaml",
	)
	if includeVPN {
		c.addManifestFiles(
			"kube-apiserver/kube-apiserver-vpnclient-secret.yaml",
		)
	}
}

func (c *pkiRenderContext) kubeControllerManager() {
	c.addManifestFiles(
		"kube-controller-manager/kube-controller-manager-secret.yaml",
		"kube-controller-manager/kube-controller-manager-configmap.yaml",
	)
}

func (c *pkiRenderContext) kubeScheduler() {
	c.addManifestFiles(
		"kube-scheduler/kube-scheduler-secret.yaml",
	)
}

func (c *pkiRenderContext) openshiftAPIServer() {
	c.addManifestFiles(
		"openshift-apiserver/openshift-apiserver-secret.yaml",
		"openshift-apiserver/openshift-apiserver-configmap.yaml",
	)
}

func (c *pkiRenderContext) openshiftControllerManager() {
	c.addManifestFiles(
		"openshift-controller-manager/openshift-controller-manager-secret.yaml",
		"openshift-controller-manager/openshift-controller-manager-configmap.yaml",
	)
}

func (c *pkiRenderContext) controlPlaneOperator() {
	c.addManifestFiles(
		"control-plane-operator/cp-operator-configmap.yaml",
	)
}

func (c *pkiRenderContext) hypershiftOperator() {
	c.addManifestFiles(
		"hypershift-operator/hypershift-operator-configmap.yaml",
	)
}

func (c *pkiRenderContext) openVPN() {
	c.addManifestFiles(
		"openvpn/openvpn-server-secret.yaml",
		"openvpn/openvpn-client-secret.yaml",
	)
}

func (c *pkiRenderContext) serviceAdminKubeconfig() {
	c.addManifestFiles(
		"common/service-network-admin-kubeconfig-secret.yaml",
	)
}

func (c *pkiRenderContext) routerProxy() {
	c.addManifestFiles(
		"router-proxy/router-proxy-vpnclient-secret.yaml",
	)
}

func (c *pkiRenderContext) machineConfigServer() {
	c.addManifestFiles(
		"machine-config-server/machine-config-server-secret.yaml",
		"machine-config-server/machine-config-server-kubeconfig-secret.yaml",
	)
}
