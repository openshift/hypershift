package util

import (
	"context"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	. "github.com/onsi/gomega"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	"github.com/openshift/hypershift/support/azureutil"
	hyperutil "github.com/openshift/hypershift/support/util"

	configv1 "github.com/openshift/api/config/v1"
	securityv1 "github.com/openshift/api/security/v1"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/utils/ptr"

	crclient "sigs.k8s.io/controller-runtime/pkg/client"
)

var (
	// CiliumVersion is read from CILIUM_VERSION at runtime. When empty, the Cilium tests should skip.
	CiliumVersion = os.Getenv("CILIUM_VERSION")
)

const (
	// Generic timeouts and intervals for Cilium tests
	CiliumDefaultTimeout           = 10 * time.Minute
	CiliumLongTimeout              = 20 * time.Minute
	CiliumShortTimeout             = 2 * time.Minute
	CiliumDefaultPollInterval      = 10 * time.Second
	CiliumLongPollInterval         = 15 * time.Second
	CiliumConnectivityWaitDuration = 60 * time.Second
)

const (
	// CiliumNamespace is the namespace where Cilium agent pods run.
	CiliumNamespace = "cilium"
	// CiliumTestNamespace is the namespace created for Cilium connectivity tests.
	CiliumTestNamespace = "cilium-test"
	// CiliumTestServiceAccount is the name of the service account used for Cilium connectivity tests.
	CiliumTestServiceAccount = "default"
	// CiliumConfigGroup is the group for CiliumConfig.
	CiliumConfigGroup = "cilium.io"
	// CiliumConfigVersion is the version for CiliumConfig.
	CiliumConfigVersion = "v1alpha1"
	// CiliumConfigKind is the kind for CiliumConfig.
	CiliumConfigKind = "CiliumConfig"
	// CiliumConfigName is the name of the CiliumConfig resource.
	CiliumConfigName = "cilium"
)

// CiliumNamespaceManifest returns the cilium namespace with the required PodSecurity labels.
func CiliumNamespaceManifest() *corev1.Namespace {
	return &corev1.Namespace{
		ObjectMeta: metav1.ObjectMeta{
			Name: CiliumNamespace,
			Labels: map[string]string{
				"security.openshift.io/scc.podSecurityLabelSync": "false",
				"pod-security.kubernetes.io/enforce":             "privileged",
				"pod-security.kubernetes.io/audit":               "privileged",
				"pod-security.kubernetes.io/warn":                "privileged",
			},
		},
	}
}

// CiliumSCCManifest returns the SecurityContextConstraints for Cilium.
func CiliumSCCManifest() *securityv1.SecurityContextConstraints {
	return &securityv1.SecurityContextConstraints{
		ObjectMeta: metav1.ObjectMeta{
			Name: "cilium-scc",
		},
		AllowHostPorts:           true,
		AllowHostNetwork:         true,
		AllowHostDirVolumePlugin: true,
		AllowHostIPC:             false,
		AllowHostPID:             false,
		AllowPrivilegeEscalation: ptr.To(true),
		AllowPrivilegedContainer: true,
		ReadOnlyRootFilesystem:   false,
		RequiredDropCapabilities: []corev1.Capability{},
		AllowedCapabilities: []corev1.Capability{
			"CHOWN", "KILL", "NET_ADMIN", "NET_RAW", "IPC_LOCK",
			"SYS_MODULE", "SYS_ADMIN", "SYS_RESOURCE", "DAC_OVERRIDE",
			"FOWNER", "SETGID", "SETUID", "SYS_CHROOT", "SYS_PTRACE",
		},
		RunAsUser:      securityv1.RunAsUserStrategyOptions{Type: securityv1.RunAsUserStrategyRunAsAny},
		SELinuxContext: securityv1.SELinuxContextStrategyOptions{Type: securityv1.SELinuxStrategyRunAsAny},
		Volumes: []securityv1.FSType{
			securityv1.FSTypeHostPath,
			securityv1.FSTypeEmptyDir,
			securityv1.FSTypeSecret,
			securityv1.FSTypeConfigMap,
			securityv1.FSProjected,
		},
		Users: []string{
			"system:serviceaccount:cilium:cilium",
			"system:serviceaccount:cilium:cilium-operator",
		},
	}
}

