package util

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	. "github.com/onsi/gomega"
	routev1 "github.com/openshift/api/route/v1"
	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	"github.com/openshift/hypershift/hypershift-operator/controllers/manifests"
	suppconfig "github.com/openshift/hypershift/support/config"
	"github.com/openshift/hypershift/support/util"
	admissionregistrationv1 "k8s.io/api/admissionregistration/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	kapierror "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/cli-runtime/pkg/genericclioptions"
	"k8s.io/utils/pointer"
	capiv1 "sigs.k8s.io/cluster-api/api/v1beta1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	crclient "sigs.k8s.io/controller-runtime/pkg/client"
)

type Ensure interface {
	Ensure(t *testing.T, ctx context.Context, testParams *TestParams)
}

type EnsureFunc func(t *testing.T, ctx context.Context, testParams *TestParams)

func (f EnsureFunc) Ensure(t *testing.T, ctx context.Context, testParams *TestParams) {
	f(t, ctx, testParams)
}

type TestParams struct {
	MgmtClient    crclient.Client
	GuestClient   crclient.Client
	HostedCluster *hyperv1.HostedCluster
}

func EnsureNoCrashingPods(t *testing.T, ctx context.Context, params *TestParams) {
	client := params.MgmtClient
	hostedCluster := params.HostedCluster
	t.Run("EnsureNoCrashingPods", func(t *testing.T) {

		var crashToleration int32

		switch hostedCluster.Spec.Platform.Type {
		case hyperv1.KubevirtPlatform:
			kvPlatform := hostedCluster.Spec.Platform.Kubevirt
			// External infra can be slow at times due to the nested nature
			// of how external infra is tested within a kubevirt hcp running
			// within baremetal ocp. Occasionally pods will fail with
			// "Error: context deadline exceeded" reported by the kubelet. This
			// seems to be an infra issue with etcd latency within the external
			// infra test environment. Tolerating a single restart for random
			// components helps.
			//
			// This toleration is not used for the default local HCP KubeVirt,
			// only external infra
			if kvPlatform != nil && kvPlatform.Credentials != nil {
				crashToleration = 1
			}

			// In Azure infra, CAPK pod might crash on startup due to not being able to
			// get a leader election lock lease at the early stages, due to
			// "context deadline exceeded" error
			if kvPlatform != nil && hostedCluster.Annotations != nil {
				mgmtPlatform, annotationExists := hostedCluster.Annotations[hyperv1.ManagementPlatformAnnotation]
				if annotationExists && mgmtPlatform == string(hyperv1.AzurePlatform) {
					crashToleration = 1
				}
			}
		default:
			crashToleration = 0
		}

		namespace := manifests.HostedControlPlaneNamespace(hostedCluster.Namespace, hostedCluster.Name)

		var podList corev1.PodList
		if err := client.List(ctx, &podList, crclient.InNamespace(namespace)); err != nil {
			t.Fatalf("failed to list pods in namespace %s: %v", namespace, err)
		}
		for _, pod := range podList.Items {
			// TODO: Figure out why Route kind does not exist when ingress-operator first starts
			if strings.HasPrefix(pod.Name, "ingress-operator-") {
				continue
			}
			// Seeing flakes due to https://issues.redhat.com/browse/OCPBUGS-30068
			if strings.HasPrefix(pod.Name, "cloud-credential-operator-") {
				continue
			}
			// Restart built into OLM by design by
			// https://github.com/openshift/operator-framework-olm/commit/1cf358424a0cbe353428eab9a16051c6cabbd002
			if strings.HasPrefix(pod.Name, "olm-operator-") {
				continue
			}

			if strings.HasPrefix(pod.Name, "catalog-operator-") {
				continue
			}
			if strings.Contains(pod.Name, "-catalog") {
				continue
			}

			// Temporary workaround for https://issues.redhat.com/browse/CNV-40820
			if strings.HasPrefix(pod.Name, "kubevirt-csi") {
				continue
			}
			for _, containerStatus := range pod.Status.ContainerStatuses {
				if containerStatus.RestartCount > crashToleration {
					t.Errorf("Container %s in pod %s has a restartCount > 0 (%d)", containerStatus.Name, pod.Name, containerStatus.RestartCount)
				}
			}
		}
	})
}

