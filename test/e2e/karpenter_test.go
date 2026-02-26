//go:build e2e

package e2e

import (
	"context"
	"fmt"
	"maps"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go/aws"
	"github.com/aws/aws-sdk-go/service/ec2"
	awskarpenterv1 "github.com/aws/karpenter-provider-aws/pkg/apis/v1"
	"github.com/blang/semver"
	. "github.com/onsi/gomega"
	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	hyperkarpenterv1 "github.com/openshift/hypershift/api/karpenter/v1beta1"
	karpentercpov2 "github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/v2/karpenter"
	karpenteroperatorcpov2 "github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/v2/karpenteroperator"
	"github.com/openshift/hypershift/hypershift-operator/controllers/manifests"
	karpenterassets "github.com/openshift/hypershift/karpenter-operator/controllers/karpenter/assets"
	"github.com/openshift/hypershift/support/releaseinfo"
	e2eutil "github.com/openshift/hypershift/test/e2e/util"
	dto "github.com/prometheus/client_model/go"
	corev1 "k8s.io/api/core/v1"
	policyv1 "k8s.io/api/policy/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/util/intstr"
	"k8s.io/apimachinery/pkg/util/wait"
	crclient "sigs.k8s.io/controller-runtime/pkg/client"
	karpenterv1 "sigs.k8s.io/karpenter/pkg/apis/v1"
	"sigs.k8s.io/yaml"
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
	clusterOpts.ControlPlaneAvailabilityPolicy = string(hyperv1.HighlyAvailable)
	clusterOpts.ReleaseImage = globalOpts.PreviousReleaseImage

	e2eutil.NewHypershiftTest(t, ctx, func(t *testing.T, g Gomega, mgtClient crclient.Client, hostedCluster *hyperv1.HostedCluster) {
		guestClient := e2eutil.WaitForGuestClient(t, ctx, mgtClient, hostedCluster)

		// Unmarshal Karpenter NodePool.
		karpenterNodePool := &unstructured.Unstructured{}
		yamlFile, err := content.ReadFile(fmt.Sprintf("assets/%s", "karpenter-nodepool.yaml"))
		g.Expect(err).NotTo(HaveOccurred())
		err = yaml.Unmarshal(yamlFile, karpenterNodePool)
		g.Expect(err).NotTo(HaveOccurred())

		// Unmarshal workloads.
		workLoads := &unstructured.Unstructured{}
		yamlFile, err = content.ReadFile(fmt.Sprintf("assets/%s", "karpenter-workloads.yaml"))
		g.Expect(err).NotTo(HaveOccurred())
		err = yaml.Unmarshal(yamlFile, workLoads)
		g.Expect(err).NotTo(HaveOccurred())

		nodeLabels := map[string]string{
			"node.kubernetes.io/instance-type": "t3.large",
			"karpenter.sh/nodepool":            karpenterNodePool.GetName(),
		}

		t.Run("Karpenter operator plumbing and smoketesting", func(t *testing.T) {
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

			t.Log("Validating EC2NodeClass")
			ec2NodeClassList := &awskarpenterv1.EC2NodeClassList{}
			g.Expect(guestClient.List(ctx, ec2NodeClassList)).To(Succeed())
			g.Expect(ec2NodeClassList.Items).ToNot(BeEmpty())

			// validate admin cannot delete EC2NodeClass directly
			ec2NodeClass := ec2NodeClassList.Items[0]
			g.Expect(guestClient.Delete(ctx, &ec2NodeClass)).To(MatchError(ContainSubstring("EC2NodeClass resource can't be created/updated/deleted directly, please use OpenshiftEC2NodeClass resource instead")))

			// TODO(alberto): increase coverage:
			// - Karpenter operator plumbing, e.g:
			// -- validate the CRDs are installed
			// -- validate the default class is created and has expected values
			// -- validate admin can't modify fields owned by the service, e.g. ami.
			// - Karpenter functionality:
			//
			// Tracked in https://issues.redhat.com/browse/AUTOSCALE-138
		})

		t.Run("Control plane upgrade and Karpenter Drift", func(t *testing.T) {
			g := NewWithT(t)

			t.Logf("Starting Karpenter control plane upgrade. FromImage: %s, toImage: %s", globalOpts.PreviousReleaseImage, globalOpts.LatestReleaseImage)

			// Lookup os and kubelet versions of the latestReleaseImage
			releaseProvider := &releaseinfo.RegistryClientProvider{}
			pullSecret, err := os.ReadFile(clusterOpts.PullSecretFile)
			g.Expect(err).NotTo(HaveOccurred())

			latestReleaseImage, err := releaseProvider.Lookup(ctx, globalOpts.LatestReleaseImage, pullSecret)
			g.Expect(err).NotTo(HaveOccurred())
			releaseImageComponentVersions, err := latestReleaseImage.ComponentVersions()
			g.Expect(err).NotTo(HaveOccurred())

			expectedRHCOSVersion := releaseImageComponentVersions["machine-os"]
			g.Expect(expectedRHCOSVersion).NotTo(BeEmpty())
			expectedKubeletVersion := releaseImageComponentVersions["kubernetes"]
			g.Expect(expectedKubeletVersion).NotTo(BeEmpty())

			replicas := 1
			workLoads.Object["spec"].(map[string]interface{})["replicas"] = replicas

			// Apply both Karpenter NodePool and workloads.
			defer guestClient.Delete(ctx, karpenterNodePool)
			defer guestClient.Delete(ctx, workLoads)
			g.Expect(guestClient.Create(ctx, karpenterNodePool)).To(Succeed())
			t.Logf("Created Karpenter NodePool")
			g.Expect(guestClient.Create(ctx, workLoads)).To(Succeed())
			t.Logf("Created workloads")

			// Wait for Nodes, NodeClaims and Pods to be ready.
			nodes := e2eutil.WaitForReadyNodesByLabels(t, ctx, guestClient, hostedCluster.Spec.Platform.Type, int32(replicas), nodeLabels)
			nodeClaims := waitForReadyNodeClaims(t, ctx, guestClient, len(nodes))
			waitForReadyKarpenterPods(t, ctx, guestClient, nodes, replicas)

			// Update hosted control plane to induce Drift
			t.Logf("Updating cluster image. Image: %s", globalOpts.LatestReleaseImage)
			err = e2eutil.UpdateObject(t, ctx, mgtClient, hostedCluster, func(obj *hyperv1.HostedCluster) {
				obj.Spec.Release.Image = globalOpts.LatestReleaseImage
				if obj.Annotations == nil {
					obj.Annotations = make(map[string]string)
				}
				obj.Annotations[hyperv1.ForceUpgradeToAnnotation] = globalOpts.LatestReleaseImage
				if globalOpts.DisablePKIReconciliation {
					obj.Annotations[hyperv1.DisablePKIReconciliationAnnotation] = "true"
				}
			})
			g.Expect(err).NotTo(HaveOccurred(), "failed update hostedcluster image")

			// Check that the NodeClaim(s) actually Drift
			driftChan := make(chan struct{})
			go func() {
				defer close(driftChan)
				for _, nodeClaim := range nodeClaims.Items {
					waitForNodeClaimDrifted(t, ctx, guestClient, &nodeClaim)
				}
			}()

			// Wait for the new rollout to be complete
			e2eutil.WaitForImageRollout(t, ctx, mgtClient, hostedCluster)
			err = mgtClient.Get(ctx, crclient.ObjectKeyFromObject(hostedCluster), hostedCluster)
			g.Expect(err).NotTo(HaveOccurred(), "failed to get hostedcluster")

			// Ensure Karpenter Drift behaviour
			<-driftChan
			t.Logf("Karpenter Nodes drifted")

			nodes = e2eutil.WaitForNReadyNodesWithOptions(t, ctx, guestClient, int32(replicas), hyperv1.AWSPlatform, "",
				e2eutil.WithClientOptions(
					crclient.MatchingLabelsSelector{Selector: labels.SelectorFromSet(labels.Set(nodeLabels))},
				),
				e2eutil.WithPredicates(
					e2eutil.ConditionPredicate[*corev1.Node](e2eutil.Condition{
						Type:   string(corev1.NodeReady),
						Status: metav1.ConditionTrue,
					}),
					e2eutil.Predicate[*corev1.Node](func(node *corev1.Node) (done bool, reasons string, err error) {
						fullOSImageString := node.Status.NodeInfo.OSImage

						if !strings.Contains(fullOSImageString, expectedRHCOSVersion) {
							return false, fmt.Sprintf("expected node OS image name %q string to contain expected OS version string %q", fullOSImageString, expectedRHCOSVersion), nil
						}

						return true, fmt.Sprintf("expected OS version string %q, and node.Status.NodeInfo.OSImage is %q", expectedRHCOSVersion, fullOSImageString), nil
					}),
				),
			)

			t.Logf("Waiting for Karpenter pods to schedule on the new node")
			waitForReadyKarpenterPods(t, ctx, guestClient, nodes, replicas)

			// Test we can delete both Karpenter NodePool and workloads.
			g.Expect(guestClient.Delete(ctx, karpenterNodePool)).To(Succeed())
			t.Logf("Deleted Karpenter NodePool")
			g.Expect(guestClient.Delete(ctx, workLoads)).To(Succeed())
			t.Logf("Delete workloads")

			// Wait for Karpenter Nodes to go away.
			t.Logf("Waiting for Karpenter Nodes to disappear")
			_ = e2eutil.WaitForReadyNodesByLabels(t, ctx, guestClient, hostedCluster.Spec.Platform.Type, 0, nodeLabels)
		})

		t.Run("Instance profile annotation propagation", func(t *testing.T) {
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
				ec2NodeClassList := &awskarpenterv1.EC2NodeClassList{}
				err := guestClient.List(ctx, ec2NodeClassList)
				g.Expect(err).NotTo(HaveOccurred())
				g.Expect(ec2NodeClassList.Items).NotTo(BeEmpty())

				// Find the default EC2NodeClass
				var defaultEC2NodeClass *awskarpenterv1.EC2NodeClass
				for i := range ec2NodeClassList.Items {
					if ec2NodeClassList.Items[i].Name == "default" {
						defaultEC2NodeClass = &ec2NodeClassList.Items[i]
						break
					}
				}
				g.Expect(defaultEC2NodeClass).NotTo(BeNil(), "default EC2NodeClass should exist")
				g.Expect(defaultEC2NodeClass.Spec.InstanceProfile).NotTo(BeNil(), "InstanceProfile should be set")
				g.Expect(*defaultEC2NodeClass.Spec.InstanceProfile).To(Equal(workerInstanceProfile))
			}).WithTimeout(2 * time.Minute).WithPolling(5 * time.Second).Should(Succeed())
			t.Logf("EC2NodeClass has InstanceProfile set correctly")

			// Now provision actual nodes to verify EC2 instances get the instance profile
			t.Logf("Creating Karpenter NodePool and workloads to provision nodes")

			// Create copies to avoid polluting shared test objects
			testNodePool := karpenterNodePool.DeepCopy()
			testWorkLoads := workLoads.DeepCopy()
			testNodePool.SetResourceVersion("")
			testWorkLoads.SetResourceVersion("")

			replicas := 1
			testWorkLoads.Object["spec"].(map[string]interface{})["replicas"] = replicas
			testNodePool.SetName("instance-profile-test")
			testWorkLoads.SetName("instance-profile-web-app")

			g.Expect(guestClient.Create(ctx, testNodePool)).To(Succeed())
			t.Logf("Created Karpenter NodePool")
			g.Expect(guestClient.Create(ctx, testWorkLoads)).To(Succeed())
			t.Logf("Created workloads")

			// Ensure cleanup happens even if assertions fail
			defer func() {
				_ = guestClient.Delete(ctx, testWorkLoads)
				_ = guestClient.Delete(ctx, testNodePool)
			}()

			// Update node labels to match the unique NodePool name
			testNodeLabels := map[string]string{
				"node.kubernetes.io/instance-type": "t3.large",
				"karpenter.sh/nodepool":            testNodePool.GetName(),
			}

			// Wait for nodes to be provisioned
			nodes := e2eutil.WaitForReadyNodesByLabels(t, ctx, guestClient, hostedCluster.Spec.Platform.Type, int32(replicas), testNodeLabels)
			t.Logf("Karpenter nodes are ready")

			// Verify EC2 instances have the correct instance profile
			ec2client := ec2Client(clusterOpts.AWSPlatform.Credentials.AWSCredentialsFile, clusterOpts.AWSPlatform.Region)

			for _, node := range nodes {
				// Extract instance ID from providerID (format: aws:///region/instance-id)
				providerID := node.Spec.ProviderID
				g.Expect(providerID).NotTo(BeEmpty(), "node should have a providerID")

				// Parse instance ID from providerID
				parts := strings.Split(providerID, "/")
				g.Expect(parts).To(HaveLen(5), "providerID should have 5 parts")
				instanceID := parts[4]
				t.Logf("Checking instance profile for node %s (instance %s)", node.Name, instanceID)

				// Get EC2 instance details
				result, err := ec2client.DescribeInstancesWithContext(ctx, &ec2.DescribeInstancesInput{
					InstanceIds: []*string{aws.String(instanceID)},
				})
				g.Expect(err).NotTo(HaveOccurred(), "failed to describe EC2 instance")
				g.Expect(result.Reservations).NotTo(BeEmpty(), "expected at least one reservation")
				g.Expect(result.Reservations[0].Instances).NotTo(BeEmpty(), "expected at least one instance")

				instance := result.Reservations[0].Instances[0]
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
		})

		t.Run("OpenshiftEC2NodeClass version unset uses control plane release image", func(t *testing.T) {
			g := NewWithT(t)

			// Re-fetch the hosted cluster to get the latest state
			err := mgtClient.Get(ctx, crclient.ObjectKeyFromObject(hostedCluster), hostedCluster)
			g.Expect(err).NotTo(HaveOccurred())

			t.Log("Waiting for default OpenshiftEC2NodeClass to have VersionResolved and SupportedVersionSkew conditions")
			e2eutil.EventuallyObject(t, ctx, "default OpenshiftEC2NodeClass to have VersionResolved=True and SupportedVersionSkew=True",
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
				e2eutil.WithTimeout(5*time.Minute),
			)
			t.Log("Default OpenshiftEC2NodeClass has correct version status")
		})

		t.Run("OpenshiftEC2NodeClass with custom version provisions nodes", func(t *testing.T) {
			g := NewWithT(t)

			// Re-fetch the hosted cluster to get the latest version status
			err := mgtClient.Get(ctx, crclient.ObjectKeyFromObject(hostedCluster), hostedCluster)
			g.Expect(err).NotTo(HaveOccurred())
			g.Expect(hostedCluster.Status.Version).NotTo(BeNil(), "hostedCluster.Status.Version should not be nil")
			g.Expect(hostedCluster.Status.Version.Desired.Version).NotTo(BeEmpty(), "hostedCluster.Status.Version.Desired.Version should not be empty")

			cpVersion, err := semver.Parse(hostedCluster.Status.Version.Desired.Version)
			g.Expect(err).NotTo(HaveOccurred(), "failed to parse control plane version")
			t.Logf("Control plane version: %s", cpVersion.String())

			// Use previous minor version (e.g., 4.21.0 for CP 4.22.x) to test a genuinely different version.
			// This is within the n-3 skew policy and avoids issues with patch-0 underflow or nightly pre-release identifiers.
			nodeClassVersion := fmt.Sprintf("%d.%d.0", cpVersion.Major, cpVersion.Minor-1)

			// Create a custom OpenshiftEC2NodeClass with the version field set to the previous minor.
			// This exercises the full version resolution path via Cincinnati.
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
				},
			}
			g.Expect(guestClient.Create(ctx, nc)).To(Succeed())
			t.Logf("Created OpenshiftEC2NodeClass %q with version %s (CP version: %s)", nc.Name, nodeClassVersion, cpVersion)
			defer func() {
				_ = guestClient.Delete(ctx, nc)
			}()

			// Wait for version resolution to succeed
			t.Log("Waiting for OpenshiftEC2NodeClass to have VersionResolved=True and SupportedVersionSkew=True")
			e2eutil.EventuallyObject(t, ctx, "OpenshiftEC2NodeClass version-test to have VersionResolved=True and SupportedVersionSkew=True",
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
						return true, fmt.Sprintf("status.releaseImage resolved to: %s", nc.Status.ReleaseImage), nil
					}),
				},
				e2eutil.WithTimeout(5*time.Minute),
			)
			t.Log("OpenshiftEC2NodeClass version resolved successfully")

			// Create a Karpenter NodePool that references the custom EC2NodeClass (created automatically from OpenshiftEC2NodeClass)
			testNodePool := karpenterNodePool.DeepCopy()
			testNodePool.SetResourceVersion("")
			testNodePool.SetName("version-test")
			// Update nodeClassRef to point to our custom NodeClass
			spec := testNodePool.Object["spec"].(map[string]interface{})
			template := spec["template"].(map[string]interface{})
			templateSpec := template["spec"].(map[string]interface{})
			templateSpec["nodeClassRef"] = map[string]interface{}{
				"group": "karpenter.k8s.aws",
				"kind":  "EC2NodeClass",
				"name":  nc.Name,
			}

			// Create workload to trigger provisioning
			testWorkLoads := workLoads.DeepCopy()
			testWorkLoads.SetResourceVersion("")
			testWorkLoads.SetName("version-test-app")
			replicas := 1
			testWorkLoads.Object["spec"].(map[string]interface{})["replicas"] = replicas

			g.Expect(guestClient.Create(ctx, testNodePool)).To(Succeed())
			t.Logf("Created Karpenter NodePool %q", testNodePool.GetName())
			g.Expect(guestClient.Create(ctx, testWorkLoads)).To(Succeed())
			t.Logf("Created workload %q with %d replica(s)", testWorkLoads.GetName(), replicas)

			defer func() {
				_ = guestClient.Delete(ctx, testWorkLoads)
				_ = guestClient.Delete(ctx, testNodePool)
			}()

			testNodeLabels := map[string]string{
				"node.kubernetes.io/instance-type": "t3.large",
				"karpenter.sh/nodepool":            testNodePool.GetName(),
			}

			// Log diagnostic info about the version-test NodeClass infrastructure.
			// This helps debug failures where nodes never join.
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

			// Wait for node to be provisioned
			_ = e2eutil.WaitForReadyNodesByLabels(t, ctx, guestClient, hostedCluster.Spec.Platform.Type, int32(replicas), testNodeLabels)
			t.Log("Node provisioned successfully with custom version NodeClass")

			// Clean up
			g.Expect(guestClient.Delete(ctx, testWorkLoads)).To(Succeed())
			t.Log("Deleted workload")
			g.Expect(guestClient.Delete(ctx, testNodePool)).To(Succeed())
			t.Log("Deleted Karpenter NodePool")
			_ = e2eutil.WaitForReadyNodesByLabels(t, ctx, guestClient, hostedCluster.Spec.Platform.Type, 0, testNodeLabels)
			t.Log("Nodes drained successfully")
		})

		t.Run("OpenshiftEC2NodeClass with version exceeding allowed skew sets SupportedVersionSkew condition", func(t *testing.T) {
			g := NewWithT(t)

			// Re-fetch the hosted cluster to get the latest version status
			err := mgtClient.Get(ctx, crclient.ObjectKeyFromObject(hostedCluster), hostedCluster)
			g.Expect(err).NotTo(HaveOccurred())
			g.Expect(hostedCluster.Status.Version).NotTo(BeNil(), "hostedCluster.Status.Version should not be nil")
			g.Expect(hostedCluster.Status.Version.Desired.Version).NotTo(BeEmpty(), "hostedCluster.Status.Version.Desired.Version should not be empty")

			cpVersion, err := semver.Parse(hostedCluster.Status.Version.Desired.Version)
			g.Expect(err).NotTo(HaveOccurred(), "failed to parse control plane version")

			// Compute a version that exceeds the n-3 skew (cpMinor - 4)
			skewMinor := cpVersion.Minor - 4
			if skewMinor <= 14 {
				t.Skipf("Skipping: computed skew version 4.%d.0 would be at or below MinSupportedVersion (4.14.0), which would be caught by minimum version check instead", skewMinor)
			}
			skewPatch := 1 // There are cases where x.y.0 doesn't exist, so arbitrarily stick with x.y.1 for test consistency
			skewVersion := fmt.Sprintf("%d.%d.%d", cpVersion.Major, skewMinor, skewPatch)

			nc := &hyperkarpenterv1.OpenshiftEC2NodeClass{
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
			g.Expect(guestClient.Create(ctx, nc)).To(Succeed())
			t.Logf("Created OpenshiftEC2NodeClass %q with version %s (CP version: %s)", nc.Name, skewVersion, cpVersion)
			defer func() {
				_ = guestClient.Delete(ctx, nc)
				t.Logf("Cleaned up OpenshiftEC2NodeClass %q", nc.Name)
			}()

			// Version should still resolve successfully despite the skew
			t.Log("Waiting for VersionResolved=True and SupportedVersionSkew=False")
			e2eutil.EventuallyObject(t, ctx, "OpenshiftEC2NodeClass version-skew-test to have SupportedVersionSkew=False",
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
			t.Logf("OpenshiftEC2NodeClass %q has SupportedVersionSkew=False for version %s (exceeds n-3 skew from CP %s)", nc.Name, skewVersion, cpVersion)
		})

		// TODO(jkyros): This test doesn't clean up after itself (I think intentionally) so we can test general cluster
		// cleanup, but as a result it needs to run last, otherwise it will pollute any other cases that come after it
		// and its "on-demand" nodepool may service requests that are not intended for it
		t.Run("Test basic provisioning and de-provisioning", func(t *testing.T) {
			// Test that we can provision as many nodes as needed (in this case, we need 3 nodes for 3 replicas)
			replicas := 3
			workLoads.Object["spec"].(map[string]interface{})["replicas"] = replicas
			workLoads.SetResourceVersion("")
			karpenterNodePool.SetResourceVersion("")

			// Leave dangling resources, and hope the teardown is not blocked, else the test will fail.
			g.Expect(guestClient.Create(ctx, karpenterNodePool)).To(Succeed())
			t.Logf("Created Karpenter NodePool")
			g.Expect(guestClient.Create(ctx, workLoads)).To(Succeed())
			t.Logf("Created workloads")

			// Create a blocking PDB to validate karpenter-operator will terminates stuck nodes forcefully to unblock cluster deletion.
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

			_ = e2eutil.WaitForReadyNodesByLabels(t, ctx, guestClient, hostedCluster.Spec.Platform.Type, int32(replicas), nodeLabels)

		})

	}).Execute(&clusterOpts, globalOpts.Platform, globalOpts.ArtifactDir, "karpenter", globalOpts.ServiceAccountSigningKey)
}

func waitForReadyKarpenterPods(t *testing.T, ctx context.Context, client crclient.Client, nodes []corev1.Node, n int) []corev1.Pod {
	pods := &corev1.PodList{}
	waitTimeout := 20 * time.Minute
	e2eutil.EventuallyObjects(t, ctx, fmt.Sprintf("Pods to be scheduled on provisioned Karpenter nodes"),
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
	e2eutil.EventuallyObjects(t, ctx, fmt.Sprintf("NodeClaims to be ready"),
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

// getNodeNames returns the names of the nodes in the list
func getNodeNames(nodes []corev1.Node) []string {
	nodeNames := make([]string, len(nodes))
	for i, node := range nodes {
		nodeNames[i] = node.Name
	}
	return nodeNames
}