// CiliumManifestURLs returns the list of Cilium manifest URLs for a given version.
func CiliumManifestURLs(version string) []string {
	return []string{
		fmt.Sprintf("https://raw.githubusercontent.com/isovalent/olm-for-cilium/main/manifests/cilium.v%s/cluster-network-03-cilium-ciliumconfigs-crd.yaml", version),
		fmt.Sprintf("https://raw.githubusercontent.com/isovalent/olm-for-cilium/main/manifests/cilium.v%s/cluster-network-06-cilium-00000-cilium-namespace.yaml", version),
		fmt.Sprintf("https://raw.githubusercontent.com/isovalent/olm-for-cilium/main/manifests/cilium.v%s/cluster-network-06-cilium-00001-cilium-olm-serviceaccount.yaml", version),
		fmt.Sprintf("https://raw.githubusercontent.com/isovalent/olm-for-cilium/main/manifests/cilium.v%s/cluster-network-06-cilium-00002-cilium-olm-deployment.yaml", version),
		fmt.Sprintf("https://raw.githubusercontent.com/isovalent/olm-for-cilium/main/manifests/cilium.v%s/cluster-network-06-cilium-00003-cilium-olm-service.yaml", version),
		fmt.Sprintf("https://raw.githubusercontent.com/isovalent/olm-for-cilium/main/manifests/cilium.v%s/cluster-network-06-cilium-00004-cilium-olm-leader-election-role.yaml", version),
		fmt.Sprintf("https://raw.githubusercontent.com/isovalent/olm-for-cilium/main/manifests/cilium.v%s/cluster-network-06-cilium-00005-cilium-olm-role.yaml", version),
		fmt.Sprintf("https://raw.githubusercontent.com/isovalent/olm-for-cilium/main/manifests/cilium.v%s/cluster-network-06-cilium-00006-leader-election-rolebinding.yaml", version),
		fmt.Sprintf("https://raw.githubusercontent.com/isovalent/olm-for-cilium/main/manifests/cilium.v%s/cluster-network-06-cilium-00007-cilium-olm-rolebinding.yaml", version),
		fmt.Sprintf("https://raw.githubusercontent.com/isovalent/olm-for-cilium/main/manifests/cilium.v%s/cluster-network-06-cilium-00008-cilium-cilium-olm-clusterrole.yaml", version),
		fmt.Sprintf("https://raw.githubusercontent.com/isovalent/olm-for-cilium/main/manifests/cilium.v%s/cluster-network-06-cilium-00009-cilium-cilium-clusterrole.yaml", version),
		fmt.Sprintf("https://raw.githubusercontent.com/isovalent/olm-for-cilium/main/manifests/cilium.v%s/cluster-network-06-cilium-00010-cilium-cilium-olm-clusterrolebinding.yaml", version),
		fmt.Sprintf("https://raw.githubusercontent.com/isovalent/olm-for-cilium/main/manifests/cilium.v%s/cluster-network-06-cilium-00011-cilium-cilium-clusterrolebinding.yaml", version),
	}
}

