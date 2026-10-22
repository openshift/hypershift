package autonode

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"testing"
	"time"

	. "github.com/onsi/gomega"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	hyperkarpenterv1 "github.com/openshift/hypershift/api/karpenter/v1"
	karpenterutil "github.com/openshift/hypershift/support/karpenter"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/ec2"
	ec2types "github.com/aws/aws-sdk-go-v2/service/ec2/types"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/uuid"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/utils/ptr"

	crclient "sigs.k8s.io/controller-runtime/pkg/client"
	karpenterv1 "sigs.k8s.io/karpenter/pkg/apis/v1"
)

type localZoneOptions struct {
	zone, subnet, instanceType, volumeType string
}

type localZoneEC2 interface {
	DescribeAvailabilityZones(context.Context, *ec2.DescribeAvailabilityZonesInput, ...func(*ec2.Options)) (*ec2.DescribeAvailabilityZonesOutput, error)
	DescribeSubnets(context.Context, *ec2.DescribeSubnetsInput, ...func(*ec2.Options)) (*ec2.DescribeSubnetsOutput, error)
	DescribeInstanceTypeOfferings(context.Context, *ec2.DescribeInstanceTypeOfferingsInput, ...func(*ec2.Options)) (*ec2.DescribeInstanceTypeOfferingsOutput, error)
	DescribeInstanceTypes(context.Context, *ec2.DescribeInstanceTypesInput, ...func(*ec2.Options)) (*ec2.DescribeInstanceTypesOutput, error)
	DescribeInstances(context.Context, *ec2.DescribeInstancesInput, ...func(*ec2.Options)) (*ec2.DescribeInstancesOutput, error)
}

func localZoneOptionsFromEnv(getenv func(string) string) (localZoneOptions, error) {
	options := localZoneOptions{
		zone: getenv("E2E_AUTONODE_LOCAL_ZONE"), subnet: getenv("E2E_AUTONODE_LOCAL_ZONE_SUBNET_ID"),
		instanceType: getenv("E2E_AUTONODE_LOCAL_ZONE_INSTANCE_TYPE"), volumeType: getenv("E2E_AUTONODE_LOCAL_ZONE_VOLUME_TYPE"),
	}
	if options == (localZoneOptions{}) {
		return options, nil
	}
	if options.zone == "" || options.instanceType == "" || options.volumeType == "" {
		return options, fmt.Errorf("local zone coverage requires E2E_AUTONODE_LOCAL_ZONE, E2E_AUTONODE_LOCAL_ZONE_INSTANCE_TYPE and E2E_AUTONODE_LOCAL_ZONE_VOLUME_TYPE; v2 also requires E2E_AUTONODE_LOCAL_ZONE_SUBNET_ID")
	}
	if options.volumeType != "gp2" && options.volumeType != "gp3" {
		return options, fmt.Errorf("local zone root volume type must be gp2 or gp3, supported by the selected zone; got %q", options.volumeType)
	}
	return options, nil
}

func validateLocalZone(options localZoneOptions, region, vpc string, zones []ec2types.AvailabilityZone, subnets []ec2types.Subnet, offerings []ec2types.InstanceTypeOffering) error {
	if len(zones) != 1 || aws.ToString(zones[0].ZoneName) != options.zone || aws.ToString(zones[0].ZoneType) != "local-zone" || aws.ToString(zones[0].RegionName) != region || zones[0].State != ec2types.AvailabilityZoneStateAvailable || zones[0].OptInStatus != ec2types.AvailabilityZoneOptInStatusOptedIn {
		return fmt.Errorf("requested zone %s must be an available, already opted-in Local Zone in region %s: %+v", options.zone, region, zones)
	}
	if options.subnet != "" && (len(subnets) != 1 || aws.ToString(subnets[0].SubnetId) != options.subnet || aws.ToString(subnets[0].VpcId) != vpc || aws.ToString(subnets[0].AvailabilityZone) != options.zone || subnets[0].State != ec2types.SubnetStateAvailable) {
		return fmt.Errorf("subnet %s must be available in VPC %s and Local Zone %s", options.subnet, vpc, options.zone)
	}
	for _, offering := range offerings {
		if aws.ToString(offering.Location) == options.zone && string(offering.InstanceType) == options.instanceType {
			return nil
		}
	}
	return fmt.Errorf("instance type %s is not offered in Local Zone %s", options.instanceType, options.zone)
}

