//go:build e2ev2

package tests

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"regexp"
	"strings"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	supportawsutil "github.com/openshift/hypershift/support/awsutil"
	"github.com/openshift/hypershift/support/podspec"
	"github.com/openshift/hypershift/test/e2e/v2/internal"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/ec2"
	ec2types "github.com/aws/aws-sdk-go-v2/service/ec2/types"
	"github.com/aws/aws-sdk-go-v2/service/iam"
	"github.com/aws/aws-sdk-go-v2/service/kms"
	"github.com/aws/aws-sdk-go-v2/service/sqs"
	sqstypes "github.com/aws/aws-sdk-go-v2/service/sqs/types"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/util/retry"
	"k8s.io/utils/ptr"

	capiaws "sigs.k8s.io/cluster-api-provider-aws/v2/api/v1beta2"
	capiv1 "sigs.k8s.io/cluster-api/api/core/v1beta1"
	crclient "sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/blang/semver"
	"github.com/google/uuid"
)

var _ = Describe("[sig-hypershift][Jira:Hypershift][Feature:NodePoolAWS] AWS NodePool cloud behavior", Serial, Label("lifecycle", "nodepool-aws-cloud"), func() {
	RegisterNodePoolAWSCloudTests(internal.GetTestContext)
})

func RegisterNodePoolAWSCloudTests(getTestCtx internal.TestContextGetter) {
	It("should encrypt the actual root EBS volume with the requested KMS key", Label("nodepool-aws-root-volume"), func() {
		testCtx, hc, hcClient, cloudConfig := awsNodePoolContext(getTestCtx)
		key := internal.GetEnvVarValue("E2E_AWS_EBS_KMS_KEY_ARN")
		cloud := ec2.NewFromConfig(cloudConfig)
		useDefaultKey := key == ""
		var expectedARN string
		if !useDefaultKey {
			expectedARN = awsNodePoolKMSKeyARN(testCtx, cloudConfig, key)
		}
		pool := createAWSNodePool(testCtx, hc, "kms", true, func(pool *hyperv1.NodePool) {
			if pool.Spec.Platform.AWS.RootVolume == nil {
				pool.Spec.Platform.AWS.RootVolume = &hyperv1.Volume{Size: 16, Type: "gp3"}
			}
			pool.Spec.Platform.AWS.RootVolume.Encrypted = ptr.To(true)
			pool.Spec.Platform.AWS.RootVolume.EncryptionKey = ""
			if !useDefaultKey {
				pool.Spec.Platform.AWS.RootVolume.EncryptionKey = expectedARN
			}
		})
		nodes := waitForAWSNodePoolNodes(testCtx, hcClient, pool, nil)
		if useDefaultKey {
			defaultKey, err := cloud.GetEbsDefaultKmsKeyId(testCtx.Context, &ec2.GetEbsDefaultKmsKeyIdInput{})
			Expect(err).NotTo(HaveOccurred())
			Expect(defaultKey.KmsKeyId).NotTo(BeNil())
			expectedARN = awsNodePoolKMSKeyARN(testCtx, cloudConfig, aws.ToString(defaultKey.KmsKeyId))
		}
		for _, node := range nodes {
			instance, err := describeAWSNodeInstance(testCtx.Context, cloud, node)
			Expect(err).NotTo(HaveOccurred())
			Expect(instance.RootDeviceName).NotTo(BeNil())
			var rootVolumeID string
			for _, mapping := range instance.BlockDeviceMappings {
				if aws.ToString(mapping.DeviceName) == aws.ToString(instance.RootDeviceName) && mapping.Ebs != nil {
					rootVolumeID = aws.ToString(mapping.Ebs.VolumeId)
				}
			}
			Expect(rootVolumeID).NotTo(BeEmpty(), "must inspect the root volume, not an arbitrary encrypted attachment")
			Eventually(func(g Gomega) {
				volumes, err := cloud.DescribeVolumes(testCtx.Context, &ec2.DescribeVolumesInput{VolumeIds: []string{rootVolumeID}})
				g.Expect(err).NotTo(HaveOccurred())
				g.Expect(volumes.Volumes).To(HaveLen(1))
				g.Expect(aws.ToBool(volumes.Volumes[0].Encrypted)).To(BeTrue())
				g.Expect(aws.ToString(volumes.Volumes[0].KmsKeyId)).To(Equal(expectedARN))
			}).WithContext(testCtx.Context).WithTimeout(2 * time.Minute).WithPolling(5 * time.Second).Should(Succeed())
		}
		expectAWSNodePoolSteadyState(testCtx, pool)
	})

	It("should propagate day-2 tags, labels and taints without replacing machines or nodes", Label("nodepool-aws-day2-tags"), func() {
		testCtx, hc, hcClient, cloudConfig := awsNodePoolContext(getTestCtx)
		pool := createAWSNodePool(testCtx, hc, "tags", true, nil)
		nodes := waitForAWSNodePoolNodes(testCtx, hcClient, pool, nil)
		deploymentKey := crclient.ObjectKey{Namespace: testCtx.ControlPlaneNamespace, Name: pool.Name}
		deployment := &capiv1.MachineDeployment{}
		Expect(testCtx.MgmtClient.Get(testCtx.Context, deploymentKey, deployment)).To(Succeed())
		generation := deployment.Generation
		originalNodes := awsNodeUIDs(nodes)
		originalMachines, err := awsPoolMachineUIDs(testCtx, pool)
		Expect(err).NotTo(HaveOccurred())
		const tagKey, tagValue = "test-day2-tag", "test-day2-value"
		Expect(mutateAWSNodePool(testCtx, pool, func(pool *hyperv1.NodePool) {
			pool.Spec.Platform.AWS.ResourceTags = append(pool.Spec.Platform.AWS.ResourceTags, hyperv1.AWSNodePoolResourceTag{Key: tagKey, Value: tagValue})
			if pool.Spec.NodeLabels == nil {
				pool.Spec.NodeLabels = map[string]string{}
			}
			pool.Spec.NodeLabels["e2e.day2.validation"] = "true"
			pool.Spec.Taints = append(pool.Spec.Taints, hyperv1.Taint{Key: "e2e-day2", Value: "test", Effect: corev1.TaintEffectPreferNoSchedule})
		})).To(Succeed())
		cloud := ec2.NewFromConfig(cloudConfig)
		noRollout := &awsPoolNoRolloutObservation{}
		assertNoRollout := func(g Gomega) {
			current := &hyperv1.NodePool{}
			g.Expect(testCtx.MgmtClient.Get(testCtx.Context, crclient.ObjectKeyFromObject(pool), current)).To(Succeed())
			g.Expect(noRollout.observe(current)).To(Succeed())
			g.Expect(testCtx.MgmtClient.Get(testCtx.Context, deploymentKey, deployment)).To(Succeed())
			g.Expect(deployment.Generation).To(Equal(generation))
			machineUIDs, err := awsPoolMachineUIDs(testCtx, pool)
			g.Expect(err).NotTo(HaveOccurred())
			g.Expect(machineUIDs).To(ConsistOf(originalMachines))
			currentNodes := &corev1.NodeList{}
			g.Expect(hcClient.List(testCtx.Context, currentNodes, crclient.MatchingLabels{hyperv1.NodePoolLabel: pool.Name})).To(Succeed())
			g.Expect(awsNodeUIDs(currentNodes.Items)).To(ConsistOf(originalNodes))
		}
		Eventually(func(g Gomega) {
			assertNoRollout(g)
			machines := &capiaws.AWSMachineList{}
			g.Expect(testCtx.MgmtClient.List(testCtx.Context, machines, crclient.InNamespace(testCtx.ControlPlaneNamespace), crclient.MatchingLabels{capiv1.MachineDeploymentNameLabel: pool.Name})).To(Succeed())
			g.Expect(machines.Items).NotTo(BeEmpty())
			for _, machine := range machines.Items {
				g.Expect(machine.Spec.AdditionalTags).To(HaveKeyWithValue(tagKey, tagValue))
			}
			for _, node := range nodes {
				current := &corev1.Node{}
				g.Expect(hcClient.Get(testCtx.Context, crclient.ObjectKeyFromObject(&node), current)).To(Succeed())
				g.Expect(awsNodeMetadataMatches(current, pool)).To(Succeed())
				instance, err := describeAWSNodeInstance(testCtx.Context, cloud, node)
				g.Expect(err).NotTo(HaveOccurred())
				g.Expect(instance.Tags).To(ContainElement(ec2types.Tag{Key: ptr.To(tagKey), Value: ptr.To(tagValue)}))
			}
		}).WithContext(testCtx.Context).WithTimeout(2 * time.Minute).WithPolling(time.Second).Should(Succeed())
		Expect(noRollout.violation).NotTo(HaveOccurred(), "no-rollout must hold throughout tag propagation, not only after convergence")
		Consistently(assertNoRollout).WithContext(testCtx.Context).WithTimeout(2 * time.Minute).WithPolling(time.Second).Should(Succeed())
		expectAWSNodePoolSteadyState(testCtx, pool)
	})

	It("should use the licensed Windows AMI while running RHCOS Linux", Label("nodepool-aws-licensed-ami"), func() {
		testCtx := getTestCtx()
		Expect(testCtx).NotTo(BeNil())
		testCtx.SkipIfNotPlatform(hyperv1.AWSPlatform)
		applicable, err := nodePoolCloudVersionApplicable(testCtx, semver.MustParse("4.19.0"))
		Expect(err).NotTo(HaveOccurred(), "resolve originating release for licensed AMI eligibility")
		if !applicable {
			Skip("licensed AMI tests require originating release 4.19 or later")
		}
		_, hc, hcClient, cloudConfig := awsNodePoolContext(func() *internal.TestContext { return testCtx })
		instanceType := internal.GetEnvVarValue("E2E_AWS_LICENSED_INSTANCE_TYPE")
		Expect(instanceType).NotTo(BeEmpty(), "configure a Windows LI eligible instance type (for example m5.metal)")
		pool := createAWSNodePool(testCtx, hc, "licensed", true, func(pool *hyperv1.NodePool) {
			pool.Spec.Platform.AWS.InstanceType = instanceType
			pool.Spec.Platform.AWS.ImageType = hyperv1.ImageTypeWindows
		})
		var expectedAMI string
		Eventually(func(g Gomega) {
			current := &hyperv1.NodePool{}
			g.Expect(testCtx.MgmtClient.Get(testCtx.Context, crclient.ObjectKeyFromObject(pool), current)).To(Succeed())
			condition := awsPoolCondition(current, hyperv1.NodePoolValidPlatformImageType)
			g.Expect(condition).NotTo(BeNil())
			g.Expect(condition.Status).To(Equal(corev1.ConditionTrue), "a missing licensed AMI is an unsatisfied prerequisite, not coverage")
			g.Expect(strings.ToLower(condition.Message)).To(ContainSubstring("windows"))
			expectedAMI = awsLicensedAMIFromMessage(condition.Message)
			g.Expect(expectedAMI).NotTo(BeEmpty())
		}).WithContext(testCtx.Context).WithTimeout(5 * time.Minute).WithPolling(10 * time.Second).Should(Succeed())
		nodes := waitForAWSNodePoolNodes(testCtx, hcClient, pool, nil)
		cloud := ec2.NewFromConfig(cloudConfig)
		for _, node := range nodes {
			Expect(node.Status.NodeInfo.OperatingSystem).To(Equal("linux"))
			Expect(strings.ToLower(node.Status.NodeInfo.OSImage)).To(ContainSubstring("red hat enterprise linux coreos"))
			instance, err := describeAWSNodeInstance(testCtx.Context, cloud, node)
			Expect(err).NotTo(HaveOccurred())
			Expect(aws.ToString(instance.ImageId)).To(Equal(expectedAMI))
		}
		expectAWSNodePoolSteadyState(testCtx, pool)
	})

	It("should process a spot rebalance event through SQS and remediate the node", Label("nodepool-aws-spot-termination"), func() {
		testCtx := getTestCtx()
		Expect(testCtx).NotTo(BeNil())
		testCtx.SkipIfNotPlatform(hyperv1.AWSPlatform)
		applicable, err := nodePoolCloudVersionApplicable(testCtx, semver.MustParse("4.22.0"))
		Expect(err).NotTo(HaveOccurred(), "resolve originating release for spot termination eligibility")
		if !applicable {
			Skip("spot termination tests require originating release 4.22 or later")
		}
		_, hc, hcClient, cloudConfig := awsNodePoolContext(func() *internal.TestContext { return testCtx })
		queueClient := sqs.NewFromConfig(cloudConfig)
		queue, err := queueClient.CreateQueue(testCtx.Context, &sqs.CreateQueueInput{
			QueueName: ptr.To("hypershift-e2e-" + uuid.NewString()),
			Tags:      awsNodePoolResourceTags(hc.Spec.InfraID, hc.Name, internal.GetEnvVarValue("PROW_JOB_ID")),
		})
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(func() {
			_, err := queueClient.DeleteQueue(testCtx.Context, &sqs.DeleteQueueInput{QueueUrl: queue.QueueUrl})
			Expect(err).NotTo(HaveOccurred(), "cleanup: delete SQS queue")
		})
		attributes, err := queueClient.GetQueueAttributes(testCtx.Context, &sqs.GetQueueAttributesInput{QueueUrl: queue.QueueUrl, AttributeNames: []sqstypes.QueueAttributeName{sqstypes.QueueAttributeNameQueueArn}})
		Expect(err).NotTo(HaveOccurred())
		queueARN := attributes.Attributes[string(sqstypes.QueueAttributeNameQueueArn)]
		Expect(queueARN).NotTo(BeEmpty())
		roleARN := hc.Spec.Platform.AWS.RolesRef.NodePoolManagementARN
		roleName := roleARN[strings.LastIndex(roleARN, "/")+1:]
		Expect(roleName).NotTo(BeEmpty())
		policyName := "hypershift-e2e-" + uuid.NewString()
		policy := fmt.Sprintf(`{"Version":"2012-10-17","Statement":[{"Effect":"Allow","Action":["sqs:ReceiveMessage","sqs:DeleteMessage"],"Resource":%q}]}`, queueARN)
		iamClient := iam.NewFromConfig(cloudConfig)
		_, err = iamClient.PutRolePolicy(testCtx.Context, &iam.PutRolePolicyInput{RoleName: ptr.To(roleName), PolicyName: ptr.To(policyName), PolicyDocument: ptr.To(policy)})
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(func() {
			_, err := iamClient.DeleteRolePolicy(testCtx.Context, &iam.DeleteRolePolicyInput{RoleName: ptr.To(roleName), PolicyName: ptr.To(policyName)})
			Expect(err).NotTo(HaveOccurred(), "cleanup: remove temporary SQS policy")
		})
		originalQueue := hc.Spec.Platform.AWS.TerminationHandlerQueueURL
		Expect(mutateAWSHostedCluster(testCtx, hc, aws.ToString(queue.QueueUrl))).To(Succeed())
		DeferCleanup(func() { Expect(mutateAWSHostedCluster(testCtx, hc, originalQueue)).To(Succeed()) })
		pool := createAWSNodePool(testCtx, hc, "spot", true, func(pool *hyperv1.NodePool) {
			pool.Annotations = map[string]string{"hypershift.openshift.io/enable-spot": "true"}
			pool.Spec.Management.AutoRepair = true
		})
		Eventually(func(g Gomega) {
			deployment := &appsv1.Deployment{}
			g.Expect(testCtx.MgmtClient.Get(testCtx.Context, crclient.ObjectKey{Namespace: testCtx.ControlPlaneNamespace, Name: "aws-node-termination-handler"}, deployment)).To(Succeed())
			g.Expect(deployment.Spec.Replicas).NotTo(BeNil())
			g.Expect(*deployment.Spec.Replicas).To(BeNumerically(">", 0))
			g.Expect(podspec.IsDeploymentReady(testCtx.Context, deployment)).To(BeTrue())
			healthCheck := &capiv1.MachineHealthCheck{}
			g.Expect(testCtx.MgmtClient.Get(testCtx.Context, crclient.ObjectKey{Namespace: testCtx.ControlPlaneNamespace, Name: pool.Name + "-spot"}, healthCheck)).To(Succeed())
			g.Expect(healthCheck.Spec.Selector.MatchLabels).To(HaveKey("hypershift.openshift.io/interruptible-instance"))
		}).WithContext(testCtx.Context).WithTimeout(5 * time.Minute).WithPolling(10 * time.Second).Should(Succeed())
		nodes := waitForAWSNodePoolNodes(testCtx, hcClient, pool, nil)
		instanceID, err := awsInstanceID(nodes[0].Spec.ProviderID)
		Expect(err).NotTo(HaveOccurred())
		message, err := json.Marshal(map[string]interface{}{
			"version": "0", "source": "aws.ec2", "detail-type": "EC2 Instance Rebalance Recommendation",
			"detail": map[string]string{"instance-id": instanceID}, "id": uuid.NewString(),
			"time": time.Now().UTC().Format(time.RFC3339), "region": hc.Spec.Platform.AWS.Region, "account": "123456789012",
		})
		Expect(err).NotTo(HaveOccurred())
		_, err = queueClient.SendMessage(testCtx.Context, &sqs.SendMessageInput{QueueUrl: queue.QueueUrl, MessageBody: ptr.To(string(message))})
		Expect(err).NotTo(HaveOccurred())
		Eventually(func(g Gomega) {
			current := &corev1.Node{}
			err := hcClient.Get(testCtx.Context, crclient.ObjectKeyFromObject(&nodes[0]), current)
			if apierrors.IsNotFound(err) {
				return
			}
			g.Expect(err).NotTo(HaveOccurred())
			g.Expect(current.Spec.Taints).To(ContainElement(HaveField("Key", "aws-node-termination-handler/rebalance-recommendation")))
		}).WithContext(testCtx.Context).WithTimeout(15 * time.Minute).WithPolling(5 * time.Second).Should(Succeed())
		Expect(mutateAWSHostedCluster(testCtx, hc, originalQueue)).To(Succeed())
		expectAWSNodePoolSteadyState(testCtx, pool)
	})
}

