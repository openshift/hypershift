//go:build e2ev2

package tests

import (
	"context"
	_ "embed"
	"encoding/json"
	"fmt"
	"io"
	"maps"
	"os"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/ec2"
	ec2types "github.com/aws/aws-sdk-go-v2/service/ec2/types"
	awskarpenterv1 "github.com/aws/karpenter-provider-aws/pkg/apis/v1"
	"github.com/blang/semver"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	hyperkarpenterv1 "github.com/openshift/hypershift/api/karpenter/v1"
	"github.com/openshift/hypershift/hypershift-operator/controllers/manifests"
	karpenterassets "github.com/openshift/hypershift/karpenter-operator/controllers/karpenter/assets"
	cpconst "github.com/openshift/hypershift/pkg/controlplane"
	npmetrics "github.com/openshift/hypershift/pkg/metrics/nodepool"
	karpenterutil "github.com/openshift/hypershift/support/karpenter"
	"github.com/openshift/hypershift/support/releaseinfo"
	"github.com/openshift/hypershift/support/supportedversion"
	e2eutil "github.com/openshift/hypershift/test/e2e/util"
	"github.com/openshift/hypershift/test/e2e/v2/internal"
	v2util "github.com/openshift/hypershift/test/e2e/v2/util"
	dto "github.com/prometheus/client_model/go"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	policyv1 "k8s.io/api/policy/v1"
	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/util/intstr"
	"k8s.io/apimachinery/pkg/util/wait"
	kubeclient "k8s.io/client-go/kubernetes"
	"k8s.io/utils/ptr"
	crclient "sigs.k8s.io/controller-runtime/pkg/client"
	karpenterv1 "sigs.k8s.io/karpenter/pkg/apis/v1"
	"sigs.k8s.io/yaml"
)

//go:embed assets/karpenter-kubelet-checker-pod.yaml
var kubeletCheckerPodRaw []byte

var kubeletCheckerPodTemplate = func() *corev1.Pod {
	pod := &corev1.Pod{}
	if err := yaml.Unmarshal(kubeletCheckerPodRaw, pod); err != nil {
		panic(err)
	}
	return pod
}()

func RegisterKarpenterTests(getTestCtx internal.TestContextGetter) {
	// Note: in the v1 tests, parallel subtests that provision nodes must create
	// their own OpenshiftEC2NodeClass rather than using the "default" class, because
	// the instance-profile test mutates the default EC2NodeClass which would trigger
	// NodeClassDrift on any NodeClaims referencing it.
	//
	// In v2, all the tests are serialized within a cluster. The note on parallelism
	// is preserved to help inform future refactors.

	KarpenterPlumbingTests(getTestCtx)
	KarpenterARM64ProvisioningTest(getTestCtx)
	KarpenterInstanceProfileTest(getTestCtx)
	KarpenterNodeClassVersionTest(getTestCtx)
	KarpenterCapacityReservationTest(getTestCtx)
	KarpenterArbitrarySubnetTest(getTestCtx)
	KarpenterKubeletPropagationTest(getTestCtx)
	KarpenterAutoNodeLifecycleTest(getTestCtx)
	// This test intentionally leaves dangling resources so cluster teardown must
	// force-terminate nodes despite a blocking PDB. It must run last.
	KarpenterBillingConsolidationTest(getTestCtx)
}

var _ = Describe("[sig-hypershift][Jira:Hypershift] Karpenter",
	Label("lifecycle", "karpenter", internal.InformingLabel), Ordered, func() {
		var testCtx *internal.TestContext

		BeforeEach(func() {
			testCtx = internal.GetTestContext()
			Expect(testCtx).NotTo(BeNil(), "test context should be set up in BeforeSuite")

			// Skips unless the Karpenter v1 API is available.
			// The v1 API exists on 4.23+, but when the operator is built from main and
			// tested against a 4.22 hosted cluster, set RUN_KARPENTER_TESTS=true to
			// lower the gate to 4.22.
			if internal.GetEnvVarValue("RUN_KARPENTER_TESTS") == "true" {
				testCtx.SkipIfVersionBelow(e2eutil.Version422)
			} else {
				testCtx.SkipIfVersionBelow(e2eutil.Version423)
			}
		})

		RegisterKarpenterTests(func() *internal.TestContext { return testCtx })
	})

// ---------------------------------------------------------------------------
// Plumbing tests — stateless validation of Karpenter infrastructure
// ---------------------------------------------------------------------------

func KarpenterPlumbingTests(getTestCtx internal.TestContextGetter) {
	Context("[Feature:AutoNode] Karpenter Plumbing", func() {
		BeforeEach(func() {
			tc := getTestCtx()
			tc.SkipIfNotPlatform(hyperv1.AWSPlatform)
			hc, err := tc.GetHostedCluster()
			Expect(err).NotTo(HaveOccurred())
			if !karpenterutil.IsKarpenterEnabled(hc.Spec.AutoNode) {
				Skip("AutoNode not configured on hosted cluster")
			}
		})

		It("should expose Karpenter metrics", func() {
			tc := getTestCtx()
			ctx := tc.Context
			t := GinkgoTB()
			hc, err := tc.GetHostedCluster()
			Expect(err).NotTo(HaveOccurred())

			karpenterMetrics := []string{
				karpenterassets.KarpenterBuildInfoMetricName,
				karpenterassets.KarpenterOperatorInfoMetricName,
			}
			karpenterNamespace := manifests.HostedControlPlaneNamespace(hc.Namespace, hc.Name)

			By("Waiting for Karpenter and Karpenter operator metrics to be exposed")
			err = wait.PollUntilContextTimeout(ctx, 10*time.Second, 5*time.Minute, true, func(ctx context.Context) (bool, error) {
				kmf, err := e2eutil.GetMetricsFromPod(ctx, tc.MgmtClient, cpconst.KarpenterComponentName, cpconst.KarpenterComponentName, karpenterNamespace, "8080")
				if err != nil {
					GinkgoWriter.Printf("Unable to get Karpenter metrics: %v\n", err)
					return false, nil
				}
				komf, err := e2eutil.GetMetricsFromPod(ctx, tc.MgmtClient, cpconst.KarpenterOperatorComponentName, cpconst.KarpenterOperatorComponentName, karpenterNamespace, "8080")
				if err != nil {
					GinkgoWriter.Printf("Unable to get Karpenter metrics: %v\n", err)
					return false, nil
				}
				combined := map[string]*dto.MetricFamily{}
				if kmf != nil {
					maps.Copy(combined, kmf)
				}
				if komf != nil {
					maps.Copy(combined, komf)
				}
				for _, metricName := range karpenterMetrics {
					if !e2eutil.ValidateMetricPresence(t, combined, metricName, "", "", metricName, true) {
						return false, nil
					}
				}

				GinkgoWriter.Printf("Expected metrics are exposed: %v\n", karpenterMetrics)
				return true, nil
			})
			Expect(err).NotTo(HaveOccurred(), "failed to validate Karpenter metrics")
		})

		It("should report AutoNode vCPUs status as 0 when no Karpenter nodes are provisioned", func() {
			tc := getTestCtx()
			hc, err := tc.GetHostedCluster()
			Expect(err).NotTo(HaveOccurred())
			v2util.WaitForAutoNodeStatusVCPUs(Default, tc.Context, tc.MgmtClient, hc, 0)
		})

		It("should have Karpenter CRDs installed in the hosted cluster", func() {
			tc := getTestCtx()
			ctx := tc.Context
			t := GinkgoTB()
			hc, err := tc.GetHostedCluster()
			Expect(err).NotTo(HaveOccurred())
			hcClient, err := tc.GetHostedClusterClient(hc)
			Expect(err).NotTo(HaveOccurred())

			expectedCRDs := []string{
				"ec2nodeclasses.karpenter.k8s.aws",
				"openshiftec2nodeclasses.karpenter.hypershift.openshift.io",
				"nodepools.karpenter.sh",
				"nodeclaims.karpenter.sh",
			}
			for _, crdName := range expectedCRDs {
				e2eutil.EventuallyObject(t, ctx, fmt.Sprintf("CRD %s to exist in the hosted cluster", crdName),
					func(ctx context.Context) (*apiextensionsv1.CustomResourceDefinition, error) {
						crd := &apiextensionsv1.CustomResourceDefinition{}
						err := hcClient.Get(ctx, crclient.ObjectKey{Name: crdName}, crd)
						return crd, err
					},
					nil,
					e2eutil.WithTimeout(2*time.Minute),
				)
			}
		})

		It("should have default OpenshiftEC2NodeClass with correct subnet and security group selectors", func() {
			tc := getTestCtx()
			ctx := tc.Context
			t := GinkgoTB()
			hc, err := tc.GetHostedCluster()
			Expect(err).NotTo(HaveOccurred())
			hcClient, err := tc.GetHostedClusterClient(hc)
			Expect(err).NotTo(HaveOccurred())

			By("Validating default OpenshiftEC2NodeClass exists with expected values")
			infraID := hc.Spec.InfraID
			e2eutil.EventuallyObject(t, ctx, "default OpenshiftEC2NodeClass to have expected spec",
				func(ctx context.Context) (*hyperkarpenterv1.OpenshiftEC2NodeClass, error) {
					nc := &hyperkarpenterv1.OpenshiftEC2NodeClass{}
					err := hcClient.Get(ctx, crclient.ObjectKey{Name: karpenterassets.EC2NodeClassDefault}, nc)
					return nc, err
				},
				[]e2eutil.Predicate[*hyperkarpenterv1.OpenshiftEC2NodeClass]{
					func(nc *hyperkarpenterv1.OpenshiftEC2NodeClass) (bool, string, error) {
						if len(nc.Spec.SubnetSelectorTerms) == 0 {
							return false, "SubnetSelectorTerms is empty", nil
						}
						subnetTags := nc.Spec.SubnetSelectorTerms[0].Tags
						internalELBTagKey := "kubernetes.io/role/internal-elb"
						if subnetTags[internalELBTagKey] != "1" {
							return false, fmt.Sprintf("expected subnet tag %s=1, got %v", internalELBTagKey, subnetTags), nil
						}
						clusterTagKey := fmt.Sprintf("kubernetes.io/cluster/%s", infraID)
						if subnetTags[clusterTagKey] != "*" {
							return false, fmt.Sprintf("expected subnet tag %s=*, got %v", clusterTagKey, subnetTags), nil
						}
						if len(nc.Spec.SecurityGroupSelectorTerms) == 0 {
							return false, "SecurityGroupSelectorTerms is empty", nil
						}
						sgTags := nc.Spec.SecurityGroupSelectorTerms[0].Tags
						discoveryTagKey := "karpenter.sh/discovery"
						if sgTags[discoveryTagKey] != infraID {
							return false, fmt.Sprintf("expected SG tag %s=%s, got %v", discoveryTagKey, infraID, sgTags), nil
						}
						return true, "default OpenshiftEC2NodeClass has expected fields set", nil
					},
				},
				e2eutil.WithTimeout(1*time.Minute),
			)
		})

		It("should have default EC2NodeClass with immutable service-owned fields", func() {
			tc := getTestCtx()
			ctx := tc.Context
			t := GinkgoTB()
			hc, err := tc.GetHostedCluster()
			Expect(err).NotTo(HaveOccurred())
			hcClient, err := tc.GetHostedClusterClient(hc)
			Expect(err).NotTo(HaveOccurred())

			expectedTags := v2util.ExpectedPlatformTags(Default, hc)
			Expect(expectedTags).NotTo(BeEmpty(), "HostedCluster has no non-restricted resource tags; cluster setup must include at least one propagatable tag for this test to be meaningful")

			By("Validating default EC2NodeClass has immutable service-owned fields set")
			e2eutil.EventuallyObject(t, ctx, "EC2NodeClass to have service-owned fields populated",
				func(ctx context.Context) (*awskarpenterv1.EC2NodeClass, error) {
					nc := &awskarpenterv1.EC2NodeClass{}
					err := hcClient.Get(ctx, crclient.ObjectKey{Name: karpenterassets.EC2NodeClassDefault}, nc)
					return nc, err
				},
				[]e2eutil.Predicate[*awskarpenterv1.EC2NodeClass]{
					func(nc *awskarpenterv1.EC2NodeClass) (bool, string, error) {
						if len(nc.Spec.AMISelectorTerms) == 0 {
							return false, "AMISelectorTerms is empty", nil
						}
						if nc.Spec.AMIFamily == nil || *nc.Spec.AMIFamily != "Custom" {
							return false, fmt.Sprintf("expected AMIFamily=Custom, got %v", nc.Spec.AMIFamily), nil
						}
						if nc.Spec.UserData == nil || strings.TrimSpace(*nc.Spec.UserData) == "" {
							return false, "UserData is empty", nil
						}
						for k, v := range expectedTags {
							if nc.Spec.Tags[k] != v {
								return false, fmt.Sprintf("expected tag %s=%s, got %v", k, v, nc.Spec.Tags[k]), nil
							}
						}
						return true, "default EC2NodeClass has expected fields set", nil
					},
				},
				e2eutil.WithTimeout(1*time.Minute),
			)
		})

		It("should block direct deletion of EC2NodeClass", func() {
			tc := getTestCtx()
			hc, err := tc.GetHostedCluster()
			Expect(err).NotTo(HaveOccurred())
			hcClient, err := tc.GetHostedClusterClient(hc)
			Expect(err).NotTo(HaveOccurred())

			ec2NodeClass := &awskarpenterv1.EC2NodeClass{}
			Expect(hcClient.Get(tc.Context, crclient.ObjectKey{Name: karpenterassets.EC2NodeClassDefault}, ec2NodeClass)).To(Succeed())
			Expect(hcClient.Delete(tc.Context, ec2NodeClass)).To(MatchError(ContainSubstring("EC2NodeClass resource can't be created/updated/deleted directly, please use OpenshiftEC2NodeClass resource instead")))
		})

		It("should block direct mutation of EC2NodeClass", func() {
			tc := getTestCtx()
			hc, err := tc.GetHostedCluster()
			Expect(err).NotTo(HaveOccurred())
			hcClient, err := tc.GetHostedClusterClient(hc)
			Expect(err).NotTo(HaveOccurred())

			ec2NodeClass := &awskarpenterv1.EC2NodeClass{}
			Expect(hcClient.Get(tc.Context, crclient.ObjectKey{Name: karpenterassets.EC2NodeClassDefault}, ec2NodeClass)).To(Succeed())
			ec2NodeClassCopy := ec2NodeClass.DeepCopy()
			ec2NodeClassCopy.Spec.AMISelectorTerms = []awskarpenterv1.AMISelectorTerm{{ID: "ami-fake123"}}
			Expect(hcClient.Update(tc.Context, ec2NodeClassCopy)).To(MatchError(ContainSubstring("EC2NodeClass resource can't be created/updated/deleted directly, please use OpenshiftEC2NodeClass resource instead")))
		})

		It("should have AutoNodeEnabled condition set to True", func() {
			tc := getTestCtx()
			ctx := tc.Context
			t := GinkgoTB()
			hc, err := tc.GetHostedCluster()
			Expect(err).NotTo(HaveOccurred())

			e2eutil.EventuallyObject(t, ctx, fmt.Sprintf("HostedCluster %s/%s to have AutoNodeEnabled condition", hc.Namespace, hc.Name),
				func(ctx context.Context) (*hyperv1.HostedCluster, error) {
					obj := &hyperv1.HostedCluster{}
					err := tc.MgmtClient.Get(ctx, crclient.ObjectKeyFromObject(hc), obj)
					return obj, err
				},
				[]e2eutil.Predicate[*hyperv1.HostedCluster]{
					e2eutil.ConditionPredicate[*hyperv1.HostedCluster](e2eutil.Condition{
						Type:   string(hyperv1.AutoNodeEnabled),
						Status: metav1.ConditionTrue,
						Reason: hyperv1.AsExpectedReason,
					}),
				},
				e2eutil.WithTimeout(2*time.Minute),
			)
		})
	})
}

