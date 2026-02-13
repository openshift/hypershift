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
	"context"
	"fmt"
	"strings"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/openshift/hypershift/test/e2e/v2/internal"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	crclient "sigs.k8s.io/controller-runtime/pkg/client"
)

var infraWorkloads = internal.GetControlPlaneInfrastructureWorkloads()

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

// InfrastructureRegistryValidationTest registers tests for infrastructure workload registry validation
func InfrastructureRegistryValidationTest(getTestCtx internal.TestContextGetter) {
	Context("Infrastructure registry validation", func() {
		// Label("Informing"): failures skip (non-blocking) until registry is complete
		It("all pods in infrastructure namespaces should belong to known workloads", Label("Informing"), func() {
			testCtx := getTestCtx()

			// Track unmatched pods across all infrastructure namespaces
			var podsNotBelongingToWorkloads []string

			// Check each infrastructure namespace
			for _, namespace := range internal.GetInfrastructureNamespaces() {
				// Check if namespace exists
				ns := &corev1.Namespace{}
				err := testCtx.MgmtClient.Get(context.Background(), crclient.ObjectKey{Name: namespace}, ns)
				if apierrors.IsNotFound(err) {
					// Namespace doesn't exist, skip
					continue
				}
				Expect(err).NotTo(HaveOccurred(), "failed to get namespace %s", namespace)

				// List all pods in the namespace
				podList := &corev1.PodList{}
				err = testCtx.MgmtClient.List(context.Background(), podList, &crclient.ListOptions{
					Namespace: namespace,
				})
				Expect(err).NotTo(HaveOccurred(), "failed to list pods in namespace %s", namespace)

				// Check each pod
				for _, pod := range podList.Items {
					belongsToWorkload := false
					for _, workload := range infraWorkloads {
						if workload.Namespace == namespace && workload.MatchesPod(pod) {
							belongsToWorkload = true
							break
						}
					}

					if !belongsToWorkload {
						podsNotBelongingToWorkloads = append(podsNotBelongingToWorkloads,
							fmt.Sprintf("pod %s/%s", pod.Namespace, pod.Name))
					}
				}
			}

			Expect(podsNotBelongingToWorkloads).To(BeEmpty(),
				"The following pods do not belong to any predefined infrastructure workload:\n%s",
				strings.Join(podsNotBelongingToWorkloads, "\n"))
		})
	})
}

// InfrastructureResourceRequestsTest registers tests for infrastructure workload resource requests
func InfrastructureResourceRequestsTest(getTestCtx internal.TestContextGetter) {
	Context("Container resource requests", func() {
		for _, workload := range infraWorkloads {
			workload := workload // capture range variable

			It(fmt.Sprintf("should have resource requests for %s containers", workload.Name), func() {
				testCtx := getTestCtx()

				// Check if namespace exists
				ns := &corev1.Namespace{}
				err := testCtx.MgmtClient.Get(context.Background(), crclient.ObjectKey{Name: workload.Namespace}, ns)
				if apierrors.IsNotFound(err) {
					Skip(fmt.Sprintf("namespace %s not found", workload.Namespace))
				}
				Expect(err).NotTo(HaveOccurred(), "failed to get namespace %s", workload.Namespace)

				// List pods matching the workload selector
				podList := &corev1.PodList{}
				err = testCtx.MgmtClient.List(context.Background(), podList, &crclient.ListOptions{
					Namespace: workload.Namespace,
				})
				Expect(err).NotTo(HaveOccurred(), "failed to list pods in namespace %s", workload.Namespace)

				// Filter pods that match this workload
				var matchingPods []corev1.Pod
				for _, pod := range podList.Items {
					if workload.MatchesPod(pod) {
						matchingPods = append(matchingPods, pod)
					}
				}

				// Track failures across all matching pods
				var failures []string
				for _, pod := range matchingPods {
					// Validate regular containers
					failures = append(failures, validateContainerResourceRequests(pod.Namespace, pod.Name, pod.Spec.Containers)...)
					// Validate init containers
					failures = append(failures, validateContainerResourceRequests(pod.Namespace, pod.Name, pod.Spec.InitContainers)...)
				}

				// Report all failures at once for better visibility
				if len(failures) > 0 {
					Fail(strings.Join(failures, "\n"))
				}
			})
		}
	})
}

// RegisterControlPlaneInfrastructureTests registers all control plane infrastructure tests
func RegisterControlPlaneInfrastructureTests(getTestCtx internal.TestContextGetter) {
	InfrastructureRegistryValidationTest(getTestCtx)
	InfrastructureResourceRequestsTest(getTestCtx)
}

var _ = Describe("Control Plane Infrastructure Workloads", Label("control-plane-workloads"), func() {
	var testCtx *internal.TestContext
	BeforeEach(func() {
		testCtx = internal.GetTestContext()
		Expect(testCtx).NotTo(BeNil(), "test context should be set up in BeforeSuite")
	})
	RegisterControlPlaneInfrastructureTests(func() *internal.TestContext { return testCtx })
})
