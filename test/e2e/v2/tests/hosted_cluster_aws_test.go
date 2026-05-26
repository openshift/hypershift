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

	"github.com/aws/aws-sdk-go-v2/aws"
	ec2types "github.com/aws/aws-sdk-go-v2/service/ec2/types"
	elbv2 "github.com/aws/aws-sdk-go-v2/service/elasticloadbalancingv2"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	awsutil "github.com/openshift/hypershift/cmd/infra/aws/util"
	e2eutil "github.com/openshift/hypershift/test/e2e/util"
	"github.com/openshift/hypershift/test/e2e/v2/internal"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"
	crclient "sigs.k8s.io/controller-runtime/pkg/client"
)

func RegisterHostedClusterAWSTests(getTestCtx internal.TestContextGetter) {
	EnsureDefaultSecurityGroupTagsTest(getTestCtx)
	AWSCCMWithCustomizationsTest(getTestCtx)
}

func EnsureDefaultSecurityGroupTagsTest(getTestCtx internal.TestContextGetter) {
	When("a day-2 resource tag is added to the HostedCluster spec", func() {
		It("should apply the tag to the default worker security group via AWS API", Label("AWS"), func() {
			tc := getTestCtx()
			if e2eutil.IsLessThan(e2eutil.Version420) {
				Skip("default security group tags test requires version >= 4.20")
			}
			hc := tc.GetHostedCluster()
			if hc.Spec.Platform.Type != hyperv1.AWSPlatform {
				Skip("default security group tags test is only for AWS platform")
			}

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
			defer func() {
				Expect(cleanup()).To(Succeed(), "failed to cleanup role policy for tagging default security group")
			}()

			day2TagKey := "test-day2-tag"
			day2TagValue := "test-day2-value"

			originalTags := append([]hyperv1.AWSResourceTag(nil), hc.Spec.Platform.AWS.ResourceTags...)

			err = e2eutil.UpdateObject(GinkgoTB(), tc.Context, tc.MgmtClient, hc, func(obj *hyperv1.HostedCluster) {
				obj.Spec.Platform.AWS.ResourceTags = append(obj.Spec.Platform.AWS.ResourceTags, hyperv1.AWSResourceTag{
					Key:   day2TagKey,
					Value: day2TagValue,
				})
			})
			Expect(err).NotTo(HaveOccurred(), "failed to update HostedCluster with day-2 tag")
			DeferCleanup(func() {
				err := e2eutil.UpdateObject(GinkgoTB(), tc.Context, tc.MgmtClient, hc, func(obj *hyperv1.HostedCluster) {
					obj.Spec.Platform.AWS.ResourceTags = append([]hyperv1.AWSResourceTag(nil), originalTags...)
				})
				if err != nil && !apierrors.IsNotFound(err) {
					Expect(err).NotTo(HaveOccurred(), "cleanup: failed to restore HostedCluster AWS resource tags")
				}
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

func AWSCCMWithCustomizationsTest(getTestCtx internal.TestContextGetter) {
	Context("AWS CCM NLB Security Group", Label("AWS", "CCM"), func() {
		BeforeEach(func() {
			tc := getTestCtx()
			if e2eutil.IsLessThan(e2eutil.Version423) {
				Skip("AWS CCM NLB security group test requires version >= 4.23")
			}
			hc := tc.GetHostedCluster()
			if hc.Spec.Platform.Type != hyperv1.AWSPlatform {
				Skip("AWS CCM test is only for AWS platform")
			}
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
				tc.ValidateHostedClusterClient()
				hcClient := tc.GetHostedClusterClient()
				hc := tc.GetHostedCluster()

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

func extractLBNameFromHostname(hostname string) string {
	firstLabel := strings.SplitN(hostname, ".", 2)[0]
	firstLabel = strings.TrimPrefix(firstLabel, "internal-")
	lastHyphen := strings.LastIndex(firstLabel, "-")
	if lastHyphen == -1 {
		return firstLabel
	}
	return firstLabel[:lastHyphen]
}

var _ = Describe("Hosted Cluster AWS", Label("lifecycle", "hosted-cluster-aws"), func() {
	var testCtx *internal.TestContext

	BeforeEach(func() {
		testCtx = internal.GetTestContext()
		Expect(testCtx).NotTo(BeNil(), "test context should be set up in BeforeSuite")

		testCtx.ValidateHostedCluster()
	})

	RegisterHostedClusterAWSTests(func() *internal.TestContext { return testCtx })
})
