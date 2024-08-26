package util

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/go-logr/logr"
	"github.com/go-logr/zapr"
	"github.com/google/go-cmp/cmp"
	. "github.com/onsi/gomega"
	configv1 "github.com/openshift/api/config/v1"
	routev1 "github.com/openshift/api/route/v1"
	routev1client "github.com/openshift/client-go/route/clientset/versioned"
	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	hcmetrics "github.com/openshift/hypershift/hypershift-operator/controllers/hostedcluster/metrics"
	"github.com/openshift/hypershift/hypershift-operator/controllers/manifests"
	"github.com/openshift/hypershift/support/conditions"
	suppconfig "github.com/openshift/hypershift/support/config"
	"github.com/openshift/hypershift/support/util"
	"github.com/openshift/library-go/test/library/metrics"
	prometheusv1 "github.com/prometheus/client_golang/api/prometheus/v1"
	"github.com/prometheus/common/model"
	"go.uber.org/zap/zaptest"
	admissionregistrationv1 "k8s.io/api/admissionregistration/v1"
	appsv1 "k8s.io/api/apps/v1"
	authenticationv1 "k8s.io/api/authentication/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	kapierror "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/cli-runtime/pkg/genericclioptions"
	"k8s.io/client-go/kubernetes"
	k8s "k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/clientcmd"
	"k8s.io/utils/pointer"
	"k8s.io/utils/ptr"
	capiv1 "sigs.k8s.io/cluster-api/api/v1beta1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	crclient "sigs.k8s.io/controller-runtime/pkg/client"
)

var expectedKasManagementComponents = []string{
	"cluster-network-operator",
	"ignition-server",
	"cluster-storage-operator",
	"csi-snapshot-controller-operator",
	"machine-approver",
	"cluster-autoscaler",
	"cluster-node-tuning-operator",
	"capi-provider-controller-manager",
	"capi-provider",
	"cluster-api",
	"etcd",
	"control-plane-operator",
	"control-plane-pki-operator",
	"hosted-cluster-config-operator",
	"cloud-controller-manager",
	"olm-collect-profiles",
	"aws-ebs-csi-driver-operator",
}

func UpdateObject[T crclient.Object](t *testing.T, ctx context.Context, client crclient.Client, original T, mutate func(obj T)) error {
	return wait.PollUntilContextTimeout(ctx, time.Second, time.Minute*1, true, func(ctx context.Context) (done bool, err error) {
		if err := client.Get(ctx, crclient.ObjectKeyFromObject(original), original); err != nil {
			t.Logf("failed to retrieve object %s, will retry: %v", original.GetName(), err)
			return false, nil
		}

		obj := original.DeepCopyObject().(T)
		mutate(obj)

		if err := client.Patch(ctx, obj, crclient.MergeFrom(original)); err != nil {
			t.Logf("failed to patch object %s, will retry: %v", original.GetName(), err)
			if errors.IsConflict(err) {
				return false, nil
			}
			return false, err
		}

		return true, nil
	})
}

// DeleteNamespace deletes and finalizes the given namespace, logging any failures
// along the way.
func DeleteNamespace(t *testing.T, ctx context.Context, client crclient.Client, namespace string) error {
	if os.Getenv("EVENTUALLY_VERBOSE") != "false" {
		t.Logf("Deleting namespace %s", namespace)
	}
	err := wait.PollUntilContextTimeout(ctx, 10*time.Second, 20*time.Minute, true, func(ctx context.Context) (done bool, err error) {
		ns := &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: namespace}}
		if err := client.Delete(ctx, ns, &crclient.DeleteOptions{}); err != nil {
			if errors.IsNotFound(err) {
				return true, nil
			}
			t.Logf("Failed to delete namespace: %s, will retry: %v", namespace, err)
			return false, nil
		}
		return true, nil
	})
	if err != nil {
		return fmt.Errorf("failed to delete namespace: %w", err)
	}

	if os.Getenv("EVENTUALLY_VERBOSE") != "false" {
		t.Logf("Waiting for namespace %s to be finalized", namespace)
	}
	err = wait.PollUntilContextTimeout(ctx, 10*time.Second, 20*time.Minute, true, func(ctx context.Context) (done bool, err error) {
		ns := &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: namespace}}
		if err := client.Get(ctx, crclient.ObjectKeyFromObject(ns), ns); err != nil {
			if errors.IsNotFound(err) {
				return true, nil
			}
			t.Logf("Failed to get namespace: %s. %v", namespace, err)
			return false, nil
		}
		return false, nil
	})
	if err != nil {
		return fmt.Errorf("namespace still exists after deletion timeout: %v", err)
	}
	if os.Getenv("EVENTUALLY_VERBOSE") != "false" {
		t.Logf("Deleted namespace %s", namespace)
	}
	return nil
}