func EnsureNoPodsWithTooHighPriority(t *testing.T, ctx context.Context, client crclient.Client, hostedCluster *hyperv1.HostedCluster) {
	// Priority of the etcd priority class, nothing should ever exceed this.
	const maxAllowedPriority = 100002000
	t.Run("EnsureNoPodsWithTooHighPriority", func(t *testing.T) {
		namespace := manifests.HostedControlPlaneNamespace(hostedCluster.Namespace, hostedCluster.Name)

		var podList corev1.PodList
		if err := client.List(ctx, &podList, crclient.InNamespace(namespace)); err != nil {
			t.Fatalf("failed to list pods in namespace %s: %v", namespace, err)
		}
		for _, pod := range podList.Items {
			// Bandaid until this is fixed in the CNO
			if strings.HasPrefix(pod.Name, "multus-admission-controller") {
				continue
			}
			if pod.Spec.Priority != nil && *pod.Spec.Priority > maxAllowedPriority {
				t.Errorf("pod %s with priorityClassName %s has a priority of %d with exceeds the max allowed of %d", pod.Name, pod.Spec.PriorityClassName, *pod.Spec.Priority, maxAllowedPriority)
			}
		}
	})
}

func EnsureOAPIMountsTrustBundle(t *testing.T, ctx context.Context, mgmtClient crclient.Client, hostedCluster *hyperv1.HostedCluster) {
	t.Run("EnsureOAPIMountsTrustBundle", func(t *testing.T) {
		g := NewWithT(t)
		var (
			podList corev1.PodList
			oapiPod corev1.Pod
			command = []string{
				"test",
				"-f",
				"/etc/pki/tls/certs/ca-bundle.crt",
			}
			hcpNs = manifests.HostedControlPlaneNamespace(hostedCluster.Namespace, hostedCluster.Name)
		)

		err := mgmtClient.List(ctx, &podList, crclient.InNamespace(hcpNs), crclient.MatchingLabels{"app": "openshift-apiserver"})
		g.Expect(err).ToNot(HaveOccurred(), "failed to get pods in namespace %s: %v", hcpNs, err)

		for _, pod := range podList.Items {
			if strings.HasPrefix(pod.Name, "openshift-apiserver") {
				oapiPod = *pod.DeepCopy()
			}
		}
		g.Expect(oapiPod.ObjectMeta).ToNot(BeNil(), "no openshift-apiserver pod found")
		g.Expect(oapiPod.ObjectMeta.Name).ToNot(BeEmpty(), "no openshift-apiserver pod found")

		// Check additionalTrustBundle volume and volumeMount
		if hostedCluster.Spec.AdditionalTrustBundle != nil && hostedCluster.Spec.AdditionalTrustBundle.Name != "" {
			g.Expect(oapiPod.Spec.Volumes).To(ContainElement(corev1.Volume{
				Name: "additional-trust-bundle",
			}), "no volume named additional-trust-bundle found in openshift-apiserver pod")
		}

		// Check Proxy TLS Certificates
		if hostedCluster.Spec.Configuration != nil && hostedCluster.Spec.Configuration.Proxy != nil && hostedCluster.Spec.Configuration.Proxy.TrustedCA.Name != "" {
			g.Expect(oapiPod.Spec.Volumes).To(ContainElement(corev1.Volume{
				Name: "proxy-additional-trust-bundle",
			}), "no volume named proxy-additional-trust-bundle found in openshift-apiserver pod")
		}

		_, err = RunCommandInPod(ctx, mgmtClient, "openshift-apiserver", hcpNs, command, "openshift-apiserver", 0)
		g.Expect(err).ToNot(HaveOccurred(), "failed to run command in pod: %v", err)
	})

}

func EnsureAllContainersHavePullPolicyIfNotPresent(t *testing.T, ctx context.Context, client crclient.Client, hostedCluster *hyperv1.HostedCluster) {
	t.Run("EnsureAllContainersHavePullPolicyIfNotPresent", func(t *testing.T) {
		namespace := manifests.HostedControlPlaneNamespace(hostedCluster.Namespace, hostedCluster.Name)

		var podList corev1.PodList
		if err := client.List(ctx, &podList, crclient.InNamespace(namespace)); err != nil {
			t.Fatalf("failed to list pods in namespace %s: %v", namespace, err)
		}
		for _, pod := range podList.Items {
			for _, initContainer := range pod.Spec.InitContainers {
				if initContainer.ImagePullPolicy != corev1.PullIfNotPresent {
					t.Errorf("container %s in pod %s has doesn't have imagePullPolicy %s but %s", initContainer.Name, pod.Name, corev1.PullIfNotPresent, initContainer.ImagePullPolicy)
				}
			}
			for _, container := range pod.Spec.Containers {
				if container.ImagePullPolicy != corev1.PullIfNotPresent {
					t.Errorf("container %s in pod %s has doesn't have imagePullPolicy %s but %s", container.Name, pod.Name, corev1.PullIfNotPresent, container.ImagePullPolicy)
				}
			}
		}
	})
}

