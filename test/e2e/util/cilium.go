package util

import (
	"context"
	"fmt"
	"testing"
	"time"

	. "github.com/onsi/gomega"

	hyperutil "github.com/openshift/hypershift/support/util"

	securityv1 "github.com/openshift/api/security/v1"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/utils/ptr"

	crclient "sigs.k8s.io/controller-runtime/pkg/client"
)

const (
	// CiliumNamespace is the namespace where Cilium agent pods run.
	CiliumNamespace = "cilium"
	// CiliumTestNamespace is the namespace created for Cilium connectivity tests.
	CiliumTestNamespace = "cilium-test"
	// CiliumTestServiceAccount is the name of the service account used for Cilium connectivity tests.
	CiliumTestServiceAccount = "default"
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
	})

	t.Log("Cilium connectivity test completed successfully")

	return func() {
		CleanupCiliumConnectivityTestResources(ctx, t, guestClient)
	}
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
