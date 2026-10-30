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
	"sort"
	"strings"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	"github.com/openshift/hypershift/support/gcputil"
	"github.com/openshift/hypershift/test/e2e/v2/internal"

	corev1 "k8s.io/api/core/v1"

	"google.golang.org/api/compute/v1"
	"google.golang.org/api/option"
)

// RegisterHostedClusterGCPTests registers tests for GCP-specific hosted cluster resources.
func RegisterHostedClusterGCPTests(getTestCtx internal.TestContextGetter) {
	GCPComputeResourceLabelsTest(getTestCtx)
	GCPLoadBalancerServiceResourceLabelsTest(getTestCtx)
}

// newGCPComputeClient creates a Compute client using the workload identity
// credentials made available to GCP E2E jobs.
func newGCPComputeClient(g Gomega, tc *internal.TestContext) *compute.Service {
	GinkgoHelper()
	sharedDir := internal.GetEnvVarValue("SHARED_DIR")
	if sharedDir == "" {
		Skip("SHARED_DIR is required to access GCP workload identity credentials")
	}
	credentialsFile := filepath.Join(sharedDir, gcpWIFCredentialsFile)
	if _, err := os.Stat(credentialsFile); err != nil {
		if os.IsNotExist(err) {
			Skip(fmt.Sprintf("GCP workload identity credentials are unavailable at %s", credentialsFile))
		}
		g.Expect(err).NotTo(HaveOccurred(), "failed to stat GCP workload identity credentials at %s", credentialsFile)
	}

	computeService, err := compute.NewService(tc.Context,
		option.WithAuthCredentialsFile(option.ExternalAccount, credentialsFile),
		option.WithScopes(compute.ComputeScope),
	)
	g.Expect(err).NotTo(HaveOccurred(), "failed to create GCP Compute client")
	return computeService
}

// GCPComputeResourceLabelsTest validates labels on NodePool VMs and their persistent disks.
func GCPComputeResourceLabelsTest(getTestCtx internal.TestContextGetter) {
	Context("GCP compute resource labels", Label("GCP", "resource-labels"), func() {
		BeforeEach(func() {
			getTestCtx().SkipIfNotPlatform(hyperv1.GCPPlatform)
		})

		It("should apply HostedCluster resource labels to VMs and their persistent disks", Label("resource-labels", internal.InformingLabel), func() {
			tc := getTestCtx()
			hc, err := tc.GetHostedCluster()
			Expect(err).NotTo(HaveOccurred(), "failed to get HostedCluster %s/%s", tc.ClusterNamespace, tc.ClusterName)
			Expect(hc.Spec.Platform.GCP).NotTo(BeNil(), "HostedCluster %s/%s should have a GCP platform spec", hc.Namespace, hc.Name)
			if len(hc.Spec.Platform.GCP.ResourceLabels) == 0 {
				Skip("HostedCluster has no GCP resource labels configured")
			}

			expectedLabels := make(map[string]string, len(hc.Spec.Platform.GCP.ResourceLabels))
			for _, label := range hc.Spec.Platform.GCP.ResourceLabels {
				Expect(label.Value).NotTo(BeNil(), "GCP resource label %q should have a value", label.Key)
				expectedLabels[label.Key] = *label.Value
			}

			computeService := newGCPComputeClient(Default, tc)

			hostedClusterClient, err := tc.GetHostedClusterClient(hc)
			Expect(err).NotTo(HaveOccurred(), "failed to create a client for HostedCluster %s/%s", hc.Namespace, hc.Name)
			nodes := &corev1.NodeList{}
			Expect(hostedClusterClient.List(tc.Context, nodes)).To(Succeed(), "failed to list nodes in HostedCluster %s/%s", hc.Namespace, hc.Name)
			Expect(nodes.Items).NotTo(BeEmpty(), "expected at least one hosted cluster node")

			customerProjectID := hc.Spec.Platform.GCP.Project
			Expect(customerProjectID).NotTo(BeEmpty(), "HostedCluster GCP project should be set")
			for _, node := range nodes.Items {
				providerIDParts := strings.Split(node.Spec.ProviderID, "/")
				Expect(providerIDParts).To(HaveLen(5), "node %s providerID should have format gce://<project>/<zone>/<instance-name>", node.Name)
				Expect(providerIDParts[0]).To(Equal("gce:"), "node %s providerID should be a GCE provider ID", node.Name)
				Expect(providerIDParts[1]).To(BeEmpty(), "node %s providerID should have format gce://<project>/<zone>/<instance-name>", node.Name)
				Expect(providerIDParts[2]).To(Equal(customerProjectID), "node %s providerID should reference the HostedCluster GCP project", node.Name)

				zone, instanceName := providerIDParts[3], providerIDParts[4]
				Expect(zone).NotTo(BeEmpty(), "node %s providerID should include an instance zone", node.Name)
				Expect(instanceName).NotTo(BeEmpty(), "node %s providerID should include an instance name", node.Name)

				instance, err := computeService.Instances.Get(customerProjectID, zone, instanceName).Context(tc.Context).Do()
				Expect(err).NotTo(HaveOccurred(), "failed to get VM %s for node %s", instanceName, node.Name)
				assertGCPResourceLabels(Default, instanceName, instance.Labels, expectedLabels)

				persistentDiskCount := 0
				for _, attachedDisk := range instance.Disks {
					if attachedDisk.Source == "" {
						continue
					}
					persistentDiskCount++
					diskName := path.Base(attachedDisk.Source)
					disk, err := computeService.Disks.Get(customerProjectID, zone, diskName).Context(tc.Context).Do()
					Expect(err).NotTo(HaveOccurred(), "failed to get persistent disk %s for VM %s", diskName, instanceName)
					assertGCPResourceLabels(Default, diskName, disk.Labels, expectedLabels)
				}
				Expect(persistentDiskCount).To(BeNumerically(">", 0), "VM %s should have at least one persistent disk", instanceName)
			}
		})
	})
}