func KarpenterARM64ProvisioningTest(getTestCtx internal.TestContextGetter) {
	Context("[Feature:AutoNode] ARM64 instance provisioning", func() {
		BeforeEach(func() {
			tc := getTestCtx()
			tc.SkipIfNotPlatform(hyperv1.AWSPlatform)
			hc, err := tc.GetHostedCluster()
			Expect(err).NotTo(HaveOccurred())
			if !karpenterutil.IsKarpenterEnabled(hc.Spec.AutoNode) {
				Skip("AutoNode not configured on hosted cluster")
			}
			if internal.GetEnvVarValue("AWS_MULTI_ARCH") == "" {
				Skip("test only supported on multi-arch clusters")
			}
		})

		It("should provision and deprovision ARM64 nodes", func() {
			tc := getTestCtx()
			ctx := tc.Context
			t := GinkgoTB()
			hc, err := tc.GetHostedCluster()
			Expect(err).NotTo(HaveOccurred())
			hcClient, err := tc.GetHostedClusterClient(hc)
			Expect(err).NotTo(HaveOccurred())

			armNodeClass := &hyperkarpenterv1.OpenshiftEC2NodeClass{
				ObjectMeta: metav1.ObjectMeta{Name: "arm-nodeclass"},
				Spec: hyperkarpenterv1.OpenshiftEC2NodeClassSpec{
					SubnetSelectorTerms: []hyperkarpenterv1.SubnetSelectorTerm{
						{Tags: map[string]string{"karpenter.sh/discovery": hc.Spec.InfraID}},
					},
					SecurityGroupSelectorTerms: []hyperkarpenterv1.SecurityGroupSelectorTerm{
						{Tags: map[string]string{"karpenter.sh/discovery": hc.Spec.InfraID}},
					},
				},
			}
			By(fmt.Sprintf("Creating OpenshiftEC2NodeClass %q for ARM64", armNodeClass.Name))
			Expect(hcClient.Create(ctx, armNodeClass)).To(Succeed())
			DeferCleanup(func() {
				if err := hcClient.Delete(ctx, armNodeClass); err != nil && !apierrors.IsNotFound(err) {
					Expect(err).NotTo(HaveOccurred(), "cleanup: failed to delete OpenshiftEC2NodeClass %s", armNodeClass.Name)
				}
			})

			armNodePool := v2util.BaseNodePool("arm-nodepool", armNodeClass.Name)
			armNodePool.Spec.Template.Spec.Requirements = []karpenterv1.NodeSelectorRequirementWithMinValues{
				{Key: "node.kubernetes.io/instance-type", Operator: corev1.NodeSelectorOpIn, Values: []string{"m6g.xlarge"}},
				{Key: "kubernetes.io/arch", Operator: corev1.NodeSelectorOpIn, Values: []string{"arm64"}},
				{Key: karpenterv1.CapacityTypeLabelKey, Operator: corev1.NodeSelectorOpIn, Values: []string{karpenterv1.CapacityTypeOnDemand}},
			}
			// quay.io/openshift/origin-pod does not support arm64
			armWorkLoads := v2util.TestWorkloadWithImage("arm-app", 1, map[string]string{karpenterv1.NodePoolLabelKey: armNodePool.Name}, "registry.access.redhat.com/ubi10/ubi-minimal:10.1")

			armNodeLabels := map[string]string{
				karpenterv1.NodePoolLabelKey: armNodePool.Name,
				"kubernetes.io/arch":         "arm64",
			}

			v2util.CreateKarpenterNodePoolAndWorkload(Default, ctx, hcClient, hc.Spec.Platform.Type, armNodePool, armWorkLoads, armNodeLabels, false)

			nodes := e2eutil.WaitForReadyNodesByLabels(t, ctx, hcClient, hc.Spec.Platform.Type, 1, armNodeLabels)
			v2util.WaitForReadyKarpenterPods(Default, ctx, hcClient, nodes, nil, 1, map[string]string{"app": "arm-app"})
		})
	})
}

func KarpenterInstanceProfileTest(getTestCtx internal.TestContextGetter) {
	Context("[Feature:AutoNode] Instance profile annotation propagation", func() {
		BeforeEach(func() {
			tc := getTestCtx()
			tc.SkipIfNotPlatform(hyperv1.AWSPlatform)
			hc, err := tc.GetHostedCluster()
			Expect(err).NotTo(HaveOccurred())
			if !karpenterutil.IsKarpenterEnabled(hc.Spec.AutoNode) {
				Skip("AutoNode not configured on hosted cluster")
			}
		})

		It("should propagate instance profile annotation to EC2NodeClass and EC2 instances", func() {
			tc := getTestCtx()
			ctx := tc.Context
			t := GinkgoTB()
			hc, err := tc.GetHostedCluster()
			Expect(err).NotTo(HaveOccurred())
			hcClient, err := tc.GetHostedClusterClient(hc)
			Expect(err).NotTo(HaveOccurred())
			awsCredsFile := internal.GetEnvVarValue("AWS_GUEST_INFRA_CREDENTIALS_FILE")
			awsRegion := hc.Spec.Platform.AWS.Region

			workerInstanceProfile := hc.Spec.InfraID + "-worker"

			var origInstanceProfile string
			By(fmt.Sprintf("Applying HostedCluster annotation %s=%s", hyperv1.AWSKarpenterDefaultInstanceProfile, workerInstanceProfile))
			err = e2eutil.UpdateObject(t, ctx, tc.MgmtClient, hc, func(obj *hyperv1.HostedCluster) {
				if obj.Annotations == nil {
					obj.Annotations = make(map[string]string)
				}
				if existingProfile, hasExistingProfile := obj.Annotations[hyperv1.AWSKarpenterDefaultInstanceProfile]; hasExistingProfile {
					origInstanceProfile = existingProfile
				}
				obj.Annotations[hyperv1.AWSKarpenterDefaultInstanceProfile] = workerInstanceProfile
			})
			Expect(err).NotTo(HaveOccurred())

			DeferCleanup(func() {
				// Remove the annotation and verify it gets cleared from EC2NodeClass
				current := &hyperv1.HostedCluster{}
				Expect(tc.MgmtClient.Get(ctx, crclient.ObjectKeyFromObject(hc), current)).To(Succeed(),
					"cleanup: failed to get HostedCluster %s/%s", hc.Namespace, hc.Name)
				Expect(e2eutil.UpdateObject(t, ctx, tc.MgmtClient, current, func(obj *hyperv1.HostedCluster) {
					if len(origInstanceProfile) > 0 {
						if obj.Annotations == nil {
							obj.Annotations = make(map[string]string)
						}
						obj.Annotations[hyperv1.AWSKarpenterDefaultInstanceProfile] = origInstanceProfile
						GinkgoWriter.Printf("Restored annotation %s=%s to HostedCluster\n", hyperv1.AWSKarpenterDefaultInstanceProfile, origInstanceProfile)
					} else {
						delete(obj.Annotations, hyperv1.AWSKarpenterDefaultInstanceProfile)
						GinkgoWriter.Printf("Removed annotation %s from HostedCluster\n", hyperv1.AWSKarpenterDefaultInstanceProfile)
					}
				})).To(Succeed(), "cleanup: failed to remove instance profile annotation from HostedCluster %s/%s", hc.Namespace, hc.Name)

				By("Waiting for EC2NodeClass InstanceProfile to be cleared")
				Eventually(func(g Gomega) {
					ec2NodeClass := &awskarpenterv1.EC2NodeClass{}
					g.Expect(hcClient.Get(ctx, crclient.ObjectKey{Name: "default"}, ec2NodeClass)).To(Succeed())
					g.Expect(ec2NodeClass.Spec.InstanceProfile).To(BeNil(), "InstanceProfile should be cleared after annotation removal")
				}).WithTimeout(2 * time.Minute).WithPolling(5 * time.Second).Should(Succeed())
			})

			By(fmt.Sprintf("Waiting for EC2NodeClass InstanceProfile to be set to %s", workerInstanceProfile))
			Eventually(func(g Gomega) {
				ec2NodeClass := &awskarpenterv1.EC2NodeClass{}
				err := hcClient.Get(ctx, crclient.ObjectKey{Name: "default"}, ec2NodeClass)
				g.Expect(err).NotTo(HaveOccurred())
				g.Expect(ec2NodeClass.Spec.InstanceProfile).NotTo(BeNil(), "InstanceProfile should be set")
				g.Expect(*ec2NodeClass.Spec.InstanceProfile).To(Equal(workerInstanceProfile))
			}).WithTimeout(2 * time.Minute).WithPolling(5 * time.Second).Should(Succeed())

			// Now provision actual nodes to verify EC2 instances get the instance profile
			testNodePool := v2util.BaseNodePool("instance-profile-test", "default")
			testWorkLoads := v2util.TestWorkload("instance-profile-web-app", 1, map[string]string{
				karpenterv1.NodePoolLabelKey: testNodePool.Name,
			})
			testNodeLabels := map[string]string{karpenterv1.NodePoolLabelKey: testNodePool.Name}

			v2util.CreateKarpenterNodePoolAndWorkload(Default, ctx, hcClient, hc.Spec.Platform.Type, testNodePool, testWorkLoads, testNodeLabels, false)

			nodes := e2eutil.WaitForReadyNodesByLabels(t, ctx, hcClient, hc.Spec.Platform.Type, 1, testNodeLabels)

			// Verify EC2 instances have the correct instance profile
			ec2client := v2util.NewEC2Client(ctx, awsCredsFile, awsRegion)
			for _, node := range nodes {
				instance, instanceID := v2util.DescribeEC2Instance(Default, ctx, ec2client, node)
				GinkgoWriter.Printf("Checking instance profile for node %s (instance %s)\n", node.Name, instanceID)
				Expect(instance.IamInstanceProfile).NotTo(BeNil(), "instance should have an IAM instance profile")

				// Extract instance profile name from ARN (format: arn:aws:iam::account-id:instance-profile/profile-name)
				profileArn := *instance.IamInstanceProfile.Arn
				profileParts := strings.Split(profileArn, "/")
				Expect(profileParts).To(HaveLen(2), "instance profile ARN should have 2 parts")
				Expect(profileParts[1]).To(Equal(workerInstanceProfile),
					"instance %s should have instance profile %s", instanceID, workerInstanceProfile)
			}
		})
	})
}

