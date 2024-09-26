package nodepool

import (
	"fmt"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	"github.com/openshift/hypershift/support/releaseinfo"
	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	capipowervs "sigs.k8s.io/cluster-api-provider-ibmcloud/api/v1beta2"
	capiv1 "sigs.k8s.io/cluster-api/api/v1beta1"
)

const (
	defaultCOSRegion = "us-south"
)

// getImageRegion returns the nearest region os IBM COS bucket for the RHCOS images
func getImageRegion(region string) string {
	switch region {
	case "dal", "us-south":
		return "us-south"
	case "eu-de":
		return "eu-de"
	case "lon":
		return "eu-gb"
	case "osa":
		return "jp-osa"
	case "syd":
		return "au-syd"
	case "sao":
		return "br-sao"
	case "tor":
		return "ca-tor"
	case "tok":
		return "jp-tok"
	case "us-east":
		return "us-east"
	default:
		return defaultCOSRegion
	}
}

func ibmPowerVSMachineTemplateSpec(hcluster *hyperv1.HostedCluster, nodePool *hyperv1.NodePool, releaseImage *releaseinfo.ReleaseImage) (*capipowervs.IBMPowerVSMachineTemplateSpec, error) {
	// Validate PowerVS platform specific input
	var coreOSPowerVSImage *releaseinfo.CoreOSPowerVSImage
	coreOSPowerVSImage, _, err := getPowerVSImage(hcluster.Spec.Platform.PowerVS.Region, releaseImage)
	if err != nil {
		return nil, fmt.Errorf("couldn't discover a PowerVS Image for release image: %w", err)
	}
	powerVSBootImage := coreOSPowerVSImage.Release

	var image *capipowervs.IBMPowerVSResourceReference
	var imageRef *v1.LocalObjectReference
	if nodePool.Spec.Platform.PowerVS.Image != nil {
		image = &capipowervs.IBMPowerVSResourceReference{
			ID:   nodePool.Spec.Platform.PowerVS.Image.ID,
			Name: nodePool.Spec.Platform.PowerVS.Image.Name,
		}
	} else {
		imageRef = &v1.LocalObjectReference{
			Name: powerVSBootImage,
		}
	}
	subnet := capipowervs.IBMPowerVSResourceReference{
		ID:   hcluster.Spec.Platform.PowerVS.Subnet.ID,
		Name: hcluster.Spec.Platform.PowerVS.Subnet.Name,
	}
	return &capipowervs.IBMPowerVSMachineTemplateSpec{
		Template: capipowervs.IBMPowerVSMachineTemplateResource{
			Spec: capipowervs.IBMPowerVSMachineSpec{
				ServiceInstanceID: hcluster.Spec.Platform.PowerVS.ServiceInstanceID,
				Image:             image,
				ImageRef:          imageRef,
				Network:           subnet,
				SystemType:        nodePool.Spec.Platform.PowerVS.SystemType,
				ProcessorType:     capipowervs.PowerVSProcessorType(nodePool.Spec.Platform.PowerVS.ProcessorType.CastToCAPIPowerVSProcessorType()),
				Processors:        nodePool.Spec.Platform.PowerVS.Processors,
				MemoryGiB:         nodePool.Spec.Platform.PowerVS.MemoryGiB,
			},
		},
	}, nil
}

func getPowerVSImage(region string, releaseImage *releaseinfo.ReleaseImage) (*releaseinfo.CoreOSPowerVSImage, string, error) {
	arch, foundArch := releaseImage.StreamMetadata.Architectures["ppc64le"]
	if !foundArch {
		return nil, "", fmt.Errorf("couldn't find OS metadata for architecture %q", "ppc64le")
	}

	COSRegion := getImageRegion(region)

	regionData, hasRegionData := arch.Images.PowerVS.Regions[COSRegion]
	if !hasRegionData {
		return nil, "", fmt.Errorf("couldn't find PowerVS image for region %q", COSRegion)
	}
	return &regionData, COSRegion, nil
}

func IBMPowerVSImage(namespace, name string) *capipowervs.IBMPowerVSImage {
	return &capipowervs.IBMPowerVSImage{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: namespace,
			Name:      name,
		},
	}
}

func reconcileIBMPowerVSImage(ibmPowerVSImage *capipowervs.IBMPowerVSImage, hcluster *hyperv1.HostedCluster, nodePool *hyperv1.NodePool, infraID, region string, img *releaseinfo.CoreOSPowerVSImage) error {
	if ibmPowerVSImage.Annotations == nil {
		ibmPowerVSImage.Annotations = make(map[string]string)
	}
	ibmPowerVSImage.Annotations[capiv1.ClusterNameLabel] = infraID

	ibmPowerVSImage.Spec = capipowervs.IBMPowerVSImageSpec{
		ClusterName:       hcluster.Name,
		ServiceInstanceID: hcluster.Spec.Platform.PowerVS.ServiceInstanceID,
		Bucket:            &img.Bucket,
		Object:            &img.Object,
		Region:            &region,
		StorageType:       string(nodePool.Spec.Platform.PowerVS.StorageType),
		DeletePolicy:      string(nodePool.Spec.Platform.PowerVS.ImageDeletePolicy),
	}
	return nil
}
