package ignition

import (
	"bytes"
	"fmt"
	"html/template"
	"strconv"

	"github.com/clarketm/json"
	"github.com/vincent-petithory/dataurl"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	"k8s.io/apimachinery/pkg/util/intstr"
	"k8s.io/utils/pointer"

	igntypes "github.com/coreos/ignition/v2/config/v3_2/types"
	"github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/manifests"
	"github.com/openshift/hypershift/support/api"
	"github.com/openshift/hypershift/support/config"
	mcfgv1 "github.com/openshift/hypershift/thirdparty/machineconfigoperator/pkg/apis/machineconfiguration.openshift.io/v1"
)

const (
	ignitionConfigKey = "config"
	ignitionVersion   = "3.2.0"
)

var (
	setupAPIServerIPScriptTemplate    = template.Must(template.New("setupAPIServerIP").Parse(MustAsset("apiserver-haproxy/setup-apiserver-ip.sh")))
	teardownAPIServerIPScriptTemplate = template.Must(template.New("teardownAPIServerIP").Parse(MustAsset("apiserver-haproxy/teardown-apiserver-ip.sh")))
	haProxyConfigTemplate             = template.Must(template.New("haProxyConfig").Parse(MustAsset("apiserver-haproxy/haproxy.cfg")))
)

func ReconcileFIPSIgnitionConfig(cm *corev1.ConfigMap, ownerRef config.OwnerRef, fipsEnabled bool) error {
	machineConfig := manifests.MachineConfigFIPS()
	setMachineConfigLabels(machineConfig)
	machineConfig.Spec.FIPS = fipsEnabled
	return reconcileIgnitionConfigMap(cm, machineConfig, ownerRef)
}

func ReconcileWorkerSSHIgnitionConfig(cm *corev1.ConfigMap, ownerRef config.OwnerRef, sshKey string) error {
	machineConfig := manifests.MachineConfigWorkerSSH()
	setMachineConfigLabels(machineConfig)
	serializedConfig, err := workerSSHConfig(sshKey)
	if err != nil {
		return fmt.Errorf("failed to serialize ignition config: %w", err)
	}
	machineConfig.Spec.Config.Raw = serializedConfig
	return reconcileIgnitionConfigMap(cm, machineConfig, ownerRef)
}

func ReconcileAPIServerHAProxyIgnitionConfig(cm *corev1.ConfigMap, ownerRef config.OwnerRef, haProxyImage, externalAPIAddress, internalAPIAddress string, externalAPIPort, internalAPIPort int32) error {
	machineConfig := manifests.MachineConfigAPIServerHAProxy()
	setMachineConfigLabels(machineConfig)
	serializedConfig, err := apiServerHAProxyConfig(haProxyImage, externalAPIAddress, internalAPIAddress, externalAPIPort, internalAPIPort)
	if err != nil {
		return fmt.Errorf("failed to serialize ignition config: %w", err)
	}
	machineConfig.Spec.Config.Raw = serializedConfig
	return reconcileIgnitionConfigMap(cm, machineConfig, ownerRef)
}

func workerSSHConfig(sshKey string) ([]byte, error) {
	config := &igntypes.Config{}
	config.Ignition.Version = ignitionVersion
	config.Passwd = igntypes.Passwd{
		Users: []igntypes.PasswdUser{
			{
				Name: "core",
			},
		},
	}
	if len(sshKey) > 0 {
		config.Passwd.Users[0].SSHAuthorizedKeys = []igntypes.SSHAuthorizedKey{
			igntypes.SSHAuthorizedKey(sshKey),
		}
	}
	return serializeIgnitionConfig(config)
}

func apiServerHAProxyConfig(haProxyImage, externalAPIAddress, internalAPIAddress string, externalAPIPort, internalAPIPort int32) ([]byte, error) {
	config := &igntypes.Config{}
	config.Ignition.Version = ignitionVersion

	filesToAdd := []struct {
		template *template.Template
		source   func() ([]byte, error)
		name     string
		mode     int
		params   map[string]string
	}{
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
	}

	files := []igntypes.File{}
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
	config.Systemd.Units = []igntypes.Unit{
		apiServerIPUnit(),
	}
	return serializeIgnitionConfig(config)
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
					RunAsUser: pointer.Int64Ptr(1001),
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
					Handler: corev1.Handler{
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

func apiServerIPUnit() igntypes.Unit {
	content := MustAsset("apiserver-haproxy/apiserver-ip.service")
	return igntypes.Unit{
		Name:     "apiserver-ip.service",
		Contents: &content,
		Enabled:  pointer.BoolPtr(true),
	}
}

func serializeIgnitionConfig(cfg *igntypes.Config) ([]byte, error) {
	jsonBytes, err := json.Marshal(cfg)
	if err != nil {
		return nil, fmt.Errorf("error marshaling ignition config: %w", err)
	}
	return jsonBytes, nil
}

func setMachineConfigLabels(mc *mcfgv1.MachineConfig) {
	mc.Labels = map[string]string{
		"machineconfiguration.openshift.io/role": "worker",
	}
}

func reconcileIgnitionConfigMap(cm *corev1.ConfigMap, mc *mcfgv1.MachineConfig, ownerRef config.OwnerRef) error {
	ownerRef.ApplyTo(cm)
	buf := &bytes.Buffer{}
	mc.APIVersion = mcfgv1.SchemeGroupVersion.String()
	mc.Kind = "MachineConfig"
	if err := api.YamlSerializer.Encode(mc, buf); err != nil {
		return fmt.Errorf("failed to serialize machine config %s: %w", cm.Name, err)
	}
	if cm.Labels == nil {
		cm.Labels = map[string]string{}
	}
	cm.Labels["hypershift.openshift.io/core-ignition-config"] = "true"
	cm.Data = map[string]string{
		ignitionConfigKey: buf.String(),
	}
	return nil
}

// fileFromBytes creates an ignition-config file with the given contents.
// copied from openshift-installer
func fileFromBytes(path string, mode int, contents []byte) igntypes.File {
	return igntypes.File{
		Node: igntypes.Node{
			Path:      path,
			Overwrite: pointer.BoolPtr(true),
		},
		FileEmbedded1: igntypes.FileEmbedded1{
			Mode: &mode,
			Contents: igntypes.Resource{
				Source: pointer.StringPtr(dataurl.EncodeBytes(contents)),
			},
		},
	}
}
