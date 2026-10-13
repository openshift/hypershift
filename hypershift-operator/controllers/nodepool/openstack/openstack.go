package openstack

import (
	"context"
	"fmt"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	"github.com/openshift/hypershift/support/openstackutil"
	"github.com/openshift/hypershift/support/releaseinfo"

	"k8s.io/apimachinery/pkg/types"
	"k8s.io/utils/ptr"

	capiopenstackv1beta1 "sigs.k8s.io/cluster-api-provider-openstack/api/v1beta1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	orc "github.com/k-orc/openstack-resource-controller/api/v1alpha1"
)

func MachineTemplateSpec(hcluster *hyperv1.HostedCluster, nodePool *hyperv1.NodePool, releaseImage *releaseinfo.ReleaseImage) (*capiopenstackv1beta1.OpenStackMachineTemplateSpec, error) {
	openStackMachineTemplate := &capiopenstackv1beta1.OpenStackMachineTemplateSpec{Template: capiopenstackv1beta1.OpenStackMachineTemplateResource{Spec: capiopenstackv1beta1.OpenStackMachineSpec{
		Flavor: ptr.To(nodePool.Spec.Platform.OpenStack.Flavor),
	}}}

	if nodePool.Spec.Platform.OpenStack.ImageName != "" {
		openStackMachineTemplate.Template.Spec.Image.Filter = &capiopenstackv1beta1.ImageFilter{
			Name: ptr.To(nodePool.Spec.Platform.OpenStack.ImageName),
		}
	} else {
		releaseVersion, err := OpenStackReleaseImage(releaseImage)
		if err != nil {
			return nil, err
		}
		openStackMachineTemplate.Template.Spec.Image.ImageRef = &capiopenstackv1beta1.ResourceReference{
			Name: "rhcos-" + releaseVersion,
		}
	}

	// TODO: add support for BYO network/subnet
	if len(hcluster.Spec.Platform.OpenStack.Subnets) == 0 && len(nodePool.Spec.Platform.OpenStack.AdditionalPorts) > 0 {
		// Initialize the ports slice with an empty port which will be used as the primary port.
		// CAPO will figure out the network and subnet for this port since they are not provided.
		ports := []capiopenstackv1beta1.PortOpts{{}}
		openStackMachineTemplate.Template.Spec.Ports = append(openStackMachineTemplate.Template.Spec.Ports, ports...)

		additionalPorts := make([]capiopenstackv1beta1.PortOpts, len(nodePool.Spec.Platform.OpenStack.AdditionalPorts))
		for i, port := range nodePool.Spec.Platform.OpenStack.AdditionalPorts {
			additionalPorts[i] = capiopenstackv1beta1.PortOpts{}
			additionalPorts[i].Description = ptr.To("Additional port for Hypershift node pool " + nodePool.Name)
			if port.Network != nil {
				additionalPorts[i].Network = &capiopenstackv1beta1.NetworkParam{}
				if port.Network.Filter != nil {
					additionalPorts[i].Network.Filter = openstackutil.CreateCAPONetworkFilter(port.Network.Filter)
				}
				if port.Network.ID != nil {
					additionalPorts[i].Network.ID = port.Network.ID
				}
			}
			for _, allowedAddressPair := range port.AllowedAddressPairs {
				additionalPorts[i].ResolvedPortSpecFields.AllowedAddressPairs = []capiopenstackv1beta1.AddressPair{}
				additionalPorts[i].ResolvedPortSpecFields.AllowedAddressPairs = append(additionalPorts[i].ResolvedPortSpecFields.AllowedAddressPairs, capiopenstackv1beta1.AddressPair{
					IPAddress: allowedAddressPair.IPAddress,
				})
			}
			if port.VNICType != "" {
				additionalPorts[i].ResolvedPortSpecFields.VNICType = &port.VNICType
			}
			// OCPBUGS-54763 was reported because we were initially setting port security every time
			// but in most cases the network is now owned by the project so the default policy
			// wouldn't allow the port to be created with a port security option, whether it's enabled
			// or disabled. So we need to set the port security policy only if it's explicitly set in the
			// additional port spec.
			switch port.PortSecurityPolicy {
			case hyperv1.PortSecurityEnabled:
				additionalPorts[i].ResolvedPortSpecFields.DisablePortSecurity = ptr.To(false)
			case hyperv1.PortSecurityDisabled:
				additionalPorts[i].ResolvedPortSpecFields.DisablePortSecurity = ptr.To(true)
			}
		}
		openStackMachineTemplate.Template.Spec.Ports = append(openStackMachineTemplate.Template.Spec.Ports, additionalPorts...)
	}
	return openStackMachineTemplate, nil
}

