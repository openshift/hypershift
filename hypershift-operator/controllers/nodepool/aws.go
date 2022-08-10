package nodepool

import (
	"fmt"

	hyperv1 "github.com/openshift/hypershift/api/v1alpha1"
	k8sutilspointer "k8s.io/utils/pointer"
	capiaws "sigs.k8s.io/cluster-api-provider-aws/api/v1beta1"
)

const (
	// infraLifecycleOwned is the value we use when tagging infra resources to indicate
	// that the resource is considered owned and managed by the cluster.
	infraLifecycleOwned = "owned"
)

// awsClusterCloudProviderTagKey generates the key for infra resources associated to a cluster.
// https://github.com/kubernetes/cloud-provider-aws/blob/5f394ba297bf280ceb3edfc38922630b4bd83f46/pkg/providers/v2/tags.go#L31-L37
func awsClusterCloudProviderTagKey(id string) string {
	return fmt.Sprintf("kubernetes.io/cluster/%s", id)
}

func awsMachineTemplateSpec(infraName, ami string, hostedCluster *hyperv1.HostedCluster, nodePool *hyperv1.NodePool) *capiaws.AWSMachineTemplateSpec {
	subnet := &capiaws.AWSResourceReference{}
	if nodePool.Spec.Platform.AWS.Subnet != nil {
		subnet.ID = nodePool.Spec.Platform.AWS.Subnet.ID
		subnet.ARN = nodePool.Spec.Platform.AWS.Subnet.ARN
		for k := range nodePool.Spec.Platform.AWS.Subnet.Filters {
			filter := capiaws.Filter{
				Name:   nodePool.Spec.Platform.AWS.Subnet.Filters[k].Name,
				Values: nodePool.Spec.Platform.AWS.Subnet.Filters[k].Values,
			}
			subnet.Filters = append(subnet.Filters, filter)
		}
	}
	rootVolume := &capiaws.Volume{
		Size: EC2VolumeDefaultSize,
	}
	if nodePool.Spec.Platform.AWS.RootVolume != nil {
		if nodePool.Spec.Platform.AWS.RootVolume.Type != "" {
			rootVolume.Type = capiaws.VolumeType(nodePool.Spec.Platform.AWS.RootVolume.Type)
		} else {
			rootVolume.Type = capiaws.VolumeType(EC2VolumeDefaultType)
		}
		if nodePool.Spec.Platform.AWS.RootVolume.Size > 0 {
			rootVolume.Size = nodePool.Spec.Platform.AWS.RootVolume.Size
		}
		if nodePool.Spec.Platform.AWS.RootVolume.IOPS > 0 {
			rootVolume.IOPS = nodePool.Spec.Platform.AWS.RootVolume.IOPS
		}
	}

	securityGroups := []capiaws.AWSResourceReference{}
	for _, sg := range nodePool.Spec.Platform.AWS.SecurityGroups {
		filters := []capiaws.Filter{}
		for _, f := range sg.Filters {
			filters = append(filters, capiaws.Filter{
				Name:   f.Name,
				Values: f.Values,
			})
		}
		securityGroups = append(securityGroups, capiaws.AWSResourceReference{
			ARN:     sg.ARN,
			ID:      sg.ID,
			Filters: filters,
		})
	}

	instanceProfile := fmt.Sprintf("%s-worker-profile", infraName)
	if nodePool.Spec.Platform.AWS.InstanceProfile != "" {
		instanceProfile = nodePool.Spec.Platform.AWS.InstanceProfile
	}

	instanceType := nodePool.Spec.Platform.AWS.InstanceType

	tags := capiaws.Tags{}
	for _, tag := range append(nodePool.Spec.Platform.AWS.ResourceTags, hostedCluster.Spec.Platform.AWS.ResourceTags...) {
		tags[tag.Key] = tag.Value
	}

	// We enforce the AWS cluster cloud provider tag here.
	// Otherwise, this would race with the HC defaulting itself hostedCluster.Spec.Platform.AWS.ResourceTags.
	key := awsClusterCloudProviderTagKey(infraName)
	if _, ok := tags[key]; !ok {
		tags[key] = infraLifecycleOwned
	}

	awsMachineTemplateSpec := &capiaws.AWSMachineTemplateSpec{
		Template: capiaws.AWSMachineTemplateResource{
			Spec: capiaws.AWSMachineSpec{
				UncompressedUserData: k8sutilspointer.BoolPtr(true),
				CloudInit: capiaws.CloudInit{
					InsecureSkipSecretsManager: true,
					SecureSecretsBackend:       "secrets-manager",
				},
				IAMInstanceProfile: instanceProfile,
				InstanceType:       instanceType,
				AMI: capiaws.AMIReference{
					ID: k8sutilspointer.StringPtr(ami),
				},
				AdditionalSecurityGroups: securityGroups,
				Subnet:                   subnet,
				RootVolume:               rootVolume,
				AdditionalTags:           tags,
			},
		},
	}

	return awsMachineTemplateSpec
}
