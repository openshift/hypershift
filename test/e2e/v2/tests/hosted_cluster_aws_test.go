//go:build e2ev2

/*
Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package tests

import (
	"fmt"
	"strings"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	awsutil "github.com/openshift/hypershift/cmd/infra/aws/util"
	e2eutil "github.com/openshift/hypershift/test/e2e/util"
	"github.com/openshift/hypershift/test/e2e/v2/internal"

	configv1 "github.com/openshift/api/config/v1"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/ec2"
	ec2types "github.com/aws/aws-sdk-go-v2/service/ec2/types"
	elbv2 "github.com/aws/aws-sdk-go-v2/service/elasticloadbalancingv2"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"

	capiaws "sigs.k8s.io/cluster-api-provider-aws/v2/api/v1beta2"
	capiv1 "sigs.k8s.io/cluster-api/api/core/v1beta1"
	crclient "sigs.k8s.io/controller-runtime/pkg/client"
)

func RegisterHostedClusterAWSTests(getTestCtx internal.TestContextGetter) {
	EnsureDefaultSecurityGroupTagsTest(getTestCtx)
	EnsureInfrastructureResourceTagsTest(getTestCtx)
	AWSCCMWithCustomizationsTest(getTestCtx)
	AWSResourceTagOverridePolicyTest(getTestCtx)
}

func EnsureDefaultSecurityGroupTagsTest(getTestCtx internal.TestContextGetter) {
	When("[Feature:AWSSecurityGroups] a day-2 resource tag is added to the HostedCluster spec", func() {
		It("should apply the tag to the default worker security group via AWS API", Label("AWS"), func() {
			tc := getTestCtx()
			tc.SkipIfVersionBelow(e2eutil.Version420)
			tc.SkipIfNotPlatform(hyperv1.AWSPlatform)
			hc, err := tc.GetHostedCluster()
			Expect(err).NotTo(HaveOccurred())

			Expect(hc.Status.Platform).NotTo(BeNil(),
				"HostedCluster %s/%s should have platform status", hc.Namespace, hc.Name)
			Expect(hc.Status.Platform.AWS).NotTo(BeNil(),
				"HostedCluster %s/%s should have AWS platform status", hc.Namespace, hc.Name)
			sgID := hc.Status.Platform.AWS.DefaultWorkerSecurityGroupID
			Expect(sgID).NotTo(BeEmpty(), "HostedCluster status should have DefaultWorkerSecurityGroupID set")

			awsCredsFile := internal.GetEnvVarValue("AWS_GUEST_INFRA_CREDENTIALS_FILE")
			Expect(awsCredsFile).NotTo(BeEmpty(), "AWS_GUEST_INFRA_CREDENTIALS_FILE must be set for AWS security group tests")

			region := hc.Spec.Platform.AWS.Region
			Expect(region).NotTo(BeEmpty(), "HostedCluster AWS region should be set")

			tagsPolicy := fmt.Sprintf(`{
				"Version": "2012-10-17",
				"Statement": [
					{
						"Effect": "Allow",
						"Action": [
							"ec2:CreateTags",
							"ec2:DeleteTags"
						],
						"Resource": "arn:aws:ec2:*:*:security-group/%s"
					}
				]
			}`, sgID)

			Expect(hc.Spec.Platform.AWS.RolesRef.ControlPlaneOperatorARN).NotTo(BeEmpty(),
				"HostedCluster should have ControlPlaneOperatorARN set")

			cleanup, err := e2eutil.PutRolePolicy(tc.Context, awsCredsFile, region,
				hc.Spec.Platform.AWS.RolesRef.ControlPlaneOperatorARN, tagsPolicy)
			Expect(err).NotTo(HaveOccurred(), "failed to put role policy for tagging default security group")
			DeferCleanup(func() {
				Expect(cleanup()).To(Succeed(), "failed to cleanup role policy for tagging default security group")
			})

			day2TagKey := "test-day2-tag"
			day2TagValue := "test-day2-value"

			originalTags := append([]hyperv1.AWSClusterResourceTag(nil), hc.Spec.Platform.AWS.ResourceTags...)

			err = e2eutil.UpdateObject(GinkgoTB(), tc.Context, tc.MgmtClient, hc, func(obj *hyperv1.HostedCluster) {
				obj.Spec.Platform.AWS.ResourceTags = append(obj.Spec.Platform.AWS.ResourceTags, hyperv1.AWSClusterResourceTag{
					Key:   day2TagKey,
					Value: day2TagValue,
				})
			})
			Expect(err).NotTo(HaveOccurred(), "failed to update HostedCluster with day-2 tag")
			DeferCleanup(func() {
				err := e2eutil.UpdateObject(GinkgoTB(), tc.Context, tc.MgmtClient, hc, func(obj *hyperv1.HostedCluster) {
					obj.Spec.Platform.AWS.ResourceTags = append([]hyperv1.AWSClusterResourceTag(nil), originalTags...)
				})
				if err != nil && !apierrors.IsNotFound(err) {
					Expect(err).NotTo(HaveOccurred(), "cleanup: failed to restore HostedCluster AWS resource tags")
				}

				hcClient, err := tc.GetHostedClusterClient(hc)
				Expect(err).NotTo(HaveOccurred())
				Eventually(func(g Gomega) {
					infra := &configv1.Infrastructure{}
					g.Expect(hcClient.Get(tc.Context, crclient.ObjectKey{Name: "cluster"}, infra)).To(Succeed())
					g.Expect(infra.Status.PlatformStatus).NotTo(BeNil())
					g.Expect(infra.Status.PlatformStatus.AWS).NotTo(BeNil())
					g.Expect(infra.Status.PlatformStatus.AWS.ResourceTags).NotTo(
						ContainElement(configv1.AWSResourceTag{Key: day2TagKey, Value: day2TagValue}),
						"cleanup: day-2 tag should be removed from infrastructure resource",
					)
				}, 5*time.Minute, 10*time.Second).Should(Succeed())
			})

			Eventually(func(g Gomega) {
				sg, err := e2eutil.GetDefaultSecurityGroup(tc.Context, awsCredsFile, region, sgID)
				g.Expect(err).NotTo(HaveOccurred(), "failed to get default security group")
				g.Expect(sg.Tags).To(ContainElement(ec2types.Tag{
					Key:   aws.String(day2TagKey),
					Value: aws.String(day2TagValue),
				}), "day-2 tag should be applied to the default worker security group")
			}, 10*time.Minute, time.Second).Should(Succeed())
		})
	})
}

func EnsureInfrastructureResourceTagsTest(getTestCtx internal.TestContextGetter) {
	When("a HostedCluster is created with additional AWS resource tags", func() {
		It("should propagate those tags to the infrastructure resource in the hosted cluster", Label("AWS"), func() {
			tc := getTestCtx()
			tc.SkipIfNotPlatform(hyperv1.AWSPlatform)
			hc, err := tc.GetHostedCluster()
			Expect(err).NotTo(HaveOccurred())

			// Re-fetch to get current server state; the cached hc pointer may be
			// stale if a prior test mutated it via UpdateObject.
			freshHC := &hyperv1.HostedCluster{}
			Expect(tc.MgmtClient.Get(tc.Context, crclient.ObjectKeyFromObject(hc), freshHC)).To(Succeed(),
				"failed to re-fetch HostedCluster")

			specTags := freshHC.Spec.Platform.AWS.ResourceTags
			if len(specTags) == 0 {
				Skip("HostedCluster does not have AWS resource tags configured")
			}

			// Filter kubernetes.io prefixed keys to match production logic in
			// support/globalconfig/infrastructure.go which skips them to avoid
			// breaking the AWS CSI driver.
			var expectedTags []configv1.AWSResourceTag
			for _, tag := range specTags {
				if strings.HasPrefix(tag.Key, "kubernetes.io") {
					continue
				}
				expectedTags = append(expectedTags, configv1.AWSResourceTag{
					Key:   tag.Key,
					Value: tag.Value,
				})
			}
			if len(expectedTags) == 0 {
				Skip("HostedCluster has only kubernetes.io-prefixed tags which are filtered out")
			}

			hcClient, err := tc.GetHostedClusterClient(hc)
			Expect(err).NotTo(HaveOccurred())

			infra := &configv1.Infrastructure{}
			Expect(hcClient.Get(tc.Context, crclient.ObjectKey{Name: "cluster"}, infra)).To(Succeed(),
				"failed to get infrastructure/cluster from hosted cluster")

			Expect(infra.Status.PlatformStatus).NotTo(BeNil(),
				"infrastructure/cluster should have platform status")
			Expect(infra.Status.PlatformStatus.AWS).NotTo(BeNil(),
				"infrastructure/cluster should have AWS platform status")
			Expect(infra.Status.PlatformStatus.AWS.ResourceTags).NotTo(BeEmpty(),
				"infrastructure/cluster AWS platform status should have resource tags")

			for _, expected := range expectedTags {
				Expect(infra.Status.PlatformStatus.AWS.ResourceTags).To(
					ContainElement(expected),
					"infrastructure resource should contain tag %s=%s", expected.Key, expected.Value)
			}

			Expect(infra.Status.PlatformStatus.AWS.ResourceTags).To(HaveLen(len(expectedTags)),
				"infrastructure resource should have exactly the non-kubernetes.io tags, no extra tags should leak through")
		})
	})
}

func AWSCCMWithCustomizationsTest(getTestCtx internal.TestContextGetter) {
	Context("[Feature:AWSNLB] AWS CCM NLB Security Group", Label("AWS", "CCM"), func() {
		BeforeEach(func() {
			tc := getTestCtx()
			tc.SkipIfVersionBelow(e2eutil.Version423)
			tc.SkipIfNotPlatform(hyperv1.AWSPlatform)
		})

		When("AWSServiceLBNetworkSecurityGroup feature gate is enabled", func() {
			It("should have NLBSecurityGroupMode=Managed in the aws-cloud-config ConfigMap", func() {
				tc := getTestCtx()

				Eventually(func(g Gomega) {
					cm := &corev1.ConfigMap{}
					g.Expect(tc.MgmtClient.Get(tc.Context, crclient.ObjectKey{
						Namespace: tc.ControlPlaneNamespace,
						Name:      "aws-cloud-config",
					}, cm)).To(Succeed(), "failed to get aws-cloud-config ConfigMap")

					awsConf, exists := cm.Data["aws.conf"]
					g.Expect(exists).To(BeTrue(), "aws.conf key should exist in ConfigMap")
					g.Expect(awsConf).To(ContainSubstring("NLBSecurityGroupMode = Managed"),
						"aws.conf should contain NLBSecurityGroupMode = Managed")
				}, 2*time.Minute, 5*time.Second).Should(Succeed())
			})
		})

		When("a LoadBalancer NLB service is created in the hosted cluster", func() {
			It("should attach managed security groups to the NLB", func() {
				tc := getTestCtx()
				hc, err := tc.GetHostedCluster()
				Expect(err).NotTo(HaveOccurred())
				hcClient, err := tc.GetHostedClusterClient(hc)
				Expect(err).NotTo(HaveOccurred())

				awsCredsFile := internal.GetEnvVarValue("AWS_GUEST_INFRA_CREDENTIALS_FILE")
				Expect(awsCredsFile).NotTo(BeEmpty(), "AWS_GUEST_INFRA_CREDENTIALS_FILE must be set for AWS CCM NLB test")

				testNS := &corev1.Namespace{
					ObjectMeta: metav1.ObjectMeta{
						Name: "test-ccm-nlb-sg",
					},
				}
				Expect(hcClient.Create(tc.Context, testNS)).To(Succeed(), "failed to create test namespace")
				DeferCleanup(func() {
					err := hcClient.Delete(tc.Context, testNS)
					if err != nil && !apierrors.IsNotFound(err) {
						Expect(err).NotTo(HaveOccurred(), "cleanup: failed to delete test namespace %s", testNS.Name)
					}
				})

				testSvc := &corev1.Service{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "test-ccm-nlb-sg-svc",
						Namespace: testNS.Name,
						Annotations: map[string]string{
							"service.beta.kubernetes.io/aws-load-balancer-type":                     "nlb",
							"service.beta.kubernetes.io/aws-load-balancer-additional-resource-tags": "red-hat-managed=true",
						},
					},
					Spec: corev1.ServiceSpec{
						Type: corev1.ServiceTypeLoadBalancer,
						Selector: map[string]string{
							"app": "test-ccm-nlb-sg",
						},
						Ports: []corev1.ServicePort{
							{
								Name:       "http",
								Port:       80,
								TargetPort: intstr.FromInt32(8080),
								Protocol:   corev1.ProtocolTCP,
							},
						},
					},
				}
				Expect(hcClient.Create(tc.Context, testSvc)).To(Succeed(), "failed to create LoadBalancer service")
				DeferCleanup(func() {
					err := hcClient.Delete(tc.Context, testSvc)
					if err != nil && !apierrors.IsNotFound(err) {
						Expect(err).NotTo(HaveOccurred(), "cleanup: failed to delete test service %s", testSvc.Name)
					}
				})

				var lbHostname string
				Eventually(func(g Gomega) {
					svc := &corev1.Service{}
					g.Expect(hcClient.Get(tc.Context, crclient.ObjectKey{
						Namespace: testNS.Name,
						Name:      testSvc.Name,
					}, svc)).To(Succeed(), "failed to get test service")

					g.Expect(svc.Status.LoadBalancer.Ingress).NotTo(BeEmpty(),
						"LoadBalancer should have at least one ingress entry")
					g.Expect(svc.Status.LoadBalancer.Ingress[0].Hostname).NotTo(BeEmpty(),
						"LoadBalancer ingress hostname should be set")
					lbHostname = svc.Status.LoadBalancer.Ingress[0].Hostname
				}, 5*time.Minute, 10*time.Second).Should(Succeed())

				lbName := extractLBNameFromHostname(lbHostname)
				Expect(lbName).NotTo(BeEmpty(), "load balancer name should be extracted from hostname %s", lbHostname)

				awsSession := awsutil.NewSession(tc.Context, "e2e-ccm-nlb-sg", awsCredsFile, "", "", hc.Spec.Platform.AWS.Region)
				Expect(awsSession).NotTo(BeNil(), "failed to create AWS session")

				awsConfig := awsutil.NewConfig()
				Expect(awsConfig).NotTo(BeNil(), "failed to create AWS config")

				elbv2Client := elbv2.NewFromConfig(*awsSession, func(o *elbv2.Options) {
					o.Retryer = awsConfig()
				})

				describeLBInput := &elbv2.DescribeLoadBalancersInput{
					Names: []string{lbName},
				}

				waiter := elbv2.NewLoadBalancerAvailableWaiter(elbv2Client, func(o *elbv2.LoadBalancerAvailableWaiterOptions) {
					o.MinDelay = 5 * time.Second
					o.MaxDelay = 30 * time.Second
				})
				Expect(waiter.Wait(tc.Context, describeLBInput, 3*time.Minute)).To(Succeed(),
					"load balancer %s did not become available in time", lbName)

				describeLBOutput, err := elbv2Client.DescribeLoadBalancers(tc.Context, describeLBInput)
				Expect(err).NotTo(HaveOccurred(), "failed to describe load balancer %s", lbName)
				Expect(describeLBOutput.LoadBalancers).NotTo(BeEmpty(),
					"no load balancers found with name %s", lbName)

				lb := describeLBOutput.LoadBalancers[0]
				Expect(lb.SecurityGroups).NotTo(BeEmpty(),
					"load balancer should have security groups attached when NLBSecurityGroupMode = Managed")
			})
		})
	})
}

func AWSResourceTagOverridePolicyTest(getTestCtx internal.TestContextGetter) {
	When("[Feature:AWSResourceTagOverrides] HostedCluster tags have mixed override policies", func() {
		It("should block or allow NodePool tag overrides based on overridePolicy and reflect conflicts in the NodePool condition", Label("AWS"), func() {
			tc := getTestCtx()
			tc.SkipIfNotPlatform(hyperv1.AWSPlatform)

			hc, err := tc.GetHostedCluster()
			Expect(err).NotTo(HaveOccurred(), "failed to get HostedCluster")

			hcClient, err := tc.GetHostedClusterClient(hc)
			Expect(err).NotTo(HaveOccurred(), "failed to get hosted cluster client")

			ctx := tc.Context

			originalTags := append([]hyperv1.AWSClusterResourceTag(nil), hc.Spec.Platform.AWS.ResourceTags...)
			err = e2eutil.UpdateObject(GinkgoTB(), ctx, tc.MgmtClient, hc, func(obj *hyperv1.HostedCluster) {
				obj.Spec.Platform.AWS.ResourceTags = append(obj.Spec.Platform.AWS.ResourceTags,
					hyperv1.AWSClusterResourceTag{
						Key:            "e2e-tag-deny",
						Value:          "hc-deny-value",
						OverridePolicy: hyperv1.AWSResourceTagOverridePolicyDeny,
					},
					hyperv1.AWSClusterResourceTag{
						Key:            "e2e-tag-allow",
						Value:          "hc-allow-value",
						OverridePolicy: hyperv1.AWSResourceTagOverridePolicyAllow,
					},
				)
			})
			Expect(err).NotTo(HaveOccurred(), "failed to update HostedCluster with override policy tags")
			DeferCleanup(func() {
				err := e2eutil.UpdateObject(GinkgoTB(), ctx, tc.MgmtClient, hc, func(obj *hyperv1.HostedCluster) {
					obj.Spec.Platform.AWS.ResourceTags = append([]hyperv1.AWSClusterResourceTag(nil), originalTags...)
				})
				if err != nil && !apierrors.IsNotFound(err) {
					Expect(err).NotTo(HaveOccurred(), "cleanup: failed to restore HostedCluster AWS resource tags")
				}
			})

			defaultNP := getDefaultNodePool(ctx, tc.MgmtClient, hc)
			Expect(defaultNP).NotTo(BeNil(), "default NodePool should exist")

			var oneReplica int32 = 1
			np := buildTestNodePool(defaultNP, "tag-override", func(pool *hyperv1.NodePool) {
				pool.Spec.Replicas = &oneReplica
				pool.Spec.Platform.AWS.ResourceTags = []hyperv1.AWSNodePoolResourceTag{
					{Key: "e2e-tag-deny", Value: "np-deny-value"},
					{Key: "e2e-tag-allow", Value: "np-allow-value"},
				}
			})

			err = tc.MgmtClient.Create(ctx, np)
			Expect(err).NotTo(HaveOccurred(), "failed to create NodePool %s", np.Name)
			GinkgoWriter.Printf("Created NodePool %s with conflicting tags\n", np.Name)
			DeferCleanup(func() {
				cleanupNodePool(ctx, tc.MgmtClient, np)
			})

			e2eutil.WaitForReadyNodesByNodePool(GinkgoTB(), ctx, hcClient, np, hc.Spec.Platform.Type)

			Eventually(func(g Gomega) {
				fresh := &hyperv1.NodePool{}
				g.Expect(tc.MgmtClient.Get(ctx, crclient.ObjectKeyFromObject(np), fresh)).To(Succeed())

				var conflictCond *hyperv1.NodePoolCondition
				for i := range fresh.Status.Conditions {
					if fresh.Status.Conditions[i].Type == hyperv1.NodePoolAWSResourceTagConflictConditionType {
						conflictCond = &fresh.Status.Conditions[i]
						break
					}
				}
				g.Expect(conflictCond).NotTo(BeNil(),
					"NodePool %s should have AWSResourceTagConflict condition", np.Name)
				g.Expect(conflictCond.Status).To(Equal(corev1.ConditionTrue),
					"AWSResourceTagConflict should be True when a Deny conflict exists")
				g.Expect(conflictCond.Reason).To(Equal(hyperv1.AWSResourceTagConflictDetectedReason))
				g.Expect(conflictCond.Message).To(ContainSubstring("e2e-tag-deny"),
					"condition message should report the blocked key")
				g.Expect(conflictCond.Message).To(ContainSubstring("e2e-tag-allow"),
					"condition message should report the overridden key")
			}, 5*time.Minute, 10*time.Second).Should(Succeed())

			awsCredsFile := internal.GetEnvVarValue("AWS_GUEST_INFRA_CREDENTIALS_FILE")
			Expect(awsCredsFile).NotTo(BeEmpty(), "AWS_GUEST_INFRA_CREDENTIALS_FILE must be set")
			region := hc.Spec.Platform.AWS.Region

			awsSession := awsutil.NewSession(ctx, "e2e-tag-override", awsCredsFile, "", "", region)
			awsConfig := awsutil.NewConfig()
			ec2Client := ec2.NewFromConfig(*awsSession, func(o *ec2.Options) {
				o.Retryer = awsConfig()
			})

			awsMachines := &capiaws.AWSMachineList{}
			err = tc.MgmtClient.List(ctx, awsMachines,
				crclient.InNamespace(tc.ControlPlaneNamespace),
				crclient.MatchingLabels{capiv1.MachineDeploymentNameLabel: np.Name})
			Expect(err).NotTo(HaveOccurred(), "failed to list AWSMachines")
			Expect(awsMachines.Items).NotTo(BeEmpty(),
				"expected at least one AWSMachine for NodePool %s", np.Name)

			inspectedInstances := 0
			for _, awsMachine := range awsMachines.Items {
				Expect(awsMachine.Spec.AdditionalTags).To(HaveKeyWithValue("e2e-tag-deny", "hc-deny-value"),
					"AWSMachine %s should have HC value for Deny tag", awsMachine.Name)
				Expect(awsMachine.Spec.AdditionalTags).To(HaveKeyWithValue("e2e-tag-allow", "np-allow-value"),
					"AWSMachine %s should have NP value for Allow tag", awsMachine.Name)

				if awsMachine.Spec.InstanceID == nil {
					continue
				}
				instance, err := ec2Client.DescribeInstances(ctx, &ec2.DescribeInstancesInput{
					InstanceIds: []string{aws.ToString(awsMachine.Spec.InstanceID)},
				})
				Expect(err).NotTo(HaveOccurred(), "failed to describe EC2 instance %s", aws.ToString(awsMachine.Spec.InstanceID))
				Expect(instance.Reservations).NotTo(BeEmpty())
				Expect(instance.Reservations[0].Instances).NotTo(BeEmpty())
				tags := instance.Reservations[0].Instances[0].Tags

				Expect(tags).To(ContainElement(ec2types.Tag{
					Key:   aws.String("e2e-tag-deny"),
					Value: aws.String("hc-deny-value"),
				}), "EC2 instance should have HC value for Deny tag")
				Expect(tags).To(ContainElement(ec2types.Tag{
					Key:   aws.String("e2e-tag-allow"),
					Value: aws.String("np-allow-value"),
				}), "EC2 instance should have NP value for Allow tag")
				inspectedInstances++
			}
			Expect(inspectedInstances).To(BeNumerically(">=", 1),
				"expected to inspect at least one EC2 instance but all AWSMachines had nil InstanceID")
		})
	})
}

func extractLBNameFromHostname(hostname string) string {
	firstLabel := strings.SplitN(hostname, ".", 2)[0]
	firstLabel = strings.TrimPrefix(firstLabel, "internal-")
	lastHyphen := strings.LastIndex(firstLabel, "-")
	if lastHyphen == -1 {
		return firstLabel
	}
	return firstLabel[:lastHyphen]
}

var _ = Describe("[sig-hypershift][Jira:Hypershift] Hosted Cluster AWS", Label("lifecycle", "hosted-cluster-aws"), func() {
	var testCtx *internal.TestContext

	BeforeEach(func() {
		testCtx = internal.GetTestContext()
		Expect(testCtx).NotTo(BeNil(), "test context should be set up in BeforeSuite")
	})

	RegisterHostedClusterAWSTests(func() *internal.TestContext { return testCtx })
})