func EnsureNodeCountMatchesNodePoolReplicas(t *testing.T, ctx context.Context, hostClient, guestClient crclient.Client, platform hyperv1.PlatformType, nodePoolNamespace string) {
	t.Run("EnsureNodeCountMatchesNodePoolReplicas", func(t *testing.T) {
		var nodePoolList hyperv1.NodePoolList
		if err := hostClient.List(ctx, &nodePoolList, &crclient.ListOptions{Namespace: nodePoolNamespace}); err != nil {
			t.Errorf("Failed to list NodePools: %v", err)
			return
		}

		for i := range nodePoolList.Items {
			WaitForReadyNodesByNodePool(t, ctx, guestClient, &nodePoolList.Items[i], platform)
		}
	})
}

func EnsureMachineDeploymentGeneration(t *testing.T, ctx context.Context, hostClient crclient.Client, hostedCluster *hyperv1.HostedCluster, expectedGeneration int64) {
	t.Run("EnsureMachineDeploymentGeneration", func(t *testing.T) {
		namespace := manifests.HostedControlPlaneNamespace(hostedCluster.Namespace, hostedCluster.Name)
		var machineDeploymentList capiv1.MachineDeploymentList
		if err := hostClient.List(ctx, &machineDeploymentList, crclient.InNamespace(namespace)); err != nil {
			t.Fatalf("failed to list machinedeployments: %v", err)
		}
		for _, machineDeployment := range machineDeploymentList.Items {
			if machineDeployment.Generation != expectedGeneration {
				t.Errorf("machineDeployment %s does not have expected generation %d but %d", crclient.ObjectKeyFromObject(&machineDeployment), expectedGeneration, machineDeployment.Generation)
			}
		}
	})
}

func EnsurePSANotPrivileged(t *testing.T, ctx context.Context, guestClient crclient.Client) {
	t.Run("EnsurePSANotPrivileged", func(t *testing.T) {
		testNamespaceName := "e2e-psa-check"
		namespace := &corev1.Namespace{
			ObjectMeta: metav1.ObjectMeta{
				Name: testNamespaceName,
			},
		}
		if err := guestClient.Create(ctx, namespace); err != nil {
			t.Fatalf("failed to create namespace: %v", err)
		}
		pod := &corev1.Pod{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "test-pod",
				Namespace: testNamespaceName,
			},
			Spec: corev1.PodSpec{
				NodeSelector: map[string]string{
					"e2e.openshift.io/unschedulable": "should-not-run",
				},
				Containers: []corev1.Container{
					{Name: "first", Image: "something-innocuous"},
				},
				HostPID: true, // enforcement of restricted or baseline policy should reject this
			},
		}
		err := guestClient.Create(ctx, pod)
		if err == nil {
			t.Errorf("pod admitted when rejection was expected")
		}
		if !kapierror.IsForbidden(err) {
			t.Errorf("forbidden error expected, got %s", err)
		}
	})
}

func EnsureAllRoutesUseHCPRouter(t *testing.T, ctx context.Context, hostClient crclient.Client, hostedCluster *hyperv1.HostedCluster) {
	t.Run("EnsureAllRoutesUseHCPRouter", func(t *testing.T) {
		for _, svc := range hostedCluster.Spec.Services {
			if svc.Service == hyperv1.APIServer && svc.Type != hyperv1.Route {
				t.Skip("skipping test because APIServer is not exposed through a route")
			}
		}
		var routes routev1.RouteList
		if err := hostClient.List(ctx, &routes, crclient.InNamespace(manifests.HostedControlPlaneNamespace(hostedCluster.Namespace, hostedCluster.Name))); err != nil {
			t.Fatalf("failed to list routes: %v", err)
		}
		for _, route := range routes.Items {
			original := route.DeepCopy()
			util.AddHCPRouteLabel(&route)
			if diff := cmp.Diff(route.GetLabels(), original.GetLabels()); diff != "" {
				t.Errorf("route %s is missing the label to use the per-HCP router: %s", route.Name, diff)
			}
		}
	})
}

