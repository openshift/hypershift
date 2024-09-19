package nodepool

import (
	"fmt"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	"github.com/openshift/hypershift/support/releaseinfo"
	k8sutilspointer "k8s.io/utils/pointer"
	capiaws "sigs.k8s.io/cluster-api-provider-aws/v2/api/v1beta2"
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

func awsMachineTemplateSpec(infraName string, hostedCluster *hyperv1.HostedCluster, nodePool *hyperv1.NodePool, defaultSG bool, releaseImage *releaseinfo.ReleaseImage) (*capiaws.AWSMachineTemplateSpec, error) {

	var ami string
	region := hostedCluster.Spec.Platform.AWS.Region
	arch := nodePool.Spec.Arch
	if nodePool.Spec.Platform.AWS.AMI != "" {
		ami = nodePool.Spec.Platform.AWS.AMI
	} else {
		// TODO: Should the region be included in the NodePool platform information?
		var err error
		ami, err = defaultNodePoolAMI(region, arch, releaseImage)
		if err != nil {
			return nil, fmt.Errorf("couldn't discover an AMI for release image: %w", err)
		}
	}

	subnet := &capiaws.AWSResourceReference{}
	subnet.ID = nodePool.Spec.Platform.AWS.Subnet.ID
	for k := range nodePool.Spec.Platform.AWS.Subnet.Filters {
		filter := capiaws.Filter{
			Name:   nodePool.Spec.Platform.AWS.Subnet.Filters[k].Name,
			Values: nodePool.Spec.Platform.AWS.Subnet.Filters[k].Values,
		}
		subnet.Filters = append(subnet.Filters, filter)
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

		rootVolume.Encrypted = nodePool.Spec.Platform.AWS.RootVolume.Encrypted
		rootVolume.EncryptionKey = nodePool.Spec.Platform.AWS.RootVolume.EncryptionKey
	}

	securityGroups := []capiaws.AWSResourceReference{}
	for _, sg := range nodePool.Spec.Platform.AWS.SecurityGroups {
		var filters []capiaws.Filter
		for _, f := range sg.Filters {
			filters = append(filters, capiaws.Filter{
				Name:   f.Name,
				Values: f.Values,
			})
		}
		securityGroups = append(securityGroups, capiaws.AWSResourceReference{
			ID:      sg.ID,
			Filters: filters,
		})
	}
	if defaultSG {
		if hostedCluster.Status.Platform == nil || hostedCluster.Status.Platform.AWS == nil || hostedCluster.Status.Platform.AWS.DefaultWorkerSecurityGroupID == "" {
			return nil, &NotReadyError{fmt.Errorf("the default security group for the HostedCluster has not been created")}
		}
		sgID := hostedCluster.Status.Platform.AWS.DefaultWorkerSecurityGroupID
		securityGroups = append(securityGroups, capiaws.AWSResourceReference{
			ID: &sgID,
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

	instanceMetadataOptions := &capiaws.InstanceMetadataOptions{
		HTTPTokens:              capiaws.HTTPTokensStateOptional,
		HTTPPutResponseHopLimit: 2, // set to 2 as per AWS recommendation for container envs https://docs.aws.amazon.com/AWSEC2/latest/UserGuide/instancedata-data-retrieval.html#imds-considerations
		HTTPEndpoint:            capiaws.InstanceMetadataEndpointStateEnabled,
		InstanceMetadataTags:    capiaws.InstanceMetadataEndpointStateDisabled,
	}
	if value, found := nodePool.Annotations[ec2InstanceMetadataHTTPTokensAnnotation]; found && value == string(capiaws.HTTPTokensStateRequired) {
		instanceMetadataOptions.HTTPTokens = capiaws.HTTPTokensStateRequired
	}

	tenancy := ""
	if nodePool.Spec.Platform.AWS.Placement != nil {
		tenancy = nodePool.Spec.Platform.AWS.Placement.Tenancy
	}

	awsMachineTemplateSpec := &capiaws.AWSMachineTemplateSpec{
		Template: capiaws.AWSMachineTemplateResource{
			Spec: capiaws.AWSMachineSpec{
				UncompressedUserData: k8sutilspointer.Bool(true),
				CloudInit: capiaws.CloudInit{
					InsecureSkipSecretsManager: true,
					SecureSecretsBackend:       "secrets-manager",
				},
				IAMInstanceProfile: instanceProfile,
				InstanceType:       instanceType,
				AMI: capiaws.AMIReference{
					ID: k8sutilspointer.String(ami),
				},
				AdditionalSecurityGroups: securityGroups,
				Subnet:                   subnet,
				RootVolume:               rootVolume,
				AdditionalTags:           tags,
				InstanceMetadataOptions:  instanceMetadataOptions,
				Tenancy:                  tenancy,
			},
		},
	}

	return awsMachineTemplateSpec, nil
}
