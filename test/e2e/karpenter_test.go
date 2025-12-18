//go:build e2e

package e2e

import (
	"context"
	"fmt"
	"maps"
	"os"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/openshift/hypershift/hypershift-operator/controllers/manifests"

	"github.com/aws/aws-sdk-go/aws"
	"github.com/aws/aws-sdk-go/service/ec2"
	"github.com/aws/aws-sdk-go/service/iam"
	awskarpenterv1 "github.com/aws/karpenter-provider-aws/pkg/apis/v1"
	. "github.com/onsi/gomega"
	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	hyperkarpenterv1 "github.com/openshift/hypershift/api/karpenter/v1beta1"
	karpentercpov2 "github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/v2/karpenter"
	karpenteroperatorcpov2 "github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/v2/karpenteroperator"
	karpenterassets "github.com/openshift/hypershift/karpenter-operator/controllers/karpenter/assets"
	"github.com/openshift/hypershift/support/releaseinfo"
	e2eutil "github.com/openshift/hypershift/test/e2e/util"
	dto "github.com/prometheus/client_model/go"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/utils/ptr"
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

			// validate admin cannot modify the default OpenshiftEC2NodeClass
			t.Log("Validating default OpenshiftEC2NodeClass is protected from modifications")
			defaultOpenshiftEC2NodeClass := &hyperkarpenterv1.OpenshiftEC2NodeClass{}
			g.Expect(guestClient.Get(ctx, types.NamespacedName{Name: "default"}, defaultOpenshiftEC2NodeClass)).To(Succeed())

			// Store original spec to verify it doesn't change
			originalInstanceProfile := defaultOpenshiftEC2NodeClass.Spec.InstanceProfile

			// Attempt to update the default OpenshiftEC2NodeClass with an instanceProfile
			err = e2eutil.UpdateObject(t, ctx, guestClient, defaultOpenshiftEC2NodeClass, func(obj *hyperkarpenterv1.OpenshiftEC2NodeClass) {
				obj.Spec.InstanceProfile = ptr.To("should-be-rejected")
			})
			g.Expect(err).To(HaveOccurred(), "updating default OpenshiftEC2NodeClass should fail")
			g.Expect(err.Error()).To(ContainSubstring("The 'default' OpenshiftEC2NodeClass is system-managed and cannot be modified"))
			t.Logf("Verified default OpenshiftEC2NodeClass correctly rejected modification: %v", err)

			// Verify the spec remained unchanged
			g.Expect(guestClient.Get(ctx, types.NamespacedName{Name: "default"}, defaultOpenshiftEC2NodeClass)).To(Succeed())
			g.Expect(defaultOpenshiftEC2NodeClass.Spec.InstanceProfile).To(Equal(originalInstanceProfile), "default OpenshiftEC2NodeClass spec should remain unchanged")
			t.Log("Verified default OpenshiftEC2NodeClass spec was not modified")

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

		t.Run("Test custom instance profile", func(t *testing.T) {
			g := NewWithT(t)

			// Use the existing worker instance profile that's already created for the cluster
			workerInstanceProfile := fmt.Sprintf("%s-worker", hostedCluster.Spec.InfraID)
			t.Logf("Using existing worker instance profile: %s", workerInstanceProfile)

			// Get the default OpenshiftEC2NodeClass to copy its selectors
			defaultOpenshiftEC2NodeClass := &hyperkarpenterv1.OpenshiftEC2NodeClass{}
			g.Expect(guestClient.Get(ctx, types.NamespacedName{Name: "default"}, defaultOpenshiftEC2NodeClass)).To(Succeed())

			// Create custom OpenshiftEC2NodeClass with instanceProfile
			customNodeClassName := "custom-worker-profile"
			customOpenshiftEC2NodeClass := &hyperkarpenterv1.OpenshiftEC2NodeClass{
				ObjectMeta: metav1.ObjectMeta{
					Name: customNodeClassName,
				},
				Spec: hyperkarpenterv1.OpenshiftEC2NodeClassSpec{
					InstanceProfile: ptr.To(workerInstanceProfile),
					// Copy all fields from default to ensure nodes can properly join the cluster
					SubnetSelectorTerms:        defaultOpenshiftEC2NodeClass.Spec.SubnetSelectorTerms,
					SecurityGroupSelectorTerms: defaultOpenshiftEC2NodeClass.Spec.SecurityGroupSelectorTerms,
					AssociatePublicIPAddress:   defaultOpenshiftEC2NodeClass.Spec.AssociatePublicIPAddress,
					Tags:                       defaultOpenshiftEC2NodeClass.Spec.Tags,
					BlockDeviceMappings:        defaultOpenshiftEC2NodeClass.Spec.BlockDeviceMappings,
					DetailedMonitoring:         defaultOpenshiftEC2NodeClass.Spec.DetailedMonitoring,
					InstanceStorePolicy:        defaultOpenshiftEC2NodeClass.Spec.InstanceStorePolicy,
					// Note: We're setting InstanceProfile instead of Role
				},
			}
			g.Expect(guestClient.Create(ctx, customOpenshiftEC2NodeClass)).To(Succeed())
			t.Logf("Created custom OpenshiftEC2NodeClass %s with instanceProfile: %s", customNodeClassName, workerInstanceProfile)
			defer guestClient.Delete(ctx, customOpenshiftEC2NodeClass)

			// Verify corresponding EC2NodeClass gets created and synced with Spec.InstanceProfile
			ec2NodeClass := &awskarpenterv1.EC2NodeClass{}
			g.Eventually(func() *string {
				if err := guestClient.Get(ctx, types.NamespacedName{Name: customNodeClassName}, ec2NodeClass); err != nil {
					return nil
				}
				return ec2NodeClass.Spec.InstanceProfile
			}, 2*time.Minute, 5*time.Second).Should(Equal(ptr.To(workerInstanceProfile)), "EC2NodeClass should have instanceProfile set in Spec")
			t.Logf("Verified EC2NodeClass Spec has instanceProfile: %s", workerInstanceProfile)

			// Verify it syncs to EC2NodeClass Status (this is what Karpenter actually uses)
			g.Eventually(func() string {
				if err := guestClient.Get(ctx, types.NamespacedName{Name: customNodeClassName}, ec2NodeClass); err != nil {
					return ""
				}
				return ec2NodeClass.Status.InstanceProfile
			}, 2*time.Minute, 5*time.Second).Should(Equal(workerInstanceProfile), "EC2NodeClass should have instanceProfile set in Status")
			t.Logf("Verified EC2NodeClass Status has instanceProfile: %s", workerInstanceProfile)

			// Create a unique NodePool for this test to avoid conflicts with other tests
			profileTestNodePool := karpenterNodePool.DeepCopy()
			profileTestNodePool.SetName("nodepool-instance-profile")
			profileTestNodePool.SetResourceVersion("")

			// Update NodePool to reference the custom EC2NodeClass
			nodeClassRef := profileTestNodePool.Object["spec"].(map[string]interface{})["template"].(map[string]interface{})["spec"].(map[string]interface{})["nodeClassRef"].(map[string]interface{})
			nodeClassRef["name"] = customNodeClassName
			t.Logf("Updated NodePool to reference custom EC2NodeClass: %s", customNodeClassName)

			// Create unique workload for this test
			profileTestWorkloads := workLoads.DeepCopy()
			profileTestWorkloads.SetName("workload-instance-profile")
			profileTestWorkloads.SetResourceVersion("")
			replicas := 1
			profileTestWorkloads.Object["spec"].(map[string]interface{})["replicas"] = replicas

			defer guestClient.Delete(ctx, profileTestNodePool)
			defer guestClient.Delete(ctx, profileTestWorkloads)
			g.Expect(guestClient.Create(ctx, profileTestNodePool)).To(Succeed())
			t.Logf("Created Karpenter NodePool %s", profileTestNodePool.GetName())
			g.Expect(guestClient.Create(ctx, profileTestWorkloads)).To(Succeed())
			t.Logf("Created workloads")

			// Wait for nodes to be ready using this test's unique NodePool labels
			profileTestNodeLabels := map[string]string{
				"node.kubernetes.io/instance-type": "t3.large",
				"karpenter.sh/nodepool":            profileTestNodePool.GetName(),
			}
			nodes := e2eutil.WaitForReadyNodesByLabels(t, ctx, guestClient, hostedCluster.Spec.Platform.Type, int32(replicas), profileTestNodeLabels)
			t.Logf("Karpenter provisioned %d node(s)", len(nodes))

			// Verify instances have the correct instance profile via AWS API
			ec2Client := e2eutil.GetEC2Client(clusterOpts.AWSPlatform.Credentials.AWSCredentialsFile, clusterOpts.AWSPlatform.Region)
			instanceIDRegex := regexp.MustCompile(`(?P<Provider>.*):///(?P<AZ>.*)/(?P<InstanceID>.*)`)

			for _, node := range nodes {
				providerID := node.Spec.ProviderID
				matches := instanceIDRegex.FindStringSubmatch(providerID)
				g.Expect(matches).NotTo(BeNil(), "providerID should match expected format")

				var instanceID string
				for i, name := range instanceIDRegex.SubexpNames() {
					if name == "InstanceID" {
						instanceID = matches[i]
						break
					}
				}
				g.Expect(instanceID).NotTo(BeEmpty(), "should extract instance ID from providerID")

				t.Logf("Verifying instance %s has instance profile %s", instanceID, workerInstanceProfile)

				// Describe the instance to get its IAM instance profile
				describeOutput, err := ec2Client.DescribeInstances(&ec2.DescribeInstancesInput{
					InstanceIds: []*string{aws.String(instanceID)},
				})
				g.Expect(err).NotTo(HaveOccurred(), "should describe instance")
				g.Expect(describeOutput.Reservations).NotTo(BeEmpty(), "should have at least one reservation")
				g.Expect(describeOutput.Reservations[0].Instances).NotTo(BeEmpty(), "should have at least one instance")

				instance := describeOutput.Reservations[0].Instances[0]
				g.Expect(instance.IamInstanceProfile).NotTo(BeNil(), "instance should have IAM instance profile")

				// Extract just the profile name from the ARN
				// ARN format: arn:aws:iam::123456789012:instance-profile/profile-name
				profileARN := aws.StringValue(instance.IamInstanceProfile.Arn)
				profileName := profileARN[strings.LastIndex(profileARN, "/")+1:]

				g.Expect(profileName).To(Equal(workerInstanceProfile), "instance should have the correct instance profile")
				t.Logf("✓ Instance %s has correct instance profile: %s", instanceID, profileName)
			}

			// Test we can delete both Karpenter NodePool and workloads.
			g.Expect(guestClient.Delete(ctx, profileTestNodePool)).To(Succeed())
			t.Logf("Deleted Karpenter NodePool %s", profileTestNodePool.GetName())
			g.Expect(guestClient.Delete(ctx, profileTestWorkloads)).To(Succeed())
			t.Logf("Deleted workloads")

			// Wait for Karpenter Nodes to go away.
			t.Log("Waiting for Karpenter Nodes to disappear")
			_ = e2eutil.WaitForReadyNodesByLabels(t, ctx, guestClient, hostedCluster.Spec.Platform.Type, 0, profileTestNodeLabels)

			t.Logf("Successfully verified all instances have the correct instance profile")
		})

		t.Run("Test custom role", func(t *testing.T) {
			g := NewWithT(t)

			// Get the existing worker instance profile to extract its role
			// The role name pattern varies depending on whether ROSA managed policies are used
			workerInstanceProfile := fmt.Sprintf("%s-worker", hostedCluster.Spec.InfraID)
			iamClient := e2eutil.GetIAMClient(clusterOpts.AWSPlatform.Credentials.AWSCredentialsFile, clusterOpts.AWSPlatform.Region)
			profileOutput, err := iamClient.GetInstanceProfile(&iam.GetInstanceProfileInput{
				InstanceProfileName: aws.String(workerInstanceProfile),
			})
			g.Expect(err).NotTo(HaveOccurred(), "should get worker instance profile")
			g.Expect(profileOutput.InstanceProfile.Roles).NotTo(BeEmpty(), "worker instance profile should have a role")

			workerRoleName := *profileOutput.InstanceProfile.Roles[0].RoleName
			t.Logf("Using existing worker role from instance profile: %s", workerRoleName)

			// Get the default OpenshiftEC2NodeClass to copy configuration
			defaultOpenshiftEC2NodeClass := &hyperkarpenterv1.OpenshiftEC2NodeClass{}
			g.Expect(guestClient.Get(ctx, types.NamespacedName{Name: "default"}, defaultOpenshiftEC2NodeClass)).To(Succeed())

			// Create custom OpenshiftEC2NodeClass with role
			customNodeClassName := "custom-worker-role"
			customOpenshiftEC2NodeClass := &hyperkarpenterv1.OpenshiftEC2NodeClass{
				ObjectMeta: metav1.ObjectMeta{
					Name: customNodeClassName,
				},
				Spec: hyperkarpenterv1.OpenshiftEC2NodeClassSpec{
					Role: workerRoleName, // Set role instead of instanceProfile
					// Copy all fields from default
					SubnetSelectorTerms:        defaultOpenshiftEC2NodeClass.Spec.SubnetSelectorTerms,
					SecurityGroupSelectorTerms: defaultOpenshiftEC2NodeClass.Spec.SecurityGroupSelectorTerms,
					AssociatePublicIPAddress:   defaultOpenshiftEC2NodeClass.Spec.AssociatePublicIPAddress,
					Tags:                       defaultOpenshiftEC2NodeClass.Spec.Tags,
					BlockDeviceMappings:        defaultOpenshiftEC2NodeClass.Spec.BlockDeviceMappings,
					DetailedMonitoring:         defaultOpenshiftEC2NodeClass.Spec.DetailedMonitoring,
					InstanceStorePolicy:        defaultOpenshiftEC2NodeClass.Spec.InstanceStorePolicy,
				},
			}
			g.Expect(guestClient.Create(ctx, customOpenshiftEC2NodeClass)).To(Succeed())
			t.Logf("Created custom OpenshiftEC2NodeClass %s with role: %s", customNodeClassName, workerRoleName)
			defer guestClient.Delete(ctx, customOpenshiftEC2NodeClass)

			// Verify EC2NodeClass gets created and synced with Role in Spec
			ec2NodeClass := &awskarpenterv1.EC2NodeClass{}
			g.Eventually(func() string {
				if err := guestClient.Get(ctx, types.NamespacedName{Name: customNodeClassName}, ec2NodeClass); err != nil {
					return ""
				}
				return ec2NodeClass.Spec.Role
			}, 2*time.Minute, 5*time.Second).Should(Equal(workerRoleName), "EC2NodeClass should have role set in Spec")
			t.Logf("Verified EC2NodeClass Spec has role: %s", workerRoleName)

			// Verify Karpenter's instanceprofile controller creates an instance profile
			// When using Spec.Role, Karpenter generates a dynamic instance profile name
			var generatedInstanceProfile string
			g.Eventually(func() string {
				if err := guestClient.Get(ctx, types.NamespacedName{Name: customNodeClassName}, ec2NodeClass); err != nil {
					return ""
				}
				return ec2NodeClass.Status.InstanceProfile
			}, 2*time.Minute, 5*time.Second).ShouldNot(BeEmpty(), "EC2NodeClass should have instanceProfile generated in Status")

			generatedInstanceProfile = ec2NodeClass.Status.InstanceProfile
			t.Logf("Verified EC2NodeClass Status has generated instanceProfile: %s", generatedInstanceProfile)

			// The generated instance profile should be DIFFERENT from the default worker profile
			defaultWorkerProfile := fmt.Sprintf("%s-worker", hostedCluster.Spec.InfraID)
			g.Expect(generatedInstanceProfile).NotTo(Equal(defaultWorkerProfile), "Generated instance profile should be different from default")

			// Create a unique NodePool for this test to avoid conflicts with other tests
			roleTestNodePool := karpenterNodePool.DeepCopy()
			roleTestNodePool.SetName("nodepool-custom-role")
			roleTestNodePool.SetResourceVersion("")

			// Update NodePool to reference the custom EC2NodeClass
			nodeClassRef := roleTestNodePool.Object["spec"].(map[string]interface{})["template"].(map[string]interface{})["spec"].(map[string]interface{})["nodeClassRef"].(map[string]interface{})
			nodeClassRef["name"] = customNodeClassName
			t.Logf("Updated NodePool to reference custom EC2NodeClass: %s", customNodeClassName)

			// Create unique workload for this test
			roleTestWorkloads := workLoads.DeepCopy()
			roleTestWorkloads.SetName("workload-custom-role")
			roleTestWorkloads.SetResourceVersion("")
			replicas := 1
			roleTestWorkloads.Object["spec"].(map[string]interface{})["replicas"] = replicas

			defer guestClient.Delete(ctx, roleTestNodePool)
			defer guestClient.Delete(ctx, roleTestWorkloads)
			g.Expect(guestClient.Create(ctx, roleTestNodePool)).To(Succeed())
			t.Logf("Created Karpenter NodePool %s", roleTestNodePool.GetName())
			g.Expect(guestClient.Create(ctx, roleTestWorkloads)).To(Succeed())
			t.Logf("Created workloads")

			// Wait for nodes to be ready using this test's unique NodePool labels
			roleTestNodeLabels := map[string]string{
				"node.kubernetes.io/instance-type": "t3.large",
				"karpenter.sh/nodepool":            roleTestNodePool.GetName(),
			}
			nodes := e2eutil.WaitForReadyNodesByLabels(t, ctx, guestClient, hostedCluster.Spec.Platform.Type, int32(replicas), roleTestNodeLabels)
			t.Logf("Karpenter provisioned %d node(s) using role", len(nodes))

			// Verify instances have the GENERATED instance profile (not the default one)
			ec2Client := e2eutil.GetEC2Client(clusterOpts.AWSPlatform.Credentials.AWSCredentialsFile, clusterOpts.AWSPlatform.Region)
			instanceIDRegex := regexp.MustCompile(`(?P<Provider>.*):///(?P<AZ>.*)/(?P<InstanceID>.*)`)

			for _, node := range nodes {
				providerID := node.Spec.ProviderID
				matches := instanceIDRegex.FindStringSubmatch(providerID)
				g.Expect(matches).NotTo(BeNil(), "providerID should match expected format")

				var instanceID string
				for i, name := range instanceIDRegex.SubexpNames() {
					if name == "InstanceID" {
						instanceID = matches[i]
						break
					}
				}
				g.Expect(instanceID).NotTo(BeEmpty(), "should extract instance ID from providerID")

				t.Logf("Verifying instance %s has generated instance profile %s", instanceID, generatedInstanceProfile)

				// Describe the instance to get its IAM instance profile
				describeOutput, err := ec2Client.DescribeInstances(&ec2.DescribeInstancesInput{
					InstanceIds: []*string{aws.String(instanceID)},
				})
				g.Expect(err).NotTo(HaveOccurred(), "should describe instance")
				g.Expect(describeOutput.Reservations).NotTo(BeEmpty(), "should have reservations")
				g.Expect(describeOutput.Reservations[0].Instances).NotTo(BeEmpty(), "should have instances")

				instance := describeOutput.Reservations[0].Instances[0]
				g.Expect(instance.IamInstanceProfile).NotTo(BeNil(), "instance should have IAM instance profile")

				// Extract instance profile name from ARN
				// ARN format: arn:aws:iam::ACCOUNT:instance-profile/NAME
				profileARN := *instance.IamInstanceProfile.Arn
				parts := strings.Split(profileARN, "/")
				actualProfileName := parts[len(parts)-1]

				g.Expect(actualProfileName).To(Equal(generatedInstanceProfile),
					fmt.Sprintf("instance %s should have instance profile %s (got %s)", instanceID, generatedInstanceProfile, actualProfileName))

				t.Logf("✓ Instance %s has correct generated instance profile: %s", instanceID, actualProfileName)
			}

			// Test we can delete both Karpenter NodePool and workloads.
			g.Expect(guestClient.Delete(ctx, roleTestNodePool)).To(Succeed())
			t.Logf("Deleted Karpenter NodePool")
			g.Expect(guestClient.Delete(ctx, roleTestWorkloads)).To(Succeed())
			t.Logf("Deleted workloads")

			// Wait for Karpenter Nodes to go away.
			t.Log("Waiting for Karpenter Nodes to disappear")
			_ = e2eutil.WaitForReadyNodesByLabels(t, ctx, guestClient, hostedCluster.Spec.Platform.Type, 0, roleTestNodeLabels)
		})

		t.Run("Test basic provisioning and deprovising", func(t *testing.T) {
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
