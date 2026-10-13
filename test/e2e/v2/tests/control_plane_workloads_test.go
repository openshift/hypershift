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
	"slices"
	"strconv"
	"strings"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	hyperutil "github.com/openshift/hypershift/support/util"
	e2eutil "github.com/openshift/hypershift/test/e2e/util"
	"github.com/openshift/hypershift/test/e2e/v2/internal"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/labels"

	crclient "sigs.k8s.io/controller-runtime/pkg/client"
)

var workloads = internal.GetControlPlaneWorkloads()

// Helper function to get pods for a workload
func getWorkloadPods(testCtx *internal.TestContext, workload internal.WorkloadSpec) []corev1.Pod {
	GinkgoHelper()
	pods, err := internal.GetWorkloadPodsBySelector(context.Background(), testCtx.MgmtClient, testCtx.ControlPlaneNamespace, workload)
	Expect(err).NotTo(HaveOccurred(), "failed to list pods for workload %s", workload.Name)
	return pods
}

// DeploymentGenerationTest registers tests for deployment generation validation
func DeploymentGenerationTest(getTestCtx internal.TestContextGetter) {

	Context("Deployment generation", func() {
		BeforeEach(func() {
			testCtx := getTestCtx()
			hostedCluster := testCtx.GetHostedCluster()
			if hostedCluster == nil || hostedCluster.CreationTimestamp.IsZero() || time.Since(hostedCluster.CreationTimestamp.Time) > 4*time.Hour {
				Skip("Deployment generation test is only for recently created hosted clusters")
			}
		})

		// EnsureNoRapidDeploymentRollouts
		const maxAllowedGeneration = 10

		for _, workload := range workloads {
			if workload.Type != "Deployment" {
				continue
			}

			It(fmt.Sprintf("should not indicate rapid rollouts for %s", workload.Name), func() {
				testCtx := getTestCtx()
				hostedCluster := testCtx.GetHostedCluster()
				if internal.ShouldSkipWorkloadForPlatform(workload, hostedCluster) {
					Skip(fmt.Sprintf("workload %s is platform-specific and doesn't match cluster platform", workload.Name))
				}

				deployment := &appsv1.Deployment{}
				err := testCtx.MgmtClient.Get(context.Background(), crclient.ObjectKey{
					Namespace: testCtx.ControlPlaneNamespace,
					Name:      workload.Name,
				}, deployment)
				if apierrors.IsNotFound(err) {
					Skip(fmt.Sprintf("Deployment %s not found", workload.Name))
				}
				Expect(err).NotTo(HaveOccurred(), "failed to get Deployment %s", workload.Name)

				Expect(deployment.Generation).To(BeNumerically("<=", maxAllowedGeneration),
					"Deployment %s has generation %d which exceeds max allowed %d",
					workload.Name, deployment.Generation, maxAllowedGeneration)
			})
		}
	})
}

