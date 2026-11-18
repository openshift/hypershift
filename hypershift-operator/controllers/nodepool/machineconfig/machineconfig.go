package machineconfig

import (
	"fmt"

	"github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/ignition"
	"github.com/openshift/hypershift/support/api"

	mcfgv1 "github.com/openshift/api/machineconfiguration/v1"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/utils/ptr"

	"github.com/clarketm/json"
	ignitionapi "github.com/coreos/ignition/v2/config/v3_2/types"
	"github.com/vincent-petithory/dataurl"
)

// FileFromBytes creates an Ignition file that overwrites the destination with
// the given contents.
func FileFromBytes(path string, mode int, contents []byte) ignitionapi.File {
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

// MachineConfigFromIgnition wraps an Ignition config in a worker MachineConfig.
func MachineConfigFromIgnition(name string, config ignitionapi.Config) (*mcfgv1.MachineConfig, error) {
	raw, err := json.Marshal(config)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal %s ignition config: %w", name, err)
	}

	machineConfig := &mcfgv1.MachineConfig{
		TypeMeta: metav1.TypeMeta{
			APIVersion: mcfgv1.SchemeGroupVersion.String(),
			Kind:       "MachineConfig",
		},
		ObjectMeta: metav1.ObjectMeta{Name: name},
		Spec: mcfgv1.MachineConfigSpec{
			Config: runtime.RawExtension{Raw: raw},
		},
	}
	ignition.SetMachineConfigLabels(machineConfig)
	return machineConfig, nil
}

// MachineConfigYAML returns a YAML-encoded worker MachineConfig for the given
// Ignition config.
func MachineConfigYAML(name string, config ignitionapi.Config) (string, error) {
	machineConfig, err := MachineConfigFromIgnition(name, config)
	if err != nil {
		return "", err
	}

	encoded, err := api.CompatibleYAMLEncode(machineConfig, api.YamlSerializer)
	if err != nil {
		return "", fmt.Errorf("failed to serialize %s MachineConfig: %w", name, err)
	}
	return string(encoded), nil
}
