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

// cloud_integration_test.go validates that cloud provider integration is functioning
// correctly from the guest cluster's perspective. This includes CCM node initialization
// (providerID, topology labels, taint removal) and LoadBalancer service provisioning.
//
// Unlike control_plane_workloads_test.go (which checks deployment health on the
// management cluster), these tests verify that control plane components are actually
// producing the expected effects on the guest cluster.
//
// Each platform adds its own labeled Context block. Platform-specific tests are skipped
// automatically when running against a different platform.

package tests

import (
	"context"
	"fmt"
	"strings"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	"github.com/openshift/hypershift/test/e2e/v2/internal"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"
	crclient "sigs.k8s.io/controller-runtime/pkg/client"
)

var _ = Describe("Cloud Integration", Label("cloud-integration"), func() {
	var testCtx *internal.TestContext

	BeforeEach(func() {
		testCtx = internal.GetTestContext()
		Expect(testCtx).NotTo(BeNil(), "test context must be initialized")
	})

	// GCP Cloud Controller Manager tests
	Context("GCP", Label("GCP", "CCM"), func() {
		BeforeEach(func() {
			hc := testCtx.GetHostedCluster()
			if hc == nil || hc.Spec.Platform.Type != hyperv1.GCPPlatform {
				Skip("Test requires a GCP HostedCluster")
			}
		})

		Context("When nodes are initialized by the CCM", func() {
			It("should set providerID on all nodes", func() {
				guestClient := testCtx.GetGuestClient()
				Expect(guestClient).NotTo(BeNil(), "guest client is required")

				nodes := &corev1.NodeList{}
				Expect(guestClient.List(context.Background(), nodes)).To(Succeed())
				Expect(nodes.Items).NotTo(BeEmpty(), "cluster should have nodes")

				hc := testCtx.GetHostedCluster()
				gcpProject := hc.Spec.Platform.GCP.Project

				for _, node := range nodes.Items {
					Expect(node.Spec.ProviderID).NotTo(BeEmpty(),
						"node %s should have providerID set", node.Name)
					// GCP providerID format: gce://<project>/<zone>/<instance-name>
					Expect(node.Spec.ProviderID).To(HavePrefix("gce://"+gcpProject+"/"),
						"node %s providerID should reference project %s", node.Name, gcpProject)
					parts := strings.Split(node.Spec.ProviderID, "/")
					Expect(parts).To(HaveLen(5),
						"node %s providerID should have format gce://project/zone/instance", node.Name)
				}
			})

			It("should set zone and region topology labels on all nodes", func() {
				guestClient := testCtx.GetGuestClient()
				Expect(guestClient).NotTo(BeNil(), "guest client is required")

				nodes := &corev1.NodeList{}
				Expect(guestClient.List(context.Background(), nodes)).To(Succeed())
				Expect(nodes.Items).NotTo(BeEmpty())

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
				guestClient := testCtx.GetGuestClient()
				Expect(guestClient).NotTo(BeNil(), "guest client is required")

				nodes := &corev1.NodeList{}
				Expect(guestClient.List(context.Background(), nodes)).To(Succeed())
				Expect(nodes.Items).NotTo(BeEmpty())

				for _, node := range nodes.Items {
					for _, taint := range node.Spec.Taints {
						Expect(taint.Key).NotTo(Equal("node.cloudprovider.kubernetes.io/uninitialized"),
							"node %s should not have the cloud provider uninitialized taint", node.Name)
					}
				}
			})
		})

		Context("When a LoadBalancer service is created", func() {
			const (
				testNamespace   = "default"
				testServiceName = "ccm-lb-test"
			)

			AfterEach(func() {
				guestClient := testCtx.GetGuestClient()
				if guestClient == nil {
					return
				}
				svc := &corev1.Service{
					ObjectMeta: metav1.ObjectMeta{
						Name:      testServiceName,
						Namespace: testNamespace,
					},
				}
				_ = guestClient.Delete(context.Background(), svc)
			})

			It("should provision a GCP load balancer and assign an external IP", func() {
				guestClient := testCtx.GetGuestClient()
				Expect(guestClient).NotTo(BeNil(), "guest client is required")

				svc := &corev1.Service{
					ObjectMeta: metav1.ObjectMeta{
						Name:      testServiceName,
						Namespace: testNamespace,
					},
					Spec: corev1.ServiceSpec{
						Type: corev1.ServiceTypeLoadBalancer,
						Ports: []corev1.ServicePort{
							{
								Port:       80,
								TargetPort: intstr.FromInt32(8080),
								Protocol:   corev1.ProtocolTCP,
							},
						},
						Selector: map[string]string{
							"app": "ccm-lb-test",
						},
					},
				}
				Expect(guestClient.Create(context.Background(), svc)).To(Succeed())

				Eventually(func(g Gomega) {
					err := guestClient.Get(context.Background(),
						crclient.ObjectKeyFromObject(svc), svc)
					g.Expect(err).NotTo(HaveOccurred())
					g.Expect(svc.Status.LoadBalancer.Ingress).NotTo(BeEmpty(),
						"service should have LoadBalancer ingress")
					g.Expect(svc.Status.LoadBalancer.Ingress[0].IP).NotTo(BeEmpty(),
						"LoadBalancer should have an external IP")
				}).WithTimeout(5 * time.Minute).WithPolling(10 * time.Second).Should(Succeed())

				fmt.Fprintf(GinkgoWriter, "LoadBalancer external IP: %s\n",
					svc.Status.LoadBalancer.Ingress[0].IP)
			})
		})
	})
})