// GCPLoadBalancerServiceResourceLabelsTest validates the annotations that the
// hosted-cluster config operator uses to propagate resource labels to legacy
// GCP load balancers.
func GCPLoadBalancerServiceResourceLabelsTest(getTestCtx internal.TestContextGetter) {
	Context("GCP load balancer Service resource labels", Label("GCP", "resource-labels"), func() {
		BeforeEach(func() {
			getTestCtx().SkipIfNotPlatform(hyperv1.GCPPlatform)
		})

		It("should apply HostedCluster resource labels to legacy load balancer Services", Label("resource-labels", internal.InformingLabel), func() {
			tc := getTestCtx()
			hc, err := tc.GetHostedCluster()
			Expect(err).NotTo(HaveOccurred(), "failed to get HostedCluster %s/%s", tc.ClusterNamespace, tc.ClusterName)
			Expect(hc.Spec.Platform.GCP).NotTo(BeNil(), "HostedCluster %s/%s should have a GCP platform spec", hc.Namespace, hc.Name)
			if len(hc.Spec.Platform.GCP.ResourceLabels) == 0 {
				Skip("HostedCluster has no GCP resource labels configured")
			}

			expectedLabels := make(map[string]string, len(hc.Spec.Platform.GCP.ResourceLabels))
			for _, label := range hc.Spec.Platform.GCP.ResourceLabels {
				Expect(label.Value).NotTo(BeNil(), "GCP resource label %q should have a value", label.Key)
				expectedLabels[label.Key] = *label.Value
			}

			hostedClusterClient, err := tc.GetHostedClusterClient(hc)
			Expect(err).NotTo(HaveOccurred(), "failed to create a client for HostedCluster %s/%s", hc.Namespace, hc.Name)
			services := &corev1.ServiceList{}
			Expect(hostedClusterClient.List(tc.Context, services)).To(Succeed(), "failed to list Services in HostedCluster %s/%s", hc.Namespace, hc.Name)
			Expect(services.Items).NotTo(BeEmpty(), "expected Services in HostedCluster %s/%s", hc.Namespace, hc.Name)

			legacyServiceCount := 0
			for i := range services.Items {
				service := &services.Items[i]
				if !isLegacyGCPLoadBalancerService(service) {
					continue
				}
				legacyServiceCount++

				actualLabels, annotationPresent, err := gcputil.ParseLBResourceLabelsAnnotation(service.Annotations)
				Expect(err).NotTo(HaveOccurred(), "Service %s/%s has an invalid %s annotation", service.Namespace, service.Name, gcputil.LBResourceLabelsAnnotation)
				Expect(annotationPresent).To(BeTrue(), "legacy GCP load balancer Service %s/%s should have %s annotation", service.Namespace, service.Name, gcputil.LBResourceLabelsAnnotation)
				for key, value := range expectedLabels {
					Expect(actualLabels).To(HaveKeyWithValue(key, value), "legacy GCP load balancer Service %s/%s should include label %s=%s", service.Namespace, service.Name, key, value)
				}

				managedLabelKeys := strings.Split(service.Annotations[gcputil.ManagedLBResourceLabelsAnnotation], ",")
				expectedLabelKeys := make([]string, 0, len(expectedLabels))
				for key := range expectedLabels {
					expectedLabelKeys = append(expectedLabelKeys, key)
				}
				sort.Strings(expectedLabelKeys)
				Expect(managedLabelKeys).To(Equal(expectedLabelKeys), "legacy GCP load balancer Service %s/%s should record all HyperShift-managed label keys", service.Namespace, service.Name)
			}
			Expect(legacyServiceCount).To(BeNumerically(">", 0), "expected at least one legacy GCP load balancer Service")
		})
	})
}

func isLegacyGCPLoadBalancerService(service *corev1.Service) bool {
	const (
		legacyRegionalInternalLoadBalancerClass = "networking.gke.io/l4-regional-internal-legacy"
		legacyRegionalExternalLoadBalancerClass = "networking.gke.io/l4-regional-external-legacy"
		l4RBSAnnotation                         = "cloud.google.com/l4-rbs"
		l4RBSEnabled                            = "enabled"
		netLBFinalizerV2                        = "gke.networking.io/l4-netlb-v2"
		netLBFinalizerV3                        = "gke.networking.io/l4-netlb-v3"
	)

	if service.Spec.Type != corev1.ServiceTypeLoadBalancer {
		return false
	}
	if service.Spec.LoadBalancerClass != nil {
		return *service.Spec.LoadBalancerClass == legacyRegionalInternalLoadBalancerClass || *service.Spec.LoadBalancerClass == legacyRegionalExternalLoadBalancerClass
	}
	if service.Annotations[l4RBSAnnotation] == l4RBSEnabled {
		return false
	}
	for _, finalizer := range service.Finalizers {
		if finalizer == netLBFinalizerV2 || finalizer == netLBFinalizerV3 {
			return false
		}
	}
	return true
}

var _ = Describe("[sig-hypershift][Jira:Hypershift][Feature:GCPComputeResources] Hosted Cluster GCP", Label("hosted-cluster-gcp"), func() {
	var testCtx *internal.TestContext

	BeforeEach(func() {
		testCtx = internal.GetTestContext()
		Expect(testCtx).NotTo(BeNil(), "test context should be set up in BeforeSuite")
	})

	RegisterHostedClusterGCPTests(func() *internal.TestContext { return testCtx })
})
