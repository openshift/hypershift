//go:build e2e

package e2e

import (
	"context"
	"encoding/json"
	"fmt"
	"maps"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go/aws"
	"github.com/aws/aws-sdk-go/service/ec2"
	awskarpenterv1 "github.com/aws/karpenter-provider-aws/pkg/apis/v1"
	. "github.com/onsi/gomega"
	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	hyperkarpenterv1 "github.com/openshift/hypershift/api/karpenter/v1beta1"
	karpentercpov2 "github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/v2/karpenter"
	karpenteroperatorcpov2 "github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/v2/karpenteroperator"
	"github.com/openshift/hypershift/hypershift-operator/controllers/manifests"
	karpenterassets "github.com/openshift/hypershift/karpenter-operator/controllers/karpenter/assets"
	karpenterutil "github.com/openshift/hypershift/support/karpenter"
	"github.com/openshift/hypershift/support/releaseinfo"
	e2eutil "github.com/openshift/hypershift/test/e2e/util"
	dto "github.com/prometheus/client_model/go"
	corev1 "k8s.io/api/core/v1"
	policyv1 "k8s.io/api/policy/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/util/intstr"
	"k8s.io/apimachinery/pkg/util/sets"
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

	// Run first — creates its own PublicAndPrivate cluster, validates the full
	// Karpenter subnet aggregation pipeline, and tears down before the main
	// HighlyAvailable cluster is created. Go runs t.Run subtests sequentially
	// (without t.Parallel()), so this guarantees no resource contention.
	t.Run("Arbitrary subnet propagation", func(t *testing.T) {
		subnetClusterOpts := globalOpts.DefaultClusterOptions(t)
		subnetClusterOpts.AWSPlatform.AutoNode = true
		subnetClusterOpts.ControlPlaneAvailabilityPolicy = string(hyperv1.SingleReplica)
		subnetClusterOpts.NodePoolReplicas = 0
		subnetClusterOpts.AWSPlatform.EndpointAccess = string(hyperv1.PublicAndPrivate)

		e2eutil.NewHypershiftTest(t, ctx, func(t *testing.T, g Gomega, mgtClient crclient.Client, hostedCluster *hyperv1.HostedCluster) {
			t.Logf("Testing Karpenter subnet aggregation for PublicAndPrivate cluster")

			t.Run("ConfigMap subnet aggregation", func(t *testing.T) {
				g := NewWithT(t)

				// Get EC2 client to query AWS resources
				ec2client := ec2Client(subnetClusterOpts.AWSPlatform.Credentials.AWSCredentialsFile, subnetClusterOpts.AWSPlatform.Region)

				hcpNamespace := fmt.Sprintf("%s-%s", hostedCluster.Namespace, hostedCluster.Name)

				// Step 1: Wait for an AWSEndpointService to have a populated EndpointServiceName
				// so we can look up its supported AZs. Both kube-apiserver-private and
				// private-router are backed by NLBs in the same AZs, so either will do.
				t.Logf("Waiting for AWSEndpointService to populate EndpointServiceName in namespace: %s", hcpNamespace)
				var endpointServiceName string
				err := wait.PollUntilContextTimeout(ctx, 5*time.Second, 5*time.Minute, true, func(ctx context.Context) (bool, error) {
					epsList := &hyperv1.AWSEndpointServiceList{}
					if err := mgtClient.List(ctx, epsList, crclient.InNamespace(hcpNamespace)); err != nil {
						return false, nil
					}
					for i := range epsList.Items {
						if epsList.Items[i].Status.EndpointServiceName != "" {
							endpointServiceName = epsList.Items[i].Status.EndpointServiceName
							return true, nil
						}
					}
					return false, nil
				})
				g.Expect(err).NotTo(HaveOccurred(), "AWSEndpointService should have a populated EndpointServiceName")
				t.Logf("Found endpoint service: %s", endpointServiceName)

				// Step 2: Query the supported AZs for that endpoint service.
				svcOut, err := ec2client.DescribeVpcEndpointServicesWithContext(ctx, &ec2.DescribeVpcEndpointServicesInput{
					ServiceNames: []*string{aws.String(endpointServiceName)},
				})
				g.Expect(err).NotTo(HaveOccurred(), "failed to describe VPC endpoint services")
				g.Expect(svcOut.ServiceDetails).NotTo(BeEmpty(), "endpoint service should have details")

				supportedAZs := sets.NewString()
				for _, az := range svcOut.ServiceDetails[0].AvailabilityZones {
					supportedAZs.Insert(aws.StringValue(az))
				}
				t.Logf("Endpoint service supported AZs: %v", supportedAZs.List())

				// Step 3: Find a VPC subnet in a supported AZ.
				// This ensures ModifyVpcEndpoint won't fail with an AZ mismatch error.
				vpcID := hostedCluster.Spec.Platform.AWS.CloudProviderConfig.VPC
				t.Logf("Finding subnet in a supported AZ in VPC: %s", vpcID)

				subnetsOutput, err := ec2client.DescribeSubnetsWithContext(ctx, &ec2.DescribeSubnetsInput{
					Filters: []*ec2.Filter{
						{
							Name:   aws.String("vpc-id"),
							Values: []*string{aws.String(vpcID)},
						},
						{
							Name:   aws.String("state"),
							Values: []*string{aws.String("available")},
						},
					},
				})
				g.Expect(err).NotTo(HaveOccurred(), "failed to describe subnets")
				g.Expect(subnetsOutput.Subnets).NotTo(BeEmpty(), "VPC should have at least one subnet")

				var arbitrarySubnet *ec2.Subnet
				for _, s := range subnetsOutput.Subnets {
					if supportedAZs.Has(aws.StringValue(s.AvailabilityZone)) {
						arbitrarySubnet = s
						break
					}
				}
				g.Expect(arbitrarySubnet).NotTo(BeNil(), "VPC should have at least one subnet in a supported AZ (%v)", supportedAZs.List())

				arbitrarySubnetID := aws.StringValue(arbitrarySubnet.SubnetId)
				t.Logf("Using arbitrary subnet: %s in AZ: %s", arbitrarySubnetID, aws.StringValue(arbitrarySubnet.AvailabilityZone))

				// Step 4: Get the guest client to create OpenshiftEC2NodeClass resources.
				guestClient := e2eutil.WaitForGuestClient(t, ctx, mgtClient, hostedCluster)

				// Step 5: Create OpenshiftEC2NodeClass with custom SubnetSelectorTerms
				customNodeClassName := "custom-subnet-nodeclass"
				openshiftEC2NodeClass := &hyperkarpenterv1.OpenshiftEC2NodeClass{
					ObjectMeta: metav1.ObjectMeta{
						Name: customNodeClassName,
					},
					Spec: hyperkarpenterv1.OpenshiftEC2NodeClassSpec{
						SubnetSelectorTerms: []hyperkarpenterv1.SubnetSelectorTerm{
							{
								ID: arbitrarySubnetID,
							},
						},
					},
				}

				g.Expect(guestClient.Create(ctx, openshiftEC2NodeClass)).To(Succeed())
				t.Logf("Created OpenshiftEC2NodeClass with custom subnet selector")
				defer func() {
					_ = guestClient.Delete(ctx, openshiftEC2NodeClass)
				}()

				// Step 6: Wait for OpenshiftEC2NodeClass status to be populated by Karpenter
				t.Logf("Waiting for Karpenter to resolve subnets...")
				var resolvedSubnets []hyperkarpenterv1.Subnet
				err = wait.PollUntilContextTimeout(ctx, 5*time.Second, 2*time.Minute, true, func(ctx context.Context) (bool, error) {
					nodeClass := &hyperkarpenterv1.OpenshiftEC2NodeClass{}
					if err := guestClient.Get(ctx, crclient.ObjectKey{Name: customNodeClassName}, nodeClass); err != nil {
						return false, err
					}
					if len(nodeClass.Status.Subnets) > 0 {
						resolvedSubnets = nodeClass.Status.Subnets
						return true, nil
					}
					return false, nil
				})
				g.Expect(err).NotTo(HaveOccurred(), "OpenshiftEC2NodeClass should have resolved subnets in status")
				g.Expect(resolvedSubnets).To(HaveLen(1), "should have resolved exactly one subnet")
				g.Expect(resolvedSubnets[0].ID).To(Equal(arbitrarySubnetID), "resolved subnet should match the specified subnet")
				t.Logf("Karpenter resolved subnet: %s", resolvedSubnets[0].ID)

				// Step 7: Verify ConfigMap is created with the Karpenter-resolved subnet.
				// This proves the EC2NodeClass controller is aggregating subnets correctly.
				t.Logf("Checking for karpenter-subnets ConfigMap in namespace: %s", hcpNamespace)

				configMap := &corev1.ConfigMap{}
				err = wait.PollUntilContextTimeout(ctx, 5*time.Second, 2*time.Minute, true, func(ctx context.Context) (bool, error) {
					if err := mgtClient.Get(ctx, crclient.ObjectKey{
						Namespace: hcpNamespace,
						Name:      karpenterutil.KarpenterSubnetsConfigMapName,
					}, configMap); err != nil {
						t.Logf("ConfigMap not yet available: %v", err)
						return false, nil
					}
					return true, nil
				})
				g.Expect(err).NotTo(HaveOccurred(), "karpenter-subnets ConfigMap should be created")

				// Validate ConfigMap labels
				g.Expect(configMap.Labels).To(HaveKeyWithValue("hypershift.openshift.io/managed-by", "karpenter"))
				g.Expect(configMap.Labels).To(HaveKey("hypershift.openshift.io/infra-id"))
				t.Logf("ConfigMap has correct labels")

				// Validate ConfigMap contains our subnet
				subnetIDsJSON := configMap.Data["subnetIDs"]
				g.Expect(subnetIDsJSON).NotTo(BeEmpty(), "ConfigMap should contain subnetIDs data")

				var subnetIDs []string
				err = json.Unmarshal([]byte(subnetIDsJSON), &subnetIDs)
				g.Expect(err).NotTo(HaveOccurred(), "subnetIDs should be valid JSON")
				g.Expect(subnetIDs).To(ConsistOf(arbitrarySubnetID),
					"ConfigMap should contain exactly the user-specified subnet, not default NodeClass subnets")
				t.Logf("ConfigMap contains Karpenter-resolved subnet: %v", subnetIDs)

				// Step 8: Verify the subnet flows through to AWSEndpointService.Spec.SubnetIDs.
				// This proves the full pipeline: OpenshiftEC2NodeClass → ConfigMap → AWSEndpointService.
				// We check Spec.SubnetIDs rather than waiting for AWSEndpointAvailable=True to avoid
				// prolonging the test with the AWS-side endpoint modification round-trip.
				t.Logf("Waiting for AWSEndpointService to include subnet %s in Spec.SubnetIDs", arbitrarySubnetID)
				err = wait.PollUntilContextTimeout(ctx, 5*time.Second, 3*time.Minute, true, func(ctx context.Context) (bool, error) {
					eps := &hyperv1.AWSEndpointService{}
					if err := mgtClient.Get(ctx, crclient.ObjectKey{
						Namespace: hcpNamespace,
						Name:      "kube-apiserver-private",
					}, eps); err != nil {
						return false, nil
					}
					for _, id := range eps.Spec.SubnetIDs {
						if id == arbitrarySubnetID {
							return true, nil
						}
					}
					return false, nil
				})
				g.Expect(err).NotTo(HaveOccurred(), "AWSEndpointService should include the Karpenter subnet in Spec.SubnetIDs")
				t.Logf("AWSEndpointService.Spec.SubnetIDs includes Karpenter subnet: %s", arbitrarySubnetID)

				// Step 9: Provision a Karpenter node so the framework's EnsureHostedCluster
				// post-validation phase finds a real worker node and cluster operators can converge.
				// The default OpenshiftEC2NodeClass ("default") is created automatically by AutoNode.
				// We reuse the existing assets from TestKarpenter.
				t.Logf("Creating Karpenter NodePool and workload to provision a worker node")
				karpenterNodePool := &unstructured.Unstructured{}
				yamlFile, err := content.ReadFile("assets/karpenter-nodepool.yaml")
				g.Expect(err).NotTo(HaveOccurred(), "should read karpenter-nodepool.yaml")
				g.Expect(yaml.Unmarshal(yamlFile, karpenterNodePool)).To(Succeed())

				workLoads := &unstructured.Unstructured{}
				yamlFile, err = content.ReadFile("assets/karpenter-workloads.yaml")
				g.Expect(err).NotTo(HaveOccurred(), "should read karpenter-workloads.yaml")
				g.Expect(yaml.Unmarshal(yamlFile, workLoads)).To(Succeed())
				// Override replicas to 1 so Karpenter provisions exactly one node.
				// The YAML default is 3 (with podAntiAffinity), which would cause
				// WaitForReadyNodesByLabels to fail since it checks for an exact count.
				workLoads.Object["spec"].(map[string]interface{})["replicas"] = 1

				g.Expect(guestClient.Create(ctx, karpenterNodePool)).To(Succeed())
				// DO NOT defer-delete the NodePool: PublicAndPrivate clusters are treated as
				// private by IsPrivateHC(), so EnsureHostedCluster skips the node-list check
				// and defaults hasWorkerNodes=true. If the NodePool is deleted here (end of
				// Main), Karpenter terminates the node before EnsureHostedCluster runs, and
				// ValidateHostedClusterConditions times out waiting for cluster operators that
				// need worker nodes. The framework tears down the whole HostedCluster during
				// Teardown, so no cleanup is needed here.
				// defer func() { _ = guestClient.Delete(ctx, karpenterNodePool) }()
				g.Expect(guestClient.Create(ctx, workLoads)).To(Succeed())
				// defer func() { _ = guestClient.Delete(ctx, workLoads) }()

				nodeLabels := map[string]string{
					"node.kubernetes.io/instance-type": "t3.large",
					"karpenter.sh/nodepool":            karpenterNodePool.GetName(),
				}
				t.Logf("Waiting for a Karpenter node to become ready")
				e2eutil.WaitForReadyNodesByLabels(t, ctx, guestClient, hostedCluster.Spec.Platform.Type, 1, nodeLabels)
				t.Logf("Karpenter node is ready")

				// Cleanup: delete the OpenshiftEC2NodeClass
				t.Logf("Cleaning up OpenshiftEC2NodeClass")
				g.Expect(guestClient.Delete(ctx, openshiftEC2NodeClass)).To(Succeed())

				// Step 10: Verify ConfigMap cleanup when all OpenshiftEC2NodeClasses are deleted.
				// List remaining NodeClasses to determine if we were the last one.
				nodeClassList := &hyperkarpenterv1.OpenshiftEC2NodeClassList{}
				err = guestClient.List(ctx, nodeClassList)
				g.Expect(err).NotTo(HaveOccurred())

				if len(nodeClassList.Items) == 0 {
					t.Logf("All OpenshiftEC2NodeClass resources deleted, ConfigMap should be cleaned up")
					err = wait.PollUntilContextTimeout(ctx, 5*time.Second, 1*time.Minute, true, func(ctx context.Context) (bool, error) {
						cm := &corev1.ConfigMap{}
						err := mgtClient.Get(ctx, crclient.ObjectKey{
							Namespace: hcpNamespace,
							Name:      karpenterutil.KarpenterSubnetsConfigMapName,
						}, cm)
						if err != nil && crclient.IgnoreNotFound(err) == nil {
							return true, nil // ConfigMap deleted
						}
						return false, nil
					})
					// Don't fail if cleanup didn't happen - other tests may have created nodeclasses
					if err == nil {
						t.Logf("ConfigMap successfully cleaned up after last OpenshiftEC2NodeClass deletion")
					} else {
						t.Logf("ConfigMap still exists (may be used by other NodeClasses)")
					}
				} else {
					t.Logf("Other OpenshiftEC2NodeClass resources exist, ConfigMap should remain")
				}
			})
		}).Execute(&subnetClusterOpts, globalOpts.Platform, globalOpts.ArtifactDir,
			"karpenter-arbitrary-subnets", globalOpts.ServiceAccountSigningKey)
	})

	// Wrap the main HighlyAvailable cluster in its own t.Run so that
	// -run TestKarpenter/Arbitrary_subnet_propagation skips this block
	// entirely (no cluster created, no stuck teardown).
	t.Run("Main karpenter cluster", func(t *testing.T) {
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

			// TODO(jkyros): This test doesn't clean up after itself (I think intentionally) so we can test general cluster
			// cleanup, but as a result it needs to run last, otherwise it will pollute any other cases that come after it
			// and its "on-demand" nodepool may service requests that are not intended for it
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
	}) // end t.Run("Main karpenter cluster")
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
