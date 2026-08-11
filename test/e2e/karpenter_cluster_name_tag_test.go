//go:build e2e

package e2e

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	ec2types "github.com/aws/aws-sdk-go-v2/service/ec2/types"
	awskarpenterv1 "github.com/aws/karpenter-provider-aws/pkg/apis/v1"
	. "github.com/onsi/gomega"
	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	hyperkarpenterv1 "github.com/openshift/hypershift/api/karpenter/v1"
	karpentercpov2 "github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/v2/karpenter"
	"github.com/openshift/hypershift/hypershift-operator/controllers/manifests"
	karpenterassets "github.com/openshift/hypershift/karpenter-operator/controllers/karpenter/assets"
	e2eutil "github.com/openshift/hypershift/test/e2e/util"
	appsv1 "k8s.io/api/apps/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	crclient "sigs.k8s.io/controller-runtime/pkg/client"
	karpenterv1 "sigs.k8s.io/karpenter/pkg/apis/v1"
)

func TestKarpenterClusterNameTagKey(t *testing.T) {
	e2eutil.ShouldRunKarpenterTests(t)
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
	clusterOpts.NodePoolReplicas = 0

	// Set the annotation before cluster creation so Karpenter starts with the
	// custom tag key from the beginning and no pod restart is required.
	customTagKey := "openshift:cluster-name"
	clusterOpts.Annotations = append(clusterOpts.Annotations,
		fmt.Sprintf("%s=%s", hyperkarpenterv1.KarpenterProviderAWSClusterNameTagKey, customTagKey),
		// TODO(jkyros): Tag stuff hasn't merged upstream yet, take this out once it has, we're just testing plumbing for now,
		// this absolutely needs to come out before we merge
		fmt.Sprintf("%s=%s", hyperkarpenterv1.KarpenterProviderAWSImage, "quay.io/jkyros/aws-karpenter-provider-aws:tags"),
	)

	e2eutil.NewHypershiftTest(t, ctx, func(t *testing.T, g Gomega, mgtClient crclient.Client, hostedCluster *hyperv1.HostedCluster) {
		guestClient := e2eutil.WaitForGuestClient(t, ctx, mgtClient, hostedCluster)
		awsCredsFile := clusterOpts.AWSPlatform.Credentials.AWSCredentialsFile
		awsRegion := clusterOpts.AWSPlatform.Region

		t.Run("When cluster-name tag key annotation is set, it should propagate and tag EC2 instances correctly",
			testClusterNameTagKey(ctx, mgtClient, guestClient, hostedCluster, awsCredsFile, awsRegion, customTagKey))
	}).Execute(&clusterOpts, globalOpts.Platform, globalOpts.ArtifactDir, "karpenter-tag-key", globalOpts.ServiceAccountSigningKey)
}

