package nodepool

import (
	"bytes"
	"context"
	"embed"
	"fmt"
	"html/template"
	"net"
	"net/url"
	"slices"
	"strconv"
	"strings"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	"github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/ignition"
	"github.com/openshift/hypershift/hypershift-operator/controllers/hostedcluster"
	"github.com/openshift/hypershift/hypershift-operator/controllers/manifests"
	sharedingress "github.com/openshift/hypershift/hypershift-operator/controllers/sharedingress"
	api "github.com/openshift/hypershift/support/api"
	"github.com/openshift/hypershift/support/config"
	"github.com/openshift/hypershift/support/releaseinfo"
	"github.com/openshift/hypershift/support/util"

	mcfgv1 "github.com/openshift/api/machineconfiguration/v1"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/intstr"
	"k8s.io/client-go/tools/clientcmd"
	"k8s.io/utils/ptr"

	crclient "sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/clarketm/json"
	ignitionapi "github.com/coreos/ignition/v2/config/v3_2/types"
	"github.com/vincent-petithory/dataurl"
)

const (
	controlPlaneOperatorSkipsHAProxyConfigGenerationLabel = "io.openshift.hypershift.control-plane-operator-skips-haproxy"
	haProxyRouterImageName                                = "haproxy-router"
)

func (r *NodePoolReconciler) isHAProxyIgnitionConfigManaged(ctx context.Context, hcluster *hyperv1.HostedCluster) (m bool, cpoImage string, err error) {
	var pullSecret corev1.Secret
	if err := r.Client.Get(ctx, types.NamespacedName{Namespace: hcluster.Namespace, Name: hcluster.Spec.PullSecret.Name}, &pullSecret); err != nil {
		return false, "", fmt.Errorf("failed to get pull secret: %w", err)
	}
	pullSecretBytes, ok := pullSecret.Data[corev1.DockerConfigJsonKey]
	if !ok {
		return false, "", fmt.Errorf("expected %s key in pull secret", corev1.DockerConfigJsonKey)
	}
	controlPlaneOperatorImage, err := hostedcluster.GetControlPlaneOperatorImage(ctx, hcluster, r.ReleaseProvider, r.HypershiftOperatorImage, pullSecretBytes)
	if err != nil {
		return false, "", fmt.Errorf("failed to get controlPlaneOperatorImage: %w", err)
	}

	controlPlaneOperatorImageMetadata, err := r.ImageMetadataProvider.ImageMetadata(ctx, controlPlaneOperatorImage, pullSecretBytes)
	if err != nil {
		return false, "", fmt.Errorf("failed to look up image metadata for %s: %w", controlPlaneOperatorImage, err)
	}

	_, cpoSkips := util.ImageLabels(controlPlaneOperatorImageMetadata)[controlPlaneOperatorSkipsHAProxyConfigGenerationLabel]
	return cpoSkips, controlPlaneOperatorImage, nil
}

