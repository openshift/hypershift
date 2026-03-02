//go:build e2e

package e2e

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go/aws"
	"github.com/aws/aws-sdk-go/service/sqs"
	"github.com/google/uuid"
	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	"github.com/openshift/hypershift/hypershift-operator/controllers/manifests"
	"github.com/openshift/hypershift/support/util"
	e2eutil "github.com/openshift/hypershift/test/e2e/util"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/utils/ptr"
	capiv1 "sigs.k8s.io/cluster-api/api/v1beta1"
	crclient "sigs.k8s.io/controller-runtime/pkg/client"
)

const (
	// AnnotationEnableSpot is the annotation key to enable spot instances on a NodePool.
	AnnotationEnableSpot = "hypershift.openshift.io/enable-spot"

	// interruptibleInstanceLabel is the label applied to spot instance machines.
	interruptibleInstanceLabel = "hypershift.openshift.io/interruptible-instance"

	// awsNodeTerminationHandlerDeploymentName is the name of the termination handler deployment.
	awsNodeTerminationHandlerDeploymentName = "aws-node-termination-handler"

	// testSQSQueueName is the SQS queue name used for testing.
	testSQSQueueName = "agarcial-nth-queue"

	// rebalanceRecommendationTaintKey is the taint key applied by the AWS Node Termination Handler
	// when it receives an EC2 rebalance recommendation event.
	rebalanceRecommendationTaintKey = "aws-node-termination-handler/rebalance-recommendation"
)

// ec2RebalanceRecommendationEvent represents the structure of an EC2 Rebalance Recommendation
// event as sent by AWS EventBridge.
type ec2RebalanceRecommendationEvent struct {
	Version    string                 `json:"version"`
	Source     string                 `json:"source"`
	DetailType string                 `json:"detail-type"`
	Detail     map[string]interface{} `json:"detail"`
	ID         string                 `json:"id"`
	Time       string                 `json:"time"`
	Region     string                 `json:"region"`
	Account    string                 `json:"account"`
}

// extractInstanceIDFromProviderID extracts the EC2 instance ID from a node's providerID.
// Format: aws:///us-east-1a/i-0123456789abcdef0
func extractInstanceIDFromProviderID(providerID string) string {
	return providerID[strings.LastIndex(providerID, "/")+1:]
}

type SpotTerminationHandlerTest struct {
	DummyInfraSetup
	ctx        context.Context
	mgmtClient crclient.Client

	hostedCluster       *hyperv1.HostedCluster
	hostedClusterClient crclient.Client
	clusterOpts         e2eutil.PlatformAgnosticOptions
}

func NewSpotTerminationHandlerTest(ctx context.Context, mgmtClient crclient.Client, hostedCluster *hyperv1.HostedCluster, hcClient crclient.Client, clusterOpts e2eutil.PlatformAgnosticOptions) *SpotTerminationHandlerTest {
	return &SpotTerminationHandlerTest{
		ctx:                 ctx,
		mgmtClient:          mgmtClient,
		hostedCluster:       hostedCluster,
		hostedClusterClient: hcClient,
		clusterOpts:         clusterOpts,
	}
}

func (s *SpotTerminationHandlerTest) Setup(t *testing.T) {
	if globalOpts.Platform != hyperv1.AWSPlatform {
		t.Skip("test only supported on platform AWS")
	}
}

func (s *SpotTerminationHandlerTest) BuildNodePoolManifest(defaultNodepool hyperv1.NodePool) (*hyperv1.NodePool, error) {
	nodePool := &hyperv1.NodePool{
		ObjectMeta: metav1.ObjectMeta{
			Name:      s.hostedCluster.Name + "-" + "test-spot-termination",
			Namespace: s.hostedCluster.Namespace,
			// We use the annotation to enable spot instances for the e2e test
			// since real spot instances are not reliable for CI.
			Annotations: map[string]string{
				AnnotationEnableSpot: "true",
			},
		},
	}
	defaultNodepool.Spec.DeepCopyInto(&nodePool.Spec)
	nodePool.Spec.Replicas = &oneReplicas

	return nodePool, nil
}