func EnsureNetworkPolicies(t *testing.T, ctx context.Context, c crclient.Client, hostedCluster *hyperv1.HostedCluster) {
	t.Run("EnsureNetworkPolicies", func(t *testing.T) {
		if hostedCluster.Spec.Platform.Type != hyperv1.AWSPlatform {
			t.Skipf("test only supported on AWS platform, saw %s", hostedCluster.Spec.Platform.Type)
		}

		hcpNamespace := manifests.HostedControlPlaneNamespace(hostedCluster.Namespace, hostedCluster.Name)
		t.Run("EnsureComponentsHaveNeedManagementKASAccessLabel", func(t *testing.T) {
			g := NewWithT(t)
			err := checkPodsHaveLabel(ctx, c, expectedKasManagementComponents, hcpNamespace, client.MatchingLabels{suppconfig.NeedManagementKASAccessLabel: "true"})
			g.Expect(err).ToNot(HaveOccurred())
		})

		t.Run("EnsureLimitedEgressTrafficToManagementKAS", func(t *testing.T) {
			g := NewWithT(t)

			kubernetesEndpoint := &corev1.Endpoints{ObjectMeta: metav1.ObjectMeta{Name: "kubernetes", Namespace: "default"}}
			err := c.Get(ctx, client.ObjectKeyFromObject(kubernetesEndpoint), kubernetesEndpoint)
			g.Expect(err).ToNot(HaveOccurred())

			kasAddress := ""
			for _, subset := range kubernetesEndpoint.Subsets {
				if len(subset.Addresses) > 0 && len(subset.Ports) > 0 {
					kasAddress = fmt.Sprintf("https://%s:%v", subset.Addresses[0].IP, subset.Ports[0].Port)
					break
				}
			}
			t.Logf("Connecting to kubernetes endpoint on: %s", kasAddress)

			command := []string{
				"curl",
				"--connect-timeout",
				"2",
				"-Iks",
				// Default KAS advertised address.
				kasAddress,
			}

			// Validate cluster-version-operator is not allowed to access management KAS.
			_, err = RunCommandInPod(ctx, c, "cluster-version-operator", hcpNamespace, command, "cluster-version-operator", 0)
			g.Expect(err).To(HaveOccurred())

			// Validate private router is not allowed to access management KAS.
			if hostedCluster.Spec.Platform.Type == hyperv1.AWSPlatform {
				if hostedCluster.Spec.Platform.AWS.EndpointAccess != hyperv1.Private {
					// TODO (alberto): Run also in private case. Today it results in a flake:
					// === CONT  TestCreateClusterPrivate/EnsureHostedCluster/EnsureNetworkPolicies/EnsureLimitedEgressTrafficToManagementKAS
					//    util.go:851: private router pod was unexpectedly allowed to reach the management KAS. stdOut: . stdErr: Internal error occurred: error executing command in container: container is not created or running
					// Should be solve with https://issues.redhat.com/browse/HOSTEDCP-1200
					_, err := RunCommandInPod(ctx, c, "private-router", hcpNamespace, command, "private-router", 0)
					g.Expect(err).To(HaveOccurred())
				}
			}

			// Validate cluster api is allowed to access management KAS.
			stdOut, err := RunCommandInPod(ctx, c, "cluster-api", hcpNamespace, command, "manager", 0)
			// Expect curl return a 403 from the KAS.
			if !strings.Contains(stdOut, "HTTP/2 403") || err != nil {
				t.Errorf("cluster api pod was unexpectedly not allowed to reach the management KAS. stdOut: %s. stdErr: %s", stdOut, err.Error())
			}
		})
	})
}

func EnsureHCPContainersHaveResourceRequests(t *testing.T, ctx context.Context, client crclient.Client, hostedCluster *hyperv1.HostedCluster) {
	t.Run("EnsureHCPContainersHaveResourceRequests", func(t *testing.T) {
		namespace := manifests.HostedControlPlaneNamespace(hostedCluster.Namespace, hostedCluster.Name)
		var podList corev1.PodList
		if err := client.List(ctx, &podList, &crclient.ListOptions{Namespace: namespace}); err != nil {
			t.Fatalf("failed to list pods: %v", err)
		}
		for _, pod := range podList.Items {
			if strings.Contains(pod.Name, "-catalog-rollout-") {
				continue
			}
			for _, container := range pod.Spec.Containers {
				if container.Resources.Requests == nil {
					t.Errorf("container %s in pod %s has no resource requests", container.Name, pod.Name)
					continue
				}
				if _, ok := container.Resources.Requests[corev1.ResourceCPU]; !ok {
					t.Errorf("container %s in pod %s has no CPU resource request", container.Name, pod.Name)
				}
				if _, ok := container.Resources.Requests[corev1.ResourceMemory]; !ok {
					t.Errorf("container %s in pod %s has no memory resource request", container.Name, pod.Name)
				}
			}
		}
	})
}

