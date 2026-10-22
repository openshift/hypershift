//go:build e2ev2

package tests

import (
	"context"
	"fmt"
	"net/netip"
	"strconv"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	supportawsutil "github.com/openshift/hypershift/support/awsutil"
	"github.com/openshift/hypershift/support/thirdparty/library-go/pkg/image/reference"
	"github.com/openshift/hypershift/test/e2e/v2/internal"
	v2util "github.com/openshift/hypershift/test/e2e/v2/util"

	configv1 "github.com/openshift/api/config/v1"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/ec2"
	ec2types "github.com/aws/aws-sdk-go-v2/service/ec2/types"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/fields"
	"k8s.io/apimachinery/pkg/watch"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/util/retry"

	crclient "sigs.k8s.io/controller-runtime/pkg/client"
	karpenterv1 "sigs.k8s.io/karpenter/pkg/apis/v1"

	"github.com/blang/semver"
	"github.com/opencontainers/go-digest"
	dto "github.com/prometheus/client_model/go"
)

func autoNodeUpgradeTarget(cluster *hyperv1.HostedCluster, target releaseImageIdentity, targetVersion semver.Version) error {
	if cluster == nil || target.Requested == "" || target.Canonical == "" {
		return fmt.Errorf("auto node upgrade requires a HostedCluster and a target release image")
	}
	if cluster.Status.Version == nil || len(cluster.Status.Version.History) == 0 {
		return fmt.Errorf("auto node upgrade requires an observed starting release history")
	}
	starting := cluster.Status.Version.History[0]
	if starting.State != configv1.CompletedUpdate {
		return fmt.Errorf("auto node upgrade requires a completed starting release, got %s", starting.State)
	}
	if cluster.Spec.Release.Image == "" || starting.Image == "" || target.MatchesDataPlane(cluster.Spec.Release.Image) || target.MatchesDataPlane(starting.Image) {
		return fmt.Errorf("auto node upgrade requires a previous-release fixture distinct from target %q, not a no-op", target.Requested)
	}
	controlPlane := cluster.Status.ControlPlaneVersion
	if len(controlPlane.History) == 0 || controlPlane.History[0].State != configv1.CompletedUpdate || controlPlane.Desired.Image != controlPlane.History[0].Image {
		return fmt.Errorf("auto node upgrade requires a completed observed starting control plane payload")
	}
	startingReference, err := reference.Parse(controlPlane.History[0].Image)
	if err != nil || digest.Digest(startingReference.ID).Validate() != nil {
		return fmt.Errorf("auto node upgrade requires a resolved starting manifest digest")
	}
	targetReference, err := reference.Parse(target.Canonical)
	if err != nil || digest.Digest(targetReference.ID).Validate() != nil {
		return fmt.Errorf("auto node upgrade requires a resolved target manifest digest")
	}
	if startingReference.ID == targetReference.ID {
		return fmt.Errorf("auto node upgrade target must change release content, not its tag or repository")
	}
	startingVersion, err := semver.Parse(starting.Version)
	if err != nil {
		return fmt.Errorf("parsing AutoNode starting release %q: %w", starting.Version, err)
	}
	if targetVersion.LT(startingVersion) {
		return fmt.Errorf("resolved AutoNode target release %s must not be older than starting release %s", targetVersion, startingVersion)
	}
	return nil
}

func autoNodeControlPlaneRolloutComplete(version hyperv1.ControlPlaneVersionStatus, identity releaseImageIdentity) bool {
	return identity.Matches(version.Desired.Image) && len(version.History) > 0 &&
		identity.Matches(version.History[0].Image) && version.History[0].State == configv1.CompletedUpdate
}

func autoNodeClaimReadiness(claim *karpenterv1.NodeClaim) error {
	if claim == nil || claim.DeletionTimestamp != nil || claim.Status.ProviderID == "" {
		return fmt.Errorf("expected a live NodeClaim with a provider ID")
	}
	for _, required := range []string{karpenterv1.ConditionTypeLaunched, karpenterv1.ConditionTypeRegistered, karpenterv1.ConditionTypeInitialized} {
		matched := false
		for _, condition := range claim.Status.Conditions {
			if condition.Type == required && condition.Status == metav1.ConditionTrue {
				matched = true
				break
			}
		}
		if !matched {
			return fmt.Errorf("NodeClaim %s does not have %s=True", claim.Name, required)
		}
	}
	return nil
}