// InstallCilium validates that Cilium network policies are properly enforced
// in ARO HCP guest clusters. This test covers:Verifying Cilium installation
func InstallCilium(t *testing.T, ctx context.Context, guestClient crclient.Client, hostedCluster *hyperv1.HostedCluster) {
	t.Run("InstallCilium", func(t *testing.T) {
		if !azureutil.IsAroHCP() {
			t.Skip("test only supported on ARO HCP clusters")
		}

		t.Run("InstallCiliumNameSpace", func(t *testing.T) {
			g := NewWithT(t)
			t.Log("Configuring cilium namespace with PodSecurity labels")
			ciliumNs := &corev1.Namespace{}
			err := guestClient.Get(ctx, crclient.ObjectKey{Name: "cilium"}, ciliumNs)

			manifest := CiliumNamespaceManifest()

			if apierrors.IsNotFound(err) {
				// Namespace doesn't exist, create it with labels
				t.Log("Creating cilium namespace")
				ciliumNs = manifest
				err = guestClient.Create(ctx, ciliumNs)
				g.Expect(err).ToNot(HaveOccurred(), "failed to create cilium namespace")
			} else {
				// Namespace exists, update labels
				g.Expect(err).ToNot(HaveOccurred(), "unexpected error checking cilium namespace")

				t.Log("Updating existing cilium namespace with PodSecurity labels")
				if ciliumNs.Labels == nil {
					ciliumNs.Labels = make(map[string]string)
				}
				for k, v := range manifest.Labels {
					ciliumNs.Labels[k] = v
				}

				err = guestClient.Update(ctx, ciliumNs)
				g.Expect(err).ToNot(HaveOccurred(), "failed to update cilium namespace labels")
			}
			t.Log("Updated cilium namespace PodSecurity to 'privileged'")
		})

		t.Run("CreateSecurityContextConstraints", func(t *testing.T) {
			g := NewWithT(t)

			t.Log("Creating SecurityContextConstraints for Cilium")
			// Create SCC for Cilium namespace
			ciliumSCC := CiliumSCCManifest()

			err := guestClient.Create(ctx, ciliumSCC)
			if err != nil && !apierrors.IsAlreadyExists(err) {
				g.Expect(err).ToNot(HaveOccurred(), "failed to create Cilium SCC")
			}

			t.Log("Cilium SecurityContextConstraints created successfully")
		})

		t.Run("InstallCilium", func(t *testing.T) {
			g := NewWithT(t)
			t.Logf("Installing Cilium operator version %s", CiliumVersion)
			// Install Cilium operator manifests
			manifestURLs := CiliumManifestURLs(CiliumVersion)

			for _, url := range manifestURLs {
				t.Logf("Applying manifest from %s", url)
				err := ApplyYAMLFromURL(ctx, guestClient, url)
				g.Expect(err).ToNot(HaveOccurred(), "failed to apply manifest from %s", url)
			}

			// Verify critical resources were created successfully
			t.Log("Verifying Cilium resources creation")
			// Verify Deployment exists
			deployment := &appsv1.Deployment{}
			err := guestClient.Get(ctx, crclient.ObjectKey{Name: "cilium-olm", Namespace: "cilium"}, deployment)
			if err != nil {
				t.Logf("Failed to get Deployment cilium-olm: %v", err)
			}
			// Wait for cilium-olm deployment to be ready
			WaitForDeploymentAvailable(ctx, t, guestClient, "cilium-olm", "cilium", CiliumDefaultTimeout, CiliumDefaultPollInterval)

			// Get cluster network configuration from guest cluster
			podCIDR, hostPrefix := GetCiliumNetworkConfig(ctx, guestClient)

			// Create CiliumConfig
			t.Log("Creating CiliumConfig with pod CIDR and host prefix")
			ciliumConfig := CreateCiliumConfig(podCIDR, hostPrefix)
			err = guestClient.Create(ctx, ciliumConfig)
			if err != nil {
				if apierrors.IsAlreadyExists(err) {
					// CiliumConfig already exists (likely created by cilium-olm operator), update it
					t.Log("CiliumConfig already exists, updating it with correct configuration")
					existingConfig := &unstructured.Unstructured{}
					existingConfig.SetGroupVersionKind(schema.GroupVersionKind{
						Group:   CiliumConfigGroup,
						Version: CiliumConfigVersion,
						Kind:    CiliumConfigKind,
					})
					err = guestClient.Get(ctx, crclient.ObjectKey{Name: CiliumConfigName, Namespace: CiliumNamespace}, existingConfig)
					g.Expect(err).ToNot(HaveOccurred(), "failed to get existing CiliumConfig")

					// Update the spec with our desired configuration
					existingConfig.Object["spec"] = ciliumConfig.Object["spec"]
					err = guestClient.Update(ctx, existingConfig)
					g.Expect(err).ToNot(HaveOccurred(), "failed to update CiliumConfig")
					t.Log("Successfully updated CiliumConfig with pod CIDR and host prefix")
				} else {
					g.Expect(err).ToNot(HaveOccurred(), "failed to create CiliumConfig")
				}
			}

			// Verify CiliumConfig has correct configuration
			t.Log("Verifying CiliumConfig has correct IPAM configuration")
			g.Eventually(func() bool {
				ciliumConfig := &unstructured.Unstructured{}
				ciliumConfig.SetGroupVersionKind(schema.GroupVersionKind{
					Group:   CiliumConfigGroup,
					Version: CiliumConfigVersion,
					Kind:    CiliumConfigKind,
				})
				err := guestClient.Get(ctx, crclient.ObjectKey{Name: CiliumConfigName, Namespace: CiliumNamespace}, ciliumConfig)
				if err != nil {
					t.Logf("CiliumConfig not found yet: %v", err)
					return false
				}

				// Verify the clusterPoolIPv4MaskSize is set correctly
				spec, found, err := unstructured.NestedMap(ciliumConfig.Object, "spec")
				if err != nil || !found {
					t.Logf("Failed to get spec from CiliumConfig: %v", err)
					return false
				}
				ipam, found, err := unstructured.NestedMap(spec, "ipam")
				if err != nil || !found {
					t.Logf("Failed to get ipam from CiliumConfig spec: %v", err)
					return false
				}
				operator, found, err := unstructured.NestedMap(ipam, "operator")
				if err != nil || !found {
					t.Logf("Failed to get operator from CiliumConfig ipam: %v", err)
					return false
				}
				maskSize, found, err := unstructured.NestedInt64(operator, "clusterPoolIPv4MaskSize")
				if err != nil || !found {
					t.Logf("Failed to get clusterPoolIPv4MaskSize: %v", err)
					return false
				}
				if maskSize != int64(hostPrefix) {
					t.Logf("CiliumConfig clusterPoolIPv4MaskSize is %d, expected %d. Updating...", maskSize, hostPrefix)
					// Update it again if the operator overwrote it
					desiredConfig := CreateCiliumConfig(podCIDR, hostPrefix)
					ciliumConfig.Object["spec"] = desiredConfig.Object["spec"]
					err = guestClient.Update(ctx, ciliumConfig)
					if err != nil {
						t.Logf("Failed to update CiliumConfig: %v", err)
					}
					return false
				}

				t.Logf("CiliumConfig has correct clusterPoolIPv4MaskSize: %d", maskSize)
				return true
			}, CiliumShortTimeout, CiliumDefaultPollInterval).Should(BeTrue(), "CiliumConfig should have correct configuration")

			// Wait for operator to create DaemonSet
			t.Log("Waiting for Cilium DaemonSet to be created by operator")
			var ciliumDaemonSet *appsv1.DaemonSet
			g.Eventually(func() bool {
				dsList := &appsv1.DaemonSetList{}
				err := guestClient.List(ctx, dsList, crclient.InNamespace(CiliumNamespace))
				if err != nil {
					t.Logf("Failed to list DaemonSets: %v", err)
					return false
				}

				t.Logf("Found %d DaemonSets in cilium namespace", len(dsList.Items))
				for i, ds := range dsList.Items {
					t.Logf("  DaemonSet %d: %s", i+1, ds.Name)
					// Look for the main Cilium DaemonSet (usually named "cilium")
					if ds.Name == "cilium" || strings.HasPrefix(ds.Name, "cilium-") && !strings.Contains(ds.Name, "operator") {
						ciliumDaemonSet = &dsList.Items[i]
						t.Logf("Found Cilium DaemonSet: %s", ds.Name)
						return true
					}
				}

				t.Log("Cilium DaemonSet not created by operator yet")
				return false
			}, CiliumDefaultTimeout, CiliumDefaultPollInterval).Should(BeTrue(), "cilium-olm operator should create Cilium DaemonSet")

			// Now wait for DaemonSet pods to be ready
			t.Log("Waiting for Cilium agent pods from DaemonSet to be ready")
			WaitForDaemonSetReady(ctx, t, guestClient, ciliumDaemonSet.Name, ciliumDaemonSet.Namespace, CiliumLongTimeout, CiliumLongPollInterval)

			t.Log("Cilium installation completed successfully")
		})
	})
}

