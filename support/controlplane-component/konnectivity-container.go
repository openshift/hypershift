package controlplanecomponent

import (
	"fmt"
	"path"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	"github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/manifests"
	"github.com/openshift/hypershift/support/proxy"
	"github.com/openshift/hypershift/support/util"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	"k8s.io/utils/ptr"

	configv1 "github.com/openshift/api/config/v1"
)

type ProxyMode string

const (
	Socks5 ProxyMode = "socks5"
	HTTPS  ProxyMode = "https"

	// Dual mode will inject 2 konnectivity containers, one using HTTPS mode and the other using Socks5 mode.
	Dual ProxyMode = "dual"
)

type KonnectivityContainerOptions struct {
	Mode ProxyMode
	// defaults to 'kubeconfig'
	KubeconfingVolumeName string

	HTTPSOptions  HTTPSOptions
	Socks5Options Socks5Options
}

type HTTPSOptions struct {
	// KonnectivityHost is the host name of the Konnectivity server proxy.
	KonnectivityHost string
	// KonnectivityPort is the port of the Konnectivity server proxy.
	KonnectivityPort uint32
	// The port that https proxy should serve on.
	ServingPort uint32
	// ConnectDirectlyToCloudAPIs specifies whether cloud APIs should be bypassed
	// by the proxy. This is used by the ingress operator to be able to create DNS records
	// before worker nodes are present in the cluster.
	// See https://github.com/openshift/hypershift/pull/1601
	ConnectDirectlyToCloudAPIs *bool
}

type Socks5Options struct {
	// KonnectivityHost is the host name of the Konnectivity server proxy.
	KonnectivityHost string
	// KonnectivityPort is the port of the Konnectivity server proxy.
	KonnectivityPort uint32
	// The port that socks5 proxy should serve on.
	ServingPort uint32
	// ConnectDirectlyToCloudAPIs specifies whether cloud APIs should be bypassed
	// by the proxy. This is used by the ingress operator to be able to create DNS records
	// before worker nodes are present in the cluster.
	// See https://github.com/openshift/hypershift/pull/1601
	ConnectDirectlyToCloudAPIs *bool
	// ResolveFromManagementClusterDNS tells the dialer to fallback to the management
	// cluster's DNS (and direct dialer) initially until the konnectivity tunnel is available.
	// Once the konnectivity tunnel is available, it no longer falls back on the management
	// cluster. This is used by the OAuth server to allow quicker initialization of identity
	// providers while worker nodes have not joined.
	// See https://github.com/openshift/hypershift/pull/2261
	ResolveFromManagementClusterDNS *bool
	// ResolveFromGuestClusterDNS tells the dialer to resolve names using the guest
	// cluster's coreDNS service. Used by oauth and ingress operator.
	ResolveFromGuestClusterDNS *bool
	// DisableResolver disables any name resolution by the resolver. This is used by the CNO.
	// See https://github.com/openshift/hypershift/pull/3986
	DisableResolver *bool
}

func (opts *KonnectivityContainerOptions) injectKonnectivityContainer(cpContext ControlPlaneContext, podSpec *corev1.PodSpec) {
	if opts.Mode == "" {
		// programmer error.
		panic("Konnectivity proxy mode must be specified!")
	}

	hcp := cpContext.HCP
	var proxyAdditionalCAs []corev1.VolumeProjection
	if hcp.Spec.AdditionalTrustBundle != nil {
		proxyAdditionalCAs = append(proxyAdditionalCAs, corev1.VolumeProjection{
			ConfigMap: &corev1.ConfigMapProjection{
				LocalObjectReference: *hcp.Spec.AdditionalTrustBundle,
				Items:                []corev1.KeyToPath{{Key: "ca-bundle.crt", Path: "additional-ca-bundle.pem"}},
			},
		})
	}

	if hcp.Spec.Configuration != nil && hcp.Spec.Configuration.Proxy != nil && len(hcp.Spec.Configuration.Proxy.TrustedCA.Name) > 0 {
		proxyAdditionalCAs = append(proxyAdditionalCAs, corev1.VolumeProjection{
			ConfigMap: &corev1.ConfigMapProjection{
				LocalObjectReference: corev1.LocalObjectReference{
					Name: hcp.Spec.Configuration.Proxy.TrustedCA.Name,
				},
				Items: []corev1.KeyToPath{{Key: "ca-bundle.crt", Path: "proxy-trusted-ca.pem"}},
			},
		})
	}

	image := cpContext.ReleaseImageProvider.GetImage(util.CPOImageName)

	if opts.Mode == Dual {
		opts.Mode = HTTPS
		podSpec.Containers = append(podSpec.Containers, opts.buildContainer(hcp, image, proxyAdditionalCAs))

		opts.Mode = Socks5
		podSpec.Containers = append(podSpec.Containers, opts.buildContainer(hcp, image, proxyAdditionalCAs))
	} else {
		podSpec.Containers = append(podSpec.Containers, opts.buildContainer(hcp, image, proxyAdditionalCAs))
	}

	podSpec.Volumes = append(podSpec.Volumes, opts.buildVolumes(proxyAdditionalCAs)...)
}

const certsTrustPath = "/etc/pki/tls/certs"

