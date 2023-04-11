package nodepool

import (
	"bytes"
	"context"
	"embed"
	"fmt"
	"html/template"
	"strconv"
	"strings"

	"github.com/clarketm/json"
	ignitionapi "github.com/coreos/ignition/v2/config/v3_2/types"
	api "github.com/openshift/hypershift/api"
	hyperv1 "github.com/openshift/hypershift/api/v1alpha1"
	"github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/ignition"
	"github.com/openshift/hypershift/hypershift-operator/controllers/hostedcluster"
	"github.com/openshift/hypershift/hypershift-operator/controllers/manifests"
	"github.com/openshift/hypershift/support/config"
	"github.com/openshift/hypershift/support/releaseinfo"
	"github.com/openshift/hypershift/support/util"
	mcfgv1 "github.com/openshift/hypershift/thirdparty/machineconfigoperator/pkg/apis/machineconfiguration.openshift.io/v1"
	"github.com/vincent-petithory/dataurl"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/intstr"
	"k8s.io/utils/pointer"
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

func (r *NodePoolReconciler) reconcileHAProxyIgnitionConfig(ctx context.Context, releaseImage *releaseinfo.ReleaseImage, hcluster *hyperv1.HostedCluster, controlPlaneOperatorImage string) (cfg string, missing bool, err error) {
	var apiServerExternalAddress string
	apiServerExternalPort := util.APIPortWithDefaultFromHostedCluster(hcluster, config.DefaultAPIServerPort)
	if util.IsPrivateHC(hcluster) {
		apiServerExternalAddress = fmt.Sprintf("api.%s.hypershift.local", hcluster.Name)
	}

	haProxyImage, ok := releaseImage.ComponentImages()[haProxyRouterImageName]
	if !ok {
		return "", true, fmt.Errorf("release image doesn't have a %s image", haProxyRouterImageName)
	}

	apiServerInternalAddress := config.DefaultAdvertiseAddress
	apiServerInternalPort := int32(config.DefaultAPIServerPort)
	if hcluster.Spec.Networking.APIServer != nil {
		if hcluster.Spec.Networking.APIServer.AdvertiseAddress != nil {
			apiServerInternalAddress = *hcluster.Spec.Networking.APIServer.AdvertiseAddress
		}
		if hcluster.Spec.Networking.APIServer.Port != nil {
			apiServerInternalPort = *hcluster.Spec.Networking.APIServer.Port
		}
	}

	var apiserverProxy string
	if hcluster.Spec.Configuration != nil && hcluster.Spec.Configuration.Proxy != nil && hcluster.Spec.Configuration.Proxy.HTTPSProxy != "" && util.ConnectsThroughInternetToControlplane(hcluster.Spec.Platform) {
		apiserverProxy = hcluster.Spec.Configuration.Proxy.HTTPSProxy
	}

	machineConfig := manifests.MachineConfigAPIServerHAProxy()
	ignition.SetMachineConfigLabels(machineConfig)
	serializedConfig, err := apiServerProxyConfig(haProxyImage, controlPlaneOperatorImage, apiServerExternalAddress, apiServerInternalAddress, apiServerExternalPort, apiServerInternalPort, apiserverProxy)
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

type fileToAdd struct {
	template *template.Template
	source   func() ([]byte, error)
	name     string
	mode     int
	params   map[string]string
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

func apiServerProxyConfig(haProxyImage, cpoImage, externalAPIAddress, internalAPIAddress string, externalAPIPort, internalAPIPort int32, proxyAddr string) ([]byte, error) {
	config := &ignitionapi.Config{}
	config.Ignition.Version = ignitionapi.MaxVersion.String()

	filesToAdd := []fileToAdd{
		{
			template: setupAPIServerIPScriptTemplate,
			name:     "/usr/local/bin/setup-apiserver-ip.sh",
			mode:     0755,
			params: map[string]string{
				"InternalAPIAddress": internalAPIAddress,
			},
		},
		{
			template: teardownAPIServerIPScriptTemplate,
			name:     "/usr/local/bin/teardown-apiserver-ip.sh",
			mode:     0755,
			params: map[string]string{
				"InternalAPIAddress": internalAPIAddress,
			},
		},
	}
	if proxyAddr == "" {
		filesToAdd = append(filesToAdd, []fileToAdd{
			{
				template: haProxyConfigTemplate,
				name:     "/etc/kubernetes/apiserver-proxy-config/haproxy.cfg",
				mode:     0644,
				params: map[string]string{
					"ExternalAPIAddress": externalAPIAddress,
					"ExternalAPIPort":    strconv.FormatInt(int64(externalAPIPort), 10),
					"InternalAPIAddress": internalAPIAddress,
					"InternalAPIPort":    strconv.FormatInt(int64(internalAPIPort), 10),
				},
			},
			{
				source: generateHAProxyStaticPod(haProxyImage, internalAPIAddress, internalAPIPort),
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

func generateHAProxyStaticPod(image, internalAPIAddress string, internalAPIPort int32) func() ([]byte, error) {
	return func() ([]byte, error) {
		pod := &corev1.Pod{}
		pod.APIVersion = corev1.SchemeGroupVersion.String()
		pod.Kind = "Pod"
		pod.Name = "kube-apiserver-proxy"
		pod.Namespace = "kube-system"
		pod.Labels = map[string]string{
			"k8s-app": "kube-apiserver-proxy",
			"hypershift.openshift.io/control-plane-component": "kube-apiserver-proxy",
		}
		pod.Spec.HostNetwork = true
		pod.Spec.PriorityClassName = "system-node-critical"
		pod.Spec.Volumes = []corev1.Volume{
			{
				Name: "config",
				VolumeSource: corev1.VolumeSource{
					HostPath: &corev1.HostPathVolumeSource{
						Path: "/etc/kubernetes/apiserver-proxy-config",
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
					RunAsUser: pointer.Int64Ptr(config.DefaultSecurityContextUser),
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
							Path:   "/version",
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
					"hypershift.openshift.io/control-plane-component": "kube-apiserver-proxy",
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
						RunAsUser: pointer.Int64Ptr(config.DefaultSecurityContextUser),
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
			Overwrite: pointer.BoolPtr(true),
		},
		FileEmbedded1: ignitionapi.FileEmbedded1{
			Mode: &mode,
			Contents: ignitionapi.Resource{
				Source: pointer.StringPtr(dataurl.EncodeBytes(contents)),
			},
		},
	}
}

func apiServerIPUnit() ignitionapi.Unit {
	content := MustAsset("apiserver-haproxy/apiserver-ip.service")
	return ignitionapi.Unit{
		Name:     "apiserver-ip.service",
		Contents: &content,
		Enabled:  pointer.BoolPtr(true),
	}
}