// SafeToEvictAnnotationsTest registers tests for safe-to-evict annotations
func SafeToEvictAnnotationsTest(getTestCtx internal.TestContextGetter) {
	Context("Safe-to-evict annotations", func() {

		BeforeEach(func() {
			e2eutil.GinkgoAtLeast(e2eutil.Version420)
		})

		// TODO: Fix these in their corresponding repositories
		exemptions := []string{
			// Storage workloads
			"aws-ebs-csi-driver-operator",
			"azure-disk-csi-driver-operator",
			"azure-disk-csi-driver-controller",
			"azure-file-csi-driver-operator",
			"azure-file-csi-driver-controller",
			"openstack-cinder-csi-driver-operator",
			"openstack-cinder-csi-driver-controller",
			"openstack-manila-csi-driver-operator",
			"openstack-manila-csi-controller",

			// Network workloads
			"network-node-identity",
			"ovnkube-control-plane",
		}
		for _, workload := range workloads {
			It(fmt.Sprintf("should exist for pods with emptyDir or hostPath volumes belonging to %s", workload.Name), func() {
				testCtx := getTestCtx()
				hostedCluster := testCtx.GetHostedCluster()
				if internal.ShouldSkipWorkloadForPlatform(workload, hostedCluster) {
					Skip(fmt.Sprintf("workload %s is platform-specific and doesn't match cluster platform", workload.Name))
				}

				// Skip if workload is in exemption list
				if slices.Contains(exemptions, workload.Name) {
					Skip(fmt.Sprintf("workload %s is exempt from safe-to-evict annotations check", workload.Name))
				}

				pods := getWorkloadPods(testCtx, workload)
				if len(pods) == 0 {
					Skip(fmt.Sprintf("no pods found for workload %s", workload.Name))
				}

				for _, pod := range pods {
					// Check if pod has emptyDir or hostPath volumes
					hasLocalVolumes := false
					var localVolumeNames []string
					for _, volume := range pod.Spec.Volumes {
						if volume.EmptyDir != nil || volume.HostPath != nil {
							hasLocalVolumes = true
							localVolumeNames = append(localVolumeNames, volume.Name)
						}
					}

					if hasLocalVolumes {
						annotationKey := "cluster-autoscaler.kubernetes.io/safe-to-evict-local-volumes"
						annotationValue, exists := pod.Annotations[annotationKey]
						Expect(exists).To(BeTrue(), "pod %s has local volumes but missing safe-to-evict annotation", pod.Name)
						Expect(annotationValue).NotTo(BeEmpty(), "pod %s has empty safe-to-evict annotation", pod.Name)

						// Verify all local volumes are listed in annotation
						annotatedVolumes := strings.Split(annotationValue, ",")
						for _, volName := range localVolumeNames {
							found := false
							for _, annVol := range annotatedVolumes {
								if strings.TrimSpace(annVol) == volName {
									found = true
									break
								}
							}
							Expect(found).To(BeTrue(), "pod %s local volume %s not found in annotation", pod.Name, volName)
						}
					}
				}
			})
		}
	})
}

// ReadOnlyRootFilesystemTest registers tests for read-only root filesystem validation
func ReadOnlyRootFilesystemTest(getTestCtx internal.TestContextGetter) {
	Context("Read-only root filesystem", func() {
		BeforeEach(func() {
			e2eutil.GinkgoAtLeast(e2eutil.Version420)
		})

		// EnsureReadOnlyRootFilesystem
		// TODO: Fix these in their corresponding repositories
		exemptions := []string{
			// Storage workloads
			"azure-disk-csi-driver-controller",
			"azure-disk-csi-driver-operator",
			"azure-file-csi-driver-controller",
			"azure-file-csi-driver-operator",
			"aws-ebs-csi-driver-controller",
			"aws-ebs-csi-driver-operator",
			"openstack-cinder-csi-driver-controller",
			"openstack-manila-csi-controller",
			"csi-snapshot-controller",
			"csi-snapshot-webhook",
			"shared-resource-csi-driver-operator",

			// Network workloads
			"multus-admission-controller",
			"network-node-identity",
			"ovnkube-control-plane",
			"cloud-network-config-controller",

			// KubeVirt workloads
			"vmi-console-debug",
			"virt-launcher",

			// Other workloads
			"etcd",
			"featuregate-generator",
			"packageserver",
		}

		for _, workload := range workloads {
			It(fmt.Sprintf("should have read-only root filesystem for %s containers", workload.Name), func() {
				testCtx := getTestCtx()
				hostedCluster := testCtx.GetHostedCluster()
				if internal.ShouldSkipWorkloadForPlatform(workload, hostedCluster) {
					Skip(fmt.Sprintf("workload %s is platform-specific and doesn't match cluster platform", workload.Name))
				}

				// Skip if workload is in exemption list
				if slices.Contains(exemptions, workload.Name) {
					Skip(fmt.Sprintf("workload %s is exempt from read-only root filesystem check", workload.Name))
				}

				pods := getWorkloadPods(testCtx, workload)
				if len(pods) == 0 {
					Skip(fmt.Sprintf("no pods found for workload %s", workload.Name))
				}

				for _, pod := range pods {
					for _, container := range pod.Spec.Containers {
						Expect(container.SecurityContext).NotTo(BeNil(),
							"container %s in pod %s should have security context", container.Name, pod.Name)
						Expect(container.SecurityContext.ReadOnlyRootFilesystem).NotTo(BeNil(),
							"container %s in pod %s should have ReadOnlyRootFilesystem set", container.Name, pod.Name)
						Expect(*container.SecurityContext.ReadOnlyRootFilesystem).To(BeTrue(),
							"container %s in pod %s should have ReadOnlyRootFilesystem=true", container.Name, pod.Name)
					}
				}
			})
		}
	})
}