func autoNodeEndpointsAvailable(services []hyperv1.AWSEndpointService) error {
	if len(services) == 0 {
		return fmt.Errorf("expected at least one AWSEndpointService")
	}
	for _, service := range services {
		matched := false
		for _, condition := range service.Status.Conditions {
			if condition.Type == string(hyperv1.AWSEndpointAvailable) && condition.Status == metav1.ConditionTrue {
				matched = true
				break
			}
		}
		if !matched {
			return fmt.Errorf("AWSEndpointService %s does not have AWSEndpointAvailable=True", service.Name)
		}
	}
	return nil
}

func watchAutoNodeTransitions(tc *internal.TestContext, hc *hyperv1.HostedCluster) watch.Interface {
	GinkgoHelper()
	config, err := v2util.GetConfig()
	Expect(err).NotTo(HaveOccurred())
	client, err := crclient.NewWithWatch(config, crclient.Options{Scheme: tc.MgmtClient.Scheme()})
	Expect(err).NotTo(HaveOccurred())
	events, err := client.Watch(tc.Context, &hyperv1.HostedClusterList{}, &crclient.ListOptions{
		Namespace: hc.Namespace, FieldSelector: fields.OneTermEqualSelector("metadata.name", hc.Name),
		Raw: &metav1.ListOptions{ResourceVersion: hc.ResourceVersion},
	})
	Expect(err).NotTo(HaveOccurred())
	DeferCleanup(events.Stop)
	return events
}

func autoNodeProgressEvent(event watch.Event) (bool, error) {
	if event.Type == watch.Error {
		return false, apierrors.FromObject(event.Object)
	}
	if event.Type != watch.Added && event.Type != watch.Modified {
		return false, nil
	}
	hc, ok := event.Object.(*hyperv1.HostedCluster)
	if !ok {
		return false, fmt.Errorf("unexpected AutoNode watch object %T", event.Object)
	}
	matched, _, err := autoNodeConditionPredicate[*hyperv1.HostedCluster](v2util.Condition{
		Type: string(hyperv1.AutoNodeEnabled), Status: metav1.ConditionFalse, Reason: hyperv1.AutoNodeProgressingReason,
	})(hc)
	return matched, err
}

func updateAutoNodeObject[Object crclient.Object](ctx context.Context, client crclient.Client, object Object, update func(Object)) error {
	return retry.RetryOnConflict(retry.DefaultRetry, func() error {
		if err := client.Get(ctx, crclient.ObjectKeyFromObject(object), object); err != nil {
			return err
		}
		update(object)
		return client.Update(ctx, object)
	})
}

func autoNodeConditionPredicate[Object crclient.Object](expected v2util.Condition) func(Object) (bool, string, error) {
	return func(object Object) (bool, string, error) {
		conditions, err := v2util.Conditions(object)
		if err != nil {
			return false, "", err
		}
		for _, condition := range conditions {
			if expected.Matches(condition) {
				return true, condition.String(), nil
			}
		}
		return false, fmt.Sprintf("expected %s, observed %v", expected.String(), conditions), nil
	}
}

func autoNodeMetrics(ctx context.Context, client crclient.Client, component, container, namespace, port string) (map[string]*dto.MetricFamily, error) {
	pods := &corev1.PodList{}
	if err := client.List(ctx, pods, crclient.InNamespace(namespace), crclient.MatchingLabels{"app": component}); err != nil {
		return nil, err
	}
	config, err := v2util.GetConfig()
	if err != nil {
		return nil, err
	}
	clientset, err := kubernetes.NewForConfig(config)
	if err != nil {
		return nil, err
	}
	portNumber, err := strconv.Atoi(port)
	if err != nil {
		return nil, err
	}
	for _, pod := range pods.Items {
		if pod.Status.Phase == corev1.PodRunning && pod.DeletionTimestamp == nil {
			return v2util.GetMetricsFromPod(ctx, clientset, config, namespace, pod.Name, container, portNumber)
		}
	}
	return nil, fmt.Errorf("no running %s pod in namespace %s", component, namespace)
}