func EnsureSecretEncryptedUsingKMS(t *testing.T, ctx context.Context, hostedCluster *hyperv1.HostedCluster, guestClient crclient.Client) {
	t.Run("EnsureSecretEncryptedUsingKMS", func(t *testing.T) {
		// create secret in guest cluster
		testSecret := corev1.Secret{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "test-secret",
				Namespace: "default",
			},
			Type: corev1.SecretTypeOpaque,
			Data: map[string][]byte{
				"myKey": []byte("myData"),
			},
		}
		if err := guestClient.Create(ctx, &testSecret); err != nil {
			t.Errorf("failed to create a secret in guest cluster; %v", err)
		}

		restConfig, err := GetConfig()
		if err != nil {
			t.Errorf("failed to get restConfig; %v", err)
		}

		secretEtcdKey := fmt.Sprintf("/kubernetes.io/secrets/%s/%s", testSecret.Namespace, testSecret.Name)
		command := []string{
			"/usr/bin/etcdctl",
			"--endpoints=localhost:2379",
			"--cacert=/etc/etcd/tls/etcd-ca/ca.crt",
			"--cert=/etc/etcd/tls/client/etcd-client.crt",
			"--key=/etc/etcd/tls/client/etcd-client.key",
			"get",
			secretEtcdKey,
		}

		hcpNamespace := manifests.HostedControlPlaneNamespace(hostedCluster.Namespace, hostedCluster.Name)
		out := new(bytes.Buffer)

		podExecuter := PodExecOptions{
			StreamOptions: StreamOptions{
				IOStreams: genericclioptions.IOStreams{
					Out:    out,
					ErrOut: os.Stderr,
				},
			},
			Command:       command,
			Namespace:     hcpNamespace,
			PodName:       "etcd-0",
			ContainerName: "etcd",
			Config:        restConfig,
		}

		if err := podExecuter.Run(ctx); err != nil {
			t.Errorf("failed to execute etcdctl command; %v", err)
		}

		if !strings.Contains(out.String(), "k8s:enc:kms:") {
			t.Errorf("secret is not encrypted using kms")
		}
	})
}

func EnsurePodsWithEmptyDirPVsHaveSafeToEvictAnnotations(t *testing.T, ctx context.Context, hostClient crclient.Client, hcpNs string) {
	t.Run("EnsurePodsWithEmptyDirPVsHaveSafeToEvictAnnotations", func(t *testing.T) {
		g := NewWithT(t)

		auditedAppList := map[string]string{
			"cloud-controller-manager":         "app",
			"cloud-credential-operator":        "app",
			"aws-ebs-csi-driver-controller":    "app",
			"capi-provider-controller-manager": "app",
			"cloud-network-config-controller":  "app",
			"cluster-network-operator":         "app",
			"cluster-version-operator":         "app",
			"control-plane-operator":           "app",
			"ignition-server":                  "app",
			"ingress-operator":                 "app",
			"kube-apiserver":                   "app",
			"kube-controller-manager":          "app",
			"kube-scheduler":                   "app",
			"multus-admission-controller":      "app",
			"oauth-openshift":                  "app",
			"openshift-apiserver":              "app",
			"openshift-oauth-apiserver":        "app",
			"packageserver":                    "app",
			"ovnkube-master":                   "app",
			"kubevirt-csi-driver":              "app",
			"cluster-image-registry-operator":  "name",
			"virt-launcher":                    "kubevirt.io",
			"azure-disk-csi-driver-controller": "app",
			"azure-file-csi-driver-controller": "app",
		}

		hcpPods := &corev1.PodList{}
		if err := hostClient.List(ctx, hcpPods, &client.ListOptions{
			Namespace: hcpNs,
		}); err != nil {
			t.Fatalf("cannot list hostedControlPlane pods: %v", err)
		}

		// Check if the pod's volumes are hostPath or emptyDir, if so, the deployment
		// should annotate the pods with the safe-to-evict-local-volumes, with all the volumes
		// involved as an string  and separated by comma, to satisfy CA operator contract.
		// more info here: https://github.com/kubernetes/autoscaler/blob/master/cluster-autoscaler/FAQ.md#what-types-of-pods-can-prevent-ca-from-removing-a-node
		for _, pod := range hcpPods.Items {
			var labelKey, labelValue string
			// Go through our audited list looking for the label that matches the pod Labels
			// Get the key and value and delete the entry from the audited list.
			for appName, prefix := range auditedAppList {
				if pod.Labels[prefix] == appName {
					labelKey = prefix
					labelValue = appName
					delete(auditedAppList, prefix)
					break
				}
			}

			if labelKey == "" || labelValue == "" {
				// if the Key/Value are empty we asume that the pod is not in the auditedList,
				// if that's the case the annotation should not exists in that pod.
				// Then continue to the next pod
				g.Expect(pod.Annotations[suppconfig.PodSafeToEvictLocalVolumesKey]).To(BeEmpty(), "the pod  %s is not in the audited list for safe-eviction and should not contain the safe-to-evict-local-volume annotation", pod.Name)
				continue
			}

			annotationValue := pod.ObjectMeta.Annotations[suppconfig.PodSafeToEvictLocalVolumesKey]
			for _, volume := range pod.Spec.Volumes {
				// Check the pod's volumes, if they are emptyDir or hostPath,
				// they should include that volume in the annotation
				if (volume.EmptyDir != nil && volume.EmptyDir.Medium != corev1.StorageMediumMemory) || volume.HostPath != nil {
					g.Expect(strings.Contains(annotationValue, volume.Name)).To(BeTrue(), "pod with name %s do not have the right volumes set in the safe-to-evict-local-volume annotation: \nCurrent: %s, Expected to be included in: %s", pod.Name, volume.Name, annotationValue)
				}
			}
		}
	})
}

