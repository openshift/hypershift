package nodepool

import (
	"context"
	"fmt"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	"github.com/openshift/hypershift/support/releaseinfo"

	"github.com/aws/aws-sdk-go/service/ec2"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/errors"
	"k8s.io/utils/ptr"

	capiaws "sigs.k8s.io/cluster-api-provider-aws/v2/api/v1beta2"
	capiv1 "sigs.k8s.io/cluster-api/api/v1beta1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"

	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"
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

	instanceMetadataOptions := &capiaws.InstanceMetadataOptions{
		HTTPTokens:              capiaws.HTTPTokensStateOptional,
		HTTPPutResponseHopLimit: 2, // set to 2 as per AWS recommendation for container envs https://docs.aws.amazon.com/AWSEC2/latest/UserGuide/instancedata-data-retrieval.html#imds-considerations
		HTTPEndpoint:            capiaws.InstanceMetadataEndpointStateEnabled,
		InstanceMetadataTags:    capiaws.InstanceMetadataEndpointStateDisabled,
	}
	if value, found := nodePool.Annotations[ec2InstanceMetadataHTTPTokensAnnotation]; found && value == string(capiaws.HTTPTokensStateRequired) {
		instanceMetadataOptions.HTTPTokens = capiaws.HTTPTokensStateRequired
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
				AdditionalTags:           awsAdditionalTags(nodePool, hostedCluster, infraName),
				InstanceMetadataOptions:  instanceMetadataOptions,
			},
		},
	}

	if placement := nodePool.Spec.Platform.AWS.Placement; placement != nil {
		awsMachineTemplateSpec.Template.Spec.Tenancy = placement.Tenancy

		if capacityReservation := placement.CapacityReservation; capacityReservation != nil {
			awsMachineTemplateSpec.Template.Spec.CapacityReservationID = capacityReservation.ID

			switch capacityReservation.MarketType {
			case hyperv1.MarketTypeCapacityBlock:
				awsMachineTemplateSpec.Template.Spec.MarketType = capiaws.MarketTypeCapacityBlock
			case hyperv1.MarketTypeOnDemand:
				awsMachineTemplateSpec.Template.Spec.MarketType = capiaws.MarketTypeOnDemand
			default:
				if placement.Tenancy != "host" && capacityReservation.ID != nil {
					// if the tenancy is not host and the ID is set, default the market type to CapacityBlock
					awsMachineTemplateSpec.Template.Spec.MarketType = capiaws.MarketTypeCapacityBlock
				}
			}

			awsMachineTemplateSpec.Template.Spec.CapacityReservationPreference = capiaws.CapacityReservationPreference(capacityReservation.Preference)
		}
	}

	if nodePool.Spec.Platform.AWS.Placement != nil && nodePool.Spec.Platform.AWS.Placement.SpotMarketOptions != nil {
		awsMachineTemplateSpec.Template.Spec.SpotMarketOptions = &capiaws.SpotMarketOptions{
			MaxPrice: nodePool.Spec.Platform.AWS.Placement.SpotMarketOptions.MaxPrice,
		}
		awsMachineTemplateSpec.Template.Spec.MarketType = capiaws.MarketTypeSpot
	}

	if hostedCluster.Annotations[hyperv1.AWSMachinePublicIPs] == "true" {
		awsMachineTemplateSpec.Template.Spec.PublicIP = ptr.To(true)
	}

	return awsMachineTemplateSpec, nil
}

func awsAdditionalTags(nodePool *hyperv1.NodePool, hostedCluster *hyperv1.HostedCluster, infraName string) capiaws.Tags {
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
	return tags
}

