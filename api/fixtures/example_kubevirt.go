package fixtures

import (
	"fmt"

	hyperv1 "github.com/openshift/hypershift/api/v1alpha1"
	apiresource "k8s.io/apimachinery/pkg/api/resource"
)

func ExampleKubeVirtTemplate(o *ExampleKubevirtOptions) *hyperv1.KubevirtNodePoolPlatform {
	var storageClassName *string
	volumeSize := apiresource.MustParse(fmt.Sprintf("%vGi", o.RootVolumeSize))

	if o.RootVolumeStorageClass != "" {
		storageClassName = &o.RootVolumeStorageClass
	}

	exampleTemplate := &hyperv1.KubevirtNodePoolPlatform{
		RootVolume: &hyperv1.KubevirtRootVolume{
			Image: &hyperv1.KubevirtDiskImage{
				ContainerDiskImage: &o.Image,
			},
			KubevirtVolume: hyperv1.KubevirtVolume{
				Type: hyperv1.KubevirtVolumeTypePersistent,
				Persistent: &hyperv1.KubevirtPersistentVolume{
					Size:         &volumeSize,
					StorageClass: storageClassName,
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

	return exampleTemplate
}