// EnsureCiliumConnectivityTestResources performs the Cilium connectivity tests.
// It returns a cleanup function.
func EnsureCiliumConnectivityTestResources(t *testing.T, ctx context.Context, guestClient crclient.Client) func() {
	t.Run("CheckCiliumPodsRunning", func(t *testing.T) {
		g := NewWithT(t)

		t.Log("Check cilium pods to be running")
		g.Eventually(func(g Gomega) {
			podList := &corev1.PodList{}
			err := guestClient.List(ctx, podList, crclient.InNamespace(CiliumNamespace))
			g.Expect(err).NotTo(HaveOccurred(), "failed to list Cilium pods")

			g.Expect(podList.Items).NotTo(BeEmpty(), "no Cilium pods found yet")
			t.Logf("Found %d Cilium pods", len(podList.Items))

			// Check all pods are Ready
			g.Expect(podList.Items).To(HaveEach(
				HaveField("Status.Conditions", ContainElement(
					And(
						HaveField("Type", corev1.PodReady),
						HaveField("Status", corev1.ConditionTrue),
					),
				)),
			), "not all Cilium pods are ready")
		}, CiliumLongTimeout, CiliumDefaultPollInterval).Should(Succeed(), "all Cilium pods should be running")

		t.Log("All Cilium pods are running")
	})

	t.Run("CreateSecurityContextConstraints", func(t *testing.T) {
		g := NewWithT(t)

		t.Log("Creating SecurityContextConstraints for Cilium connectivity test")
		scc := &securityv1.SecurityContextConstraints{
			ObjectMeta: metav1.ObjectMeta{
				Name: "cilium-test",
			},
			AllowHostPorts:           true,
			AllowHostNetwork:         true,
			AllowHostDirVolumePlugin: false,
			AllowHostIPC:             false,
			AllowHostPID:             false,
			AllowPrivilegeEscalation: ptrBool(false),
			AllowPrivilegedContainer: false,
			ReadOnlyRootFilesystem:   false,
			RequiredDropCapabilities: []corev1.Capability{},
			RunAsUser:                securityv1.RunAsUserStrategyOptions{Type: securityv1.RunAsUserStrategyMustRunAsRange},
			SELinuxContext:           securityv1.SELinuxContextStrategyOptions{Type: securityv1.SELinuxStrategyMustRunAs},
			Users:                    []string{fmt.Sprintf("system:serviceaccount:%s:%s", CiliumTestNamespace, CiliumTestServiceAccount)},
		}

		err := guestClient.Create(ctx, scc)
		if err != nil && !apierrors.IsAlreadyExists(err) {
			g.Expect(err).ToNot(HaveOccurred(), "failed to create SCC")
		}

		t.Log("SecurityContextConstraints created successfully")
	})

	t.Run("CreateTestNamespace", func(t *testing.T) {
		g := NewWithT(t)

		t.Log("Creating cilium-test namespace")
		ns := &corev1.Namespace{
			ObjectMeta: metav1.ObjectMeta{
				Name: CiliumTestNamespace,
				Labels: map[string]string{"security.openshift.io/scc.podSecurityLabelSync": "false",
					"pod-security.kubernetes.io/enforce": "privileged",
					"pod-security.kubernetes.io/audit":   "privileged",
					"pod-security.kubernetes.io/warn":    "privileged",
				},
			},
		}

		err := guestClient.Create(ctx, ns)
		if err != nil && !apierrors.IsAlreadyExists(err) {
			g.Expect(err).ToNot(HaveOccurred(), "failed to create namespace")
		}

		t.Log("Test namespace created successfully")
	})

	t.Run("DeployCiliumConnectivityTest", func(t *testing.T) {
		g := NewWithT(t)

		t.Logf("Deploying Cilium connectivity test from version %s", CiliumVersion)
		connectivityTestURL := fmt.Sprintf("https://raw.githubusercontent.com/cilium/cilium/%s/examples/kubernetes/connectivity-check/connectivity-check.yaml", CiliumVersion)

		err := ApplyYAMLFromURL(ctx, guestClient, connectivityTestURL, CiliumTestNamespace)
		g.Expect(err).ToNot(HaveOccurred(), "failed to apply connectivity test manifest")

		t.Log("Connectivity test manifests applied successfully")
	})

	t.Run("WaitForConnectivityTestPodsReady", func(t *testing.T) {
		g := NewWithT(t)

		t.Log("Waiting for connectivity test pods to be ready")
		g.Eventually(func(g Gomega) {
			podList := &corev1.PodList{}
			err := guestClient.List(ctx, podList, crclient.InNamespace(CiliumTestNamespace))
			g.Expect(err).NotTo(HaveOccurred(), "failed to list pods")
			g.Expect(podList.Items).NotTo(BeEmpty(), "no pods found in cilium-test namespace yet")

			t.Logf("Found %d pods in cilium-test namespace", len(podList.Items))

			// Check all pods are Ready
			g.Expect(podList.Items).To(HaveEach(
				HaveField("Status.Conditions", ContainElement(
					And(
						HaveField("Type", corev1.PodReady),
						HaveField("Status", corev1.ConditionTrue),
					),
				)),
			), "some pods are not ready")
		}, CiliumDefaultTimeout, CiliumLongPollInterval).Should(Succeed(), "all connectivity test pods should be ready")

		t.Log("All connectivity test pods are ready")
	})

	t.Run("WaitForConnectivityTestCompletion", func(t *testing.T) {
		g := NewWithT(t)

		t.Logf("Waiting %v for connectivity tests to run", CiliumConnectivityWaitDuration)
		time.Sleep(CiliumConnectivityWaitDuration)

		t.Log("Verifying all test pods are still running")
		podList := &corev1.PodList{}
		err := guestClient.List(ctx, podList, crclient.InNamespace(CiliumTestNamespace))
		g.Expect(err).ToNot(HaveOccurred(), "should be able to list test pods")

		failedPods := []string{}
		for _, pod := range podList.Items {
			if pod.Status.Phase != corev1.PodRunning {
				failedPods = append(failedPods, fmt.Sprintf("%s (phase: %s)", pod.Name, pod.Status.Phase))
			}
		}

		if len(failedPods) > 0 {
			t.Errorf("Found %d pods not in Running phase: %v", len(failedPods), failedPods)
		} else {
			t.Logf("All %d connectivity test pods are running successfully", len(podList.Items))
		}
	}) // Closes the anonymous function for "WaitForConnectivityTestCompletion"

	t.Log("Cilium connectivity test completed successfully")

	return func() {
		CleanupCiliumConnectivityTestResources(ctx, t, guestClient)
	}
}

