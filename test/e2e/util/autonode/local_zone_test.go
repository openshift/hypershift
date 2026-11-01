package autonode

import (
	"context"
	"fmt"
	"strings"
	"testing"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	hyperkarpenterv1 "github.com/openshift/hypershift/api/karpenter/v1"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/ec2"
	ec2types "github.com/aws/aws-sdk-go-v2/service/ec2/types"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/utils/ptr"

	crclient "sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"
	karpenterapis "sigs.k8s.io/karpenter/pkg/apis"
	karpenterv1 "sigs.k8s.io/karpenter/pkg/apis/v1"
)

func TestLocalZoneOptionsFromEnv(t *testing.T) {
	t.Parallel()
	for _, testCase := range []struct {
		name      string
		values    map[string]string
		wantError bool
	}{
		{name: "When no inputs are set, it should disable Local Zone coverage"},
		{name: "When only a zone is set, it should reject incomplete configuration", values: map[string]string{"E2E_AUTONODE_LOCAL_ZONE": "us-west-2-lax-1a"}, wantError: true},
		{name: "When only a subnet is set, it should reject incomplete configuration", values: map[string]string{"E2E_AUTONODE_LOCAL_ZONE_SUBNET_ID": "subnet-local"}, wantError: true},
		{name: "When all inputs are set, it should accept explicit gp2 storage", values: map[string]string{"E2E_AUTONODE_LOCAL_ZONE": "us-west-2-lax-1a", "E2E_AUTONODE_LOCAL_ZONE_SUBNET_ID": "subnet-local", "E2E_AUTONODE_LOCAL_ZONE_INSTANCE_TYPE": "m5.xlarge", "E2E_AUTONODE_LOCAL_ZONE_VOLUME_TYPE": "gp2"}},
		{name: "When all inputs are set, it should accept explicit gp3 storage", values: map[string]string{"E2E_AUTONODE_LOCAL_ZONE": "us-west-2-lax-1a", "E2E_AUTONODE_LOCAL_ZONE_SUBNET_ID": "subnet-local", "E2E_AUTONODE_LOCAL_ZONE_INSTANCE_TYPE": "m5.xlarge", "E2E_AUTONODE_LOCAL_ZONE_VOLUME_TYPE": "gp3"}},
		{name: "When storage is unsupported, it should reject the configuration", values: map[string]string{"E2E_AUTONODE_LOCAL_ZONE": "us-west-2-lax-1a", "E2E_AUTONODE_LOCAL_ZONE_SUBNET_ID": "subnet-local", "E2E_AUTONODE_LOCAL_ZONE_INSTANCE_TYPE": "m5.xlarge", "E2E_AUTONODE_LOCAL_ZONE_VOLUME_TYPE": "io1"}, wantError: true},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()
			options, err := localZoneOptionsFromEnv(func(name string) string { return testCase.values[name] })
			if (err != nil) != testCase.wantError {
				t.Fatalf("unexpected configuration error: %v", err)
			}
			if err == nil && len(testCase.values) == 0 && options != (localZoneOptions{}) {
				t.Fatalf("expected disabled configuration, got %+v", options)
			}
		})
	}
}

