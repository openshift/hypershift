//go:build e2ev2

/*
Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package tests

import (
	"fmt"
	"os"
	"path"
	"path/filepath"
	"strings"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	"github.com/openshift/hypershift/test/e2e/v2/internal"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	crclient "sigs.k8s.io/controller-runtime/pkg/client"

	"google.golang.org/api/compute/v1"
	"google.golang.org/api/option"
)

const (
	gcpWIFCredentialsFile        = "wif-cred.json"
	gcpControlPlaneProjectIDFile = "control-plane-project-id"
	backendServiceAnnotation     = "service.kubernetes.io/backend-service"
)

// GCPPrivateServiceConnectTest registers tests that validate PSC resource correctness on GCP.
func GCPPrivateServiceConnectTest(getTestCtx internal.TestContextGetter) {
	Context("GCP Private Service Connect", Label("GCP", "PSC"), func() {
		BeforeEach(func() {
			getTestCtx().SkipIfNotPlatform(hyperv1.GCPPlatform)
		})

		// GCP enforces that the NAT subnet and the forwarding rule must belong to the same VPC
		// when creating a Service Attachment. A True GCPServiceAttachmentAvailable condition
		// is therefore proof that the controller selected a subnet from the correct VPC.
		It("should have GCPServiceAttachmentAvailable condition set to True", func() {
			testCtx := getTestCtx()

			// Find the GCPPrivateServiceConnect CR in the control plane namespace.
			// There is exactly one GCPPrivateServiceConnect per hosted cluster.
			pscList := &hyperv1.GCPPrivateServiceConnectList{}
			Expect(testCtx.MgmtClient.List(testCtx.Context, pscList,
				crclient.InNamespace(testCtx.ControlPlaneNamespace),
			)).To(Succeed())
			Expect(pscList.Items).NotTo(BeEmpty(),
				"expected at least one GCPPrivateServiceConnect in namespace %s", testCtx.ControlPlaneNamespace)

			psc := pscList.Items[0]

			// Assert both spec fields are populated — the controller successfully resolved
			// the forwarding rule and the VPC-scoped NAT subnet.
			Expect(string(psc.Spec.ForwardingRuleName)).NotTo(BeEmpty(),
				"GCPPrivateServiceConnect %s should have ForwardingRuleName set", psc.Name)
			Expect(string(psc.Spec.NATSubnet)).NotTo(BeEmpty(),
				"GCPPrivateServiceConnect %s should have NATSubnet set", psc.Name)

			// Assert GCP accepted the Service Attachment. GCP rejects a Service Attachment
			// whose NAT subnet is not in the same VPC as the forwarding rule, so a True
			// condition here implicitly validates VPC correctness without requiring GCP
			// credentials in the test binary.
			found := false
			for _, cond := range psc.Status.Conditions {
				if cond.Type == string(hyperv1.GCPServiceAttachmentAvailable) {
					found = true
					Expect(cond.Status).To(Equal(metav1.ConditionTrue),
						"GCPServiceAttachmentAvailable condition should be True on GCPPrivateServiceConnect %s: %s",
						psc.Name, cond.Message)
					break
				}
			}
			Expect(found).To(BeTrue(),
				"expected GCPServiceAttachmentAvailable condition on GCPPrivateServiceConnect %s", psc.Name)
		})
	})
}

// GCPResourceLabelsTest registers tests that validate labels on GCP resources
// created for Private Service Connect and router load balancers.
func GCPResourceLabelsTest(getTestCtx internal.TestContextGetter) {
	Context("GCP resource labels", Label("GCP", "resource-labels"), func() {
		BeforeEach(func() {
			getTestCtx().SkipIfNotPlatform(hyperv1.GCPPlatform)
		})

		It("should apply HostedCluster resource labels to PSC and router forwarding resources", func() {
			tc := getTestCtx()
			hc, err := tc.GetHostedCluster()
			Expect(err).NotTo(HaveOccurred())
			Expect(hc.Spec.Platform.GCP).NotTo(BeNil(), "HostedCluster %s/%s should have a GCP platform spec", hc.Namespace, hc.Name)

			expectedLabels := make(map[string]string, len(hc.Spec.Platform.GCP.ResourceLabels))
			for _, label := range hc.Spec.Platform.GCP.ResourceLabels {
				Expect(label.Value).NotTo(BeNil(), "GCP resource label %q should have a value", label.Key)
				expectedLabels[label.Key] = *label.Value
			}
			if len(expectedLabels) == 0 {
				Skip("HostedCluster has no GCP resource labels configured")
			}

			sharedDir := internal.GetEnvVarValue("SHARED_DIR")
			if sharedDir == "" {
				Skip("SHARED_DIR is required to access GCP workload identity credentials")
			}
			credentialsFile := filepath.Join(sharedDir, gcpWIFCredentialsFile)
			if _, err := os.Stat(credentialsFile); err != nil {
				Skip(fmt.Sprintf("GCP workload identity credentials are unavailable at %s: %v", credentialsFile, err))
			}
			controlPlaneProjectID, err := readGCPProjectID(filepath.Join(sharedDir, gcpControlPlaneProjectIDFile))
			Expect(err).NotTo(HaveOccurred())

			computeService, err := compute.NewService(tc.Context,
				option.WithAuthCredentialsFile(option.ExternalAccount, credentialsFile),
				option.WithScopes(compute.ComputeScope),
			)
			Expect(err).NotTo(HaveOccurred(), "failed to create GCP Compute client")

			pscList := &hyperv1.GCPPrivateServiceConnectList{}
			Expect(tc.MgmtClient.List(tc.Context, pscList, crclient.InNamespace(tc.ControlPlaneNamespace))).To(Succeed())
			Expect(pscList.Items).To(HaveLen(1), "expected exactly one GCPPrivateServiceConnect in namespace %s", tc.ControlPlaneNamespace)
			serviceAttachmentName := pscList.Items[0].Status.ServiceAttachmentName
			Expect(serviceAttachmentName).NotTo(BeEmpty(), "GCPPrivateServiceConnect %s should have ServiceAttachmentName set", pscList.Items[0].Name)

			customerProjectID := hc.Spec.Platform.GCP.Project
			region := hc.Spec.Platform.GCP.Region
			Expect(customerProjectID).NotTo(BeEmpty(), "HostedCluster GCP project should be set")
			Expect(region).NotTo(BeEmpty(), "HostedCluster GCP region should be set")

			pscAddressName := serviceAttachmentName + "-ip"
			pscAddress, err := computeService.Addresses.Get(customerProjectID, region, pscAddressName).Context(tc.Context).Do()
			Expect(err).NotTo(HaveOccurred(), "failed to get PSC Address %s", pscAddressName)
			assertGCPResourceLabels(pscAddressName, pscAddress.Labels, expectedLabels)

			pscForwardingRuleName := serviceAttachmentName + "-endpoint"
			pscForwardingRule, err := computeService.ForwardingRules.Get(customerProjectID, region, pscForwardingRuleName).Context(tc.Context).Do()
			Expect(err).NotTo(HaveOccurred(), "failed to get PSC ForwardingRule %s", pscForwardingRuleName)
			assertGCPResourceLabels(pscForwardingRuleName, pscForwardingRule.Labels, expectedLabels)

			routerService := &corev1.Service{}
			Expect(tc.MgmtClient.Get(tc.Context, crclient.ObjectKey{Namespace: tc.ControlPlaneNamespace, Name: "router"}, routerService)).To(Succeed())
			backendServiceName := routerService.Annotations[backendServiceAnnotation]
			Expect(backendServiceName).NotTo(BeEmpty(), "router Service should have %s annotation", backendServiceAnnotation)

			managementForwardingRules, err := computeService.ForwardingRules.List(controlPlaneProjectID, region).Context(tc.Context).Do()
			Expect(err).NotTo(HaveOccurred(), "failed to list management-project forwarding rules")
			Expect(managementForwardingRules.Items).NotTo(BeEmpty(), "expected management-project forwarding rules in project %s", controlPlaneProjectID)

			var routerForwardingRule *compute.ForwardingRule
			for _, forwardingRule := range managementForwardingRules.Items {
				if path.Base(forwardingRule.BackendService) == backendServiceName {
					routerForwardingRule = forwardingRule
					break
				}
			}
			Expect(routerForwardingRule).NotTo(BeNil(), "expected a management-project forwarding rule for router backend service %s", backendServiceName)
			assertGCPResourceLabels(routerForwardingRule.Name, routerForwardingRule.Labels, expectedLabels)
		})
	})
}

func assertGCPResourceLabels(resourceName string, actual, expected map[string]string) {
	for key, value := range expected {
		Expect(actual).To(HaveKeyWithValue(key, value), "GCP resource %s should have label %s=%s", resourceName, key, value)
	}
}

func readGCPProjectID(filename string) (string, error) {
	contents, err := os.ReadFile(filename)
	if err != nil {
		return "", fmt.Errorf("read GCP project ID file %s: %w", filename, err)
	}
	projectID := strings.TrimSpace(string(contents))
	if projectID == "" {
		return "", fmt.Errorf("GCP project ID file %s is empty", filename)
	}
	return projectID, nil
}

// RegisterGCPPSCTests registers all GCP Private Service Connect tests.
func RegisterGCPPSCTests(getTestCtx internal.TestContextGetter) {
	GCPPrivateServiceConnectTest(getTestCtx)
}

var _ = Describe("[sig-hypershift][Jira:Hypershift][Feature:GCPPrivateServiceConnect] GCP Private Service Connect", Label("gcp-psc"), func() {
	var testCtx *internal.TestContext

	BeforeEach(func() {
		testCtx = internal.GetTestContext()
		Expect(testCtx).NotTo(BeNil(), "test context should be set up in BeforeSuite")
	})

	RegisterGCPPSCTests(func() *internal.TestContext { return testCtx })
})

var _ = Describe("[sig-hypershift][Jira:Hypershift][Feature:GCPResourceLabels] GCP Resource Labels", Label("gcp-resource-labels"), func() {
	var testCtx *internal.TestContext

	BeforeEach(func() {
		testCtx = internal.GetTestContext()
		Expect(testCtx).NotTo(BeNil(), "test context should be set up in BeforeSuite")
	})

	GCPResourceLabelsTest(func() *internal.TestContext { return testCtx })
})