func autoNodeResourceTags(hc *hyperv1.HostedCluster) []ec2types.Tag {
	tags := []ec2types.Tag{
		{Key: aws.String(supportawsutil.HypershiftInfraIDTagKey), Value: aws.String(hc.Spec.InfraID)},
		{Key: aws.String(supportawsutil.HypershiftClusterNameTagKey), Value: aws.String(hc.Name)},
		{Key: aws.String(supportawsutil.HypershiftSourceTagKey), Value: aws.String("e2e")},
	}
	if job := internal.GetEnvVarValue("PROW_JOB_ID"); job != "" {
		tags = append(tags, ec2types.Tag{Key: aws.String(supportawsutil.HypershiftProwJobIDTagKey), Value: aws.String(job)})
	}
	return tags
}

func createAutoNodeCapacityReservation(ctx context.Context, credentials, region, instanceType, zone string, count int32, tags []ec2types.Tag) string {
	GinkgoHelper()
	client := v2util.NewEC2Client(ctx, credentials, region)
	result, err := client.CreateCapacityReservation(ctx, &ec2.CreateCapacityReservationInput{
		InstanceType: aws.String(instanceType), InstancePlatform: ec2types.CapacityReservationInstancePlatformLinuxUnix,
		AvailabilityZone: aws.String(zone), InstanceCount: aws.Int32(count), InstanceMatchCriteria: ec2types.InstanceMatchCriteriaTargeted,
		EndDateType: ec2types.EndDateTypeLimited, EndDate: aws.Time(time.Now().Add(2 * time.Hour)),
		TagSpecifications: []ec2types.TagSpecification{{ResourceType: ec2types.ResourceTypeCapacityReservation, Tags: tags}},
	})
	Expect(err).NotTo(HaveOccurred())
	Expect(result.CapacityReservation).NotTo(BeNil())
	reservation := aws.ToString(result.CapacityReservation.CapacityReservationId)
	Expect(reservation).NotTo(BeEmpty())
	DeferCleanup(func() {
		cleanupCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 2*time.Minute)
		defer cancel()
		_, err := client.CancelCapacityReservation(cleanupCtx, &ec2.CancelCapacityReservationInput{CapacityReservationId: aws.String(reservation)})
		Expect(err).NotTo(HaveOccurred(), "cleanup: cancel capacity reservation %s", reservation)
	})
	Eventually(func(g Gomega) {
		current, err := client.DescribeCapacityReservations(ctx, &ec2.DescribeCapacityReservationsInput{CapacityReservationIds: []string{reservation}})
		g.Expect(err).NotTo(HaveOccurred())
		g.Expect(current.CapacityReservations).To(HaveLen(1))
		g.Expect(current.CapacityReservations[0].State).To(Equal(ec2types.CapacityReservationStateActive))
	}).WithContext(ctx).WithTimeout(3 * time.Minute).WithPolling(5 * time.Second).Should(Succeed())
	return reservation
}

func autoNodeSubnetCIDR(vpc string, occupied []string) (string, error) {
	space, err := netip.ParsePrefix(vpc)
	if err != nil || !space.Addr().Is4() || space.Bits() > 28 {
		return "", fmt.Errorf("invalid IPv4 VPC CIDR %q for a /28 subnet", vpc)
	}
	networks := make([]netip.Prefix, 0, len(occupied))
	for _, cidr := range occupied {
		network, err := netip.ParsePrefix(cidr)
		if err != nil {
			return "", fmt.Errorf("invalid occupied subnet %q: %w", cidr, err)
		}
		networks = append(networks, network)
	}
	for address := space.Masked().Addr(); space.Contains(address); {
		candidate := netip.PrefixFrom(address, 28)
		available := true
		for _, network := range networks {
			if candidate.Overlaps(network) {
				available = false
				break
			}
		}
		if available {
			return candidate.String(), nil
		}
		for offset := 0; offset < 16; offset++ {
			address = address.Next()
		}
	}
	return "", fmt.Errorf("no unused /28 subnet in %s", vpc)
}

