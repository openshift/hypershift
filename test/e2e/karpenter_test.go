//go:build e2e

package e2e

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"maps"
	"os"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/ec2"
	ec2types "github.com/aws/aws-sdk-go-v2/service/ec2/types"
	awskarpenterv1 "github.com/aws/karpenter-provider-aws/pkg/apis/v1"
	"github.com/blang/semver"
	. "github.com/onsi/gomega"
	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	hyperkarpenterv1 "github.com/openshift/hypershift/api/karpenter/v1beta1"
	karpentercpov2 "github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/v2/karpenter"
	karpenteroperatorcpov2 "github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/v2/karpenteroperator"
	"github.com/openshift/hypershift/hypershift-operator/controllers/manifests"
	karpenterassets "github.com/openshift/hypershift/karpenter-operator/controllers/karpenter/assets"
	karpenterutil "github.com/openshift/hypershift/support/karpenter"
	"github.com/openshift/hypershift/support/releaseinfo"
	"github.com/openshift/hypershift/support/supportedversion"
	e2eutil "github.com/openshift/hypershift/test/e2e/util"
	dto "github.com/prometheus/client_model/go"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	policyv1 "k8s.io/api/policy/v1"
	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/util/intstr"
	"k8s.io/apimachinery/pkg/util/wait"
	kubeclient "k8s.io/client-go/kubernetes"
	"k8s.io/utils/ptr"
	crclient "sigs.k8s.io/controller-runtime/pkg/client"
	karpenterv1 "sigs.k8s.io/karpenter/pkg/apis/v1"
)

func TestKarpenter(t *testing.T) {
	e2eutil.AtLeast(t, e2eutil.Version419)
	if os.Getenv("TECH_PREVIEW_NO_UPGRADE") != "true" {
		t.Skipf("Only tested when CI sets TECH_PREVIEW_NO_UPGRADE=true and the Hypershift Operator is installed with --tech-preview-no-upgrade")
	}
	if globalOpts.Platform != hyperv1.AWSPlatform {
		t.Skip("test only supported on platform AWS")
	}
	t.Parallel()

	ctx, cancel := context.WithCancel(testContext)
	defer cancel()

	clusterOpts := globalOpts.DefaultClusterOptions(t)
	clusterOpts.AWSPlatform.AutoNode = true
	clusterOpts.AWSPlatform.PublicOnly = false
	clusterOpts.AWSPlatform.EndpointAccess = string(hyperv1.PublicAndPrivate)

	e2eutil.NewHypershiftTest(t, ctx, func(t *testing.T, g Gomega, mgtClient crclient.Client, hostedCluster *hyperv1.HostedCluster) {
		guestClient := e2eutil.WaitForGuestClient(t, ctx, mgtClient, hostedCluster)
		awsCredsFile := clusterOpts.AWSPlatform.Credentials.AWSCredentialsFile
		awsRegion := clusterOpts.AWSPlatform.Region
		pullSecretFile := clusterOpts.PullSecretFile

		t.Run("Karpenter operator plumbing and smoketesting", testKarpenterPlumbing(ctx, mgtClient, guestClient, hostedCluster))

		// Parallel subtests that provision nodes must create their own OpenshiftEC2NodeClass
		// rather than using the "default" class, because the instance-profile test mutates
		// the default EC2NodeClass which would trigger NodeClassDrift on any NodeClaims referencing it.
		t.Run("Parallel provisioning tests", func(t *testing.T) {
			t.Run("ARM64 instance provisioning", testARM64Provisioning(ctx, guestClient, hostedCluster))
			t.Run("Instance profile annotation propagation", testInstanceProfileAnnotation(ctx, mgtClient, guestClient, hostedCluster, awsCredsFile, awsRegion))
			t.Run("OpenshiftEC2NodeClass version field and MetadataOptions", testNodeClassVersionField(ctx, mgtClient, guestClient, hostedCluster, awsCredsFile, awsRegion, pullSecretFile))
			t.Run("Capacity reservation selector propagation", testCapacityReservation(ctx, mgtClient, guestClient, hostedCluster, awsCredsFile, awsRegion))
			t.Run("Arbitrary subnet propagation", testArbitrarySubnet(ctx, mgtClient, guestClient, hostedCluster, awsCredsFile, awsRegion))
		})

		t.Run("AutoNode enable/disable lifecycle", testAutoNodeLifecycle(ctx, mgtClient, hostedCluster))

		// This test intentionally leaves dangling resources so cluster teardown must
		// force-terminate nodes despite a blocking PDB. It must run last.
		t.Run("Karpenter consolidation and cluster deletion with blocking PDB", testConsolidationAndPDB(ctx, guestClient, hostedCluster))
	}).Execute(&clusterOpts, globalOpts.Platform, globalOpts.ArtifactDir, "karpenter", globalOpts.ServiceAccountSigningKey)
}

