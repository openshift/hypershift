package nodepool

import (
	"fmt"
	"strconv"

	hyperv1 "github.com/openshift/hypershift/api/v1alpha1"
	"github.com/openshift/hypershift/support/releaseinfo"
	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	capipowervs "sigs.k8s.io/cluster-api-provider-ibmcloud/api/v1beta1"
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

func ibmPowerVSMachineTemplateSpec(hcluster *hyperv1.HostedCluster, nodePool *hyperv1.NodePool, powerVSbootImage string) *capipowervs.IBMPowerVSMachineTemplateSpec {
	var image *capipowervs.IBMPowerVSResourceReference
	var imageRef *v1.LocalObjectReference
	if nodePool.Spec.Platform.PowerVS.Image != nil {
		image = &capipowervs.IBMPowerVSResourceReference{
			ID:   nodePool.Spec.Platform.PowerVS.Image.ID,
			Name: nodePool.Spec.Platform.PowerVS.Image.Name,
		}
	} else {
		imageRef = &v1.LocalObjectReference{
			Name: powerVSbootImage,
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
				SysType:           nodePool.Spec.Platform.PowerVS.SystemType,
				ProcType:          nodePool.Spec.Platform.PowerVS.ProcessorType,
				Processors:        nodePool.Spec.Platform.PowerVS.Processors.String(),
				Memory:            strconv.Itoa(int(nodePool.Spec.Platform.PowerVS.MemoryGiB)),
			},
		},
	}
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
	ibmPowerVSImage.Annotations[capiv1.ClusterLabelName] = infraID

	ibmPowerVSImage.Spec = capipowervs.IBMPowerVSImageSpec{
		ClusterName:       hcluster.Name,
		ServiceInstanceID: hcluster.Spec.Platform.PowerVS.ServiceInstanceID,
		Bucket:            &img.Bucket,
		Object:            &img.Object,
		Region:            &region,
		StorageType:       nodePool.Spec.Platform.PowerVS.StorageType,
		DeletePolicy:      nodePool.Spec.Platform.PowerVS.ImageDeletePolicy,
	}
	return nil
}
