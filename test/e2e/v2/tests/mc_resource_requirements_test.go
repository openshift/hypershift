//go:build e2ev2
// +build e2ev2

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
	"context"
	"strings"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/openshift/hypershift/test/e2e/v2/internal"

	corev1 "k8s.io/api/core/v1"
	crclient "sigs.k8s.io/controller-runtime/pkg/client"
)

// resourceRequestExemption represents a workload exempted from resource request validation
type resourceRequestExemption struct {
	Namespace     string
	PodPrefix     string
	Container     string
	TrackingIssue string
}

// resourceRequestExemptions lists workload that are temporarily exempt from resource request validation.
// TODO: Remove these exemptions once the tracked issues are resolved.
var resourceRequestExemptions = []resourceRequestExemption{
	{Namespace: "hypershift-sharedingress", PodPrefix: "router-", Container: "private-router", TrackingIssue: "GCP-304"},
	{Namespace: "hypershift-sharedingress", PodPrefix: "router-", Container: "config-generator", TrackingIssue: "GCP-304"},
	{Namespace: "hypershift-request-serving-node-placeholders", PodPrefix: "placeholder-", Container: "placeholder", TrackingIssue: "GCP-304"},
	{Namespace: "hypershift", PodPrefix: "operator-", Container: "init-environment", TrackingIssue: "GCP-304"},
}

// isExempt checks if a given container in a pod is exempt from resource request validation
func isExempt(namespace, podName, containerName string) bool {
	for _, exemption := range resourceRequestExemptions {
		if exemption.Namespace == namespace &&
			strings.HasPrefix(podName, exemption.PodPrefix) &&
			exemption.Container == containerName {
			return true
		}
	}
	return false
}

// validateContainerResourceRequests validates resource requests for a slice of containers
func validateContainerResourceRequests(podNamespace, podName string, containers []corev1.Container) []string {
	var failures []string
	for _, container := range containers {
		if isExempt(podNamespace, podName, container.Name) {
			continue
		}

		if container.Resources.Requests == nil {
			failures = append(failures, "container "+container.Name+" in pod "+podNamespace+"/"+podName+" missing resource requests")
			continue
		}

		if _, hasCPU := container.Resources.Requests[corev1.ResourceCPU]; !hasCPU {
			failures = append(failures, "container "+container.Name+" in pod "+podNamespace+"/"+podName+" missing CPU resource request")
		}

		if _, hasMemory := container.Resources.Requests[corev1.ResourceMemory]; !hasMemory {
			failures = append(failures, "container "+container.Name+" in pod "+podNamespace+"/"+podName+" missing memory resource request")
		}
	}
	return failures
}

// ManagementClusterNamespaceResourceRequestsTest registers tests for management cluster namespace resource requests
func ManagementClusterNamespaceResourceRequestsTest(getTestCtx internal.TestContextGetter) {
	It("all containers in HyperShift component namespaces should have resource requests", func() {
		testCtx := getTestCtx()

		// Discover all namespaces with the HyperShift component label
		namespaceList := &corev1.NamespaceList{}
		err := testCtx.MgmtClient.List(context.Background(), namespaceList, crclient.HasLabels{
			"hypershift.openshift.io/component",
		})
		Expect(err).NotTo(HaveOccurred())
		Expect(namespaceList.Items).NotTo(BeEmpty(), "no HyperShift component namespaces found")

		// Track failures across all namespaces
		var failures []string

		// Validate resource requests for pods in each discovered namespace
		for _, namespace := range namespaceList.Items {
			podList := &corev1.PodList{}
			err := testCtx.MgmtClient.List(context.Background(), podList, &crclient.ListOptions{
				Namespace: namespace.Name,
			})
			Expect(err).NotTo(HaveOccurred())

			for _, pod := range podList.Items {
				// Validate regular containers
				failures = append(failures, validateContainerResourceRequests(pod.Namespace, pod.Name, pod.Spec.Containers)...)
				// Validate init containers
				failures = append(failures, validateContainerResourceRequests(pod.Namespace, pod.Name, pod.Spec.InitContainers)...)
			}
		}

		// Report all failures at once for better visibility
		if len(failures) > 0 {
			Fail(strings.Join(failures, "\n"))
		}
	})
}

var _ = Describe("Management Cluster Resource Requirements", Label("mc-resources"), func() {
	var testCtx *internal.TestContext
	BeforeEach(func() {
		testCtx = internal.GetTestContext()
		Expect(testCtx).NotTo(BeNil(), "test context should be set up in BeforeSuite")
	})
	ManagementClusterNamespaceResourceRequestsTest(func() *internal.TestContext { return testCtx })
})