func KarpenterNodeClassVersionTest(getTestCtx internal.TestContextGetter) {
	Context("[Feature:AutoNode] OpenshiftEC2NodeClass version field and MetadataOptions", func() {
		BeforeEach(func() {
			tc := getTestCtx()
			tc.SkipIfNotPlatform(hyperv1.AWSPlatform)
			hc, err := tc.GetHostedCluster()
			Expect(err).NotTo(HaveOccurred())
			if !karpenterutil.IsKarpenterEnabled(hc.Spec.AutoNode) {
				Skip("AutoNode not configured on hosted cluster")
			}
			pullSecretFile := internal.GetEnvVarValue("PULL_SECRET_FILE")
			if pullSecretFile == "" {
				Skip("PULL_SECRET_FILE not set")
			}
		})

		It("should resolve version, propagate MetadataOptions, and provision node with correct kubelet version", func() {
			tc := getTestCtx()
			ctx := tc.Context
			t := GinkgoTB()
			hc, err := tc.GetHostedCluster()
			Expect(err).NotTo(HaveOccurred())
			hcClient, err := tc.GetHostedClusterClient(hc)
			Expect(err).NotTo(HaveOccurred())
			awsCredsFile := internal.GetEnvVarValue("AWS_GUEST_INFRA_CREDENTIALS_FILE")
			awsRegion := hc.Spec.Platform.AWS.Region
			pullSecretFile := internal.GetEnvVarValue("PULL_SECRET_FILE")

			Expect(hc.Status.Version).NotTo(BeNil(), "hostedCluster.Status.Version should not be nil")
			Expect(hc.Status.Version.Desired.Version).NotTo(BeEmpty())

			cpVersion, err := semver.Parse(hc.Status.Version.Desired.Version)
			Expect(err).NotTo(HaveOccurred(), "failed to parse control plane version")
			GinkgoWriter.Printf("Control plane version: %s\n", cpVersion.String())

			// Verify default OpenshiftEC2NodeClass uses control plane release image
			By("Verifying default OpenshiftEC2NodeClass uses control plane release image")
			e2eutil.EventuallyObject(t, ctx, "default OpenshiftEC2NodeClass to have VersionResolved=True",
				func(ctx context.Context) (*hyperkarpenterv1.OpenshiftEC2NodeClass, error) {
					nc := &hyperkarpenterv1.OpenshiftEC2NodeClass{}
					err := hcClient.Get(ctx, crclient.ObjectKey{Name: "default"}, nc)
					return nc, err
				},
				[]e2eutil.Predicate[*hyperkarpenterv1.OpenshiftEC2NodeClass]{
					e2eutil.ConditionPredicate[*hyperkarpenterv1.OpenshiftEC2NodeClass](e2eutil.Condition{
						Type:   hyperkarpenterv1.ConditionTypeVersionResolved,
						Status: metav1.ConditionTrue,
						Reason: "VersionNotSpecified",
					}),
					e2eutil.ConditionPredicate[*hyperkarpenterv1.OpenshiftEC2NodeClass](e2eutil.Condition{
						Type:   hyperkarpenterv1.ConditionTypeSupportedVersionSkew,
						Status: metav1.ConditionTrue,
						Reason: "VersionNotSpecified",
					}),
					func(nc *hyperkarpenterv1.OpenshiftEC2NodeClass) (bool, string, error) {
						if nc.Status.ReleaseImage == "" {
							return false, "status.releaseImage is empty", nil
						}
						if nc.Status.ReleaseImage != hc.Spec.Release.Image {
							return false, fmt.Sprintf("expected status.releaseImage %q to match hostedCluster.Spec.Release.Image %q", nc.Status.ReleaseImage, hc.Spec.Release.Image), nil
						}
						return true, fmt.Sprintf("status.releaseImage matches control plane: %s", nc.Status.ReleaseImage), nil
					},
				},
				e2eutil.WithTimeout(2*time.Minute),
			)

			// Use a previous minor version (n-2) to test a genuinely different version.
			prevMajor, prevMinor, err := supportedversion.PreviousMinorVersion(cpVersion, 2)
			Expect(err).NotTo(HaveOccurred())
			nodeClassVersion := fmt.Sprintf("%d.%d.0", prevMajor, prevMinor)

			// Create a custom OpenshiftEC2NodeClass with the version field set to the n-2 previous minor version.
			nc := &hyperkarpenterv1.OpenshiftEC2NodeClass{
				ObjectMeta: metav1.ObjectMeta{Name: "version-test"},
				Spec: hyperkarpenterv1.OpenshiftEC2NodeClassSpec{
					Version: nodeClassVersion,
					SubnetSelectorTerms: []hyperkarpenterv1.SubnetSelectorTerm{
						{Tags: map[string]string{"karpenter.sh/discovery": hc.Spec.InfraID}},
					},
					SecurityGroupSelectorTerms: []hyperkarpenterv1.SecurityGroupSelectorTerm{
						{Tags: map[string]string{"karpenter.sh/discovery": hc.Spec.InfraID}},
					},
					MetadataOptions: hyperkarpenterv1.MetadataOptions{
						Access:                  hyperkarpenterv1.MetadataAccessHTTPEndpoint,
						HTTPIPProtocol:          hyperkarpenterv1.MetadataHTTPProtocolIPv4,
						HTTPPutResponseHopLimit: 2,
						HTTPTokens:              hyperkarpenterv1.MetadataHTTPTokensStateRequired,
					},
				},
			}
			By(fmt.Sprintf("Creating OpenshiftEC2NodeClass %q with version %s (control plane %s)", nc.Name, nodeClassVersion, cpVersion))
			Expect(hcClient.Create(ctx, nc)).To(Succeed())
			DeferCleanup(func() {
				if err := hcClient.Delete(ctx, nc); err != nil && !apierrors.IsNotFound(err) {
					Expect(err).NotTo(HaveOccurred(), "cleanup: failed to delete OpenshiftEC2NodeClass %s", nc.Name)
				}
			})

			// Wait for version resolution and get the resolved release image
			var resolvedReleaseImage string
			By("Waiting for OpenshiftEC2NodeClass version resolution")
			e2eutil.EventuallyObject(t, ctx, "OpenshiftEC2NodeClass version-test to resolve version",
				func(ctx context.Context) (*hyperkarpenterv1.OpenshiftEC2NodeClass, error) {
					result := &hyperkarpenterv1.OpenshiftEC2NodeClass{}
					err := hcClient.Get(ctx, crclient.ObjectKey{Name: nc.Name}, result)
					return result, err
				},
				[]e2eutil.Predicate[*hyperkarpenterv1.OpenshiftEC2NodeClass]{
					e2eutil.ConditionPredicate[*hyperkarpenterv1.OpenshiftEC2NodeClass](e2eutil.Condition{
						Type:   hyperkarpenterv1.ConditionTypeVersionResolved,
						Status: metav1.ConditionTrue,
						Reason: "VersionResolved",
					}),
					e2eutil.ConditionPredicate[*hyperkarpenterv1.OpenshiftEC2NodeClass](e2eutil.Condition{
						Type:   hyperkarpenterv1.ConditionTypeSupportedVersionSkew,
						Status: metav1.ConditionTrue,
						Reason: "AsExpected",
					}),
					func(nc *hyperkarpenterv1.OpenshiftEC2NodeClass) (bool, string, error) {
						if nc.Status.ReleaseImage == "" {
							return false, "status.releaseImage is empty", nil
						}
						resolvedReleaseImage = nc.Status.ReleaseImage
						return true, fmt.Sprintf("status.releaseImage resolved to: %s", nc.Status.ReleaseImage), nil
					},
				},
				e2eutil.WithTimeout(5*time.Minute),
			)

			// Verify MetadataOptions propagated to downstream EC2NodeClass
			By("Verifying MetadataOptions propagated to EC2NodeClass")
			e2eutil.EventuallyObject(t, ctx, "EC2NodeClass to have MetadataOptions propagated",
				func(ctx context.Context) (*awskarpenterv1.EC2NodeClass, error) {
					ec2NodeClass := &awskarpenterv1.EC2NodeClass{}
					err := hcClient.Get(ctx, crclient.ObjectKey{Name: nc.Name}, ec2NodeClass)
					return ec2NodeClass, err
				},
				[]e2eutil.Predicate[*awskarpenterv1.EC2NodeClass]{
					func(ec2nc *awskarpenterv1.EC2NodeClass) (bool, string, error) {
						if ec2nc.Spec.MetadataOptions == nil {
							return false, "MetadataOptions is nil", nil
						}
						if ec2nc.Spec.MetadataOptions.HTTPEndpoint == nil || *ec2nc.Spec.MetadataOptions.HTTPEndpoint != "enabled" {
							return false, fmt.Sprintf("expected HTTPEndpoint=enabled, got %v", ec2nc.Spec.MetadataOptions.HTTPEndpoint), nil
						}
						if ec2nc.Spec.MetadataOptions.HTTPProtocolIPv6 == nil || *ec2nc.Spec.MetadataOptions.HTTPProtocolIPv6 != "disabled" {
							return false, fmt.Sprintf("expected HTTPProtocolIPv6=disabled, got %v", ec2nc.Spec.MetadataOptions.HTTPProtocolIPv6), nil
						}
						if ec2nc.Spec.MetadataOptions.HTTPPutResponseHopLimit == nil || *ec2nc.Spec.MetadataOptions.HTTPPutResponseHopLimit != 2 {
							return false, fmt.Sprintf("expected HTTPPutResponseHopLimit=2, got %v", ec2nc.Spec.MetadataOptions.HTTPPutResponseHopLimit), nil
						}
						if ec2nc.Spec.MetadataOptions.HTTPTokens == nil || *ec2nc.Spec.MetadataOptions.HTTPTokens != "required" {
							return false, fmt.Sprintf("expected HTTPTokens=required, got %v", ec2nc.Spec.MetadataOptions.HTTPTokens), nil
						}
						return true, "MetadataOptions propagated correctly", nil
					},
				},
				e2eutil.WithTimeout(2*time.Minute),
			)

			// Look up expected kubelet version from the resolved release image
			pullSecret, err := os.ReadFile(pullSecretFile)
			Expect(err).NotTo(HaveOccurred())
			releaseProvider := &releaseinfo.RegistryClientProvider{}
			resolvedRelease, err := releaseProvider.Lookup(ctx, resolvedReleaseImage, pullSecret)
			Expect(err).NotTo(HaveOccurred(), "failed to look up resolved release image %s", resolvedReleaseImage)
			componentVersions, err := resolvedRelease.ComponentVersions()
			Expect(err).NotTo(HaveOccurred())
			expectedKubeletVersion := componentVersions["kubernetes"]
			Expect(expectedKubeletVersion).NotTo(BeEmpty(), "resolved release should have a kubernetes version")
			GinkgoWriter.Printf("Expected kubelet version for %s: v%s\n", nodeClassVersion, expectedKubeletVersion)

			// Create a Karpenter NodePool that references the custom EC2NodeClass
			testNodePool := v2util.BaseNodePool("version-test", nc.Name)
			testWorkLoads := v2util.TestWorkload("version-test-app", 1, map[string]string{
				karpenterv1.NodePoolLabelKey: testNodePool.Name,
			})
			// Use only the nodepool label to select nodes exclusively tied to our version-test nodeclass.
			testNodeLabels := map[string]string{
				karpenterv1.NodePoolLabelKey: testNodePool.GetName(),
			}

			v2util.CreateKarpenterNodePoolAndWorkload(Default, ctx, hcClient, hc.Spec.Platform.Type, testNodePool, testWorkLoads, testNodeLabels, false)

			// Log diagnostic info about the version-test NodeClass infrastructure.
			hcpNamespace := manifests.HostedControlPlaneNamespace(hc.Namespace, hc.Name)
			secretList := &corev1.SecretList{}
			if err := tc.MgmtClient.List(ctx, secretList,
				crclient.InNamespace(hcpNamespace),
				crclient.MatchingLabels{"hypershift.openshift.io/managed-by-karpenter": "true"},
			); err != nil {
				GinkgoWriter.Printf("WARNING: failed to list karpenter secrets in %s: %v\n", hcpNamespace, err)
			} else {
				foundUserData := false
				for _, s := range secretList.Items {
					npAnnotation := s.Annotations["hypershift.openshift.io/nodePool"]
					if strings.Contains(npAnnotation, "version-test") {
						GinkgoWriter.Printf("Found Karpenter secret %q for NodePool %q (labels: %v)\n", s.Name, npAnnotation, s.Labels)
						foundUserData = true
					}
				}
				if !foundUserData {
					GinkgoWriter.Printf("WARNING: no user-data secret found for version-test NodeClass. Token creation may be failing - check karpenter-operator logs.\n")
				}
			}

			// Wait for node to be provisioned and verify it has the correct kubelet version
			nodes := e2eutil.WaitForNReadyNodesWithOptions(t, ctx, hcClient, 1, hyperv1.AWSPlatform, "",
				e2eutil.WithClientOptions(
					crclient.MatchingLabelsSelector{Selector: labels.SelectorFromSet(labels.Set(testNodeLabels))},
				),
				e2eutil.WithPredicates(
					func(node *corev1.Node) (bool, string, error) {
						kubeletVersion := node.Status.NodeInfo.KubeletVersion
						if !strings.Contains(kubeletVersion, expectedKubeletVersion) {
							return false, fmt.Sprintf("node %s kubelet version %q does not contain expected %q", node.Name, kubeletVersion, expectedKubeletVersion), nil
						}
						return true, fmt.Sprintf("node %s has expected kubelet version %s", node.Name, kubeletVersion), nil
					},
				),
			)
			GinkgoWriter.Printf("Node provisioned with correct kubelet version (v%s) for NodeClass version %s\n", expectedKubeletVersion, nodeClassVersion)

			// Verify MetadataOptions on EC2 instance
			By("Verifying MetadataOptions on EC2 instance via DescribeInstances")
			ec2client := v2util.NewEC2Client(ctx, awsCredsFile, awsRegion)
			for _, node := range nodes {
				instance, instanceID := v2util.DescribeEC2Instance(Default, ctx, ec2client, node)
				GinkgoWriter.Printf("Checking MetadataOptions for node %s (instance %s)\n", node.Name, instanceID)
				Expect(instance.MetadataOptions).NotTo(BeNil(), "instance should have MetadataOptions")
				Expect(string(instance.MetadataOptions.HttpEndpoint)).To(Equal("enabled"), "instance %s HttpEndpoint mismatch", instanceID)
				Expect(string(instance.MetadataOptions.HttpProtocolIpv6)).To(Equal("disabled"), "instance %s HttpProtocolIpv6 mismatch", instanceID)
				Expect(*instance.MetadataOptions.HttpPutResponseHopLimit).To(Equal(int32(2)), "instance %s HttpPutResponseHopLimit mismatch", instanceID)
				Expect(string(instance.MetadataOptions.HttpTokens)).To(Equal("required"), "instance %s HttpTokens mismatch", instanceID)
				GinkgoWriter.Printf("Instance %s has correct MetadataOptions: HttpTokens=%s, HttpEndpoint=%s, HttpPutResponseHopLimit=%d\n",
					instanceID, instance.MetadataOptions.HttpTokens, instance.MetadataOptions.HttpEndpoint, *instance.MetadataOptions.HttpPutResponseHopLimit)
			}

			// Trigger cleanup and wait for nodes to fully terminate so stale
			// NodeClaims don't leak vCPUs into subsequent sequential tests.
			Expect(hcClient.Delete(ctx, testWorkLoads)).To(Succeed())
			Expect(hcClient.Delete(ctx, testNodePool)).To(Succeed())
			_ = e2eutil.WaitForReadyNodesByLabels(t, ctx, hcClient, hc.Spec.Platform.Type, 0, testNodeLabels)

			// Verify n-4 version skew produces SupportedVersionSkew=False
			skewMajor, skewMinor, err := supportedversion.PreviousMinorVersion(cpVersion, 4)
			if err != nil {
				Expect(err).NotTo(HaveOccurred(), "Cannot compute n-4 skew version for n=%s", cpVersion)
			}
			if skewMajor == 4 && skewMinor <= 14 {
				Skip(fmt.Sprintf("Skipping version-skew check: computed skew version %d.%d.0 would be at or below MinSupportedVersion (4.14.0)", skewMajor, skewMinor))
			}
			skewPatch := 1 // There are cases where x.y.0 doesn't exist, so arbitrarily stick with x.y.1 for test consistency
			skewVersion := fmt.Sprintf("%d.%d.%d", skewMajor, skewMinor, skewPatch)
			skewNC := &hyperkarpenterv1.OpenshiftEC2NodeClass{
				ObjectMeta: metav1.ObjectMeta{Name: "version-skew-test"},
				Spec: hyperkarpenterv1.OpenshiftEC2NodeClassSpec{
					Version: skewVersion,
					SubnetSelectorTerms: []hyperkarpenterv1.SubnetSelectorTerm{
						{Tags: map[string]string{"test": "version-skew"}},
					},
					SecurityGroupSelectorTerms: []hyperkarpenterv1.SecurityGroupSelectorTerm{
						{Tags: map[string]string{"test": "version-skew"}},
					},
				},
			}
			By(fmt.Sprintf("Creating OpenshiftEC2NodeClass %q with out-of-skew version %s", skewNC.Name, skewVersion))
			Expect(hcClient.Create(ctx, skewNC)).To(Succeed())
			DeferCleanup(func() {
				if err := hcClient.Delete(ctx, skewNC); err != nil && !apierrors.IsNotFound(err) {
					Expect(err).NotTo(HaveOccurred(), "cleanup: failed to delete OpenshiftEC2NodeClass %s", skewNC.Name)
				}
				GinkgoWriter.Printf("Cleaned up OpenshiftEC2NodeClass %q\n", skewNC.Name)
			})

			By("Waiting for VersionResolved=True and SupportedVersionSkew=False")
			e2eutil.EventuallyObject(t, ctx, "OpenshiftEC2NodeClass version-skew-test to have SupportedVersionSkew=False",
				func(ctx context.Context) (*hyperkarpenterv1.OpenshiftEC2NodeClass, error) {
					result := &hyperkarpenterv1.OpenshiftEC2NodeClass{}
					err := hcClient.Get(ctx, crclient.ObjectKey{Name: skewNC.Name}, result)
					return result, err
				},
				[]e2eutil.Predicate[*hyperkarpenterv1.OpenshiftEC2NodeClass]{
					e2eutil.ConditionPredicate[*hyperkarpenterv1.OpenshiftEC2NodeClass](e2eutil.Condition{
						Type:   hyperkarpenterv1.ConditionTypeVersionResolved,
						Status: metav1.ConditionTrue,
						Reason: "VersionResolved",
					}),
					e2eutil.ConditionPredicate[*hyperkarpenterv1.OpenshiftEC2NodeClass](e2eutil.Condition{
						Type:   hyperkarpenterv1.ConditionTypeSupportedVersionSkew,
						Status: metav1.ConditionFalse,
						Reason: "UnsupportedSkew",
					}),
					func(nc *hyperkarpenterv1.OpenshiftEC2NodeClass) (bool, string, error) {
						for _, c := range nc.Status.Conditions {
							if c.Type == hyperkarpenterv1.ConditionTypeSupportedVersionSkew && c.Status == metav1.ConditionFalse {
								if strings.Contains(c.Message, "minor version") {
									return true, fmt.Sprintf("SupportedVersionSkew condition message describes skew issue: %s", c.Message), nil
								}
								return false, fmt.Sprintf("expected SupportedVersionSkew message to mention version skew, got %q", c.Message), nil
							}
						}
						return false, "SupportedVersionSkew=False condition not found", nil
					},
				},
				e2eutil.WithTimeout(2*time.Minute),
			)
			GinkgoWriter.Printf("OpenshiftEC2NodeClass %q has SupportedVersionSkew=False for version %s (exceeds n-3 skew from CP %s)\n", skewNC.Name, skewVersion, cpVersion)
		})
	})
}