// ReadOnlyRootFilesystemTmpDirMountTest registers tests for read-only root filesystem tmp dir mount validation
func ReadOnlyRootFilesystemTmpDirMountTest(getTestCtx internal.TestContextGetter) {
	Context("Read-only root filesystem tmp dir mount", func() {
		BeforeEach(func() {
			e2eutil.GinkgoAtLeast(e2eutil.Version420)
		})

		// EnsureReadOnlyRootFilesystemTmpDirMount
		// TODO: Fix these in their corresponding repositories
		exemptions := []string{
			// Storage workloads
			"azure-disk-csi-driver-controller",
			"azure-disk-csi-driver-operator",
			"azure-file-csi-driver-controller",
			"azure-file-csi-driver-operator",
			"aws-ebs-csi-driver-controller",
			"aws-ebs-csi-driver-operator",
			"openstack-cinder-csi-driver-controller",
			"openstack-manila-csi",
			"csi-snapshot-controller",
			"csi-snapshot-webhook",

			// Network workloads
			"multus-admission-controller",
			"network-node-identity",
			"ovnkube-control-plane",
			"cloud-network-config-controller",

			// KubeVirt workloads
			"vmi-console-debug",
			"kubevirt.io/virt-launcher",

			// Other workloads
			"packageserver",
			"etcd",
			"featuregate-generator",
		}

		for _, workload := range workloads {
			It(fmt.Sprintf("should have /tmp mounted for %s containers", workload.Name), func() {
				testCtx := getTestCtx()
				hostedCluster := testCtx.GetHostedCluster()
				if internal.ShouldSkipWorkloadForPlatform(workload, hostedCluster) {
					Skip(fmt.Sprintf("workload %s is platform-specific and doesn't match cluster platform", workload.Name))
				}

				// Skip if workload is in exemption list
				if slices.Contains(exemptions, workload.Name) {
					Skip(fmt.Sprintf("workload %s is exempt from tmp dir mount check", workload.Name))
				}

				pods := getWorkloadPods(testCtx, workload)
				if len(pods) == 0 {
					Skip(fmt.Sprintf("no pods found for workload %s", workload.Name))
				}

				for _, pod := range pods {
					for _, container := range pod.Spec.Containers {
						hasTmpMount := false
						for _, mount := range container.VolumeMounts {
							if mount.MountPath == hyperutil.PodTmpDirMountPath {
								hasTmpMount = true
								break
							}
						}
						Expect(hasTmpMount).To(BeTrue(),
							"container %s in pod %s should have /tmp mounted", container.Name, pod.Name)
					}
				}
			})
		}
	})
}

// ContainerImagePullPolicyTest registers tests for container image pull policy validation
func ContainerImagePullPolicyTest(getTestCtx internal.TestContextGetter) {
	Context("Container image pull policy", func() {
		// EnsureAllContainersHavePullPolicyIfNotPresent
		for _, workload := range workloads {
			It(fmt.Sprintf("should have IfNotPresent pull policy for %s containers", workload.Name), func() {
				testCtx := getTestCtx()
				hostedCluster := testCtx.GetHostedCluster()
				if internal.ShouldSkipWorkloadForPlatform(workload, hostedCluster) {
					Skip(fmt.Sprintf("workload %s is platform-specific and doesn't match cluster platform", workload.Name))
				}

				pods := getWorkloadPods(testCtx, workload)
				if len(pods) == 0 {
					Skip(fmt.Sprintf("no pods found for workload %s", workload.Name))
				}

				for _, pod := range pods {
					for _, container := range pod.Spec.Containers {
						if container.ImagePullPolicy == "" {
							Fail(fmt.Sprintf("container %s in pod %s has no ImagePullPolicy set", container.Name, pod.Name))
						}
						Expect(container.ImagePullPolicy).To(Equal(corev1.PullIfNotPresent),
							"container %s in pod %s should have ImagePullPolicy=IfNotPresent, got %s",
							container.Name, pod.Name, container.ImagePullPolicy)
					}
				}
			})
		}
	})
}