func (r *NodePoolReconciler) reconcileHAProxyIgnitionConfig(ctx context.Context, componentImages map[string]string, hcluster *hyperv1.HostedCluster, controlPlaneOperatorImage string) (cfg string, missing bool, err error) {
	var apiServerExternalAddress string
	var apiServerExternalPort int32
	var apiServerInternalAddress string

	if util.IsPrivateHC(hcluster) {
		apiServerExternalAddress = fmt.Sprintf("api.%s.hypershift.local", hcluster.Name)
		apiServerExternalPort = util.APIPortForLocalZone(util.IsLBKASByHC(hcluster))
	} else {
		if hcluster.Status.KubeConfig == nil {
			return "", true, nil
		}
		var kubeconfig corev1.Secret
		if err := r.Get(ctx, crclient.ObjectKey{Namespace: hcluster.Namespace, Name: hcluster.Status.KubeConfig.Name}, &kubeconfig); err != nil {
			return "", true, fmt.Errorf("failed to get kubeconfig: %w", err)
		}
		kubeconfigBytes, found := kubeconfig.Data["kubeconfig"]
		if !found {
			return "", true, fmt.Errorf("kubeconfig secret %s has no 'kubeconfig' key", crclient.ObjectKeyFromObject(&kubeconfig))
		}
		restConfig, err := clientcmd.RESTConfigFromKubeConfig(kubeconfigBytes)
		if err != nil {
			return "", true, fmt.Errorf("failed to parse kubeconfig from secret %s: %w", crclient.ObjectKeyFromObject(&kubeconfig), err)
		}
		hostURL, err := url.Parse(restConfig.Host)
		if err != nil {
			return "", true, fmt.Errorf("failed to parse host in kubeconfig from secret %s as url: %w", crclient.ObjectKeyFromObject(&kubeconfig), err)
		}
		apiServerExternalPort, err = urlPort(hostURL)
		if err != nil {
			return "", true, fmt.Errorf("cannot determine api server external port: %w", err)
		}
		apiServerExternalAddress = hostURL.Hostname()
	}

	haProxyImage, ok := componentImages[haProxyRouterImageName]
	if !ok {
		return "", true, fmt.Errorf("release image doesn't have a %s image", haProxyRouterImageName)
	}

	// This provides support for HTTP Proxy on IPv6 scenarios
	ipv4, err := util.IsIPv4CIDR(hcluster.Spec.Networking.ServiceNetwork[0].CIDR.String())
	if err != nil {
		return "", true, fmt.Errorf("error checking the stack in the first ServiceNetworkCIDR %s: %w", hcluster.Spec.Networking.ServiceNetwork[0].CIDR.String(), err)
	}

	// Set the default
	if ipv4 {
		apiServerInternalAddress = config.DefaultAdvertiseIPv4Address
	} else {
		apiServerInternalAddress = config.DefaultAdvertiseIPv6Address
	}

	// TODO (alberto): Technically this should call util.BindAPIPortWithDefaultFromHostedCluster and let 443 be an invalid value.
	// How ever we allow it here to keep backward compatibility with existing clusters which defaulted .port to 443.
	apiServerInternalPort := haproxyFrontendListenPort(hcluster)
	if hcluster.Spec.Networking.APIServer != nil {
		if hcluster.Spec.Networking.APIServer.AdvertiseAddress != nil {
			apiServerInternalAddress = *hcluster.Spec.Networking.APIServer.AdvertiseAddress
		}
	}
	var apiserverProxy string
	var noProxy string
	if hcluster.Spec.Configuration != nil && hcluster.Spec.Configuration.Proxy != nil && hcluster.Spec.Configuration.Proxy.HTTPSProxy != "" && util.ConnectsThroughInternetToControlplane(hcluster.Spec.Platform) {
		apiserverProxy, err = joinDefaultPortIfMissing(hcluster.Spec.Configuration.Proxy.HTTPSProxy)
		if err != nil {
			return "", true, fmt.Errorf("failed to parse .Spec.Configuration.Proxy.HTTPSProxy: %v", err)
		}
		noProxy = hcluster.Spec.Configuration.Proxy.NoProxy
	}

	machineConfig := manifests.MachineConfigAPIServerHAProxy()
	ignition.SetMachineConfigLabels(machineConfig)

	// Sanity check, thought this should never be <0 as hcluster.Spec.Networking is defaulted in the API.
	var serviceNetworkCIDR, clusterNetworkCIDR string
	if len(hcluster.Spec.Networking.ServiceNetwork) > 0 {
		serviceNetworkCIDR = hcluster.Spec.Networking.ServiceNetwork[0].CIDR.String()
	}
	if len(hcluster.Spec.Networking.ClusterNetwork) > 0 {
		clusterNetworkCIDR = hcluster.Spec.Networking.ClusterNetwork[0].CIDR.String()
	}

	if sharedingress.UseSharedIngress() {
		sharedIngressRouteSVC := &corev1.Service{
			TypeMeta: metav1.TypeMeta{},
			ObjectMeta: metav1.ObjectMeta{
				Name:      sharedingress.RouterPublicService().Name,
				Namespace: sharedingress.RouterNamespace,
			},
		}
		if err := r.Client.Get(ctx, crclient.ObjectKeyFromObject(sharedIngressRouteSVC), sharedIngressRouteSVC); err != nil {
			return "", true, err
		}
		if len(sharedIngressRouteSVC.Status.LoadBalancer.Ingress) < 1 {
			return "", true, nil
		}
		apiServerExternalAddress = sharedIngressRouteSVC.Status.LoadBalancer.Ingress[0].IP
		apiServerExternalPort = sharedingress.KASSVCLBPort
	}

	serializedConfig, err := apiServerProxyConfig(haProxyImage, controlPlaneOperatorImage, hcluster.Spec.ClusterID,
		apiServerExternalAddress, apiServerInternalAddress,
		apiServerExternalPort, apiServerInternalPort,
		apiserverProxy, noProxy, serviceNetworkCIDR, clusterNetworkCIDR)
	if err != nil {
		return "", true, fmt.Errorf("failed to create apiserver haproxy config: %w", err)
	}
	machineConfig.Spec.Config.Raw = serializedConfig

	buf := &bytes.Buffer{}
	machineConfig.APIVersion = mcfgv1.SchemeGroupVersion.String()
	machineConfig.Kind = "MachineConfig"
	if err := api.YamlSerializer.Encode(machineConfig, buf); err != nil {
		return "", true, fmt.Errorf("failed to serialize haproxy machine config: %w", err)
	}

	return buf.String(), false, nil
}