func KarpenterCapacityReservationTest(getTestCtx internal.TestContextGetter) {
	Context("[Feature:AutoNode] Capacity reservation selector propagation", func() {
		BeforeEach(func() {
			tc := getTestCtx()
			tc.SkipIfNotPlatform(hyperv1.AWSPlatform)
			hc, err := tc.GetHostedCluster()
			Expect(err).NotTo(HaveOccurred())
			if !karpenterutil.IsKarpenterEnabled(hc.Spec.AutoNode) {
				Skip("AutoNode not configured on hosted cluster")
			}
		})

		It("should provision a node into a capacity reservation", func() {
			tc := getTestCtx()
			ctx := tc.Context
			t := GinkgoTB()
			hc, err := tc.GetHostedCluster()
			Expect(err).NotTo(HaveOccurred())
			hcClient, err := tc.GetHostedClusterClient(hc)
			Expect(err).NotTo(HaveOccurred())
			awsCredsFile := internal.GetEnvVarValue("AWS_GUEST_INFRA_CREDENTIALS_FILE")
			awsRegion := hc.Spec.Platform.AWS.Region

			// Determine an availability zone to use: pick the AZ from the first subnet in the cluster.
			defaultNodeClass := &hyperkarpenterv1.OpenshiftEC2NodeClass{}
			Expect(hcClient.Get(ctx, crclient.ObjectKey{Name: "default"}, defaultNodeClass)).To(Succeed())
			Expect(defaultNodeClass.Status.Subnets).NotTo(BeEmpty(), "default OpenshiftEC2NodeClass should have resolved subnets")
			targetAZ := defaultNodeClass.Status.Subnets[0].Zone
			By(fmt.Sprintf("Creating EC2 capacity reservation in availability zone %s", targetAZ))

			// Create a real EC2 capacity reservation with 1 instance of t3.xlarge in targeted mode.
			// We use t3.xlarge to match the instance type used by the other karpenter tests — OpenShift
			// platform daemonsets consume enough overhead that smaller types (t3.small, t3.medium) don't
			// have enough free memory to satisfy karpenter's scheduling check.
			// We need a real reservation because karpenter 1.8 runs with ReservedCapacity=true by default,
			// so selector terms that match nothing would cause CapacityReservationsReady=False on the
			// EC2NodeClass and block provisioning.
			crID, cleanupCR, err := e2eutil.CreateCapacityReservation(
				ctx, awsCredsFile, awsRegion, "t3.xlarge", targetAZ, 1,
				hc.Spec.InfraID, hc.Name, e2eutil.E2ETagsFromEnvironment(),
			)
			Expect(err).NotTo(HaveOccurred(), "failed to create capacity reservation")
			DeferCleanup(func() {
				Expect(cleanupCR()).To(Succeed(), "cleanup: failed to cancel capacity reservation %s", crID)
			})
			// Create a new OpenshiftEC2NodeClass (not "default") pointing to the capacity reservation by ID.
			// Using a separate object avoids contaminating the shared "default" class used by other sub-tests.
			crNodeClass := &hyperkarpenterv1.OpenshiftEC2NodeClass{
				ObjectMeta: metav1.ObjectMeta{Name: "capacity-reservation-test"},
				Spec: hyperkarpenterv1.OpenshiftEC2NodeClassSpec{
					CapacityReservationSelectorTerms: []hyperkarpenterv1.CapacityReservationSelectorTerm{
						{ID: crID},
					},
				},
			}
			By(fmt.Sprintf("Creating OpenshiftEC2NodeClass %q with capacity reservation %s", crNodeClass.Name, crID))
			Expect(hcClient.Create(ctx, crNodeClass)).To(Succeed())
			DeferCleanup(func() {
				if err := hcClient.Delete(ctx, crNodeClass); err != nil && !apierrors.IsNotFound(err) {
					Expect(err).NotTo(HaveOccurred(), "cleanup: failed to delete OpenshiftEC2NodeClass %s", crNodeClass.Name)
				}
			})

			// Verify the downstream EC2NodeClass has the CapacityReservationSelectorTerms propagated.
			e2eutil.EventuallyObject(t, ctx, "EC2NodeClass capacity-reservation-test to have CapacityReservationSelectorTerms set",
				func(ctx context.Context) (*awskarpenterv1.EC2NodeClass, error) {
					ec2nc := &awskarpenterv1.EC2NodeClass{}
					return ec2nc, hcClient.Get(ctx, crclient.ObjectKey{Name: "capacity-reservation-test"}, ec2nc)
				},
				[]e2eutil.Predicate[*awskarpenterv1.EC2NodeClass]{
					func(ec2nc *awskarpenterv1.EC2NodeClass) (bool, string, error) {
						if len(ec2nc.Spec.CapacityReservationSelectorTerms) == 1 &&
							ec2nc.Spec.CapacityReservationSelectorTerms[0].ID == crID {
							return true, "", nil
						}
						return false, fmt.Sprintf("expected CapacityReservationSelectorTerms[0].ID=%s, got %+v",
							crID, ec2nc.Spec.CapacityReservationSelectorTerms), nil
					},
				},
				e2eutil.WithTimeout(2*time.Minute), e2eutil.WithInterval(5*time.Second),
			)

			// Verify karpenter resolves the capacity reservation and reflects it in the OpenshiftEC2NodeClass status.
			e2eutil.EventuallyObject(t, ctx, fmt.Sprintf("OpenshiftEC2NodeClass capacity-reservation-test to have capacity reservation %s in status", crID),
				func(ctx context.Context) (*hyperkarpenterv1.OpenshiftEC2NodeClass, error) {
					updated := &hyperkarpenterv1.OpenshiftEC2NodeClass{}
					return updated, hcClient.Get(ctx, crclient.ObjectKey{Name: "capacity-reservation-test"}, updated)
				},
				[]e2eutil.Predicate[*hyperkarpenterv1.OpenshiftEC2NodeClass]{
					func(updated *hyperkarpenterv1.OpenshiftEC2NodeClass) (bool, string, error) {
						if len(updated.Status.CapacityReservations) > 0 && updated.Status.CapacityReservations[0].ID == crID {
							return true, "", nil
						}
						return false, fmt.Sprintf("expected capacity reservation %s in status, got %+v", crID, updated.Status.CapacityReservations), nil
					},
				},
				e2eutil.WithTimeout(5*time.Minute), e2eutil.WithInterval(10*time.Second),
			)

			// Create a dedicated NodePool that targets the capacity-reservation-test NodeClass and requires
			// capacity-type=reserved so karpenter launches the instance into the reservation (not alongside it).
			crNodePool := v2util.BaseNodePool("capacity-reservation-test", "capacity-reservation-test")
			crNodePool.Spec.Template.Spec.Requirements = []karpenterv1.NodeSelectorRequirementWithMinValues{
				{Key: "node.kubernetes.io/instance-type", Operator: corev1.NodeSelectorOpIn, Values: []string{"t3.xlarge"}},
				{Key: karpenterv1.CapacityTypeLabelKey, Operator: corev1.NodeSelectorOpIn, Values: []string{karpenterv1.CapacityTypeReserved}},
			}
			crNodeLabels := map[string]string{karpenterv1.NodePoolLabelKey: crNodePool.Name}
			crWorkload := v2util.TestWorkload("capacity-reservation-web-app", 1, crNodeLabels)

			v2util.CreateKarpenterNodePoolAndWorkload(Default, ctx, hcClient, hc.Spec.Platform.Type, crNodePool, crWorkload, crNodeLabels, false)

			// Wait for the node to be ready.
			nodes := e2eutil.WaitForReadyNodesByLabels(t, ctx, hcClient, hc.Spec.Platform.Type, 1, crNodeLabels)
			Expect(nodes).To(HaveLen(1))

			// Verify the EC2 instance was launched into the capacity reservation.
			ec2client := v2util.NewEC2Client(ctx, awsCredsFile, awsRegion)

			instance, instanceID := v2util.DescribeEC2Instance(Default, ctx, ec2client, nodes[0])
			By(fmt.Sprintf("Verifying EC2 instance %s was launched into capacity reservation %s", instanceID, crID))
			Expect(instance.CapacityReservationId).NotTo(BeNil(), "instance %s should have a CapacityReservationId", instanceID)
			Expect(aws.ToString(instance.CapacityReservationId)).To(Equal(crID),
				"instance %s should have been launched into capacity reservation %s", instanceID, crID)

			// Delete workload and NodePool, then wait for nodes to fully terminate
			// so stale NodeClaims don't leak vCPUs into subsequent sequential tests.
			Expect(hcClient.Delete(ctx, crWorkload)).To(Succeed())
			Expect(hcClient.Delete(ctx, crNodePool)).To(Succeed())
			_ = e2eutil.WaitForReadyNodesByLabels(t, ctx, hcClient, hc.Spec.Platform.Type, 0, crNodeLabels)
		})
	})
}

