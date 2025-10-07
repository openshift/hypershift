package kubevirt

import (
	"bytes"
	"fmt"
	"sort"
	"strings"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	component "github.com/openshift/hypershift/support/controlplane-component"

	corev1 "k8s.io/api/core/v1"

	sigyaml "sigs.k8s.io/yaml"
)

type StorageSnapshotMapping struct {
	VolumeSnapshotClasses []string `yaml:"volumeSnapshotClasses,omitempty"`
	StorageClasses        []string `yaml:"storageClasses"`
}

func adaptConfigMap(cpContext component.WorkloadContext, cm *corev1.ConfigMap) error {
	hcp := cpContext.HCP
	storageDriverType := getStorageDriverType(hcp)

	var storageClassEnforcement string

	switch storageDriverType {
	case hyperv1.ManualKubevirtStorageDriverConfigType:
		allowedSC := []string{}
		storageMap := make(map[string][]string)
		snapshotMap := make(map[string][]string)

		if hcp.Spec.Platform.Kubevirt.StorageDriver.Manual != nil {
			for _, mapping := range hcp.Spec.Platform.Kubevirt.StorageDriver.Manual.StorageClassMapping {
				allowedSC = append(allowedSC, mapping.InfraStorageClassName)
				storageMap[mapping.Group] = append(storageMap[mapping.Group], mapping.InfraStorageClassName)
			}
			for _, mapping := range hcp.Spec.Platform.Kubevirt.StorageDriver.Manual.VolumeSnapshotClassMapping {
				snapshotMap[mapping.Group] = append(snapshotMap[mapping.Group], mapping.InfraVolumeSnapshotClassName)
			}
		}

		storageSnapshotMapping := []StorageSnapshotMapping{}

		// Sort storage map keys to ensure consistent ordering
		storageGroups := make([]string, 0, len(storageMap))
		for group := range storageMap {
			storageGroups = append(storageGroups, group)
		}
		sort.Strings(storageGroups)

		for _, group := range storageGroups {
			storageClasses := storageMap[group]
			// Sort storage classes within each group for consistency
			sort.Strings(storageClasses)

			mapping := StorageSnapshotMapping{}
			mapping.StorageClasses = storageClasses
			mapping.VolumeSnapshotClasses = snapshotMap[group]
			// Sort volume snapshot classes within each group for consistency
			if mapping.VolumeSnapshotClasses != nil {
				sort.Strings(mapping.VolumeSnapshotClasses)
			}
			delete(snapshotMap, group)
			storageSnapshotMapping = append(storageSnapshotMapping, mapping)
		}

		// Sort remaining snapshot map keys to ensure consistent ordering
		snapshotGroups := make([]string, 0, len(snapshotMap))
		for group := range snapshotMap {
			snapshotGroups = append(snapshotGroups, group)
		}
		sort.Strings(snapshotGroups)

		for _, group := range snapshotGroups {
			snapshotClasses := snapshotMap[group]
			// Sort snapshot classes for consistency
			sort.Strings(snapshotClasses)

			mapping := StorageSnapshotMapping{}
			mapping.VolumeSnapshotClasses = snapshotClasses
			storageSnapshotMapping = append(storageSnapshotMapping, mapping)
		}
		mappingBytes, err := sigyaml.Marshal(storageSnapshotMapping)
		if err != nil {
			return err
		}
		// For some reason yaml.Marhsal is generating upper case keys, so we need to convert them to lower case
		mappingBytes = bytes.ReplaceAll(mappingBytes, []byte("VolumeSnapshotClasses"), []byte("volumeSnapshotClasses"))
		mappingBytes = bytes.ReplaceAll(mappingBytes, []byte("StorageClasses"), []byte("storageClasses"))

		// Sort allowedSC to ensure consistent ordering in allowList
		sort.Strings(allowedSC)
		storageClassEnforcement = fmt.Sprintf("allowAll: false\nallowList: [%s]\nstorageSnapshotMapping: \n%s", strings.Join(allowedSC, ", "), string(mappingBytes))
	case hyperv1.NoneKubevirtStorageDriverConfigType:
		storageClassEnforcement = "allowDefault: false\nallowAll: false\n"
	case hyperv1.DefaultKubevirtStorageDriverConfigType:
		storageClassEnforcement = "allowDefault: true\nallowAll: false\n"
	default:
		storageClassEnforcement = "allowDefault: true\nallowAll: false\n"
	}
	var infraClusterNamespace string
	if isExternalInfraKubvirt(hcp) {
		infraClusterNamespace = hcp.Spec.Platform.Kubevirt.Credentials.InfraNamespace
	} else {
		infraClusterNamespace = cm.Namespace
	}

	cm.Data = map[string]string{
		"infraClusterNamespace":        infraClusterNamespace,
		"infraClusterLabels":           fmt.Sprintf("%s=%s", hyperv1.InfraIDLabel, hcp.Spec.InfraID),
		"infraStorageClassEnforcement": storageClassEnforcement,
	}
	return nil
}