func TestValidateLocalZone(t *testing.T) {
	t.Parallel()
	for _, testCase := range []struct {
		name      string
		mutate    func(*ec2types.AvailabilityZone, *ec2types.Subnet, *ec2types.InstanceTypeOffering)
		wantError bool
	}{
		{name: "When the zone is opted in and resources match, it should accept the configuration"},
		{name: "When the zone is not opted in, it should fail rather than opt in or skip", mutate: func(zone *ec2types.AvailabilityZone, _ *ec2types.Subnet, _ *ec2types.InstanceTypeOffering) {
			zone.OptInStatus = ec2types.AvailabilityZoneOptInStatusNotOptedIn
		}, wantError: true},
		{name: "When a standard AZ is requested, it should reject it", mutate: func(zone *ec2types.AvailabilityZone, _ *ec2types.Subnet, _ *ec2types.InstanceTypeOffering) {
			zone.ZoneType = aws.String("availability-zone")
		}, wantError: true},
		{name: "When the zone is in a different region, it should reject it", mutate: func(zone *ec2types.AvailabilityZone, _ *ec2types.Subnet, _ *ec2types.InstanceTypeOffering) {
			zone.RegionName = aws.String("us-east-1")
		}, wantError: true},
		{name: "When the zone is unavailable, it should reject it", mutate: func(zone *ec2types.AvailabilityZone, _ *ec2types.Subnet, _ *ec2types.InstanceTypeOffering) {
			zone.State = ec2types.AvailabilityZoneStateImpaired
		}, wantError: true},
		{name: "When the subnet is in another VPC, it should reject it", mutate: func(_ *ec2types.AvailabilityZone, subnet *ec2types.Subnet, _ *ec2types.InstanceTypeOffering) {
			subnet.VpcId = aws.String("vpc-other")
		}, wantError: true},
		{name: "When the subnet is in another zone, it should reject it", mutate: func(_ *ec2types.AvailabilityZone, subnet *ec2types.Subnet, _ *ec2types.InstanceTypeOffering) {
			subnet.AvailabilityZone = aws.String("us-west-2a")
		}, wantError: true},
		{name: "When the subnet is unavailable, it should reject it", mutate: func(_ *ec2types.AvailabilityZone, subnet *ec2types.Subnet, _ *ec2types.InstanceTypeOffering) {
			subnet.State = ec2types.SubnetStatePending
		}, wantError: true},
		{name: "When the instance type is not offered, it should reject it", mutate: func(_ *ec2types.AvailabilityZone, _ *ec2types.Subnet, offering *ec2types.InstanceTypeOffering) {
			offering.InstanceType = ec2types.InstanceTypeT3Xlarge
		}, wantError: true},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()
			options := localZoneOptions{zone: "us-west-2-lax-1a", subnet: "subnet-local", instanceType: "m5.xlarge", volumeType: "gp2"}
			zone := ec2types.AvailabilityZone{ZoneName: aws.String(options.zone), ZoneType: aws.String("local-zone"), RegionName: aws.String("us-west-2"), State: ec2types.AvailabilityZoneStateAvailable, OptInStatus: ec2types.AvailabilityZoneOptInStatusOptedIn}
			subnet := ec2types.Subnet{SubnetId: aws.String(options.subnet), AvailabilityZone: aws.String(options.zone), VpcId: aws.String("vpc-test"), State: ec2types.SubnetStateAvailable}
			offering := ec2types.InstanceTypeOffering{Location: aws.String(options.zone), InstanceType: ec2types.InstanceTypeM5Xlarge}
			if testCase.mutate != nil {
				testCase.mutate(&zone, &subnet, &offering)
			}
			err := validateLocalZone(options, "us-west-2", "vpc-test", []ec2types.AvailabilityZone{zone}, []ec2types.Subnet{subnet}, []ec2types.InstanceTypeOffering{offering})
			if (err != nil) != testCase.wantError {
				t.Fatalf("unexpected validation error: %v", err)
			}
			if err := validateLocalZone(options, "us-west-2", "vpc-test", nil, nil, nil); err == nil {
				t.Fatal("missing capability data must not pass")
			}
		})
	}
}

