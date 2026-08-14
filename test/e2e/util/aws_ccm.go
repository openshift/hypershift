//go:build e2e
// +build e2e

package util

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	awsutil "github.com/openshift/hypershift/cmd/infra/aws/util"

	"github.com/aws/aws-sdk-go-v2/aws"
	elbv2 "github.com/aws/aws-sdk-go-v2/service/elasticloadbalancingv2"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"

	crclient "sigs.k8s.io/controller-runtime/pkg/client"

	gomega "github.com/onsi/gomega"
)

// AWSCCMTestConfig is a configuration struct for AWS CCM tests.
type AWSCCMTestConfig struct {
	MgtClient     crclient.Client
	GuestClient   crclient.Client
	HostedCluster *hyperv1.HostedCluster
	AWSCredsFile  string
	Platform      hyperv1.PlatformType
}

// EnsureAWSCCMWithCustomizations implements tests that exercise AWS CCM controller for critical features.
// This test is only supported on platform AWS, as well runs only when the feature gate AWSServiceLBNetworkSecurityGroup is enabled.
func EnsureAWSCCMWithCustomizations(t *testing.T, ctx context.Context, cfg *AWSCCMTestConfig) {
	t.Run("EnsureAWSCCMWithManagedSG", func(t *testing.T) {
		t.Parallel()
		AtLeast(t, Version423)
		if cfg.Platform != hyperv1.AWSPlatform {
			t.Skip("test only supported on platform AWS")
		}

		// Test case: Validate managed security groups in the config
		t.Run("When AWSServiceLBNetworkSecurityGroup is enabled it must have config NLBSecurityGroupMode=Managed entry in cloud-config configmap", func(t *testing.T) {
			t.Logf("Validating aws-cloud-config ConfigMap contains entry NLBSecurityGroupMode=Managed")

			// The control plane namespace is {namespace}-{name}
			controlPlaneNamespace := fmt.Sprintf("%s-%s", cfg.HostedCluster.Namespace, cfg.HostedCluster.Name)
			t.Logf("Using control plane namespace: %s", controlPlaneNamespace)

			// Ensure the configuration is present in the configmap
			EventuallyObject(t, ctx, "NLBSecurityGroupMode = Managed entry exists in aws-cloud-config ConfigMap",
				func(ctx context.Context) (*corev1.ConfigMap, error) {
					cm := &corev1.ConfigMap{}
					err := cfg.MgtClient.Get(ctx, crclient.ObjectKey{
						Namespace: controlPlaneNamespace,
						Name:      "aws-cloud-config",
					}, cm)
					return cm, err
				},
				[]Predicate[*corev1.ConfigMap]{func(cm *corev1.ConfigMap) (done bool, reasons string, err error) {
					awsConf, exists := cm.Data["aws.conf"]
					if !exists {
						return false, "aws.conf key not found in ConfigMap", nil
					}

					t.Logf("verifying NLBSecurityGroupMode is present and set to Managed")
					if !strings.Contains(awsConf, "NLBSecurityGroupMode = Managed") {
						return false, "NLBSecurityGroupMode = Managed not present in aws.conf yet", nil
					}

					t.Logf("Successfully validated cloud-config contains NLBSecurityGroupMode = Managed")
					return true, "Successfully validated aws-config", nil
				},
				},
				WithTimeout(2*time.Minute),
			)
		})

		// Test case: Create custom service type NLB in the hosted cluster, the NLB resource must
		// have a security group attached to it
		// Note: this test must run only when the NLBSecurityGroupMode = Managed is present in the configmap
		t.Run("When AWSServiceLBNetworkSecurityGroup is enabled it must create a LoadBalancer NLB with managed security group attached", func(t *testing.T) {
			g := gomega.NewWithT(t)

			// Create a test namespace in the guest cluster
			testNS := &corev1.Namespace{
				ObjectMeta: metav1.ObjectMeta{
					Name: "test-ccm-nlb-sg",
				},
			}
			t.Logf("Creating test namespace %s in guest cluster", testNS.Name)

			err := cfg.GuestClient.Create(ctx, testNS)
			g.Expect(err).NotTo(gomega.HaveOccurred(), "failed to create test namespace")
			defer func() {
				t.Logf("Cleaning up test namespace %s", testNS.Name)
				_ = cfg.GuestClient.Delete(ctx, testNS)
			}()

			// Create a LoadBalancer service
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
							TargetPort: intstr.FromInt(8080),
							Protocol:   corev1.ProtocolTCP,
						},
					},
				},
			}
			t.Logf("Creating LoadBalancer service %s/%s", testSvc.Namespace, testSvc.Name)
			err = cfg.GuestClient.Create(ctx, testSvc)
			g.Expect(err).NotTo(gomega.HaveOccurred(), "failed to create LoadBalancer service")

			// Wait for the LoadBalancer to be provisioned and get hostname
			var lbHostname string
			EventuallyObject(t, ctx, "LoadBalancer service to have ingress hostname",
				func(ctx context.Context) (*corev1.Service, error) {
					svc := &corev1.Service{}
					err := cfg.GuestClient.Get(ctx, crclient.ObjectKey{
						Namespace: testNS.Name,
						Name:      testSvc.Name,
					}, svc)
					return svc, err
				},
				[]Predicate[*corev1.Service]{
					func(svc *corev1.Service) (done bool, reasons string, err error) {
						if len(svc.Status.LoadBalancer.Ingress) == 0 {
							return false, "LoadBalancer ingress list is empty", nil
						}
						if svc.Status.LoadBalancer.Ingress[0].Hostname == "" {
							return false, "LoadBalancer hostname is empty", nil
						}
						lbHostname = svc.Status.LoadBalancer.Ingress[0].Hostname
						return true, fmt.Sprintf("LoadBalancer hostname is %s", lbHostname), nil
					},
				},
				WithTimeout(5*time.Minute),
			)
			t.Logf("LoadBalancer provisioned with hostname: %s", lbHostname)

			lbName := extractLoadBalancerNameFromHostname(lbHostname)
			g.Expect(lbName).NotTo(gomega.BeEmpty(), "load balancer name should be extracted from hostname")
			t.Logf("Extracted load balancer name: %s", lbName)

			t.Logf("Verifying load balancer has security groups using AWS SDK")
			awsSession := awsutil.NewSession(ctx, "e2e-ccm-nlb-sg", cfg.AWSCredsFile, "", "", cfg.HostedCluster.Spec.Platform.AWS.Region)
			g.Expect(awsSession).NotTo(gomega.BeNil(), "failed to create AWS session")

			awsConfig := awsutil.NewConfig()
			g.Expect(awsConfig).NotTo(gomega.BeNil(), "failed to create AWS config")

			elbv2Client := elbv2.NewFromConfig(*awsSession, func(o *elbv2.Options) {
				o.Retryer = awsConfig()
			})
			g.Expect(elbv2Client).NotTo(gomega.BeNil(), "failed to create ELBv2 client")

			describeLBInput := &elbv2.DescribeLoadBalancersInput{
				Names: []string{lbName},
			}

			// Wait for the load balancer to exist and become active before validating attributes like SecurityGroups.
			// This avoids flakes where the Service is created but the LB is still provisioning.
			t.Logf("Waiting for load balancer %q to become available (up to ~3 minutes)", lbName)
			waiter := elbv2.NewLoadBalancerAvailableWaiter(elbv2Client, func(o *elbv2.LoadBalancerAvailableWaiterOptions) {
				o.MinDelay = 5 * time.Second
				o.MaxDelay = 30 * time.Second
			})
			err = waiter.Wait(ctx, describeLBInput, 3*time.Minute)
			g.Expect(err).NotTo(gomega.HaveOccurred(), "load balancer did not become available in time")

			// Describe the load balancer
			t.Logf("Describing load balancer to check for security groups")

			describeLBOutput, err := elbv2Client.DescribeLoadBalancers(ctx, describeLBInput)
			g.Expect(err).NotTo(gomega.HaveOccurred(), "failed to describe load balancer")
			g.Expect(len(describeLBOutput.LoadBalancers)).To(gomega.BeNumerically(">", 0), "no load balancers found with name %s", lbName)

			lb := describeLBOutput.LoadBalancers[0]
			t.Logf("Load balancer ARN: %s", aws.ToString(lb.LoadBalancerArn))
			t.Logf("Load balancer Type: %s", string(lb.Type))
			t.Logf("Load balancer Security Groups: %v", lb.SecurityGroups)

			// Verify security groups are attached
			g.Expect(len(lb.SecurityGroups)).To(gomega.BeNumerically(">", 0), "load balancer should have security groups attached when NLBSecurityGroupMode = Managed")

			t.Logf("Successfully validated that load balancer has %d security group(s) attached", len(lb.SecurityGroups))
			for i, sg := range lb.SecurityGroups {
				t.Logf("  Security Group %d: %s", i+1, sg)
			}
		})
	})
}

// extractLoadBalancerNameFromHostname extracts the load balancer name from the DNS hostname.
// The hostname is in the format of <name>-<id>.elb.<region>.amazonaws.com.
// The function drops only the last hyphen segment.
// Example:
// - Input: "e2e-v7-fnt8p-ext-9a316db0952d7e14.elb.us-east-1.amazonaws.com"
// - Output: "e2e-v7-fnt8p-ext"
// - Input: "af1c7bcc09ce1420db0292d91f0dad1f-f4ad6ce6794c3afd.elb.us-east-1.amazonaws.com"
// - Output: "af1c7bcc09ce1420db0292d91f0dad1f"
// - Input: "a7f9d8c870a2b44c39d9565e2ec22e81-1194117244.us-east-1.elb.amazonaws.com"
// - Output: "a7f9d8c870a2b44c39d9565e2ec22e81"
func extractLoadBalancerNameFromHostname(hostname string) string {
	firstLabel := strings.SplitN(hostname, ".", 2)[0]
	firstLabel = strings.TrimPrefix(firstLabel, "internal-")
	lastHyphen := strings.LastIndex(firstLabel, "-")
	if lastHyphen == -1 {
		return firstLabel
	}
	return firstLabel[:lastHyphen]
}
