package nodepool

import (
	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	mcfgutil "github.com/openshift/hypershift/hypershift-operator/controllers/nodepool/machineconfig"

	mcfgv1 "github.com/openshift/api/machineconfiguration/v1"

	"sigs.k8s.io/yaml"

	"github.com/blang/semver"
	"github.com/clarketm/json"
	ignitionapi "github.com/coreos/ignition/v2/config/v3_2/types"
)

const (
	crioDefaultUlimitsMachineConfigName = "00-hypershift-crio-default-ulimits"
	crioDefaultUlimitsDropInPath        = "/etc/crio/crio.conf.d/01-ctrcfg-defaultUlimits"
	crioDefaultUlimitsDropInContents    = `[crio.runtime]
default_ulimits = ["nofile=1048576"]
`
)

// CRI-O 1.33 (OCP 4.21) changed the default nofile ulimit. Existing clusters
// are grandfathered to the pre-4.21 value; new clusters keep the new default.
var crioDefaultUlimitsChangeVersion = semver.Version{Major: 4, Minor: 21}

func versionHasCRIODefaultUlimitsChange(version semver.Version) bool {
	return version.Major > crioDefaultUlimitsChangeVersion.Major ||
		(version.Major == crioDefaultUlimitsChangeVersion.Major && version.Minor >= crioDefaultUlimitsChangeVersion.Minor)
}

// defaultUlimitsMigrationRequired reports whether a NodePool is crossing the
// 4.21 boundary where the CRI-O nofile default changed.
func defaultUlimitsMigrationRequired(currentVersion, targetVersion string) bool {
	if currentVersion == "" || targetVersion == "" {
		return false
	}

	current, err := semver.Parse(currentVersion)
	if err != nil {
		return false
	}
	target, err := semver.Parse(targetVersion)
	if err != nil {
		return false
	}

	return !versionHasCRIODefaultUlimitsChange(current) && versionHasCRIODefaultUlimitsChange(target)
}

// shouldPreserveCRIODefaultUlimits returns true once the one-way marker is set,
// or when the NodePool is being upgraded across the 4.21 boundary. The target
// version must still be at least 4.21 so a rollback does not keep the drop-in
// in the older config.
func shouldPreserveCRIODefaultUlimits(nodePool *hyperv1.NodePool, targetVersion string) bool {
	if nodePool == nil {
		return false
	}
	target, err := semver.Parse(targetVersion)
	if err != nil || !versionHasCRIODefaultUlimitsChange(target) {
		return false
	}
	if nodePool.Annotations[CRIODefaultUlimitsPreserveAnnotation] == "true" {
		return true
	}
	return defaultUlimitsMigrationRequired(nodePool.Status.Version, targetVersion)
}

// MarkCRIODefaultUlimitsPreserved makes the migration decision sticky so it
// survives later NodePool upgrades.
func MarkCRIODefaultUlimitsPreserved(nodePool *hyperv1.NodePool, targetVersion string) {
	if !shouldPreserveCRIODefaultUlimits(nodePool, targetVersion) {
		return
	}
	if nodePool.Annotations == nil {
		nodePool.Annotations = map[string]string{}
	}
	nodePool.Annotations[CRIODefaultUlimitsPreserveAnnotation] = "true"
}

func crioDefaultUlimitsIgnitionConfig() ignitionapi.Config {
	return ignitionapi.Config{
		Ignition: ignitionapi.Ignition{Version: ignitionapi.MaxVersion.String()},
		Storage: ignitionapi.Storage{
			Files: []ignitionapi.File{
				mcfgutil.FileFromBytes(crioDefaultUlimitsDropInPath, 0644, []byte(crioDefaultUlimitsDropInContents)),
			},
		},
	}
}

func crioDefaultUlimitsMachineConfig() (*mcfgv1.MachineConfig, error) {
	return mcfgutil.MachineConfigFromIgnition(crioDefaultUlimitsMachineConfigName, crioDefaultUlimitsIgnitionConfig())
}

// machineConfigWritesFile reports whether the encoded MachineConfig manifest
// writes the given file path.
func machineConfigWritesFile(manifest []byte, path string) bool {
	var machineConfig mcfgv1.MachineConfig
	if err := yaml.Unmarshal(manifest, &machineConfig); err != nil {
		return false
	}
	if machineConfig.Kind != "MachineConfig" {
		return false
	}

	var ignitionConfig ignitionapi.Config
	if err := json.Unmarshal(machineConfig.Spec.Config.Raw, &ignitionConfig); err != nil {
		return false
	}
	for _, file := range ignitionConfig.Storage.Files {
		if file.Path == path {
			return true
		}
	}
	return false
}

func crioDefaultUlimitsRawConfig() (string, error) {
	return mcfgutil.MachineConfigYAML(crioDefaultUlimitsMachineConfigName, crioDefaultUlimitsIgnitionConfig())
}