func EnsureGuestWebhooksValidated(t *testing.T, ctx context.Context, guestClient crclient.Client) {
	t.Run("EnsureGuestWebhooksValidated", func(t *testing.T) {
		guestWebhookConf := &admissionregistrationv1.ValidatingWebhookConfiguration{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "test-webhook",
				Namespace: "default",
				Annotations: map[string]string{
					"service.beta.openshift.io/inject-cabundle": "true",
				},
			},
		}

		sideEffectsNone := admissionregistrationv1.SideEffectClassNone
		guestWebhookConf.Webhooks = []admissionregistrationv1.ValidatingWebhook{{
			AdmissionReviewVersions: []string{"v1"},
			Name:                    "etcd-client.example.com",
			ClientConfig: admissionregistrationv1.WebhookClientConfig{
				URL: pointer.String("https://etcd-client:2379"),
			},
			Rules: []admissionregistrationv1.RuleWithOperations{{
				Operations: []admissionregistrationv1.OperationType{admissionregistrationv1.Create},
				Rule: admissionregistrationv1.Rule{
					APIGroups:   []string{""},
					APIVersions: []string{"v1"},
					Resources:   []string{"pods"},
				},
			}},
			SideEffects: &sideEffectsNone,
		}}

		if err := guestClient.Create(ctx, guestWebhookConf); err != nil {
			t.Errorf("failed to create webhook: %v", err)
		}

		err := wait.PollUntilContextTimeout(ctx, 5*time.Second, 1*time.Minute, true, func(ctx context.Context) (done bool, err error) {
			webhook := &admissionregistrationv1.ValidatingWebhookConfiguration{}
			if err := guestClient.Get(ctx, client.ObjectKeyFromObject(guestWebhookConf), webhook); err != nil && errors.IsNotFound(err) {
				// webhook has been deleted
				return true, nil
			}

			return false, nil
		})

		if err != nil {
			t.Errorf("failed to ensure guest webhooks validated, violating webhook %s was not deleted: %v", guestWebhookConf.Name, err)
		}

	})
}