func testKarpenterPlumbing(ctx context.Context, mgtClient, guestClient crclient.Client, hostedCluster *hyperv1.HostedCluster) func(t *testing.T) {
	return func(t *testing.T) {
		g := NewWithT(t)

		karpenterMetrics := []string{
			karpenterassets.KarpenterBuildInfoMetricName,
			karpenterassets.KarpenterOperatorInfoMetricName,
		}
		operatorComponentName := karpenteroperatorcpov2.ComponentName
		karpenterComponentName := karpentercpov2.ComponentName
		karpenterNamespace := manifests.HostedControlPlaneNamespace(hostedCluster.Namespace, hostedCluster.Name)

		t.Log("Checking Karpenter metrics are exposed")
		err := wait.PollUntilContextTimeout(ctx, 10*time.Second, 5*time.Minute, true, func(ctx context.Context) (bool, error) {
			kmf, err := e2eutil.GetMetricsFromPod(ctx, mgtClient, karpenterComponentName, karpenterComponentName, karpenterNamespace, "8080")
			if err != nil {
				t.Logf("unable to get karpenter metrics: %v", err)
				return false, nil
			}
			komf, err := e2eutil.GetMetricsFromPod(ctx, mgtClient, operatorComponentName, operatorComponentName, karpenterNamespace, "8080")
			if err != nil {
				t.Logf("unable to get karpenter-operator metrics: %v", err)
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

			t.Logf("Expected metrics are exposed: %v", karpenterMetrics)
			return true, nil
		})
		g.Expect(err).NotTo(HaveOccurred(), "failed to validate Karpenter metrics")

		t.Log("Checking Karpenter version is logged")
		cfg, err := e2eutil.GetConfig()
		g.Expect(err).NotTo(HaveOccurred(), "failed to get client config")
		k8sClient := kubeclient.NewForConfigOrDie(cfg)

		karpenterPodList := &corev1.PodList{}
		g.Expect(mgtClient.List(ctx, karpenterPodList,
			crclient.InNamespace(karpenterNamespace),
			crclient.MatchingLabels{"app": karpenterComponentName})).To(Succeed())
		g.Expect(karpenterPodList.Items).NotTo(BeEmpty(), "karpenter pods should exist")

		versionFound := false
		var foundVersion string
		for _, pod := range karpenterPodList.Items {
			if found, version := checkKarpenterVersionInLogs(ctx, k8sClient, &pod, karpenterComponentName, t); found {
				versionFound = true
				foundVersion = version
				break
			}
		}
		g.Expect(versionFound).To(BeTrue(), "karpenter version should be present in logs")
		t.Logf("Karpenter version found in logs: %s", foundVersion)

		t.Log("Validating Karpenter CRDs are installed")
		expectedCRDs := []string{
			"ec2nodeclasses.karpenter.k8s.aws",
			"openshiftec2nodeclasses.karpenter.hypershift.openshift.io",
			"nodepools.karpenter.sh",
			"nodeclaims.karpenter.sh",
		}
		for _, crdName := range expectedCRDs {
			e2eutil.EventuallyObject(t, ctx, fmt.Sprintf("CRD %s to exist in the guest cluster", crdName),
				func(ctx context.Context) (*apiextensionsv1.CustomResourceDefinition, error) {
					crd := &apiextensionsv1.CustomResourceDefinition{}
					err := guestClient.Get(ctx, crclient.ObjectKey{Name: crdName}, crd)
					return crd, err
				},
				nil,
				e2eutil.WithTimeout(2*time.Minute),
			)
		}

		t.Log("Validating default OpenshiftEC2NodeClass exists with expected values")
		infraID := hostedCluster.Spec.InfraID
		e2eutil.EventuallyObject(t, ctx, "default OpenshiftEC2NodeClass to have expected spec",
			func(ctx context.Context) (*hyperkarpenterv1.OpenshiftEC2NodeClass, error) {
				nc := &hyperkarpenterv1.OpenshiftEC2NodeClass{}
				err := guestClient.Get(ctx, crclient.ObjectKey{Name: karpenterassets.EC2NodeClassDefault}, nc)
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

		t.Log("Validating corresponding default EC2NodeClass has immutable service-owned fields set")
		e2eutil.EventuallyObject(t, ctx, "EC2NodeClass to have service-owned fields populated",
			func(ctx context.Context) (*awskarpenterv1.EC2NodeClass, error) {
				nc := &awskarpenterv1.EC2NodeClass{}
				err := guestClient.Get(ctx, crclient.ObjectKey{Name: karpenterassets.EC2NodeClassDefault}, nc)
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
					if nc.Spec.Tags["red-hat-clustertype"] != "rosa" {
						return false, fmt.Sprintf("expected tag red-hat-clustertype=rosa, got %v", nc.Spec.Tags["red-hat-clustertype"]), nil
					}
					if nc.Spec.Tags["red-hat-managed"] != "true" {
						return false, fmt.Sprintf("expected tag red-hat-managed=true, got %v", nc.Spec.Tags["red-hat-managed"]), nil
					}
					return true, "default EC2NodeClass has expected fields set", nil
				},
			},
			e2eutil.WithTimeout(1*time.Minute),
		)

		t.Log("Validating admin cannot delete EC2NodeClass directly")
		ec2NodeClass := &awskarpenterv1.EC2NodeClass{}
		g.Expect(guestClient.Get(ctx, crclient.ObjectKey{Name: karpenterassets.EC2NodeClassDefault}, ec2NodeClass)).To(Succeed())
		g.Expect(guestClient.Delete(ctx, ec2NodeClass)).To(MatchError(ContainSubstring("EC2NodeClass resource can't be created/updated/deleted directly, please use OpenshiftEC2NodeClass resource instead")))

		t.Log("Validating admin cannot modify fields on EC2NodeClass directly")
		ec2NodeClassCopy := ec2NodeClass.DeepCopy()
		ec2NodeClassCopy.Spec.AMISelectorTerms = []awskarpenterv1.AMISelectorTerm{{ID: "ami-fake123"}}
		g.Expect(guestClient.Update(ctx, ec2NodeClassCopy)).To(MatchError(ContainSubstring("EC2NodeClass resource can't be created/updated/deleted directly, please use OpenshiftEC2NodeClass resource instead")))

		t.Log("Validating AutoNodeEnabled condition is set on HostedCluster")
		e2eutil.EventuallyObject(t, ctx, fmt.Sprintf("HostedCluster %s/%s to have AutoNodeEnabled condition", hostedCluster.Namespace, hostedCluster.Name),
			func(ctx context.Context) (*hyperv1.HostedCluster, error) {
				hc := &hyperv1.HostedCluster{}
				err := mgtClient.Get(ctx, crclient.ObjectKeyFromObject(hostedCluster), hc)
				return hc, err
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
	}
}

func testARM64Provisioning(ctx context.Context, guestClient crclient.Client, hostedCluster *hyperv1.HostedCluster) func(t *testing.T) {
	return func(t *testing.T) {
		t.Parallel()
		g := NewWithT(t)

		if !globalOpts.ConfigurableClusterOptions.AWSMultiArch && !globalOpts.ConfigurableClusterOptions.AzureMultiArch {
			t.Skip("test only supported on multi-arch clusters")
		}

		armNodeClass := &hyperkarpenterv1.OpenshiftEC2NodeClass{
			ObjectMeta: metav1.ObjectMeta{Name: "arm-nodeclass"},
			Spec: hyperkarpenterv1.OpenshiftEC2NodeClassSpec{
				SubnetSelectorTerms: []hyperkarpenterv1.SubnetSelectorTerm{
					{Tags: map[string]string{"karpenter.sh/discovery": hostedCluster.Spec.InfraID}},
				},
				SecurityGroupSelectorTerms: []hyperkarpenterv1.SecurityGroupSelectorTerm{
					{Tags: map[string]string{"karpenter.sh/discovery": hostedCluster.Spec.InfraID}},
				},
			},
		}
		g.Expect(guestClient.Create(ctx, armNodeClass)).To(Succeed())
		t.Logf("Created ARM64 OpenshiftEC2NodeClass")

		armNodePool := baseNodePool("arm-nodepool", armNodeClass.Name)
		armNodePool.Spec.Template.Spec.Requirements = []karpenterv1.NodeSelectorRequirementWithMinValues{
			{Key: "node.kubernetes.io/instance-type", Operator: corev1.NodeSelectorOpIn, Values: []string{"m6g.xlarge"}},
			{Key: "kubernetes.io/arch", Operator: corev1.NodeSelectorOpIn, Values: []string{"arm64"}},
			{Key: karpenterv1.CapacityTypeLabelKey, Operator: corev1.NodeSelectorOpIn, Values: []string{karpenterv1.CapacityTypeOnDemand}},
		}
		armWorkLoads := testWorkload("arm-app", 1, map[string]string{karpenterv1.NodePoolLabelKey: armNodePool.Name})

		t.Cleanup(func() {
			_ = guestClient.Delete(ctx, armWorkLoads)
			_ = guestClient.Delete(ctx, armNodePool)
			_ = guestClient.Delete(ctx, armNodeClass)
		})

		g.Expect(guestClient.Create(ctx, armNodePool)).To(Succeed())
		t.Logf("Created ARM64 NodePool")
		g.Expect(guestClient.Create(ctx, armWorkLoads)).To(Succeed())
		t.Logf("Created ARM64 workloads")

		armNodeLabels := map[string]string{
			karpenterv1.NodePoolLabelKey: armNodePool.Name,
			"kubernetes.io/arch":         "arm64",
		}

		nodes := e2eutil.WaitForReadyNodesByLabels(t, ctx, guestClient, hostedCluster.Spec.Platform.Type, 1, armNodeLabels)
		waitForReadyKarpenterPods(t, ctx, guestClient, nodes, 1)

		g.Expect(guestClient.Delete(ctx, armNodePool)).To(Succeed())
		t.Logf("Deleted ARM64 NodePool")
		g.Expect(guestClient.Delete(ctx, armWorkLoads)).To(Succeed())
		t.Logf("Deleted ARM64 workloads")

		t.Logf("Waiting for Karpenter ARM64 Nodes to disappear")
		_ = e2eutil.WaitForReadyNodesByLabels(t, ctx, guestClient, hostedCluster.Spec.Platform.Type, 0, armNodeLabels)
	}
}

func testInstanceProfileAnnotation(ctx context.Context, mgtClient, guestClient crclient.Client, hostedCluster *hyperv1.HostedCluster, awsCredsFile, awsRegion string) func(t *testing.T) {
	return func(t *testing.T) {
		t.Parallel()
		g := NewWithT(t)

		// Get the current HostedCluster
		err := mgtClient.Get(ctx, crclient.ObjectKeyFromObject(hostedCluster), hostedCluster)
		g.Expect(err).NotTo(HaveOccurred())

		// Use the default worker instance profile (typically {infraID}-worker)
		workerInstanceProfile := hostedCluster.Spec.InfraID + "-worker"

		// Apply the annotation to the HostedCluster
		err = e2eutil.UpdateObject(t, ctx, mgtClient, hostedCluster, func(obj *hyperv1.HostedCluster) {
			if obj.Annotations == nil {
				obj.Annotations = make(map[string]string)
			}
			obj.Annotations[hyperv1.AWSKarpenterDefaultInstanceProfile] = workerInstanceProfile

		})
		g.Expect(err).NotTo(HaveOccurred())
		t.Logf("Applied annotation %s=%s to HostedCluster", hyperv1.AWSKarpenterDefaultInstanceProfile, workerInstanceProfile)

		// Wait for the EC2NodeClass to have the InstanceProfile field set
		t.Logf("Waiting for EC2NodeClass to have InstanceProfile set to %s", workerInstanceProfile)
		g.Eventually(func(g Gomega) {
			ec2NodeClass := &awskarpenterv1.EC2NodeClass{}
			err := guestClient.Get(ctx, crclient.ObjectKey{Name: "default"}, ec2NodeClass)
			g.Expect(err).NotTo(HaveOccurred())
			g.Expect(ec2NodeClass.Spec.InstanceProfile).NotTo(BeNil(), "InstanceProfile should be set")
			g.Expect(*ec2NodeClass.Spec.InstanceProfile).To(Equal(workerInstanceProfile))
		}).WithTimeout(2 * time.Minute).WithPolling(5 * time.Second).Should(Succeed())
		t.Logf("EC2NodeClass has InstanceProfile set correctly")

		// Now provision actual nodes to verify EC2 instances get the instance profile
		t.Logf("Creating Karpenter NodePool and workloads to provision nodes")

		testNodePool := baseNodePool("instance-profile-test", "default")
		testWorkLoads := testWorkload("instance-profile-web-app", 1, map[string]string{
			karpenterv1.NodePoolLabelKey: testNodePool.Name,
		})

		g.Expect(guestClient.Create(ctx, testNodePool)).To(Succeed())
		t.Logf("Created Karpenter NodePool")
		g.Expect(guestClient.Create(ctx, testWorkLoads)).To(Succeed())
		t.Logf("Created workloads")
		t.Cleanup(func() {
			_ = guestClient.Delete(ctx, testWorkLoads)
			_ = guestClient.Delete(ctx, testNodePool)
		})

		testNodeLabels := map[string]string{
			karpenterv1.NodePoolLabelKey: testNodePool.Name,
		}

		nodes := e2eutil.WaitForReadyNodesByLabels(t, ctx, guestClient, hostedCluster.Spec.Platform.Type, 1, testNodeLabels)
		t.Logf("Karpenter nodes are ready")

		// Verify EC2 instances have the correct instance profile
		ec2client := ec2Client(awsCredsFile, awsRegion)

		for _, node := range nodes {
			instance, instanceID := describeEC2Instance(ctx, g, ec2client, node)
			t.Logf("Checking instance profile for node %s (instance %s)", node.Name, instanceID)
			g.Expect(instance.IamInstanceProfile).NotTo(BeNil(), "instance should have an IAM instance profile")

			// Extract instance profile name from ARN (format: arn:aws:iam::account-id:instance-profile/profile-name)
			profileArn := *instance.IamInstanceProfile.Arn
			profileParts := strings.Split(profileArn, "/")
			g.Expect(profileParts).To(HaveLen(2), "instance profile ARN should have 2 parts")
			actualInstanceProfile := profileParts[1]

			g.Expect(actualInstanceProfile).To(Equal(workerInstanceProfile),
				"instance %s should have instance profile %s, but has %s", instanceID, workerInstanceProfile, actualInstanceProfile)
			t.Logf("Instance %s has correct instance profile: %s", instanceID, actualInstanceProfile)
		}

		// Wait for nodes to be deleted
		t.Logf("Waiting for Karpenter nodes to be deleted")
		g.Expect(guestClient.Delete(ctx, testWorkLoads)).To(Succeed())
		g.Expect(guestClient.Delete(ctx, testNodePool)).To(Succeed())
		_ = e2eutil.WaitForReadyNodesByLabels(t, ctx, guestClient, hostedCluster.Spec.Platform.Type, 0, testNodeLabels)

		// Remove the annotation and verify it gets cleared from EC2NodeClass
		err = mgtClient.Get(ctx, crclient.ObjectKeyFromObject(hostedCluster), hostedCluster)
		g.Expect(err).NotTo(HaveOccurred())

		err = e2eutil.UpdateObject(t, ctx, mgtClient, hostedCluster, func(obj *hyperv1.HostedCluster) {
			delete(obj.Annotations, hyperv1.AWSKarpenterDefaultInstanceProfile)
		})
		g.Expect(err).NotTo(HaveOccurred())
		t.Logf("Removed annotation %s from HostedCluster", hyperv1.AWSKarpenterDefaultInstanceProfile)

		// Wait for the EC2NodeClass to have InstanceProfile cleared
		t.Logf("Waiting for EC2NodeClass to have InstanceProfile cleared")
		g.Eventually(func(g Gomega) {
			ec2NodeClass := &awskarpenterv1.EC2NodeClass{}
			err := guestClient.Get(ctx, crclient.ObjectKey{Name: "default"}, ec2NodeClass)
			g.Expect(err).NotTo(HaveOccurred())
			g.Expect(ec2NodeClass.Spec.InstanceProfile).To(BeNil(), "InstanceProfile should be cleared")
		}).WithTimeout(2 * time.Minute).WithPolling(5 * time.Second).Should(Succeed())
		t.Logf("EC2NodeClass InstanceProfile cleared successfully")
	}
}

func testNodeClassVersionField(ctx context.Context, mgtClient, guestClient crclient.Client, hostedCluster *hyperv1.HostedCluster, awsCredsFile, awsRegion, pullSecretFile string) func(t *testing.T) {
	return func(t *testing.T) {
		t.Parallel()
		g := NewWithT(t)

		// Re-fetch the hosted cluster to get the latest version status
		err := mgtClient.Get(ctx, crclient.ObjectKeyFromObject(hostedCluster), hostedCluster)
		g.Expect(err).NotTo(HaveOccurred())
		g.Expect(hostedCluster.Status.Version).NotTo(BeNil(), "hostedCluster.Status.Version should not be nil")
		g.Expect(hostedCluster.Status.Version.Desired.Version).NotTo(BeEmpty(), "hostedCluster.Status.Version.Desired.Version should not be empty")

		cpVersion, err := semver.Parse(hostedCluster.Status.Version.Desired.Version)
		g.Expect(err).NotTo(HaveOccurred(), "failed to parse control plane version")
		t.Logf("Control plane version: %s", cpVersion.String())

		// Verify the default OpenshiftEC2NodeClass (version unset) uses the control plane release image
		t.Log("Verifying default OpenshiftEC2NodeClass uses control plane release image")
		e2eutil.EventuallyObject(t, ctx, "default OpenshiftEC2NodeClass to have VersionResolved=True",
			func(ctx context.Context) (*hyperkarpenterv1.OpenshiftEC2NodeClass, error) {
				nc := &hyperkarpenterv1.OpenshiftEC2NodeClass{}
				err := guestClient.Get(ctx, crclient.ObjectKey{Name: "default"}, nc)
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
				e2eutil.Predicate[*hyperkarpenterv1.OpenshiftEC2NodeClass](func(nc *hyperkarpenterv1.OpenshiftEC2NodeClass) (done bool, reasons string, err error) {
					if nc.Status.ReleaseImage == "" {
						return false, "status.releaseImage is empty", nil
					}
					if nc.Status.ReleaseImage != hostedCluster.Spec.Release.Image {
						return false, fmt.Sprintf("expected status.releaseImage %q to match hostedCluster.Spec.Release.Image %q", nc.Status.ReleaseImage, hostedCluster.Spec.Release.Image), nil
					}
					return true, fmt.Sprintf("status.releaseImage matches control plane: %s", nc.Status.ReleaseImage), nil
				}),
			},
			e2eutil.WithTimeout(2*time.Minute),
		)
		t.Log("Default OpenshiftEC2NodeClass has correct version status")

		// Use a previous minor version (n-2) to test a genuinely different version.
		prevMajor, prevMinor, err := supportedversion.PreviousMinorVersion(cpVersion, 2)
		g.Expect(err).NotTo(HaveOccurred())
		nodeClassVersion := fmt.Sprintf("%d.%d.0", prevMajor, prevMinor)

		// Create a custom OpenshiftEC2NodeClass with the version field set to the n-2 previous minor version.
		nc := &hyperkarpenterv1.OpenshiftEC2NodeClass{
			ObjectMeta: metav1.ObjectMeta{
				Name: "version-test",
			},
			Spec: hyperkarpenterv1.OpenshiftEC2NodeClassSpec{
				Version: nodeClassVersion,
				SubnetSelectorTerms: []hyperkarpenterv1.SubnetSelectorTerm{
					{Tags: map[string]string{"karpenter.sh/discovery": hostedCluster.Spec.InfraID}},
				},
				SecurityGroupSelectorTerms: []hyperkarpenterv1.SecurityGroupSelectorTerm{
					{Tags: map[string]string{"karpenter.sh/discovery": hostedCluster.Spec.InfraID}},
				},
				MetadataOptions: hyperkarpenterv1.MetadataOptions{
					Access:                  hyperkarpenterv1.MetadataAccessHTTPEndpoint,
					HTTPIPProtocol:          hyperkarpenterv1.MetadataHTTPProtocolIPv4,
					HTTPPutResponseHopLimit: 2,
					HTTPTokens:              hyperkarpenterv1.MetadataHTTPTokensStateRequired,
				},
			},
		}
		g.Expect(guestClient.Create(ctx, nc)).To(Succeed())
		t.Logf("Created OpenshiftEC2NodeClass %q with version %s (CP version: %s)", nc.Name, nodeClassVersion, cpVersion)
		t.Cleanup(func() {
			_ = guestClient.Delete(ctx, nc)
		})

		// Wait for version resolution and get the resolved release image
		var resolvedReleaseImage string
		t.Log("Waiting for OpenshiftEC2NodeClass version resolution")
		e2eutil.EventuallyObject(t, ctx, "OpenshiftEC2NodeClass version-test to resolve version",
			func(ctx context.Context) (*hyperkarpenterv1.OpenshiftEC2NodeClass, error) {
				result := &hyperkarpenterv1.OpenshiftEC2NodeClass{}
				err := guestClient.Get(ctx, crclient.ObjectKey{Name: nc.Name}, result)
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
				e2eutil.Predicate[*hyperkarpenterv1.OpenshiftEC2NodeClass](func(nc *hyperkarpenterv1.OpenshiftEC2NodeClass) (done bool, reasons string, err error) {
					if nc.Status.ReleaseImage == "" {
						return false, "status.releaseImage is empty", nil
					}
					resolvedReleaseImage = nc.Status.ReleaseImage
					return true, fmt.Sprintf("status.releaseImage resolved to: %s", nc.Status.ReleaseImage), nil
				}),
			},
			e2eutil.WithTimeout(5*time.Minute),
		)
		t.Log("OpenshiftEC2NodeClass version resolved successfully")

		// Verify MetadataOptions propagated to the downstream EC2NodeClass
		t.Log("Verifying MetadataOptions propagated to EC2NodeClass")
		e2eutil.EventuallyObject(t, ctx, "EC2NodeClass to have MetadataOptions propagated",
			func(ctx context.Context) (*awskarpenterv1.EC2NodeClass, error) {
				ec2NodeClass := &awskarpenterv1.EC2NodeClass{}
				err := guestClient.Get(ctx, crclient.ObjectKey{Name: nc.Name}, ec2NodeClass)
				return ec2NodeClass, err
			},
			[]e2eutil.Predicate[*awskarpenterv1.EC2NodeClass]{
				e2eutil.Predicate[*awskarpenterv1.EC2NodeClass](func(ec2nc *awskarpenterv1.EC2NodeClass) (done bool, reasons string, err error) {
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
				}),
			},
			e2eutil.WithTimeout(2*time.Minute),
		)
		t.Log("MetadataOptions propagated correctly to EC2NodeClass")

		// Look up the expected kubelet version from the resolved release image
		pullSecret, err := os.ReadFile(pullSecretFile)
		g.Expect(err).NotTo(HaveOccurred())
		releaseProvider := &releaseinfo.RegistryClientProvider{}
		resolvedRelease, err := releaseProvider.Lookup(ctx, resolvedReleaseImage, pullSecret)
		g.Expect(err).NotTo(HaveOccurred(), "failed to look up resolved release image %s", resolvedReleaseImage)
		componentVersions, err := resolvedRelease.ComponentVersions()
		g.Expect(err).NotTo(HaveOccurred(), "failed to get component versions from resolved release")
		expectedKubeletVersion := componentVersions["kubernetes"]
		g.Expect(expectedKubeletVersion).NotTo(BeEmpty(), "resolved release should have a kubernetes version")
		t.Logf("Expected kubelet version for %s: v%s", nodeClassVersion, expectedKubeletVersion)

		// Create a Karpenter NodePool that references the custom EC2NodeClass
		testNodePool := baseNodePool("version-test", nc.Name)

		testWorkLoads := testWorkload("version-test-app", 1, map[string]string{
			karpenterv1.NodePoolLabelKey: testNodePool.Name,
		})

		g.Expect(guestClient.Create(ctx, testNodePool)).To(Succeed())
		t.Logf("Created Karpenter NodePool %q", testNodePool.Name)
		g.Expect(guestClient.Create(ctx, testWorkLoads)).To(Succeed())
		t.Logf("Created workload %q", testWorkLoads.Name)
		t.Cleanup(func() {
			_ = guestClient.Delete(ctx, testWorkLoads)
			_ = guestClient.Delete(ctx, testNodePool)
		})

		// Use only the nodepool label to select nodes exclusively tied to our version-test nodeclass.
		testNodeLabels := map[string]string{
			karpenterv1.NodePoolLabelKey: testNodePool.GetName(),
		}

		// Log diagnostic info about the version-test NodeClass infrastructure.
		hcpNamespace := manifests.HostedControlPlaneNamespace(hostedCluster.Namespace, hostedCluster.Name)
		secretList := &corev1.SecretList{}
		if err := mgtClient.List(ctx, secretList,
			crclient.InNamespace(hcpNamespace),
			crclient.MatchingLabels{"hypershift.openshift.io/managed-by-karpenter": "true"},
		); err != nil {
			t.Logf("WARNING: failed to list karpenter secrets in %s: %v", hcpNamespace, err)
		} else {
			foundUserData := false
			for _, s := range secretList.Items {
				npAnnotation := s.Annotations["hypershift.openshift.io/nodePool"]
				if strings.Contains(npAnnotation, "version-test") {
					t.Logf("Found karpenter secret %q for nodepool %q (labels: %v)", s.Name, npAnnotation, s.Labels)
					foundUserData = true
				}
			}
			if !foundUserData {
				t.Log("WARNING: no user-data secret found for version-test NodeClass. Token creation may be failing - check karpenter-operator logs.")
			}
		}

		// Wait for node to be provisioned and verify it has the correct kubelet version
		nodes := e2eutil.WaitForNReadyNodesWithOptions(t, ctx, guestClient, 1, hyperv1.AWSPlatform, "",
			e2eutil.WithClientOptions(
				crclient.MatchingLabelsSelector{Selector: labels.SelectorFromSet(labels.Set(testNodeLabels))},
			),
			e2eutil.WithPredicates(
				e2eutil.Predicate[*corev1.Node](func(node *corev1.Node) (done bool, reasons string, err error) {
					kubeletVersion := node.Status.NodeInfo.KubeletVersion
					if !strings.Contains(kubeletVersion, expectedKubeletVersion) {
						return false, fmt.Sprintf("node %s kubelet version %q does not contain expected %q", node.Name, kubeletVersion, expectedKubeletVersion), nil
					}
					return true, fmt.Sprintf("node %s has expected kubelet version %s", node.Name, kubeletVersion), nil
				}),
			),
		)
		t.Logf("Node provisioned with correct kubelet version (v%s) for NodeClass version %s", expectedKubeletVersion, nodeClassVersion)

		// Verify MetadataOptions propagated to the actual EC2 instance
		t.Log("Verifying MetadataOptions on EC2 instance via DescribeInstances")
		ec2client := ec2Client(awsCredsFile, awsRegion)
		for _, node := range nodes {
			instance, instanceID := describeEC2Instance(ctx, g, ec2client, node)
			t.Logf("Checking MetadataOptions for node %s (instance %s)", node.Name, instanceID)
			g.Expect(instance.MetadataOptions).NotTo(BeNil(), "instance should have MetadataOptions")
			g.Expect(string(instance.MetadataOptions.HttpEndpoint)).To(Equal("enabled"),
				"instance %s HttpEndpoint mismatch", instanceID)
			g.Expect(string(instance.MetadataOptions.HttpProtocolIpv6)).To(Equal("disabled"),
				"instance %s HttpProtocolIpv6 mismatch", instanceID)
			g.Expect(*instance.MetadataOptions.HttpPutResponseHopLimit).To(Equal(int32(2)),
				"instance %s HttpPutResponseHopLimit mismatch", instanceID)
			g.Expect(string(instance.MetadataOptions.HttpTokens)).To(Equal("required"),
				"instance %s HttpTokens mismatch", instanceID)
			t.Logf("Instance %s has correct MetadataOptions: HttpTokens=%s, HttpEndpoint=%s, HttpPutResponseHopLimit=%d",
				instanceID, instance.MetadataOptions.HttpTokens, instance.MetadataOptions.HttpEndpoint, *instance.MetadataOptions.HttpPutResponseHopLimit)
		}

		// Trigger cleanup; the t.Cleanup handles final deletion.
		g.Expect(guestClient.Delete(ctx, testWorkLoads)).To(Succeed())
		g.Expect(guestClient.Delete(ctx, testNodePool)).To(Succeed())

		// Verify that a version exceeding the allowed n-3 skew sets SupportedVersionSkew=False.
		skewMajor, skewMinor, err := supportedversion.PreviousMinorVersion(cpVersion, 4)
		if err != nil {
			t.Fatalf("Cannot compute n-4 skew version: %v", err)
		} else if skewMajor == 4 && skewMinor <= 14 {
			t.Skipf("Skipping version-skew check: computed skew version %d.%d.0 would be at or below MinSupportedVersion (4.14.0)", skewMajor, skewMinor)
		} else {
			skewPatch := 1 // There are cases where x.y.0 doesn't exist, so arbitrarily stick with x.y.1 for test consistency
			skewVersion := fmt.Sprintf("%d.%d.%d", skewMajor, skewMinor, skewPatch)

			skewNC := &hyperkarpenterv1.OpenshiftEC2NodeClass{
				ObjectMeta: metav1.ObjectMeta{
					Name: "version-skew-test",
				},
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
			g.Expect(guestClient.Create(ctx, skewNC)).To(Succeed())
			t.Logf("Created OpenshiftEC2NodeClass %q with version %s (CP version: %s)", skewNC.Name, skewVersion, cpVersion)
			t.Cleanup(func() {
				_ = guestClient.Delete(ctx, skewNC)
				t.Logf("Cleaned up OpenshiftEC2NodeClass %q", skewNC.Name)
			})

			t.Log("Waiting for VersionResolved=True and SupportedVersionSkew=False")
			e2eutil.EventuallyObject(t, ctx, "OpenshiftEC2NodeClass version-skew-test to have SupportedVersionSkew=False",
				func(ctx context.Context) (*hyperkarpenterv1.OpenshiftEC2NodeClass, error) {
					result := &hyperkarpenterv1.OpenshiftEC2NodeClass{}
					err := guestClient.Get(ctx, crclient.ObjectKey{Name: skewNC.Name}, result)
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
					e2eutil.Predicate[*hyperkarpenterv1.OpenshiftEC2NodeClass](func(nc *hyperkarpenterv1.OpenshiftEC2NodeClass) (done bool, reasons string, err error) {
						for _, c := range nc.Status.Conditions {
							if c.Type == hyperkarpenterv1.ConditionTypeSupportedVersionSkew && c.Status == metav1.ConditionFalse {
								if strings.Contains(c.Message, "minor version") {
									return true, fmt.Sprintf("SupportedVersionSkew condition message describes skew issue: %s", c.Message), nil
								}
								return false, fmt.Sprintf("expected SupportedVersionSkew message to mention version skew, got %q", c.Message), nil
							}
						}
						return false, "SupportedVersionSkew=False condition not found", nil
					}),
				},
				e2eutil.WithTimeout(2*time.Minute),
			)
			t.Logf("OpenshiftEC2NodeClass %q has SupportedVersionSkew=False for version %s (exceeds n-3 skew from CP %s)", skewNC.Name, skewVersion, cpVersion)
		}
	}
}

func testCapacityReservation(ctx context.Context, mgtClient, guestClient crclient.Client, hostedCluster *hyperv1.HostedCluster, awsCredsFile, awsRegion string) func(t *testing.T) {
	return func(t *testing.T) {
		t.Parallel()
		g := NewWithT(t)

		// AutoNode.Provisioner.Karpenter.AWS is required at the API level when karpenter
		// is configured, so this should never happen/never be nil for a valid karpenter cluster.
		if hostedCluster.Spec.AutoNode == nil ||
			hostedCluster.Spec.AutoNode.Provisioner.Karpenter == nil ||
			hostedCluster.Spec.AutoNode.Provisioner.Karpenter.AWS == nil {
			t.Skip("HostedCluster does not have a Karpenter AWS role configured, skipping capacity reservation test")
		}

		// Determine an availability zone to use: pick the AZ from the first subnet in the cluster.
		defaultNodeClass := &hyperkarpenterv1.OpenshiftEC2NodeClass{}
		g.Expect(guestClient.Get(ctx, crclient.ObjectKey{Name: "default"}, defaultNodeClass)).To(Succeed())
		g.Expect(defaultNodeClass.Status.Subnets).NotTo(BeEmpty(), "default OpenshiftEC2NodeClass should have resolved subnets")
		targetAZ := defaultNodeClass.Status.Subnets[0].Zone
		t.Logf("Using availability zone %s for capacity reservation", targetAZ)

		// Create a real EC2 capacity reservation with 1 instance of t3.xlarge in targeted mode.
		// We use t3.xlarge to match the instance type used by the other karpenter tests — OpenShift
		// platform daemonsets consume enough overhead that smaller types (t3.small, t3.medium) don't
		// have enough free memory to satisfy karpenter's scheduling check.
		// We need a real reservation because karpenter 1.8 runs with ReservedCapacity=true by default,
		// so selector terms that match nothing would cause CapacityReservationsReady=False on the
		// EC2NodeClass and block provisioning.
		crID, cleanupCR, err := e2eutil.CreateCapacityReservation(
			ctx,
			awsCredsFile,
			awsRegion,
			"t3.xlarge",
			targetAZ,
			1,
		)
		g.Expect(err).NotTo(HaveOccurred(), "failed to create capacity reservation")
		t.Logf("Created capacity reservation %s in %s", crID, targetAZ)
		t.Cleanup(func() {
			if err := cleanupCR(); err != nil {
				t.Logf("warning: failed to cancel capacity reservation %s: %v", crID, err)
			}
		})

		// Create a new OpenshiftEC2NodeClass (not "default") pointing to the capacity reservation by ID.
		// Using a separate object avoids contaminating the shared "default" class used by other sub-tests.
		crNodeClass := &hyperkarpenterv1.OpenshiftEC2NodeClass{
			ObjectMeta: metav1.ObjectMeta{
				Name: "capacity-reservation-test",
			},
			Spec: hyperkarpenterv1.OpenshiftEC2NodeClassSpec{
				CapacityReservationSelectorTerms: []hyperkarpenterv1.CapacityReservationSelectorTerm{
					{ID: crID},
				},
			},
		}
		g.Expect(guestClient.Create(ctx, crNodeClass)).To(Succeed())
		t.Logf("Created OpenshiftEC2NodeClass capacity-reservation-test with CapacityReservationSelectorTerms ID=%s", crID)
		t.Cleanup(func() {
			if err := guestClient.Delete(ctx, crNodeClass); err != nil {
				t.Logf("warning: failed to delete OpenshiftEC2NodeClass capacity-reservation-test: %v", err)
			}
		})

		// Verify the downstream EC2NodeClass has the CapacityReservationSelectorTerms propagated.
		e2eutil.EventuallyObject(
			t, ctx, "EC2NodeClass capacity-reservation-test to have CapacityReservationSelectorTerms set",
			func(ctx context.Context) (*awskarpenterv1.EC2NodeClass, error) {
				ec2nc := &awskarpenterv1.EC2NodeClass{}
				return ec2nc, guestClient.Get(ctx, crclient.ObjectKey{Name: "capacity-reservation-test"}, ec2nc)
			},
			[]e2eutil.Predicate[*awskarpenterv1.EC2NodeClass]{
				func(ec2nc *awskarpenterv1.EC2NodeClass) (done bool, reasons string, err error) {
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
		e2eutil.EventuallyObject(
			t, ctx, fmt.Sprintf("OpenshiftEC2NodeClass capacity-reservation-test to have capacity reservation %s in status", crID),
			func(ctx context.Context) (*hyperkarpenterv1.OpenshiftEC2NodeClass, error) {
				updated := &hyperkarpenterv1.OpenshiftEC2NodeClass{}
				return updated, guestClient.Get(ctx, crclient.ObjectKey{Name: "capacity-reservation-test"}, updated)
			},
			[]e2eutil.Predicate[*hyperkarpenterv1.OpenshiftEC2NodeClass]{
				func(updated *hyperkarpenterv1.OpenshiftEC2NodeClass) (done bool, reasons string, err error) {
					if len(updated.Status.CapacityReservations) > 0 &&
						updated.Status.CapacityReservations[0].ID == crID {
						return true, "", nil
					}
					return false, fmt.Sprintf("expected at least one resolved capacity reservation with ID=%s in status, got %+v",
						crID, updated.Status.CapacityReservations), nil
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
		g.Expect(guestClient.Create(ctx, crNodePool)).To(Succeed())
		t.Logf("Created NodePool capacity-reservation-test targeting capacity reservation %s", crID)
		t.Cleanup(func() {
			if err := guestClient.Delete(ctx, crNodePool); err != nil {
				t.Logf("warning: failed to delete NodePool capacity-reservation-test: %v", err)
			}
		})

		crNodeLabels := map[string]string{
			karpenterv1.NodePoolLabelKey: crNodePool.Name,
		}
		crWorkload := testWorkload("capacity-reservation-web-app", 1, crNodeLabels)

		g.Expect(guestClient.Create(ctx, crWorkload)).To(Succeed())
		t.Cleanup(func() {
			if err := guestClient.Delete(ctx, crWorkload); err != nil {
				t.Logf("warning: failed to delete workload capacity-reservation-web-app: %v", err)
			}
		})
		t.Logf("Created workload capacity-reservation-web-app to trigger node provisioning")

		// Wait for the node to be ready.
		nodes := e2eutil.WaitForReadyNodesByLabels(t, ctx, guestClient, hostedCluster.Spec.Platform.Type, 1, crNodeLabels)
		g.Expect(nodes).To(HaveLen(1))
		t.Logf("Node provisioned by capacity-reservation-test NodePool is ready")

		// Verify the EC2 instance was launched into the capacity reservation.
		ec2client := ec2Client(awsCredsFile, awsRegion)

		instance, instanceID := describeEC2Instance(ctx, g, ec2client, nodes[0])
		t.Logf("Verifying EC2 instance %s was launched into capacity reservation %s", instanceID, crID)
		g.Expect(instance.CapacityReservationId).NotTo(BeNil(), "instance %s should have a CapacityReservationId", instanceID)
		g.Expect(aws.ToString(instance.CapacityReservationId)).To(Equal(crID),
			"instance %s should have been launched into capacity reservation %s", instanceID, crID)
		t.Logf("Instance %s correctly launched into capacity reservation %s", instanceID, crID)
	}
}

func testArbitrarySubnet(ctx context.Context, mgtClient, guestClient crclient.Client, hostedCluster *hyperv1.HostedCluster, awsCredsFile, awsRegion string) func(t *testing.T) {
	return func(t *testing.T) {
		t.Parallel()
		g := NewWithT(t)

		// Get VPC ID and find an AZ that is:
		// (a) supported by the VPC endpoint service (to avoid InvalidParameter), and
		// (b) not already occupied by a VPC subnet (to avoid DuplicateSubnetsInSameZone).
		// This exercises the real scenario: a customer brings a subnet in a new AZ,
		// it propagates to the VPC endpoint, and nodes in that AZ can reach the cluster.
		ec2client := ec2Client(awsCredsFile, awsRegion)
		vpcID := hostedCluster.Spec.Platform.AWS.CloudProviderConfig.VPC
		subnetsOut, err := ec2client.DescribeSubnets(ctx, &ec2.DescribeSubnetsInput{
			Filters: []ec2types.Filter{{Name: aws.String("vpc-id"), Values: []string{vpcID}}},
		})
		g.Expect(err).NotTo(HaveOccurred())
		g.Expect(subnetsOut.Subnets).NotTo(BeEmpty())

		// Collect AZs already occupied by VPC subnets.
		usedAZs := map[string]bool{}
		for _, s := range subnetsOut.Subnets {
			usedAZs[aws.ToString(s.AvailabilityZone)] = true
		}

		// Get the AZs supported by the VPC endpoint service.
		hcpNamespace := manifests.HostedControlPlaneNamespace(hostedCluster.Namespace, hostedCluster.Name)
		esList := &hyperv1.AWSEndpointServiceList{}
		g.Expect(mgtClient.List(ctx, esList, crclient.InNamespace(hcpNamespace))).To(Succeed())
		g.Expect(esList.Items).NotTo(BeEmpty(), "expected at least one AWSEndpointService")

		var endpointServiceName string
		for _, es := range esList.Items {
			if es.Status.EndpointServiceName != "" {
				endpointServiceName = es.Status.EndpointServiceName
				break
			}
		}
		g.Expect(endpointServiceName).NotTo(BeEmpty(), "no AWSEndpointService has an endpoint service name yet")

		svcOut, err := ec2client.DescribeVpcEndpointServices(ctx, &ec2.DescribeVpcEndpointServicesInput{
			ServiceNames: []string{endpointServiceName},
		})
		g.Expect(err).NotTo(HaveOccurred())
		g.Expect(svcOut.ServiceDetails).NotTo(BeEmpty())
		supportedAZs := svcOut.ServiceDetails[0].AvailabilityZones
		t.Logf("VPC endpoint service %s supports AZs: %v", endpointServiceName, supportedAZs)

		// Pick an AZ supported by the endpoint service but not already in the VPC.
		var az string
		for _, supportedAZ := range supportedAZs {
			if !usedAZs[supportedAZ] {
				az = supportedAZ
				break
			}
		}
		g.Expect(az).NotTo(BeEmpty(),
			"no AZ found that is supported by VPC endpoint service %s and not already occupied in VPC %s (supported: %v, used: %v)",
			endpointServiceName, vpcID, supportedAZs, usedAZs)
		t.Logf("Selected AZ %s for test subnet (supported by endpoint service, not in VPC)", az)

		// Create a small test subnet in the VPC.
		subnetID, cleanupSubnet := e2eutil.CreateTestSubnet(ctx, t, ec2client, vpcID, az, hostedCluster.Spec.InfraID)
		t.Logf("Created test subnet %s in AZ %s", subnetID, az)

		// Create an OpenshiftEC2NodeClass that selects the subnet by ID.
		customNodeClass := &hyperkarpenterv1.OpenshiftEC2NodeClass{
			ObjectMeta: metav1.ObjectMeta{Name: "arbitrary-subnet-test"},
			Spec: hyperkarpenterv1.OpenshiftEC2NodeClassSpec{
				SubnetSelectorTerms: []hyperkarpenterv1.SubnetSelectorTerm{{ID: subnetID}},
				SecurityGroupSelectorTerms: []hyperkarpenterv1.SecurityGroupSelectorTerm{
					{Tags: map[string]string{"karpenter.sh/discovery": hostedCluster.Spec.InfraID}},
				},
			},
		}
		g.Expect(guestClient.Create(ctx, customNodeClass)).To(Succeed())
		t.Cleanup(func() {
			// Delete the NodeClass first so controllers stop referencing the subnet.
			if err := guestClient.Delete(ctx, customNodeClass); err != nil {
				t.Logf("cleanup: failed to delete OpenshiftEC2NodeClass %q: %v", customNodeClass.Name, err)
			}
			// Wait for the subnet to be removed from the karpenter-subnets ConfigMap.
			// The karpenter-operator removes it during NodeClass deletion reconciliation.
			hcpNS := manifests.HostedControlPlaneNamespace(hostedCluster.Namespace, hostedCluster.Name)
			if err := wait.PollUntilContextTimeout(ctx, 5*time.Second, 2*time.Minute, true, func(ctx context.Context) (bool, error) {
				cm := &corev1.ConfigMap{}
				if err := mgtClient.Get(ctx, crclient.ObjectKey{
					Namespace: hcpNS,
					Name:      karpenterutil.KarpenterSubnetsConfigMapName,
				}, cm); err != nil {
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
			}); err != nil {
				t.Logf("cleanup: timed out waiting for subnet %s to leave ConfigMap: %v", subnetID, err)
			} else {
				t.Logf("cleanup: subnet %s removed from karpenter-subnets ConfigMap", subnetID)
			}
			// Wait for the subnet to be removed from all AWSEndpointService.Spec.SubnetIDs.
			// The hypershift-operator watches the ConfigMap and reconciles Spec.SubnetIDs.
			if err := wait.PollUntilContextTimeout(ctx, 5*time.Second, 2*time.Minute, true, func(ctx context.Context) (bool, error) {
				list := &hyperv1.AWSEndpointServiceList{}
				if err := mgtClient.List(ctx, list, crclient.InNamespace(hcpNS)); err != nil {
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
			}); err != nil {
				t.Logf("cleanup: timed out waiting for subnet %s to leave AWSEndpointService specs: %v", subnetID, err)
			} else {
				t.Logf("cleanup: subnet %s removed from all AWSEndpointService specs", subnetID)
			}
			// Wait for AWSEndpointAvailable=True to confirm the CPO has finished
			// reconciling the VPC endpoint (subnet actually removed from AWS).
			if err := wait.PollUntilContextTimeout(ctx, 5*time.Second, 3*time.Minute, true, func(ctx context.Context) (bool, error) {
				list := &hyperv1.AWSEndpointServiceList{}
				if err := mgtClient.List(ctx, list, crclient.InNamespace(hcpNS)); err != nil {
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
			}); err != nil {
				t.Logf("cleanup: timed out waiting for AWSEndpointAvailable=True after subnet removal: %v", err)
			} else {
				t.Logf("cleanup: all AWSEndpointServices have AWSEndpointAvailable=True")
			}
			cleanupSubnet()
		})
		t.Logf("Created OpenshiftEC2NodeClass %q selecting subnet %s", customNodeClass.Name, subnetID)

		// Wait for OpenshiftEC2NodeClass.Status.Subnets to contain the subnet ID.
		t.Logf("Waiting for OpenshiftEC2NodeClass status to reflect subnet %s", subnetID)
		g.Eventually(func(g Gomega) {
			nc := &hyperkarpenterv1.OpenshiftEC2NodeClass{}
			g.Expect(guestClient.Get(ctx, crclient.ObjectKeyFromObject(customNodeClass), nc)).To(Succeed())
			subnetIDs := make([]string, 0, len(nc.Status.Subnets))
			for _, s := range nc.Status.Subnets {
				subnetIDs = append(subnetIDs, s.ID)
			}
			g.Expect(subnetIDs).To(ContainElement(subnetID), "status.subnets should contain the test subnet")
		}).WithTimeout(2 * time.Minute).WithPolling(5 * time.Second).Should(Succeed())
		t.Logf("OpenshiftEC2NodeClass status.subnets contains %s", subnetID)

		// Wait for the karpenter-subnets ConfigMap in the HCP namespace to contain the subnet ID.
		// hcpNamespace was already set above during AZ selection.
		t.Logf("Waiting for karpenter-subnets ConfigMap in %s to contain subnet %s", hcpNamespace, subnetID)
		g.Eventually(func(g Gomega) {
			cm := &corev1.ConfigMap{}
			g.Expect(mgtClient.Get(ctx, crclient.ObjectKey{
				Namespace: hcpNamespace,
				Name:      karpenterutil.KarpenterSubnetsConfigMapName,
			}, cm)).To(Succeed())
			g.Expect(cm.Data).To(HaveKey("subnetIDs"))
			var cmSubnetIDs []string
			g.Expect(json.Unmarshal([]byte(cm.Data["subnetIDs"]), &cmSubnetIDs)).To(Succeed())
			g.Expect(cmSubnetIDs).To(ContainElement(subnetID), "karpenter-subnets ConfigMap should contain the test subnet")
		}).WithTimeout(2 * time.Minute).WithPolling(5 * time.Second).Should(Succeed())
		t.Logf("karpenter-subnets ConfigMap contains subnet %s", subnetID)

		// Wait for any AWSEndpointService in the HCP namespace to include the subnet ID.
		// Which AWSEndpointService resources exist depends on the APIServer publishing
		// strategy: with LoadBalancer publishing, "kube-apiserver-private" is created;
		// with Route publishing (used when ExternalDNS is configured), only
		// "private-router" exists. We check all of them to be independent of the
		// publishing strategy.
		t.Logf("Waiting for any AWSEndpointService in %s to include subnet %s", hcpNamespace, subnetID)
		g.Eventually(func(g Gomega) {
			list := &hyperv1.AWSEndpointServiceList{}
			g.Expect(mgtClient.List(ctx, list, crclient.InNamespace(hcpNamespace))).To(Succeed())
			g.Expect(list.Items).NotTo(BeEmpty(), "expected at least one AWSEndpointService in namespace %s", hcpNamespace)
			found := false
			for _, es := range list.Items {
				for _, id := range es.Spec.SubnetIDs {
					if id == subnetID {
						t.Logf("AWSEndpointService %q includes subnet %s", es.Name, subnetID)
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
		t.Logf("Waiting for AWSEndpointAvailable=True on all AWSEndpointServices in %s", hcpNamespace)
		g.Eventually(func(g Gomega) {
			list := &hyperv1.AWSEndpointServiceList{}
			g.Expect(mgtClient.List(ctx, list, crclient.InNamespace(hcpNamespace))).To(Succeed())
			for _, es := range list.Items {
				available := false
				for _, cond := range es.Status.Conditions {
					if cond.Type == string(hyperv1.AWSEndpointAvailable) {
						g.Expect(cond.Status).To(Equal(metav1.ConditionTrue),
							"AWSEndpointService %q has AWSEndpointAvailable=%s: %s",
							es.Name, cond.Status, cond.Message)
						available = true
						break
					}
				}
				g.Expect(available).To(BeTrue(),
					"AWSEndpointService %q has no AWSEndpointAvailable condition", es.Name)
			}
		}).WithTimeout(5 * time.Minute).WithPolling(10 * time.Second).Should(Succeed())
		t.Logf("All AWSEndpointServices have AWSEndpointAvailable=True")

		// Launch a node in the custom subnet to verify it's functional.
		testNodePool := baseNodePool("arbitrary-subnet-test", customNodeClass.Name)

		testWorkLoads := testWorkload("arbitrary-subnet-web-app", 1, map[string]string{
			karpenterv1.NodePoolLabelKey: testNodePool.Name,
		})

		g.Expect(guestClient.Create(ctx, testNodePool)).To(Succeed())
		t.Logf("Created Karpenter NodePool %q", testNodePool.Name)
		g.Expect(guestClient.Create(ctx, testWorkLoads)).To(Succeed())
		t.Logf("Created workload %q", testWorkLoads.Name)
		t.Cleanup(func() {
			_ = guestClient.Delete(ctx, testWorkLoads)
			_ = guestClient.Delete(ctx, testNodePool)
		})

		testNodeLabels := map[string]string{
			karpenterv1.NodePoolLabelKey: testNodePool.Name,
		}
		nodes := e2eutil.WaitForReadyNodesByLabels(t, ctx, guestClient, hostedCluster.Spec.Platform.Type, 1, testNodeLabels)
		t.Logf("Node launched in arbitrary subnet, verifying it used subnet %s", subnetID)

		// Verify the launched node's EC2 instance is in the expected subnet.
		for _, node := range nodes {
			instance, instanceID := describeEC2Instance(ctx, g, ec2client, node)
			g.Expect(aws.ToString(instance.SubnetId)).To(Equal(subnetID),
				"instance %s should be in subnet %s", instanceID, subnetID)
			t.Logf("Instance %s confirmed in subnet %s", instanceID, subnetID)
		}

		// Trigger cleanup; the t.Cleanup handles final subnet removal.
		// No need to wait for node deprovisioning — subsequent tests use isolated NodePools.
		g.Expect(guestClient.Delete(ctx, testWorkLoads)).To(Succeed())
		g.Expect(guestClient.Delete(ctx, testNodePool)).To(Succeed())
	}
}

func testAutoNodeLifecycle(ctx context.Context, mgtClient crclient.Client, hostedCluster *hyperv1.HostedCluster) func(t *testing.T) {
	return func(t *testing.T) {
		g := NewWithT(t)

		// Refresh to get current spec (including AutoNode config with RoleARN).
		err := mgtClient.Get(ctx, crclient.ObjectKeyFromObject(hostedCluster), hostedCluster)
		g.Expect(err).NotTo(HaveOccurred())
		savedAutoNode := hostedCluster.Spec.AutoNode

		// Disable Karpenter.
		t.Log("Disabling AutoNode (Karpenter) on HostedCluster")
		err = e2eutil.UpdateObject(t, ctx, mgtClient, hostedCluster, func(obj *hyperv1.HostedCluster) {
			obj.Spec.AutoNode = nil
		})
		g.Expect(err).NotTo(HaveOccurred(), "failed to disable AutoNode")

		// Note: we do NOT poll for AutoNodeProgressing during disable. The disable path completes
		// in a single reconcile loop (~<1s), which is shorter than our poll interval (3s), making
		// the transient Progressing state unreliably catchable. Go straight to the final state.

		// Expect fully disabled (components removed).
		t.Log("Waiting for AutoNodeEnabled=False/AutoNodeNotConfigured (disable complete)")
		e2eutil.EventuallyObject(t, ctx, fmt.Sprintf("HostedCluster %s/%s to have AutoNodeEnabled=False/AutoNodeNotConfigured", hostedCluster.Namespace, hostedCluster.Name),
			func(ctx context.Context) (*hyperv1.HostedCluster, error) {
				hc := &hyperv1.HostedCluster{}
				err := mgtClient.Get(ctx, crclient.ObjectKeyFromObject(hostedCluster), hc)
				return hc, err
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
		t.Log("Re-enabling AutoNode (Karpenter) on HostedCluster")
		err = e2eutil.UpdateObject(t, ctx, mgtClient, hostedCluster, func(obj *hyperv1.HostedCluster) {
			obj.Spec.AutoNode = savedAutoNode
		})
		g.Expect(err).NotTo(HaveOccurred(), "failed to re-enable AutoNode")

		// Expect progressing (enable in flight — components being created/rolled out).
		t.Log("Waiting for AutoNodeEnabled=False/AutoNodeProgressing (enable in progress)")
		e2eutil.EventuallyObject(t, ctx, fmt.Sprintf("HostedCluster %s/%s to have AutoNodeEnabled=False/AutoNodeProgressing", hostedCluster.Namespace, hostedCluster.Name),
			func(ctx context.Context) (*hyperv1.HostedCluster, error) {
				hc := &hyperv1.HostedCluster{}
				err := mgtClient.Get(ctx, crclient.ObjectKeyFromObject(hostedCluster), hc)
				return hc, err
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
		t.Log("Waiting for AutoNodeEnabled=True/AsExpected (enable complete)")
		e2eutil.EventuallyObject(t, ctx, fmt.Sprintf("HostedCluster %s/%s to have AutoNodeEnabled=True/AsExpected", hostedCluster.Namespace, hostedCluster.Name),
			func(ctx context.Context) (*hyperv1.HostedCluster, error) {
				hc := &hyperv1.HostedCluster{}
				err := mgtClient.Get(ctx, crclient.ObjectKeyFromObject(hostedCluster), hc)
				return hc, err
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
	}
}

func testConsolidationAndPDB(ctx context.Context, guestClient crclient.Client, hostedCluster *hyperv1.HostedCluster) func(t *testing.T) {
	return func(t *testing.T) {
		g := NewWithT(t)

		karpenterNodePool := baseNodePool("on-demand", "default")
		workLoads := testWorkload("web-app", 2, map[string]string{
			karpenterv1.NodePoolLabelKey: karpenterNodePool.Name,
		})
		nodeLabels := map[string]string{
			karpenterv1.NodePoolLabelKey: karpenterNodePool.Name,
		}

		g.Expect(guestClient.Create(ctx, karpenterNodePool)).To(Succeed())
		t.Logf("Created Karpenter NodePool")
		g.Expect(guestClient.Create(ctx, workLoads)).To(Succeed())
		t.Logf("Created workloads with 2 replicas")

		_ = e2eutil.WaitForReadyNodesByLabels(t, ctx, guestClient, hostedCluster.Spec.Platform.Type, 2, nodeLabels)
		t.Logf("Both nodes ready, scaling workload to 1 replica to verify deprovisioning and consolidation")

		err := e2eutil.UpdateObject(t, ctx, guestClient, workLoads, func(obj *appsv1.Deployment) {
			obj.Spec.Replicas = ptr.To(int32(1))
		})
		g.Expect(err).NotTo(HaveOccurred())

		_ = e2eutil.WaitForReadyNodesByLabels(t, ctx, guestClient, hostedCluster.Spec.Platform.Type, 1, nodeLabels)
		t.Logf("Karpenter consolidated the extra node")

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
		g.Expect(guestClient.Create(ctx, pdb)).To(Succeed())
		t.Logf("Created cluster-deletion-blocking PodDisruptionBudget")
	}
}

func waitForReadyKarpenterPods(t *testing.T, ctx context.Context, client crclient.Client, nodes []corev1.Node, n int) []corev1.Pod {
	pods := &corev1.PodList{}
	waitTimeout := 20 * time.Minute
	e2eutil.EventuallyObjects(t, ctx, "Pods to be scheduled on provisioned Karpenter nodes",
		func(ctx context.Context) ([]*corev1.Pod, error) {
			err := client.List(ctx, pods, crclient.InNamespace("default"))
			items := make([]*corev1.Pod, len(pods.Items))
			for i := range pods.Items {
				items[i] = &pods.Items[i]
			}
			return items, err
		},
		[]e2eutil.Predicate[[]*corev1.Pod]{
			func(pods []*corev1.Pod) (done bool, reasons string, err error) {
				want, got := int(n), len(pods)
				return want == got, fmt.Sprintf("expected %d pods, got %d", want, got), nil
			},
		},
		[]e2eutil.Predicate[*corev1.Pod]{
			// wait for the pods to be scheduled
			e2eutil.ConditionPredicate[*corev1.Pod](e2eutil.Condition{
				Type:   string(corev1.PodScheduled),
				Status: metav1.ConditionTrue,
			}),
			// wait for each pod to be scheduled on one of the correct nodes
			e2eutil.Predicate[*corev1.Pod](func(pod *corev1.Pod) (done bool, reasons string, err error) {
				nodeName := pod.Spec.NodeName
				for _, node := range getNodeNames(nodes) {
					if nodeName == node {
						return true, fmt.Sprintf("pod %s correctly scheduled on a specified node %s", pod.Name, nodeName), nil
					}
				}
				return false, fmt.Sprintf("expected pod %s to be scheduled on at least one of these nodes %v, got %s", pod.Name, getNodeNames(nodes), nodeName), nil
			}),
			// wait for the pods to be ready
			e2eutil.Predicate[*corev1.Pod](func(pod *corev1.Pod) (done bool, reasons string, err error) {
				return pod.Status.Phase == corev1.PodRunning, fmt.Sprintf("pod %s is not running", pod.Name), nil
			}),
		},
		e2eutil.WithTimeout(waitTimeout),
	)
	return pods.Items
}

func waitForReadyNodeClaims(t *testing.T, ctx context.Context, client crclient.Client, n int) *karpenterv1.NodeClaimList {
	nodeClaims := &karpenterv1.NodeClaimList{}
	waitTimeout := 5 * time.Minute
	e2eutil.EventuallyObjects(t, ctx, "NodeClaims to be ready",
		func(ctx context.Context) ([]*karpenterv1.NodeClaim, error) {
			err := client.List(ctx, nodeClaims)
			if err != nil {
				return nil, err
			}
			items := make([]*karpenterv1.NodeClaim, 0)
			for i := range nodeClaims.Items {
				items = append(items, &nodeClaims.Items[i])
			}
			return items, nil
		},
		[]e2eutil.Predicate[[]*karpenterv1.NodeClaim]{
			func(claims []*karpenterv1.NodeClaim) (done bool, reasons string, err error) {
				want, got := n, len(claims)
				return want == got, fmt.Sprintf("expected %d NodeClaims, got %d", want, got), nil
			},
		},
		[]e2eutil.Predicate[*karpenterv1.NodeClaim]{
			func(claim *karpenterv1.NodeClaim) (done bool, reasons string, err error) {
				hasLaunched := false
				hasRegistered := false
				hasInitialized := false

				for _, condition := range claim.Status.Conditions {
					if condition.Type == karpenterv1.ConditionTypeLaunched && condition.Status == metav1.ConditionTrue {
						hasLaunched = true
					}
					if condition.Type == karpenterv1.ConditionTypeRegistered && condition.Status == metav1.ConditionTrue {
						hasRegistered = true
					}
					if condition.Type == karpenterv1.ConditionTypeInitialized && condition.Status == metav1.ConditionTrue {
						hasInitialized = true
					}
				}

				if !hasLaunched || !hasRegistered || !hasInitialized {
					return false, fmt.Sprintf("NodeClaim %s not ready: Launched=%v, Registered=%v, Initialized=%v",
						claim.Name, hasLaunched, hasRegistered, hasInitialized), nil
				}
				return true, "", nil
			},
		},
		e2eutil.WithTimeout(waitTimeout),
	)

	return nodeClaims
}

func waitForNodeClaimDrifted(t *testing.T, ctx context.Context, client crclient.Client, nc *karpenterv1.NodeClaim) {
	waitTimeout := 5 * time.Minute
	e2eutil.EventuallyObject(t, ctx, fmt.Sprintf("NodeClaim %s to be drifted", nc.Name),
		func(ctx context.Context) (*karpenterv1.NodeClaim, error) {
			nodeClaim := &karpenterv1.NodeClaim{}
			err := client.Get(ctx, crclient.ObjectKeyFromObject(nc), nodeClaim)
			// make sure that the condition actually exists first
			if err == nil {
				haystack, err := e2eutil.Conditions(nodeClaim)
				if err != nil {
					return nil, err
				}
				for _, condition := range haystack {
					if karpenterv1.ConditionTypeDrifted == condition.Type {
						if condition.Status == metav1.ConditionTrue {
							return nodeClaim, nil
						}
						return nil, fmt.Errorf("condition %s is not True in NodeClaim %s", karpenterv1.ConditionTypeDrifted, nc.Name)
					}
				}
				return nil, fmt.Errorf("condition %s not found in NodeClaim %s", karpenterv1.ConditionTypeDrifted, nc.Name)
			} else {
				return nil, err
			}
		},
		[]e2eutil.Predicate[*karpenterv1.NodeClaim]{
			e2eutil.ConditionPredicate[*karpenterv1.NodeClaim](e2eutil.Condition{
				Type:   karpenterv1.ConditionTypeDrifted,
				Status: metav1.ConditionTrue,
			}),
		},
		e2eutil.WithTimeout(waitTimeout),
	)
}

func baseNodePool(name, nodeClassName string) *karpenterv1.NodePool {
	return &karpenterv1.NodePool{
		ObjectMeta: metav1.ObjectMeta{Name: name},
		Spec: karpenterv1.NodePoolSpec{
			Disruption: karpenterv1.Disruption{
				ConsolidateAfter: karpenterv1.MustParseNillableDuration("0s"),
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
						Image: "quay.io/openshift/origin-pod:4.22.0",
						Resources: corev1.ResourceRequirements{
							Requests: corev1.ResourceList{
								corev1.ResourceCPU:    resource.MustParse("250m"),
								corev1.ResourceMemory: resource.MustParse("256M"),
							},
						},
						SecurityContext: &corev1.SecurityContext{
							AllowPrivilegeEscalation: ptr.To(false),
						},
					}},
					NodeSelector: nodeSelector,
				},
			},
		},
	}
}

// describeEC2Instance extracts the instance ID from a node's providerID and calls
// DescribeInstances to return the full EC2 instance details.
func describeEC2Instance(ctx context.Context, g Gomega, ec2client *ec2.Client, node corev1.Node) (ec2types.Instance, string) {
	providerID := node.Spec.ProviderID
	g.Expect(providerID).NotTo(BeEmpty(), "node should have a providerID")

	parts := strings.Split(providerID, "/")
	g.Expect(parts).To(HaveLen(5), "providerID should have 5 parts")
	instanceID := parts[4]

	result, err := ec2client.DescribeInstances(ctx, &ec2.DescribeInstancesInput{
		InstanceIds: []string{instanceID},
	})
	g.Expect(err).NotTo(HaveOccurred(), "failed to describe EC2 instance %s", instanceID)
	g.Expect(result.Reservations).NotTo(BeEmpty(), "expected at least one reservation")
	g.Expect(result.Reservations[0].Instances).NotTo(BeEmpty(), "expected at least one instance")
	return result.Reservations[0].Instances[0], instanceID
}

// getNodeNames returns the names of the nodes in the list
func getNodeNames(nodes []corev1.Node) []string {
	nodeNames := make([]string, len(nodes))
	for i, node := range nodes {
		nodeNames[i] = node.Name
	}
	return nodeNames
}

// machineOSVersions extracts all machine-os version strings from a release image's ImageStream.
// The release payload may ship multiple RHCOS variants (e.g. rhel-coreos 9.8 and rhel-coreos-10 10.2),
// so ComponentVersions() can't be used — it either errors or picks only one.
func machineOSVersions(releaseImage *releaseinfo.ReleaseImage) []string {
	var versions []string
	for _, tag := range releaseImage.ImageStream.Spec.Tags {
		buildVersions, ok := tag.Annotations["io.openshift.build.versions"]
		if !ok {
			continue
		}
		for _, pair := range strings.Split(buildVersions, ",") {
			parts := strings.SplitN(strings.TrimSpace(pair), "=", 2)
			if len(parts) == 2 && parts[0] == "machine-os" {
				versions = append(versions, parts[1])
			}
		}
	}
	return versions
}

func checkKarpenterVersionInLogs(ctx context.Context, client *kubeclient.Clientset, pod *corev1.Pod, containerName string, t *testing.T) (bool, string) {
	podLogOpts := corev1.PodLogOptions{
		Container: containerName,
	}
	req := client.CoreV1().Pods(pod.Namespace).GetLogs(pod.Name, &podLogOpts)
	podLogs, err := req.Stream(ctx)
	if err != nil {
		t.Logf("couldn't stream pod log; pod namespace: %s, pod name: %s, error: %v", pod.Namespace, pod.Name, err)
		return false, ""
	}
	defer podLogs.Close()

	logBytes, err := io.ReadAll(podLogs)
	if err != nil {
		t.Logf("failed to read pod log; pod namespace: %s, pod name: %s, error: %v", pod.Namespace, pod.Name, err)
		return false, ""
	}

	scanner := bufio.NewScanner(strings.NewReader(string(logBytes)))
	const (
		bufSize          = 256 * 1024
		maxScanTokenSize = 512 * 1024
	)
	buf := make([]byte, bufSize)
	scanner.Buffer(buf, maxScanTokenSize)

	versionRegex := regexp.MustCompile(`"version"\s*:\s*"([^"]+)"`)

	for scanner.Scan() {
		line := scanner.Text()
		if matches := versionRegex.FindStringSubmatch(line); len(matches) > 1 {
			return true, matches[1]
		}
	}

	if err = scanner.Err(); err != nil {
		t.Logf("failed to scan pod log; pod namespace: %s, pod name: %s, error: %v", pod.Namespace, pod.Name, err)
	}

	return false, ""
}