func (c *CAPI) awsMachineTemplate(ctx context.Context, templateNameGenerator func(spec any) (string, error)) (*capiaws.AWSMachineTemplate, error) {
	desiredSpec, err := awsMachineTemplateSpec(c.capiClusterName, c.hostedCluster, c.nodePool, c.cpoCapabilities.CreateDefaultAWSSecurityGroup, c.releaseImage)
	if err != nil {
		return nil, fmt.Errorf("failed to generate AWSMachineTemplateSpec: %w", err)
	}

	hashedSpec := *desiredSpec.DeepCopy()
	// set tags to nil so that it doesn't get considered in the hash calculation for the MachineTemplate name.
	// this is to avoid a rolling upgrade when tags are changed.
	hashedSpec.Template.Spec.AdditionalTags = nil
	templateName, err := templateNameGenerator(hashedSpec)
	if err != nil {
		return nil, fmt.Errorf("failed to generate template name: %w", err)
	}

	existingTemplate := &capiaws.AWSMachineTemplate{}
	if err := c.getExistingMachineTemplate(ctx, existingTemplate); err == nil {
		opts := cmp.Options{
			cmpopts.IgnoreFields(capiaws.AWSMachineSpec{}, "AdditionalTags"),
		}

		if cmp.Equal(*desiredSpec, existingTemplate.Spec, opts...) {
			// If a template already exist and the spec has not changed (excluding AdditionalTags), we should reuse the existing template name.
			// This is especially important for clusters created before the change that omitted the AdditionalTags from template name generation (https://github.com/openshift/hypershift/pull/6285).
			// Otherwise, we would end up with a new template name and trigger a rolling upgrade.
			templateName = existingTemplate.Name
		}
	} else if client.IgnoreNotFound(err) != nil {
		return nil, fmt.Errorf("failed to get existing AWSMachineTemplate: %w", err)
	}

	template := &capiaws.AWSMachineTemplate{
		ObjectMeta: metav1.ObjectMeta{
			Name: templateName,
		},
		Spec: *desiredSpec,
	}

	return template, nil
}

func (c *CAPI) reconcileAWSMachines(ctx context.Context) error {
	awsMachines := &capiaws.AWSMachineList{}
	if err := c.List(ctx, awsMachines, client.InNamespace(c.controlplaneNamespace), client.MatchingLabels{
		capiv1.MachineDeploymentNameLabel: c.nodePool.Name,
	}); err != nil {
		return fmt.Errorf("failed to list AWSMachines for NodePool %s: %w", c.nodePool.Name, err)
	}

	var errs []error
	for _, machine := range awsMachines.Items {
		if _, err := controllerutil.CreateOrPatch(ctx, c.Client, &machine, func() error {
			machine.Spec.AdditionalTags = awsAdditionalTags(c.nodePool, c.hostedCluster, c.capiClusterName)
			return nil
		}); err != nil {
			errs = append(errs, fmt.Errorf("failed to reconcile AWSMachine %s: %w", machine.Name, err))
		}
	}

	return errors.NewAggregate(errs)
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

func (r NodePoolReconciler) validateAWSPlatformConfig(ctx context.Context, nodePool *hyperv1.NodePool, hc *hyperv1.HostedCluster, oldCondition *hyperv1.NodePoolCondition) error {
	if nodePool.Spec.Platform.AWS == nil {
		return fmt.Errorf("aws platform not populated")
	}
	if nodePool.Spec.Platform.AWS.Placement != nil && nodePool.Spec.Platform.AWS.Placement.CapacityReservation != nil {
		pullSecretBytes, err := r.getPullSecretBytes(ctx, hc)
		if err != nil {
			return err
		}
		hostedClusterVersion, err := r.getHostedClusterVersion(ctx, hc, pullSecretBytes)
		if err != nil {
			return fmt.Errorf("failed to get controlPlane version: %w", err)
		}

		if hostedClusterVersion.Major == 4 && hostedClusterVersion.Minor < 19 {
			return fmt.Errorf("capacityReservation is only supported on 4.19+ clusters")
		}
	}

	if nodePool.Spec.Platform.AWS.Placement != nil && nodePool.Spec.Platform.AWS.Placement.SpotMarketOptions != nil {
		if nodePool.Spec.Platform.AWS.Placement.Tenancy != "" && nodePool.Spec.Platform.AWS.Placement.Tenancy != ec2.TenancyDefault {
			return fmt.Errorf("spotMarketOptions is incompatible with capacityReservation and requires tenancy to be 'default' or unset (not 'dedicated' or 'host')")
		}
		if nodePool.Spec.Platform.AWS.Placement.CapacityReservation != nil {
			return fmt.Errorf("spotMarketOptions is incompatible with capacityReservation and requires tenancy to be 'default' or unset (not 'dedicated' or 'host')")
		}
	}

	return nil
}