func (s *SpotTerminationHandlerTest) Run(t *testing.T, nodePool hyperv1.NodePool, nodes []corev1.Node) {
	t.Run("SpotTerminationHandlerTest", func(t *testing.T) {
		controlPlaneNamespace := manifests.HostedControlPlaneNamespace(s.hostedCluster.Namespace, s.hostedCluster.Name)

		// Step 0: Add SQS permissions to the NodePool role so the termination handler can access the queue
		sqsPolicy := fmt.Sprintf(`{
			"Version": "2012-10-17",
			"Statement": [
				{
					"Effect": "Allow",
					"Action": [
						"sqs:ReceiveMessage",
						"sqs:DeleteMessage"
					],
					"Resource": "arn:aws:sqs:%s:*:*"
				}
			]
		}`, s.clusterOpts.AWSPlatform.Region)

		t.Logf("Adding SQS policy to NodePool role %s", s.hostedCluster.Spec.Platform.AWS.RolesRef.NodePoolManagementARN)
		cleanupSQSPolicy, err := e2eutil.PutRolePolicy(
			s.ctx,
			s.clusterOpts.AWSPlatform.Credentials.AWSCredentialsFile,
			s.clusterOpts.AWSPlatform.Region,
			s.hostedCluster.Spec.Platform.AWS.RolesRef.NodePoolManagementARN,
			sqsPolicy,
		)
		if err != nil {
			t.Fatalf("failed to add SQS policy to NodePool role: %v", err)
		}
		defer func() {
			t.Log("Cleaning up: removing SQS policy from NodePool role")
			if err := cleanupSQSPolicy(); err != nil {
				t.Logf("warning: failed to cleanup SQS policy: %v", err)
			}
		}()

		// Step 1: Discover SQS queue URL and add it to the HostedCluster spec
		sqsClient := e2eutil.GetSQSClient(s.clusterOpts.AWSPlatform.Credentials.AWSCredentialsFile, s.clusterOpts.AWSPlatform.Region)
		queueURLResult, err := sqsClient.GetQueueUrl(&sqs.GetQueueUrlInput{
			QueueName: aws.String(testSQSQueueName),
		})
		if err != nil {
			t.Fatalf("failed to get SQS queue URL for queue %s: %v", testSQSQueueName, err)
		}
		sqsQueueURL := aws.StringValue(queueURLResult.QueueUrl)
		t.Logf("Discovered SQS queue URL: %s", sqsQueueURL)

		t.Logf("Adding SQS queue URL to HostedCluster spec %s/%s", s.hostedCluster.Namespace, s.hostedCluster.Name)
		err = e2eutil.UpdateObject(t, s.ctx, s.mgmtClient, s.hostedCluster, func(obj *hyperv1.HostedCluster) {
			if obj.Spec.Platform.AWS == nil {
				obj.Spec.Platform.AWS = &hyperv1.AWSPlatformSpec{}
			}
			obj.Spec.Platform.AWS.TerminationHandlerQueueURL = ptr.To(sqsQueueURL)
		})
		if err != nil {
			t.Fatalf("failed to update HostedCluster with SQS queue URL: %v", err)
		}

		// Step 2: Wait for the aws-node-termination-handler deployment to be ready
		t.Logf("Waiting for aws-node-termination-handler deployment to be ready in namespace %s", controlPlaneNamespace)
		terminationHandlerDeployment := &appsv1.Deployment{
			ObjectMeta: metav1.ObjectMeta{
				Name:      awsNodeTerminationHandlerDeploymentName,
				Namespace: controlPlaneNamespace,
			},
		}
		e2eutil.EventuallyObject(t, s.ctx, fmt.Sprintf("Waiting for deployment %s/%s to be ready", controlPlaneNamespace, awsNodeTerminationHandlerDeploymentName),
			func(ctx context.Context) (*appsv1.Deployment, error) {
				err := s.mgmtClient.Get(ctx, crclient.ObjectKeyFromObject(terminationHandlerDeployment), terminationHandlerDeployment)
				return terminationHandlerDeployment, err
			},
			[]e2eutil.Predicate[*appsv1.Deployment]{
				func(obj *appsv1.Deployment) (bool, string, error) {
					if obj.Spec.Replicas == nil || *obj.Spec.Replicas == 0 {
						return false, "Deployment has 0 replicas", nil
					}
					if ready := util.IsDeploymentReady(s.ctx, obj); !ready {
						return false, "Deployment is not ready", nil
					}
					return true, "Deployment is ready", nil
				},
			},
			e2eutil.WithInterval(10*time.Second), e2eutil.WithTimeout(5*time.Minute),
		)
		t.Logf("aws-node-termination-handler deployment is ready")

		// Step 3: Wait for the spot MachineHealthCheck to be created
		spotMHCName := nodePool.Name + "-spot"
		t.Logf("Waiting for spot MachineHealthCheck %s/%s to be created", controlPlaneNamespace, spotMHCName)
		spotMHC := &capiv1.MachineHealthCheck{
			ObjectMeta: metav1.ObjectMeta{
				Name:      spotMHCName,
				Namespace: controlPlaneNamespace,
			},
		}
		e2eutil.EventuallyObject(t, s.ctx, fmt.Sprintf("Waiting for MachineHealthCheck %s/%s to be created with correct selector", controlPlaneNamespace, spotMHCName),
			func(ctx context.Context) (*capiv1.MachineHealthCheck, error) {
				err := s.mgmtClient.Get(ctx, crclient.ObjectKeyFromObject(spotMHC), spotMHC)
				return spotMHC, err
			},
			[]e2eutil.Predicate[*capiv1.MachineHealthCheck]{
				func(obj *capiv1.MachineHealthCheck) (bool, string, error) {
					// Verify the MHC has the correct label selector for spot instances
					if obj.Spec.Selector.MatchLabels == nil {
						return false, "MachineHealthCheck has no MatchLabels", nil
					}
					if _, ok := obj.Spec.Selector.MatchLabels[interruptibleInstanceLabel]; !ok {
						return false, fmt.Sprintf("MachineHealthCheck does not have label selector for %s", interruptibleInstanceLabel), nil
					}
					return true, "MachineHealthCheck has correct label selector for spot instances", nil
				},
			},
			e2eutil.WithInterval(10*time.Second), e2eutil.WithTimeout(5*time.Minute),
		)
		t.Logf("Spot MachineHealthCheck is created with correct label selector")

		// Step 4: Verify we have at least one ready spot node (passed from the test framework)
		if len(nodes) == 0 {
			t.Fatal("expected at least one ready node from the spot nodepool")
		}
		spotNode := &nodes[0]
		t.Logf("Found ready spot node: %s with providerID: %s", spotNode.Name, spotNode.Spec.ProviderID)

		// Step 5: Send EC2 Rebalance Recommendation event to SQS queue
		instanceID := extractInstanceIDFromProviderID(spotNode.Spec.ProviderID)
		t.Logf("Sending EC2 Rebalance Recommendation event to SQS queue for instance %s", instanceID)

		// Build the EC2 Rebalance Recommendation event message
		rebalanceEvent := ec2RebalanceRecommendationEvent{
			Version:    "0",
			Source:     "aws.ec2",
			DetailType: "EC2 Instance Rebalance Recommendation",
			Detail: map[string]interface{}{
				"instance-id": instanceID,
			},
			ID:      uuid.New().String(),
			Time:    time.Now().UTC().Format(time.RFC3339),
			Region:  s.clusterOpts.AWSPlatform.Region,
			Account: "123456789012",
		}

		eventJSON, err := json.Marshal(rebalanceEvent)
		if err != nil {
			t.Fatalf("failed to marshal rebalance event: %v", err)
		}

		_, err = sqsClient.SendMessage(&sqs.SendMessageInput{
			QueueUrl:    aws.String(sqsQueueURL),
			MessageBody: aws.String(string(eventJSON)),
		})
		if err != nil {
			t.Fatalf("failed to send SQS message: %v", err)
		}
		t.Logf("Successfully sent EC2 Rebalance Recommendation event to SQS queue")

		// Step 6: Wait for the node to have the rebalance recommendation taint
		t.Logf("Waiting for node %s to have taint prefix %s", spotNode.Name, rebalanceRecommendationTaintKey)
		e2eutil.EventuallyObject(t, s.ctx, fmt.Sprintf("Waiting for node %s to have rebalance recommendation taint", spotNode.Name),
			func(ctx context.Context) (*corev1.Node, error) {
				node := &corev1.Node{}
				err := s.hostedClusterClient.Get(ctx, crclient.ObjectKey{Name: spotNode.Name}, node)
				return node, err
			},
			[]e2eutil.Predicate[*corev1.Node]{
				func(node *corev1.Node) (bool, string, error) {
					for _, taint := range node.Spec.Taints {
						if strings.HasPrefix(taint.Key, rebalanceRecommendationTaintKey) {
							return true, fmt.Sprintf("Node has taint %s with effect %s", taint.Key, taint.Effect), nil
						}
					}
					return false, "Node does not have aws-node-termination-handler taint", nil
				},
			},
			e2eutil.WithInterval(5*time.Second), e2eutil.WithTimeout(15*time.Minute),
		)
		t.Logf("Node %s has the rebalance recommendation taint", spotNode.Name)

		// Step 7: Clean up - remove the SQS queue URL from spec
		t.Logf("Cleaning up: removing SQS queue URL from HostedCluster spec")
		err = e2eutil.UpdateObject(t, s.ctx, s.mgmtClient, s.hostedCluster, func(obj *hyperv1.HostedCluster) {
			if obj.Spec.Platform.AWS != nil {
				obj.Spec.Platform.AWS.TerminationHandlerQueueURL = nil
			}
		})
		if err != nil {
			t.Fatalf("failed to remove SQS queue URL from HostedCluster: %v", err)
		}
	})
}