func EnsureHCPPodsAffinitiesAndTolerations(t *testing.T, ctx context.Context, client crclient.Client, hostedCluster *hyperv1.HostedCluster) {
	t.Run("EnsureHCPPodsAffinitiesAndTolerations", func(t *testing.T) {
		namespace := manifests.HostedControlPlaneNamespace(hostedCluster.Namespace, hostedCluster.Name)
		awsEbsCsiDriverOperatorPodSubstring := "aws-ebs-csi-driver-operator"
		controlPlaneLabelTolerationKey := "hypershift.openshift.io/control-plane"
		clusterNodeSchedulingAffinityWeight := 100
		controlPlaneNodeSchedulingAffinityWeight := clusterNodeSchedulingAffinityWeight / 2
		colocationLabelKey := "hypershift.openshift.io/hosted-control-plane"

		g := NewGomegaWithT(t)

		var podList corev1.PodList
		if err := client.List(ctx, &podList, crclient.InNamespace(namespace)); err != nil {
			t.Fatalf("failed to list pods in namespace %s: %v", namespace, err)
		}

		namespacedName := types.NamespacedName{
			Namespace: namespace,
			Name:      hostedCluster.Name,
		}

		var hcp hyperv1.HostedControlPlane
		if err := client.Get(ctx, namespacedName, &hcp); err != nil {
			t.Fatalf("failed to get hostedcontrolplane: %v", err)
		}

		expected := suppconfig.DeploymentConfig{
			Scheduling: suppconfig.Scheduling{
				Tolerations: []corev1.Toleration{
					{
						Key:      controlPlaneLabelTolerationKey,
						Operator: corev1.TolerationOpEqual,
						Value:    "true",
						Effect:   corev1.TaintEffectNoSchedule,
					},
					{
						Key:      hyperv1.HostedClusterLabel,
						Operator: corev1.TolerationOpEqual,
						Value:    hcp.Namespace,
						Effect:   corev1.TaintEffectNoSchedule,
					},
				},
				Affinity: &corev1.Affinity{
					NodeAffinity: &corev1.NodeAffinity{
						PreferredDuringSchedulingIgnoredDuringExecution: []corev1.PreferredSchedulingTerm{
							{
								Weight: int32(controlPlaneNodeSchedulingAffinityWeight),
								Preference: corev1.NodeSelectorTerm{
									MatchExpressions: []corev1.NodeSelectorRequirement{
										{
											Key:      controlPlaneLabelTolerationKey,
											Operator: corev1.NodeSelectorOpIn,
											Values:   []string{"true"},
										},
									},
								},
							},
							{
								Weight: int32(clusterNodeSchedulingAffinityWeight),
								Preference: corev1.NodeSelectorTerm{
									MatchExpressions: []corev1.NodeSelectorRequirement{
										{
											Key:      hyperv1.HostedClusterLabel,
											Operator: corev1.NodeSelectorOpIn,
											Values:   []string{hcp.Namespace},
										},
									},
								},
							},
						},
					},
					PodAffinity: &corev1.PodAffinity{
						PreferredDuringSchedulingIgnoredDuringExecution: []corev1.WeightedPodAffinityTerm{
							{
								Weight: 100,
								PodAffinityTerm: corev1.PodAffinityTerm{
									LabelSelector: &metav1.LabelSelector{
										MatchLabels: map[string]string{
											colocationLabelKey: hcp.Namespace,
										},
									},
									TopologyKey: corev1.LabelHostname,
								},
							},
						},
					},
				},
			},
		}

		awsEbsCsiDriverOperatorTolerations := []corev1.Toleration{
			{
				Key:      controlPlaneLabelTolerationKey,
				Operator: corev1.TolerationOpExists,
			},
			{
				Key:      hyperv1.HostedClusterLabel,
				Operator: corev1.TolerationOpEqual,
				Value:    hcp.Namespace,
			},
		}

		for _, pod := range podList.Items {
			// Skip KubeVirt VM worker node related pods
			if pod.Labels["kubevirt.io"] == "virt-launcher" || pod.Labels["app"] == "vmi-console-debug" {
				continue
			}

			// aws-ebs-csi-driver-operator tolerations are set through CSO and are different from the ones in the DC
			if strings.Contains(pod.Name, awsEbsCsiDriverOperatorPodSubstring) {
				g.Expect(pod.Spec.Tolerations).To(ContainElements(awsEbsCsiDriverOperatorTolerations))
			} else {
				g.Expect(pod.Spec.Tolerations).To(ContainElements(expected.Scheduling.Tolerations))
			}

			g.Expect(pod.Spec.Affinity.NodeAffinity.PreferredDuringSchedulingIgnoredDuringExecution).To(ContainElements(expected.Scheduling.Affinity.NodeAffinity.PreferredDuringSchedulingIgnoredDuringExecution))
			g.Expect(pod.Spec.Affinity.PodAffinity.PreferredDuringSchedulingIgnoredDuringExecution).To(ContainElements(expected.Scheduling.Affinity.PodAffinity.PreferredDuringSchedulingIgnoredDuringExecution))

		}
	})
}

func EnsureOnlyRequestServingPodsOnRequestServingNodes(t *testing.T, ctx context.Context, client crclient.Client) {
	g := NewWithT(t)

	var reqServingNodes corev1.NodeList
	if err := client.List(ctx, &reqServingNodes, crclient.MatchingLabels{hyperv1.RequestServingComponentLabel: "true"}); err != nil {
		t.Fatalf("failed to list requestServing nodes in guest cluster: %v", err)
	}

	for _, node := range reqServingNodes.Items {
		var podObj corev1.Pod
		var podList corev1.PodList

		if err := client.List(ctx, &podList, crclient.MatchingFields{podObj.Spec.NodeName: node.Name}); err != nil {
			t.Fatalf("failed to list pods for node: %v , error: %v", node.Name, err)
		}

		for _, pod := range podList.Items {
			g.Expect(pod.Labels).To(HaveKeyWithValue(hyperv1.RequestServingComponentLabel, "true"))
		}
	}
}