func TestLocalZoneResources(t *testing.T) {
	t.Parallel()
	for _, volumeType := range []string{"gp2", "gp3"} {
		t.Run("When "+volumeType+" storage is selected, it should constrain provisioning and workload placement", func(t *testing.T) {
			t.Parallel()
			options := localZoneOptions{zone: "us-west-2-lax-1a", subnet: "subnet-local", instanceType: "m5.xlarge", volumeType: volumeType}
			nodeClass, pool, pod := localZoneResources("local-zone-test", "infra-test", options)
			if nodeClass.Spec.SubnetSelectorTerms[0].ID != options.subnet || nodeClass.Spec.SecurityGroupSelectorTerms[0].Tags["karpenter.sh/discovery"] != "infra-test" {
				t.Fatal("NodeClass must select only the requested subnet and cluster security group")
			}
			mapping := nodeClass.Spec.BlockDeviceMappings[0]
			if mapping.DeviceName != "/dev/xvda" || string(mapping.EBS.VolumeType) != strings.ToUpper(volumeType) || mapping.EBS.VolumeSizeGiB != 120 || mapping.EBS.DeleteOnTermination != hyperkarpenterv1.DeleteOnTerminationPolicyDelete {
				t.Fatalf("unexpected root storage: %+v", mapping)
			}
			if pool.Spec.Template.Spec.NodeClassRef.Name != nodeClass.Name {
				t.Fatal("pool must reference isolated NodeClass")
			}
			expected := map[string]string{corev1.LabelTopologyZone: options.zone, corev1.LabelInstanceTypeStable: options.instanceType, corev1.LabelArchStable: "amd64", karpenterv1.CapacityTypeLabelKey: karpenterv1.CapacityTypeOnDemand}
			for _, requirement := range pool.Spec.Template.Spec.Requirements {
				if len(requirement.Values) != 1 || requirement.Values[0] != expected[requirement.Key] || requirement.Operator != corev1.NodeSelectorOpIn {
					t.Fatalf("unexpected pool requirement %+v", requirement)
				}
				delete(expected, requirement.Key)
			}
			if len(expected) != 0 {
				t.Fatalf("missing pool constraints: %v", expected)
			}
			if pod.Spec.NodeSelector[karpenterv1.NodePoolLabelKey] != pool.Name || pod.Spec.NodeSelector[corev1.LabelTopologyZone] != options.zone || pod.Spec.RestartPolicy != corev1.RestartPolicyNever {
				t.Fatal("workload must complete on isolated Local Zone pool")
			}
			if pool.Spec.Template.Spec.Taints[0].Value != pod.Spec.Tolerations[0].Value {
				t.Fatal("workload must tolerate isolated pool taint")
			}
		})
	}
}

func TestValidateLocalZoneNode(t *testing.T) {
	t.Parallel()
	for _, testCase := range []struct {
		name      string
		mutate    func(*corev1.Node)
		existing  []corev1.Node
		wantError bool
	}{
		{name: "When a new node joins Ready in the requested zone, it should succeed"},
		{name: "When a node has only been created, it should fail", mutate: func(node *corev1.Node) { node.Status.Conditions = nil }, wantError: true},
		{name: "When a node is NotReady, it should fail", mutate: func(node *corev1.Node) { node.Status.Conditions[0].Status = corev1.ConditionFalse }, wantError: true},
		{name: "When a node is in a standard AZ, it should fail", mutate: func(node *corev1.Node) { node.Labels[corev1.LabelTopologyZone] = "us-west-2a" }, wantError: true},
		{name: "When a node belongs to another pool, it should fail", mutate: func(node *corev1.Node) { node.Labels[karpenterv1.NodePoolLabelKey] = "other" }, wantError: true},
		{name: "When a node is deleting, it should fail", mutate: func(node *corev1.Node) { now := metav1.Now(); node.DeletionTimestamp = &now }, wantError: true},
		{name: "When a node existed before provisioning, it should fail", existing: []corev1.Node{{ObjectMeta: metav1.ObjectMeta{UID: "new-node"}}}, wantError: true},
		{name: "When a node name was reused, it should fail", existing: []corev1.Node{{ObjectMeta: metav1.ObjectMeta{Name: "new", UID: "old-node"}}}, wantError: true},
		{name: "When a node has no UID, it should fail", mutate: func(node *corev1.Node) { node.UID = "" }, wantError: true},
		{name: "When a node has no provider ID, it should fail", mutate: func(node *corev1.Node) { node.Spec.ProviderID = "" }, wantError: true},
		{name: "When readiness is unknown, it should fail", mutate: func(node *corev1.Node) { node.Status.Conditions[0].Status = corev1.ConditionUnknown }, wantError: true},
		{name: "When an instance was reused, it should fail", existing: []corev1.Node{{Spec: corev1.NodeSpec{ProviderID: "aws:///us-west-2-lax-1a/i-new"}}}, wantError: true},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()
			node := &corev1.Node{ObjectMeta: metav1.ObjectMeta{Name: "new", UID: "new-node", Labels: map[string]string{corev1.LabelTopologyZone: "us-west-2-lax-1a", karpenterv1.NodePoolLabelKey: "local-pool"}}, Spec: corev1.NodeSpec{ProviderID: "aws:///us-west-2-lax-1a/i-new"}, Status: corev1.NodeStatus{Conditions: []corev1.NodeCondition{{Type: corev1.NodeReady, Status: corev1.ConditionTrue}}}}
			if testCase.mutate != nil {
				testCase.mutate(node)
			}
			if err := validateLocalZoneNode(node, testCase.existing, "local-pool", "us-west-2-lax-1a"); (err != nil) != testCase.wantError {
				t.Fatalf("unexpected node validation result: %v", err)
			}
		})
	}
}