// TODO (alberto): Technically anything should be calling util.BindAPIPortWithDefaultFromHostedCluster and let 443 be an invalid value.
// How ever we allow it here to keep backward compatibility with existing clusters which defaulted .port to 443.
func haproxyFrontendListenPort(hc *hyperv1.HostedCluster) int32 {
	if hc.Spec.Networking.APIServer != nil && hc.Spec.Networking.APIServer.Port != nil {
		return *hc.Spec.Networking.APIServer.Port
	}
	return config.KASPodDefaultPort
}

func urlPort(u *url.URL) (int32, error) {
	portStr := u.Port()
	if portStr == "" {
		switch u.Scheme {
		case "http":
			return 80, nil
		case "https":
			return 443, nil
		default:
			return 0, fmt.Errorf("unknown scheme: %s", u.Scheme)
		}
	}
	port, err := strconv.Atoi(portStr)
	return int32(port), err
}

type fileToAdd struct {
	template *template.Template
	source   func() ([]byte, error)
	name     string
	mode     int
	params   map[string]any
}

//go:embed apiserver-haproxy/*
var content embed.FS

func MustAsset(name string) string {
	b, err := content.ReadFile(name)
	if err != nil {
		panic(err)
	}
	return string(b)
}

var (
	setupAPIServerIPScriptTemplate    = template.Must(template.New("setupAPIServerIP").Parse(MustAsset("apiserver-haproxy/setup-apiserver-ip.sh")))
	teardownAPIServerIPScriptTemplate = template.Must(template.New("teardownAPIServerIP").Parse(MustAsset("apiserver-haproxy/teardown-apiserver-ip.sh")))
	haProxyConfigTemplate             = template.Must(template.New("haProxyConfig").Parse(MustAsset("apiserver-haproxy/haproxy.cfg")))
)

func apiServerProxyConfig(haProxyImage, cpoImage, clusterID,
	externalAPIAddress, internalAPIAddress string,
	externalAPIPort, internalAPIPort int32, proxyAddr, noProxy, serviceNetwork, clusterNetwork string) ([]byte, error) {
	config := &ignitionapi.Config{}
	config.Ignition.Version = ignitionapi.MaxVersion.String()

	if sharedingress.UseSharedIngress() {
		// proxy protocol v2 with TLV support (custom proxy protocol header) requires haproxy v2.9+, see: https://www.haproxy.com/blog/announcing-haproxy-2-9#proxy-protocol-tlv-fields
		// TODO: get the image from the payload once available https://issues.redhat.com/browse/HOSTEDCP-1819
		haProxyImage = "quay.io/rh_ee_brcox/hypershift:haproxy2.9.9-multi"
	}

	filesToAdd := []fileToAdd{
		{
			template: setupAPIServerIPScriptTemplate,
			name:     "/usr/local/bin/setup-apiserver-ip.sh",
			mode:     0755,
			params: map[string]any{
				"InternalAPIAddress": internalAPIAddress,
			},
		},
		{
			template: teardownAPIServerIPScriptTemplate,
			name:     "/usr/local/bin/teardown-apiserver-ip.sh",
			mode:     0755,
			params: map[string]any{
				"InternalAPIAddress": internalAPIAddress,
			},
		},
	}

	// Check if no proxy contains any address that should result in skipping the system proxy
	skipProxyForKAS := slices.ContainsFunc([]string{externalAPIAddress, internalAPIAddress, "kubernetes", serviceNetwork, clusterNetwork}, func(s string) bool {
		return strings.Contains(noProxy, s)
	})

	if proxyAddr == "" || skipProxyForKAS {
		filesToAdd = append(filesToAdd, []fileToAdd{
			{
				template: haProxyConfigTemplate,
				name:     "/etc/kubernetes/apiserver-proxy-config/haproxy.cfg",
				mode:     0644,
				params: map[string]any{
					"InternalAPIAddress": internalAPIAddress,
					"InternalAPIPort":    internalAPIPort,
					"ExternalAPIAddress": externalAPIAddress,
					"ExternalAPIPort":    externalAPIPort,
					"UseProxyProtocol":   sharedingress.UseSharedIngress(),
					"ClusterID":          clusterID,
				},
			},
			{
				source: generateHAProxyStaticPod("kube-apiserver-proxy", haProxyImage, internalAPIAddress, "/etc/kubernetes/apiserver-proxy-config", internalAPIPort),
				name:   "/etc/kubernetes/manifests/kube-apiserver-proxy.yaml",
				mode:   0644,
			},
		}...)
	} else {
		filesToAdd = append(filesToAdd, fileToAdd{
			source: generateKubernetesDefaultProxyPod(cpoImage, fmt.Sprintf("%s:%d", internalAPIAddress, internalAPIPort), proxyAddr, fmt.Sprintf("%s:%d", externalAPIAddress, externalAPIPort)),
			name:   "/etc/kubernetes/manifests/kube-apiserver-proxy.yaml",
			mode:   0644,
		})
	}

	files := []ignitionapi.File{}
	for _, file := range filesToAdd {
		var fileBytes []byte
		if file.template != nil {
			out := &bytes.Buffer{}
			if err := file.template.Execute(out, file.params); err != nil {
				return nil, err
			}
			fileBytes = out.Bytes()
		}
		if file.source != nil {
			out, err := file.source()
			if err != nil {
				return nil, err
			}
			fileBytes = out
		}
		files = append(files, fileFromBytes(file.name, file.mode, fileBytes))
	}
	config.Storage.Files = files
	config.Systemd.Units = []ignitionapi.Unit{
		apiServerIPUnit(),
	}
	return json.Marshal(config)
}