func EnsureAllReqServingPodsLandOnReqServingNodes(t *testing.T, ctx context.Context, client crclient.Client) {
	g := NewWithT(t)

	var reqServingPods corev1.PodList
	if err := client.List(ctx, &reqServingPods, crclient.MatchingLabels{hyperv1.RequestServingComponentLabel: "true"}); err != nil {
		t.Fatalf("failed to list requestServing pods in guest cluster: %v", err)
	}

	for _, pod := range reqServingPods.Items {
		var node corev1.Node
		node.Name = pod.Spec.NodeName

		if err := client.Get(ctx, crclient.ObjectKeyFromObject(&node), &node); err != nil {
			t.Fatalf("failed to get node: %v , error: %v", node.Name, err)
		}

		g.Expect(node.Labels).To(HaveKeyWithValue(hyperv1.RequestServingComponentLabel, "true"))
	}
}

func EnsureNoHCPPodsLandOnDefaultNode(t *testing.T, ctx context.Context, client crclient.Client, hostedCluster *hyperv1.HostedCluster) {
	g := NewWithT(t)

	namespace := manifests.HostedControlPlaneNamespace(hostedCluster.Namespace, hostedCluster.Name)
	var podList corev1.PodList
	if err := client.List(ctx, &podList, crclient.InNamespace(namespace)); err != nil {
		t.Fatalf("failed to list pods in namespace %s: %v", namespace, err)
	}

	var HCPNodes corev1.NodeList
	if err := client.List(ctx, &HCPNodes, crclient.MatchingLabels{"hypershift.openshift.io/control-plane": "true"}); err != nil {
		t.Fatalf("failed to list nodes with \"hypershift.openshift.io/control-plane\": \"true\" label: %v", err)
	}

	var hcpNodeNames []string
	for _, node := range HCPNodes.Items {
		hcpNodeNames = append(hcpNodeNames, node.Name)
	}

	for _, pod := range podList.Items {
		g.Expect(pod.Spec.NodeSelector).To(HaveKeyWithValue("hypershift.openshift.io/control-plane", "true"))
		g.Expect(hcpNodeNames).To(ContainElement(pod.Spec.NodeName))
	}
}

func EnsureSATokenNotMountedUnlessNecessary(t *testing.T, ctx context.Context, c crclient.Client, hostedCluster *hyperv1.HostedCluster) {
	t.Run("EnsureSATokenNotMountedUnlessNecessary", func(t *testing.T) {
		g := NewWithT(t)

		hcpNamespace := manifests.HostedControlPlaneNamespace(hostedCluster.Namespace, hostedCluster.Name)

		var pods corev1.PodList
		if err := c.List(ctx, &pods, &crclient.ListOptions{Namespace: hcpNamespace}); err != nil {
			t.Fatalf("failed to list pods in namespace %s: %v", hcpNamespace, err)
		}

		expectedComponentsWithTokenMount := append(expectedKasManagementComponents,
			"aws-ebs-csi-driver-controller",
			"packageserver",
			"csi-snapshot-webhook",
			"csi-snapshot-controller",
		)

		if hostedCluster.Spec.Platform.Type == hyperv1.AzurePlatform {
			expectedComponentsWithTokenMount = append(expectedComponentsWithTokenMount,
				"azure-cloud-controller-manager",
				"azure-disk-csi-driver-controller",
				"azure-disk-csi-driver-operator",
				"azure-file-csi-driver-controller",
				"azure-file-csi-driver-operator",
			)
		}

		if hostedCluster.Spec.Platform.Type == hyperv1.KubevirtPlatform {
			expectedComponentsWithTokenMount = append(expectedComponentsWithTokenMount, hostedCluster.Name+"-test-",
				"kubevirt-cloud-controller-manager",
				"kubevirt-csi-controller",
			)

			for _, pod := range pods.Items {
				if strings.HasSuffix(pod.Name, "-console-logger") {
					expectedComponentsWithTokenMount = append(expectedComponentsWithTokenMount, pod.Name)
				}
			}
		}

		for _, pod := range pods.Items {
			hasPrefix := false
			for _, prefix := range expectedComponentsWithTokenMount {
				if strings.HasPrefix(pod.Name, prefix) {
					hasPrefix = true
					break
				}
			}
			if !hasPrefix {
				for _, volume := range pod.Spec.Volumes {
					g.Expect(volume.Name).ToNot(HavePrefix("kube-api-access-"))
				}
			}
		}
	})
}