// ContainerTerminationMessagePolicyTest registers tests for container termination message policy validation
func ContainerTerminationMessagePolicyTest(getTestCtx internal.TestContextGetter) {
	Context("Container termination message policy", func() {
		BeforeEach(func() {
			e2eutil.GinkgoAtLeast(e2eutil.Version419)
		})

		// EnsureAllContainersHaveTerminationMessagePolicyFallbackToLogsOnError
		// TODO: Fix these in their corresponding repositories
		exemptions := []string{
			// Network workloads
			"cloud-network-config-controller",
			"network-node-identity",
			"ovnkube-control-plane",

			// KubeVirt workloads
			"vmi-console-debug",
			"virt-launcher",
		}

		for _, workload := range workloads {
			It(fmt.Sprintf("should have FallbackToLogsOnError termination message policy for %s containers", workload.Name), func() {
				testCtx := getTestCtx()
				hostedCluster := testCtx.GetHostedCluster()
				if internal.ShouldSkipWorkloadForPlatform(workload, hostedCluster) {
					Skip(fmt.Sprintf("workload %s is platform-specific and doesn't match cluster platform", workload.Name))
				}

				// Skip if workload is in exemption list
				if slices.Contains(exemptions, workload.Name) {
					Skip(fmt.Sprintf("workload %s is exempt from termination message policy check", workload.Name))
				}

				// Skip KubeVirt related pods
				pods := getWorkloadPods(testCtx, workload)
				if len(pods) == 0 {
					Skip(fmt.Sprintf("no pods found for workload %s", workload.Name))
				}

				for _, pod := range pods {
					for _, initContainer := range pod.Spec.InitContainers {
						Expect(initContainer.TerminationMessagePolicy).To(Equal(corev1.TerminationMessageFallbackToLogsOnError),
							"initContainer %s in pod %s should have TerminationMessagePolicy=FallbackToLogsOnError",
							initContainer.Name, pod.Name)
					}
					for _, container := range pod.Spec.Containers {
						Expect(container.TerminationMessagePolicy).To(Equal(corev1.TerminationMessageFallbackToLogsOnError),
							"container %s in pod %s should have TerminationMessagePolicy=FallbackToLogsOnError",
							container.Name, pod.Name)
					}
				}
			})
		}
	})
}

// ContainerResourceRequestsTest registers tests for container resource requests validation
func ContainerResourceRequestsTest(getTestCtx internal.TestContextGetter) {
	Context("Container resource requests", func() {
		// EnsureHCPContainersHaveResourceRequests
		for _, workload := range workloads {
			It(fmt.Sprintf("should have resource requests for %s containers", workload.Name), func() {
				testCtx := getTestCtx()
				hostedCluster := testCtx.GetHostedCluster()
				if internal.ShouldSkipWorkloadForPlatform(workload, hostedCluster) {
					Skip(fmt.Sprintf("workload %s is platform-specific and doesn't match cluster platform", workload.Name))
				}

				pods := getWorkloadPods(testCtx, workload)
				if len(pods) == 0 {
					Skip(fmt.Sprintf("no pods found for workload %s", workload.Name))
				}

				for _, pod := range pods {
					for _, container := range pod.Spec.Containers {
						Expect(container.Resources.Requests).NotTo(BeNil(),
							"container %s in pod %s should have resource requests", container.Name, pod.Name)
						_, hasCPU := container.Resources.Requests[corev1.ResourceCPU]
						Expect(hasCPU).To(BeTrue(),
							"container %s in pod %s should have CPU resource request", container.Name, pod.Name)
						_, hasMemory := container.Resources.Requests[corev1.ResourceMemory]
						Expect(hasMemory).To(BeTrue(),
							"container %s in pod %s should have memory resource request", container.Name, pod.Name)
					}
				}
			})
		}
	})
}