func generateHAProxyStaticPod(name, image, internalAPIAddress, configPath string, internalAPIPort int32) func() ([]byte, error) {
	return func() ([]byte, error) {
		pod := &corev1.Pod{}
		pod.APIVersion = corev1.SchemeGroupVersion.String()
		pod.Kind = "Pod"
		pod.Name = name
		pod.Namespace = "kube-system"
		pod.Labels = map[string]string{
			"k8s-app": name,
		}
		pod.Spec.HostNetwork = true
		pod.Spec.PriorityClassName = "system-node-critical"
		pod.Spec.Volumes = []corev1.Volume{
			{
				Name: "config",
				VolumeSource: corev1.VolumeSource{
					HostPath: &corev1.HostPathVolumeSource{
						Path: configPath,
					},
				},
			},
		}
		pod.Spec.Containers = []corev1.Container{
			{
				Name:  "haproxy",
				Image: image,
				Command: []string{
					"haproxy",
					"-f",
					"/usr/local/etc/haproxy",
				},
				VolumeMounts: []corev1.VolumeMount{
					{
						Name:      "config",
						MountPath: "/usr/local/etc/haproxy",
					},
				},
				SecurityContext: &corev1.SecurityContext{
					RunAsUser: ptr.To[int64](config.DefaultSecurityContextUser),
				},
				Resources: corev1.ResourceRequirements{
					Requests: corev1.ResourceList{
						corev1.ResourceMemory: resource.MustParse("16Mi"),
						corev1.ResourceCPU:    resource.MustParse("13m"),
					},
				},
				LivenessProbe: &corev1.Probe{
					FailureThreshold:    3,
					InitialDelaySeconds: 120,
					PeriodSeconds:       120,
					SuccessThreshold:    1,
					ProbeHandler: corev1.ProbeHandler{
						HTTPGet: &corev1.HTTPGetAction{
							Path:   "/livez/ping",
							Scheme: corev1.URISchemeHTTPS,
							Host:   internalAPIAddress,
							Port:   intstr.FromInt(int(internalAPIPort)),
						},
					},
				},
				Ports: []corev1.ContainerPort{
					{
						Name:          "apiserver",
						Protocol:      corev1.ProtocolTCP,
						HostPort:      internalAPIPort,
						ContainerPort: internalAPIPort,
					},
				},
			},
		}
		out := &bytes.Buffer{}
		if err := api.YamlSerializer.Encode(pod, out); err != nil {
			return nil, err
		}
		return out.Bytes(), nil
	}
}

