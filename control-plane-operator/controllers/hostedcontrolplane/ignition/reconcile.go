package ignition

import (
	"bytes"
	"fmt"
	"html/template"

	"github.com/clarketm/json"
	"github.com/vincent-petithory/dataurl"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/utils/pointer"

	igntypes "github.com/coreos/ignition/v2/config/v3_2/types"
	"github.com/openshift/hypershift/control-plane-operator/api"
	"github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/config"
	"github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/manifests"
	mcfgv1 "github.com/openshift/hypershift/thirdparty/machineconfigoperator/pkg/apis/machineconfiguration.openshift.io/v1"
)

const (
	ignitionConfigKey = "config"
	ignitionVersion   = "3.2.0"
)

var (
	setupAPIServerIPScriptTemplate    = template.Must(template.New("setupAPIServerIP").Parse(MustAsset("apiserver-haproxy/setup-apiserver-ip.sh")))
	teardownAPIServerIPScriptTemplate = template.Must(template.New("teardownAPIServerIP").Parse(MustAsset("apiserver-haproxy/teardown-apiserver-ip.sh")))
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

func ReconcileAPIServerHAProxyIgnitionConfig(cm *corev1.ConfigMap, ownerRef config.OwnerRef, internalAPIAddress string) error {
	machineConfig := manifests.MachineConfigAPIServerHAProxy()
	setMachineConfigLabels(machineConfig)
	serializedConfig, err := apiServerHAProxyConfig(internalAPIAddress)
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

func apiServerHAProxyConfig(internalAPIAddress string) ([]byte, error) {
	config := &igntypes.Config{}
	config.Ignition.Version = ignitionVersion

	filesToAdd := []struct {
		template *template.Template
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
	}

	files := []igntypes.File{}
	for _, file := range filesToAdd {
		out := &bytes.Buffer{}
		if err := file.template.Execute(out, file.params); err != nil {
			return nil, err
		}
		files = append(files, fileFromBytes(file.name, file.mode, out.Bytes()))
	}
	config.Storage.Files = files
	config.Systemd.Units = []igntypes.Unit{
		apiServerIPUnit(),
	}
	return serializeIgnitionConfig(config)
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