func testClusterNameTagKey(ctx context.Context, mgtClient, guestClient crclient.Client,
	hostedCluster *hyperv1.HostedCluster, awsCredsFile, awsRegion, customTagKey string,
) func(t *testing.T) {
	return func(t *testing.T) {
		g := NewWithT(t)

		hcpNamespace := manifests.HostedControlPlaneNamespace(hostedCluster.Namespace, hostedCluster.Name)

		// Verify the annotation propagated from HostedCluster to HostedControlPlane.
		t.Log("Waiting for cluster-name tag key annotation to appear on the HostedControlPlane")
		hcp := &hyperv1.HostedControlPlane{}
		g.Eventually(func(g Gomega) {
			err := mgtClient.Get(ctx, crclient.ObjectKey{Namespace: hcpNamespace, Name: hostedCluster.Name}, hcp)
			g.Expect(err).NotTo(HaveOccurred())
			g.Expect(hcp.Annotations).To(HaveKeyWithValue(
				hyperkarpenterv1.KarpenterProviderAWSClusterNameTagKey, customTagKey,
			))
		}).WithTimeout(2 * time.Minute).WithPolling(5 * time.Second).Should(Succeed())
		t.Logf("Annotation %s=%s is present on the HostedControlPlane", hyperkarpenterv1.KarpenterProviderAWSClusterNameTagKey, customTagKey)

		// Verify CLUSTER_NAME_TAG_KEY env var is set on the Karpenter container.
		t.Log("Waiting for CLUSTER_NAME_TAG_KEY env var to appear in the Karpenter deployment")
		g.Eventually(func(g Gomega) {
			deployment := &appsv1.Deployment{}
			err := mgtClient.Get(ctx, crclient.ObjectKey{
				Namespace: hcpNamespace,
				Name:      karpentercpov2.ComponentName,
			}, deployment)
			g.Expect(err).NotTo(HaveOccurred())
			var found bool
			for _, c := range deployment.Spec.Template.Spec.Containers {
				if c.Name != karpentercpov2.ComponentName {
					continue
				}
				for _, env := range c.Env {
					if env.Name == "CLUSTER_NAME_TAG_KEY" && env.Value == customTagKey {
						found = true
					}
				}
			}
			g.Expect(found).To(BeTrue(), "CLUSTER_NAME_TAG_KEY=%s not found in Karpenter container env vars", customTagKey)
		}).WithTimeout(2 * time.Minute).WithPolling(5 * time.Second).Should(Succeed())
		t.Logf("CLUSTER_NAME_TAG_KEY=%s is set in the Karpenter deployment", customTagKey)

		// Wait for AutoNodeEnabled=True before provisioning: this confirms that Karpenter
		// and the karpenter-operator are fully rolled out and the default EC2NodeClass is ready.
		t.Log("Waiting for AutoNodeEnabled condition to be True on the HostedCluster")
		e2eutil.EventuallyObject(t, ctx, fmt.Sprintf("HostedCluster %s/%s to have AutoNodeEnabled=True", hostedCluster.Namespace, hostedCluster.Name),
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
		t.Log("AutoNodeEnabled=True: Karpenter is ready to provision nodes")

		// Also wait for the default EC2NodeClass to have AMISelectorTerms and UserData populated.
		t.Log("Waiting for default EC2NodeClass to be ready for provisioning")
		g.Eventually(func(g Gomega) {
			nc := &awskarpenterv1.EC2NodeClass{}
			err := guestClient.Get(ctx, crclient.ObjectKey{Name: karpenterassets.EC2NodeClassDefault}, nc)
			g.Expect(err).NotTo(HaveOccurred())
			g.Expect(nc.Spec.AMISelectorTerms).NotTo(BeEmpty(), "EC2NodeClass AMISelectorTerms should be populated")
			g.Expect(nc.Spec.UserData).NotTo(BeNil(), "EC2NodeClass UserData should be populated")
			g.Expect(*nc.Spec.UserData).NotTo(BeEmpty(), "EC2NodeClass UserData should not be empty")
		}).WithTimeout(5 * time.Minute).WithPolling(10 * time.Second).Should(Succeed())
		t.Log("Default EC2NodeClass is ready")

		// Provision a node and verify the EC2 instance carries the custom tag key.
		hc := hostedCluster.DeepCopy()
		err := mgtClient.Get(ctx, crclient.ObjectKeyFromObject(hc), hc)
		g.Expect(err).NotTo(HaveOccurred())

		testNodePool := baseNodePool("tag-key-test", "default")
		workloads := testWorkload("tag-key-web-app", 1, map[string]string{
			karpenterv1.NodePoolLabelKey: testNodePool.Name,
		})

		g.Expect(guestClient.Create(ctx, testNodePool)).To(Succeed())
		t.Log("Created Karpenter NodePool")
		g.Expect(guestClient.Create(ctx, workloads)).To(Succeed())
		t.Log("Created workloads")
		t.Cleanup(func() {
			_ = guestClient.Delete(ctx, workloads)
			_ = guestClient.Delete(ctx, testNodePool)
		})

		nodeLabels := map[string]string{karpenterv1.NodePoolLabelKey: testNodePool.Name}
		nodes := e2eutil.WaitForReadyNodesByLabels(t, ctx, guestClient, hc.Spec.Platform.Type, 1, nodeLabels)
		t.Logf("Karpenter node is ready: %s", nodes[0].Name)

		ec2client := ec2Client(awsCredsFile, awsRegion)
		instance, instanceID := describeEC2Instance(ctx, g, ec2client, nodes[0])
		t.Logf("Verifying tags on EC2 instance %s", instanceID)

		// The custom tag key must be present with the cluster's infraID as the value.
		// Karpenter sets the cluster-name tag value to CLUSTER_NAME (= infraID).
		g.Expect(instance.Tags).To(ContainElement(ec2types.Tag{
			Key:   aws.String(customTagKey),
			Value: aws.String(hc.Spec.InfraID),
		}), "EC2 instance %s should have tag %s=%s", instanceID, customTagKey, hc.Spec.InfraID)

		// The default eks:eks-cluster-name tag must not be present; the custom key replaced it.
		for _, tag := range instance.Tags {
			g.Expect(aws.ToString(tag.Key)).NotTo(Equal("eks:eks-cluster-name"),
				"EC2 instance %s should not have eks:eks-cluster-name tag when a custom key is configured", instanceID)
		}

		t.Logf("EC2 instance %s correctly has tag %s=%s and no eks:eks-cluster-name tag",
			instanceID, customTagKey, hc.Spec.InfraID)
	}
}