func KarpenterArbitrarySubnetTest(getTestCtx internal.TestContextGetter) {
	Context("[Feature:AutoNode] Arbitrary subnet propagation", func() {
		BeforeEach(func() {
			tc := getTestCtx()
			tc.SkipIfNotPlatform(hyperv1.AWSPlatform)
			hc, err := tc.GetHostedCluster()
			Expect(err).NotTo(HaveOccurred())
			if !karpenterutil.IsKarpenterEnabled(hc.Spec.AutoNode) {
				Skip("AutoNode not configured on hosted cluster")
			}
		})

		It("should propagate a custom subnet through the VPC endpoint and provision a node in it", func() {
			tc := getTestCtx()
			ctx := tc.Context
			t := GinkgoTB()
			hc, err := tc.GetHostedCluster()
			Expect(err).NotTo(HaveOccurred())
			hcClient, err := tc.GetHostedClusterClient(hc)
			Expect(err).NotTo(HaveOccurred())
			awsCredsFile := internal.GetEnvVarValue("AWS_GUEST_INFRA_CREDENTIALS_FILE")
			awsRegion := hc.Spec.Platform.AWS.Region

			// Get VPC ID and find an AZ that is:
			// (a) supported by the VPC endpoint service (to avoid InvalidParameter), and
			// (b) not already occupied by a VPC subnet (to avoid DuplicateSubnetsInSameZone).
			// This exercises the real scenario: a customer brings a subnet in a new AZ,
			// it propagates to the VPC endpoint, and nodes in that AZ can reach the cluster.
			ec2client := v2util.NewEC2Client(ctx, awsCredsFile, awsRegion)
			vpcID := hc.Spec.Platform.AWS.CloudProviderConfig.VPC
			subnetsOut, err := ec2client.DescribeSubnets(ctx, &ec2.DescribeSubnetsInput{
				Filters: []ec2types.Filter{{Name: aws.String("vpc-id"), Values: []string{vpcID}}},
			})
			Expect(err).NotTo(HaveOccurred())
			Expect(subnetsOut.Subnets).NotTo(BeEmpty())

			// Collect AZs already occupied by VPC subnets.
			usedAZs := map[string]bool{}
			for _, s := range subnetsOut.Subnets {
				usedAZs[aws.ToString(s.AvailabilityZone)] = true
			}

			// Get the AZs supported by the VPC endpoint service.
			hcpNamespace := manifests.HostedControlPlaneNamespace(hc.Namespace, hc.Name)
			esList := &hyperv1.AWSEndpointServiceList{}
			Expect(tc.MgmtClient.List(ctx, esList, crclient.InNamespace(hcpNamespace))).To(Succeed())
			Expect(esList.Items).NotTo(BeEmpty(), "expected at least one AWSEndpointService")

			var endpointServiceName string
			for _, es := range esList.Items {
				if es.Status.EndpointServiceName != "" {
					endpointServiceName = es.Status.EndpointServiceName
					break
				}
			}
			Expect(endpointServiceName).NotTo(BeEmpty(), "no AWSEndpointService has an endpoint service name yet")

			svcOut, err := ec2client.DescribeVpcEndpointServices(ctx, &ec2.DescribeVpcEndpointServicesInput{
				ServiceNames: []string{endpointServiceName},
			})
			Expect(err).NotTo(HaveOccurred())
			Expect(svcOut.ServiceDetails).NotTo(BeEmpty())
			supportedAZs := svcOut.ServiceDetails[0].AvailabilityZones
			GinkgoWriter.Printf("VPC endpoint service %s supports AZs: %v\n", endpointServiceName, supportedAZs)

			// Pick an AZ supported by the endpoint service but not already in the VPC.
			var az string
			for _, supportedAZ := range supportedAZs {
				if !usedAZs[supportedAZ] {
					az = supportedAZ
					break
				}
			}
			Expect(az).NotTo(BeEmpty(),
				"no AZ found that is supported by VPC endpoint service %s and not already occupied in VPC %s",
				endpointServiceName, vpcID)
			GinkgoWriter.Printf("Selected AZ %s for test subnet (supported by endpoint service, not in VPC)\n", az)

			// Create a small test subnet in the VPC.
			subnetID, cleanupSubnet := e2eutil.CreateTestSubnet(ctx, t, ec2client, vpcID, az, hc.Spec.InfraID, hc.Name, e2eutil.E2ETagsFromEnvironment())
			DeferCleanup(func() {
				cleanupSubnet()
			})
			GinkgoWriter.Printf("Created test subnet %s in AZ %s\n", subnetID, az)

			// Create an OpenshiftEC2NodeClass that selects the subnet by ID.
			customNodeClass := &hyperkarpenterv1.OpenshiftEC2NodeClass{
				ObjectMeta: metav1.ObjectMeta{Name: "arbitrary-subnet-test"},
				Spec: hyperkarpenterv1.OpenshiftEC2NodeClassSpec{
					SubnetSelectorTerms: []hyperkarpenterv1.SubnetSelectorTerm{{ID: subnetID}},
					SecurityGroupSelectorTerms: []hyperkarpenterv1.SecurityGroupSelectorTerm{
						{Tags: map[string]string{"karpenter.sh/discovery": hc.Spec.InfraID}},
					},
				},
			}
			By(fmt.Sprintf("Creating OpenshiftEC2NodeClass %q selecting subnet %s", customNodeClass.Name, subnetID))
			Expect(hcClient.Create(ctx, customNodeClass)).To(Succeed())
			DeferCleanup(func() {
				// Delete the NodeClass first so controllers stop referencing the subnet.
				if err := hcClient.Delete(ctx, customNodeClass); err != nil && !apierrors.IsNotFound(err) {
					Expect(err).NotTo(HaveOccurred(), "cleanup: failed to delete OpenshiftEC2NodeClass %s", customNodeClass.Name)
				}

				// Wait for the subnet to be removed from the karpenter-subnets ConfigMap.
				// The karpenter-operator removes it during NodeClass deletion reconciliation.
				Expect(wait.PollUntilContextTimeout(ctx, 5*time.Second, 2*time.Minute, true, func(ctx context.Context) (bool, error) {
					cm := &corev1.ConfigMap{}
					if err := tc.MgmtClient.Get(ctx, crclient.ObjectKey{Namespace: hcpNamespace, Name: karpenterutil.KarpenterSubnetsConfigMapName}, cm); err != nil {
						return false, nil
					}
					var ids []string
					if err := json.Unmarshal([]byte(cm.Data["subnetIDs"]), &ids); err != nil {
						return false, nil
					}
					for _, id := range ids {
						if id == subnetID {
							return false, nil
						}
					}
					return true, nil
				})).To(Succeed(), "cleanup: subnet %s was not removed from karpenter-subnets ConfigMap", subnetID)

				// Wait for the subnet to be removed from all AWSEndpointService.Spec.SubnetIDs.
				// The hypershift-operator watches the ConfigMap and reconciles Spec.SubnetIDs.
				Expect(wait.PollUntilContextTimeout(ctx, 5*time.Second, 2*time.Minute, true, func(ctx context.Context) (bool, error) {
					list := &hyperv1.AWSEndpointServiceList{}
					if err := tc.MgmtClient.List(ctx, list, crclient.InNamespace(hcpNamespace)); err != nil {
						return false, nil
					}
					for _, es := range list.Items {
						for _, id := range es.Spec.SubnetIDs {
							if id == subnetID {
								return false, nil
							}
						}
					}
					return true, nil
				})).To(Succeed(), "cleanup: subnet %s was not removed from AWSEndpointService specs", subnetID)

				// Wait for AWSEndpointAvailable=True to confirm the CPO has finished
				// reconciling the VPC endpoint (subnet actually removed from AWS).
				Expect(wait.PollUntilContextTimeout(ctx, 5*time.Second, 3*time.Minute, true, func(ctx context.Context) (bool, error) {
					list := &hyperv1.AWSEndpointServiceList{}
					if err := tc.MgmtClient.List(ctx, list, crclient.InNamespace(hcpNamespace)); err != nil {
						return false, nil
					}
					for _, es := range list.Items {
						for _, cond := range es.Status.Conditions {
							if cond.Type == string(hyperv1.AWSEndpointAvailable) && cond.Status != metav1.ConditionTrue {
								return false, nil
							}
						}
					}
					return true, nil
				})).To(Succeed(), "cleanup: AWSEndpointServices did not return to AWSEndpointAvailable=True after subnet removal")
			})

			// Wait for OpenshiftEC2NodeClass.Status.Subnets to contain the subnet ID.
			By(fmt.Sprintf("Waiting for OpenshiftEC2NodeClass status to reflect subnet %s", subnetID))
			Eventually(func(g Gomega) {
				nc := &hyperkarpenterv1.OpenshiftEC2NodeClass{}
				g.Expect(hcClient.Get(ctx, crclient.ObjectKeyFromObject(customNodeClass), nc)).To(Succeed())
				subnetIDs := make([]string, 0, len(nc.Status.Subnets))
				for _, s := range nc.Status.Subnets {
					subnetIDs = append(subnetIDs, s.ID)
				}
				g.Expect(subnetIDs).To(ContainElement(subnetID), "status.subnets should contain the test subnet")
			}).WithTimeout(2 * time.Minute).WithPolling(5 * time.Second).Should(Succeed())

			// Wait for the karpenter-subnets ConfigMap in the HCP namespace to contain the subnet ID.
			// hcpNamespace was already set above during AZ selection.
			By(fmt.Sprintf("Waiting for karpenter-subnets ConfigMap in %s to contain subnet %s", hcpNamespace, subnetID))
			Eventually(func(g Gomega) {
				cm := &corev1.ConfigMap{}
				g.Expect(tc.MgmtClient.Get(ctx, crclient.ObjectKey{Namespace: hcpNamespace, Name: karpenterutil.KarpenterSubnetsConfigMapName}, cm)).To(Succeed())
				g.Expect(cm.Data).To(HaveKey("subnetIDs"))
				var cmSubnetIDs []string
				g.Expect(json.Unmarshal([]byte(cm.Data["subnetIDs"]), &cmSubnetIDs)).To(Succeed())
				g.Expect(cmSubnetIDs).To(ContainElement(subnetID), "karpenter-subnets ConfigMap should contain the test subnet")
			}).WithTimeout(2 * time.Minute).WithPolling(5 * time.Second).Should(Succeed())

			// Wait for any AWSEndpointService in the HCP namespace to include the subnet ID.
			// Which AWSEndpointService resources exist depends on the APIServer publishing
			// strategy: with LoadBalancer publishing, "kube-apiserver-private" is created;
			// with Route publishing (used when ExternalDNS is configured), only
			// "private-router" exists. We check all of them to be independent of the
			// publishing strategy.
			By(fmt.Sprintf("Waiting for any AWSEndpointService in %s to include subnet %s", hcpNamespace, subnetID))
			Eventually(func(g Gomega) {
				list := &hyperv1.AWSEndpointServiceList{}
				g.Expect(tc.MgmtClient.List(ctx, list, crclient.InNamespace(hcpNamespace))).To(Succeed())
				g.Expect(list.Items).NotTo(BeEmpty())
				found := false
				for _, es := range list.Items {
					for _, id := range es.Spec.SubnetIDs {
						if id == subnetID {
							found = true
							break
						}
					}
				}
				g.Expect(found).To(BeTrue(), "no AWSEndpointService in %s contains subnet %s", hcpNamespace, subnetID)
			}).WithTimeout(3 * time.Minute).WithPolling(10 * time.Second).Should(Succeed())

			// Wait for all AWSEndpointServices to have AWSEndpointAvailable=True.
			// This confirms the CPO successfully created/modified the VPC endpoint
			// with the new subnet — the feature actually works end-to-end.
			By(fmt.Sprintf("Waiting for AWSEndpointAvailable=True on all AWSEndpointServices in %s", hcpNamespace))
			Eventually(func(g Gomega) {
				list := &hyperv1.AWSEndpointServiceList{}
				g.Expect(tc.MgmtClient.List(ctx, list, crclient.InNamespace(hcpNamespace))).To(Succeed())
				for _, es := range list.Items {
					available := false
					for _, cond := range es.Status.Conditions {
						if cond.Type == string(hyperv1.AWSEndpointAvailable) {
							g.Expect(cond.Status).To(Equal(metav1.ConditionTrue),
								"AWSEndpointService %q has AWSEndpointAvailable=%s: %s", es.Name, cond.Status, cond.Message)
							available = true
							break
						}
					}
					g.Expect(available).To(BeTrue(), "AWSEndpointService %q has no AWSEndpointAvailable condition", es.Name)
				}
			}).WithTimeout(5 * time.Minute).WithPolling(10 * time.Second).Should(Succeed())

			// Launch a node in the custom subnet to verify it's functional.
			testNodePool := v2util.BaseNodePool("arbitrary-subnet-test", customNodeClass.Name)
			testWorkLoads := v2util.TestWorkload("arbitrary-subnet-web-app", 1, map[string]string{
				karpenterv1.NodePoolLabelKey: testNodePool.Name,
			})
			testNodeLabels := map[string]string{karpenterv1.NodePoolLabelKey: testNodePool.Name}

			v2util.CreateKarpenterNodePoolAndWorkload(Default, ctx, hcClient, hc.Spec.Platform.Type, testNodePool, testWorkLoads, testNodeLabels, false)

			nodes := e2eutil.WaitForReadyNodesByLabels(t, ctx, hcClient, hc.Spec.Platform.Type, 1, testNodeLabels)
			By(fmt.Sprintf("Verifying node launched in arbitrary subnet %s", subnetID))

			// Verify the launched node's EC2 instance is in the expected subnet.
			for _, node := range nodes {
				instance, instanceID := v2util.DescribeEC2Instance(Default, ctx, ec2client, node)
				Expect(aws.ToString(instance.SubnetId)).To(Equal(subnetID),
					"instance %s should be in subnet %s", instanceID, subnetID)
				GinkgoWriter.Printf("Instance %s confirmed in subnet %s\n", instanceID, subnetID)
			}

			// Trigger cleanup; the deferred cleanup handles final subnet removal.
			// No need to wait for node deprovisioning — subsequent tests use isolated NodePools.
			Expect(hcClient.Delete(ctx, testWorkLoads)).To(Succeed())
			Expect(hcClient.Delete(ctx, testNodePool)).To(Succeed())
		})
	})
}

