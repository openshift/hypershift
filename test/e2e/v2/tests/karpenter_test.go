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
	awsutil "github.com/openshift/hypershift/cmd/infra/aws/util"
	karpentercpov2 "github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/v2/karpenter"
	karpenteroperatorcpov2 "github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/v2/karpenteroperator"
	"github.com/openshift/hypershift/hypershift-operator/controllers/manifests"
	npmetrics "github.com/openshift/hypershift/hypershift-operator/controllers/nodepool/metrics"
	karpenterassets "github.com/openshift/hypershift/karpenter-operator/controllers/karpenter/assets"
	karpenterutil "github.com/openshift/hypershift/support/karpenter"
	"github.com/openshift/hypershift/support/releaseinfo"
	"github.com/openshift/hypershift/support/supportedversion"
	e2eutil "github.com/openshift/hypershift/test/e2e/util"
	"github.com/openshift/hypershift/test/e2e/v2/internal"
	dto "github.com/prometheus/client_model/go"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	policyv1 "k8s.io/api/policy/v1"
	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/resource"
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

			err = wait.PollUntilContextTimeout(ctx, 10*time.Second, 5*time.Minute, true, func(ctx context.Context) (bool, error) {
				kmf, err := e2eutil.GetMetricsFromPod(ctx, tc.MgmtClient, karpentercpov2.ComponentName, karpentercpov2.ComponentName, karpenterNamespace, "8080")
				if err != nil {
					GinkgoWriter.Printf("unable to get karpenter metrics: %v", err)
					return false, nil
				}
				komf, err := e2eutil.GetMetricsFromPod(ctx, tc.MgmtClient, karpenteroperatorcpov2.ComponentName, karpenteroperatorcpov2.ComponentName, karpenterNamespace, "8080")
				if err != nil {
					GinkgoWriter.Printf("unable to get karpenter metrics: %v", err)
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

				GinkgoWriter.Printf("Expected metrics are exposed: %v", karpenterMetrics)
				return true, nil
			})
			Expect(err).NotTo(HaveOccurred(), "failed to validate Karpenter metrics")
		})

		It("should report AutoNode vCPUs status as 0 when no Karpenter nodes are provisioned", func() {
			tc := getTestCtx()
			hc, err := tc.GetHostedCluster()
			Expect(err).NotTo(HaveOccurred())
			waitForAutoNodeStatusVCPUs(tc.Context, tc.MgmtClient, hc, 0)
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

			GinkgoWriter.Println("Validating default OpenshiftEC2NodeClass exists with expected values")
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

			expectedTags := expectedPlatformTags(hc)
			Expect(expectedTags).NotTo(BeEmpty(), "HostedCluster has no non-restricted resource tags; cluster setup must include at least one propagatable tag for this test to be meaningful")

			GinkgoWriter.Println("Validating corresponding default EC2NodeClass has immutable service-owned fields set")
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
			Expect(hcClient.Create(ctx, armNodeClass)).To(Succeed())
			GinkgoWriter.Println("Created ARM64 OpenshiftEC2NodeClass")
			DeferCleanup(func() {
				if err := hcClient.Delete(ctx, armNodeClass); err != nil && !apierrors.IsNotFound(err) {
					Expect(err).NotTo(HaveOccurred(), "cleanup: failed to delete OpenshiftEC2NodeClass %s", armNodeClass.Name)
				}
			})

			armNodePool := baseNodePool("arm-nodepool", armNodeClass.Name)
			armNodePool.Spec.Template.Spec.Requirements = []karpenterv1.NodeSelectorRequirementWithMinValues{
				{Key: "node.kubernetes.io/instance-type", Operator: corev1.NodeSelectorOpIn, Values: []string{"m6g.xlarge"}},
				{Key: "kubernetes.io/arch", Operator: corev1.NodeSelectorOpIn, Values: []string{"arm64"}},
				{Key: karpenterv1.CapacityTypeLabelKey, Operator: corev1.NodeSelectorOpIn, Values: []string{karpenterv1.CapacityTypeOnDemand}},
			}
			// quay.io/openshift/origin-pod does not support arm64
			armWorkLoads := testWorkloadWithImage("arm-app", 1, map[string]string{karpenterv1.NodePoolLabelKey: armNodePool.Name}, "registry.access.redhat.com/ubi10/ubi-minimal:10.1")

			armNodeLabels := map[string]string{
				karpenterv1.NodePoolLabelKey: armNodePool.Name,
				"kubernetes.io/arch":         "arm64",
			}

			Expect(hcClient.Create(ctx, armNodePool)).To(Succeed())
			GinkgoWriter.Println("Created ARM64 NodePool")
			DeferCleanup(func() {
				if err := hcClient.Delete(ctx, armNodePool); err != nil {
					if apierrors.IsNotFound(err) {
						return
					}
					Expect(err).NotTo(HaveOccurred(), "cleanup: failed to delete NodePool %s", armNodePool.Name)
				}
				_ = e2eutil.WaitForReadyNodesByLabels(t, ctx, hcClient, hc.Spec.Platform.Type, 0, armNodeLabels)
			})

			Expect(hcClient.Create(ctx, armWorkLoads)).To(Succeed())
			DeferCleanup(func() {
				if err := hcClient.Delete(ctx, armWorkLoads); err != nil && !apierrors.IsNotFound(err) {
					Expect(err).NotTo(HaveOccurred(), "cleanup: failed to delete Deployment %s", armWorkLoads.Name)
				}
			})
			GinkgoWriter.Println("Created ARM64 workloads")

			nodes := e2eutil.WaitForReadyNodesByLabels(t, ctx, hcClient, hc.Spec.Platform.Type, 1, armNodeLabels)
			waitForReadyKarpenterPods(ctx, hcClient, nodes, 1, map[string]string{"app": "arm-app"})
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
			GinkgoWriter.Printf("Applied annotation %s=%s to HostedCluster", hyperv1.AWSKarpenterDefaultInstanceProfile, workerInstanceProfile)

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
						GinkgoWriter.Printf("Restored annotation %s=%s to HostedCluster", hyperv1.AWSKarpenterDefaultInstanceProfile, origInstanceProfile)
					} else {
						delete(obj.Annotations, hyperv1.AWSKarpenterDefaultInstanceProfile)
						GinkgoWriter.Printf("Removed annotation %s from HostedCluster", hyperv1.AWSKarpenterDefaultInstanceProfile)
					}
				})).To(Succeed(), "cleanup: failed to remove instance profile annotation from HostedCluster %s/%s", hc.Namespace, hc.Name)

				GinkgoWriter.Printf("Waiting for EC2NodeClass to have InstanceProfile cleared")
				Eventually(func(g Gomega) {
					ec2NodeClass := &awskarpenterv1.EC2NodeClass{}
					g.Expect(hcClient.Get(ctx, crclient.ObjectKey{Name: "default"}, ec2NodeClass)).To(Succeed())
					g.Expect(ec2NodeClass.Spec.InstanceProfile).To(BeNil(), "InstanceProfile should be cleared after annotation removal")
				}).WithTimeout(2 * time.Minute).WithPolling(5 * time.Second).Should(Succeed())
			})

			GinkgoWriter.Printf("Waiting for EC2NodeClass to have InstanceProfile set to %s", workerInstanceProfile)
			Eventually(func(g Gomega) {
				ec2NodeClass := &awskarpenterv1.EC2NodeClass{}
				err := hcClient.Get(ctx, crclient.ObjectKey{Name: "default"}, ec2NodeClass)
				g.Expect(err).NotTo(HaveOccurred())
				g.Expect(ec2NodeClass.Spec.InstanceProfile).NotTo(BeNil(), "InstanceProfile should be set")
				g.Expect(*ec2NodeClass.Spec.InstanceProfile).To(Equal(workerInstanceProfile))
			}).WithTimeout(2 * time.Minute).WithPolling(5 * time.Second).Should(Succeed())

			// Now provision actual nodes to verify EC2 instances get the instance profile
			GinkgoWriter.Printf("Creating Karpenter NodePool and workloads to provision nodes")

			testNodePool := baseNodePool("instance-profile-test", "default")
			testWorkLoads := testWorkload("instance-profile-web-app", 1, map[string]string{
				karpenterv1.NodePoolLabelKey: testNodePool.Name,
			})
			testNodeLabels := map[string]string{karpenterv1.NodePoolLabelKey: testNodePool.Name}

			Expect(hcClient.Create(ctx, testWorkLoads)).To(Succeed())
			DeferCleanup(func() {
				if err := hcClient.Delete(ctx, testWorkLoads); err != nil && !apierrors.IsNotFound(err) {
					Expect(err).NotTo(HaveOccurred(), "cleanup: failed to delete Deployment %s", testWorkLoads.Name)
				}
			})
			Expect(hcClient.Create(ctx, testNodePool)).To(Succeed())
			DeferCleanup(func() {
				if err := hcClient.Delete(ctx, testNodePool); err != nil {
					if apierrors.IsNotFound(err) {
						return
					}
					Expect(err).NotTo(HaveOccurred(), "cleanup: failed to delete NodePool %s", testNodePool.Name)
				}
				_ = e2eutil.WaitForReadyNodesByLabels(t, ctx, hcClient, hc.Spec.Platform.Type, 0, testNodeLabels)
			})

			nodes := e2eutil.WaitForReadyNodesByLabels(t, ctx, hcClient, hc.Spec.Platform.Type, 1, testNodeLabels)

			// Verify EC2 instances have the correct instance profile
			ec2client := newEC2Client(awsCredsFile, awsRegion)
			for _, node := range nodes {
				instance, instanceID := describeEC2Instance(ctx, ec2client, node)
				GinkgoWriter.Printf("Checking instance profile for node %s (instance %s)", node.Name, instanceID)
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
			GinkgoWriter.Printf("Control plane version: %s", cpVersion.String())

			// Verify default OpenshiftEC2NodeClass uses control plane release image
			GinkgoWriter.Printf("Verifying default OpenshiftEC2NodeClass uses control plane release image")
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
			Expect(hcClient.Create(ctx, nc)).To(Succeed())
			GinkgoWriter.Printf("Created OpenshiftEC2NodeClass %q with version %s (CP version: %s)", nc.Name, nodeClassVersion, cpVersion)
			DeferCleanup(func() {
				if err := hcClient.Delete(ctx, nc); err != nil && !apierrors.IsNotFound(err) {
					Expect(err).NotTo(HaveOccurred(), "cleanup: failed to delete OpenshiftEC2NodeClass %s", nc.Name)
				}
			})

			// Wait for version resolution and get the resolved release image
			var resolvedReleaseImage string
			GinkgoWriter.Printf("Waiting for OpenshiftEC2NodeClass version resolution")
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
			GinkgoWriter.Printf("Verifying MetadataOptions propagated to EC2NodeClass")
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
			GinkgoWriter.Printf("Expected kubelet version for %s: v%s", nodeClassVersion, expectedKubeletVersion)

			// Create a Karpenter NodePool that references the custom EC2NodeClass
			testNodePool := baseNodePool("version-test", nc.Name)
			testWorkLoads := testWorkload("version-test-app", 1, map[string]string{
				karpenterv1.NodePoolLabelKey: testNodePool.Name,
			})
			// Use only the nodepool label to select nodes exclusively tied to our version-test nodeclass.
			testNodeLabels := map[string]string{
				karpenterv1.NodePoolLabelKey: testNodePool.GetName(),
			}

			Expect(hcClient.Create(ctx, testNodePool)).To(Succeed())
			GinkgoWriter.Printf("Created Karpenter NodePool %q", testNodePool.Name)
			DeferCleanup(func() {
				if err := hcClient.Delete(ctx, testNodePool); err != nil {
					if apierrors.IsNotFound(err) {
						return
					}
					Expect(err).NotTo(HaveOccurred(), "cleanup: failed to delete NodePool %s", testNodePool.Name)
				}
				_ = e2eutil.WaitForReadyNodesByLabels(t, ctx, hcClient, hc.Spec.Platform.Type, 0, testNodeLabels)
			})

			Expect(hcClient.Create(ctx, testWorkLoads)).To(Succeed())
			GinkgoWriter.Printf("Created workload %q", testWorkLoads.Name)
			DeferCleanup(func() {
				if err := hcClient.Delete(ctx, testWorkLoads); err != nil && !apierrors.IsNotFound(err) {
					Expect(err).NotTo(HaveOccurred(), "cleanup: failed to delete Deployment %s", testWorkLoads.Name)
				}
			})

			// Log diagnostic info about the version-test NodeClass infrastructure.
			hcpNamespace := manifests.HostedControlPlaneNamespace(hc.Namespace, hc.Name)
			secretList := &corev1.SecretList{}
			if err := tc.MgmtClient.List(ctx, secretList,
				crclient.InNamespace(hcpNamespace),
				crclient.MatchingLabels{"hypershift.openshift.io/managed-by-karpenter": "true"},
			); err != nil {
				GinkgoWriter.Printf("WARNING: failed to list karpenter secrets in %s: %v", hcpNamespace, err)
			} else {
				foundUserData := false
				for _, s := range secretList.Items {
					npAnnotation := s.Annotations["hypershift.openshift.io/nodePool"]
					if strings.Contains(npAnnotation, "version-test") {
						GinkgoWriter.Printf("Found karpenter secret %q for nodepool %q (labels: %v)", s.Name, npAnnotation, s.Labels)
						foundUserData = true
					}
				}
				if !foundUserData {
					GinkgoWriter.Printf("WARNING: no user-data secret found for version-test NodeClass. Token creation may be failing - check karpenter-operator logs.")
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
			GinkgoWriter.Printf("Node provisioned with correct kubelet version (v%s) for NodeClass version %s", expectedKubeletVersion, nodeClassVersion)

			// Verify MetadataOptions on EC2 instance
			GinkgoWriter.Printf("Verifying MetadataOptions on EC2 instance via DescribeInstances")
			ec2client := newEC2Client(awsCredsFile, awsRegion)
			for _, node := range nodes {
				instance, instanceID := describeEC2Instance(ctx, ec2client, node)
				GinkgoWriter.Printf("Checking MetadataOptions for node %s (instance %s)", node.Name, instanceID)
				Expect(instance.MetadataOptions).NotTo(BeNil(), "instance should have MetadataOptions")
				Expect(string(instance.MetadataOptions.HttpEndpoint)).To(Equal("enabled"), "instance %s HttpEndpoint mismatch", instanceID)
				Expect(string(instance.MetadataOptions.HttpProtocolIpv6)).To(Equal("disabled"), "instance %s HttpProtocolIpv6 mismatch", instanceID)
				Expect(*instance.MetadataOptions.HttpPutResponseHopLimit).To(Equal(int32(2)), "instance %s HttpPutResponseHopLimit mismatch", instanceID)
				Expect(string(instance.MetadataOptions.HttpTokens)).To(Equal("required"), "instance %s HttpTokens mismatch", instanceID)
				GinkgoWriter.Printf("Instance %s has correct MetadataOptions: HttpTokens=%s, HttpEndpoint=%s, HttpPutResponseHopLimit=%d",
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
			Expect(hcClient.Create(ctx, skewNC)).To(Succeed())
			GinkgoWriter.Printf("Created OpenshiftEC2NodeClass %q with version %s (CP version: %s)", skewNC.Name, skewVersion, cpVersion)
			DeferCleanup(func() {
				if err := hcClient.Delete(ctx, skewNC); err != nil && !apierrors.IsNotFound(err) {
					Expect(err).NotTo(HaveOccurred(), "cleanup: failed to delete OpenshiftEC2NodeClass %s", skewNC.Name)
				}
				GinkgoWriter.Printf("Cleaned up OpenshiftEC2NodeClass %q", skewNC.Name)
			})

			GinkgoWriter.Printf("Waiting for VersionResolved=True and SupportedVersionSkew=False")
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
			GinkgoWriter.Printf("OpenshiftEC2NodeClass %q has SupportedVersionSkew=False for version %s (exceeds n-3 skew from CP %s)", skewNC.Name, skewVersion, cpVersion)
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
			GinkgoWriter.Printf("Using availability zone %s for capacity reservation", targetAZ)

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
			GinkgoWriter.Printf("Created capacity reservation %s in %s", crID, targetAZ)

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
			Expect(hcClient.Create(ctx, crNodeClass)).To(Succeed())
			DeferCleanup(func() {
				if err := hcClient.Delete(ctx, crNodeClass); err != nil && !apierrors.IsNotFound(err) {
					Expect(err).NotTo(HaveOccurred(), "cleanup: failed to delete OpenshiftEC2NodeClass %s", crNodeClass.Name)
				}
			})
			GinkgoWriter.Printf("Created OpenshiftEC2NodeClass capacity-reservation-test with CapacityReservationSelectorTerms ID=%s", crID)

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
			crNodePool := baseNodePool("capacity-reservation-test", "capacity-reservation-test")
			crNodePool.Spec.Template.Spec.Requirements = []karpenterv1.NodeSelectorRequirementWithMinValues{
				{Key: "node.kubernetes.io/instance-type", Operator: corev1.NodeSelectorOpIn, Values: []string{"t3.xlarge"}},
				{Key: karpenterv1.CapacityTypeLabelKey, Operator: corev1.NodeSelectorOpIn, Values: []string{karpenterv1.CapacityTypeReserved}},
			}
			crNodeLabels := map[string]string{karpenterv1.NodePoolLabelKey: crNodePool.Name}
			crWorkload := testWorkload("capacity-reservation-web-app", 1, crNodeLabels)

			Expect(hcClient.Create(ctx, crNodePool)).To(Succeed())
			DeferCleanup(func() {
				if err := hcClient.Delete(ctx, crNodePool); err != nil {
					if apierrors.IsNotFound(err) {
						return
					}
					Expect(err).NotTo(HaveOccurred(), "cleanup: failed to delete NodePool %s", crNodePool.Name)
				}
				_ = e2eutil.WaitForReadyNodesByLabels(t, ctx, hcClient, hc.Spec.Platform.Type, 0, crNodeLabels)
			})
			GinkgoWriter.Printf("Created NodePool capacity-reservation-test targeting capacity reservation %s", crID)

			Expect(hcClient.Create(ctx, crWorkload)).To(Succeed())
			DeferCleanup(func() {
				if err := hcClient.Delete(ctx, crWorkload); err != nil && !apierrors.IsNotFound(err) {
					Expect(err).NotTo(HaveOccurred(), "cleanup: failed to delete Deployment %s", crWorkload.Name)
				}
			})
			GinkgoWriter.Printf("Created workload capacity-reservation-web-app to trigger node provisioning")

			// Wait for the node to be ready.
			nodes := e2eutil.WaitForReadyNodesByLabels(t, ctx, hcClient, hc.Spec.Platform.Type, 1, crNodeLabels)
			Expect(nodes).To(HaveLen(1))

			// Verify the EC2 instance was launched into the capacity reservation.
			ec2client := newEC2Client(awsCredsFile, awsRegion)

			instance, instanceID := describeEC2Instance(ctx, ec2client, nodes[0])
			GinkgoWriter.Printf("Verifying EC2 instance %s was launched into capacity reservation %s", instanceID, crID)
			Expect(instance.CapacityReservationId).NotTo(BeNil(), "instance %s should have a CapacityReservationId", instanceID)
			Expect(aws.ToString(instance.CapacityReservationId)).To(Equal(crID),
				"instance %s should have been launched into capacity reservation %s", instanceID, crID)
			GinkgoWriter.Printf("Instance %s correctly launched into capacity reservation %s", instanceID, crID)

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
			ec2client := newEC2Client(awsCredsFile, awsRegion)
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
			GinkgoWriter.Printf("VPC endpoint service %s supports AZs: %v", endpointServiceName, supportedAZs)

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
			GinkgoWriter.Printf("Selected AZ %s for test subnet (supported by endpoint service, not in VPC)", az)

			// Create a small test subnet in the VPC.
			subnetID, cleanupSubnet := e2eutil.CreateTestSubnet(ctx, t, ec2client, vpcID, az, hc.Spec.InfraID, hc.Name, e2eutil.E2ETagsFromEnvironment())
			DeferCleanup(func() {
				cleanupSubnet()
			})
			GinkgoWriter.Printf("Created test subnet %s in AZ %s", subnetID, az)

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
			Expect(hcClient.Create(ctx, customNodeClass)).To(Succeed())
			GinkgoWriter.Printf("Created OpenshiftEC2NodeClass %q selecting subnet %s", customNodeClass.Name, subnetID)
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
			GinkgoWriter.Printf("Waiting for OpenshiftEC2NodeClass status to reflect subnet %s", subnetID)
			Eventually(func(g Gomega) {
				nc := &hyperkarpenterv1.OpenshiftEC2NodeClass{}
				g.Expect(hcClient.Get(ctx, crclient.ObjectKeyFromObject(customNodeClass), nc)).To(Succeed())
				subnetIDs := make([]string, 0, len(nc.Status.Subnets))
				for _, s := range nc.Status.Subnets {
					subnetIDs = append(subnetIDs, s.ID)
				}
				g.Expect(subnetIDs).To(ContainElement(subnetID), "status.subnets should contain the test subnet")
			}).WithTimeout(2 * time.Minute).WithPolling(5 * time.Second).Should(Succeed())
			GinkgoWriter.Printf("OpenshiftEC2NodeClass status.subnets contains %s", subnetID)

			// Wait for the karpenter-subnets ConfigMap in the HCP namespace to contain the subnet ID.
			// hcpNamespace was already set above during AZ selection.
			GinkgoWriter.Printf("Waiting for karpenter-subnets ConfigMap in %s to contain subnet %s", hcpNamespace, subnetID)
			Eventually(func(g Gomega) {
				cm := &corev1.ConfigMap{}
				g.Expect(tc.MgmtClient.Get(ctx, crclient.ObjectKey{Namespace: hcpNamespace, Name: karpenterutil.KarpenterSubnetsConfigMapName}, cm)).To(Succeed())
				g.Expect(cm.Data).To(HaveKey("subnetIDs"))
				var cmSubnetIDs []string
				g.Expect(json.Unmarshal([]byte(cm.Data["subnetIDs"]), &cmSubnetIDs)).To(Succeed())
				g.Expect(cmSubnetIDs).To(ContainElement(subnetID), "karpenter-subnets ConfigMap should contain the test subnet")
			}).WithTimeout(2 * time.Minute).WithPolling(5 * time.Second).Should(Succeed())
			GinkgoWriter.Printf("karpenter-subnets ConfigMap contains subnet %s", subnetID)

			// Wait for any AWSEndpointService in the HCP namespace to include the subnet ID.
			// Which AWSEndpointService resources exist depends on the APIServer publishing
			// strategy: with LoadBalancer publishing, "kube-apiserver-private" is created;
			// with Route publishing (used when ExternalDNS is configured), only
			// "private-router" exists. We check all of them to be independent of the
			// publishing strategy.
			GinkgoWriter.Printf("Waiting for any AWSEndpointService in %s to include subnet %s", hcpNamespace, subnetID)
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
			GinkgoWriter.Printf("Waiting for AWSEndpointAvailable=True on all AWSEndpointServices in %s", hcpNamespace)
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
			testNodePool := baseNodePool("arbitrary-subnet-test", customNodeClass.Name)
			testWorkLoads := testWorkload("arbitrary-subnet-web-app", 1, map[string]string{
				karpenterv1.NodePoolLabelKey: testNodePool.Name,
			})
			testNodeLabels := map[string]string{karpenterv1.NodePoolLabelKey: testNodePool.Name}

			Expect(hcClient.Create(ctx, testNodePool)).To(Succeed())
			GinkgoWriter.Printf("Created Karpenter NodePool %q", testNodePool.Name)
			DeferCleanup(func() {
				if err := hcClient.Delete(ctx, testNodePool); err != nil {
					if apierrors.IsNotFound(err) {
						return
					}
					Expect(err).NotTo(HaveOccurred(), "cleanup: failed to delete NodePool %s", testNodePool.Name)
				}
				_ = e2eutil.WaitForReadyNodesByLabels(t, ctx, hcClient, hc.Spec.Platform.Type, 0, testNodeLabels)
			})

			Expect(hcClient.Create(ctx, testWorkLoads)).To(Succeed())
			GinkgoWriter.Printf("Created workload %q", testWorkLoads.Name)
			DeferCleanup(func() {
				if err := hcClient.Delete(ctx, testWorkLoads); err != nil && !apierrors.IsNotFound(err) {
					Expect(err).NotTo(HaveOccurred(), "cleanup: failed to delete Deployment %s", testWorkLoads.Name)
				}
			})

			nodes := e2eutil.WaitForReadyNodesByLabels(t, ctx, hcClient, hc.Spec.Platform.Type, 1, testNodeLabels)
			GinkgoWriter.Printf("Node launched in arbitrary subnet, verifying it used subnet %s", subnetID)

			// Verify the launched node's EC2 instance is in the expected subnet.
			for _, node := range nodes {
				instance, instanceID := describeEC2Instance(ctx, ec2client, node)
				Expect(aws.ToString(instance.SubnetId)).To(Equal(subnetID),
					"instance %s should be in subnet %s", instanceID, subnetID)
				GinkgoWriter.Printf("Instance %s confirmed in subnet %s", instanceID, subnetID)
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
			Expect(hcClient.Create(ctx, nc)).To(Succeed())
			GinkgoWriter.Printf("Created OpenshiftEC2NodeClass %q with kubelet config", nc.Name)
			DeferCleanup(func() {
				if err := hcClient.Delete(ctx, nc); err != nil && !apierrors.IsNotFound(err) {
					Expect(err).NotTo(HaveOccurred(), "cleanup: failed to delete OpenshiftEC2NodeClass %s", nc.Name)
				}
			})

			// Wait for the per-nodeclass KubeletConfig ConfigMap to appear in the HCP namespace.
			kubeletCMName := karpenterutil.KarpenterNodeClassKubeletConfigName(nc.Name)
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
			GinkgoWriter.Printf("KubeletConfig ConfigMap %s is present and correct", kubeletCMName)

			// Wait for the karpenterignition controller to issue the ignition token with kubelet config.
			// The annotation is set after token.Reconcile() succeeds, guaranteeing Karpenter will use
			// the token (with kubelet config) when provisioning new nodes.
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
			GinkgoWriter.Printf("Ignition token annotation set on %q", nc.Name)

			// Wait for the OpenshiftEC2NodeClass to be fully Ready before creating the NodePool.
			// Karpenter ignores NodePools whose referenced EC2NodeClass is not Ready — the ignition
			// annotation above is set before AWS resource discovery (SecurityGroups, Subnets) completes,
			// so we must wait for the Ready condition explicitly to avoid provisioning delays.
			GinkgoWriter.Printf("Make sure OpenshiftEC2NodeClass %q is Ready before nodepool creation", nc.Name)
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
			GinkgoWriter.Printf("OpenshiftEC2NodeClass %q is Ready", nc.Name)

			// Create Karpenter NodePool pointing at the custom nodeclass and workloads to provision nodes.
			testNodePool := baseNodePool("kubelet-config-test", nc.Name)
			testWorkLoads := testWorkload("kubelet-config-web-app", 1, map[string]string{
				karpenterv1.NodePoolLabelKey: testNodePool.Name,
			})
			testNodeLabels := map[string]string{karpenterv1.NodePoolLabelKey: testNodePool.Name}

			Expect(hcClient.Create(ctx, testNodePool)).To(Succeed())
			GinkgoWriter.Printf("Created Karpenter NodePool %s", testNodePool.Name)
			DeferCleanup(func() {
				if err := hcClient.Delete(ctx, testNodePool); err != nil {
					if apierrors.IsNotFound(err) {
						return
					}
					Expect(err).NotTo(HaveOccurred(), "cleanup: failed to delete NodePool %s", testNodePool.Name)
				}
				_ = e2eutil.WaitForReadyNodesByLabels(t, ctx, hcClient, hc.Spec.Platform.Type, 0, testNodeLabels)
			})

			Expect(hcClient.Create(ctx, testWorkLoads)).To(Succeed())
			GinkgoWriter.Printf("Created workloads %s", testWorkLoads.Name)
			DeferCleanup(func() {
				if err := hcClient.Delete(ctx, testWorkLoads); err != nil && !apierrors.IsNotFound(err) {
					Expect(err).NotTo(HaveOccurred(), "cleanup: failed to delete Deployment %s", testWorkLoads.Name)
				}
			})

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
			Expect(hcClient.Create(ctx, checkerPod)).To(Succeed())
			GinkgoWriter.Printf("Created kubelet-config-checker pod on nodepool %s", testNodePool.Name)
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
			GinkgoWriter.Printf("kubelet-config-checker output:\n%s", string(logBytes))

			// Assert the pod succeeded (grep chain exited 0 = all fields found)
			p := &corev1.Pod{}
			Expect(hcClient.Get(ctx, crclient.ObjectKeyFromObject(checkerPod), p)).To(Succeed())
			Expect(p.Status.Phase).To(Equal(corev1.PodSucceeded), "kubelet config fields not all found — see pod output above")
			GinkgoWriter.Printf("kubelet config fields confirmed on node")

			// Cleanup workloads and NodePool
			Expect(hcClient.Delete(ctx, testWorkLoads)).To(Succeed())
			Expect(hcClient.Delete(ctx, testNodePool)).To(Succeed())
			_ = e2eutil.WaitForReadyNodesByLabels(t, ctx, hcClient, hc.Spec.Platform.Type, 0, testNodeLabels)
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
			GinkgoWriter.Printf("Disabling AutoNode (Karpenter) on HostedCluster")
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
			GinkgoWriter.Println("Re-enabling AutoNode (Karpenter) on HostedCluster")
			err = e2eutil.UpdateObject(t, ctx, tc.MgmtClient, hc, func(obj *hyperv1.HostedCluster) {
				obj.Spec.AutoNode = savedAutoNode
			})
			Expect(err).NotTo(HaveOccurred(), "failed to re-enable AutoNode")

			// Expect progressing (enable in flight — components being created/rolled out).
			GinkgoWriter.Println("Waiting for AutoNodeEnabled=False/AutoNodeProgressing (enable in progress)")
			e2eutil.EventuallyObject(t, ctx, "HostedCluster to have AutoNodeEnabled=False/AutoNodeProgressing",
				func(ctx context.Context) (*hyperv1.HostedCluster, error) {
					obj := &hyperv1.HostedCluster{}
					err := tc.MgmtClient.Get(ctx, crclient.ObjectKeyFromObject(hc), obj)
					return obj, err
				},
				[]e2eutil.Predicate[*hyperv1.HostedCluster]{
					e2eutil.ConditionPredicate[*hyperv1.HostedCluster](e2eutil.Condition{
						Type:   string(hyperv1.AutoNodeEnabled),
						Status: metav1.ConditionFalse,
						Reason: hyperv1.AutoNodeProgressingReason,
					}),
				},
				e2eutil.WithTimeout(2*time.Minute),
			)

			// Expect fully enabled (both components rolled out).
			GinkgoWriter.Println("Waiting for AutoNodeEnabled=True/AsExpected (enable complete)")
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
			waitForAutoNodeStatusVCPUs(ctx, tc.MgmtClient, hc, 0)
			waitForAutoNodeStatusVCPUsStable(ctx, tc.MgmtClient, hc, 0, 30*time.Second)

			baseline, found := getVCPUsMetric(ctx, tc.MgmtClient, hc)
			Expect(found).To(BeTrue(), "billing metric should exist before Karpenter nodes are provisioned")
			GinkgoWriter.Printf("Baseline billing metric vCPUs from native NodePools: %d\n", baseline)

			karpenterNodePool := baseNodePool("on-demand", "default")
			workLoads := testWorkload("web-app", 2, map[string]string{
				karpenterv1.NodePoolLabelKey: karpenterNodePool.Name,
			})
			nodeLabels := map[string]string{karpenterv1.NodePoolLabelKey: karpenterNodePool.Name}

			Expect(hcClient.Create(ctx, karpenterNodePool)).To(Succeed())
			GinkgoWriter.Println("Created Karpenter NodePool")

			Expect(hcClient.Create(ctx, workLoads)).To(Succeed())
			GinkgoWriter.Println("Created workloads with 2 replicas")

			_ = e2eutil.WaitForReadyNodesByLabels(t, ctx, hcClient, hc.Spec.Platform.Type, 2, nodeLabels)
			GinkgoWriter.Println("Both nodes ready, validating billing vCPUs")

			// t3.xlarge = 4 vCPUs; 2 nodes = 8 Karpenter vCPUs on top of baseline
			waitForAutoNodeStatusVCPUs(ctx, tc.MgmtClient, hc, 8)
			waitForAutoNodeStatusVCPUsStable(ctx, tc.MgmtClient, hc, 8, 30*time.Second)
			waitForBillingMetricVCPUs(ctx, tc.MgmtClient, hc, baseline+8)

			GinkgoWriter.Println("Scaling workload to 1 replica to verify deprovisioning and consolidation")
			err = e2eutil.UpdateObject(t, ctx, hcClient, workLoads, func(obj *appsv1.Deployment) {
				obj.Spec.Replicas = ptr.To(int32(1))
			})
			Expect(err).NotTo(HaveOccurred())

			_ = e2eutil.WaitForReadyNodesByLabels(t, ctx, hcClient, hc.Spec.Platform.Type, 1, nodeLabels)
			GinkgoWriter.Println("Karpenter consolidated the extra node")

			// t3.xlarge = 4 vCPUs; 1 node = 4 Karpenter vCPUs on top of baseline
			waitForAutoNodeStatusVCPUs(ctx, tc.MgmtClient, hc, 4)
			waitForAutoNodeStatusVCPUsStable(ctx, tc.MgmtClient, hc, 4, 30*time.Second)
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

func baseNodePool(name, nodeClassName string) *karpenterv1.NodePool {
	return &karpenterv1.NodePool{
		ObjectMeta: metav1.ObjectMeta{Name: name},
		Spec: karpenterv1.NodePoolSpec{
			Disruption: karpenterv1.Disruption{
				// 60s mitigates the risk of consolidation from racing with drift
				// the default setting of 0s can cause replacement nodes to be deleted
				// before the drift drain begins.
				ConsolidateAfter: karpenterv1.MustParseNillableDuration("60s"),
			},
			Template: karpenterv1.NodeClaimTemplate{
				ObjectMeta: karpenterv1.ObjectMeta{
					Labels: map[string]string{
						"hypershift.openshift.io/nodepool-globalps-enabled": "true",
					},
				},
				Spec: karpenterv1.NodeClaimTemplateSpec{
					Requirements: []karpenterv1.NodeSelectorRequirementWithMinValues{
						{Key: "node.kubernetes.io/instance-type", Operator: corev1.NodeSelectorOpIn, Values: []string{"t3.xlarge"}},
						{Key: karpenterv1.CapacityTypeLabelKey, Operator: corev1.NodeSelectorOpIn, Values: []string{karpenterv1.CapacityTypeOnDemand}},
					},
					NodeClassRef: &karpenterv1.NodeClassReference{
						Group: "karpenter.k8s.aws",
						Kind:  "EC2NodeClass",
						Name:  nodeClassName,
					},
				},
			},
		},
	}
}

func testWorkload(name string, replicas int32, nodeSelector map[string]string) *appsv1.Deployment {
	return testWorkloadWithImage(name, replicas, nodeSelector, "quay.io/openshift/origin-pod:4.22.0")
}

func testWorkloadWithImage(name string, replicas int32, nodeSelector map[string]string, image string) *appsv1.Deployment {
	appLabel := map[string]string{"app": name}
	return &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: "default",
		},
		Spec: appsv1.DeploymentSpec{
			Replicas: ptr.To(replicas),
			Selector: &metav1.LabelSelector{MatchLabels: appLabel},
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{Labels: appLabel},
				Spec: corev1.PodSpec{
					Affinity: &corev1.Affinity{
						PodAntiAffinity: &corev1.PodAntiAffinity{
							RequiredDuringSchedulingIgnoredDuringExecution: []corev1.PodAffinityTerm{{
								LabelSelector: &metav1.LabelSelector{MatchLabels: appLabel},
								TopologyKey:   "kubernetes.io/hostname",
							}},
						},
					},
					SecurityContext: &corev1.PodSecurityContext{
						RunAsUser:  ptr.To(int64(1000)),
						RunAsGroup: ptr.To(int64(3000)),
						FSGroup:    ptr.To(int64(2000)),
					},
					Containers: []corev1.Container{{
						Name:  name,
						Image: image,
						Resources: corev1.ResourceRequirements{
							Requests: corev1.ResourceList{
								corev1.ResourceCPU:    resource.MustParse("250m"),
								corev1.ResourceMemory: resource.MustParse("256M"),
							},
						},
						SecurityContext: &corev1.SecurityContext{
							AllowPrivilegeEscalation: ptr.To(false),
						},
						Command: []string{"/bin/sh", "-c", "sleep infinity"},
					}},
					NodeSelector: nodeSelector,
				},
			},
		},
	}
}

func expectedPlatformTags(hc *hyperv1.HostedCluster) map[string]string {
	tags := make(map[string]string)
	if hc.Spec.Platform.AWS == nil {
		return tags
	}
	for _, tag := range hc.Spec.Platform.AWS.ResourceTags {
		restricted := false
		for _, pattern := range awskarpenterv1.RestrictedTagPatterns {
			if pattern.MatchString(tag.Key) {
				restricted = true
				break
			}
		}
		if !restricted {
			tags[tag.Key] = tag.Value
		}
	}
	return tags
}

func newEC2Client(awsCredsFile, region string) *ec2.Client {
	awsSession := awsutil.NewSession(context.Background(), "hypershift-e2e", awsCredsFile, "", "", region)
	awsConfig := awsutil.NewConfig()
	return ec2.NewFromConfig(*awsSession, func(o *ec2.Options) {
		o.Retryer = awsConfig()
	})
}

func describeEC2Instance(ctx context.Context, ec2client *ec2.Client, node corev1.Node) (ec2types.Instance, string) {
	providerID := node.Spec.ProviderID
	Expect(providerID).NotTo(BeEmpty(), "node should have a providerID")

	parts := strings.Split(providerID, "/")
	Expect(parts).To(HaveLen(5), "providerID should have 5 parts")
	instanceID := parts[4]

	result, err := ec2client.DescribeInstances(ctx, &ec2.DescribeInstancesInput{
		InstanceIds: []string{instanceID},
	})
	Expect(err).NotTo(HaveOccurred(), "failed to describe EC2 instance %s", instanceID)
	Expect(result.Reservations).NotTo(BeEmpty(), "expected at least one reservation")
	Expect(result.Reservations[0].Instances).NotTo(BeEmpty(), "expected at least one instance")
	return result.Reservations[0].Instances[0], instanceID
}

func waitForReadyKarpenterPods(ctx context.Context, client crclient.Client, nodes []corev1.Node, n int, podLabels map[string]string) {
	t := GinkgoTB()
	pods := &corev1.PodList{}
	e2eutil.EventuallyObjects(t, ctx, "Pods to be scheduled on provisioned Karpenter nodes",
		func(ctx context.Context) ([]*corev1.Pod, error) {
			err := client.List(ctx, pods, crclient.InNamespace("default"), crclient.MatchingLabels(podLabels))
			items := make([]*corev1.Pod, len(pods.Items))
			for i := range pods.Items {
				items[i] = &pods.Items[i]
			}
			return items, err
		},
		[]e2eutil.Predicate[[]*corev1.Pod]{
			func(pods []*corev1.Pod) (bool, string, error) {
				want, got := n, len(pods)
				return want == got, fmt.Sprintf("expected %d pods, got %d", want, got), nil
			},
		},
		[]e2eutil.Predicate[*corev1.Pod]{
			e2eutil.ConditionPredicate[*corev1.Pod](e2eutil.Condition{
				Type:   string(corev1.PodScheduled),
				Status: metav1.ConditionTrue,
			}),
			func(pod *corev1.Pod) (bool, string, error) {
				nodeName := pod.Spec.NodeName
				for _, node := range nodes {
					if nodeName == node.Name {
						return true, fmt.Sprintf("pod %s correctly scheduled on node %s", pod.Name, nodeName), nil
					}
				}
				return false, fmt.Sprintf("pod %s scheduled on unexpected node %s", pod.Name, nodeName), nil
			},
			func(pod *corev1.Pod) (bool, string, error) {
				return pod.Status.Phase == corev1.PodRunning, fmt.Sprintf("pod %s is not running", pod.Name), nil
			},
		},
		e2eutil.WithTimeout(20*time.Minute),
	)
}

// waitForAutoNodeStatusVCPUs polls until HostedCluster.Status.AutoNode.VCPUs
// converges to the expected value. This checks only the status field (Karpenter-only vCPUs),
// not the billing metric.
func waitForAutoNodeStatusVCPUs(ctx context.Context, mgtClient crclient.Client, hostedCluster *hyperv1.HostedCluster, expected int32) {
	t := GinkgoTB()

	GinkgoWriter.Printf("Validating AutoNode.VCPUs converges to %d", expected)
	e2eutil.EventuallyObject(t, ctx,
		fmt.Sprintf("HostedCluster %s/%s AutoNode.VCPUs=%d", hostedCluster.Namespace, hostedCluster.Name, expected),
		func(ctx context.Context) (*hyperv1.HostedCluster, error) {
			hc := &hyperv1.HostedCluster{}
			err := mgtClient.Get(ctx, crclient.ObjectKeyFromObject(hostedCluster), hc)
			return hc, err
		},
		[]e2eutil.Predicate[*hyperv1.HostedCluster]{
			func(hc *hyperv1.HostedCluster) (bool, string, error) {
				if hc.Status.AutoNode.VCPUs == nil {
					return false, "AutoNode.VCPUs is nil", nil
				}
				actual := *hc.Status.AutoNode.VCPUs
				if actual != expected {
					return false, fmt.Sprintf("AutoNode.VCPUs=%d, want %d", actual, expected), nil
				}
				return true, fmt.Sprintf("AutoNode.VCPUs=%d", actual), nil
			},
		},
		e2eutil.WithTimeout(1*time.Minute),
	)
}

func waitForAutoNodeStatusVCPUsStable(ctx context.Context, mgtClient crclient.Client, hostedCluster *hyperv1.HostedCluster, expected int32, duration time.Duration) {
	Consistently(func(g Gomega) {
		hc := &hyperv1.HostedCluster{}
		g.Expect(mgtClient.Get(ctx, crclient.ObjectKeyFromObject(hostedCluster), hc)).To(Succeed())
		g.Expect(hc.Status.AutoNode.VCPUs).NotTo(BeNil(), "AutoNode.VCPUs became nil")
		g.Expect(*hc.Status.AutoNode.VCPUs).To(Equal(expected))
	}).WithTimeout(duration).WithPolling(2 * time.Second).Should(Succeed())
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