// PodPriorityTest registers tests for pod priority validation
func PodPriorityTest(getTestCtx internal.TestContextGetter) {
	Context("Pod priority", func() {
		// EnsureNoPodsWithTooHighPriority
		const maxAllowedPriority = 100002000
		for _, workload := range workloads {
			It(fmt.Sprintf("should not have too high priority for %s pods", workload.Name), func() {
				testCtx := getTestCtx()
				hostedCluster := testCtx.GetHostedCluster()
				if internal.ShouldSkipWorkloadForPlatform(workload, hostedCluster) {
					Skip(fmt.Sprintf("workload %s is platform-specific and doesn't match cluster platform", workload.Name))
				}

				pods := getWorkloadPods(testCtx, workload)
				if len(pods) == 0 {
					Skip(fmt.Sprintf("no pods found for workload %s", workload.Name))
				}

				for _, pod := range pods {
					if pod.Spec.Priority != nil && *pod.Spec.Priority > maxAllowedPriority {
						Fail(fmt.Sprintf("pod %s has priority %d which exceeds maximum allowed %d", pod.Name, *pod.Spec.Priority, maxAllowedPriority))
					}
				}
			})
		}
	})
}

// ServiceAccountTokenMountingTest registers tests for service account token mounting validation
func ServiceAccountTokenMountingTest(getTestCtx internal.TestContextGetter) {
	Context("Service account token mounting", func() {
		BeforeEach(func() {
			e2eutil.GinkgoAtLeast(e2eutil.Version416)
		})

		// EnsureSATokenNotMountedUnlessNecessary
		// Build expected components list based on platform
		exemptions := []string{
			"packageserver",
			"csi-snapshot-controller",
			"shared-resource-csi-driver-operator",
			"capi-provider",
			"cluster-api",
			"cluster-network-operator",
			"cluster-autoscaler",
			"cluster-node-tuning-operator",
			"cluster-storage-operator",
			"csi-snapshot-controller-operator",
			"machine-approver",
			"etcd",
			"control-plane-operator",
			"control-plane-pki-operator",
			"hosted-cluster-config-operator",
			"ignition-server",
			"cloud-controller-manager",
			"olm-collect-profiles",
			"karpenter",
			"karpenter-operator",
			"featuregate-generator",

			// AWS-specific exemptions
			"aws-ebs-csi-driver-controller",
			"aws-ebs-csi-driver-operator",

			// Azure-specific exemptions
			"azure-cloud-controller-manager",
			"azure-disk-csi-driver-controller",
			"azure-disk-csi-driver-operator",
			"azure-file-csi-driver-controller",
			"azure-file-csi-driver-operator",

			// OpenStack-specific exemptions
			"openstack-cinder-csi-driver-controller",
			"openstack-cinder-csi-driver-operator",
			"openstack-manila-csi-controllerplugin",
			"manila-csi-driver-operator",

			// Kubevirt-specific exemptions
			"kubevirt-cloud-controller-manager",
			"kubevirt-csi-controller",
		}

		if e2eutil.IsLessThan(e2eutil.Version418) {
			exemptions = append(exemptions,
				"csi-snapshot-webhook",
			)
		}

		for _, workload := range workloads {
			It(fmt.Sprintf("should not mount service account token unless necessary for %s pods", workload.Name), func() {
				testCtx := getTestCtx()
				hostedCluster := testCtx.GetHostedCluster()
				if internal.ShouldSkipWorkloadForPlatform(workload, hostedCluster) {
					Skip(fmt.Sprintf("workload %s is platform-specific and doesn't match cluster platform", workload.Name))
				}

				// Skip if workload is in exemption list
				if slices.Contains(exemptions, workload.Name) {
					Skip(fmt.Sprintf("workload %s is exempt from service account token mounting check", workload.Name))
				}

				pods := getWorkloadPods(testCtx, workload)
				if len(pods) == 0 {
					Skip(fmt.Sprintf("no pods found for workload %s", workload.Name))
				}

				for _, pod := range pods {
					for _, volume := range pod.Spec.Volumes {
						Expect(volume.Name).NotTo(HavePrefix("kube-api-access-"),
							"pod %s should not have kube-api-access-* volume mounted", pod.Name)
					}
				}
			})
		}
	})
}