func awsNodePoolKMSKeyARN(testCtx *internal.TestContext, cloudConfig aws.Config, key string) string {
	GinkgoHelper()
	resolvedKey, err := kms.NewFromConfig(cloudConfig).DescribeKey(testCtx.Context, &kms.DescribeKeyInput{KeyId: ptr.To(key)})
	Expect(err).NotTo(HaveOccurred())
	Expect(resolvedKey.KeyMetadata).NotTo(BeNil())
	arn := aws.ToString(resolvedKey.KeyMetadata.Arn)
	Expect(arn).NotTo(BeEmpty())
	return arn
}

func awsNodePoolResourceTags(infraID, clusterName, prowJobID string) map[string]string {
	tags := map[string]string{
		supportawsutil.HypershiftSourceTagKey:      "e2e",
		supportawsutil.HypershiftInfraIDTagKey:     infraID,
		supportawsutil.HypershiftClusterNameTagKey: clusterName,
	}
	if prowJobID != "" {
		tags[supportawsutil.HypershiftProwJobIDTagKey] = prowJobID
	}
	return tags
}

func awsNodePoolContext(getTestCtx internal.TestContextGetter) (*internal.TestContext, *hyperv1.HostedCluster, crclient.Client, aws.Config) {
	GinkgoHelper()
	testCtx := getTestCtx()
	Expect(testCtx).NotTo(BeNil())
	hc, err := testCtx.GetHostedCluster()
	Expect(err).NotTo(HaveOccurred())
	if hc.Spec.Platform.Type != hyperv1.AWSPlatform {
		Skip("AWS NodePool cloud tests are only for AWS platform")
	}
	Expect(hc.Spec.Platform.AWS).NotTo(BeNil())
	hcClient, err := testCtx.GetHostedClusterClient(hc)
	Expect(err).NotTo(HaveOccurred())
	cloudConfig, err := loadAWSNodePoolConfig(testCtx.Context, hc.Spec.Platform.AWS.Region, internal.GetEnvVarValue("AWS_GUEST_INFRA_CREDENTIALS_FILE"))
	Expect(err).NotTo(HaveOccurred())
	return testCtx, hc, hcClient, cloudConfig
}