func KarpenterKubeletPropagationTest(getTestCtx internal.TestContextGetter) {
	Context("[Feature:AutoNode] OpenshiftEC2NodeClass Kubelet propagation", func() {
		BeforeEach(func() {
			tc := getTestCtx()
			tc.SkipIfNotPlatform(hyperv1.AWSPlatform)
			hc, err := tc.GetHostedCluster()
			Expect(err).NotTo(HaveOccurred())
			if !karpenterutil.IsKarpenterEnabled(hc.Spec.AutoNode) {
				Skip("AutoNode not configured on hosted cluster")
			}
		})

		It("should propagate kubelet config to provisioned nodes", func() {
			tc := getTestCtx()
			ctx := tc.Context
			t := GinkgoTB()
			hc, err := tc.GetHostedCluster()
			Expect(err).NotTo(HaveOccurred())
			hcClient, err := tc.GetHostedClusterClient(hc)
			Expect(err).NotTo(HaveOccurred())
			hcpNamespace := manifests.HostedControlPlaneNamespace(hc.Namespace, hc.Name)

			// Create a custom OpenshiftEC2NodeClass that the controller does not manage, so that
			// reconcileOpenshiftEC2NodeClassDefault cannot overwrite spec.kubelet on every reconcile.
			// We picked weird non-round numbers specifically so we know it wasn't getting defaulted.
			//
			// Build KubeletConfiguration via JSON unmarshal so that overflow (non-typed) fields
			// like podPidsLimit and containerLogMaxSize are captured. The overflow mechanism
			// requires JSON deserialization because the overflow map is unexported.
			kubeletJSON := `{
				"maxPods": 203,
				"podsPerCore": 11,
				"systemReserved": {"cpu": "510m", "memory": "521Mi"},
				"kubeReserved": {"cpu": "520m", "memory": "531Mi"},
				"evictionHard": {"memory.available": "201Mi", "nodefs.available": "11%"},
				"evictionSoft": {"memory.available": "401Mi", "nodefs.available": "16%"},
				"evictionSoftGracePeriod": {"memory.available": "1m31s", "nodefs.available": "2m5s"},
				"evictionMaxPodGracePeriod": 31,
				"imageGCHighThresholdPercent": 81,
				"imageGCLowThresholdPercent": 71,
				"cpuCFSQuota": false,
				"podPidsLimit": 4096,
				"containerLogMaxSize": "50Mi"
			}`
			var kubeletConfig hyperkarpenterv1.KubeletConfiguration
			Expect(json.Unmarshal([]byte(kubeletJSON), &kubeletConfig)).To(Succeed())

			nc := &hyperkarpenterv1.OpenshiftEC2NodeClass{
				ObjectMeta: metav1.ObjectMeta{Name: "kubelet-config-test"},
				Spec: hyperkarpenterv1.OpenshiftEC2NodeClassSpec{
					Kubelet: kubeletConfig,
				},
			}
			By(fmt.Sprintf("Creating OpenshiftEC2NodeClass %q with kubelet config", nc.Name))
			Expect(hcClient.Create(ctx, nc)).To(Succeed())
			DeferCleanup(func() {
				if err := hcClient.Delete(ctx, nc); err != nil && !apierrors.IsNotFound(err) {
					Expect(err).NotTo(HaveOccurred(), "cleanup: failed to delete OpenshiftEC2NodeClass %s", nc.Name)
				}
			})

			// Wait for the per-nodeclass KubeletConfig ConfigMap to appear in the HCP namespace.
			kubeletCMName := karpenterutil.KarpenterNodeClassKubeletConfigName(nc.Name)
			By(fmt.Sprintf("Waiting for KubeletConfig ConfigMap %s/%s", hcpNamespace, kubeletCMName))
			e2eutil.EventuallyObject(t, ctx, fmt.Sprintf("KubeletConfig ConfigMap %s/%s to appear", hcpNamespace, kubeletCMName),
				func(ctx context.Context) (*corev1.ConfigMap, error) {
					cm := &corev1.ConfigMap{}
					err := tc.MgmtClient.Get(ctx, crclient.ObjectKey{Name: kubeletCMName, Namespace: hcpNamespace}, cm)
					return cm, err
				},
				[]e2eutil.Predicate[*corev1.ConfigMap]{
					func(cm *corev1.ConfigMap) (bool, string, error) {
						if cm.Labels[karpenterutil.KarpenterNodeClassKubeletConfigLabel] != "true" {
							return false, fmt.Sprintf("missing label %s=true", karpenterutil.KarpenterNodeClassKubeletConfigLabel), nil
						}
						return true, "label present", nil
					},
					func(cm *corev1.ConfigMap) (bool, string, error) {
						config := cm.Data["config"]
						for _, field := range []string{"maxPods", "podsPerCore", "cpuCFSQuota", "podPidsLimit", "containerLogMaxSize"} {
							if !strings.Contains(config, field) {
								return false, fmt.Sprintf("config missing field %q", field), nil
							}
						}
						return true, "all required fields present in config", nil
					},
				},
				e2eutil.WithTimeout(2*time.Minute), e2eutil.WithInterval(5*time.Second),
			)

			// Wait for the karpenterignition controller to issue the ignition token with kubelet config.
			// The annotation is set after token.Reconcile() succeeds, guaranteeing Karpenter will use
			// the token (with kubelet config) when provisioning new nodes.
			By(fmt.Sprintf("Waiting for ignition token annotation on OpenshiftEC2NodeClass %q", nc.Name))
			e2eutil.EventuallyObject(t, ctx, fmt.Sprintf("OpenshiftEC2NodeClass %q to have ignition token annotation", nc.Name),
				func(ctx context.Context) (*hyperkarpenterv1.OpenshiftEC2NodeClass, error) {
					updated := &hyperkarpenterv1.OpenshiftEC2NodeClass{}
					err := hcClient.Get(ctx, crclient.ObjectKey{Name: nc.Name}, updated)
					return updated, err
				},
				[]e2eutil.Predicate[*hyperkarpenterv1.OpenshiftEC2NodeClass]{
					func(nc *hyperkarpenterv1.OpenshiftEC2NodeClass) (bool, string, error) {
						v := nc.GetAnnotations()["hypershift.openshift.io/nodeClassCurrentConfigVersion"]
						if v == "" {
							return false, "annotation hypershift.openshift.io/nodeClassCurrentConfigVersion not yet set", nil
						}
						return true, fmt.Sprintf("annotation set to %q", v), nil
					},
				},
				e2eutil.WithTimeout(2*time.Minute), e2eutil.WithInterval(5*time.Second),
			)

			// Wait for the OpenshiftEC2NodeClass to be fully Ready before creating the NodePool.
			// Karpenter ignores NodePools whose referenced EC2NodeClass is not Ready — the ignition
			// annotation above is set before AWS resource discovery (SecurityGroups, Subnets) completes,
			// so we must wait for the Ready condition explicitly to avoid provisioning delays.
			By(fmt.Sprintf("Waiting for OpenshiftEC2NodeClass %q to be Ready", nc.Name))
			e2eutil.EventuallyObject(t, ctx, fmt.Sprintf("OpenshiftEC2NodeClass %q to be Ready", nc.Name),
				func(ctx context.Context) (*hyperkarpenterv1.OpenshiftEC2NodeClass, error) {
					updated := &hyperkarpenterv1.OpenshiftEC2NodeClass{}
					err := hcClient.Get(ctx, crclient.ObjectKey{Name: nc.Name}, updated)
					return updated, err
				},
				[]e2eutil.Predicate[*hyperkarpenterv1.OpenshiftEC2NodeClass]{
					e2eutil.ConditionPredicate[*hyperkarpenterv1.OpenshiftEC2NodeClass](e2eutil.Condition{
						Type:   "Ready",
						Status: metav1.ConditionTrue,
					}),
				},
				e2eutil.WithTimeout(5*time.Minute),
			)

			// Create Karpenter NodePool pointing at the custom nodeclass and workloads to provision nodes.
			testNodePool := v2util.BaseNodePool("kubelet-config-test", nc.Name)
			testWorkLoads := v2util.TestWorkload("kubelet-config-web-app", 1, map[string]string{
				karpenterv1.NodePoolLabelKey: testNodePool.Name,
			})
			testNodeLabels := map[string]string{karpenterv1.NodePoolLabelKey: testNodePool.Name}

			v2util.CreateKarpenterNodePoolAndWorkload(Default, ctx, hcClient, hc.Spec.Platform.Type, testNodePool, testWorkLoads, testNodeLabels, false)

			// Wait for nodes to be provisioned
			e2eutil.WaitForReadyNodesByLabels(t, ctx, hcClient, hc.Spec.Platform.Type, 1, testNodeLabels)

			// Build a clientset for the hosted cluster (needed for pod log fetching)
			guestConfig := e2eutil.WaitForGuestRestConfig(t, ctx, tc.MgmtClient, hc)
			guestClientset, err := kubeclient.NewForConfig(guestConfig)
			Expect(err).NotTo(HaveOccurred())

			// Run a privileged pod on the karpenter node that prints kubelet.conf then
			// greps each expected field, exiting non-zero if any is missing.
			checkerPod := kubeletCheckerPodTemplate.DeepCopy()
			checkerPod.Spec.NodeSelector = testNodeLabels
			checkerPod.Spec.Tolerations = []corev1.Toleration{{Operator: corev1.TolerationOpExists}}
			By(fmt.Sprintf("Creating kubelet-config-checker pod on NodePool %q", testNodePool.Name))
			Expect(hcClient.Create(ctx, checkerPod)).To(Succeed())
			DeferCleanup(func() {
				if err := hcClient.Delete(ctx, checkerPod); err != nil && !apierrors.IsNotFound(err) {
					Expect(err).NotTo(HaveOccurred(), "cleanup: failed to delete Pod %s", checkerPod.Name)
				}
			})

			// Wait for the pod to complete (Succeeded or Failed)
			// This is intentionally not an EventuallyObject because we need to do something on either state
			Eventually(func(g Gomega) {
				p := &corev1.Pod{}
				g.Expect(hcClient.Get(ctx, crclient.ObjectKeyFromObject(checkerPod), p)).To(Succeed())
				g.Expect(p.Status.Phase).To(BeElementOf(corev1.PodSucceeded, corev1.PodFailed))
			}).WithTimeout(5 * time.Minute).WithPolling(5 * time.Second).Should(Succeed())

			// Fetch pod output with retries — the kubelet serving cert on freshly
			// provisioned Karpenter nodes may not be ready immediately after the
			// node is marked Ready, causing transient TLS or HTTP/2 errors on the
			// proxied log request.
			var logBytes []byte
			Eventually(func(g Gomega) {
				logReq := guestClientset.CoreV1().Pods(checkerPod.Namespace).GetLogs(checkerPod.Name, &corev1.PodLogOptions{Container: "checker"})
				logStream, err := logReq.Stream(ctx)
				g.Expect(err).NotTo(HaveOccurred())
				defer logStream.Close()
				logBytes, err = io.ReadAll(logStream)
				g.Expect(err).NotTo(HaveOccurred())
			}).WithTimeout(2 * time.Minute).WithPolling(10 * time.Second).Should(Succeed())
			GinkgoWriter.Printf("kubelet-config-checker output:\n%s\n", string(logBytes))

			// Assert the pod succeeded (grep chain exited 0 = all fields found)
			p := &corev1.Pod{}
			Expect(hcClient.Get(ctx, crclient.ObjectKeyFromObject(checkerPod), p)).To(Succeed())
			Expect(p.Status.Phase).To(Equal(corev1.PodSucceeded), "kubelet config fields not all found — see pod output above")
		})
	})
}