func TestRunLocalZoneTest(t *testing.T) {
	t.Run("When coverage is not configured, it should skip without accessing AWS or Kubernetes", func(t *testing.T) {
		RunLocalZoneTest(context.Background(), t, nil, nil, nil, func(string) string { return "" })
		t.Fatal("unconfigured coverage must skip")
	})
	for _, testCase := range []struct {
		name, failure string
		createSubnet  bool
	}{
		{name: "When configured correctly, it should verify bootstrap workload and EC2 placement"},
		{name: "When no subnet is supplied, it should create and clean up a subnet", createSubnet: true},
		{name: "When subnet association fails, it should clean up the owned subnet", createSubnet: true, failure: "associate"},
		{name: "When provisioning fails after subnet setup, it should clean up both", createSubnet: true, failure: "pool"},
		{name: "When NodePool cleanup fails, it should retain the subnet and report the failure", createSubnet: true, failure: "pool-delete"},
		{name: "When the requested zone is not local, it should fail before provisioning", failure: "zone"},
		{name: "When NodePool creation fails, it should clean up the NodeClass", failure: "pool"},
		{name: "When the workload fails, it should fail promptly and clean up resources", failure: "pod"},
		{name: "When EC2 placement is wrong, it should fail and clean up resources", failure: "placement"},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			scheme := runtime.NewScheme()
			for _, add := range []func(*runtime.Scheme) error{corev1.AddToScheme, hyperkarpenterv1.AddToScheme} {
				if err := add(scheme); err != nil {
					t.Fatal(err)
				}
			}
			scheme.AddKnownTypes(schema.GroupVersion{Group: karpenterapis.Group, Version: "v1"}, &karpenterv1.NodePool{}, &karpenterv1.NodePoolList{}, &karpenterv1.NodeClaim{}, &karpenterv1.NodeClaimList{})
			created, deleted := 0, 0
			ec2client := &localZoneEC2Stub{failure: testCase.failure, subnetEC2Stub: subnetEC2Stub{failure: testCase.failure}}
			client := fake.NewClientBuilder().WithScheme(scheme).WithInterceptorFuncs(interceptor.Funcs{
				Create: func(ctx context.Context, client crclient.WithWatch, object crclient.Object, opts ...crclient.CreateOption) error {
					if _, ok := object.(*karpenterv1.NodePool); ok && testCase.failure == "pool" {
						return fmt.Errorf("injected NodePool create failure")
					}
					if pod, ok := object.(*corev1.Pod); ok {
						pod.Spec.NodeName = "new-node"
						pod.Status = corev1.PodStatus{Phase: corev1.PodSucceeded, ContainerStatuses: []corev1.ContainerStatus{{Name: "workload", State: corev1.ContainerState{Terminated: &corev1.ContainerStateTerminated{ExitCode: 0}}}}}
						if testCase.failure == "pod" {
							pod.Status.Phase = corev1.PodFailed
						}
						node := &corev1.Node{ObjectMeta: metav1.ObjectMeta{Name: pod.Spec.NodeName, UID: "new-uid", Labels: pod.Spec.NodeSelector}, Spec: corev1.NodeSpec{ProviderID: "aws:///us-west-2-lax-1a/i-new"}, Status: corev1.NodeStatus{Conditions: []corev1.NodeCondition{{Type: corev1.NodeReady, Status: corev1.ConditionTrue}}}}
						if err := client.Create(ctx, node); err != nil {
							return err
						}
					}
					if err := client.Create(ctx, object, opts...); err != nil {
						return err
					}
					created++
					return nil
				},
				Delete: func(ctx context.Context, client crclient.WithWatch, object crclient.Object, opts ...crclient.DeleteOption) error {
					if _, ok := object.(*karpenterv1.NodePool); ok && testCase.failure == "pool-delete" {
						return fmt.Errorf("injected NodePool deletion failure")
					}
					if err := client.Delete(ctx, object, opts...); err != nil {
						return err
					}
					deleted++
					ec2client.events = append(ec2client.events, fmt.Sprintf("delete %T", object))
					return nil
				},
			}).Build()
			hc := &hyperv1.HostedCluster{Spec: hyperv1.HostedClusterSpec{InfraID: "infra-test", AutoNode: hyperv1.AutoNode{Provisioner: hyperv1.ProvisionerConfig{Name: hyperv1.ProvisionerKarpenter, Karpenter: hyperv1.KarpenterConfig{Platform: hyperv1.AWSPlatform}}}, Platform: hyperv1.PlatformSpec{Type: hyperv1.AWSPlatform, AWS: &hyperv1.AWSPlatformSpec{Region: "us-west-2", CloudProviderConfig: &hyperv1.AWSCloudProviderConfig{VPC: "vpc-test"}}}}}
			values := map[string]string{"E2E_AUTONODE_LOCAL_ZONE": "us-west-2-lax-1a", "E2E_AUTONODE_LOCAL_ZONE_SUBNET_ID": "subnet-local", "E2E_AUTONODE_LOCAL_ZONE_INSTANCE_TYPE": "m5.xlarge", "E2E_AUTONODE_LOCAL_ZONE_VOLUME_TYPE": "gp2"}
			if testCase.createSubnet {
				delete(values, "E2E_AUTONODE_LOCAL_ZONE_SUBNET_ID")
			}
			testTB := &localZoneTestTB{TB: t}
			failed := false
			func() {
				defer func() {
					if recovered := recover(); recovered != nil {
						if recovered != testTB {
							panic(recovered)
						}
						failed = true
					}
				}()
				RunLocalZoneTest(context.Background(), testTB, client, hc, ec2client, func(name string) string { return values[name] })
			}()
			for index := len(testTB.cleanups) - 1; index >= 0; index-- {
				testTB.cleanups[index]()
			}
			if failed != (testCase.failure != "" && testCase.failure != "pool-delete") {
				t.Fatalf("unexpected failure state %t: %s", failed, testTB.failure)
			}
			verifyLocalZoneCleanup(t, testTB, ec2client, created, deleted, testCase.createSubnet, testCase.failure)
		})
	}
}