func WaitForGuestKubeConfig(t *testing.T, ctx context.Context, client crclient.Client, hostedCluster *hyperv1.HostedCluster) []byte {
	var guestKubeConfigSecretRef crclient.ObjectKey
	EventuallyObject(t, ctx, fmt.Sprintf("kubeconfig to be published for HostedCluster %s/%s", hostedCluster.Namespace, hostedCluster.Name),
		func(ctx context.Context) (*hyperv1.HostedCluster, error) {
			err := client.Get(ctx, crclient.ObjectKeyFromObject(hostedCluster), hostedCluster)
			return hostedCluster, err
		},
		[]Predicate[*hyperv1.HostedCluster]{
			func(cluster *hyperv1.HostedCluster) (done bool, reasons string, err error) {
				guestKubeConfigSecretRef = crclient.ObjectKey{
					Namespace: hostedCluster.Namespace,
					Name:      ptr.Deref(hostedCluster.Status.KubeConfig, corev1.LocalObjectReference{}).Name,
				}
				return hostedCluster.Status.KubeConfig != nil, fmt.Sprintf("expected a kubeconfig reference in status"), nil
			},
		},
	)

	var data []byte
	EventuallyObject(t, ctx, "kubeconfig secret to have data",
		func(ctx context.Context) (*corev1.Secret, error) {
			var guestKubeConfigSecret corev1.Secret
			err := client.Get(ctx, guestKubeConfigSecretRef, &guestKubeConfigSecret)
			return &guestKubeConfigSecret, err
		},
		[]Predicate[*corev1.Secret]{
			func(secret *corev1.Secret) (done bool, reasons string, err error) {
				var hasData bool
				data, hasData = secret.Data["kubeconfig"]
				return hasData, "expected secret to contain kubeconfig in data", nil
			},
		},
	)
	return data
}

func WaitForGuestClient(t *testing.T, ctx context.Context, client crclient.Client, hostedCluster *hyperv1.HostedCluster) crclient.Client {
	g := NewWithT(t)
	guestKubeConfigSecretData := WaitForGuestKubeConfig(t, ctx, client, hostedCluster)

	guestConfig, err := clientcmd.RESTConfigFromKubeConfig(guestKubeConfigSecretData)
	g.Expect(err).NotTo(HaveOccurred(), "couldn't load guest kubeconfig")
	// we know we're the only real clients for these test servers, so turn off client-side throttling
	guestConfig.QPS = -1
	guestConfig.Burst = -1

	kubeClient, err := kubernetes.NewForConfig(guestConfig)
	if err != nil {
		t.Fatalf("failed to create kube client for guest cluster: %v", err)
	}
	EventuallyObject(t, ctx, "a successful connection to the guest API server",
		func(ctx context.Context) (*authenticationv1.SelfSubjectReview, error) {
			return kubeClient.AuthenticationV1().SelfSubjectReviews().Create(ctx, &authenticationv1.SelfSubjectReview{}, metav1.CreateOptions{})
		}, nil, WithTimeout(30*time.Minute),
	)
	guestClient, err := crclient.New(guestConfig, crclient.Options{Scheme: scheme})
	if err != nil {
		t.Fatalf("could not create client for guest cluster: %v", err)
	}
	return guestClient
}

func GetGuestKubeconfigHost(t *testing.T, ctx context.Context, client crclient.Client, hostedCluster *hyperv1.HostedCluster) (string, error) {
	guestKubeConfigSecretData := WaitForGuestKubeConfig(t, ctx, client, hostedCluster)
	guestConfig, err := clientcmd.RESTConfigFromKubeConfig(guestKubeConfigSecretData)
	if err != nil {
		return "", fmt.Errorf("couldn't load guest kubeconfig: %v", err)
	}

	host := guestConfig.Host
	if len(host) == 0 {
		return "", fmt.Errorf("guest kubeconfig host is empty")
	}
	return host, nil
}

func WaitForGuestKubeconfigHostUpdate(t *testing.T, ctx context.Context, client crclient.Client, hostedCluster *hyperv1.HostedCluster, oldHost string) {
	g := NewWithT(t)
	waitTimeout := 30 * time.Minute
	pollingInterval := 15 * time.Second

	t.Logf("Waiting for guest kubeconfig host update")
	var newHost string
	var getHostError error
	err := wait.PollUntilContextTimeout(ctx, pollingInterval, waitTimeout, true, func(ctx context.Context) (done bool, err error) {
		newHost, getHostError = GetGuestKubeconfigHost(t, ctx, client, hostedCluster)
		if getHostError != nil {
			t.Logf("failed to get guest kubeconfig host: %v", getHostError)
			return false, nil
		}
		if newHost == oldHost {
			t.Logf("guest kubeconfig host is not yet updated, keep polling")
			return false, nil
		}
		return true, nil
	})
	g.Expect(err).NotTo(HaveOccurred(), "failed to wait for guest kubeconfig host update")
	t.Logf("Guest kubeconfig host switched from %s to %s", oldHost, newHost)
}

