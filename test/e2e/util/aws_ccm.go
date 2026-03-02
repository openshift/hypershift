package util

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	awsutil "github.com/openshift/hypershift/cmd/infra/aws/util"

	awsrequest "github.com/aws/aws-sdk-go/aws/request"
	"github.com/aws/aws-sdk-go/service/elbv2"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"

	crclient "sigs.k8s.io/controller-runtime/pkg/client"

	gomega "github.com/onsi/gomega"
)

// E2eTestConfig is a configuration struct for AWS CCM tests.
type E2eTestConfig struct {
	T   *testing.T
	Ctx context.Context
	// G                     gomega.Gomega
	MgtClient             crclient.Client
	GuestClient           crclient.Client
	HostedCluster         *hyperv1.HostedCluster
	FeatureSet            string
	ControlPlaneNamespace string
	FeatureGateEnabled    bool
	AWSCredsFile          string
	AWSRegion             string
	Platform              hyperv1.PlatformType
}

// EnsureAWSCCMWithCustomizations implements tests that exercise AWS CCM controller for critical features.
// This test is only supported on platform AWS, as well runs only when the feature gate AWSServiceLBNetworkSecurityGroup is enabled.
// A hosted cluster with TechPreviewNoUpgrade feature set is supported.
// It must skip tests not enabled in the feature set.
func EnsureAWSCCMWithCustomizations(cfg *E2eTestConfig) {
	cfg.T.Run("EnsureAWSCCMWithCustomizations", func(t *testing.T) {
		if cfg.Platform != hyperv1.AWSPlatform {
			cfg.T.Skip("test only supported on platform AWS")
		}
		cfg.T.Logf("Testing AWS CCM with customizations on platform %s", cfg.Platform)
		cfg.T.Logf("Feature gate enabled: %t", cfg.FeatureGateEnabled)
		EnsureAWSCCMWithCustomizations_tests(cfg)
	})
}