// PodAffinitiesAndTolerationsTest registers tests for pod affinities and tolerations validation
func PodAffinitiesAndTolerationsTest(getTestCtx internal.TestContextGetter) {
	Context("Pod affinities and tolerations", func() {
		// EnsureHCPPodsAffinitiesAndTolerations
		BeforeEach(func() {
			testCtx := getTestCtx()
			hostedCluster := testCtx.GetHostedCluster()
			if hostedCluster == nil || hostedCluster.Spec.Platform.Type != hyperv1.AWSPlatform {
				Skip("Pod affinities and tolerations test is only for AWS platform")
			}
		})

		for _, workload := range workloads {
			It(fmt.Sprintf("should have correct affinities and tolerations for %s pods", workload.Name), func() {
				testCtx := getTestCtx()
				hostedCluster := testCtx.GetHostedCluster()
				if internal.ShouldSkipWorkloadForPlatform(workload, hostedCluster) {
					Skip(fmt.Sprintf("workload %s is platform-specific and doesn't match cluster platform", workload.Name))
				}

				// SRO is being removed in 4.18
				if workload.Name == "shared-resource-csi-driver-operator" {
					Skip("shared-resource-csi-driver-operator is exempt from affinities and tolerations check")
				}

				if workload.Name == "virt-launcher" || workload.Name == "vmi-console-debug" {
					Skip("virt-launcher and vmi-console-debug are exempt from affinities and tolerations check")
				}

				pods := getWorkloadPods(testCtx, workload)
				if len(pods) == 0 {
					Skip(fmt.Sprintf("no pods found for workload %s", workload.Name))
				}

				controlPlaneLabelTolerationKey := "hypershift.openshift.io/control-plane"
				clusterNodeSchedulingAffinityWeight := 100
				controlPlaneNodeSchedulingAffinityWeight := clusterNodeSchedulingAffinityWeight / 2
				colocationLabelKey := "hypershift.openshift.io/hosted-control-plane"

				var expectedTolerations []corev1.Toleration
				switch workload.Name {
				case "aws-ebs-csi-driver-operator":
					expectedTolerations = []corev1.Toleration{
						{
							Key:      controlPlaneLabelTolerationKey,
							Operator: corev1.TolerationOpExists,
						},
						{
							Key:      hyperv1.HostedClusterLabel,
							Operator: corev1.TolerationOpEqual,
							Value:    testCtx.ControlPlaneNamespace,
						},
					}
				default:
					expectedTolerations = []corev1.Toleration{
						{
							Key:      controlPlaneLabelTolerationKey,
							Operator: corev1.TolerationOpEqual,
							Value:    "true",
							Effect:   corev1.TaintEffectNoSchedule,
						},
						{
							Key:      hyperv1.HostedClusterLabel,
							Operator: corev1.TolerationOpEqual,
							Value:    testCtx.ControlPlaneNamespace,
							Effect:   corev1.TaintEffectNoSchedule,
						},
					}
				}

				for _, pod := range pods {
					for _, expectedTol := range expectedTolerations {
						found := false
						for _, tol := range pod.Spec.Tolerations {
							if tol.Key == expectedTol.Key && tol.Operator == expectedTol.Operator && tol.Value == expectedTol.Value && tol.Effect == expectedTol.Effect {
								found = true
								break
							}
						}
						Expect(found).To(BeTrue(), "pod %s should have toleration %+v", pod.Name, expectedTol)
					}

					// Check affinities
					Expect(pod.Spec.Affinity).NotTo(BeNil(), "pod %s should have affinity", pod.Name)
					Expect(pod.Spec.Affinity.NodeAffinity).NotTo(BeNil(), "pod %s should have node affinity", pod.Name)
					Expect(pod.Spec.Affinity.NodeAffinity.PreferredDuringSchedulingIgnoredDuringExecution).NotTo(BeEmpty(),
						"pod %s should have preferred node affinity", pod.Name)

					// Check for control plane node affinity
					hasControlPlaneAffinity := false
					for _, term := range pod.Spec.Affinity.NodeAffinity.PreferredDuringSchedulingIgnoredDuringExecution {
						if term.Weight == int32(controlPlaneNodeSchedulingAffinityWeight) {
							for _, req := range term.Preference.MatchExpressions {
								if req.Key == controlPlaneLabelTolerationKey && req.Operator == corev1.NodeSelectorOpIn {
									hasControlPlaneAffinity = true
									break
								}
							}
						}
					}
					Expect(hasControlPlaneAffinity).To(BeTrue(), "pod %s should have control plane node affinity", pod.Name)

					// Check for pod affinity
					Expect(pod.Spec.Affinity.PodAffinity).NotTo(BeNil(), "pod %s should have pod affinity", pod.Name)
					Expect(pod.Spec.Affinity.PodAffinity.PreferredDuringSchedulingIgnoredDuringExecution).NotTo(BeEmpty(),
						"pod %s should have preferred pod affinity", pod.Name)

					hasColocationAffinity := false
					for _, term := range pod.Spec.Affinity.PodAffinity.PreferredDuringSchedulingIgnoredDuringExecution {
						if term.Weight != 100 || term.PodAffinityTerm.LabelSelector == nil {
							continue
						}
						for _, value := range term.PodAffinityTerm.LabelSelector.MatchLabels {
							if value == testCtx.ControlPlaneNamespace {
								hasColocationAffinity = true
								break
							}
						}
						if term.PodAffinityTerm.LabelSelector.MatchLabels[colocationLabelKey] == testCtx.ControlPlaneNamespace {
							hasColocationAffinity = true
							break
						}
					}
					Expect(hasColocationAffinity).To(BeTrue(), "pod %s should have colocation pod affinity", pod.Name)
				}
			})
		}
	})
}