func loadAWSNodePoolConfig(ctx context.Context, region, credentialsFile string) (aws.Config, error) {
	options := []func(*config.LoadOptions) error{config.WithRegion(region)}
	if credentialsFile == "" {
		credentialsFile = os.Getenv("AWS_SHARED_CREDENTIALS_FILE")
	}
	if credentialsFile != "" {
		options = append(options, config.WithSharedConfigFiles([]string{credentialsFile}), config.WithSharedCredentialsFiles([]string{credentialsFile}))
	}
	return config.LoadDefaultConfig(ctx, options...)
}

func createAWSNodePool(testCtx *internal.TestContext, hc *hyperv1.HostedCluster, suffix string, observeProvisioning bool, mutate func(*hyperv1.NodePool)) *hyperv1.NodePool {
	GinkgoHelper()
	template := nodePoolTemplate(testCtx.Context, testCtx.MgmtClient, hc)
	Expect(template).NotTo(BeNil())
	pool := &hyperv1.NodePool{ObjectMeta: metav1.ObjectMeta{Namespace: hc.Namespace, GenerateName: "aws-" + suffix + "-"}}
	template.Spec.DeepCopyInto(&pool.Spec)
	pool.Spec.Replicas = ptr.To(int32(1))
	pool.Spec.AutoScaling = nil
	pool.Spec.Platform.AWS.SecurityGroups = nil
	if pool.Spec.NodeLabels == nil {
		pool.Spec.NodeLabels = map[string]string{}
	}
	pool.Spec.NodeLabels["e2e.hypershift.openshift.io/nodepool"] = suffix
	pool.Spec.Taints = append(pool.Spec.Taints, hyperv1.Taint{Key: "e2e-nodepool", Value: "validation", Effect: corev1.TaintEffectPreferNoSchedule})
	if mutate != nil {
		mutate(pool)
	}
	Expect(testCtx.MgmtClient.Create(testCtx.Context, pool)).To(Succeed())
	DeferCleanup(func() {
		err := testCtx.MgmtClient.Delete(testCtx.Context, pool)
		if !apierrors.IsNotFound(err) {
			Expect(err).NotTo(HaveOccurred(), "cleanup: delete NodePool")
		}
		Eventually(func() bool {
			return apierrors.IsNotFound(testCtx.MgmtClient.Get(testCtx.Context, crclient.ObjectKeyFromObject(pool), &hyperv1.NodePool{}))
		}).WithContext(testCtx.Context).WithTimeout(30*time.Minute).WithPolling(10*time.Second).Should(BeTrue(), "cleanup: wait for NodePool deletion")
	})
	if observeProvisioning {
		hcClient, err := testCtx.GetHostedClusterClient(hc)
		Expect(err).NotTo(HaveOccurred())
		nodePoolWaitForInitialState(testCtx, hc, hcClient, pool)
	}
	return pool
}