func createAutoNodeSubnet(ctx context.Context, client *ec2.Client, vpc, zone string, tags []ec2types.Tag) string {
	GinkgoHelper()
	for _, tag := range tags {
		if aws.ToString(tag.Key) == supportawsutil.HypershiftInfraIDTagKey {
			tags = append(tags,
				ec2types.Tag{Key: aws.String("Name"), Value: aws.String(aws.ToString(tag.Value) + "-karpenter-test-subnet")},
				ec2types.Tag{Key: aws.String("kubernetes.io/cluster/" + aws.ToString(tag.Value)), Value: aws.String("owned")},
			)
			break
		}
	}
	vpcs, err := client.DescribeVpcs(ctx, &ec2.DescribeVpcsInput{VpcIds: []string{vpc}})
	Expect(err).NotTo(HaveOccurred())
	Expect(vpcs.Vpcs).To(HaveLen(1))
	existing, err := client.DescribeSubnets(ctx, &ec2.DescribeSubnetsInput{Filters: []ec2types.Filter{{Name: aws.String("vpc-id"), Values: []string{vpc}}}})
	Expect(err).NotTo(HaveOccurred())
	occupied := make([]string, 0, len(existing.Subnets))
	for _, subnet := range existing.Subnets {
		occupied = append(occupied, aws.ToString(subnet.CidrBlock))
	}
	cidr, err := autoNodeSubnetCIDR(aws.ToString(vpcs.Vpcs[0].CidrBlock), occupied)
	Expect(err).NotTo(HaveOccurred())
	created, err := client.CreateSubnet(ctx, &ec2.CreateSubnetInput{
		VpcId: aws.String(vpc), AvailabilityZone: aws.String(zone), CidrBlock: aws.String(cidr),
		TagSpecifications: []ec2types.TagSpecification{{ResourceType: ec2types.ResourceTypeSubnet, Tags: tags}},
	})
	Expect(err).NotTo(HaveOccurred())
	Expect(created.Subnet).NotTo(BeNil())
	subnetID := aws.ToString(created.Subnet.SubnetId)
	Expect(subnetID).NotTo(BeEmpty())
	DeferCleanup(func() {
		cleanupCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 3*time.Minute)
		defer cancel()
		Eventually(func() error {
			_, err := client.DeleteSubnet(cleanupCtx, &ec2.DeleteSubnetInput{SubnetId: aws.String(subnetID)})
			return err
		}).WithContext(cleanupCtx).WithTimeout(2*time.Minute).WithPolling(10*time.Second).Should(Succeed(), "cleanup: delete subnet %s", subnetID)
	})
	routes, err := client.DescribeRouteTables(ctx, &ec2.DescribeRouteTablesInput{Filters: []ec2types.Filter{
		{Name: aws.String("vpc-id"), Values: []string{vpc}}, {Name: aws.String("route.nat-gateway-id"), Values: []string{"nat-*"}},
	}})
	Expect(err).NotTo(HaveOccurred())
	Expect(routes.RouteTables).NotTo(BeEmpty(), "private route table required for subnet %s", subnetID)
	association, err := client.AssociateRouteTable(ctx, &ec2.AssociateRouteTableInput{SubnetId: aws.String(subnetID), RouteTableId: routes.RouteTables[0].RouteTableId})
	Expect(err).NotTo(HaveOccurred())
	DeferCleanup(func() {
		cleanupCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), time.Minute)
		defer cancel()
		_, err := client.DisassociateRouteTable(cleanupCtx, &ec2.DisassociateRouteTableInput{AssociationId: association.AssociationId})
		Expect(err).NotTo(HaveOccurred(), "cleanup: disassociate subnet %s", subnetID)
	})
	return subnetID
}