// GetCiliumNetworkConfig extracts pod CIDR and host prefix from guest cluster
// In dual-stack environments, this function ensures we return the IPv4 network
func GetCiliumNetworkConfig(ctx context.Context, guestClient crclient.Client) (podCIDR string, hostPrefix int32) {
	podCIDR = "10.132.0.0/14"
	hostPrefix = 23

	// Get Network resource from guest cluster
	network := &configv1.Network{}
	err := guestClient.Get(ctx, types.NamespacedName{Name: "cluster"}, network)
	if err != nil {
		// Return defaults if we can't get the network config
		return podCIDR, hostPrefix
	}

	// Extract IPv4 CIDR from ClusterNetwork status
	for _, clusterNet := range network.Status.ClusterNetwork {
		if strings.Contains(clusterNet.CIDR, ".") {
			podCIDR = clusterNet.CIDR
			// Only use HostPrefix if it's non-zero, otherwise keep the default
			if clusterNet.HostPrefix != 0 {
				hostPrefix = int32(clusterNet.HostPrefix)
			}
			break
		}
	}
	return podCIDR, hostPrefix
}

func CleanupCiliumConnectivityTestResources(ctx context.Context, t *testing.T, guestClient crclient.Client) {
	t.Log("Cleaning up Cilium connectivity test resources")

	// Delete SCC
	scc := &securityv1.SecurityContextConstraints{
		ObjectMeta: metav1.ObjectMeta{
			Name: "cilium-test",
		},
	}
	if _, err := hyperutil.DeleteIfNeeded(ctx, guestClient, scc); err != nil {
		t.Logf("Warning: failed to delete SCC: %v", err)
	}

	// Delete namespace
	ns := &corev1.Namespace{
		ObjectMeta: metav1.ObjectMeta{
			Name: CiliumTestNamespace,
		},
	}
	if _, err := hyperutil.DeleteIfNeeded(ctx, guestClient, ns); err != nil {
		t.Logf("Warning: failed to delete namespace: %v", err)
	}

	t.Log("Cleanup completed")
}