// WorkloadRegistryValidationTest registers tests for workload registry validation
func WorkloadRegistryValidationTest(getTestCtx internal.TestContextGetter) {
	Context("Workload registry validation", func() {
		// Label("Informing"): failures skip (non-blocking) until registry is complete
		It("all pods should belong to predefined workloads", Label("Informing"), func() {
			testCtx := getTestCtx()
			_ = testCtx.GetHostedCluster() // unused but kept for consistency
			// List all pods in control plane namespace
			podList := &corev1.PodList{}
			err := testCtx.MgmtClient.List(context.Background(), podList, &crclient.ListOptions{
				Namespace: testCtx.ControlPlaneNamespace,
			})
			Expect(err).NotTo(HaveOccurred(), "failed to list pods in control plane namespace")

			// Build a map of workload selectors for quick lookup
			workloadSelectors := make(map[string]labels.Selector)
			for _, workload := range workloads {
				selector := labels.SelectorFromSet(workload.PodSelector)
				workloadSelectors[workload.Name] = selector
			}

			// Check each pod
			var podsNotBelongingToWorkloads []string
			for _, pod := range podList.Items {
				// Skip system pods (if any)
				// Check if pod belongs to any workload
				belongsToWorkload := false
				for _, selector := range workloadSelectors {
					if selector.Matches(labels.Set(pod.Labels)) {
						belongsToWorkload = true
						break
					}
				}

				if !belongsToWorkload {
					podsNotBelongingToWorkloads = append(podsNotBelongingToWorkloads,
						fmt.Sprintf("pod %s/%s", pod.Namespace, pod.Name))
				}
			}

			Expect(podsNotBelongingToWorkloads).To(BeEmpty(),
				"The following pods do not belong to any predefined workload:\n%s",
				strings.Join(podsNotBelongingToWorkloads, "\n"))
		})
	})
}

