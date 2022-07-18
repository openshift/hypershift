package fixtures

import (
	"fmt"
	"strings"

	hyperv1 "github.com/openshift/hypershift/api/v1alpha1"
	apiresource "k8s.io/apimachinery/pkg/api/resource"
)

type ExampleKubevirtOptions struct {
	ServicePublishingStrategy string
	APIServerAddress          string
	Memory                    string
	Cores                     uint32
	Image                     string
	RootVolumeSize            uint32
	RootVolumeStorageClass    string
	RootVolumeAccessModes     string
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
		Compute: &hyperv1.KubevirtCompute{},
	}

	if o.Memory != "" {
		memory := apiresource.MustParse(o.Memory)
		exampleTemplate.Compute.Memory = &memory
	}
	if o.Cores != 0 {
		exampleTemplate.Compute.Cores = &o.Cores
	}

	if o.Image != "" {
		exampleTemplate.RootVolume.Image = &hyperv1.KubevirtDiskImage{
			ContainerDiskImage: &o.Image,
		}
	}

	return exampleTemplate
}