func verifyLocalZoneCleanup(t *testing.T, testTB *localZoneTestTB, ec2client *localZoneEC2Stub, created, deleted int, createSubnet bool, failure string) {
	t.Helper()
	if failure == "pool-delete" {
		if len(testTB.cleanupErrors) != 2 || ec2client.deleted || created-deleted != 1 {
			t.Fatalf("failed node teardown must retain the subnet and report errors: %v", testTB.cleanupErrors)
		}
		return
	}
	if len(testTB.cleanupErrors) != 0 {
		t.Fatalf("unexpected cleanup errors: %v", testTB.cleanupErrors)
	}
	if created != deleted {
		t.Fatalf("cleanup mismatch: %d resources created, %d deleted", created, deleted)
	}
	if createSubnet && (ec2client.created == nil || !ec2client.deleted) {
		t.Fatal("created subnet must be cleaned up even after setup or provisioning failure")
	}
	if !createSubnet && ec2client.created != nil {
		t.Fatal("supplied subnet must not cause network creation")
	}
	if createSubnet && failure == "" && strings.Join(ec2client.events, ",") != "create subnet,associate,delete *v1.Pod,delete *v1.NodePool,delete *v1.OpenshiftEC2NodeClass,disassociate,delete subnet" {
		t.Fatalf("unexpected cleanup order: %v", ec2client.events)
	}
	if failure == "zone" && created != 0 {
		t.Fatal("invalid prerequisites must fail before provisioning")
	}
}

