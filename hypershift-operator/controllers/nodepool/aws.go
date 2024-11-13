package nodepool

import (
	"context"
	"fmt"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	"github.com/openshift/hypershift/support/releaseinfo"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/utils/ptr"

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
				UncompressedUserData: ptr.To(true),
				CloudInit: capiaws.CloudInit{
					InsecureSkipSecretsManager: true,
					SecureSecretsBackend:       "secrets-manager",
				},
				IAMInstanceProfile: instanceProfile,
				InstanceType:       instanceType,
				AMI: capiaws.AMIReference{
					ID: ptr.To(ami),
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
	if hostedCluster.Annotations[hyperv1.AWSMachinePublicIPs] == "true" {
		awsMachineTemplateSpec.Template.Spec.PublicIP = ptr.To(true)
	}

	return awsMachineTemplateSpec, nil
}

func (r *NodePoolReconciler) setAWSConditions(ctx context.Context, nodePool *hyperv1.NodePool, hcluster *hyperv1.HostedCluster, controlPlaneNamespace string, releaseImage *releaseinfo.ReleaseImage) error {
	if nodePool.Spec.Platform.Type == hyperv1.AWSPlatform {
		if hcluster.Spec.Platform.AWS == nil {
			return fmt.Errorf("the HostedCluster for this NodePool has no .Spec.Platform.AWS, this is unsupported")
		}
		if nodePool.Spec.Platform.AWS.AMI != "" {
			// User-defined AMIs cannot be validated
			removeStatusCondition(&nodePool.Status.Conditions, hyperv1.NodePoolValidPlatformImageType)
		} else {
			// TODO: Should the region be included in the NodePool platform information?
			ami, err := defaultNodePoolAMI(hcluster.Spec.Platform.AWS.Region, nodePool.Spec.Arch, releaseImage)
			if err != nil {
				SetStatusCondition(&nodePool.Status.Conditions, hyperv1.NodePoolCondition{
					Type:               hyperv1.NodePoolValidPlatformImageType,
					Status:             corev1.ConditionFalse,
					Reason:             hyperv1.NodePoolValidationFailedReason,
					Message:            fmt.Sprintf("Couldn't discover an AMI for release image %q: %s", nodePool.Spec.Release.Image, err.Error()),
					ObservedGeneration: nodePool.Generation,
				})
				return fmt.Errorf("couldn't discover an AMI for release image: %w", err)
			}
			SetStatusCondition(&nodePool.Status.Conditions, hyperv1.NodePoolCondition{
				Type:               hyperv1.NodePoolValidPlatformImageType,
				Status:             corev1.ConditionTrue,
				Reason:             hyperv1.AsExpectedReason,
				Message:            fmt.Sprintf("Bootstrap AMI is %q", ami),
				ObservedGeneration: nodePool.Generation,
			})
		}

		if hcluster.Status.Platform == nil || hcluster.Status.Platform.AWS == nil || hcluster.Status.Platform.AWS.DefaultWorkerSecurityGroupID == "" {
			SetStatusCondition(&nodePool.Status.Conditions, hyperv1.NodePoolCondition{
				Type:               hyperv1.NodePoolAWSSecurityGroupAvailableConditionType,
				Status:             corev1.ConditionFalse,
				Reason:             hyperv1.DefaultAWSSecurityGroupNotReadyReason,
				Message:            "Waiting for AWS default security group to be created for hosted cluster",
				ObservedGeneration: nodePool.Generation,
			})
		} else {
			SetStatusCondition(&nodePool.Status.Conditions, hyperv1.NodePoolCondition{
				Type:               hyperv1.NodePoolAWSSecurityGroupAvailableConditionType,
				Status:             corev1.ConditionTrue,
				Reason:             hyperv1.AsExpectedReason,
				Message:            "NodePool has a default security group",
				ObservedGeneration: nodePool.Generation,
			})
		}
	}
	return nil
}