func ptrBool(b bool) *bool {
	return &b
}

// CreateCiliumConfig creates a CiliumConfig custom resource
func CreateCiliumConfig(podCIDR string, hostPrefix int32) *unstructured.Unstructured {
	ciliumConfig := &unstructured.Unstructured{
		Object: map[string]interface{}{
			"apiVersion": fmt.Sprintf("%s/%s", CiliumConfigGroup, CiliumConfigVersion),
			"kind":       CiliumConfigKind,
			"metadata": map[string]interface{}{
				"name":      CiliumConfigName,
				"namespace": CiliumNamespace,
			},
			"spec": map[string]interface{}{
				"debug": map[string]interface{}{
					"enabled": true,
				},
				"k8s": map[string]interface{}{
					"requireIPv4PodCIDR": true,
				},
				"logSystemLoad": true,
				"bpf": map[string]interface{}{
					"preallocateMaps": true,
				},
				"etcd": map[string]interface{}{
					"leaseTTL": "30s",
				},
				"ipv4": map[string]interface{}{
					"enabled": true,
				},
				"ipv6": map[string]interface{}{
					"enabled": false,
				},
				"identityChangeGracePeriod": "0s",
				"ipam": map[string]interface{}{
					"mode": "cluster-pool",
					"operator": map[string]interface{}{
						"clusterPoolIPv4PodCIDRList": []string{podCIDR},
						"clusterPoolIPv4MaskSize":    hostPrefix,
					},
				},
				"nativeRoutingCIDR": podCIDR,
				"endpointRoutes": map[string]interface{}{
					"enabled": true,
				},
				"clusterHealthPort": 9940,
				"tunnelPort":        4789,
				"cni": map[string]interface{}{
					"binPath":      "/var/lib/cni/bin",
					"confPath":     "/var/run/multus/cni/net.d",
					"chainingMode": "portmap",
				},
				"prometheus": map[string]interface{}{
					"serviceMonitor": map[string]interface{}{
						"enabled": false,
					},
				},
				"hubble": map[string]interface{}{
					"tls": map[string]interface{}{
						"enabled": false,
					},
				},
				"sessionAffinity": true,
				"tolerations": []map[string]interface{}{
					{
						"operator": "Exists",
					},
				},
			},
		},
	}
	return ciliumConfig
}