func waitForAWSNodePoolNodes(testCtx *internal.TestContext, client crclient.Client, pool *hyperv1.NodePool, excluded []types.UID) []corev1.Node {
	GinkgoHelper()
	var nodes []corev1.Node
	Eventually(func(g Gomega) {
		list := &corev1.NodeList{}
		g.Expect(client.List(testCtx.Context, list, crclient.MatchingLabels{hyperv1.NodePoolLabel: pool.Name})).To(Succeed())
		g.Expect(pool.Spec.Replicas).NotTo(BeNil())
		g.Expect(list.Items).To(HaveLen(int(*pool.Spec.Replicas)))
		for _, node := range list.Items {
			g.Expect(node.DeletionTimestamp).To(BeNil())
			g.Expect(excluded).NotTo(ContainElement(node.UID))
			g.Expect(awsNodeHealthy(&node)).To(Succeed())
			g.Expect(awsNodeMetadataMatches(&node, pool)).To(Succeed())
		}
		nodes = list.Items
	}).WithContext(testCtx.Context).WithTimeout(30 * time.Minute).WithPolling(10 * time.Second).Should(Succeed())
	return nodes
}

func awsNodeHealthy(node *corev1.Node) error {
	for conditionType, expected := range map[corev1.NodeConditionType]corev1.ConditionStatus{
		corev1.NodeReady: corev1.ConditionTrue, corev1.NodeMemoryPressure: corev1.ConditionFalse,
		corev1.NodeDiskPressure: corev1.ConditionFalse, corev1.NodePIDPressure: corev1.ConditionFalse,
	} {
		found := false
		for _, condition := range node.Status.Conditions {
			if condition.Type == conditionType && condition.Status == expected {
				found = true
			}
		}
		if !found {
			return fmt.Errorf("node %s must report %s=%s", node.Name, conditionType, expected)
		}
	}
	return nil
}

