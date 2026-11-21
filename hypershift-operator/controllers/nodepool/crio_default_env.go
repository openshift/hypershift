package nodepool

import (
	"fmt"
	"strings"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	"github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/ignition"
	"github.com/openshift/hypershift/support/api"

	mcfgv1 "github.com/openshift/api/machineconfiguration/v1"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/utils/ptr"

	"github.com/clarketm/json"
	ignitionapi "github.com/coreos/ignition/v2/config/v3_2/types"
	"github.com/vincent-petithory/dataurl"
)

const (
	crioDefaultEnvMachineConfigName = "50-crio-default-env"
	crioDefaultEnvDropinPath        = "/etc/crio/crio.conf.d/50-default-env.conf"
)

// GenerateCRIODefaultEnvMachineConfig generates a MachineConfig that writes a
// CRI-O drop-in configuration file setting default_env for all containers on
// the node. Returns an empty string when envVars is empty.
func GenerateCRIODefaultEnvMachineConfig(nodePool *hyperv1.NodePool) (string, error) {
	if nodePool == nil || len(nodePool.Spec.ContainerRuntimeDefaultEnv) == 0 {
		return "", nil
	}

	content := renderCRIODefaultEnvConfig(nodePool.Spec.ContainerRuntimeDefaultEnv)

	mode := 0644
	file := ignitionapi.File{
		Node: ignitionapi.Node{
			Path:      crioDefaultEnvDropinPath,
			Overwrite: ptr.To(true),
		},
		FileEmbedded1: ignitionapi.FileEmbedded1{
			Mode: &mode,
			Contents: ignitionapi.Resource{
				Source: ptr.To(dataurl.EncodeBytes(content)),
			},
		},
	}

	ignConfig := ignitionapi.Config{
		Ignition: ignitionapi.Ignition{
			Version: "3.2.0",
		},
		Storage: ignitionapi.Storage{
			Files: []ignitionapi.File{file},
		},
	}

	serializedConfig, err := json.Marshal(&ignConfig)
	if err != nil {
		return "", fmt.Errorf("failed to serialize crio default env ignition config: %w", err)
	}

	mc := &mcfgv1.MachineConfig{
		ObjectMeta: metav1.ObjectMeta{
			Name: crioDefaultEnvMachineConfigName,
		},
	}
	ignition.SetMachineConfigLabels(mc)
	mc.Spec.Config.Raw = serializedConfig

	mc.APIVersion = mcfgv1.SchemeGroupVersion.String()
	mc.Kind = "MachineConfig"

	encoded, err := api.CompatibleYAMLEncode(mc, api.YamlSerializer)
	if err != nil {
		return "", fmt.Errorf("failed to serialize crio default env machine config: %w", err)
	}

	return string(encoded), nil
}

// renderCRIODefaultEnvConfig produces a CRI-O drop-in configuration fragment
// that sets the default_env option.
func renderCRIODefaultEnvConfig(envVars []corev1.EnvVar) []byte {
	var entries []string
	for _, ev := range envVars {
		entries = append(entries, fmt.Sprintf("    %q,", ev.Name+"="+ev.Value))
	}

	var b strings.Builder
	b.WriteString("[crio.runtime]\n")
	b.WriteString("default_env = [\n")
	for _, entry := range entries {
		b.WriteString(entry)
		b.WriteByte('\n')
	}
	b.WriteString("]\n")

	return []byte(b.String())
}