func WaitForNReadyNodes(t *testing.T, ctx context.Context, client crclient.Client, n int32, platform hyperv1.PlatformType) []corev1.Node {
	return WaitForNReadyNodesWithOptions(t, ctx, client, n, platform, "")
}

func WaitForReadyNodesByNodePool(t *testing.T, ctx context.Context, client crclient.Client, np *hyperv1.NodePool, platform hyperv1.PlatformType, opts ...NodePoolPollOption) []corev1.Node {
	return WaitForNReadyNodesWithOptions(t, ctx, client, *np.Spec.Replicas, platform, fmt.Sprintf("for NodePool %s/%s", np.Namespace, np.Name), append(opts, WithClientOptions(crclient.MatchingLabelsSelector{Selector: labels.SelectorFromSet(labels.Set{hyperv1.NodePoolLabel: np.Name})}))...)
}

type NodePoolPollOptions struct {
	collectionPredicates []Predicate[[]*corev1.Node]
	predicates           []Predicate[*corev1.Node]
	clientOpts           []crclient.ListOption
	suffix               string
}

type NodePoolPollOption func(*NodePoolPollOptions)

func WithCollectionPredicates(predicates ...Predicate[[]*corev1.Node]) NodePoolPollOption {
	return func(options *NodePoolPollOptions) {
		options.collectionPredicates = predicates
	}
}

func WithPredicates(predicates ...Predicate[*corev1.Node]) NodePoolPollOption {
	return func(options *NodePoolPollOptions) {
		options.predicates = predicates
	}
}

func WithClientOptions(clientOpts ...crclient.ListOption) NodePoolPollOption {
	return func(options *NodePoolPollOptions) {
		options.clientOpts = clientOpts
	}
}

func WithSuffix(suffix string) NodePoolPollOption {
	return func(options *NodePoolPollOptions) {
		options.suffix = suffix
	}
}

func WaitForNReadyNodesWithOptions(t *testing.T, ctx context.Context, client crclient.Client, n int32, platform hyperv1.PlatformType, suffix string, opts ...NodePoolPollOption) []corev1.Node {
	options := &NodePoolPollOptions{}
	for _, opt := range opts {
		opt(options)
	}
	// waitTimeout for nodes to become Ready
	waitTimeout := 30 * time.Minute
	switch platform {
	case hyperv1.KubevirtPlatform:
		waitTimeout = 45 * time.Minute
	case hyperv1.PowerVSPlatform:
		waitTimeout = 60 * time.Minute
	}
	nodes := &corev1.NodeList{}
	if suffix != "" {
		suffix = " " + suffix
	}
	if options.suffix != "" {
		suffix += " " + options.suffix
	}
	EventuallyObjects(t, ctx, fmt.Sprintf("%d nodes to become ready%s", n, suffix),
		func(ctx context.Context) ([]*corev1.Node, error) {
			err := client.List(ctx, nodes, options.clientOpts...)
			items := make([]*corev1.Node, len(nodes.Items))
			for i := range nodes.Items {
				items[i] = &nodes.Items[i]
			}
			return items, err
		},
		append([]Predicate[[]*corev1.Node]{
			func(nodes []*corev1.Node) (done bool, reasons string, err error) {
				want, got := int(n), len(nodes)
				return want == got, fmt.Sprintf("expected %d nodes, got %d", want, got), nil
			},
		}, options.collectionPredicates...),
		append([]Predicate[*corev1.Node]{
			ConditionPredicate[*corev1.Node](Condition{
				Type:   string(corev1.NodeReady),
				Status: metav1.ConditionTrue,
			}),
		}, options.predicates...),
		WithTimeout(waitTimeout),
	)
	return nodes.Items
}