func awsNodeMetadataMatches(node *corev1.Node, pool *hyperv1.NodePool) error {
	for key, value := range pool.Spec.NodeLabels {
		actual, found := node.Labels[key]
		if !found || actual != value {
			return fmt.Errorf("node %s missing label %s=%s", node.Name, key, value)
		}
	}
	for _, expected := range pool.Spec.Taints {
		found := false
		for _, actual := range node.Spec.Taints {
			if actual.Key == expected.Key && actual.Value == expected.Value && actual.Effect == expected.Effect {
				found = true
			}
		}
		if !found {
			return fmt.Errorf("node %s missing taint %s", node.Name, expected.Key)
		}
	}
	return nil
}

func awsInstanceID(providerID string) (string, error) {
	parts := strings.Split(providerID, "/")
	if !strings.HasPrefix(providerID, "aws:///") || len(parts) != 5 || parts[3] == "" || !regexp.MustCompile(`^i-(?:[0-9a-f]{8}|[0-9a-f]{17})$`).MatchString(parts[4]) {
		return "", fmt.Errorf("invalid AWS providerID %q", providerID)
	}
	return parts[4], nil
}

func describeAWSNodeInstance(ctx context.Context, cloud *ec2.Client, node corev1.Node) (ec2types.Instance, error) {
	instanceID, err := awsInstanceID(node.Spec.ProviderID)
	if err != nil {
		return ec2types.Instance{}, err
	}
	result, err := cloud.DescribeInstances(ctx, &ec2.DescribeInstancesInput{InstanceIds: []string{instanceID}})
	if err != nil {
		return ec2types.Instance{}, err
	}
	if result == nil || len(result.Reservations) != 1 || len(result.Reservations[0].Instances) != 1 {
		return ec2types.Instance{}, fmt.Errorf("expected one EC2 instance for node %s", node.Name)
	}
	return result.Reservations[0].Instances[0], nil
}