func generateKubernetesDefaultProxyPod(image string, listenAddr string, proxyAddr string, apiserverAddr string) func() ([]byte, error) {
	return func() ([]byte, error) {
		p := &corev1.Pod{
			TypeMeta: metav1.TypeMeta{
				APIVersion: corev1.SchemeGroupVersion.String(),
				Kind:       "Pod",
			},
			ObjectMeta: metav1.ObjectMeta{
				Name:      "kube-apiserver-proxy",
				Namespace: "kube-system",
				Labels: map[string]string{
					"k8s-app": "kube-apiserver-proxy",
				},
			},
			Spec: corev1.PodSpec{
				HostNetwork:       true,
				PriorityClassName: "system-node-critical",
				Containers: []corev1.Container{{
					Image: image,
					Name:  "kubernetes-default-proxy",
					Command: []string{
						"control-plane-operator",
						"kubernetes-default-proxy",
						"--listen-addr=" + listenAddr,
						"--proxy-addr=" + strings.TrimPrefix(strings.TrimPrefix(proxyAddr, "http://"), "https://"),
						"--apiserver-addr=" + apiserverAddr,
					},
					SecurityContext: &corev1.SecurityContext{
						RunAsUser: ptr.To[int64](config.DefaultSecurityContextUser),
					},
					Resources: corev1.ResourceRequirements{
						Requests: corev1.ResourceList{
							corev1.ResourceMemory: resource.MustParse("16Mi"),
							corev1.ResourceCPU:    resource.MustParse("13m"),
						},
					},
				}},
			},
		}
		out := &bytes.Buffer{}
		if err := api.YamlSerializer.Encode(p, out); err != nil {
			return nil, err
		}
		return out.Bytes(), nil
	}
}

// fileFromBytes creates an ignition-config file with the given contents.
// copied from openshift-installer
func fileFromBytes(path string, mode int, contents []byte) ignitionapi.File {
	return ignitionapi.File{
		Node: ignitionapi.Node{
			Path:      path,
			Overwrite: ptr.To(true),
		},
		FileEmbedded1: ignitionapi.FileEmbedded1{
			Mode: &mode,
			Contents: ignitionapi.Resource{
				Source: ptr.To(dataurl.EncodeBytes(contents)),
			},
		},
	}
}

func apiServerIPUnit() ignitionapi.Unit {
	content := MustAsset("apiserver-haproxy/apiserver-ip.service")
	return ignitionapi.Unit{
		Name:     "apiserver-ip.service",
		Contents: &content,
		Enabled:  ptr.To(true),
	}
}

func joinDefaultPortIfMissing(addr string) (string, error) {
	parsedUrl, err := url.Parse(addr)
	if err != nil {
		return "", err
	}
	if parsedUrl.Scheme == "" {
		return "", fmt.Errorf("url scheme must be specified")
	}

	if parsedUrl.Port() == "" {
		if parsedUrl.Scheme == "https" {
			parsedUrl.Host = net.JoinHostPort(parsedUrl.Hostname(), "443")
		} else {
			parsedUrl.Host = net.JoinHostPort(parsedUrl.Hostname(), "80")
		}
	}

	return parsedUrl.String(), nil
}

func (r *NodePoolReconciler) generateHAProxyRawConfig(ctx context.Context, hcluster *hyperv1.HostedCluster, releaseImage *releaseinfo.ReleaseImage) (string, error) {
	var haproxyRawConfig string
	controlPlaneNamespace := manifests.HostedControlPlaneNamespace(hcluster.Namespace, hcluster.Name)
	isHAProxyIgnitionConfigManaged, cpoImage, err := r.isHAProxyIgnitionConfigManaged(ctx, hcluster)
	if err != nil {
		return "", fmt.Errorf("failed to check if we manage haproxy ignition config: %w", err)
	}
	if isHAProxyIgnitionConfigManaged {
		oldHAProxyIgnitionConfig := &corev1.ConfigMap{
			ObjectMeta: metav1.ObjectMeta{Namespace: controlPlaneNamespace, Name: "ignition-config-apiserver-haproxy"},
		}
		err := r.Client.Get(ctx, crclient.ObjectKeyFromObject(oldHAProxyIgnitionConfig), oldHAProxyIgnitionConfig)
		if err != nil && !apierrors.IsNotFound(err) {
			return "", fmt.Errorf("failed to get CPO-managed haproxy ignition config: %w", err)
		}
		if err == nil {
			if err := r.Client.Delete(ctx, oldHAProxyIgnitionConfig); err != nil && !apierrors.IsNotFound(err) {
				return "", fmt.Errorf("failed to delete the CPO-managed haproxy ignition config: %w", err)
			}
		}

		var missing bool
		haproxyRawConfig, missing, err = r.reconcileHAProxyIgnitionConfig(ctx, releaseImage.ComponentImages(), hcluster, cpoImage)
		if err != nil {
			return "", fmt.Errorf("failed to generate haproxy ignition config: %w", err)
		}
		if missing {
			return "", fmt.Errorf("failed to generate haproxy ignition config: waiting for missing component")
		}
	}
	return haproxyRawConfig, nil
}
