package openstack

import (
	"context"
	"fmt"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"sigs.k8s.io/controller-runtime/pkg/client"

	orc "github.com/k-orc/openstack-resource-controller/api/v1alpha1"
	"github.com/openshift/hypershift/support/openstackutil"
	"github.com/openshift/hypershift/support/releaseinfo"
	"github.com/openshift/hypershift/support/upsert"
	"k8s.io/utils/ptr"
	capiopenstackv1beta1 "sigs.k8s.io/cluster-api-provider-openstack/api/v1beta1"
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
		releaseVersion, err := releaseinfo.OpenStackReleaseImage(releaseImage)
		if err != nil {
			return nil, err
		}
		openStackMachineTemplate.Template.Spec.Image.ImageRef = &capiopenstackv1beta1.ResourceReference{
			Name: "rhcos-" + releaseVersion + "-" + hcluster.Name,
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
			switch port.PortSecurityPolicy {
			case hyperv1.PortSecurityEnabled:
				additionalPorts[i].ResolvedPortSpecFields.DisablePortSecurity = ptr.To(false)
			case hyperv1.PortSecurityDisabled:
				additionalPorts[i].ResolvedPortSpecFields.DisablePortSecurity = ptr.To(true)
			case hyperv1.PortSecurityDefault:
				additionalPorts[i].ResolvedPortSpecFields.DisablePortSecurity = ptr.To(false)
			default:
				additionalPorts[i].ResolvedPortSpecFields.DisablePortSecurity = ptr.To(false)
			}
		}
		openStackMachineTemplate.Template.Spec.Ports = append(openStackMachineTemplate.Template.Spec.Ports, additionalPorts...)
	}
	return openStackMachineTemplate, nil
}

func ReconcileOpenStackImageCR(ctx context.Context, client client.Client, createOrUpdate upsert.CreateOrUpdateFN, hcluster *hyperv1.HostedCluster, release *releaseinfo.ReleaseImage) error {
	releaseVersion, err := releaseinfo.OpenStackReleaseImage(release)
	if err != nil {
		return err
	}
	openStackImage := orc.Image{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "rhcos-" + releaseVersion + "-" + hcluster.Name,
			Namespace: hcluster.Namespace,
			// TODO: add proper cleanup in CAPI resources cleanup
			OwnerReferences: []metav1.OwnerReference{
				{
					APIVersion: hcluster.APIVersion,
					Kind:       hcluster.Kind,
					Name:       hcluster.Name,
					UID:        hcluster.UID,
				},
			},
		},
		Spec: orc.ImageSpec{},
	}

	if _, err := createOrUpdate(ctx, client, &openStackImage, func() error {
		err := reconcileOpenStackImageSpec(hcluster, &openStackImage.Spec, release)
		if err != nil {
			return err
		}
		return nil
	}); err != nil {
		return err
	}
	return nil
}

func reconcileOpenStackImageSpec(hcluster *hyperv1.HostedCluster, openStackImageSpec *orc.ImageSpec, release *releaseinfo.ReleaseImage) error {
	imageURL, imageHash, err := releaseinfo.UnsupportedOpenstackDefaultImage(release)
	if err != nil {
		return fmt.Errorf("failed to lookup RHCOS image: %w", err)
	}

	openStackImageSpec.CloudCredentialsRef = orc.CloudCredentialsReference{
		SecretName: hcluster.Spec.Platform.OpenStack.IdentityRef.Name,
		CloudName:  hcluster.Spec.Platform.OpenStack.IdentityRef.CloudName,
	}
	releaseVersion, err := releaseinfo.OpenStackReleaseImage(release)
	if err != nil {
		return err
	}

	openStackImageSpec.Resource = &orc.ImageResourceSpec{
		Name: "rhcos-" + releaseVersion + "-" + hcluster.Name,
		Content: &orc.ImageContent{
			DiskFormat: "qcow2",
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