func mutateAWSNodePool(testCtx *internal.TestContext, pool *hyperv1.NodePool, mutate func(*hyperv1.NodePool)) error {
	return retry.RetryOnConflict(retry.DefaultRetry, func() error {
		current := &hyperv1.NodePool{}
		if err := testCtx.MgmtClient.Get(testCtx.Context, crclient.ObjectKeyFromObject(pool), current); err != nil {
			return err
		}
		mutate(current)
		if err := testCtx.MgmtClient.Update(testCtx.Context, current); err != nil {
			return err
		}
		*pool = *current
		return nil
	})
}

func mutateAWSHostedCluster(testCtx *internal.TestContext, hc *hyperv1.HostedCluster, queueURL string) error {
	return retry.RetryOnConflict(retry.DefaultRetry, func() error {
		current := &hyperv1.HostedCluster{}
		if err := testCtx.MgmtClient.Get(testCtx.Context, crclient.ObjectKeyFromObject(hc), current); err != nil {
			return err
		}
		original := current.DeepCopy()
		current.Spec.Platform.AWS.TerminationHandlerQueueURL = queueURL
		return testCtx.MgmtClient.Patch(testCtx.Context, current, crclient.MergeFromWithOptions(original, crclient.MergeFromWithOptimisticLock{}))
	})
}