func KarpenterAutoNodeLifecycleTest(getTestCtx internal.TestContextGetter) {
	Context("[Feature:AutoNode] AutoNode enable/disable lifecycle", func() {
		BeforeEach(func() {
			tc := getTestCtx()
			tc.SkipIfNotPlatform(hyperv1.AWSPlatform)
			hc, err := tc.GetHostedCluster()
			Expect(err).NotTo(HaveOccurred())
			if !karpenterutil.IsKarpenterEnabled(hc.Spec.AutoNode) {
				Skip("AutoNode not configured on hosted cluster")
			}
		})

		It("should disable and re-enable AutoNode with correct condition transitions", func() {
			tc := getTestCtx()
			ctx := tc.Context
			t := GinkgoTB()
			hc, err := tc.GetHostedCluster()
			Expect(err).NotTo(HaveOccurred())

			Expect(tc.MgmtClient.Get(ctx, crclient.ObjectKeyFromObject(hc), hc)).To(Succeed())
			savedAutoNode := hc.Spec.AutoNode

			// Disable Karpenter.
			By("Disabling AutoNode on HostedCluster")
			err = e2eutil.UpdateObject(t, ctx, tc.MgmtClient, hc, func(obj *hyperv1.HostedCluster) {
				obj.Spec.AutoNode = hyperv1.AutoNode{}
			})
			Expect(err).NotTo(HaveOccurred(), "failed to disable AutoNode")

			// Note: we do NOT poll for AutoNodeProgressing during disable. The disable path completes
			// in a single reconcile loop (~<1s), which is shorter than our poll interval (3s), making
			// the transient Progressing state unreliably catchable. Go straight to the final state.

			// Expect fully disabled (components removed).
			e2eutil.EventuallyObject(t, ctx, "HostedCluster to have AutoNodeEnabled=False/AutoNodeNotConfigured",
				func(ctx context.Context) (*hyperv1.HostedCluster, error) {
					obj := &hyperv1.HostedCluster{}
					err := tc.MgmtClient.Get(ctx, crclient.ObjectKeyFromObject(hc), obj)
					return obj, err
				},
				[]e2eutil.Predicate[*hyperv1.HostedCluster]{
					e2eutil.ConditionPredicate[*hyperv1.HostedCluster](e2eutil.Condition{
						Type:   string(hyperv1.AutoNodeEnabled),
						Status: metav1.ConditionFalse,
						Reason: hyperv1.AutoNodeNotConfiguredReason,
					}),
				},
				e2eutil.WithTimeout(5*time.Minute),
			)

			// Re-enable Karpenter.
			By("Re-enabling AutoNode on HostedCluster")
			err = e2eutil.UpdateObject(t, ctx, tc.MgmtClient, hc, func(obj *hyperv1.HostedCluster) {
				obj.Spec.AutoNode = savedAutoNode
			})
			Expect(err).NotTo(HaveOccurred(), "failed to re-enable AutoNode")

			// The progressing state can be shorter than the polling interval, so accept either
			// the transient state or the final state here. The final state is checked below.
			autoNodeProgressingOrReady := func(obj *hyperv1.HostedCluster) (bool, string, error) {
				conditions, err := e2eutil.Conditions(obj)
				if err != nil {
					return false, "", err
				}
				for _, condition := range conditions {
					if condition.Type != string(hyperv1.AutoNodeEnabled) {
						continue
					}
					if condition.Status == metav1.ConditionFalse && condition.Reason == hyperv1.AutoNodeProgressingReason {
						return true, "AutoNode is progressing", nil
					}
					if condition.Status == metav1.ConditionTrue && condition.Reason == hyperv1.AsExpectedReason {
						return true, "AutoNode is ready", nil
					}
					return false, fmt.Sprintf("unexpected AutoNodeEnabled condition: %s", condition.String()), nil
				}
				return false, "AutoNodeEnabled condition is missing", nil
			}

			By("Waiting for AutoNodeEnabled to become progressing or ready")
			e2eutil.EventuallyObject(t, ctx, "HostedCluster AutoNodeEnabled to become progressing or ready",
				func(ctx context.Context) (*hyperv1.HostedCluster, error) {
					obj := &hyperv1.HostedCluster{}
					err := tc.MgmtClient.Get(ctx, crclient.ObjectKeyFromObject(hc), obj)
					return obj, err
				},
				[]e2eutil.Predicate[*hyperv1.HostedCluster]{autoNodeProgressingOrReady},
				e2eutil.WithTimeout(2*time.Minute),
			)

			// Expect fully enabled (both components rolled out).
			By("Waiting for AutoNodeEnabled=True with reason AsExpected")
			e2eutil.EventuallyObject(t, ctx, "HostedCluster to have AutoNodeEnabled=True/AsExpected",
				func(ctx context.Context) (*hyperv1.HostedCluster, error) {
					obj := &hyperv1.HostedCluster{}
					err := tc.MgmtClient.Get(ctx, crclient.ObjectKeyFromObject(hc), obj)
					return obj, err
				},
				[]e2eutil.Predicate[*hyperv1.HostedCluster]{
					e2eutil.ConditionPredicate[*hyperv1.HostedCluster](e2eutil.Condition{
						Type:   string(hyperv1.AutoNodeEnabled),
						Status: metav1.ConditionTrue,
						Reason: hyperv1.AsExpectedReason,
					}),
				},
				e2eutil.WithTimeout(5*time.Minute),
			)
		})
	})
}