func EnsureAWSCCMWithCustomizations_tests(cfg *E2eTestConfig) {
	// Test case: Validate managed security groups in TechPreviewNoUpgrade feature set
	cfg.T.Run("When NLBSecurityGroupMode is enabled it must have config NLBSecurityGroupMode=Managed entry in cloud-config configmap", func(t *testing.T) {
		g := gomega.NewWithT(t)
		AtLeast(t, Version418)

		// Check if the feature is enabled in the feature set
		if !cfg.FeatureGateEnabled {
			t.Logf("Feature gate is not enabled in the feature set: %s", cfg.FeatureSet)
			t.Skipf("Skipping test: feature gate is not enabled in the feature set: %s", cfg.FeatureSet)
		}
		t.Logf("Validating aws-cloud-config ConfigMap contains NLBSecurityGroupMode = Managed")

		// Ensure the configuration is present when the feature gate is enabled
		EventuallyObject(t, cfg.Ctx, "NLBSecurityGroupMode = Managed entry exists in aws-cloud-config ConfigMap",
			func(ctx context.Context) (*corev1.ConfigMap, error) {
				cm := &corev1.ConfigMap{}
				err := cfg.MgtClient.Get(cfg.Ctx, crclient.ObjectKey{
					Namespace: cfg.ControlPlaneNamespace,
					Name:      "aws-cloud-config",
				}, cm)
				return cm, err
			},
			[]Predicate[*corev1.ConfigMap]{func(cm *corev1.ConfigMap) (done bool, reasons string, err error) {
				awsConf, exists := cm.Data["aws.conf"]
				if !exists {
					return false, "aws.conf key not found in ConfigMap", nil
				}

				t.Logf("verifying NLBSecurityGroupMode is present in cloud config")
				g.Expect(awsConf).To(gomega.ContainSubstring("NLBSecurityGroupMode"),
					"NLBSecurityGroupMode must be present in cloud-config when feature gate is enabled")

				t.Logf("verifying NLBSecurityGroupMode is set to Managed")
				g.Expect(awsConf).To(gomega.MatchRegexp(`NLBSecurityGroupMode\s*=\s*Managed`),
					"NLBSecurityGroupMode must be set to 'Managed' in aws-config when feature gate is enabled")

				t.Logf("Successfully validated cloud-config contains NLBSecurityGroupMode = Managed")

				return true, "Successfully validated aws-config", nil
			},
			},
			WithTimeout(2*time.Minute),
		)

	})

	// Test case: Create custom service type NLB in the hosted cluster, the NLB resource must
	// have a security group attached to it
	// Note: this test must executed only when the feature gate AWSServiceLBNetworkSecurityGroup is enabled.
	cfg.T.Run("When AWSServiceLBNetworkSecurityGroup is enabled it must create a LoadBalancer NLB with managed security group attached", func(t *testing.T) {
		g := gomega.NewWithT(t)

		AtLeast(t, Version418)
		if !cfg.FeatureGateEnabled {
			t.Logf("Feature gate is not enabled in the feature set: %s", cfg.FeatureSet)
			t.Skipf("Skipping test: feature gate is not enabled in the feature set: %s", cfg.FeatureSet)
		}

		// Create a test namespace in the guest cluster
		testNS := &corev1.Namespace{}
		testNS.Name = "test-ccm-nlb-sg"
		t.Logf("Creating test namespace %s in guest cluster", testNS.Name)
		err := cfg.GuestClient.Create(cfg.Ctx, testNS)
		g.Expect(err).NotTo(gomega.HaveOccurred(), "failed to create test namespace")
		defer func() {
			t.Logf("Cleaning up test namespace %s", testNS.Name)
			_ = cfg.GuestClient.Delete(cfg.Ctx, testNS)
		}()

		// Create a LoadBalancer service
		testSvc := &corev1.Service{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "test-ccm-nlb-sg-svc",
				Namespace: testNS.Name,
				Annotations: map[string]string{
					"service.beta.kubernetes.io/aws-load-balancer-type": "nlb",
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
		err = cfg.GuestClient.Create(cfg.Ctx, testSvc)
		g.Expect(err).NotTo(gomega.HaveOccurred(), "failed to create LoadBalancer service")

		// Wait for the LoadBalancer to be provisioned and get hostname
		var lbHostname string
		EventuallyObject(cfg.T, cfg.Ctx, "LoadBalancer service to have ingress hostname",
			func(ctx context.Context) (*corev1.Service, error) {
				svc := &corev1.Service{}
				err := cfg.GuestClient.Get(cfg.Ctx, crclient.ObjectKey{
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
		awsSession := awsutil.NewSession("e2e-ccm-nlb-sg", cfg.AWSCredsFile, "", "", cfg.AWSRegion)
		g.Expect(awsSession).NotTo(gomega.BeNil(), "failed to create AWS session")

		awsConfig := awsutil.NewConfig()
		g.Expect(awsConfig).NotTo(gomega.BeNil(), "failed to create AWS config")

		elbv2Client := elbv2.New(awsSession, awsConfig)
		g.Expect(elbv2Client).NotTo(gomega.BeNil(), "failed to create ELBv2 client")

		describeLBInput := &elbv2.DescribeLoadBalancersInput{
			Names: []*string{&lbName},
		}

		// Wait for the load balancer to exist and become active before validating attributes like SecurityGroups.
		// This avoids flakes where the Service is created but the LB is still provisioning.
		t.Logf("Waiting for load balancer %q to become available (up to ~3 minutes)", lbName)
		waiterDelay := func(attempt int) time.Duration {
			// Backoff: 5s, 10s, 20s, then cap at 30s.
			switch {
			case attempt <= 1:
				return 5 * time.Second
			case attempt == 2:
				return 10 * time.Second
			case attempt == 3:
				return 20 * time.Second
			default:
				return 30 * time.Second
			}
		}
		// 9 attempts with the delay profile above waits ~215s between attempts (plus request time),
		// satisfying the "wait at least 3 minutes" requirement before failing.
		err = elbv2Client.WaitUntilLoadBalancerAvailableWithContext(
			cfg.Ctx,
			describeLBInput,
			awsrequest.WithWaiterMaxAttempts(9),
			awsrequest.WithWaiterDelay(waiterDelay),
		)
		g.Expect(err).NotTo(gomega.HaveOccurred(), "load balancer did not become available in time")

		// Describe the load balancer
		t.Logf("Describing load balancer to check for security groups")

		describeLBOutput, err := elbv2Client.DescribeLoadBalancersWithContext(cfg.Ctx, describeLBInput)
		g.Expect(err).NotTo(gomega.HaveOccurred(), "failed to describe load balancer")
		g.Expect(len(describeLBOutput.LoadBalancers)).To(gomega.BeNumerically(">", 0), "no load balancers found with name %s", lbName)

		lb := describeLBOutput.LoadBalancers[0]
		t.Logf("Load balancer ARN: %s", *lb.LoadBalancerArn)
		t.Logf("Load balancer Type: %s", *lb.Type)
		t.Logf("Load balancer Security Groups: %v", lb.SecurityGroups)

		// Verify security groups are attached
		g.Expect(len(lb.SecurityGroups)).To(gomega.BeNumerically(">", 0), "load balancer should have security groups attached when NLBSecurityGroupMode = Managed")

		t.Logf("Successfully validated that load balancer has %d security group(s) attached", len(lb.SecurityGroups))
		for i, sg := range lb.SecurityGroups {
			t.Logf("  Security Group %d: %s", i+1, *sg)
		}

	})

	// Test case: validate the load balancer name extraction function from the DNS hostname.
	cfg.T.Run("When extracting a load balancer name from a DNS hostname it should drop only the last hyphen segment", func(t *testing.T) {
		cases := []struct {
			name     string
			hostname string
			want     string
		}{
			{
				name:     "NLB with multiple hyphens in name",
				hostname: "e2e-v7-fnt8p-ext-9a316db0952d7e14.elb.us-east-1.amazonaws.com",
				want:     "e2e-v7-fnt8p-ext",
			},
			{
				name:     "NLB with only hashed name and suffix",
				hostname: "af1c7bcc09ce1420db0292d91f0dad1f-f4ad6ce6794c3afd.elb.us-east-1.amazonaws.com",
				want:     "af1c7bcc09ce1420db0292d91f0dad1f",
			},
			{
				name:     "Classic ELB style hostname",
				hostname: "a7f9d8c870a2b44c39d9565e2ec22e81-1194117244.us-east-1.elb.amazonaws.com",
				want:     "a7f9d8c870a2b44c39d9565e2ec22e81",
			},
			{
				name:     "Hostname without dot",
				hostname: "foo-bar-baz-123",
				want:     "foo-bar-baz",
			},
			{
				name:     "Hostname without hyphen",
				hostname: "foo.elb.us-east-1.amazonaws.com",
				want:     "foo",
			},
		}

		for _, tc := range cases {
			t.Run(tc.name, func(t *testing.T) {
				got := extractLoadBalancerNameFromHostname(tc.hostname)
				if got != tc.want {
					t.Fatalf("unexpected load balancer name extracted from hostname %q: got %q, want %q", tc.hostname, got, tc.want)
				}
			})
		}
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
	lastHyphen := strings.LastIndex(firstLabel, "-")
	if lastHyphen == -1 {
		return firstLabel
	}
	return firstLabel[:lastHyphen]
}