func awsNodeUIDs(nodes []corev1.Node) []types.UID {
	identities := make([]types.UID, 0, len(nodes))
	for _, node := range nodes {
		identities = append(identities, node.UID)
	}
	return identities
}

func awsPoolMachineUIDs(testCtx *internal.TestContext, pool *hyperv1.NodePool) ([]types.UID, error) {
	machines := &capiv1.MachineList{}
	if err := testCtx.MgmtClient.List(testCtx.Context, machines, crclient.InNamespace(testCtx.ControlPlaneNamespace), crclient.MatchingLabels{capiv1.MachineDeploymentNameLabel: pool.Name}); err != nil {
		return nil, err
	}
	if len(machines.Items) == 0 {
		return nil, fmt.Errorf("no machines found for NodePool %s", pool.Name)
	}
	identities := make([]types.UID, 0, len(machines.Items))
	for _, machine := range machines.Items {
		identities = append(identities, machine.UID)
	}
	return identities, nil
}

type awsPoolNoRolloutObservation struct {
	violation error
}

func (observation *awsPoolNoRolloutObservation) observe(pool *hyperv1.NodePool) error {
	if observation.violation == nil {
		observation.violation = awsPoolNoRolloutConditions(pool)
	}
	return observation.violation
}

func awsPoolNoRolloutConditions(pool *hyperv1.NodePool) error {
	if pool == nil {
		return fmt.Errorf("cannot validate rollout conditions on a nil NodePool")
	}
	for _, conditionType := range []string{hyperv1.NodePoolUpdatingConfigConditionType, hyperv1.NodePoolUpdatingPlatformMachineTemplateConditionType} {
		condition := awsPoolCondition(pool, conditionType)
		if condition == nil {
			return fmt.Errorf("NodePool %s/%s is missing condition %s", pool.Namespace, pool.Name, conditionType)
		}
		if condition.Status != corev1.ConditionFalse {
			return fmt.Errorf("NodePool %s/%s condition %s must be False, got %q", pool.Namespace, pool.Name, conditionType, condition.Status)
		}
	}
	return nil
}

func awsPoolCondition(pool *hyperv1.NodePool, conditionType string) *hyperv1.NodePoolCondition {
	for index := range pool.Status.Conditions {
		if pool.Status.Conditions[index].Type == conditionType {
			return &pool.Status.Conditions[index]
		}
	}
	return nil
}

func awsLicensedAMIFromMessage(message string) string {
	return regexp.MustCompile(`\bami-(?:[0-9a-f]{17}|[0-9a-f]{8})\b`).FindString(message)
}
