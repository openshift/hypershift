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
	"strings"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	"github.com/openshift/hypershift/test/e2e/v2/internal"

	corev1 "k8s.io/api/core/v1"
)

// GCPCloudControllerManagerTest registers tests for GCP CCM node initialization validation.
// These tests verify that the CCM workload is producing the expected effects on the hosted cluster:
// providerID assignment, topology labels, and taint removal.
func GCPCloudControllerManagerTest(getTestCtx internal.TestContextGetter) {
	Context("GCP Cloud Controller Manager", Label("GCP", "CCM"), func() {
		BeforeEach(func() {
			testCtx := getTestCtx()
			hc := testCtx.GetHostedCluster()
			if hc == nil || hc.Spec.Platform.Type != hyperv1.GCPPlatform {
				Skip("GCP Cloud Controller Manager test is only for GCP platform")
			}
		})

		Context("When nodes are initialized by the CCM", func() {
			var nodes *corev1.NodeList

			BeforeEach(func() {
				testCtx := getTestCtx()
				hostedClusterClient := testCtx.GetHostedClusterClient()
				Expect(hostedClusterClient).NotTo(BeNil(), "hosted cluster client is nil; HostedCluster may not have KubeConfig status set")

				nodes = &corev1.NodeList{}
				Expect(hostedClusterClient.List(context.Background(), nodes)).To(Succeed())
				Expect(nodes.Items).NotTo(BeEmpty(), "cluster should have nodes")
			})

			It("should set providerID on all nodes", func() {
				testCtx := getTestCtx()
				hc := testCtx.GetHostedCluster()
				Expect(hc.Spec.Platform.GCP).NotTo(BeNil(), "GCP platform spec must be set for GCP HostedCluster %s/%s", hc.Namespace, hc.Name)
				gcpProject := hc.Spec.Platform.GCP.Project

				for _, node := range nodes.Items {
					Expect(node.Spec.ProviderID).NotTo(BeEmpty(),
						"node %s should have providerID set", node.Name)
					// GCP providerID format: gce://<project>/<zone>/<instance-name>
					Expect(node.Spec.ProviderID).To(HavePrefix("gce://"+gcpProject+"/"),
						"node %s providerID should reference project %s", node.Name, gcpProject)
					parts := strings.Split(node.Spec.ProviderID, "/")
					Expect(parts).To(HaveLen(5),
						"node %s providerID should have format gce://<project>/<zone>/<instance-name>", node.Name)
				}
			})

			It("should set zone and region topology labels on all nodes", func() {
				for _, node := range nodes.Items {
					zone, ok := node.Labels["topology.kubernetes.io/zone"]
					Expect(ok).To(BeTrue(),
						"node %s should have topology.kubernetes.io/zone label", node.Name)
					Expect(zone).NotTo(BeEmpty(),
						"node %s zone label should not be empty", node.Name)

					region, ok := node.Labels["topology.kubernetes.io/region"]
					Expect(ok).To(BeTrue(),
						"node %s should have topology.kubernetes.io/region label", node.Name)
					Expect(region).NotTo(BeEmpty(),
						"node %s region label should not be empty", node.Name)
				}
			})

			It("should remove the uninitialized taint from all nodes", func() {
				for _, node := range nodes.Items {
					for _, taint := range node.Spec.Taints {
						Expect(taint.Key).NotTo(Equal("node.cloudprovider.kubernetes.io/uninitialized"),
							"node %s should not have the cloud provider uninitialized taint", node.Name)
					}
				}
			})
		})
	})
}

// RegisterHostedClusterCCMTests registers all hosted cluster CCM tests
func RegisterHostedClusterCCMTests(getTestCtx internal.TestContextGetter) {
	GCPCloudControllerManagerTest(getTestCtx)
}

var _ = Describe("Hosted Cluster CCM", Label("hosted-cluster-ccm"), func() {
	var testCtx *internal.TestContext

	BeforeEach(func() {
		testCtx = internal.GetTestContext()
		Expect(testCtx).NotTo(BeNil(), "test context should be set up in BeforeSuite")
	})

	RegisterHostedClusterCCMTests(func() *internal.TestContext { return testCtx })
})
