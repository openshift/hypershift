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
	. "github.com/onsi/gomega"
	configv1 "github.com/openshift/api/config/v1"
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
	appsv1 "k8s.io/api/apps/v1"
	authenticationv1 "k8s.io/api/authentication/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/cli-runtime/pkg/genericclioptions"
	"k8s.io/client-go/kubernetes"
	k8s "k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/clientcmd"
	"k8s.io/utils/ptr"
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
	var (
		EnsureFuncNoCrashingPods EnsureFunc = EnsureNoCrashingPods
	)
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
	EnsureFuncNoCrashingPods(t, ctx, &TestParams{
		MgmtClient:    client,
		HostedCluster: hostedCluster,
	})
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
	var (
		EnsureFuncNoCrashingPods EnsureFunc = EnsureNoCrashingPods
	)
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

	EnsureFuncNoCrashingPods(t, ctx, &TestParams{
		MgmtClient:    client,
		HostedCluster: hostedCluster,
	})
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
