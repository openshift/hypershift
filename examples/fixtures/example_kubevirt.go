package fixtures

import (
	"fmt"
	"strings"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	corev1 "k8s.io/api/core/v1"
	apiresource "k8s.io/apimachinery/pkg/api/resource"
)

type ExampleKubevirtOptions struct {
	ServicePublishingStrategy  string
	APIServerAddress           string
	Memory                     string
	Cores                      uint32
	Image                      string
	RootVolumeSize             uint32
	RootVolumeStorageClass     string
	RootVolumeAccessModes      string
	RootVolumeVolumeMode       string
	BaseDomainPassthrough      bool
	InfraKubeConfig            []byte
	InfraNamespace             string
	CacheStrategyType          string
	InfraStorageClassMappings  []string
	NetworkInterfaceMultiQueue *hyperv1.MultiQueueSetting
	QoSClass                   *hyperv1.QoSClass
	AdditionalNetworks         []hyperv1.KubevirtNetwork
	AttachDefaultNetwork       *bool
	VmNodeSelector             map[string]string
}

func ExampleKubeVirtTemplate(o *ExampleKubevirtOptions) *hyperv1.KubevirtNodePoolPlatform {
	var storageClassName *string
	var accessModesStr []string
	var accessModes []hyperv1.PersistentVolumeAccessMode
	volumeSize := apiresource.MustParse(fmt.Sprintf("%vGi", o.RootVolumeSize))

	if o.RootVolumeStorageClass != "" {
		storageClassName = &o.RootVolumeStorageClass
	}

	if o.RootVolumeAccessModes != "" {
		accessModesStr = strings.Split(o.RootVolumeAccessModes, ",")
		for _, ams := range accessModesStr {
			var am hyperv1.PersistentVolumeAccessMode
			am = hyperv1.PersistentVolumeAccessMode(ams)
			accessModes = append(accessModes, am)
		}
	}

	exampleTemplate := &hyperv1.KubevirtNodePoolPlatform{
		RootVolume: &hyperv1.KubevirtRootVolume{
			KubevirtVolume: hyperv1.KubevirtVolume{
				Type: hyperv1.KubevirtVolumeTypePersistent,
				Persistent: &hyperv1.KubevirtPersistentVolume{
					Size:         &volumeSize,
					StorageClass: storageClassName,
					AccessModes:  accessModes,
				},
			},
		},
		Compute:              &hyperv1.KubevirtCompute{},
		AdditionalNetworks:   o.AdditionalNetworks,
		AttachDefaultNetwork: o.AttachDefaultNetwork,
	}

	if o.RootVolumeVolumeMode != "" {
		vm := corev1.PersistentVolumeMode(o.RootVolumeVolumeMode)
		exampleTemplate.RootVolume.KubevirtVolume.Persistent.VolumeMode = &vm
	}

	if o.Memory != "" {
		memory := apiresource.MustParse(o.Memory)
		exampleTemplate.Compute.Memory = &memory
	}
	if o.Cores != 0 {
		exampleTemplate.Compute.Cores = &o.Cores
	}

	if o.QoSClass != nil && *o.QoSClass == hyperv1.QoSClassGuaranteed {
		exampleTemplate.Compute.QosClass = o.QoSClass
	}

	if o.Image != "" {
		exampleTemplate.RootVolume.Image = &hyperv1.KubevirtDiskImage{
			ContainerDiskImage: &o.Image,
		}
	}

	strategyType := hyperv1.KubevirtCachingStrategyType(o.CacheStrategyType)
	if strategyType == hyperv1.KubevirtCachingStrategyNone || strategyType == hyperv1.KubevirtCachingStrategyPVC {
		exampleTemplate.RootVolume.CacheStrategy = &hyperv1.KubevirtCachingStrategy{
			Type: strategyType,
		}
	}

	if o.NetworkInterfaceMultiQueue != nil && *o.NetworkInterfaceMultiQueue == hyperv1.MultiQueueEnable {
		exampleTemplate.NetworkInterfaceMultiQueue = o.NetworkInterfaceMultiQueue
	}

	if o.VmNodeSelector != nil && len(o.VmNodeSelector) > 0 {
		exampleTemplate.NodeSelector = o.VmNodeSelector
	}

	return exampleTemplate
}