// SecurityContextUIDTest registers tests for security context UID validation
func SecurityContextUIDTest(getTestCtx internal.TestContextGetter) {
	Context("Security context UID", func() {
		// EnsureSecurityContextUID
		var expectedUID int64

		BeforeEach(func() {
			testCtx := getTestCtx()
			hostedCluster := testCtx.GetHostedCluster()
			if hostedCluster == nil || hostedCluster.Spec.Platform.Type != hyperv1.AzurePlatform {
				Skip("Security context UID test is only for Azure platform")
			}

			// Get the control plane namespace to check for UID annotation
			controlPlaneNamespace := &corev1.Namespace{}
			err := testCtx.MgmtClient.Get(context.Background(), crclient.ObjectKey{Name: testCtx.ControlPlaneNamespace}, controlPlaneNamespace)
			Expect(err).NotTo(HaveOccurred(), "failed to get namespace %s", testCtx.ControlPlaneNamespace)

			uid, ok := controlPlaneNamespace.Annotations["hypershift.openshift.io/default-security-context-uid"]
			if !ok {
				Skip(fmt.Sprintf("namespace %s missing SCC UID annotation", controlPlaneNamespace.Name))
			}

			parsedUID, err := strconv.ParseInt(uid, 10, 64)
			Expect(err).NotTo(HaveOccurred(), "couldn't parse SCC UID %s from namespace %s", uid, controlPlaneNamespace.Name)
			expectedUID = parsedUID
		})

		// TODO: Fix these in their corresponding repositories
		exemptions := []string{
			// Storage workloads
			"azure-disk-csi-driver-controller",
			"azure-disk-csi-driver-operator",
			"azure-file-csi-driver-controller",
			"azure-file-csi-driver-operator",

			// Network workloads
			"network-node-identity",
			"ovnkube-control-plane",
		}

		for _, workload := range workloads {
			It(fmt.Sprintf("should have expected RunAsUser UID for %s pods", workload.Name), func() {
				testCtx := getTestCtx()
				hostedCluster := testCtx.GetHostedCluster()
				if internal.ShouldSkipWorkloadForPlatform(workload, hostedCluster) {
					Skip(fmt.Sprintf("workload %s is platform-specific and doesn't match cluster platform", workload.Name))
				}

				// Skip if workload is in exemption list
				if slices.Contains(exemptions, workload.Name) {
					Skip(fmt.Sprintf("workload %s is exempt from security context UID check", workload.Name))
				}

				pods := getWorkloadPods(testCtx, workload)
				if len(pods) == 0 {
					Skip(fmt.Sprintf("no pods found for workload %s", workload.Name))
				}

				for _, pod := range pods {
					runAsUser := func() *int64 {
						if pod.Spec.SecurityContext == nil || pod.Spec.SecurityContext.RunAsUser == nil {
							return nil
						}
						return pod.Spec.SecurityContext.RunAsUser
					}()

					if runAsUser == nil {
						Fail(fmt.Sprintf("pod %s/%s: RunAsUser is not set (expected UID %d)", pod.Namespace, pod.Name, expectedUID))
					}

					Expect(*runAsUser).To(Equal(expectedUID),
						"pod %s/%s: RunAsUser %d does not match expected UID %d",
						pod.Namespace, pod.Name, *runAsUser, expectedUID)
				}
			})
		}
	})
}

// RegisterControlPlaneWorkloadsTests registers all control plane workloads tests
func RegisterControlPlaneWorkloadsTests(getTestCtx internal.TestContextGetter) {
	WorkloadRegistryValidationTest(getTestCtx)
	DeploymentGenerationTest(getTestCtx)
	SafeToEvictAnnotationsTest(getTestCtx)
	ReadOnlyRootFilesystemTest(getTestCtx)
	ReadOnlyRootFilesystemTmpDirMountTest(getTestCtx)
	ContainerImagePullPolicyTest(getTestCtx)
	ContainerTerminationMessagePolicyTest(getTestCtx)
	ContainerResourceRequestsTest(getTestCtx)
	PodPriorityTest(getTestCtx)
	ServiceAccountTokenMountingTest(getTestCtx)
	PodAffinitiesAndTolerationsTest(getTestCtx)
	SecurityContextUIDTest(getTestCtx)
}

var _ = Describe("Control Plane Workloads", Label("control-plane-workloads"), func() {
	var (
		testCtx *internal.TestContext
	)

	BeforeEach(func() {
		testCtx = internal.GetTestContext()
		Expect(testCtx).NotTo(BeNil(), "test context should be set up in BeforeSuite")

		if err := testCtx.ValidateControlPlaneNamespace(); err != nil {
			AbortSuite(err.Error())
		}
	})

	RegisterControlPlaneWorkloadsTests(func() *internal.TestContext { return testCtx })
})