func GetOpenStackClusterForHostedCluster(ctx context.Context, c client.Client, hcluster *hyperv1.HostedCluster, controlPlaneNamespace string) (capiopenstackv1beta1.OpenStackCluster, error) {
	cluster := capiopenstackv1beta1.OpenStackCluster{}

	if err := c.Get(ctx, types.NamespacedName{Namespace: controlPlaneNamespace, Name: hcluster.Name}, &cluster); err != nil {
		return cluster, fmt.Errorf("failed to get Cluster: %w", err)
	}

	return cluster, nil
}

// ReconcileOpenStackImageSpec reconciles the OpenStack ImageSpec for the given HostedCluster.
// The image spec will be set to the default RHCOS image for the given release.
func ReconcileOpenStackImageSpec(hcluster *hyperv1.HostedCluster, openStackImageSpec *orc.ImageSpec, release *releaseinfo.ReleaseImage) error {
	imageURL, imageHash, err := OpenstackDefaultImage(release)
	if err != nil {
		return fmt.Errorf("failed to lookup RHCOS image: %w", err)
	}

	openStackImageSpec.CloudCredentialsRef = orc.CloudCredentialsReference{
		SecretName: hcluster.Spec.Platform.OpenStack.IdentityRef.Name,
		CloudName:  hcluster.Spec.Platform.OpenStack.IdentityRef.CloudName,
	}

	imageName, err := PrefixedClusterImageName(hcluster, release)
	if err != nil {
		return fmt.Errorf("failed to get image name: %w", err)
	}

	openStackImageSpec.Resource = &orc.ImageResourceSpec{
		Name: imageName,
		Content: &orc.ImageContent{
			ContainerFormat: "bare",
			DiskFormat:      "qcow2",
			Download: &orc.ImageContentSourceDownload{
				URL:        imageURL,
				Decompress: ptr.To(orc.ImageCompressionGZ),
				Hash: &orc.ImageHash{
					Algorithm: "sha256",
					Value:     imageHash,
				},
			},
		},
	}

	return nil
}

// OpenstackDefaultImage returns the default RHCOS image for the given release.
// The image URL and SHA256 hash are returned.
func OpenstackDefaultImage(releaseImage *releaseinfo.ReleaseImage) (string, string, error) {
	arch, foundArch := releaseImage.StreamMetadata.Architectures["x86_64"]
	if !foundArch {
		return "", "", fmt.Errorf("couldn't find OS metadata for architecture %q", "x86_64")
	}
	openStack, exists := arch.Artifacts["openstack"]
	if !exists {
		return "", "", fmt.Errorf("couldn't find OS metadata for openstack")
	}
	artifact, exists := openStack.Formats["qcow2.gz"]
	if !exists {
		return "", "", fmt.Errorf("couldn't find OS metadata for openstack qcow2.gz")
	}
	disk, exists := artifact["disk"]
	if !exists {
		return "", "", fmt.Errorf("couldn't find OS metadata for the openstack qcow2.gz disk")
	}

	return disk.Location, disk.SHA256, nil
}

// OpenStackReleaseImage returns the release version for the OpenStack image.
// The release version is extracted from the release metadata.
func OpenStackReleaseImage(releaseImage *releaseinfo.ReleaseImage) (string, error) {
	arch, foundArch := releaseImage.StreamMetadata.Architectures["x86_64"]
	if !foundArch {
		return "", fmt.Errorf("couldn't find OS metadata for architecture %q", "x86_64")
	}
	openStack, exists := arch.Artifacts["openstack"]
	if !exists {
		return "", fmt.Errorf("couldn't find OS metadata for openstack")
	}
	return openStack.Release, nil
}

// PrefixedClusterImageName returns a prefixed name of the image for the given HostedCluster.
func PrefixedClusterImageName(hcluster *hyperv1.HostedCluster, releaseImage *releaseinfo.ReleaseImage) (string, error) {
	releaseVersion, err := OpenStackReleaseImage(releaseImage)
	if err != nil {
		return "", err
	}
	return hcluster.Name + "-rhcos-" + releaseVersion, nil
}