func WaitForImageRollout(t *testing.T, ctx context.Context, client crclient.Client, hostedCluster *hyperv1.HostedCluster, image string) {
	EventuallyObject(t, ctx, fmt.Sprintf("HostedCluster %s/%s to rollout image %s", hostedCluster.Namespace, hostedCluster.Name, image),
		func(ctx context.Context) (*hyperv1.HostedCluster, error) {
			hc := &hyperv1.HostedCluster{}
			err := client.Get(ctx, crclient.ObjectKeyFromObject(hostedCluster), hc)
			return hc, err
		},
		[]Predicate[*hyperv1.HostedCluster]{
			ConditionPredicate[*hyperv1.HostedCluster](Condition{
				Type:   string(hyperv1.HostedClusterAvailable),
				Status: metav1.ConditionTrue,
			}),
			ConditionPredicate[*hyperv1.HostedCluster](Condition{
				Type:   string(hyperv1.HostedClusterProgressing),
				Status: metav1.ConditionFalse,
			}),
			func(hostedCluster *hyperv1.HostedCluster) (done bool, reasons string, err error) {
				if wanted, got := image, ptr.Deref(hostedCluster.Status.Version, hyperv1.ClusterVersionStatus{}).Desired.Image; wanted != got {
					return false, fmt.Sprintf("wanted HostedCluster to desire image %s, got %s", wanted, got), nil
				}
				if len(ptr.Deref(hostedCluster.Status.Version, hyperv1.ClusterVersionStatus{}).History) == 0 {
					return false, "HostedCluster has no version history", nil
				}
				if wanted, got := hostedCluster.Status.Version.Desired.Image, hostedCluster.Status.Version.History[0].Image; wanted != got {
					return false, fmt.Sprintf("desired image %s doesn't match most recent image in history %s", wanted, got), nil
				}
				if wanted, got := configv1.CompletedUpdate, hostedCluster.Status.Version.History[0].State; wanted != got {
					return false, fmt.Sprintf("wanted most recent version history to have state %s, has state %s", wanted, got), nil
				}
				return true, fmt.Sprintf("image %s rolled out", image), nil
			},
		},
		WithTimeout(30*time.Minute),
	)
}

func WaitForConditionsOnHostedControlPlane(t *testing.T, ctx context.Context, client crclient.Client, hostedCluster *hyperv1.HostedCluster, image string) {
	var predicates []Predicate[*hyperv1.HostedControlPlane]
	for _, conditionType := range []hyperv1.ConditionType{
		hyperv1.HostedControlPlaneAvailable,
		hyperv1.EtcdAvailable,
		hyperv1.KubeAPIServerAvailable,
		hyperv1.InfrastructureReady,
		hyperv1.ValidHostedControlPlaneConfiguration,
	} {
		predicates = append(predicates, ConditionPredicate[*hyperv1.HostedControlPlane](Condition{
			Type:   string(conditionType),
			Status: metav1.ConditionTrue,
		}))
	}

	namespace := manifests.HostedControlPlaneNamespace(hostedCluster.Namespace, hostedCluster.Name)
	EventuallyObject(t, ctx, fmt.Sprintf("HostedControlPlane %s/%s to be ready", namespace, hostedCluster.Name),
		func(ctx context.Context) (*hyperv1.HostedControlPlane, error) {
			hcp := &hyperv1.HostedControlPlane{}
			err := client.Get(ctx, types.NamespacedName{Namespace: namespace, Name: hostedCluster.Name}, hcp)
			return hcp, err
		}, predicates, WithTimeout(30*time.Minute),
	)
}

func WaitForNodePoolDesiredNodes(t *testing.T, ctx context.Context, client crclient.Client, hostedCluster *hyperv1.HostedCluster) {
	EventuallyObjects(t, ctx, fmt.Sprintf("NodePools for HostedCluster %s/%s to have all of their desired nodes", hostedCluster.Namespace, hostedCluster.Name),
		func(ctx context.Context) ([]*hyperv1.NodePool, error) {
			list := &hyperv1.NodePoolList{}
			err := client.List(ctx, list, crclient.InNamespace(hostedCluster.Namespace))
			nodePools := make([]*hyperv1.NodePool, len(list.Items))
			for i := range list.Items {
				nodePools[i] = &list.Items[i]
			}
			return nodePools, err
		}, nil,
		[]Predicate[*hyperv1.NodePool]{
			func(nodePool *hyperv1.NodePool) (done bool, reasons string, err error) {
				want, got := ptr.Deref(nodePool.Spec.Replicas, 0), nodePool.Status.Replicas
				return want == got, fmt.Sprintf("expected %d replicas, got %d", want, got), nil
			},
		},
		WithTimeout(30*time.Minute),
	)
}