func localZoneResources(name, infraID string, options localZoneOptions) (*hyperkarpenterv1.OpenshiftEC2NodeClass, *karpenterv1.NodePool, *corev1.Pod) {
	nodeClass := &hyperkarpenterv1.OpenshiftEC2NodeClass{
		ObjectMeta: metav1.ObjectMeta{Name: name},
		Spec: hyperkarpenterv1.OpenshiftEC2NodeClassSpec{
			SubnetSelectorTerms:        []hyperkarpenterv1.SubnetSelectorTerm{{ID: options.subnet}},
			SecurityGroupSelectorTerms: []hyperkarpenterv1.SecurityGroupSelectorTerm{{Tags: map[string]string{"karpenter.sh/discovery": infraID}}},
			BlockDeviceMappings: []hyperkarpenterv1.BlockDeviceMapping{{DeviceName: "/dev/xvda", EBS: hyperkarpenterv1.BlockDevice{
				VolumeSizeGiB: 120, VolumeType: hyperkarpenterv1.VolumeType(strings.ToUpper(options.volumeType)), DeleteOnTermination: hyperkarpenterv1.DeleteOnTerminationPolicyDelete,
			}}},
		},
	}
	nodePool := &karpenterv1.NodePool{
		ObjectMeta: metav1.ObjectMeta{Name: name},
		Spec: karpenterv1.NodePoolSpec{
			Disruption: karpenterv1.Disruption{ConsolidateAfter: karpenterv1.MustParseNillableDuration("Never")},
			Template: karpenterv1.NodeClaimTemplate{Spec: karpenterv1.NodeClaimTemplateSpec{
				NodeClassRef: &karpenterv1.NodeClassReference{Group: "karpenter.k8s.aws", Kind: "EC2NodeClass", Name: name},
				Requirements: []karpenterv1.NodeSelectorRequirementWithMinValues{
					{Key: corev1.LabelTopologyZone, Operator: corev1.NodeSelectorOpIn, Values: []string{options.zone}},
					{Key: corev1.LabelInstanceTypeStable, Operator: corev1.NodeSelectorOpIn, Values: []string{options.instanceType}},
					{Key: corev1.LabelArchStable, Operator: corev1.NodeSelectorOpIn, Values: []string{"amd64"}},
					{Key: karpenterv1.CapacityTypeLabelKey, Operator: corev1.NodeSelectorOpIn, Values: []string{karpenterv1.CapacityTypeOnDemand}},
				},
				Taints: []corev1.Taint{{Key: "hypershift.io/test", Value: name, Effect: corev1.TaintEffectNoSchedule}},
			}},
		},
	}
	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: "default"},
		Spec: corev1.PodSpec{
			RestartPolicy:   corev1.RestartPolicyNever,
			NodeSelector:    map[string]string{karpenterv1.NodePoolLabelKey: name, corev1.LabelTopologyZone: options.zone},
			Tolerations:     []corev1.Toleration{{Key: "hypershift.io/test", Operator: corev1.TolerationOpEqual, Value: name, Effect: corev1.TaintEffectNoSchedule}},
			SecurityContext: &corev1.PodSecurityContext{RunAsNonRoot: ptr.To(true), RunAsUser: ptr.To(int64(1000)), SeccompProfile: &corev1.SeccompProfile{Type: corev1.SeccompProfileTypeRuntimeDefault}},
			Containers: []corev1.Container{{Name: "workload", Image: "quay.io/openshift/origin-pod:4.22.0", Command: []string{"/bin/sh", "-c", "printf 'Local Zone workload completed\\n'"},
				Resources:       corev1.ResourceRequirements{Requests: corev1.ResourceList{corev1.ResourceCPU: resource.MustParse("250m"), corev1.ResourceMemory: resource.MustParse("256Mi")}},
				SecurityContext: &corev1.SecurityContext{AllowPrivilegeEscalation: ptr.To(false), Capabilities: &corev1.Capabilities{Drop: []corev1.Capability{"ALL"}}},
			}},
		},
	}
	return nodeClass, nodePool, pod
}

func validateLocalZoneNode(node *corev1.Node, existing []corev1.Node, pool, zone string) error {
	for _, previous := range existing {
		if node.UID == previous.UID || node.Name == previous.Name || node.Spec.ProviderID == previous.Spec.ProviderID {
			return fmt.Errorf("workload ran on pre-existing node %s", node.Name)
		}
	}
	if node.UID == "" || node.Spec.ProviderID == "" || node.DeletionTimestamp != nil || node.Labels[corev1.LabelTopologyZone] != zone || node.Labels[karpenterv1.NodePoolLabelKey] != pool {
		return fmt.Errorf("node %s must be new, not deleting, and belong to pool %s in zone %s", node.Name, pool, zone)
	}
	for _, condition := range node.Status.Conditions {
		if condition.Type == corev1.NodeReady && condition.Status == corev1.ConditionTrue {
			return nil
		}
	}
	return fmt.Errorf("node %s has not joined Ready: %+v", node.Name, node.Status.Conditions)
}