func KarpenterBillingConsolidationTest(getTestCtx internal.TestContextGetter) {
	Context("[Feature:AutoNode] Billing vCPUs and consolidation", func() {
		BeforeEach(func() {
			tc := getTestCtx()
			tc.SkipIfNotPlatform(hyperv1.AWSPlatform)
			hc, err := tc.GetHostedCluster()
			Expect(err).NotTo(HaveOccurred())
			if !karpenterutil.IsKarpenterEnabled(hc.Spec.AutoNode) {
				Skip("AutoNode not configured on hosted cluster")
			}
		})

		It("should track vCPU billing metrics and consolidate nodes when workload scales down", func() {
			tc := getTestCtx()
			ctx := tc.Context
			t := GinkgoTB()
			hc, err := tc.GetHostedCluster()
			Expect(err).NotTo(HaveOccurred())
			hcClient, err := tc.GetHostedClusterClient(hc)
			Expect(err).NotTo(HaveOccurred())

			// Before any Karpenter nodes are provisioned, Karpenter vCPUs should be 0.
			v2util.WaitForAutoNodeStatusVCPUs(Default, ctx, tc.MgmtClient, hc, 0)
			v2util.WaitForAutoNodeStatusVCPUsStable(Default, ctx, tc.MgmtClient, hc, 0, 30*time.Second)

			baseline, found := getVCPUsMetric(ctx, tc.MgmtClient, hc)
			Expect(found).To(BeTrue(), "billing metric should exist before Karpenter nodes are provisioned")
			GinkgoWriter.Printf("Baseline billing metric vCPUs from native NodePools: %d\n", baseline)

			karpenterNodePool := v2util.BaseNodePool("on-demand", "default")
			workLoads := v2util.TestWorkload("web-app", 2, map[string]string{
				karpenterv1.NodePoolLabelKey: karpenterNodePool.Name,
			})
			nodeLabels := map[string]string{karpenterv1.NodePoolLabelKey: karpenterNodePool.Name}

			v2util.CreateKarpenterNodePoolAndWorkload(Default, ctx, hcClient, hc.Spec.Platform.Type, karpenterNodePool, workLoads, nodeLabels, true)

			_ = e2eutil.WaitForReadyNodesByLabels(t, ctx, hcClient, hc.Spec.Platform.Type, 2, nodeLabels)
			By("Validating billing vCPUs after provisioning two nodes")

			// t3.xlarge = 4 vCPUs; 2 nodes = 8 Karpenter vCPUs on top of baseline
			v2util.WaitForAutoNodeStatusVCPUs(Default, ctx, tc.MgmtClient, hc, 8)
			v2util.WaitForAutoNodeStatusVCPUsStable(Default, ctx, tc.MgmtClient, hc, 8, 30*time.Second)
			waitForBillingMetricVCPUs(ctx, tc.MgmtClient, hc, baseline+8)

			By("Scaling workload to 1 replica to verify deprovisioning and consolidation")
			err = e2eutil.UpdateObject(t, ctx, hcClient, workLoads, func(obj *appsv1.Deployment) {
				obj.Spec.Replicas = ptr.To(int32(1))
			})
			Expect(err).NotTo(HaveOccurred())

			_ = e2eutil.WaitForReadyNodesByLabels(t, ctx, hcClient, hc.Spec.Platform.Type, 1, nodeLabels)
			By("Validating billing vCPUs after consolidation")

			// t3.xlarge = 4 vCPUs; 1 node = 4 Karpenter vCPUs on top of baseline
			v2util.WaitForAutoNodeStatusVCPUs(Default, ctx, tc.MgmtClient, hc, 4)
			v2util.WaitForAutoNodeStatusVCPUsStable(Default, ctx, tc.MgmtClient, hc, 4, 30*time.Second)
			waitForBillingMetricVCPUs(ctx, tc.MgmtClient, hc, baseline+4)

			// Create a blocking PDB and leave everything dangling so cluster teardown
			// must force-terminate nodes despite a blocking PDB.
			pdb := &policyv1.PodDisruptionBudget{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "blocking-pdb",
					Namespace: "default",
				},
				Spec: policyv1.PodDisruptionBudgetSpec{
					Selector: &metav1.LabelSelector{
						MatchLabels: map[string]string{
							"app": "web-app",
						},
					},
					MinAvailable: &intstr.IntOrString{
						Type:   intstr.String,
						StrVal: "100%",
					},
				},
			}
			Expect(hcClient.Create(ctx, pdb)).To(Succeed())
			t.Logf("Created cluster-deletion-blocking PodDisruptionBudget")
		})
	})
}

func waitForBillingMetricVCPUs(ctx context.Context, mgtClient crclient.Client, hostedCluster *hyperv1.HostedCluster, expectedTotal int32) {
	err := wait.PollUntilContextTimeout(ctx, 5*time.Second, 1*time.Minute, true, func(ctx context.Context) (bool, error) {
		actual, found := getVCPUsMetric(ctx, mgtClient, hostedCluster)
		if !found {
			return false, nil
		}
		return actual == expectedTotal, nil
	})
	Expect(err).NotTo(HaveOccurred(), "failed to validate %s metric", npmetrics.VCpusCountByHClusterMetricName)
}

func getVCPUsMetric(ctx context.Context, mgtClient crclient.Client, hostedCluster *hyperv1.HostedCluster) (int32, bool) {
	mf, err := e2eutil.GetMetricsFromPod(ctx, mgtClient, "operator", "operator", "hypershift", "9000")
	if err != nil {
		return 0, false
	}
	family, ok := mf[npmetrics.VCpusCountByHClusterMetricName]
	if !ok {
		return 0, false
	}
	for _, m := range family.Metric {
		var matchedName, matchedNamespace bool
		for _, l := range m.GetLabel() {
			if l.GetName() == "name" && l.GetValue() == hostedCluster.Name {
				matchedName = true
			}
			if l.GetName() == "namespace" && l.GetValue() == hostedCluster.Namespace {
				matchedNamespace = true
			}
		}
		if matchedName && matchedNamespace {
			return int32(m.GetGauge().GetValue()), true
		}
	}
	return 0, false
}