func EnsureNoCrashingPods(t *testing.T, ctx context.Context, client crclient.Client, hostedCluster *hyperv1.HostedCluster) {
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

func NoticePreemptionOrFailedScheduling(t *testing.T, ctx context.Context, client crclient.Client, hostedCluster *hyperv1.HostedCluster) {
	t.Run("NoticePreemptionOrFailedScheduling", func(t *testing.T) {
		namespace := manifests.HostedControlPlaneNamespace(hostedCluster.Namespace, hostedCluster.Name)

		var eventList corev1.EventList
		if err := client.List(ctx, &eventList, crclient.InNamespace(namespace)); err != nil {
			t.Fatalf("failed to list events in namespace %s: %v", namespace, err)
		}
		for _, event := range eventList.Items {
			if event.Reason == "FailedScheduling" || event.Reason == "Preempted" {
				// "error: " is to trigger prow syntax highlight in prow
				t.Logf("error: non-fatal, observed FailedScheduling or Preempted event: %s", event.Message)
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

func getComponentName(pod *corev1.Pod) string {
	if pod.Labels["app"] != "" {
		return pod.Labels["app"]
	}

	if pod.Labels["name"] != "" {
		return pod.Labels["name"]
	}

	if strings.HasPrefix(pod.Labels["job-name"], "olm-collect-profiles") {
		return "olm-collect-profiles"
	}

	return ""
}

func checkPodsHaveLabel(ctx context.Context, c crclient.Client, allowedComponents []string, namespace string, labels map[string]string) error {
	// Get all Pods with wanted label.
	podList := &corev1.PodList{}
	err := c.List(ctx, podList, client.InNamespace(namespace), client.MatchingLabels(labels))
	if err != nil {
		return fmt.Errorf("failed to list pods: %w", err)
	}

	// Get the component name for each labelled pod and ensure it exists in the components slice
	for _, pod := range podList.Items {
		if pod.Labels[suppconfig.NeedManagementKASAccessLabel] == "" {
			continue
		}
		componentName := getComponentName(&pod)
		if componentName == "" {
			return fmt.Errorf("unable to determine component name for pod that has NeedManagementKASAccessLabel: %s", pod.Name)
		}
		allowed := false
		for _, component := range allowedComponents {
			if component == componentName {
				allowed = true
				break
			}
		}
		if !allowed {
			return fmt.Errorf("NeedManagementKASAccessLabel label is not allowed on component: %s", componentName)
		}
	}

	return nil
}

func RunCommandInPod(ctx context.Context, c crclient.Client, component, namespace string, command []string, containerName string, timeout time.Duration) (string, error) {
	podList := &corev1.PodList{}
	if err := c.List(ctx, podList,
		client.InNamespace(namespace),
		client.MatchingLabels{"app": component}); err != nil {
		return "", fmt.Errorf("failed to list Pods: %w", err)
	}
	if len(podList.Items) < 1 {
		return "", fmt.Errorf("pods for component %q not found", component)
	}

	restConfig, err := GetConfig()
	if err != nil {
		return "", fmt.Errorf("failed to get restConfig; %w", err)
	}

	stdOut := new(bytes.Buffer)
	podExecuter := PodExecOptions{
		StreamOptions: StreamOptions{
			IOStreams: genericclioptions.IOStreams{
				Out:    stdOut,
				ErrOut: os.Stderr,
			},
		},
		Command:       command,
		Namespace:     namespace,
		PodName:       podList.Items[0].Name,
		Config:        restConfig,
		ContainerName: containerName,
		Timeout:       timeout,
	}

	err = podExecuter.Run(ctx)
	return stdOut.String(), err
}

func getPrometheusToken(ctx context.Context, secretName string, client crclient.Client) ([]byte, error) {
	if secretName == "" {
		return createPrometheusToken(ctx)
	} else {
		return getTokenFromSecret(ctx, secretName, client)
	}
}

func createPrometheusToken(ctx context.Context) ([]byte, error) {
	cli, err := createK8sClient()
	if err != nil {
		return nil, err
	}

	tokenReq, err := cli.CoreV1().ServiceAccounts("openshift-monitoring").CreateToken(
		ctx,
		"prometheus-k8s",
		&authenticationv1.TokenRequest{
			Spec: authenticationv1.TokenRequestSpec{
				// Avoid specifying any audiences so that the token will be
				// issued for the default audience of the issuer.
			},
		},
		metav1.CreateOptions{},
	)

	if err != nil {
		return nil, fmt.Errorf("failed to create token; %w", err)
	}

	return []byte(tokenReq.Status.Token), nil
}

func getTokenFromSecret(ctx context.Context, secretName string, client crclient.Client) ([]byte, error) {
	tokenSecret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      secretName,
			Namespace: "hypershift",
		},
	}
	if err := client.Get(ctx, crclient.ObjectKeyFromObject(tokenSecret), tokenSecret); err != nil {
		return nil, fmt.Errorf("failed to get hypershift operator token secret: %w", err)
	}
	token, ok := tokenSecret.Data["token"]
	if !ok {
		return nil, fmt.Errorf("token secret did not contain a token value")
	}
	return token, nil
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

func EnsureAPIUX(t *testing.T, ctx context.Context, hostClient crclient.Client, hostedCluster *hyperv1.HostedCluster) {
	t.Run("EnsureHostedClusterImmutability", func(t *testing.T) {
		g := NewWithT(t)

		err := UpdateObject(t, ctx, hostClient, hostedCluster, func(obj *hyperv1.HostedCluster) {
			for i, svc := range obj.Spec.Services {
				if svc.Service == hyperv1.APIServer {
					svc.Type = hyperv1.NodePort
					obj.Spec.Services[i] = svc
				}
			}
		})
		g.Expect(err).To(HaveOccurred())
		g.Expect(err.Error()).To(ContainSubstring("Services is immutable"))
	})
}

func EnsureSecretEncryptedUsingKMS(t *testing.T, ctx context.Context, hostedCluster *hyperv1.HostedCluster, guestClient crclient.Client) {
	t.Run("EnsureSecretEncryptedUsingKMS", func(t *testing.T) {
		ensureSecretEncryptedUsingKMS(t, ctx, hostedCluster, guestClient, "k8s:enc:kms:")
	})
}

func EnsureSecretEncryptedUsingKMSV1(t *testing.T, ctx context.Context, hostedCluster *hyperv1.HostedCluster, guestClient crclient.Client) {
	t.Run("EnsureSecretEncryptedUsingKMSV1", func(t *testing.T) {
		ensureSecretEncryptedUsingKMS(t, ctx, hostedCluster, guestClient, "k8s:enc:kms:v1")
	})
}

func EnsureSecretEncryptedUsingKMSV2(t *testing.T, ctx context.Context, hostedCluster *hyperv1.HostedCluster, guestClient crclient.Client) {
	t.Run("EnsureSecretEncryptedUsingKMSV2", func(t *testing.T) {
		ensureSecretEncryptedUsingKMS(t, ctx, hostedCluster, guestClient, "k8s:enc:kms:v2")
	})
}

func ensureSecretEncryptedUsingKMS(t *testing.T, ctx context.Context, hostedCluster *hyperv1.HostedCluster, guestClient crclient.Client, expectedPrefix string) {
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

	if !strings.Contains(out.String(), expectedPrefix) {
		t.Errorf("secret is not encrypted using kms")
	}
}

func createK8sClient() (*k8s.Clientset, error) {
	config, err := GetConfig()
	if err != nil {
		return nil, fmt.Errorf("unable to get kubernetes config: %w", err)
	}

	cli, err := k8s.NewForConfig(config)
	if err != nil {
		return nil, fmt.Errorf("unable to get kubernetes client: %w", err)
	}

	return cli, nil
}

func NewLogr(t *testing.T) logr.Logger {
	return zapr.NewLogger(zaptest.NewLogger(t))
}

func CorrelateDaemonSet(ds *appsv1.DaemonSet, nodePool *hyperv1.NodePool, dsName string) {

	for _, c := range ds.Spec.Template.Spec.Containers {
		if c.Name == ds.Name {
			c.Name = dsName
		}
	}

	ds.Name = dsName
	ds.ObjectMeta.Labels = make(map[string]string)
	ds.ObjectMeta.Labels["hypershift.openshift.io/nodePool"] = nodePool.Name

	ds.Spec.Selector.MatchLabels["name"] = dsName
	ds.Spec.Selector.MatchLabels["hypershift.openshift.io/nodePool"] = nodePool.Name

	ds.Spec.Template.ObjectMeta.Labels["name"] = dsName
	ds.Spec.Template.ObjectMeta.Labels["hypershift.openshift.io/nodePool"] = nodePool.Name

	// Set NodeSelector for the DS
	ds.Spec.Template.Spec.NodeSelector = make(map[string]string)
	ds.Spec.Template.Spec.NodeSelector["hypershift.openshift.io/nodePool"] = nodePool.Name

}

func NewPrometheusClient(ctx context.Context) (prometheusv1.API, error) {
	config, err := GetConfig()
	if err != nil {
		return nil, err
	}

	kubeClient, err := kubernetes.NewForConfig(config)
	if err != nil {
		panic(err)
	}
	routeClient, err := routev1client.NewForConfig(config)
	if err != nil {
		panic(err)
	}
	prometheusClient, err := metrics.NewPrometheusClient(ctx, kubeClient, routeClient)
	if err != nil {
		panic(err)
	}
	return prometheusClient, nil
}

// PrometheusResponse is used to contain prometheus query results
type PrometheusResponse struct {
	Data prometheusResponseData `json:"data"`
}

type prometheusResponseData struct {
	Result model.Vector `json:"result"`
}

func RunQueryAtTime(ctx context.Context, log logr.Logger, prometheusClient prometheusv1.API, query string, evaluationTime time.Time) (*PrometheusResponse, error) {
	result, warnings, err := prometheusClient.Query(ctx, query, evaluationTime)
	if err != nil {
		return nil, err
	}
	if len(warnings) > 0 {
		log.Info(fmt.Sprintf("#### warnings \n\t%v\n", strings.Join(warnings, "\n\t")))
	}
	if result.Type() != model.ValVector {
		return nil, fmt.Errorf("result type is not the vector: %v", result.Type())
	}
	return &PrometheusResponse{
		Data: prometheusResponseData{
			Result: result.(model.Vector),
		},
	}, nil
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

const (
	// Metrics
	// TODO (jparrill): We need to separate the metrics.go from the main pkg in the hypershift-operator.
	//     Delete these references when it's done and import it from there
	HypershiftOperatorInfoName = "hypershift_operator_info"
)

// Verifies that the given metrics are defined for the given hosted cluster if areMetricsExpectedToBePresent is set to true.
// Verifies that the given metrics are not defined otherwise.
func ValidateMetrics(t *testing.T, ctx context.Context, hc *hyperv1.HostedCluster, metricsNames []string, areMetricsExpectedToBePresent bool) {
	t.Run("ValidateMetricsAreExposed", func(t *testing.T) {
		// TODO (alberto) this test should pass in None.
		// https://gcsweb-ci.apps.ci.l2s4.p1.openshiftapps.com/gcs/origin-ci-test/pr-logs/pull/openshift_hypershift/2459/pull-ci-openshift-hypershift-main-e2e-aws/1650438383060652032/artifacts/e2e-aws/run-e2e/artifacts/TestNoneCreateCluster_PreTeardownClusterDump/
		// https://storage.googleapis.com/origin-ci-test/pr-logs/pull/openshift_hypershift/2459/pull-ci-openshift-hypershift-main-e2e-aws/1650438383060652032/build-log.txt
		// https://prow.ci.openshift.org/view/gs/origin-ci-test/pr-logs/pull/openshift_hypershift/2459/pull-ci-openshift-hypershift-main-e2e-aws/1650438383060652032
		if hc.Spec.Platform.Type == hyperv1.NonePlatform {
			t.Skip("skipping on None platform")
		}

		if hc.Spec.Platform.Type == hyperv1.AzurePlatform {
			t.Skip("skipping on Azure platform")
		}

		g := NewWithT(t)

		prometheusClient, err := NewPrometheusClient(ctx)
		g.Expect(err).ToNot(HaveOccurred())

		// Polling to prevent races with prometheus scrape interval.
		err = wait.PollUntilContextTimeout(ctx, 10*time.Second, 5*time.Minute, true, func(ctx context.Context) (bool, error) {
			for _, metricName := range metricsNames {
				// Query fo HC specific metrics by hc.name.
				query := fmt.Sprintf("%v{name=\"%s\"}", metricName, hc.Name)
				if metricName == HypershiftOperatorInfoName {
					// Query HO info metric
					query = HypershiftOperatorInfoName
				}
				if strings.HasPrefix(metricName, "hypershift_nodepools") {
					query = fmt.Sprintf("%v{cluster_name=\"%s\"}", metricName, hc.Name)
				}
				// upgrade metric is only available for TestUpgradeControlPlane
				if metricName == hcmetrics.UpgradingDurationMetricName && !strings.HasPrefix("TestUpgradeControlPlane", t.Name()) {
					continue
				}

				result, err := RunQueryAtTime(ctx, NewLogr(t), prometheusClient, query, time.Now())
				if err != nil {
					return false, err
				}

				if areMetricsExpectedToBePresent {
					if len(result.Data.Result) < 1 {
						t.Logf("Expected results for metric %q, found none", metricName)
						return false, nil
					}
				} else {
					if len(result.Data.Result) > 0 {
						t.Logf("Expected 0 results for metric %q, found %d", metricName, len(result.Data.Result))
						return false, nil
					}
				}
			}
			return true, nil
		})
		if err != nil {
			t.Errorf("Failed to validate all metrics")
		}
	})
}

func ValidatePublicCluster(t *testing.T, ctx context.Context, client crclient.Client, hostedCluster *hyperv1.HostedCluster, clusterOpts *PlatformAgnosticOptions) {
	g := NewWithT(t)

	// Sanity check the cluster by waiting for the nodes to report ready
	guestClient := WaitForGuestClient(t, ctx, client, hostedCluster)

	// Wait for Nodes to be Ready
	numNodes := clusterOpts.NodePoolReplicas * int32(len(clusterOpts.AWSPlatform.Zones))
	WaitForNReadyNodes(t, ctx, guestClient, numNodes, hostedCluster.Spec.Platform.Type)

	// rollout will not complete if there are no worker nodes.
	if numNodes > 0 {
		WaitForImageRollout(t, ctx, client, hostedCluster, clusterOpts.ReleaseImage)
	}

	err := client.Get(ctx, crclient.ObjectKeyFromObject(hostedCluster), hostedCluster)
	g.Expect(err).NotTo(HaveOccurred(), "failed to get hostedcluster")

	serviceStrategy := util.ServicePublishingStrategyByTypeByHC(hostedCluster, hyperv1.APIServer)
	g.Expect(serviceStrategy).ToNot(BeNil())
	if serviceStrategy.Type == hyperv1.Route && serviceStrategy.Route != nil && serviceStrategy.Route.Hostname != "" {
		g.Expect(hostedCluster.Status.ControlPlaneEndpoint.Host).To(Equal(serviceStrategy.Route.Hostname))
	} else {
		// sanity check
		g.Expect(hostedCluster.Status.ControlPlaneEndpoint.Host).ToNot(ContainSubstring("hypershift.local"))
	}

	validateHostedClusterConditions(t, ctx, client, hostedCluster, numNodes > 0, 10*time.Minute)

	EnsureNodeCountMatchesNodePoolReplicas(t, ctx, client, guestClient, hostedCluster.Spec.Platform.Type, hostedCluster.Namespace)
	EnsureNoCrashingPods(t, ctx, client, hostedCluster)
	EnsureOAPIMountsTrustBundle(t, context.Background(), client, hostedCluster)
	EnsureGuestWebhooksValidated(t, ctx, guestClient)

	if numNodes > 0 {
		EnsureNodeCommunication(t, ctx, client, hostedCluster)
	}

	if hostedCluster.Spec.Platform.Type == hyperv1.AWSPlatform {
		g.Expect(hostedCluster.Spec.Configuration.Ingress.LoadBalancer.Platform.AWS.Type).To(Equal(configv1.NLB))
	}
}

func ValidatePrivateCluster(t *testing.T, ctx context.Context, client crclient.Client, hostedCluster *hyperv1.HostedCluster, clusterOpts *PlatformAgnosticOptions) {
	g := NewWithT(t)

	// We can't wait for a guest client since we don't have connectivity to the API server
	WaitForGuestKubeConfig(t, ctx, client, hostedCluster)

	// Ensure NodePools have all Nodes ready.
	WaitForNodePoolDesiredNodes(t, ctx, client, hostedCluster)

	numNodes := clusterOpts.NodePoolReplicas * int32(len(clusterOpts.AWSPlatform.Zones))
	// rollout will not complete if there are no worker nodes.
	if numNodes > 0 {
		WaitForImageRollout(t, ctx, client, hostedCluster, clusterOpts.ReleaseImage)
	}

	err := client.Get(ctx, crclient.ObjectKeyFromObject(hostedCluster), hostedCluster)
	g.Expect(err).NotTo(HaveOccurred(), "failed to get hostedcluster")

	serviceStrategy := util.ServicePublishingStrategyByTypeByHC(hostedCluster, hyperv1.APIServer)
	g.Expect(serviceStrategy).ToNot(BeNil())
	if serviceStrategy.Route != nil && serviceStrategy.Route.Hostname != "" {
		g.Expect(hostedCluster.Status.ControlPlaneEndpoint.Host).To(Equal(serviceStrategy.Route.Hostname))
	} else {
		// sanity check
		g.Expect(hostedCluster.Status.ControlPlaneEndpoint.Host).ToNot(ContainSubstring("hypershift.local"))
	}

	validateHostedClusterConditions(t, ctx, client, hostedCluster, numNodes > 0, 10*time.Minute)

	EnsureNoCrashingPods(t, ctx, client, hostedCluster)
	EnsureOAPIMountsTrustBundle(t, context.Background(), client, hostedCluster)

	if hostedCluster.Spec.Platform.Type == hyperv1.AWSPlatform {
		g.Expect(hostedCluster.Spec.Configuration.Ingress.LoadBalancer.Platform.AWS.Type).To(Equal(configv1.NLB))
	}

}

func validateHostedClusterConditions(t *testing.T, ctx context.Context, client crclient.Client, hostedCluster *hyperv1.HostedCluster, hasWorkerNodes bool, timeout time.Duration) {
	expectedConditions := conditions.ExpectedHCConditions(hostedCluster)
	if !hasWorkerNodes {
		expectedConditions[hyperv1.ClusterVersionAvailable] = metav1.ConditionFalse
		expectedConditions[hyperv1.ClusterVersionSucceeding] = metav1.ConditionFalse
		expectedConditions[hyperv1.ClusterVersionProgressing] = metav1.ConditionTrue
		delete(expectedConditions, hyperv1.ValidKubeVirtInfraNetworkMTU)
		delete(expectedConditions, hyperv1.KubeVirtNodesLiveMigratable)
	}

	var predicates []Predicate[*hyperv1.HostedCluster]
	for conditionType, conditionStatus := range expectedConditions {
		predicates = append(predicates, ConditionPredicate[*hyperv1.HostedCluster](Condition{
			Type:   string(conditionType),
			Status: conditionStatus,
		}))
	}
	EventuallyObject(t, ctx, fmt.Sprintf("HostedCluster %s/%s to have valid conditions", hostedCluster.Namespace, hostedCluster.Name),
		func(ctx context.Context) (*hyperv1.HostedCluster, error) {
			hc := &hyperv1.HostedCluster{}
			err := client.Get(ctx, crclient.ObjectKeyFromObject(hostedCluster), hc)
			return hc, err
		}, predicates, WithTimeout(timeout), WithoutConditionDump(),
	)
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