type localZoneTestTB struct {
	testing.TB
	cleanups      []func()
	failure       string
	cleanupErrors []string
}

func (testTB *localZoneTestTB) Errorf(format string, args ...interface{}) {
	testTB.cleanupErrors = append(testTB.cleanupErrors, fmt.Sprintf(format, args...))
}

func (testTB *localZoneTestTB) Cleanup(cleanup func()) {
	testTB.cleanups = append(testTB.cleanups, cleanup)
}
func (testTB *localZoneTestTB) Fatalf(format string, args ...interface{}) {
	testTB.failure = fmt.Sprintf(format, args...)
	panic(testTB)
}

type localZoneEC2Stub struct {
	subnetEC2Stub
	failure string
}

func (stub *localZoneEC2Stub) DescribeAvailabilityZones(context.Context, *ec2.DescribeAvailabilityZonesInput, ...func(*ec2.Options)) (*ec2.DescribeAvailabilityZonesOutput, error) {
	zoneType := "local-zone"
	if stub.failure == "zone" {
		zoneType = "availability-zone"
	}
	return &ec2.DescribeAvailabilityZonesOutput{AvailabilityZones: []ec2types.AvailabilityZone{{ZoneName: ptr.To("us-west-2-lax-1a"), ZoneType: &zoneType, RegionName: ptr.To("us-west-2"), State: ec2types.AvailabilityZoneStateAvailable, OptInStatus: ec2types.AvailabilityZoneOptInStatusOptedIn}}}, nil
}
func (stub *localZoneEC2Stub) DescribeInstanceTypeOfferings(context.Context, *ec2.DescribeInstanceTypeOfferingsInput, ...func(*ec2.Options)) (*ec2.DescribeInstanceTypeOfferingsOutput, error) {
	return &ec2.DescribeInstanceTypeOfferingsOutput{InstanceTypeOfferings: []ec2types.InstanceTypeOffering{{Location: ptr.To("us-west-2-lax-1a"), InstanceType: ec2types.InstanceTypeM5Xlarge}}}, nil
}
func (stub *localZoneEC2Stub) DescribeInstanceTypes(context.Context, *ec2.DescribeInstanceTypesInput, ...func(*ec2.Options)) (*ec2.DescribeInstanceTypesOutput, error) {
	return &ec2.DescribeInstanceTypesOutput{InstanceTypes: []ec2types.InstanceTypeInfo{{ProcessorInfo: &ec2types.ProcessorInfo{SupportedArchitectures: []ec2types.ArchitectureType{ec2types.ArchitectureTypeX8664}}}}}, nil
}
func (stub *localZoneEC2Stub) DescribeInstances(context.Context, *ec2.DescribeInstancesInput, ...func(*ec2.Options)) (*ec2.DescribeInstancesOutput, error) {
	subnet := "subnet-local"
	if stub.failure == "placement" {
		subnet = "subnet-wrong"
	}
	return &ec2.DescribeInstancesOutput{Reservations: []ec2types.Reservation{{Instances: []ec2types.Instance{{SubnetId: &subnet, InstanceType: ec2types.InstanceTypeM5Xlarge, Placement: &ec2types.Placement{AvailabilityZone: ptr.To("us-west-2-lax-1a")}}}}}}, nil
}