// RunLocalZoneTest provisions a new AutoNode in an explicitly configured Local Zone subnet
// and requires a successful workload on that Ready node. It skips only when all Local Zone inputs
// are absent, never opts an account into a zone, and never modifies a supplied subnet.
// If no subnet is supplied, createSubnet must prepare one in the hosted cluster VPC
// and return its cleanup function; that cleanup runs after node teardown.
func RunLocalZoneTest(ctx context.Context, t testing.TB, client crclient.Client, hc *hyperv1.HostedCluster, ec2client localZoneEC2, getenv func(string) string, createSubnet func(context.Context, string) (string, func())) {
	t.Helper()
	g := NewWithT(t)
	ctx, cancel := context.WithTimeout(ctx, 20*time.Minute)
	defer cancel()
	options, err := localZoneOptionsFromEnv(getenv)
	g.Expect(err).NotTo(HaveOccurred(), "parse Local Zone test inputs")
	if options == (localZoneOptions{}) {
		t.Skip("Local Zone coverage not requested; set E2E_AUTONODE_LOCAL_ZONE and its subnet, instance and volume inputs")
	}
	g.Expect(hc.Spec.Platform.Type).To(Equal(hyperv1.AWSPlatform))
	g.Expect(hc.Spec.Platform.AWS).NotTo(BeNil())
	g.Expect(karpenterutil.IsKarpenterEnabled(hc.Spec.AutoNode)).To(BeTrue(), "requested Local Zone test requires AutoNode")
	g.Expect(hc.Spec.Platform.AWS.CloudProviderConfig).NotTo(BeNil())
	zones, err := ec2client.DescribeAvailabilityZones(ctx, &ec2.DescribeAvailabilityZonesInput{AllAvailabilityZones: ptr.To(true), ZoneNames: []string{options.zone}})
	g.Expect(err).NotTo(HaveOccurred(), "describe requested Local Zone; no opt-in is performed")
	offerings, err := ec2client.DescribeInstanceTypeOfferings(ctx, &ec2.DescribeInstanceTypeOfferingsInput{LocationType: ec2types.LocationTypeAvailabilityZone, Filters: []ec2types.Filter{
		{Name: ptr.To("location"), Values: []string{options.zone}}, {Name: ptr.To("instance-type"), Values: []string{options.instanceType}},
	}})
	g.Expect(err).NotTo(HaveOccurred(), "describe instance offerings in the requested Local Zone")
	instanceTypes, err := ec2client.DescribeInstanceTypes(ctx, &ec2.DescribeInstanceTypesInput{InstanceTypes: []ec2types.InstanceType{ec2types.InstanceType(options.instanceType)}})
	g.Expect(err).NotTo(HaveOccurred(), "describe the requested instance type")
	g.Expect(instanceTypes.InstanceTypes).To(HaveLen(1), "describe must resolve exactly one requested instance type")
	g.Expect(instanceTypes.InstanceTypes[0].ProcessorInfo).NotTo(BeNil(), "requested instance type must report processor capabilities")
	g.Expect(instanceTypes.InstanceTypes[0].ProcessorInfo.SupportedArchitectures).To(ContainElement(ec2types.ArchitectureTypeX8664))
	if options.subnet == "" {
		g.Expect(createSubnet).NotTo(BeNil(), "this entry point requires E2E_AUTONODE_LOCAL_ZONE_SUBNET_ID")
		g.Expect(validateLocalZone(options, hc.Spec.Platform.AWS.Region, hc.Spec.Platform.AWS.CloudProviderConfig.VPC, zones.AvailabilityZones, nil, offerings.InstanceTypeOfferings)).To(Succeed())
		var cleanup func()
		options.subnet, cleanup = createSubnet(ctx, options.zone)
		t.Cleanup(cleanup)
		g.Expect(options.subnet).NotTo(BeEmpty())
	}
	subnets, err := ec2client.DescribeSubnets(ctx, &ec2.DescribeSubnetsInput{SubnetIds: []string{options.subnet}})
	g.Expect(err).NotTo(HaveOccurred(), "describe the selected Local Zone subnet")
	g.Expect(validateLocalZone(options, hc.Spec.Platform.AWS.Region, hc.Spec.Platform.AWS.CloudProviderConfig.VPC, zones.AvailabilityZones, subnets.Subnets, offerings.InstanceTypeOfferings)).To(Succeed())

	before := &corev1.NodeList{}
	g.Expect(client.List(ctx, before)).To(Succeed(), "snapshot existing nodes before Local Zone provisioning")
	name := "local-zone-" + string(uuid.NewUUID())[:8]
	nodeClass, nodePool, pod := localZoneResources(name, hc.Spec.InfraID, options)
	for _, object := range []crclient.Object{nodeClass, nodePool, pod} {
		g.Expect(client.Create(ctx, object)).To(Succeed(), "create Local Zone test resource %T", object)
		t.Cleanup(func() {
			cleanupCtx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
			defer cancel()
			if err := client.Delete(cleanupCtx, object); err != nil && !apierrors.IsNotFound(err) {
				t.Errorf("cleanup: delete %T %s: %v", object, object.GetName(), err)
				return
			}
			err := wait.PollUntilContextTimeout(cleanupCtx, 5*time.Second, 5*time.Minute, true, func(ctx context.Context) (bool, error) {
				err := client.Get(ctx, crclient.ObjectKeyFromObject(object), object)
				if apierrors.IsNotFound(err) {
					return true, nil
				}
				return false, err
			})
			if err != nil {
				t.Errorf("cleanup: waiting for %T %s deletion: %v", object, object.GetName(), err)
			}
			if object == nodePool {
				g.Eventually(func(g Gomega) {
					claims := &karpenterv1.NodeClaimList{}
					g.Expect(client.List(cleanupCtx, claims, crclient.MatchingLabels{karpenterv1.NodePoolLabelKey: name})).To(Succeed())
					g.Expect(claims.Items).To(BeEmpty(), "wait for instance termination before deleting NodeClass")
				}, 5*time.Minute, 5*time.Second).Should(Succeed())
			}
		})
	}
	err = wait.PollUntilContextTimeout(ctx, 10*time.Second, 15*time.Minute, true, func(ctx context.Context) (bool, error) {
		if err := client.Get(ctx, crclient.ObjectKeyFromObject(pod), pod); err != nil {
			return false, err
		}
		if pod.Status.Phase == corev1.PodFailed {
			return false, fmt.Errorf("local zone workload failed: %+v", pod.Status)
		}
		return pod.Status.Phase == corev1.PodSucceeded, nil
	})
	g.Expect(err).NotTo(HaveOccurred(), "Local Zone bootstrap/join/workload did not complete: %+v", pod.Status)
	node := &corev1.Node{}
	g.Eventually(func(g Gomega) {
		g.Expect(client.Get(ctx, types.NamespacedName{Name: pod.Spec.NodeName}, node)).To(Succeed(), "get the node assigned to the completed Local Zone workload")
		g.Expect(validateLocalZoneNode(node, before.Items, name, options.zone)).To(Succeed())
	}, 2*time.Minute, 5*time.Second).Should(Succeed())
	instanceID := node.Spec.ProviderID[strings.LastIndex(node.Spec.ProviderID, "/")+1:]
	instances, err := ec2client.DescribeInstances(ctx, &ec2.DescribeInstancesInput{InstanceIds: []string{instanceID}})
	g.Expect(err).NotTo(HaveOccurred(), "describe the instance backing the new workload node")
	g.Expect(instances.Reservations).To(HaveLen(1), "node provider ID must resolve to exactly one reservation")
	g.Expect(instances.Reservations[0].Instances).To(HaveLen(1), "node provider ID must resolve to exactly one instance")
	instance := instances.Reservations[0].Instances[0]
	g.Expect(aws.ToString(instance.SubnetId)).To(Equal(options.subnet))
	g.Expect(instance.Placement).NotTo(BeNil())
	g.Expect(aws.ToString(instance.Placement.AvailabilityZone)).To(Equal(options.zone))
	g.Expect(string(instance.InstanceType)).To(Equal(options.instanceType))
	g.Expect(slices.ContainsFunc(pod.Status.ContainerStatuses, func(status corev1.ContainerStatus) bool {
		return status.Name == "workload" && status.State.Terminated != nil && status.State.Terminated.ExitCode == 0
	})).To(BeTrue(), "workload must exit successfully on the new Ready Local Zone node")
	t.Log("New Local Zone node joined Ready; exact EC2 placement verified and workload exited successfully")
}