func (opts *KonnectivityContainerOptions) buildContainer(hcp *hyperv1.HostedControlPlane, image string, proxyAdditionalCAs []corev1.VolumeProjection) corev1.Container {
	var proxyConfig *configv1.ProxySpec
	if hcp.Spec.Configuration != nil {
		proxyConfig = hcp.Spec.Configuration.Proxy
	}

	command := []string{"/usr/bin/control-plane-operator"}
	args := []string{"run"}
	switch opts.Mode {
	case HTTPS:
		command = append(command, "konnectivity-https-proxy")
		if proxyConfig != nil {
			noProxy := proxy.DefaultNoProxy(hcp)
			args = append(args, "--http-proxy", proxyConfig.HTTPProxy)
			args = append(args, "--https-proxy", proxyConfig.HTTPSProxy)
			args = append(args, "--no-proxy", noProxy)
		}
		if host := opts.HTTPSOptions.KonnectivityHost; host != "" {
			args = append(args, fmt.Sprintf("--konnectivity-hostname=%s", host))
		}
		if port := opts.HTTPSOptions.KonnectivityPort; port != 0 {
			args = append(args, fmt.Sprintf("--konnectivity-port=%d", port))
		}
		if servingPort := opts.HTTPSOptions.ServingPort; servingPort != 0 {
			args = append(args, fmt.Sprintf("--serving-port=%d", servingPort))
		}
		if value := opts.HTTPSOptions.ConnectDirectlyToCloudAPIs; value != nil {
			args = append(args, fmt.Sprintf("--connect-directly-to-cloud-apis=%t", *value))
		}
	case Socks5:
		command = append(command, "konnectivity-socks5-proxy")
		if host := opts.Socks5Options.KonnectivityHost; host != "" {
			args = append(args, fmt.Sprintf("--konnectivity-hostname=%s", host))
		}
		if port := opts.Socks5Options.KonnectivityPort; port != 0 {
			args = append(args, fmt.Sprintf("--konnectivity-port=%d", port))
		}
		if servingPort := opts.Socks5Options.ServingPort; servingPort != 0 {
			args = append(args, fmt.Sprintf("--serving-port=%d", servingPort))
		}
		if value := opts.Socks5Options.ConnectDirectlyToCloudAPIs; value != nil {
			args = append(args, fmt.Sprintf("--connect-directly-to-cloud-apis=%t", *value))
		}
		if value := opts.Socks5Options.ResolveFromGuestClusterDNS; value != nil {
			args = append(args, fmt.Sprintf("--resolve-from-guest-cluster-dns=%t", *value))
		}
		if value := opts.Socks5Options.ResolveFromManagementClusterDNS; value != nil {
			args = append(args, fmt.Sprintf("--resolve-from-management-cluster-dns=%t", *value))
		}
		if value := opts.Socks5Options.DisableResolver; value != nil {
			args = append(args, fmt.Sprintf("--disable-resolver=%t", *value))
		}
	}

	kubeconfingVolumeName := opts.KubeconfingVolumeName
	if kubeconfingVolumeName == "" {
		kubeconfingVolumeName = "kubeconfig"
	}

	container := corev1.Container{
		Name:    fmt.Sprintf("konnectivity-proxy-%s", opts.Mode),
		Image:   image,
		Command: command,
		Args:    args,

		Resources: corev1.ResourceRequirements{
			Requests: corev1.ResourceList{
				corev1.ResourceCPU:    resource.MustParse("10m"),
				corev1.ResourceMemory: resource.MustParse("30Mi"),
			},
		},
		Env: []corev1.EnvVar{{
			Name:  "KUBECONFIG",
			Value: "/etc/kubernetes/secrets/kubeconfig/kubeconfig",
		}},
		VolumeMounts: []corev1.VolumeMount{
			{
				Name:      kubeconfingVolumeName,
				MountPath: "/etc/kubernetes/secrets/kubeconfig",
			},
			{
				Name:      "konnectivity-proxy-cert",
				MountPath: "/etc/konnectivity/proxy-client",
			},
			{
				Name:      "konnectivity-proxy-ca",
				MountPath: "/etc/konnectivity/proxy-ca",
			},
		},
	}

	if len(proxyAdditionalCAs) > 0 {
		for _, additionalCA := range proxyAdditionalCAs {
			for _, item := range additionalCA.ConfigMap.Items {
				container.VolumeMounts = append(container.VolumeMounts, corev1.VolumeMount{
					Name:      "proxy-additional-trust-bundle",
					MountPath: path.Join(certsTrustPath, item.Path),
					SubPath:   item.Path,
				})
			}
		}
	}

	return container
}

func (opts *KonnectivityContainerOptions) buildVolumes(proxyAdditionalCAs []corev1.VolumeProjection) []corev1.Volume {
	volumes := []corev1.Volume{
		{
			Name: "konnectivity-proxy-cert",
			VolumeSource: corev1.VolumeSource{
				Secret: &corev1.SecretVolumeSource{
					SecretName:  manifests.KonnectivityClientSecret("").Name,
					DefaultMode: ptr.To[int32](0640),
				},
			},
		},
		{
			Name: "konnectivity-proxy-ca",
			VolumeSource: corev1.VolumeSource{
				ConfigMap: &corev1.ConfigMapVolumeSource{
					LocalObjectReference: corev1.LocalObjectReference{
						Name: manifests.KonnectivityCAConfigMap("").Name,
					},
				},
			},
		},
	}

	if len(proxyAdditionalCAs) > 0 {
		volumes = append(volumes, corev1.Volume{
			Name: "proxy-additional-trust-bundle",
			VolumeSource: corev1.VolumeSource{
				Projected: &corev1.ProjectedVolumeSource{
					Sources:     proxyAdditionalCAs,
					DefaultMode: ptr.To[int32](420),
				},
			},
		})
	}

	return volumes
}
