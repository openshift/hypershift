package util

import (
	"bytes"
	"context"
	"crypto/x509"
	"crypto/x509/pkix"
	"fmt"
	"net"
	"os"
	"reflect"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	. "github.com/onsi/gomega"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	awsinfra "github.com/openshift/hypershift/cmd/infra/aws"
	awsutil "github.com/openshift/hypershift/cmd/infra/aws/util"
	awsprivatelink "github.com/openshift/hypershift/control-plane-operator/controllers/awsprivatelink"
	cpomanifests "github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/manifests"
	hccokasvap "github.com/openshift/hypershift/control-plane-operator/hostedclusterconfigoperator/controllers/resources/kas"
	hccomanifests "github.com/openshift/hypershift/control-plane-operator/hostedclusterconfigoperator/controllers/resources/manifests"
	hcmetrics "github.com/openshift/hypershift/hypershift-operator/controllers/hostedcluster/metrics"
	"github.com/openshift/hypershift/hypershift-operator/controllers/manifests"
	"github.com/openshift/hypershift/support/azureutil"
	"github.com/openshift/hypershift/support/certs"
	"github.com/openshift/hypershift/support/conditions"
	suppconfig "github.com/openshift/hypershift/support/config"
	"github.com/openshift/hypershift/support/releaseinfo/registryclient"
	"github.com/openshift/hypershift/support/util"
	hyperutil "github.com/openshift/hypershift/support/util"

	configv1 "github.com/openshift/api/config/v1"
	operatorv1 "github.com/openshift/api/operator/v1"
	routev1 "github.com/openshift/api/route/v1"
	configv1client "github.com/openshift/client-go/config/clientset/versioned"
	routev1client "github.com/openshift/client-go/route/clientset/versioned"
	"github.com/openshift/library-go/test/library/metrics"

	"github.com/aws/aws-sdk-go/aws"
	"github.com/aws/aws-sdk-go/service/ec2"
	"github.com/aws/aws-sdk-go/service/route53"

	admissionregistrationv1 "k8s.io/api/admissionregistration/v1"
	k8sadmissionv1 "k8s.io/api/admissionregistration/v1"
	appsv1 "k8s.io/api/apps/v1"
	authenticationv1 "k8s.io/api/authentication/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	kapierror "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/cli-runtime/pkg/genericclioptions"
	"k8s.io/client-go/kubernetes"
	kubeclient "k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
	"k8s.io/client-go/util/retry"
	"k8s.io/utils/ptr"

	capiv1 "sigs.k8s.io/cluster-api/api/v1beta1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	crclient "sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/go-logr/logr"
	"github.com/go-logr/zapr"
	"github.com/google/go-cmp/cmp"
	prometheusv1 "github.com/prometheus/client_golang/api/prometheus/v1"
	dto "github.com/prometheus/client_model/go"
	"github.com/prometheus/common/expfmt"
	"github.com/prometheus/common/model"
	"go.uber.org/zap/zaptest"
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
	"azure-disk-csi-driver-operator",
	"azure-file-csi-driver-operator",
	"openstack-cinder-csi-driver-operator",
	"manila-csi-driver-operator",
	"karpenter",
	"karpenter-operator",
	"featuregate-generator",
}

type GuestClients struct {
	CfgClient  *configv1client.Clientset
	KubeClient *kubernetes.Clientset
}

// InitGuestClients initializes the Kubernetes and OpenShift config clients for the guest cluster
func InitGuestClients(ctx context.Context, t *testing.T, g Gomega, mgtClient crclient.Client, hostedCluster *hyperv1.HostedCluster) *GuestClients {
	guestKubeConfigSecretData := WaitForGuestKubeConfig(t, ctx, mgtClient, hostedCluster)

	guestConfig, err := clientcmd.RESTConfigFromKubeConfig(guestKubeConfigSecretData)
	g.Expect(err).NotTo(HaveOccurred(), "couldn't load guest kubeconfig")
	guestConfig.QPS = -1
	guestConfig.Burst = -1

	cfgClient, err := configv1client.NewForConfig(guestConfig)
	g.Expect(err).NotTo(HaveOccurred())

	kubeClient, err := kubernetes.NewForConfig(guestConfig)
	g.Expect(err).NotTo(HaveOccurred())

	return &GuestClients{
		CfgClient:  cfgClient,
		KubeClient: kubeClient,
	}
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

func GetCustomKubeconfigClients(t *testing.T, ctx context.Context, client crclient.Client, hostedCluster *hyperv1.HostedCluster, serverAddress string) (*kubernetes.Clientset, crclient.Client) {
	g := NewWithT(t)

	customKubeconfigData := WaitForCustomKubeconfig(t, ctx, client, hostedCluster)
	customConfig, err := clientcmd.RESTConfigFromKubeConfig(customKubeconfigData)
	g.Expect(err).NotTo(HaveOccurred(), "couldn't load KAS custom kubeconfig")
	// we know we're the only real clients for these test servers, so turn off client-side throttling
	customConfig.QPS = -1
	customConfig.Burst = -1
	if len(serverAddress) > 0 {
		customConfig.Host = serverAddress
	}
	GetKubeClientSet, err := kubernetes.NewForConfig(customConfig)
	g.Expect(err).NotTo(HaveOccurred(), "failed to create custom kube client for guest cluster")
	kbCrclient, err := crclient.New(customConfig, crclient.Options{Scheme: scheme})
	g.Expect(err).NotTo(HaveOccurred(), "failed to create custom cr client for guest cluster")

	return GetKubeClientSet, kbCrclient
}

// WaitForCustomKubeconfig waits for a KAS custom kubeconfig to be published for the given HostedCluster.
func WaitForCustomKubeconfig(t *testing.T, ctx context.Context, client crclient.Client, hostedCluster *hyperv1.HostedCluster) []byte {
	var customKubeConfigSecretRef crclient.ObjectKey
	EventuallyObject(t, ctx, fmt.Sprintf("KAS custom kubeconfig to be published for HostedCluster %s/%s", hostedCluster.Namespace, hostedCluster.Name),
		func(ctx context.Context) (*hyperv1.HostedCluster, error) {
			err := client.Get(ctx, crclient.ObjectKeyFromObject(hostedCluster), hostedCluster)
			return hostedCluster, err
		},
		[]Predicate[*hyperv1.HostedCluster]{
			func(cluster *hyperv1.HostedCluster) (done bool, reasons string, err error) {
				customKubeConfigSecretRef = crclient.ObjectKey{
					Namespace: hostedCluster.Namespace,
					Name:      ptr.Deref(hostedCluster.Status.CustomKubeconfig, corev1.LocalObjectReference{}).Name,
				}
				return hostedCluster.Status.CustomKubeconfig != nil, "expected a KAS custom kubeconfig reference in status", nil
			},
		},
	)

	var data []byte
	EventuallyObject(t, ctx, "KAS custom kubeconfig secret to have data",
		func(ctx context.Context) (*corev1.Secret, error) {
			var customKubeConfigSecret corev1.Secret
			err := client.Get(ctx, customKubeConfigSecretRef, &customKubeConfigSecret)
			return &customKubeConfigSecret, err
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
				return hostedCluster.Status.KubeConfig != nil, "expected a kubeconfig reference in status", nil
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

func WaitForGuestRestConfig(t *testing.T, ctx context.Context, client crclient.Client, hostedCluster *hyperv1.HostedCluster) *rest.Config {
	g := NewWithT(t)
	guestKubeConfigSecretData := WaitForGuestKubeConfig(t, ctx, client, hostedCluster)
	guestConfig, err := clientcmd.RESTConfigFromKubeConfig(guestKubeConfigSecretData)
	g.Expect(err).NotTo(HaveOccurred(), "couldn't load guest rest config")
	return guestConfig
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
	if IsLessThan(Version415) {
		// SelfSubjectReview API is only available in 4.15+
		// Use the old method to check if the API server is up
		err = wait.PollUntilContextTimeout(ctx, 35*time.Second, 10*time.Minute, true, func(ctx context.Context) (done bool, err error) {
			_, err = crclient.New(guestConfig, crclient.Options{Scheme: scheme})
			if err != nil {
				t.Logf("attempt to connect failed: %s", err)
				return false, nil
			}
			return true, nil
		})
		if err != nil {
			t.Fatalf("failed to connect to guest cluster: %v", err)
		}
	} else {
		EventuallyObject(t, ctx, "a successful connection to the guest API server",
			func(ctx context.Context) (*authenticationv1.SelfSubjectReview, error) {
				return kubeClient.AuthenticationV1().SelfSubjectReviews().Create(ctx, &authenticationv1.SelfSubjectReview{}, metav1.CreateOptions{})
			}, nil, WithTimeout(10*time.Minute),
		)

	}
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
	t.Logf("kubeconfig host switched from %s to %s", oldHost, newHost)
}

func WaitForGuestKubeconfigHostResolutionUpdate(t *testing.T, ctx context.Context, uri string, endpointAccess hyperv1.AWSEndpointAccessType) {
	g := NewWithT(t)
	visibility := "public"
	if endpointAccess == hyperv1.Private {
		visibility = "private"
	}
	t.Logf("Waiting for guest kubeconfig host to resolve to %s address", visibility)
	err := wait.PollUntilContextTimeout(ctx, 15*time.Second, 30*time.Minute, true, func(ctx context.Context) (done bool, err error) {
		host := strings.TrimPrefix(uri, "https://")
		host = strings.Split(host, ":")[0]
		ips, err := net.LookupIP(host)
		if err != nil {
			t.Logf("failed to resolve guest kubeconfig host: %v", err)
			return false, nil
		}
		ip := ips[0].String()
		if endpointAccess == hyperv1.Private {
			if strings.HasPrefix(ip, "10.") {
				t.Logf("kubeconfig host now resolves to private address")
				return true, nil
			}
		} else {
			if !strings.HasPrefix(ip, "10.") {
				t.Logf("kubeconfig host now resolves to public address")
				return true, nil
			}
		}
		return false, nil
	})
	g.Expect(err).NotTo(HaveOccurred(), "failed to wait for guest kubeconfig host resolution to update")
}

func WaitForNReadyNodes(t *testing.T, ctx context.Context, client crclient.Client, n int32, platform hyperv1.PlatformType) []corev1.Node {
	return WaitForNReadyNodesWithOptions(t, ctx, client, n, platform, "")
}

func WaitForReadyNodesByNodePool(t *testing.T, ctx context.Context, client crclient.Client, np *hyperv1.NodePool, platform hyperv1.PlatformType, opts ...NodePoolPollOption) []corev1.Node {
	return WaitForNReadyNodesWithOptions(t, ctx, client, *np.Spec.Replicas, platform, fmt.Sprintf("for NodePool %s/%s", np.Namespace, np.Name), append(opts, WithClientOptions(crclient.MatchingLabelsSelector{Selector: labels.SelectorFromSet(labels.Set{hyperv1.NodePoolLabel: np.Name})}))...)
}

func WaitForReadyNodesByLabels(t *testing.T, ctx context.Context, client crclient.Client, platform hyperv1.PlatformType, replicas int32, nodeLabels map[string]string) []corev1.Node {
	return WaitForNReadyNodesWithOptions(t, ctx, client, replicas, platform, "", WithClientOptions(crclient.MatchingLabelsSelector{Selector: labels.SelectorFromSet(labels.Set(nodeLabels))}))
}

func WaitForNodePoolConfigUpdateComplete(t *testing.T, ctx context.Context, client crclient.Client, np *hyperv1.NodePool) {
	EventuallyObject(t, ctx, fmt.Sprintf("NodePool %s/%s to start config update", np.Namespace, np.Name),
		func(ctx context.Context) (*hyperv1.NodePool, error) {
			nodePool := &hyperv1.NodePool{}
			err := client.Get(ctx, crclient.ObjectKeyFromObject(np), nodePool)
			return nodePool, err
		},
		[]Predicate[*hyperv1.NodePool]{
			ConditionPredicate[*hyperv1.NodePool](Condition{
				Type:   hyperv1.NodePoolUpdatingConfigConditionType,
				Status: metav1.ConditionTrue,
			}),
		},
		//TODO:https://issues.redhat.com/browse/OCPBUGS-43824
		WithTimeout(5*time.Minute),   // Increased from 1 minute
		WithInterval(15*time.Second), // Increased from 10 seconds to reduce API calls and prevent rate limiting
	)
	EventuallyObject(t, ctx, fmt.Sprintf("NodePool %s/%s to finish config update", np.Namespace, np.Name),
		func(ctx context.Context) (*hyperv1.NodePool, error) {
			nodePool := &hyperv1.NodePool{}
			err := client.Get(ctx, crclient.ObjectKeyFromObject(np), nodePool)
			return nodePool, err
		},
		[]Predicate[*hyperv1.NodePool]{
			ConditionPredicate[*hyperv1.NodePool](Condition{
				Type:   hyperv1.NodePoolUpdatingConfigConditionType,
				Status: metav1.ConditionFalse,
			}),
		},
		WithTimeout(25*time.Minute),
		WithInterval(20*time.Second), // Increased from 15 seconds to reduce API calls and prevent rate limiting
	)
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
		WithInterval(3*time.Second), // Reduce polling frequency from 1s to 3s for node readiness
	)
	return nodes.Items
}

func WaitForImageRollout(t *testing.T, ctx context.Context, client crclient.Client, hostedCluster *hyperv1.HostedCluster) {
	var lastVersionCompletionTime *metav1.Time
	if hostedCluster.Status.Version != nil &&
		len(hostedCluster.Status.Version.History) > 0 {
		lastVersionCompletionTime = hostedCluster.Status.Version.History[0].CompletionTime
	}
	EventuallyObject(t, ctx, fmt.Sprintf("HostedCluster %s/%s to rollout", hostedCluster.Namespace, hostedCluster.Name),
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
				if len(ptr.Deref(hostedCluster.Status.Version, hyperv1.ClusterVersionStatus{}).History) == 0 {
					return false, "HostedCluster has no version history", nil
				}
				if lastVersionCompletionTime != nil &&
					hostedCluster.Status.Version.History[0].CompletionTime != nil &&
					lastVersionCompletionTime.Equal(hostedCluster.Status.Version.History[0].CompletionTime) {
					return false, "HostedCluster version history has not been updated yet", nil
				}
				if wanted, got := hostedCluster.Status.Version.Desired.Image, hostedCluster.Status.Version.History[0].Image; wanted != got {
					return false, fmt.Sprintf("desired image %s doesn't match most recent image in history %s", wanted, got), nil
				}
				if wanted, got := configv1.CompletedUpdate, hostedCluster.Status.Version.History[0].State; wanted != got {
					return false, fmt.Sprintf("wanted most recent version history to have state %s, has state %s", wanted, got), nil
				}
				return true, "cluster rolled out", nil
			},
		},
		WithTimeout(30*time.Minute),
	)
}

func WaitForControlPlaneComponentRollout(t *testing.T, ctx context.Context, client crclient.Client, hostedCluster *hyperv1.HostedCluster, initialVersion string) {
	controlPlaneComponents := &hyperv1.ControlPlaneComponentList{}
	controlPlaneNamespace := manifests.HostedControlPlaneNamespace(hostedCluster.Namespace, hostedCluster.Name)
	EventuallyObjects(t, ctx, "control plane components to complete rollout",
		func(ctx context.Context) ([]*hyperv1.ControlPlaneComponent, error) {
			err := client.List(ctx, controlPlaneComponents, crclient.InNamespace(controlPlaneNamespace))
			items := make([]*hyperv1.ControlPlaneComponent, len(controlPlaneComponents.Items))
			for i := range controlPlaneComponents.Items {
				items[i] = &controlPlaneComponents.Items[i]
			}
			return items, err
		},
		[]Predicate[[]*hyperv1.ControlPlaneComponent]{
			func(cpComponents []*hyperv1.ControlPlaneComponent) (done bool, reasons string, err error) {
				return len(cpComponents) > 10, "expecting more than 10 control plane components", nil
			},
		},
		[]Predicate[*hyperv1.ControlPlaneComponent]{
			ConditionPredicate[*hyperv1.ControlPlaneComponent](Condition{
				Type:   string(hyperv1.ControlPlaneComponentRolloutComplete),
				Status: metav1.ConditionTrue,
			}),
			func(cpComponent *hyperv1.ControlPlaneComponent) (done bool, reasons string, err error) {
				if initialVersion != "" && cpComponent.Status.Version == initialVersion {
					return false, fmt.Sprintf("component %s is still on version %s", cpComponent.Name, cpComponent.Status.Version), nil
				}
				return true, fmt.Sprintf("component %s has version: %s", cpComponent.Name, cpComponent.Status.Version), nil
			},
		},
		WithTimeout(30*time.Minute),
		WithInterval(10*time.Second),
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
		g := NewWithT(t)

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

		guestKubeConfigSecretData := WaitForGuestKubeConfig(t, ctx, client, hostedCluster)
		guestConfig, err := clientcmd.RESTConfigFromKubeConfig(guestKubeConfigSecretData)
		g.Expect(err).NotTo(HaveOccurred(), "couldn't load guest kubeconfig")
		guestClient := kubeclient.NewForConfigOrDie(guestConfig)

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

			// Temporary workaround for https://issues.redhat.com/browse/OCPBUGS-45182
			if strings.HasPrefix(pod.Name, "openstack-manila-csi-controllerplugin-") {
				continue
			}

			// Temporary workaround for https://issues.redhat.com/browse/CNV-40820
			if strings.HasPrefix(pod.Name, "kubevirt-csi") {
				continue
			}
			for _, containerStatus := range pod.Status.ContainerStatuses {
				if containerStatus.RestartCount > crashToleration {
					// For kube-controller-manager, check if restart was triggered by certificate rotation
					if strings.HasPrefix(pod.Name, "kube-controller-manager-") {
						if isCertificateTriggeredRestart(ctx, client, &pod) {
							t.Logf("kube-controller-manager restart in pod %s was triggered by certificate rotation (expected behavior)", pod.Name)
							continue
						}
					}

					if isLeaderElectionFailure(ctx, guestClient, &pod, containerStatus.Name) {
						t.Logf("Leader election failure detected in container %s in pod %s", containerStatus.Name, pod.Name)
						continue
					}
					t.Errorf("Container %s in pod %s has a restartCount > 0 (%d)", containerStatus.Name, pod.Name, containerStatus.RestartCount)
				}
			}
		}
	})
}

func isLeaderElectionFailure(ctx context.Context, guestClient *kubeclient.Clientset, pod *corev1.Pod, containerName string) bool {
	podLogOpts := corev1.PodLogOptions{
		Container: containerName,
		Previous:  true,
		TailLines: ptr.To[int64](10),
	}
	req := guestClient.CoreV1().Pods(pod.Namespace).GetLogs(pod.Name, &podLogOpts)
	podLogs, err := req.Stream(ctx)
	if err != nil {
		return false
	}
	defer podLogs.Close()

	buf := new(bytes.Buffer)
	_, err = buf.ReadFrom(podLogs)
	if err != nil {
		return false
	}

	return strings.Contains(buf.String(), "election lost")
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

func EnsureNoRapidDeploymentRollouts(t *testing.T, ctx context.Context, client crclient.Client, hostedCluster *hyperv1.HostedCluster) {
	const maxAllowedGeneration = 10
	t.Run("EnsureNoRapidDeploymentRollouts", func(t *testing.T) {
		namespace := manifests.HostedControlPlaneNamespace(hostedCluster.Namespace, hostedCluster.Name)

		var deploymentList appsv1.DeploymentList
		if err := client.List(ctx, &deploymentList, crclient.InNamespace(namespace)); err != nil {
			t.Fatalf("failed to list deployments in namespace %s: %v", namespace, err)
		}
		for _, deployment := range deploymentList.Items {
			if deployment.Generation > maxAllowedGeneration {
				t.Errorf("Rapidly updating deployment detected! Deployment %s exceeds the max allowed generation of %d", deployment.Name, maxAllowedGeneration)
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

		// Retry logic to handle timing issues with certificate initialization
		g.Eventually(func() error {
			_, err := RunCommandInPod(ctx, mgmtClient, "openshift-apiserver", hcpNs, command, "openshift-apiserver", 1*time.Minute)
			return err
		}, 5*time.Minute, 30*time.Second).Should(Succeed(), "ca-bundle.crt file should be available in openshift-apiserver pod")
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

func EnsureAllContainersHaveTerminationMessagePolicyFallbackToLogsOnError(t *testing.T, ctx context.Context, client crclient.Client, hostedCluster *hyperv1.HostedCluster) {
	t.Run("EnsureAllContainersHaveTerminationMessagePolicyFallbackToLogsOnError", func(t *testing.T) {
		AtLeast(t, Version419)
		var podList corev1.PodList
		if err := client.List(ctx, &podList, &crclient.ListOptions{Namespace: manifests.HostedControlPlaneNamespace(hostedCluster.Namespace, hostedCluster.Name)}); err != nil {
			t.Fatalf("failed to list pods in cluster: %v", err)
		}
		// CNO is not setting terminationMessagePolicy for its pods
		// https://issues.redhat.com/browse/OCPBUGS-56051
		excludedPods := []string{
			"cloud-network-config-controller",
			"network-node-identity",
			"ovnkube-control-plane",
		}
		for _, pod := range podList.Items {
			skip := false
			for _, excludedPod := range excludedPods {
				if strings.HasPrefix(pod.Name, excludedPod) {
					skip = true
					break
				}
			}
			if skip {
				continue
			}

			// Skip KubeVirt related pods
			if pod.Labels["kubevirt.io"] == "virt-launcher" || pod.Labels["app"] == "vmi-console-debug" {
				continue
			}

			for _, initContainer := range pod.Spec.InitContainers {
				if initContainer.TerminationMessagePolicy != corev1.TerminationMessageFallbackToLogsOnError {
					t.Errorf("ns/%s pod/%s initContainer/%s has doesn't have terminationMessagePolicy %s but %s", pod.Namespace, pod.Name, initContainer.Name, corev1.TerminationMessageFallbackToLogsOnError, initContainer.TerminationMessagePolicy)
				}
			}
			for _, container := range pod.Spec.Containers {
				if container.TerminationMessagePolicy != corev1.TerminationMessageFallbackToLogsOnError {
					t.Errorf("ns/%s pod/%s container/%s has doesn't have terminationMessagePolicy %s but %s", pod.Namespace, pod.Name, container.Name, corev1.TerminationMessageFallbackToLogsOnError, container.TerminationMessagePolicy)
				}
			}
		}
	})
}

// NOTE: This function assumes that it is not called in the middle of a version rollout
// i.e. It expects that the first entry in ClusterVersion history is Completed
func EnsureFeatureGateStatus(t *testing.T, ctx context.Context, guestClient crclient.Client) {
	t.Run("EnsureFeatureGateStatus", func(t *testing.T) {
		AtLeast(t, Version419)

		g := NewWithT(t)

		clusterVersion := &configv1.ClusterVersion{}
		err := guestClient.Get(ctx, crclient.ObjectKey{Name: "version"}, clusterVersion)
		g.Expect(err).NotTo(HaveOccurred(), "failed to get ClusterVersion resource")

		featureGate := &configv1.FeatureGate{}
		err = guestClient.Get(ctx, crclient.ObjectKey{Name: "cluster"}, featureGate)
		g.Expect(err).NotTo(HaveOccurred(), "failed to get FeatureGate resource")

		// Expect at least one entry in ClusterVersion history
		g.Expect(len(clusterVersion.Status.History)).To(BeNumerically(">", 0), "ClusterVersion history is empty")
		currentVersion := clusterVersion.Status.History[0].Version

		// Expect current version to be in Completed state
		g.Expect(clusterVersion.Status.History[0].State).To(Equal(configv1.CompletedUpdate), "most recent ClusterVersion history entry is not in Completed state")

		// Ensure that the current version in ClusterVersion is also present in FeatureGate status
		versionFound := false
		for _, details := range featureGate.Status.FeatureGates {
			if details.Version == currentVersion {
				versionFound = true
				break
			}
		}
		g.Expect(versionFound).To(BeTrue(), "current version %s from ClusterVersion not found in FeatureGate status", currentVersion)
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
		AtLeast(t, Version421)

		// Check if OpenShiftPodSecurityAdmission feature gate is enabled
		featureGate := &configv1.FeatureGate{}
		err := guestClient.Get(ctx, crclient.ObjectKey{Name: "cluster"}, featureGate)
		if err != nil {
			t.Logf("failed to get FeatureGate resource: %v", err)
			return
		}

		// Find the current version and check if OpenShiftPodSecurityAdmission is enabled
		var psaEnabled bool
		for _, details := range featureGate.Status.FeatureGates {
			for _, enabled := range details.Enabled {
				if enabled.Name == "OpenShiftPodSecurityAdmission" {
					psaEnabled = true
					break
				}
			}
			if psaEnabled {
				break
			}
		}

		if !psaEnabled {
			t.Skip("OpenShiftPodSecurityAdmission feature gate is not enabled, skipping PSA test")
		}
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
		err = guestClient.Create(ctx, pod)
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
			stdOut, err := RunCommandInPod(ctx, c, "cluster-version-operator", hcpNamespace, command, "cluster-version-operator", 0)
			g.Expect(err).To(HaveOccurred(), fmt.Sprintf("cluster-version-operator pod was unexpectedly allowed to reach the management KAS. stdOut: %s.", stdOut))

			// private-router policy only applied on AWS and Azure.
			if hostedCluster.Spec.Platform.Type == hyperv1.AWSPlatform ||
				hostedCluster.Spec.Platform.Type == hyperv1.AzurePlatform {
				// Validate private router is not allowed to access management KAS.
				stdOut, err = RunCommandInPod(ctx, c, "private-router", hcpNamespace, command, "private-router", 0)
				g.Expect(err).To(HaveOccurred(), fmt.Sprintf("private-router pod was unexpectedly allowed to reach the management KAS. stdOut: %s.", stdOut))
			}

			// Validate cluster api is allowed to access management KAS.
			stdOut, err = RunCommandInPod(ctx, c, "cluster-api", hcpNamespace, command, "manager", 0)
			// Expect curl return a 403 from the KAS.
			if !strings.Contains(stdOut, "HTTP/2 403") || err != nil {
				t.Errorf("cluster api pod was unexpectedly not allowed to reach the management KAS. stdOut: %s. stdErr: %s", stdOut, err.Error())
			}
		})
	})
}

// EnsureNodesRuntime ensures that all nodes in the NodePool have the expected runtime handlers.
// This is only supported on 4.18+ when the default runtime is changed to crun.
func EnsureNodesRuntime(t *testing.T, nodes []corev1.Node) {
	AtLeast(t, Version418)
	g := NewWithT(t)

	validHandlers := map[string]bool{
		"runc": false,
		"crun": false,
	}

	for _, node := range nodes {
		g.Expect(node.Status.RuntimeHandlers).NotTo(BeNil(), "node %s is missing runtime handlers", node.Name)
		for _, handler := range node.Status.RuntimeHandlers {
			if _, ok := validHandlers[handler.Name]; ok {
				validHandlers[handler.Name] = true
			}
		}

		for handler, present := range validHandlers {
			g.Expect(present).To(BeTrue(), "node %s is missing runtime handler %s", node.Name, handler)
		}
	}
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

	if pod.Labels["job-name"] == "featuregate-generator" {
		return "featuregate-generator"
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

	// Get the component name for each labeled pod and ensure it exists in the components slice
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

		err = UpdateObject(t, ctx, hostClient, hostedCluster, func(obj *hyperv1.HostedCluster) {
			if obj.Spec.ControllerAvailabilityPolicy == hyperv1.HighlyAvailable {
				obj.Spec.ControllerAvailabilityPolicy = hyperv1.SingleReplica
			}
			if obj.Spec.ControllerAvailabilityPolicy == hyperv1.SingleReplica {
				obj.Spec.ControllerAvailabilityPolicy = hyperv1.HighlyAvailable
			}
		})
		g.Expect(err).To(HaveOccurred())
		g.Expect(err.Error()).To(ContainSubstring("ControllerAvailabilityPolicy is immutable"))
	})

	t.Run("EnsureHostedClusterCapabilitiesImmutability", func(t *testing.T) {
		AtLeast(t, Version419)
		g := NewWithT(t)

		err := UpdateObject(t, ctx, hostClient, hostedCluster, func(obj *hyperv1.HostedCluster) {
			obj.Spec.Capabilities = &hyperv1.Capabilities{
				Disabled: []hyperv1.OptionalCapability{hyperv1.ImageRegistryCapability},
			}
		})
		g.Expect(err).To(HaveOccurred())
		g.Expect(err.Error()).To(ContainSubstring("Capabilities is immutable"))
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
		AtLeast(t, Version417)
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

// GetMetricsFromPod exec curl command in a pod metrics endpoint and return metric values if any
// Requires curl to be installed in the container
func GetMetricsFromPod(ctx context.Context, c crclient.Client, componentName, containerName, namespaceName, port string) (map[string]*dto.MetricFamily, error) {
	command := []string{"curl", "-s", fmt.Sprintf("http://127.0.0.1:%s/metrics", port)}
	cmdOutput, err := RunCommandInPod(ctx, c, componentName, namespaceName, command, containerName, 5*time.Minute)
	if err != nil {
		return nil, fmt.Errorf("couldn't obtain any metrics: %v", err)
	}
	if len(cmdOutput) == 0 {
		return nil, fmt.Errorf("no metrics found")
	}

	var parser expfmt.TextParser
	return parser.TextToMetricFamilies(strings.NewReader(cmdOutput))
}

func EnsurePodsWithEmptyDirPVsHaveSafeToEvictAnnotations(t *testing.T, ctx context.Context, hostClient crclient.Client, hcpNs string) {
	AtLeast(t, Version420)

	t.Run("EnsurePodsWithEmptyDirPVsHaveSafeToEvictAnnotations", func(t *testing.T) {

		g := NewWithT(t)

		auditedAppList := map[string]string{
			"etcd":                                   "app",
			"cloud-controller-manager":               "app",
			"cloud-credential-operator":              "app",
			"aws-ebs-csi-driver-controller":          "app",
			"capi-provider-controller-manager":       "app",
			"cloud-network-config-controller":        "app",
			"cluster-network-operator":               "app",
			"cluster-version-operator":               "app",
			"control-plane-operator":                 "app",
			"ignition-server":                        "app",
			"ingress-operator":                       "app",
			"kube-apiserver":                         "app",
			"kube-controller-manager":                "app",
			"kube-scheduler":                         "app",
			"multus-admission-controller":            "app",
			"oauth-openshift":                        "app",
			"openshift-apiserver":                    "app",
			"openshift-oauth-apiserver":              "app",
			"packageserver":                          "app",
			"ovnkube-master":                         "app",
			"kubevirt-csi-driver":                    "app",
			"cluster-image-registry-operator":        "name",
			"virt-launcher":                          "kubevirt.io",
			"azure-disk-csi-driver-controller":       "app",
			"azure-file-csi-driver-controller":       "app",
			"certified-operators-catalog":            "app",
			"community-operators-catalog":            "app",
			"redhat-operators-catalog":               "app",
			"redhat-marketplace-catalog":             "app",
			"openstack-cinder-csi-driver-controller": "app",
			"openstack-manila-csi":                   "app",
			"karpenter":                              "app",
			"karpenter-operator":                     "app",
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
			// skip etcd and feature-gate-generator pods
			// If added to the list of audited pods,it will fail the e2e check on older release branches since e2e is ran from main.
			if componentName := pod.Labels["hypershift.openshift.io/control-plane-component"]; componentName == "etcd" || componentName == "featuregate-generator" {
				continue
			}

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
				// if the Key/Value are empty we assume that the pod is not in the auditedList,
				// if that's the case the annotation should not exists in that pod (except for tmp-dir which is in every pod by default).
				// Then continue to the next pod
				hasTmpDirAnnotation := false
				safe2EvictVolumes := strings.Split(pod.Annotations[suppconfig.PodSafeToEvictLocalVolumesKey], ",")
				safe2EvictVolumes = slices.DeleteFunc(safe2EvictVolumes, func(s string) bool {
					hasTmpDir := s == util.PodTmpDirMountName
					hasTmpDirAnnotation = hasTmpDirAnnotation || hasTmpDir
					return s == "" || hasTmpDir
				})
				g.Expect(safe2EvictVolumes).To(BeEmpty(), "the pod  %s is not in the audited list for safe-eviction and should not contain the safe-to-evict-local-volume annotation", pod.Name)
				// if we have a tmpdir annotation, we need to ensure that the volume is defined correctly; done below
				if !hasTmpDirAnnotation {
					continue
				}
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

type labelSelector struct {
	label string
	value string
}

// auditedContainersHas checks the given map to see if the container name exists in it; if the map is empty, always return true
func auditedContainersHas(container corev1.Container, auditedContainers map[string]struct{}) bool {
	if auditedContainers == nil {
		return false
	}

	if len(auditedContainers) == 0 {
		return true
	}

	_, has := auditedContainers[container.Name]
	return has
}

func EnsureReadOnlyRootFilesystem(t *testing.T, ctx context.Context, hostClient crclient.Client, hcpNs string) {
	AtLeast(t, Version420)

	// By default, we enable readOnlyRootFilesystem in every container.
	// This testchecks to make sure that every container has this field enabled, unless manually specified in auditedAppContainersNoRORFS
	t.Run("EnsureReadOnlyRootFilesystem", func(t *testing.T) {
		g := NewWithT(t)

		hcpPods := &corev1.PodList{}
		if err := hostClient.List(ctx, hcpPods, &client.ListOptions{
			Namespace: hcpNs,
		}); err != nil {
			t.Fatalf("cannot list hostedControlPlane pods: %v", err)
		}

		// a list of applications that are allowed to have Pod.Spec.Containers[*].SecurityContext.ReadOnlyRootFilesystem == false
		// auditedAppContainersNoRORFS[labelSelector{label: "app", value: "value"}][pod.Spec.Containers[*]] indicates that particular container is allowed to be false.
		// if a labelSelector is given with an empty map, allow all containers to be false
		auditedAppContainersNoRORFS := map[labelSelector]map[string]struct{}{
			{label: "app", value: "azure-disk-csi-driver-controller"}:       {},
			{label: "app", value: "azure-disk-csi-driver-operator"}:         {},
			{label: "app", value: "azure-file-csi-driver-controller"}:       {},
			{label: "app", value: "azure-file-csi-driver-operator"}:         {},
			{label: "app", value: "aws-ebs-csi-driver-controller"}:          {},
			{label: "app", value: "aws-ebs-csi-driver-operator"}:            {},
			{label: "app", value: "openstack-cinder-csi-driver-operator"}:   {},
			{label: "app", value: "openstack-cinder-csi-driver-controller"}: {},
			{label: "app", value: "manila-csi-driver-operator"}:             {},
			{label: "app", value: "openstack-manila-csi"}:                   {},
			{label: "app", value: "multus-admission-controller"}:            {},
			{label: "app", value: "network-node-identity"}:                  {},
			{label: "app", value: "ovnkube-control-plane"}:                  {},
			{label: "app", value: "cloud-network-config-controller"}:        {},
			{label: "app", value: "vmi-console-debug"}:                      {},
			{label: "kubevirt.io", value: "virt-launcher"}:                  {}, // virt-launcher pods have no app label
		}

		for _, pod := range hcpPods.Items {
			// skip etcd and feature-gate-generator pods
			// If added to the list of audited pods,it will fail the e2e check on older release branches since e2e is ran from main.
			if componentName := pod.Labels["hypershift.openshift.io/control-plane-component"]; componentName == "etcd" || componentName == "featuregate-generator" {
				continue
			}

			var auditedContainers map[string]struct{}

			for selector, containers := range auditedAppContainersNoRORFS {
				if v, has := pod.Labels[selector.label]; has && v == selector.value {
					auditedContainers = containers
					break
				}
			}

			for _, c := range pod.Spec.Containers {
				isAuditedOff := auditedContainersHas(c, auditedContainers)
				isRORFS := c.SecurityContext != nil && c.SecurityContext.ReadOnlyRootFilesystem != nil && *c.SecurityContext.ReadOnlyRootFilesystem

				// valid cases are isAuditedOff && !isRORFS and !isAuditedOff && isRORFS
				g.Expect(isRORFS).ToNot(BeIdenticalTo(isAuditedOff), "container %s in pod %s expects readOnlyRootFilesystem to be %v, it was %v", c.Name, pod.Name, !isAuditedOff, isRORFS)
			}
		}
	})

	// By default, we add an emptyDir pod volume and mount it into every container in the pod at /tmp.
	// This test checks to make sure that every container has this mount, unless manually specified in auditedAppContainerNoTmpDir.
	t.Run("EnsureReadOnlyRootFilesystemTmpDirMount", func(t *testing.T) {
		g := NewWithT(t)

		hcpPods := &corev1.PodList{}
		if err := hostClient.List(ctx, hcpPods, &client.ListOptions{
			Namespace: hcpNs,
		}); err != nil {
			t.Fatalf("cannot list hostedControlPlane pods: %v", err)
		}

		// a list of applications that are allowed to not have the emptyDir "tmp-dir" mounted.
		// auditedAppContainerNoTmpDir[labelSelector{label: "app", value: "value"}][pod.Spec.Containers[*]] indicates that particular container is allowed to not have the mount
		// if a labelSelector is given with an empty map, allow all containers to not have it
		auditedAppContainerNoTmpDir := map[labelSelector]map[string]struct{}{
			{label: "app", value: "azure-disk-csi-driver-controller"}:       {},
			{label: "app", value: "azure-disk-csi-driver-operator"}:         {},
			{label: "app", value: "azure-file-csi-driver-controller"}:       {},
			{label: "app", value: "azure-file-csi-driver-operator"}:         {},
			{label: "app", value: "aws-ebs-csi-driver-controller"}:          {},
			{label: "app", value: "aws-ebs-csi-driver-operator"}:            {},
			{label: "app", value: "openstack-cinder-csi-driver-controller"}: {},
			{label: "app", value: "openstack-manila-csi"}:                   {},
			{label: "app", value: "multus-admission-controller"}:            {},
			{label: "app", value: "network-node-identity"}:                  {},
			{label: "app", value: "ovnkube-control-plane"}:                  {},
			{label: "app", value: "cloud-network-config-controller"}:        {},
			{label: "app", value: "vmi-console-debug"}:                      {},
			{label: "kubevirt.io", value: "virt-launcher"}:                  {}, // virt-launcher pods have no app label
			{label: "app", value: "csi-snapshot-controller"}:                {},
			{label: "app", value: "csi-snapshot-webhook"}:                   {},
			{label: "app", value: "packageserver"}: {
				"packageserver": {}, // the package server was able to enabled readOnlyRootFilesystem without needing to mount /tmp
			},
		}

		for _, pod := range hcpPods.Items {
			// skip etcd and feature-gate-generator pods
			// If added to the list of audited pods,it will fail the e2e check on older release branches since e2e is ran from main.
			if componentName := pod.Labels["hypershift.openshift.io/control-plane-component"]; componentName == "etcd" || componentName == "featuregate-generator" {
				continue
			}

			var auditedContainers map[string]struct{}

			for selector, containers := range auditedAppContainerNoTmpDir {
				if v, has := pod.Labels[selector.label]; has && v == selector.value {
					auditedContainers = containers
					break
				}
			}

			for _, c := range pod.Spec.Containers {
				if auditedContainersHas(c, auditedContainers) {
					continue
				}
				containerHasTmpDir := slices.ContainsFunc(c.VolumeMounts, func(v corev1.VolumeMount) bool {
					return v.MountPath == util.PodTmpDirMountPath
				})
				g.Expect(containerHasTmpDir).To(BeTrue(), "container %s in pod %s does not have /tmp mounted, and it is expected to mount it", c.Name, pod.Name)
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
				URL: ptr.To("https://etcd-client:2379"),
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

func EnsureGlobalPullSecret(t *testing.T, ctx context.Context, mgmtClient crclient.Client, entryHostedCluster *hyperv1.HostedCluster) error {
	t.Run("EnsureGlobalPullSecret", func(t *testing.T) {
		AtLeast(t, Version419)
		// TODO (jparrill): Change check of release version `releaseVersion.GT(Version420)` to `releaseVersion.GE(Version420)`
		// during the backport to 4.20 of this PR https://github.com/openshift/hypershift/pull/6736
		if entryHostedCluster.Spec.Platform.Type != hyperv1.AzurePlatform && entryHostedCluster.Spec.Platform.Type != hyperv1.AWSPlatform {
			t.Skip("test only supported on platform ARO or AWS")
		}

		if entryHostedCluster.Spec.Platform.Type == hyperv1.AWSPlatform && releaseVersion.LE(Version420) {
			t.Skip("AWS platform not supported on version 4.20 or less")
		}

		var (
			dummyImageTagMultiarch = "quay.io/hypershift/sleep:multiarch"
			dummyImageTag12        = "quay.io/hypershift/sleep:1.2.0"
			err                    error

			// Additional Pull Secret
			additionalPullSecretName            = "additional-pull-secret"
			additionalPullSecretNamespace       = "kube-system"
			pullSecretNamespace                 = "openshift-config"
			additionalPullSecretDummyData       = []byte(`{"auths": {"quay.io": {"auth": "YWRtaW46cGFzc3dvcmQ="}}}`)
			additionalPullSecretReadOnlyE2EData = []byte(`{"auths": {"quay.io": {"auth": "aHlwZXJzaGlmdCtlMmVfcmVhZG9ubHk6R1U2V0ZDTzVaVkJHVDJPREE1VVAxT0lCOVlNMFg2TlY0UkZCT1lJSjE3TDBWOFpTVlFGVE5BS0daNTNNQVAzRA=="}}}`)
			oldglobalPullSecretData             []byte
			dsImage                             string
			g                                   = NewWithT(t)
		)

		guestClient := WaitForGuestClient(t, ctx, mgmtClient, entryHostedCluster)

		// Create the additional-pull-secret secret in the DataPlane using the dummy pull secret.
		// The dummy pull secret is authorized to pull restricted images.
		err = createAdditionalPullSecret(ctx, guestClient, additionalPullSecretDummyData, additionalPullSecretName, additionalPullSecretNamespace)
		g.Expect(err).NotTo(HaveOccurred(), "failed to create additional-pull-secret secret")

		// Check if HCCO generates the GlobalPullSecret secret in the kube-system namespace in the DataPlane
		t.Run("Check if GlobalPullSecret secret is in the right place at Dataplane", func(t *testing.T) {
			globalPullSecret := hccomanifests.GlobalPullSecret()
			g.Eventually(func() error {
				if err := guestClient.Get(ctx, client.ObjectKey{Name: globalPullSecret.Name, Namespace: globalPullSecret.Namespace}, globalPullSecret); err != nil {
					return err
				}
				g.Expect(globalPullSecret.Data).NotTo(BeEmpty(), "global-pull-secret secret is empty")
				g.Expect(globalPullSecret.Data[corev1.DockerConfigJsonKey]).NotTo(BeEmpty(), "global-pull-secret secret is empty")
				oldglobalPullSecretData = globalPullSecret.Data[corev1.DockerConfigJsonKey]
				return nil
			}, 30*time.Second, 5*time.Second).Should(Succeed(), "global-pull-secret secret is not present")
		})

		// Check if the additional RBAC is present in the DataPlane
		t.Run("Check if the additional RBAC is present in the DataPlane", func(t *testing.T) {
			g.Eventually(func() error {
				// Check RBAC in kube-system and openshift-config namespace
				role := hccomanifests.GlobalPullSecretSyncerRole(additionalPullSecretNamespace)
				if err := guestClient.Get(ctx, client.ObjectKey{Name: role.Name, Namespace: role.Namespace}, role); err != nil {
					return err
				}

				roleBinding := hccomanifests.GlobalPullSecretSyncerRoleBinding(additionalPullSecretNamespace)
				if err := guestClient.Get(ctx, client.ObjectKey{Name: roleBinding.Name, Namespace: roleBinding.Namespace}, roleBinding); err != nil {
					return err
				}

				openshiftConfigRole := hccomanifests.GlobalPullSecretSyncerRole(pullSecretNamespace)
				if err := guestClient.Get(ctx, client.ObjectKey{Name: openshiftConfigRole.Name, Namespace: openshiftConfigRole.Namespace}, openshiftConfigRole); err != nil {
					return err
				}

				openshiftConfigRoleBinding := hccomanifests.GlobalPullSecretSyncerRoleBinding(pullSecretNamespace)
				if err := guestClient.Get(ctx, client.ObjectKey{Name: openshiftConfigRoleBinding.Name, Namespace: openshiftConfigRoleBinding.Namespace}, openshiftConfigRoleBinding); err != nil {
					return err
				}

				serviceAccount := hccomanifests.GlobalPullSecretSyncerServiceAccount()
				if err := guestClient.Get(ctx, client.ObjectKey{Name: serviceAccount.Name, Namespace: serviceAccount.Namespace}, serviceAccount); err != nil {
					return err
				}

				return nil
			}, 30*time.Second, 5*time.Second).Should(Succeed(), "RBAC is not present")
		})

		// Check if the DaemonSet is present in the DataPlane
		t.Run("Check if the DaemonSet is present in the DataPlane", func(t *testing.T) {
			g.Eventually(func() error {
				daemonSet := hccomanifests.GlobalPullSecretDaemonSet()
				if err := guestClient.Get(ctx, client.ObjectKey{Name: daemonSet.Name, Namespace: daemonSet.Namespace}, daemonSet); err != nil {
					return err
				}
				dsImage = daemonSet.Spec.Template.Spec.Containers[0].Image
				return nil
			}, 30*time.Second, 5*time.Second).Should(Succeed(), "DaemonSet is not present")
		})

		// Check if we can pull restricted images
		t.Run("Check if we can pull restricted images, should fail", func(t *testing.T) {
			g.Eventually(func() error {
				globalPullSecret := hccomanifests.GlobalPullSecret()
				if err := guestClient.Get(ctx, client.ObjectKey{Name: globalPullSecret.Name, Namespace: globalPullSecret.Namespace}, globalPullSecret); err != nil {
					return err
				}
				pullSecretData := globalPullSecret.Data[corev1.DockerConfigJsonKey]
				_, _, _, err := registryclient.GetMetadata(ctx, dummyImageTagMultiarch, pullSecretData)
				if err == nil {
					return fmt.Errorf("succeeded to get metadata for restricted image, should fail")
				}
				return nil
			}, 1*time.Minute, 5*time.Second).Should(Succeed(), "should not be able to get repo setup")
		})

		// Create a pod which uses the restricted image, should fail
		t.Run("Create a pod which uses the restricted image, should fail", func(t *testing.T) {
			shouldFail := true
			runAndCheckPod(t, ctx, guestClient, dummyImageTagMultiarch, additionalPullSecretNamespace, "global-pull-secret-fail", shouldFail)
		})

		// Modify the additional-pull-secret secret in the DataPlane
		t.Run("Modify the additional-pull-secret secret in the DataPlane by adding the valid pull secret", func(t *testing.T) {
			additionalPullSecret := hccomanifests.AdditionalPullSecret()
			err := guestClient.Get(ctx, client.ObjectKey{Name: additionalPullSecret.Name, Namespace: additionalPullSecret.Namespace}, additionalPullSecret)
			g.Expect(err).NotTo(HaveOccurred(), "failed to get additional-pull-secret secret")
			additionalPullSecret.Data[corev1.DockerConfigJsonKey] = additionalPullSecretReadOnlyE2EData
			err = guestClient.Update(ctx, additionalPullSecret)
			g.Expect(err).NotTo(HaveOccurred(), "failed to update additional-pull-secret secret")
		})

		// Check if GlobalPullSecret secret is updated in the DataPlane
		t.Run("Check if GlobalPullSecret secret is updated in the DataPlane", func(t *testing.T) {
			globalPullSecret := hccomanifests.GlobalPullSecret()
			g.Eventually(func() error {
				if err := guestClient.Get(ctx, client.ObjectKey{Name: globalPullSecret.Name, Namespace: globalPullSecret.Namespace}, globalPullSecret); err != nil {
					return err
				}
				g.Expect(globalPullSecret.Data[corev1.DockerConfigJsonKey]).NotTo(BeEmpty(), "global-pull-secret secret is empty")
				if bytes.Equal(globalPullSecret.Data[corev1.DockerConfigJsonKey], oldglobalPullSecretData) {
					return fmt.Errorf("global-pull-secret secret is equal to the old global-pull-secret secret, should be different")
				}
				return nil
			}, 30*time.Second, 5*time.Second).Should(Succeed(), "global-pull-secret secret is not updated")
		})

		// Check if we can pull other restricted images, should succeed
		t.Run("Check if we can pull other restricted images, should succeed", func(t *testing.T) {
			g.Eventually(func() error {
				globalPullSecret := hccomanifests.GlobalPullSecret()
				if err := guestClient.Get(ctx, client.ObjectKey{Name: globalPullSecret.Name, Namespace: globalPullSecret.Namespace}, globalPullSecret); err != nil {
					return err
				}
				pullSecretData := globalPullSecret.Data[corev1.DockerConfigJsonKey]
				_, _, _, err := registryclient.GetMetadata(ctx, dummyImageTag12, pullSecretData)
				if err != nil {
					return fmt.Errorf("failed to get metadata for restricted image: %v", err)
				}
				return nil
			}, 1*time.Minute, 5*time.Second).Should(Succeed(), "should be able to pull other restricted images")
		})

		// Check if we can run a pod with the restricted image
		t.Run("Create a pod which uses the restricted image, should succeed", func(t *testing.T) {
			shouldFail := false
			runAndCheckPod(t, ctx, guestClient, dummyImageTag12, additionalPullSecretNamespace, "global-pull-secret-success", shouldFail)
		})

		// Delete the additional-pull-secret secret in the DataPlane
		t.Log("Deleting the additional-pull-secret secret in the DataPlane")
		err = guestClient.Delete(ctx, &corev1.Secret{ObjectMeta: metav1.ObjectMeta{Name: additionalPullSecretName, Namespace: additionalPullSecretNamespace}})
		g.Expect(err).NotTo(HaveOccurred(), "failed to delete additional-pull-secret secret")

		// Check if the GlobalPullSecret secret is deleted in the DataPlane
		t.Run("Check if the GlobalPullSecret secret is deleted in the DataPlane", func(t *testing.T) {
			g.Eventually(func() error {
				globalPullSecret := hccomanifests.GlobalPullSecret()
				if err := guestClient.Get(ctx, client.ObjectKey{Name: globalPullSecret.Name, Namespace: globalPullSecret.Namespace}, globalPullSecret); err != nil {
					if !apierrors.IsNotFound(err) {
						return err
					}
					return nil
				}
				return fmt.Errorf("global-pull-secret secret is still present")
			}, 30*time.Second, 5*time.Second).Should(Succeed(), "global-pull-secret secret is still present")
		})

		// Wait for all nodes to stabilize after global-pull-secret deletion
		t.Run("Wait for pull secret synchronization to stabilize across all nodes", func(t *testing.T) {
			t.Log("Waiting for GlobalPullSecretDaemonSet to process the deletion and stabilize all nodes")

			// Wait for the GlobalPullSecretDaemonSet to be ready and stable after processing the deletion
			EventuallyObject(t, ctx, "GlobalPullSecretDaemonSet to be ready after global-pull-secret deletion", func(ctx context.Context) (*appsv1.DaemonSet, error) {
				ds := hccomanifests.GlobalPullSecretDaemonSet()
				err := guestClient.Get(ctx, crclient.ObjectKey{Name: ds.Name, Namespace: ds.Namespace}, ds)
				return ds, err
			}, []Predicate[*appsv1.DaemonSet]{func(ds *appsv1.DaemonSet) (done bool, reasons string, err error) {
				if ds.Status.ObservedGeneration < ds.Generation {
					return false, fmt.Sprintf("DaemonSet status has not observed generation %d yet (current %d)", ds.Generation, ds.Status.ObservedGeneration), nil
				}
				if ds.Status.UpdatedNumberScheduled != ds.Status.DesiredNumberScheduled {
					return false, fmt.Sprintf("DaemonSet update in flight: %d/%d pods updated", ds.Status.UpdatedNumberScheduled, ds.Status.DesiredNumberScheduled), nil
				}
				if ds.Status.NumberReady != ds.Status.DesiredNumberScheduled {
					return false, fmt.Sprintf("DaemonSet not ready: %d/%d pods ready", ds.Status.NumberReady, ds.Status.DesiredNumberScheduled), nil
				}
				return true, fmt.Sprintf("DaemonSet ready: %d/%d pods", ds.Status.NumberReady, ds.Status.DesiredNumberScheduled), nil
			}}, WithTimeout(5*time.Minute), WithInterval(10*time.Second))
		})

		// Check if the config.json is updated in all of the nodes
		t.Run("Check if the config.json is correct in all of the nodes", func(t *testing.T) {
			VerifyKubeletConfigWithDaemonSet(t, ctx, guestClient, dsImage)
		})
	})

	return nil
}

func createAdditionalPullSecret(ctx context.Context, guestClient crclient.Client, pullSecretData []byte, registrySecretName, registryNamespace string) error {
	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      registrySecretName,
			Namespace: registryNamespace,
		},
		Type: corev1.SecretTypeDockerConfigJson,
		Data: map[string][]byte{
			corev1.DockerConfigJsonKey: pullSecretData,
		},
	}

	if err := guestClient.Create(ctx, secret); err != nil && !apierrors.IsAlreadyExists(err) {
		return fmt.Errorf("failed to create secret: %v", err)
	}

	return nil
}

func EnsureKubeAPIDNSNameCustomCert(t *testing.T, ctx context.Context, mgmtClient crclient.Client, entryHostedCluster *hyperv1.HostedCluster) {
	t.Run("EnsureKubeAPIDNSNameCustomCert", func(t *testing.T) {
		AtLeast(t, Version419)

		// Skip for kubevirt HostedClusters
		if entryHostedCluster.Spec.Platform.Type == hyperv1.KubevirtPlatform {
			t.Skip("Skipping EnsureKubeAPIDNSNameCustomCert test for kubevirt")
		}

		var (
			hcKASCustomKubeconfigSecretName string

			serviceDomain        = "service.ci.hypershift.devcluster.openshift.com"
			isAzure              = entryHostedCluster.Spec.Platform.Type == hyperv1.AzurePlatform
			retryTimeout         = 5 * time.Minute
			dnsResolutionTimeout = 30 * time.Minute
			kasDeploymentTimeout = 30 * time.Minute

			// Using domain name filtered by the external-dns deployment in CI
			customApiServerHost     = fmt.Sprintf("api-custom-cert-%s.%s", entryHostedCluster.Spec.InfraID, serviceDomain)
			hcpNamespace            = manifests.HostedControlPlaneNamespace(entryHostedCluster.Namespace, entryHostedCluster.Name)
			kasCustomCertSecretName = fmt.Sprintf("%s-kas-custom-cert", entryHostedCluster.Name)
		)

		if isAzure {
			serviceDomain = "aks-e2e.hypershift.azure.devcluster.openshift.com"
			customApiServerHost = fmt.Sprintf("api-custom-cert-%s.%s", entryHostedCluster.Spec.InfraID, serviceDomain)

			// Based on sample test evidence: ~40% failure rate due to internal DNS lag after external resolution
			// Use retries instead of proactive waiting for better efficiency
			retryTimeout = 10 * time.Minute // Extended retry for the kubeconfig test
			t.Log("Using Azure-specific retry strategy for DNS propagation race condition")
		}

		g := NewWithT(t)
		if !util.IsPublicHC(entryHostedCluster) {
			return
		}

		// Generate a custom certificate for the KAS
		t.Log("Generating custom certificate with DNS name", customApiServerHost)
		customCert, customKey, err := GenerateCustomCertificate([]string{customApiServerHost}, 24*time.Hour)
		g.Expect(err).NotTo(HaveOccurred(), "failed to generate custom certificate")

		// Create secret with the custom certificate
		t.Log("Creating custom certificate secret")
		customCertSecret := &corev1.Secret{
			ObjectMeta: metav1.ObjectMeta{
				Name:      kasCustomCertSecretName,
				Namespace: entryHostedCluster.Namespace,
			},
			Data: map[string][]byte{
				"tls.crt": customCert,
				"tls.key": customKey,
			},
		}
		err = mgmtClient.Create(ctx, customCertSecret)
		g.Expect(err).NotTo(HaveOccurred(), "failed to create custom certificate secret")

		// update HC with a KubeAPIDNSName and KAS custom serving cert
		hc := entryHostedCluster.DeepCopy()
		t.Log("Updating hosted cluster with KubeAPIDNSName and KAS custom serving cert")
		err = retry.RetryOnConflict(retry.DefaultRetry, func() error {
			// Get the latest version of the object
			latestHC := &hyperv1.HostedCluster{}
			if err := mgmtClient.Get(ctx, client.ObjectKeyFromObject(hc), latestHC); err != nil {
				return err
			}

			// update the KubeAPIDNSName
			latestHC.Spec.KubeAPIServerDNSName = customApiServerHost

			// update the KAS custom serving cert
			if latestHC.Spec.Configuration == nil {
				latestHC.Spec.Configuration = &hyperv1.ClusterConfiguration{}
			}
			if latestHC.Spec.Configuration.APIServer == nil {
				latestHC.Spec.Configuration.APIServer = &configv1.APIServerSpec{}
			}

			namedCertificate := configv1.APIServerNamedServingCert{
				Names: []string{customApiServerHost},
				ServingCertificate: configv1.SecretNameReference{
					Name: kasCustomCertSecretName,
				},
			}

			if len(latestHC.Spec.Configuration.APIServer.ServingCerts.NamedCertificates) == 0 {
				latestHC.Spec.Configuration.APIServer.ServingCerts.NamedCertificates = []configv1.APIServerNamedServingCert{namedCertificate}
			} else {
				latestHC.Spec.Configuration.APIServer.ServingCerts.NamedCertificates = append(latestHC.Spec.Configuration.APIServer.ServingCerts.NamedCertificates, namedCertificate)
			}

			return mgmtClient.Update(ctx, latestHC)
		})
		g.Expect(err).NotTo(HaveOccurred(), "failed to update hosted cluster")

		t.Log("Getting custom kubeconfig client")
		customApiServerURL := fmt.Sprintf("https://%s:%s", customApiServerHost, "443")
		kasCustomKubeconfigClient, kbCrclient := GetCustomKubeconfigClients(t, ctx, mgmtClient, entryHostedCluster, customApiServerURL)

		// wait for the KubeAPIDNSName to be reconciled
		t.Log("waiting for the KubeAPIDNSName to be reconciled")
		_ = WaitForCustomKubeconfig(t, ctx, mgmtClient, entryHostedCluster)

		// Get HC and HCP updated
		err = mgmtClient.Get(ctx, client.ObjectKeyFromObject(hc), hc)
		g.Expect(err).NotTo(HaveOccurred(), "failed to get updated HostedCluster")

		hcp := &hyperv1.HostedControlPlane{}
		err = mgmtClient.Get(ctx, types.NamespacedName{Namespace: hcpNamespace, Name: entryHostedCluster.Name}, hcp)
		g.Expect(err).NotTo(HaveOccurred(), "failed to get updated HostedControlPlane")

		// Find the external name destination for the KAS Service
		t.Log("Finding the external name destination for the KAS Service")
		var externalNameDestination string
		if entryHostedCluster.Spec.Services != nil {
			for _, service := range entryHostedCluster.Spec.Services {
				fmt.Printf("service: %+v\n", service)
				switch service.Service {
				case hyperv1.APIServer:
					switch service.Type {
					case hyperv1.Route:
						if service.Route != nil && len(service.Route.Hostname) > 0 {
							externalNameDestination = service.Route.Hostname
							break
						}
					case hyperv1.LoadBalancer:
						if service.LoadBalancer != nil && len(service.LoadBalancer.Hostname) > 0 {
							externalNameDestination = service.LoadBalancer.Hostname
							break
						}
					case hyperv1.NodePort:
						if service.NodePort != nil && len(service.NodePort.Address) > 0 && service.NodePort.Port != 0 {
							externalNameDestination = fmt.Sprintf("%s:%d", service.NodePort.Address, service.NodePort.Port)
							break
						}
					}
				default:
					t.Log("service custom DNS name not found, using the control plane endpoint")
					hcp := &hyperv1.HostedControlPlane{}
					err = mgmtClient.Get(ctx, types.NamespacedName{Namespace: hcpNamespace, Name: entryHostedCluster.Name}, hcp)
					g.Expect(err).NotTo(HaveOccurred(), "failed to get updated HostedControlPlane")
					g.Expect(hcp.Status.ControlPlaneEndpoint.Host).NotTo(BeEmpty(), "failed to get the control plane endpoint")
					externalNameDestination = hcp.Status.ControlPlaneEndpoint.Host
				}
			}
		}
		g.Expect(externalNameDestination).NotTo(BeEmpty(), "failed to get the external name destination")

		// Create a new KAS Service to be used by the external-dns deployment in CI
		t.Logf("Creating a new KAS Service to be used by the external-dns deployment in CI with the custom DNS name %s", customApiServerHost)
		svc := &corev1.Service{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "kube-apiserver-private-external",
				Namespace: hcpNamespace,
				Annotations: map[string]string{
					"external-dns.alpha.kubernetes.io/hostname": customApiServerHost,
					"external-dns.alpha.kubernetes.io/ttl":      "30",
				},
			},
			Spec: corev1.ServiceSpec{
				ExternalName: externalNameDestination,
				Type:         corev1.ServiceTypeExternalName,
			},
		}

		err = mgmtClient.Create(ctx, svc)
		g.Expect(err).NotTo(HaveOccurred(), "failed to update KAS Service")

		// Wait until the URL is resolvable. This can take a long time to become accessible.
		start := time.Now()
		g.Eventually(func() error {
			t.Logf("[%s] Waiting until the URL is resolvable: %s", time.Now().Format(time.RFC3339), customApiServerHost)
			_, err := net.LookupIP(customApiServerHost)
			if err != nil {
				return fmt.Errorf("failed to resolve the custom DNS name: %v", err)
			}
			t.Logf("resolved the custom DNS name after %s\n", time.Since(start))
			return nil
		}, dnsResolutionTimeout, 10*time.Second).Should(Succeed(), "failed to resolve the custom DNS name")

		// Wait until the KAS Deployment is ready
		t.Log("Waiting until the KAS Deployment is ready")
		g.Eventually(func() bool {
			kubeAPIServerDeployment := &appsv1.Deployment{}
			err = mgmtClient.Get(ctx, types.NamespacedName{Namespace: hcpNamespace, Name: "kube-apiserver"}, kubeAPIServerDeployment)
			if err != nil {
				return false
			}
			return util.IsDeploymentReady(ctx, kubeAPIServerDeployment)
		}, kasDeploymentTimeout, 10*time.Second).Should(BeTrue(), "failed to ensure KAS Deployment is ready")

		// KAS deployment readiness should ensure certificate configuration is loaded
		// If certificate loading becomes an issue, we'll see TLS errors (not DNS errors)

		t.Run("EnsureCustomAdminKubeconfigStatusExists", func(t *testing.T) {
			g := NewWithT(t)
			t.Log("Checking CustomAdminKubeconfigStatus are present")
			g.Expect(hcp.Status.CustomKubeconfig).ToNot(BeNil(), "HostedControlPlaneKASCustomKubeconfigis nil")
			g.Expect(hc.Status.CustomKubeconfig).ToNot(BeNil(), "HostedClusterKASCustomKubeconfigis nil")
			hcKASCustomKubeconfigSecretName = hc.Status.CustomKubeconfig.Name
		})
		t.Run("EnsureCustomAdminKubeconfigExists", func(t *testing.T) {
			g := NewWithT(t)
			// Get KASCustomKubeconfig secret from HCP Namespace
			t.Log("Checking CustomAdminKubeconfigs are present")
			hcpKASCustomKubeconfig := cpomanifests.KASCustomKubeconfigSecret(hcpNamespace, nil)
			err := mgmtClient.Get(ctx, client.ObjectKeyFromObject(hcpKASCustomKubeconfig), hcpKASCustomKubeconfig)
			g.Expect(err).ToNot(HaveOccurred(), "failed to get KAS custom kubeconfig secret")
			g.Expect(hc.Status.CustomKubeconfig).ToNot(BeNil(), "KASCustomKubeconfig is nil")

			// Get KASCustomKubeconfig secret from HC Namespace
			hcCustomKubeconfigSecret := &corev1.Secret{}
			err = mgmtClient.Get(ctx, types.NamespacedName{Namespace: hc.Namespace, Name: hc.Status.CustomKubeconfig.Name}, hcCustomKubeconfigSecret)
			g.Expect(err).ToNot(HaveOccurred(), "failed to get KAS custom kubeconfig secret from HC namespace")
		})
		t.Run("EnsureCustomAdminKubeconfigReachesTheKAS", func(t *testing.T) {
			g := NewWithT(t)
			t.Log("Checking CustomAdminKubeconfig reaches the KAS")
			if isAzure {
				t.Log("Using extended retry timeout for Azure DNS propagation")
			}
			// Add retry logic for DNS-related failures with platform-specific timeout
			g.Eventually(func() error {
				cv := &configv1.ClusterVersion{}
				err := kbCrclient.Get(ctx, types.NamespacedName{Name: "version"}, cv)
				if err != nil {
					// Check if this is a DNS-related error
					if strings.Contains(err.Error(), "no such host") || strings.Contains(err.Error(), "dial tcp") ||
						strings.Contains(err.Error(), "failed to get API group resources") {
						t.Logf("DNS resolution issue detected, retrying: %v", err)
						return err
					}
					// For non-DNS errors, fail immediately
					return fmt.Errorf("non-DNS error occurred: %w", err)
				}
				t.Logf("Successfully verified custom kubeconfig can reach KAS")
				return nil
			}, retryTimeout, 10*time.Second).Should(Succeed(), "failed to get HostedCluster ClusterVersion with KAS custom kubeconfig")
		})
		t.Run("EnsureCustomAdminKubeconfigInfraStatusIsUpdated", func(t *testing.T) {
			g := NewWithT(t)
			t.Log("Checking CustomAdminKubeconfig Infrastructure status is updated")
			EventuallyObject(t, ctx, "a successful connection to the custom DNS guest API server",
				func(ctx context.Context) (*authenticationv1.SelfSubjectReview, error) {
					return kasCustomKubeconfigClient.AuthenticationV1().SelfSubjectReviews().Create(ctx, &authenticationv1.SelfSubjectReview{}, metav1.CreateOptions{})
				}, nil, WithTimeout(30*time.Minute),
			)
			infra := &configv1.Infrastructure{}
			err := kbCrclient.Get(ctx, types.NamespacedName{Name: "cluster"}, infra)
			g.Expect(err).ToNot(HaveOccurred(), "failed to get HostedCluster Infrastructure with KAS custom kubeconfig")
			g.Expect(infra.Status.APIServerURL).To(ContainSubstring(hc.Spec.KubeAPIServerDNSName), "Infrastructure APIServerURL does not contains the KubeAPIServerDNSName set in the HostedCluster")
		})

		// removing KubeAPIDNSName from HC
		err = retry.RetryOnConflict(retry.DefaultRetry, func() error {
			// Get the latest version of the object
			latestHC := &hyperv1.HostedCluster{}
			if err := mgmtClient.Get(ctx, client.ObjectKeyFromObject(hc), latestHC); err != nil {
				return err
			}
			// Apply our changes to the latest version
			latestHC.Spec.KubeAPIServerDNSName = ""
			return mgmtClient.Update(ctx, latestHC)
		})
		g.Expect(err).NotTo(HaveOccurred(), "failed to update hosted control plane")

		// Remove the annotation from the KAS Service
		err = retry.RetryOnConflict(retry.DefaultRetry, func() error {
			svc := &corev1.Service{}
			err = mgmtClient.Get(ctx, types.NamespacedName{Namespace: hcpNamespace, Name: "kube-apiserver"}, svc)
			g.Expect(err).NotTo(HaveOccurred(), "failed to get updated KAS Service")
			delete(svc.Annotations, "external-dns.alpha.kubernetes.io/hostname")
			return mgmtClient.Update(ctx, svc)
		})
		g.Expect(err).NotTo(HaveOccurred(), "failed to update KAS Service")

		EventuallyObject(t, ctx, "the KAS custom kubeconfig secret to be deleted from HC Namespace",
			func(ctx context.Context) (*hyperv1.HostedCluster, error) {
				hc := &hyperv1.HostedCluster{}
				err := mgmtClient.Get(ctx, types.NamespacedName{Name: entryHostedCluster.Name, Namespace: entryHostedCluster.Namespace}, hc)
				return hc, err
			},
			[]Predicate[*hyperv1.HostedCluster]{
				func(hostedCluster *hyperv1.HostedCluster) (done bool, reason string, err error) {
					if hostedCluster.Status.CustomKubeconfig != nil {
						return false, fmt.Sprintf("HC - KAS custom kubeconfig secret still exists: %s", hostedCluster.Status.CustomKubeconfig.Name), nil
					}
					return true, "HC - KAS custom kubeconfig secret disappeared from HC Namespace", nil
				},
			}, WithInterval(5*time.Second), WithTimeout(30*time.Minute),
		)

		EventuallyObject(t, ctx, "the KAS custom kubeconfig secret to be deleted from HCP Namespace",
			func(ctx context.Context) (*hyperv1.HostedControlPlane, error) {
				hcp := &hyperv1.HostedControlPlane{}
				err := mgmtClient.Get(ctx, types.NamespacedName{Name: entryHostedCluster.Name, Namespace: hcpNamespace}, hcp)
				return hcp, err
			},
			[]Predicate[*hyperv1.HostedControlPlane]{
				func(hcp *hyperv1.HostedControlPlane) (done bool, reason string, err error) {
					if hcp.Status.CustomKubeconfig != nil {
						return false, fmt.Sprintf("HCP - KAS custom kubeconfig secret still exists: %s", hcp.Status.CustomKubeconfig.Name), nil
					}
					return true, "KAS custom kubeconfig secret disappeared from HCP Namespace", nil
				},
			}, WithInterval(5*time.Second), WithTimeout(30*time.Minute),
		)

		t.Run("EnsureCustomAdminKubeconfigIsRemoved", func(t *testing.T) {
			g := NewWithT(t)
			t.Log("Checking CustomAdminKubeconfig are removed")
			hcpKASCustomKubeconfig := cpomanifests.KASCustomKubeconfigSecret(hcpNamespace, nil)
			err := mgmtClient.Get(ctx, client.ObjectKeyFromObject(hcpKASCustomKubeconfig), hcpKASCustomKubeconfig)
			g.Expect(err).To(HaveOccurred(), "KAS custom kubeconfig secret still exists in HCP namespace")

			// Get KASCustomKubeconfig secret from HC Namespace
			hcKASCustomKubeconfigSecret := &corev1.Secret{}
			err = mgmtClient.Get(ctx, types.NamespacedName{Namespace: hc.Namespace, Name: hcKASCustomKubeconfigSecretName}, hcKASCustomKubeconfigSecret)
			g.Expect(err).To(HaveOccurred(), "KAS custom kubeconfig secret still exists in HC namespace")
		})

		updatedHC := &hyperv1.HostedCluster{}
		EventuallyObject(t, ctx, "the KAS custom kubeconfig status to be removed",
			func(ctx context.Context) (*hyperv1.HostedCluster, error) {
				err := mgmtClient.Get(ctx, types.NamespacedName{Name: entryHostedCluster.Name, Namespace: entryHostedCluster.Namespace}, updatedHC)
				return updatedHC, err
			},
			[]Predicate[*hyperv1.HostedCluster]{
				func(hostedCluster *hyperv1.HostedCluster) (done bool, reason string, err error) {
					if updatedHC.Status.CustomKubeconfig != nil {
						return false, fmt.Sprintf("KAS custom kubeconfig status still exists: %s", updatedHC.Status.CustomKubeconfig), nil
					}
					return true, "KAS custom kubeconfig status disappeared", nil
				},
			}, WithInterval(5*time.Second), WithTimeout(30*time.Minute),
		)

		t.Run("EnsureCustomAdminKubeconfigStatusIsRemoved", func(t *testing.T) {
			g := NewWithT(t)
			t.Log("Checking CustomAdminKubeconfigStatus are removed")
			g.Expect(updatedHC.Status.CustomKubeconfig).To(BeNil(), "HostedClusterKASCustomKubeconfigis not nil")
		})

		// Delete NamedCertificates from the KAS
		err = retry.RetryOnConflict(retry.DefaultRetry, func() error {
			latestHC := &hyperv1.HostedCluster{}
			if err := mgmtClient.Get(ctx, client.ObjectKeyFromObject(hc), latestHC); err != nil {
				return fmt.Errorf("failed to get latest HostedCluster: %v", err)
			}
			latestHC.Spec.Configuration.APIServer.ServingCerts.NamedCertificates = []configv1.APIServerNamedServingCert{}
			return mgmtClient.Update(ctx, latestHC)
		})
		g.Expect(err).NotTo(HaveOccurred(), "failed to update hosted cluster")

		// Delete custom certificate secret
		t.Log("Deleting custom certificate secret")
		latestCustomCertSecret := customCertSecret.DeepCopy()
		err = mgmtClient.Get(ctx, types.NamespacedName{Namespace: hcpNamespace, Name: kasCustomCertSecretName}, latestCustomCertSecret)
		g.Expect(err).NotTo(HaveOccurred(), "failed to get custom certificate secret")
		err = mgmtClient.Delete(ctx, latestCustomCertSecret)
		g.Expect(err).NotTo(HaveOccurred(), "failed to delete custom certificate secret")
	})
}

func EnsureAdmissionPolicies(t *testing.T, ctx context.Context, mgmtClient crclient.Client, hc *hyperv1.HostedCluster) {
	if !util.IsPublicHC(hc) {
		return // Admission policies are only validated in public clusters does not worth to test it in private ones.
	}
	guestClient := WaitForGuestClient(t, ctx, mgmtClient, hc)
	t.Run("EnsureValidatingAdmissionPoliciesExists", func(t *testing.T) {
		CPOAtLeast(t, Version418, hc)
		g := NewWithT(t)
		t.Log("Checking that all ValidatingAdmissionPolicies are present")
		var validatingAdmissionPolicies k8sadmissionv1.ValidatingAdmissionPolicyList
		if err := guestClient.List(ctx, &validatingAdmissionPolicies); err != nil {
			t.Errorf("Failed to list ValidatingAdmissionPolicies: %v", err)
		}
		if len(validatingAdmissionPolicies.Items) == 0 {
			t.Errorf("No ValidatingAdmissionPolicies found")
		}
		requiredVAPs := []string{
			hccokasvap.AdmissionPolicyNameConfig,
			hccokasvap.AdmissionPolicyNameMirror,
			hccokasvap.AdmissionPolicyNameICSP,
			hccokasvap.AdmissionPolicyNameInfra,
			hccokasvap.AdmissionPolicyNameNTOMirroredConfigs,
		}
		presentVAPs := []string{}
		for _, vap := range validatingAdmissionPolicies.Items {
			presentVAPs = append(presentVAPs, vap.Name)
		}
		for _, requiredVAP := range requiredVAPs {
			g.Expect(presentVAPs).To(ContainElement(requiredVAP), fmt.Sprintf("ValidatingAdmissionPolicy %s not found", requiredVAP))
		}
	})
	t.Run("EnsureValidatingAdmissionPoliciesCheckDeniedRequests", func(t *testing.T) {
		CPOAtLeast(t, Version418, hc)
		g := NewWithT(t)
		t.Log("Checking Denied KAS Requests for ValidatingAdmissionPolicies")
		apiServer := &configv1.APIServer{
			ObjectMeta: metav1.ObjectMeta{
				Name: "cluster",
			},
		}
		err := guestClient.Get(ctx, client.ObjectKeyFromObject(apiServer), apiServer)
		g.Expect(err).NotTo(HaveOccurred(), fmt.Sprintf("Failed to wait grabbing HostedCluster apiserver configuration: %v", err))
		g.Expect(apiServer).NotTo(BeNil(), "Apiserver configuration is nil")
		apiServerCP := apiServer.DeepCopy()
		apiServerCP.Spec.Audit.Profile = configv1.AllRequestBodiesAuditProfileType
		err = guestClient.Update(ctx, apiServerCP)
		g.Expect(err).To(HaveOccurred(), fmt.Sprintf("Failed block apiservers configuration update: %v", err))

	})
	t.Run("EnsureValidatingAdmissionPoliciesDontBlockStatusModifications", func(t *testing.T) {
		g := NewWithT(t)
		t.Log("Checking ClusterOperator status modifications are allowed")
		network := &configv1.Network{
			ObjectMeta: metav1.ObjectMeta{
				Name: "cluster",
			},
		}
		err := guestClient.Get(ctx, client.ObjectKeyFromObject(network), network)
		g.Expect(err).NotTo(HaveOccurred(), fmt.Sprintf("Failed to wait grabbing HostedCluster network configuration: %v", err))
		g.Expect(network).NotTo(BeNil(), "network configuration is nil")
		cpNetwork := network.DeepCopy()
		cpNetwork.Status.ClusterNetworkMTU = 9180
		err = guestClient.Update(ctx, cpNetwork)
		g.Expect(err).ToNot(HaveOccurred(), fmt.Sprintf("Failed updating network status ClusterNetworkMTU field: %v", err))
	})
	if hc.Spec.OLMCatalogPlacement == hyperv1.GuestOLMCatalogPlacement {
		t.Run("EnsureValidatingAdmissionPoliciesCheckAllowedRequest", func(t *testing.T) {
			g := NewWithT(t)
			t.Log("Checking Allowed KAS Requests for ValidatingAdmissionPolicies")
			operatorHub := &configv1.OperatorHub{
				ObjectMeta: metav1.ObjectMeta{
					Name: "cluster",
				},
			}
			err := guestClient.Get(ctx, client.ObjectKeyFromObject(operatorHub), operatorHub)
			g.Expect(err).NotTo(HaveOccurred(), fmt.Sprintf("Failed to wait grabbing HostedCluster network configuration: %v", err))
			g.Expect(operatorHub).NotTo(BeNil(), "OperatorHub configuration is nil")
			operatorHubCP := operatorHub.DeepCopy()
			operatorHubCP.Spec.DisableAllDefaultSources = true
			err = guestClient.Update(ctx, operatorHubCP)
			g.Expect(err).NotTo(HaveOccurred(), fmt.Sprintf("Failed to update OperatorHub configuration: %v", err))
		})
	}
}

const (
	// Metrics
	// TODO (jparrill): We need to separate the metrics.go from the main pkg in the hypershift-operator.
	//     Delete these references when it's done and import it from there
	HypershiftOperatorInfoName = "hypershift_operator_info"
)

func extractDataFromFamilies(metricFamilies map[string]*dto.MetricFamily, metric, labelKey, labelValue string) []*dto.LabelPair {
	v, ok := metricFamilies[metric]
	if !ok {
		return nil
	}
	labelPairs := []*dto.LabelPair{}
	for _, m := range v.Metric {
		for _, l := range m.GetLabel() {
			if l == nil {
				continue
			}
			if len(labelKey) == 0 || (l.GetName() == labelKey && l.GetValue() == labelValue) {
				labelPairs = append(labelPairs, l)
			}
		}
	}
	return labelPairs
}

// ValidateMetricPresence checks if a metric meets the expected presence criteria
// Returns true if validation passes, false otherwise
func ValidateMetricPresence(t *testing.T, mf map[string]*dto.MetricFamily, query, labelKey, labelValue, metricName string, areMetricsExpectedToBePresent bool) bool {
	labelPairs := extractDataFromFamilies(mf, query, labelKey, labelValue)
	if areMetricsExpectedToBePresent {
		if len(labelPairs) < 1 {
			t.Logf("Expected results for metric %q, found none", metricName)
			return false
		}
	} else {
		if len(labelPairs) > 0 {
			t.Logf("Expected 0 results for metric %q, found %d", metricName, len(labelPairs))
			return false
		}
	}
	return true
}

// Verifies that the given metrics are defined for the given hosted cluster if areMetricsExpectedToBePresent is set to true.
// Verifies that the given metrics are not defined otherwise.
func ValidateMetrics(t *testing.T, ctx context.Context, client crclient.Client, hc *hyperv1.HostedCluster, metricsNames []string, areMetricsExpectedToBePresent bool) {
	t.Run("ValidateMetricsAreExposed", func(t *testing.T) {
		// TODO (alberto) this test should pass in None.
		// https://gcsweb-ci.apps.ci.l2s4.p1.openshiftapps.com/gcs/origin-ci-test/pr-logs/pull/openshift_hypershift/2459/pull-ci-openshift-hypershift-main-e2e-aws/1650438383060652032/artifacts/e2e-aws/run-e2e/artifacts/TestNoneCreateCluster_PreTeardownClusterDump/
		// https://storage.googleapis.com/origin-ci-test/pr-logs/pull/openshift_hypershift/2459/pull-ci-openshift-hypershift-main-e2e-aws/1650438383060652032/build-log.txt
		// https://prow.ci.openshift.org/view/gs/origin-ci-test/pr-logs/pull/openshift_hypershift/2459/pull-ci-openshift-hypershift-main-e2e-aws/1650438383060652032
		if hc.Spec.Platform.Type == hyperv1.NonePlatform {
			t.Skip("skipping on None platform")
		}

		// Polling to prevent races with prometheus scrape interval.
		err := wait.PollUntilContextTimeout(ctx, 10*time.Second, 5*time.Minute, true, func(ctx context.Context) (bool, error) {
			// exec curl command in HO pod metrics endpoint and return metric values if any
			mf, err := GetMetricsFromPod(ctx, client, "operator", "operator", "hypershift", "9000")
			if err != nil {
				t.Logf("unable to get exportedMetrics from hypershift-operator: %v", err)
				return false, nil
			}
			for _, metricName := range metricsNames {
				query := metricName
				labelKey := "name"
				labelValue := hc.Name
				if metricName == HypershiftOperatorInfoName {
					query = metricName
					labelKey, labelValue = "", ""
				}
				if strings.HasPrefix(metricName, "hypershift_nodepools") {
					query = metricName
					labelKey, labelValue = "cluster_name", hc.Name
				}
				// upgrade metric is only available for TestUpgradeControlPlane
				if metricName == hcmetrics.UpgradingDurationMetricName && !strings.HasPrefix("TestUpgradeControlPlane", t.Name()) {
					continue
				}

				if metricName == hcmetrics.HostedClusterManagedAzureInfoMetricName {
					if !(azureutil.IsAroHCP()) { // only for ARO
						continue
					}
					query = metricName
					labelKey, labelValue = "", ""
				}

				if !ValidateMetricPresence(t, mf, query, labelKey, labelValue, metricName, areMetricsExpectedToBePresent) {
					return false, nil
				}
			}
			return true, nil
		})
		if err != nil {
			t.Errorf("Failed to validate all metrics: %v", err)
		}
	})
}

func getIngressRouterDefaultIP(t *testing.T, ctx context.Context, client crclient.Client, hostedCluster *hyperv1.HostedCluster) (string, error) {
	t.Helper()

	defaultIngressRouterService := &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "router-default",
			Namespace: "openshift-ingress",
		},
	}

	if err := wait.PollUntilContextTimeout(ctx, 10*time.Second, 30*time.Minute, true, func(ctx context.Context) (done bool, err error) {
		getErr := client.Get(ctx, crclient.ObjectKeyFromObject(defaultIngressRouterService), defaultIngressRouterService)
		if apierrors.IsNotFound(getErr) {
			return false, nil
		}
		if len(defaultIngressRouterService.Status.LoadBalancer.Ingress) == 0 {
			return false, nil
		}
		return getErr == nil, err
	}); err != nil {
		return "", fmt.Errorf("router-default service did't become available: %v", err)
	}

	routerDefaultIP := defaultIngressRouterService.Status.LoadBalancer.Ingress[0].IP
	if routerDefaultIP == "" {
		return "", fmt.Errorf("router-default service does not have an IP")
	}

	t.Logf("router-default service IP: %s", routerDefaultIP)
	return routerDefaultIP, nil
}

func createIngressRoute53Record(t *testing.T, ctx context.Context, client crclient.Client, hostedCluster *hyperv1.HostedCluster, clusterOpts *PlatformAgnosticOptions) {
	t.Helper()
	g := NewWithT(t)

	t.Logf("Creating Ingress Route53 Record for HostedCluster %s", hostedCluster.Name)
	if clusterOpts.AWSPlatform.Credentials.AWSCredentialsFile == "" {
		t.Skip("AWS credentials file is not provided")
	}
	// This is hardcoded too in aws CreateInfraOptions
	awsRegion := "us-east-1"

	routerDefaultIP, err := getIngressRouterDefaultIP(t, ctx, client, hostedCluster)
	g.Expect(err).ToNot(HaveOccurred(), "failed to get router-default service IP")

	awsSession, err := clusterOpts.AWSPlatform.Credentials.GetSession("e2e-openstack-dns-record-on-aws", nil, awsRegion)
	g.Expect(err).ToNot(HaveOccurred(), "failed to create AWS session")

	route53Client := route53.New(awsSession, awsutil.NewAWSRoute53Config())
	g.Expect(route53Client).ToNot(BeNil(), "failed to create Route53 client")

	clusterName := hostedCluster.Name
	baseDomain := hostedCluster.Spec.DNS.BaseDomain
	zoneID, err := awsinfra.LookupZone(ctx, route53Client, hostedCluster.Spec.DNS.BaseDomain, false)
	g.Expect(err).ToNot(HaveOccurred(), "failed to lookup Route53 hosted zone %s", baseDomain)

	err = awsprivatelink.CreateRecord(ctx, route53Client, zoneID, "*.apps."+clusterName+"."+baseDomain, routerDefaultIP, "A")
	g.Expect(err).ToNot(HaveOccurred(), "failed to create Route53 record")
	t.Logf("Created Route53 record for HostedCluster %s: %s", hostedCluster.Name, "*.apps."+clusterName+"."+baseDomain)
}

func deleteIngressRoute53Records(t *testing.T, ctx context.Context, hostedCluster *hyperv1.HostedCluster, clusterOpts *PlatformAgnosticOptions) {
	t.Helper()
	g := NewWithT(t)

	t.Logf("Deleting Ingress Route53 Records for HostedCluster %s", hostedCluster.Name)
	if clusterOpts.AWSPlatform.Credentials.AWSCredentialsFile == "" {
		t.Skip("AWS credentials file is not provided")
	}
	// This is hardcoded too in aws CreateInfraOptions
	awsRegion := "us-east-1"

	awsSession, err := clusterOpts.AWSPlatform.Credentials.GetSession("e2e-openstack-dns-record-on-aws", nil, awsRegion)
	g.Expect(err).ToNot(HaveOccurred(), "failed to create AWS session")

	route53Client := route53.New(awsSession, awsutil.NewAWSRoute53Config())
	g.Expect(route53Client).ToNot(BeNil(), "failed to create Route53 client")

	clusterName := hostedCluster.Name
	baseDomain := hostedCluster.Spec.DNS.BaseDomain
	zoneID, err := awsinfra.LookupZone(ctx, route53Client, hostedCluster.Spec.DNS.BaseDomain, false)
	g.Expect(err).ToNot(HaveOccurred(), "failed to lookup Route53 hosted zone %s", baseDomain)

	record, err := awsprivatelink.FindRecord(ctx, route53Client, zoneID, "*.apps."+clusterName+"."+baseDomain, "A")
	g.Expect(err).ToNot(HaveOccurred(), "failed to find Route53 record %s", "*.apps."+clusterName+"."+baseDomain)

	if record == nil || len(record.ResourceRecords) == 0 {
		t.Logf("Route53 record for HostedCluster %s not found: %s", hostedCluster.Name, "*.apps."+clusterName+"."+baseDomain)
	} else {
		err = awsprivatelink.DeleteRecord(ctx, route53Client, zoneID, record)
		g.Expect(err).ToNot(HaveOccurred(), "failed to delete Route53 record %s", "*.apps."+clusterName+"."+baseDomain)
		t.Logf("Deleted Route53 record for HostedCluster %s: %s", hostedCluster.Name, "*.apps."+clusterName+"."+baseDomain)
	}
}

func ValidatePublicCluster(t *testing.T, ctx context.Context, client crclient.Client, hostedCluster *hyperv1.HostedCluster, clusterOpts *PlatformAgnosticOptions) {
	g := NewWithT(t)

	// Sanity check the cluster by waiting for the nodes to report ready
	guestClient := WaitForGuestClient(t, ctx, client, hostedCluster)

	// Create Ingress Route53 Record for OpenStack clusters when AWS credentials are provided
	if hostedCluster.Spec.Platform.Type == hyperv1.OpenStackPlatform && clusterOpts.AWSPlatform.Credentials.AWSCredentialsFile != "" {
		if clusterOpts.NodePoolReplicas > 0 {
			createIngressRoute53Record(t, ctx, guestClient, hostedCluster, clusterOpts)
		} else {
			t.Logf("Skipping creating Ingress Route53 Record for HostedCluster %s as there are no worker nodes", hostedCluster.Name)
		}
	}

	// Wait for Nodes to be Ready
	numNodes := clusterOpts.NodePoolReplicas * int32(len(clusterOpts.AWSPlatform.Zones))
	WaitForNReadyNodes(t, ctx, guestClient, numNodes, hostedCluster.Spec.Platform.Type)

	// rollout will not complete if there are no worker nodes.
	if numNodes > 0 {
		WaitForImageRollout(t, ctx, client, hostedCluster)
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

	// Extend the timeout to 20 minutes if there are no worker nodes as
	// 10 minutes might not be enough if image pulls are slow.
	timeout := 10 * time.Minute
	if numNodes == 0 {
		timeout = 20 * time.Minute
	}
	ValidateHostedClusterConditions(t, ctx, client, hostedCluster, numNodes > 0, timeout)

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
	// Validate configuration status matches between HCP, HC, and guest cluster
	ValidateConfigurationStatus(t, ctx, client, guestClient, hostedCluster)
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
		WaitForImageRollout(t, ctx, client, hostedCluster)
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

	// Extend the timeout to 20 minutes if there are no worker nodes as
	// 10 minutes might not be enough if image pulls are slow.
	timeout := 10 * time.Minute
	if numNodes == 0 {
		timeout = 20 * time.Minute
	}
	ValidateHostedClusterConditions(t, ctx, client, hostedCluster, numNodes > 0, timeout)

	EnsureNoCrashingPods(t, ctx, client, hostedCluster)
	EnsureOAPIMountsTrustBundle(t, context.Background(), client, hostedCluster)

	if hostedCluster.Spec.Platform.Type == hyperv1.AWSPlatform {
		g.Expect(hostedCluster.Spec.Configuration.Ingress.LoadBalancer.Platform.AWS.Type).To(Equal(configv1.NLB))
	}

}

func ValidateHostedClusterConditions(t *testing.T, ctx context.Context, client crclient.Client, hostedCluster *hyperv1.HostedCluster, hasWorkerNodes bool, timeout time.Duration) {
	expectedConditions := conditions.ExpectedHCConditions(hostedCluster)
	// OCPBUGS-59885: Ignore KubeVirtNodesLiveMigratable in e2e; CI envs may lack RWX-capable PVCs, causing false failures
	delete(expectedConditions, hyperv1.KubeVirtNodesLiveMigratable)
	if !hasWorkerNodes {
		expectedConditions[hyperv1.ClusterVersionAvailable] = metav1.ConditionFalse
		expectedConditions[hyperv1.ClusterVersionSucceeding] = metav1.ConditionFalse
		expectedConditions[hyperv1.ClusterVersionProgressing] = metav1.ConditionTrue
		delete(expectedConditions, hyperv1.ValidKubeVirtInfraNetworkMTU)
	}
	if IsLessThan(Version415) {
		// ValidKubeVirtInfraNetworkMTU condition is not present in versions < 4.15
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

		expectedTolerations := []corev1.Toleration{
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
		}

		expectedAffinity := &corev1.Affinity{
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

			// SRO is being removed in 4.18, not worth correcting the tolerations on back releases
			if pod.Labels["name"] == "shared-resource-csi-driver-operator" {
				continue
			}

			// aws-ebs-csi-driver-operator tolerations are set through CSO and are different from the ones in the DC
			if strings.Contains(pod.Name, awsEbsCsiDriverOperatorPodSubstring) {
				g.Expect(pod.Spec.Tolerations).To(ContainElements(awsEbsCsiDriverOperatorTolerations), "pod %s", pod.Name)
			} else {
				g.Expect(pod.Spec.Tolerations).To(ContainElements(expectedTolerations), "pod %s", pod.Name)
			}

			g.Expect(pod.Spec.Affinity.NodeAffinity.PreferredDuringSchedulingIgnoredDuringExecution).To(ContainElements(expectedAffinity.NodeAffinity.PreferredDuringSchedulingIgnoredDuringExecution), "pod %s", pod.Name)
			g.Expect(pod.Spec.Affinity.PodAffinity.PreferredDuringSchedulingIgnoredDuringExecution).To(ContainElements(expectedAffinity.PodAffinity.PreferredDuringSchedulingIgnoredDuringExecution), "pod %s", pod.Name)
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
		AtLeast(t, Version416)
		g := NewWithT(t)

		hcpNamespace := manifests.HostedControlPlaneNamespace(hostedCluster.Namespace, hostedCluster.Name)

		var pods corev1.PodList
		if err := c.List(ctx, &pods, &crclient.ListOptions{Namespace: hcpNamespace}); err != nil {
			t.Fatalf("failed to list pods in namespace %s: %v", hcpNamespace, err)
		}

		expectedComponentsWithTokenMount := append(expectedKasManagementComponents,
			"aws-ebs-csi-driver-controller",
			"packageserver",
			"csi-snapshot-controller",
			"shared-resource-csi-driver-operator",
		)

		if IsLessThan(Version418) {
			expectedComponentsWithTokenMount = append(expectedComponentsWithTokenMount,
				"csi-snapshot-webhook",
			)
		}

		if hostedCluster.Spec.Platform.Type == hyperv1.AzurePlatform {
			expectedComponentsWithTokenMount = append(expectedComponentsWithTokenMount,
				"azure-cloud-controller-manager",
				"azure-disk-csi-driver-controller",
				"azure-disk-csi-driver-operator",
				"azure-file-csi-driver-controller",
				"azure-file-csi-driver-operator",
			)
		}

		if hostedCluster.Spec.Platform.Type == hyperv1.OpenStackPlatform {
			expectedComponentsWithTokenMount = append(expectedComponentsWithTokenMount,
				"openstack-cinder-csi-driver-controller",
				"openstack-cinder-csi-driver-operator",
				"openstack-manila-csi-controllerplugin",
				"manila-csi-driver-operator",
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
					g.Expect(volume.Name).ToNot(HavePrefix("kube-api-access-"), "pod %s should not have kube-api-access-* volume mounted", pod.Name)
				}
			}
		}
	})
}

func EnsurePayloadArchSetCorrectly(t *testing.T, ctx context.Context, client crclient.Client, hostedCluster *hyperv1.HostedCluster) {
	t.Run("EnsurePayloadArchSetCorrectly", func(t *testing.T) {
		EventuallyObject(t, ctx, fmt.Sprintf("HostedCluster %s/%s to have valid Status.Payload", hostedCluster.Namespace, hostedCluster.Name),
			func(ctx context.Context) (*hyperv1.HostedCluster, error) {
				hc := &hyperv1.HostedCluster{}
				err := client.Get(ctx, crclient.ObjectKeyFromObject(hostedCluster), hc)
				return hc, err
			},
			[]Predicate[*hyperv1.HostedCluster]{
				func(cluster *hyperv1.HostedCluster) (done bool, reasons string, err error) {
					imageMetadataProvider := &hyperutil.RegistryClientImageMetadataProvider{}
					payloadArch, err := util.DetermineHostedClusterPayloadArch(ctx, client, cluster, imageMetadataProvider)
					if err != nil {
						return false, "failed to get hc payload arch", err
					}
					if payloadArch != cluster.Status.PayloadArch {
						return false, fmt.Sprintf("expected payload arch %s, got %s", cluster.Status.PayloadArch, payloadArch), nil
					}

					return true, "", nil
				},
			}, WithTimeout(30*time.Minute),
		)
	})
}

func EnsureCustomLabels(t *testing.T, ctx context.Context, client crclient.Client, hostedCluster *hyperv1.HostedCluster) {
	t.Run("EnsureCustomLabels", func(t *testing.T) {
		AtLeast(t, Version419)

		hcpNamespace := manifests.HostedControlPlaneNamespace(hostedCluster.Namespace, hostedCluster.Name)
		podList := &corev1.PodList{}
		if err := client.List(ctx, podList, crclient.InNamespace(hcpNamespace)); err != nil {
			t.Fatalf("error listing hcp pods: %v", err)
		}

		var podsWithoutLabel []string
		for _, pod := range podList.Items {
			// Skip KubeVirt related pods
			if pod.Labels["kubevirt.io"] == "virt-launcher" || pod.Labels["app"] == "vmi-console-debug" {
				continue
			}

			// Ensure that each pod in the HCP has the custom label
			if value, exist := pod.Labels["hypershift-e2e-test-label"]; !exist || value != "test" {
				podsWithoutLabel = append(podsWithoutLabel, pod.Name)
			}
		}

		if len(podsWithoutLabel) > 0 {
			t.Fatalf("expected pods [%s] to have label %s=%s", strings.Join(podsWithoutLabel, ", "), "hypershift-e2e-test-label", "test")
		}
	})
}

func EnsureCustomTolerations(t *testing.T, ctx context.Context, client crclient.Client, hostedCluster *hyperv1.HostedCluster) {
	t.Run("EnsureCustomTolerations", func(t *testing.T) {
		AtLeast(t, Version419)

		hcpNamespace := manifests.HostedControlPlaneNamespace(hostedCluster.Namespace, hostedCluster.Name)
		podList := &corev1.PodList{}
		if err := client.List(ctx, podList, crclient.InNamespace(hcpNamespace)); err != nil {
			t.Fatalf("error listing hcp pods: %v", err)
		}

		var podsWithoutToleration []string
		for _, pod := range podList.Items {
			// Skip KubeVirt related pods
			if pod.Labels["kubevirt.io"] == "virt-launcher" || pod.Labels["app"] == "vmi-console-debug" {
				continue
			}

			// Ensure that each pod in the HCP has the custom toleration
			found := false
			for _, toleration := range pod.Spec.Tolerations {
				if toleration.Key == "hypershift-e2e-test-toleration" &&
					toleration.Operator == corev1.TolerationOpEqual &&
					toleration.Value == "true" &&
					toleration.Effect == corev1.TaintEffectNoSchedule {
					found = true
					break
				}
			}
			if !found {
				podsWithoutToleration = append(podsWithoutToleration, pod.Name)
			}
		}

		if len(podsWithoutToleration) > 0 {
			t.Fatalf("expected pods [%s] to have hypershift-e2e-test-toleration", strings.Join(podsWithoutToleration, ", "))
		}
	})
}

func EnsureAppLabel(t *testing.T, ctx context.Context, client crclient.Client, hostedCluster *hyperv1.HostedCluster) {
	t.Run("EnsureAppLabel", func(t *testing.T) {
		AtLeast(t, Version419)

		hcpNamespace := manifests.HostedControlPlaneNamespace(hostedCluster.Namespace, hostedCluster.Name)
		podList := &corev1.PodList{}
		if err := client.List(ctx, podList, crclient.InNamespace(hcpNamespace)); err != nil {
			t.Fatalf("error listing hcp pods: %v", err)
		}

		var podsWithoutAppLabel []string
		for _, pod := range podList.Items {
			// Skip KubeVirt related pods
			if pod.Labels["kubevirt.io"] == "virt-launcher" || pod.Labels["app"] == "vmi-console-debug" {
				continue
			}

			// Ensure that each pod in the HCP has an app label set
			val, ok := pod.Labels["app"]
			if ok && val != "" {
				continue
			}

			podsWithoutAppLabel = append(podsWithoutAppLabel, pod.Name)
		}

		if len(podsWithoutAppLabel) > 0 {
			t.Fatalf("expected pods [%s] to have app label set", strings.Join(podsWithoutAppLabel, ", "))
		}
	})
}

func EnsureDefaultSecurityGroupTags(t *testing.T, ctx context.Context, client crclient.Client, hostedCluster *hyperv1.HostedCluster, clusterOpts PlatformAgnosticOptions) {
	t.Run("EnsureDefaultSecurityGroupTags", func(t *testing.T) {
		AtLeast(t, Version420)
		if hostedCluster.Spec.Platform.Type != hyperv1.AWSPlatform {
			t.Skip("This test is only applicable for AWS platform")
		}
		g := NewWithT(t)

		tagsPolicy := fmt.Sprintf(`{
			"Version": "2012-10-17",
			"Statement": [
				{
					"Effect": "Allow",
					"Action": [
						"ec2:CreateTags",
						"ec2:DeleteTags"
					],
					"Resource": "arn:aws:ec2:*:*:security-group/%s"
				}
			]
		}`, hostedCluster.Status.Platform.AWS.DefaultWorkerSecurityGroupID)

		cleanup, err := PutRolePolicy(clusterOpts.AWSPlatform.Credentials.AWSCredentialsFile, clusterOpts.AWSPlatform.Region, hostedCluster.Spec.Platform.AWS.RolesRef.ControlPlaneOperatorARN, tagsPolicy)
		g.Expect(err).NotTo(HaveOccurred(), "failed to put role policy for tagging default security group")
		defer func() {
			err := cleanup()
			g.Expect(err).NotTo(HaveOccurred(), "failed to cleanup role policy for tagging default security group")
		}()

		day2TagKey := "test-day2-tag"
		day2TagValue := "test-day2-value"

		// Update the hosted cluster to add a day2 tag
		err = UpdateObject(t, ctx, client, hostedCluster, func(object *hyperv1.HostedCluster) {
			object.Spec.Platform.AWS.ResourceTags = append(object.Spec.Platform.AWS.ResourceTags, hyperv1.AWSResourceTag{
				Key:   day2TagKey,
				Value: day2TagValue,
			})
		})
		g.Expect(err).NotTo(HaveOccurred(), "failed to update hostedcluster with day2 tag")

		// Ensure that day2 tag is correctly applied to the default security group.
		g.Eventually(func(g Gomega) {
			sg, err := GetDefaultSecurityGroup(clusterOpts.AWSPlatform.Credentials.AWSCredentialsFile, clusterOpts.AWSPlatform.Region, hostedCluster.Status.Platform.AWS.DefaultWorkerSecurityGroupID)
			g.Expect(err).NotTo(HaveOccurred(), "failed to get default security group")

			g.Expect(sg.Tags).To(ContainElement(&ec2.Tag{
				Key:   aws.String(day2TagKey),
				Value: aws.String(day2TagValue),
			}))
		}).WithContext(ctx).WithTimeout(time.Minute * 2).WithPolling(time.Second).Should(Succeed())

	})
}

func EnsureKubeAPIServerAllowedCIDRs(t *testing.T, ctx context.Context, mgmtClient crclient.Client, guestConfig *rest.Config, hc *hyperv1.HostedCluster) {
	t.Run("EnsureKubeAPIServerAllowedCIDRs", func(t *testing.T) {
		g := NewWithT(t)

		kubeClient, err := kubernetes.NewForConfig(guestConfig)
		g.Expect(err).NotTo(HaveOccurred())

		// ensure that kube-apiserver is not reachable from anywhere
		ensureAPIServerAllowedCIDRs(ctx, t, g, mgmtClient, kubeClient, hc, []string{"0.0.0.0/32"}, false)
		// ensure kube-apiserver is reachable when allowed CIDRs allow access from everywhere
		// This is useful for testing purposes, as it allows us to access the kube-apiserver from any IP
		// In a production environment, this should be restricted to specific CIDRs
		ensureAPIServerAllowedCIDRs(ctx, t, g, mgmtClient, kubeClient, hc, append([]string{"0.0.0.0/0"}, generateTestCIDRs250()...), true)
	})
}

func ensureAPIServerAllowedCIDRs(ctx context.Context, t *testing.T, g Gomega, mgmtClient crclient.Client, guestClient *kubeclient.Clientset, hc *hyperv1.HostedCluster, allowedCIDRs []string, shouldBeReachable bool) {
	err := UpdateObject(t, ctx, mgmtClient, hc, func(obj *hyperv1.HostedCluster) {
		if obj.Spec.Networking.APIServer == nil {
			obj.Spec.Networking.APIServer = &hyperv1.APIServerNetworking{}
		}
		obj.Spec.Networking.APIServer.AllowedCIDRBlocks = nil
		for _, cidr := range allowedCIDRs {
			obj.Spec.Networking.APIServer.AllowedCIDRBlocks = append(obj.Spec.Networking.APIServer.AllowedCIDRBlocks, hyperv1.CIDRBlock(cidr))
		}
	})
	g.Expect(err).To(Not(HaveOccurred()), "failed to update HostedCluster with allowed CIDRs")

	g.Eventually(func(g Gomega) {
		_, err = guestClient.ServerVersion()
		if shouldBeReachable {
			g.Expect(err).ToNot(HaveOccurred(), "kube-apiserver should be reachable")
		} else {
			g.Expect(err).To(HaveOccurred(), "kube-apiserver should not be reachable")
		}
	}).WithContext(ctx).WithTimeout(time.Minute * 3).WithPolling(time.Second * 5).Should(Succeed())

}

// generateTestCIDRs250 is a helper to generate 250 /32 CIDRs starting at 250.250.250.1
func generateTestCIDRs250() []string {
	cidrs := make([]string, 0, 250)
	for i := 1; i <= 250; i++ {
		cidrs = append(cidrs, fmt.Sprintf("250.250.250.%d/32", i))
	}
	return cidrs
}

// EnsureImageRegistryCapabilityDisabled validates the expectations for when ImageRegistryCapability is Disabled
func EnsureImageRegistryCapabilityDisabled(ctx context.Context, t *testing.T, g Gomega, clients *GuestClients) {
	t.Run("EnsureImageRegistryCapabilityDisabled", func(t *testing.T) {
		AtLeast(t, Version418)

		_, err := clients.CfgClient.ConfigV1().ClusterOperators().Get(ctx, "image-registry", metav1.GetOptions{})
		g.Expect(err).To(HaveOccurred())
		g.Expect(err.Error()).To(ContainSubstring("clusteroperators.config.openshift.io \"image-registry\" not found"))

		// ensure existing service accounts don't have pull-secrets.
		EventuallyObject(t, ctx, "Waiting for service account default/default to be provisioned...",
			func(ctx context.Context) (*corev1.ServiceAccount, error) {
				defaultSA, err := clients.KubeClient.CoreV1().ServiceAccounts("default").Get(ctx, "default", metav1.GetOptions{})
				return defaultSA, err
			},
			[]Predicate[*corev1.ServiceAccount]{
				func(serviceAccount *corev1.ServiceAccount) (done bool, reasons string, err error) {
					return serviceAccount != nil, "expected default/default service account to exist, got nil", nil
				},
			},
			WithInterval(10*time.Second), WithTimeout(2*time.Minute),
		)

		defaultSA, err := clients.KubeClient.CoreV1().ServiceAccounts("default").Get(ctx, "default", metav1.GetOptions{})
		g.Expect(err).NotTo(HaveOccurred(), "couldn't get default service account")
		g.Expect(defaultSA.ImagePullSecrets).To(BeNil())

		// create a namespace and ensure no pull-secrets are provisioned to
		// the newly auto-created service accounts.
		ns := &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: "test-namespace"}}
		ns, err = clients.KubeClient.CoreV1().Namespaces().Create(ctx, ns, metav1.CreateOptions{})
		g.Expect(err).NotTo(HaveOccurred(), "couldn't create test namespace")

		EventuallyObject(t, ctx, fmt.Sprintf("Waiting for service account default/%s to be provisioned...", ns.Name),
			func(ctx context.Context) (*corev1.ServiceAccount, error) {
				defaultSA, err := clients.KubeClient.CoreV1().ServiceAccounts(ns.Name).Get(ctx, "default", metav1.GetOptions{})
				return defaultSA, err
			},
			[]Predicate[*corev1.ServiceAccount]{
				func(serviceAccount *corev1.ServiceAccount) (done bool, reasons string, err error) {
					return serviceAccount != nil, "expected default/default service account to exist, got nil", nil
				},
			},
			WithInterval(10*time.Second), WithTimeout(2*time.Minute),
		)

		defaultSA, err = clients.KubeClient.CoreV1().ServiceAccounts(ns.Name).Get(ctx, "default", metav1.GetOptions{})
		g.Expect(err).NotTo(HaveOccurred(), "couldn't get default service account")
		g.Expect(defaultSA.ImagePullSecrets).To(BeNil())

		// ensure image-registry resources are not present
		_, err = clients.KubeClient.CoreV1().Namespaces().Get(ctx, "openshift-image-registry", metav1.GetOptions{})
		g.Expect(err).To(HaveOccurred())
		g.Expect(err.Error()).To(ContainSubstring("namespaces \"openshift-image-registry\" not found"))
	})
}

// GenerateCustomCertificate generates a self-signed certificate for the given DNS names
func GenerateCustomCertificate(dnsNames []string, validity time.Duration) ([]byte, []byte, error) {
	if len(dnsNames) == 0 {
		return nil, nil, fmt.Errorf("no DNS names provided")
	}

	cfg := &certs.CertCfg{
		Subject:      pkix.Name{CommonName: dnsNames[0], Organization: []string{"kubernetes"}, OrganizationalUnit: []string{"test"}},
		KeyUsages:    x509.KeyUsageKeyEncipherment | x509.KeyUsageDigitalSignature,
		ExtKeyUsages: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		Validity:     validity,
		DNSNames:     dnsNames,
		IsCA:         false,
	}

	key, crt, err := certs.GenerateSelfSignedCertificate(cfg)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to generate self-signed certificate: %w", err)
	}

	return certs.CertToPem(crt), certs.PrivateKeyToPem(key), nil
}

// EnsureOpenshiftSamplesCapabilityDisabled validates the expectations for when OpenShiftSamplesCapability is Disabled
func EnsureOpenshiftSamplesCapabilityDisabled(ctx context.Context, t *testing.T, g Gomega, clients *GuestClients) {
	t.Run("EnsureOpenshiftSamplesCapabilityDisabled", func(t *testing.T) {
		AtLeast(t, Version420)

		_, err := clients.CfgClient.ConfigV1().ClusterOperators().Get(ctx, "openshift-samples", metav1.GetOptions{})
		g.Expect(err).To(HaveOccurred())
		g.Expect(err.Error()).To(ContainSubstring("clusteroperators.config.openshift.io \"openshift-samples\" not found"))

		// ensure openshift-samples resources are not present
		_, err = clients.KubeClient.CoreV1().Namespaces().Get(ctx, "openshift-cluster-samples-operator", metav1.GetOptions{})
		g.Expect(err).To(HaveOccurred())
		g.Expect(err.Error()).To(ContainSubstring("namespaces \"openshift-cluster-samples-operator\" not found"))
	})
}

// EnsureInsightsCapabilityDisabled validates the expectations for when InsightsCapability is Disabled
func EnsureInsightsCapabilityDisabled(ctx context.Context, t *testing.T, g Gomega, clients *GuestClients) {
	t.Run("EnsureInsightsCapabilityDisabled", func(t *testing.T) {
		AtLeast(t, Version420)

		_, err := clients.CfgClient.ConfigV1().ClusterOperators().Get(ctx, "insights", metav1.GetOptions{})
		g.Expect(err).To(HaveOccurred())
		g.Expect(err.Error()).To(ContainSubstring("clusteroperators.config.openshift.io \"insights\" not found"))

		// ensure insights resources are not present
		_, err = clients.KubeClient.CoreV1().Namespaces().Get(ctx, "openshift-insights", metav1.GetOptions{})
		g.Expect(err).To(HaveOccurred())
		g.Expect(err.Error()).To(ContainSubstring("namespaces \"openshift-insights\" not found"))
	})
}

// EnsureConsoleCapabilityDisabled validates the expectations for when ConsoleCapability is Disabled
func EnsureConsoleCapabilityDisabled(ctx context.Context, t *testing.T, g Gomega, clients *GuestClients) {
	t.Run("EnsureConsoleCapabilityDisabled", func(t *testing.T) {
		AtLeast(t, Version420)

		_, err := clients.CfgClient.ConfigV1().ClusterOperators().Get(ctx, "console", metav1.GetOptions{})
		g.Expect(err).To(HaveOccurred())
		g.Expect(err.Error()).To(ContainSubstring("clusteroperators.config.openshift.io \"console\" not found"))

		// ensure console resources are not present
		_, err = clients.KubeClient.CoreV1().Namespaces().Get(ctx, "openshift-console", metav1.GetOptions{})
		g.Expect(err).To(HaveOccurred())
		g.Expect(err.Error()).To(ContainSubstring("namespaces \"openshift-console\" not found"))
	})
}

// EnsureNodeTuningCapabilityDisabled validates the expectations for when NodeTuningCapability is Disabled
func EnsureNodeTuningCapabilityDisabled(ctx context.Context, t *testing.T, clients *GuestClients, mgmtClient crclient.Client, hostedCluster *hyperv1.HostedCluster) {
	t.Run("EnsureNodeTuningCapabilityDisabled", func(t *testing.T) {
		AtLeast(t, Version420)
		g := NewWithT(t)
		// Check guest cluster - node-tuning cluster operator should not exist
		_, err := clients.CfgClient.ConfigV1().ClusterOperators().Get(ctx, "node-tuning", metav1.GetOptions{})
		g.Expect(err).To(HaveOccurred())
		g.Expect(err.Error()).To(ContainSubstring("clusteroperators.config.openshift.io \"node-tuning\" not found"))

		// Check guest cluster - openshift-cluster-node-tuning-operator namespace should not exist
		_, err = clients.KubeClient.CoreV1().Namespaces().Get(ctx, "openshift-cluster-node-tuning-operator", metav1.GetOptions{})
		g.Expect(err).To(HaveOccurred())
		g.Expect(err.Error()).To(ContainSubstring("namespaces \"openshift-cluster-node-tuning-operator\" not found"))

		// Get guest cluster CR client for custom resource checks
		guestClient := WaitForGuestClient(t, ctx, mgmtClient, hostedCluster)

		// Check guest cluster - Tuned resource type should not exist (equivalent to: oc get tuned)
		// Expected error: "the server doesn't have a resource type 'tuned'"
		t.Log("Checking that Tuned resource type does not exist in guest cluster")
		tunedList := &unstructured.UnstructuredList{}
		tunedList.SetGroupVersionKind(schema.GroupVersionKind{
			Group:   "tuned.openshift.io",
			Version: "v1",
			Kind:    "TunedList",
		})
		err = guestClient.List(ctx, tunedList)
		g.Expect(err).To(HaveOccurred(), "expected error when trying to list Tuned objects")
		g.Expect(meta.IsNoMatchError(err)).To(BeTrue(), "expected NoMatchError indicating 'tuned' resource type doesn't exist, got: %v", err)

		// Check guest cluster - Profile resource type should not exist (equivalent to: oc get profile)
		// Expected error: "the server doesn't have a resource type 'profile'"
		t.Log("Checking that Profile resource type does not exist in guest cluster")
		profileList := &unstructured.UnstructuredList{}
		profileList.SetGroupVersionKind(schema.GroupVersionKind{
			Group:   "tuned.openshift.io",
			Version: "v1",
			Kind:    "ProfileList",
		})
		err = guestClient.List(ctx, profileList)
		g.Expect(err).To(HaveOccurred(), "expected error when trying to list Profile objects")
		g.Expect(meta.IsNoMatchError(err)).To(BeTrue(), "expected NoMatchError indicating 'profile' resource type doesn't exist, got: %v", err)

		// Check guest cluster - no tuned DaemonSet should exist (equivalent to: oc get ds tuned --kubeconfig=hosted-kubeconfig -A)
		t.Log("Checking that no tuned DaemonSet exists in guest cluster")
		tunedDaemonSet := &appsv1.DaemonSet{}
		err = guestClient.Get(ctx, crclient.ObjectKey{
			Namespace: "openshift-cluster-node-tuning-operator",
			Name:      "tuned",
		}, tunedDaemonSet)
		g.Expect(apierrors.IsNotFound(err)).To(BeTrue(), "expected no 'tuned' DaemonSet in guest cluster openshift-cluster-node-tuning-operator namespace")

		// Check guest cluster - no tuned-related ConfigMaps should exist
		t.Log("Checking that no tuned-related ConfigMaps exist in guest cluster")
		var configMapList corev1.ConfigMapList
		err = guestClient.List(ctx, &configMapList)
		if err != nil {
			t.Logf("Failed to list ConfigMaps in guest cluster: %v", err)
		} else {
			for _, cm := range configMapList.Items {
				// Check for ConfigMaps that might be related to node tuning
				if strings.Contains(cm.Name, "tuned") || strings.Contains(cm.Name, "node-tuning") ||
					(cm.Labels != nil && (cm.Labels["tuned.openshift.io/tuned"] != "" || cm.Labels["hypershift.openshift.io/nto-generated-machine-config"] != "")) {
					t.Errorf("Found tuned-related ConfigMap %s/%s in guest cluster when NodeTuning capability is disabled", cm.Namespace, cm.Name)
				}
			}
		}

		hcpNamespace := manifests.HostedControlPlaneNamespace(hostedCluster.Namespace, hostedCluster.Name)

		// Check management cluster - no cluster-node-tuning-operator deployment in HCP namespace
		cntoDeployment := &appsv1.Deployment{}
		err = mgmtClient.Get(ctx, crclient.ObjectKey{
			Namespace: hcpNamespace,
			Name:      "cluster-node-tuning-operator",
		}, cntoDeployment)
		g.Expect(apierrors.IsNotFound(err)).To(BeTrue(), "expected no 'cluster-node-tuning-operator' deployment in management cluster HCP namespace")

		t.Log("NodeTuning capability disabled validation completed successfully")
	})
}

// EnsureIngressCapabilityDisabled validates the expectations for when IngressCapability is Disabled
func EnsureIngressCapabilityDisabled(ctx context.Context, t *testing.T, clients *GuestClients, mgmtClient crclient.Client, hostedCluster *hyperv1.HostedCluster) {
	t.Run("EnsureIngressCapabilityDisabled", func(t *testing.T) {
		AtLeast(t, Version420)
		g := NewWithT(t)

		// Check guest cluster - ingress cluster operator should not exist
		_, err := clients.CfgClient.ConfigV1().ClusterOperators().Get(ctx, "ingress", metav1.GetOptions{})
		g.Expect(err).To(HaveOccurred())
		g.Expect(err.Error()).To(ContainSubstring("clusteroperators.config.openshift.io \"ingress\" not found"))

		// Check guest cluster - openshift-ingress-operator namespace should not exist
		_, err = clients.KubeClient.CoreV1().Namespaces().Get(ctx, "openshift-ingress-operator", metav1.GetOptions{})
		g.Expect(err).To(HaveOccurred())
		g.Expect(err.Error()).To(ContainSubstring("namespaces \"openshift-ingress-operator\" not found"))

		// Check guest cluster - openshift-ingress namespace should not exist
		_, err = clients.KubeClient.CoreV1().Namespaces().Get(ctx, "openshift-ingress", metav1.GetOptions{})
		g.Expect(err).To(HaveOccurred())
		g.Expect(err.Error()).To(ContainSubstring("namespaces \"openshift-ingress\" not found"))

		// Check guest cluster - openshift-ingress-canary namespace should not exist
		_, err = clients.KubeClient.CoreV1().Namespaces().Get(ctx, "openshift-ingress-canary", metav1.GetOptions{})
		g.Expect(err).To(HaveOccurred())
		g.Expect(err.Error()).To(ContainSubstring("namespaces \"openshift-ingress-canary\" not found"))

		// Check management cluster - no ingress-operator deployment in HCP namespace
		hcpNamespace := manifests.HostedControlPlaneNamespace(hostedCluster.Namespace, hostedCluster.Name)
		var deploymentList appsv1.DeploymentList
		err = mgmtClient.List(ctx, &deploymentList, crclient.InNamespace(hcpNamespace), crclient.MatchingLabels{"app": "ingress-operator"})
		g.Expect(err).NotTo(HaveOccurred())
		g.Expect(deploymentList.Items).To(BeEmpty(), "expected no ingress-operator deployment in management cluster HCP namespace")

		// Check management cluster - no ingress-operator pods in HCP namespace
		var podList corev1.PodList
		err = mgmtClient.List(ctx, &podList, crclient.InNamespace(hcpNamespace), crclient.MatchingLabels{"app": "ingress-operator"})
		g.Expect(err).NotTo(HaveOccurred())
		g.Expect(podList.Items).To(BeEmpty(), "expected no ingress-operator pods in management cluster HCP namespace")

		// Check guest cluster - no IngressController resources should exist
		_, err = clients.KubeClient.RESTClient().Get().
			AbsPath("/apis/operator.openshift.io/v1/ingresscontrollers").
			DoRaw(ctx)
		g.Expect(err).To(HaveOccurred())
		g.Expect(err.Error()).To(ContainSubstring("the server could not find the requested resource"), "expected IngressController API to not be available when ingress capability is disabled")

		// Check guest cluster - no DNSRecord resources should exist
		_, err = clients.KubeClient.RESTClient().Get().
			AbsPath("/apis/ingress.operator.openshift.io/v1/dnsrecords").
			DoRaw(ctx)
		g.Expect(err).To(HaveOccurred())
		g.Expect(err.Error()).To(ContainSubstring("the server could not find the requested resource"), "expected DNSRecord API to not be available when ingress capability is disabled")

		// Check guest cluster - no GatewayClass resources should exist
		_, err = clients.KubeClient.RESTClient().Get().
			AbsPath("/apis/gateway.networking.k8s.io/v1/gatewayclasses").
			DoRaw(ctx)
		g.Expect(err).To(HaveOccurred())
		g.Expect(err.Error()).To(ContainSubstring("the server could not find the requested resource"), "expected GatewayClass API to not be available when ingress capability is disabled")

		// Check guest cluster - no Gateway resources should exist
		_, err = clients.KubeClient.RESTClient().Get().
			AbsPath("/apis/gateway.networking.k8s.io/v1/gateways").
			DoRaw(ctx)
		g.Expect(err).To(HaveOccurred())
		g.Expect(err.Error()).To(ContainSubstring("the server could not find the requested resource"), "expected Gateway API to not be available when ingress capability is disabled")

		// Check guest cluster - no HTTPRoute resources should exist
		_, err = clients.KubeClient.RESTClient().Get().
			AbsPath("/apis/gateway.networking.k8s.io/v1/httproutes").
			DoRaw(ctx)
		g.Expect(err).To(HaveOccurred())
		g.Expect(err.Error()).To(ContainSubstring("the server could not find the requested resource"), "expected HTTPRoute API to not be available when ingress capability is disabled")

		// Check guest cluster - no ReferenceGrant resources should exist
		_, err = clients.KubeClient.RESTClient().Get().
			AbsPath("/apis/gateway.networking.k8s.io/v1beta1/referencegrants").
			DoRaw(ctx)
		g.Expect(err).To(HaveOccurred())
		g.Expect(err.Error()).To(ContainSubstring("the server could not find the requested resource"), "expected ReferenceGrant API to not be available when ingress capability is disabled")
	})
}

// runAndCheckPod creates a pod which uses the restricted image and checks if it is running using sleep command.
// It also deletes the pod after it is running.
// Added an arguument shouldFail to check if the pod should fail to run.
func runAndCheckPod(t *testing.T, ctx context.Context, guestClient crclient.Client, imageTag, namespace, name string, shouldFail bool) {
	g := NewWithT(t)
	t.Log("Creating a pod which uses the restricted image")

	// Retry configuration
	const maxRetries = 3
	const retryDelay = 5 * time.Second

	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      fmt.Sprintf("%s-pod", name),
			Namespace: namespace,
		},
		Spec: corev1.PodSpec{
			Containers: []corev1.Container{
				{
					Name:    fmt.Sprintf("%s-container", name),
					Image:   imageTag,
					Command: []string{"sleep", "10m"},
				},
			},
		},
	}

	// Retry loop for pod creation
	var createErr error
	for attempt := 1; attempt <= maxRetries; attempt++ {
		t.Logf("Attempt %d/%d: Creating pod", attempt, maxRetries)

		// Try to create the pod
		createErr = guestClient.Create(ctx, pod)
		if createErr == nil {
			t.Logf("Successfully created pod %s in namespace %s on attempt %d", pod.Name, pod.Namespace, attempt)
			break
		}

		// If this is not the last attempt, log the error and retry
		if attempt < maxRetries {
			t.Logf("Failed to create pod on attempt %d: %v, retrying in %v...", attempt, createErr, retryDelay)
			time.Sleep(retryDelay)

			// Clean up any partially created pod before retrying
			if err := guestClient.Delete(ctx, pod); err != nil && !apierrors.IsNotFound(err) {
				t.Logf("Warning: failed to clean up pod before retry: %v", err)
			}
		}
	}

	// Check if all attempts failed
	if createErr != nil {
		t.Fatalf("Failed to create pod after %d attempts. Last error: %v", maxRetries, createErr)
	}

	t.Logf("Created pod %s in namespace %s", pod.Name, pod.Namespace)
	g.Eventually(func() error {
		pod := &corev1.Pod{}
		err := guestClient.Get(ctx, client.ObjectKey{Name: fmt.Sprintf("%s-pod", name), Namespace: namespace}, pod)
		if err != nil {
			return err
		}
		if shouldFail {
			if pod.Status.ContainerStatuses != nil && pod.Status.ContainerStatuses[0].State.Waiting.Reason == "ImagePullBackOff" {
				return fmt.Errorf("pod is not running")
			}
			return nil
		} else {
			if pod.Status.Phase != corev1.PodRunning {
				return fmt.Errorf("pod is running")
			}
			return nil
		}
	}, 7*time.Minute, 5*time.Second).Should(Succeed(), "pod is not running")

	t.Log("Pod is in the desired state, deleting it now")
	err := guestClient.Delete(ctx, pod)
	g.Expect(err).NotTo(HaveOccurred(), "failed to delete pod")
	t.Log("Deleted the pod")
}

// isCertificateTriggeredRestart checks if a kube-controller-manager restart was triggered by certificate rotation
func isCertificateTriggeredRestart(ctx context.Context, client crclient.Client, pod *corev1.Pod) bool {
	// Get the HostedControlPlane to check for certificate rotation annotations
	hcpList := &hyperv1.HostedControlPlaneList{}
	if err := client.List(ctx, hcpList, crclient.InNamespace(pod.Namespace)); err != nil {
		return false
	}

	for _, hcp := range hcpList.Items {
		// Check if there's a restart annotation that indicates certificate rotation
		if restartAnnotation, ok := hcp.Annotations[hyperv1.RestartDateAnnotation]; ok {
			// Certificate-triggered restarts have "CertHash:" prefix in the restart annotation
			if strings.HasPrefix(restartAnnotation, "CertHash:") {
				return true
			}
		}
	}
	return false
}

// EnsureSecurityContextUID validates that all pods in the control plane namespace have the expected SecurityContext UID.
// TestCreateClusterDefaultSecurityContextUID ensures uniqueness across namespaces.
func EnsureSecurityContextUID(t *testing.T, ctx context.Context, client crclient.Client, hostedCluster *hyperv1.HostedCluster) {
	t.Run("EnsureSecurityContextUID", func(t *testing.T) {
		g := NewWithT(t)

		namespaceName := manifests.HostedControlPlaneNamespace(hostedCluster.Namespace, hostedCluster.Name)
		controlPlaneNamespace := &corev1.Namespace{}
		err := client.Get(ctx, crclient.ObjectKey{Name: namespaceName}, controlPlaneNamespace)
		g.Expect(err).NotTo(HaveOccurred(), "failed to get namespace %s", controlPlaneNamespace)

		uid, ok := controlPlaneNamespace.Annotations["hypershift.openshift.io/default-security-context-uid"]
		g.Expect(ok).To(BeTrue(), "namespace %s missing SCC UID annotation", controlPlaneNamespace.Name)

		expectedUID, err := strconv.ParseInt(uid, 10, 64)
		g.Expect(err).NotTo(HaveOccurred(), "couldn't parse SCC UID", controlPlaneNamespace.Name, uid)

		var podList corev1.PodList
		err = client.List(ctx, &podList, &crclient.ListOptions{Namespace: namespaceName})
		g.Expect(err).NotTo(HaveOccurred(), "failed to list pods in namespace %s", controlPlaneNamespace)

		var errs []string
		for _, pod := range podList.Items {
			// Skip pods that are known exceptions for SecurityContext UID validation
			name := pod.Name
			switch {
			case strings.HasPrefix(name, "azure-disk-csi-driver-controller"),
				strings.HasPrefix(name, "azure-file-csi-driver-controller"),
				strings.HasPrefix(name, "azure-disk-csi-driver-operator"),
				strings.HasPrefix(name, "azure-file-csi-driver-operator"),
				strings.HasPrefix(name, "network-node-identity"),
				strings.HasPrefix(name, "ovnkube-control-plane"):
				continue
			}
			if pod.Spec.SecurityContext == nil || pod.Spec.SecurityContext.RunAsUser == nil || *pod.Spec.SecurityContext.RunAsUser != expectedUID {
				errs = append(errs, fmt.Sprintf("pod %s/%s: RunAsUser %v does not match expected UID %d", pod.Namespace, pod.Name,
					func() interface{} {
						if pod.Spec.SecurityContext == nil || pod.Spec.SecurityContext.RunAsUser == nil {
							return nil
						}
						return *pod.Spec.SecurityContext.RunAsUser
					}(), expectedUID))
			}
		}
		if len(errs) == 0 {
			t.Logf("All %d pods in namespace %s have the expected RunAsUser UID %d", len(podList.Items), namespaceName, expectedUID)
		}
		g.Expect(errs).To(BeEmpty(), "Pods with mismatched RunAsUser:\n%s", strings.Join(errs, "\n"))
	})
}

// EnsureCNOOperatorConfiguration tests that changes to the CNO operator configuration on the HostedCluster are
// properly reflected in the hosted cluster's API and that the CNO doesn't report any errors via HCP conditions.
func EnsureCNOOperatorConfiguration(t *testing.T, ctx context.Context, mgmtClient crclient.Client, guestClient crclient.Client, hostedCluster *hyperv1.HostedCluster) {
	t.Run("EnsureCNOOperatorConfiguration", func(t *testing.T) {
		AtLeast(t, Version420)
		g := NewWithT(t)
		const newJoinSubnet = "100.99.0.0/16"
		const newTransitSwitchSubnet = "100.100.0.0/16"
		// Update the HostedCluster to configure CNO settings
		t.Logf("Updating HostedCluster %s/%s with custom OVN internal subnets", hostedCluster.Namespace, hostedCluster.Name)
		err := UpdateObject(t, ctx, mgmtClient, hostedCluster, func(obj *hyperv1.HostedCluster) {
			if obj.Spec.OperatorConfiguration == nil {
				obj.Spec.OperatorConfiguration = &hyperv1.OperatorConfiguration{}
			}
			if obj.Spec.OperatorConfiguration.ClusterNetworkOperator == nil {
				obj.Spec.OperatorConfiguration.ClusterNetworkOperator = &hyperv1.ClusterNetworkOperatorSpec{}
			}
			if obj.Spec.OperatorConfiguration.ClusterNetworkOperator.OVNKubernetesConfig == nil {
				obj.Spec.OperatorConfiguration.ClusterNetworkOperator.OVNKubernetesConfig = &hyperv1.OVNKubernetesConfig{}
			}
			if obj.Spec.OperatorConfiguration.ClusterNetworkOperator.OVNKubernetesConfig.IPv4 == nil {
				obj.Spec.OperatorConfiguration.ClusterNetworkOperator.OVNKubernetesConfig.IPv4 = &hyperv1.OVNIPv4Config{}
			}
			// Set the custom subnet values.
			obj.Spec.OperatorConfiguration.ClusterNetworkOperator.OVNKubernetesConfig.IPv4.InternalJoinSubnet = newJoinSubnet
			obj.Spec.OperatorConfiguration.ClusterNetworkOperator.OVNKubernetesConfig.IPv4.InternalTransitSwitchSubnet = newTransitSwitchSubnet
		})
		g.Expect(err).NotTo(HaveOccurred(), "failed to update HostedCluster with custom OVN config")
		t.Logf("Validating CNO conditions on HostedControlPlane")
		hcpNamespace := fmt.Sprintf("%s-%s", hostedCluster.Namespace, hostedCluster.Name)
		EventuallyObject(t, ctx, fmt.Sprintf("HostedControlPlane %s/%s to have healthy CNO conditions", hcpNamespace, hostedCluster.Name),
			func(ctx context.Context) (*hyperv1.HostedControlPlane, error) {
				hcp := &hyperv1.HostedControlPlane{}
				err := mgmtClient.Get(ctx, types.NamespacedName{
					Namespace: hcpNamespace,
					Name:      hostedCluster.Name,
				}, hcp)
				return hcp, err
			},
			[]Predicate[*hyperv1.HostedControlPlane]{
				ConditionPredicate[*hyperv1.HostedControlPlane](Condition{
					Type:   "network.operator.openshift.io/Available",
					Status: metav1.ConditionTrue,
				}),
				ConditionPredicate[*hyperv1.HostedControlPlane](Condition{
					Type:   "network.operator.openshift.io/Progressing",
					Status: metav1.ConditionFalse,
				}),
				ConditionPredicate[*hyperv1.HostedControlPlane](Condition{
					Type:   "network.operator.openshift.io/Degraded",
					Status: metav1.ConditionFalse,
				}),
			},
			WithTimeout(10*time.Minute),
		)
		ValidateHostedClusterConditions(t, ctx, mgmtClient, hostedCluster, true, 5*time.Minute)
		// Check that the Network.operator.openshift.io resource in the guest cluster reflects our changes
		EventuallyObject(t, ctx, "Network.operator.openshift.io/cluster in guest cluster to reflect the custom subnet changes",
			func(ctx context.Context) (*operatorv1.Network, error) {
				network := &operatorv1.Network{}
				err := guestClient.Get(ctx, types.NamespacedName{Name: "cluster"}, network)
				return network, err
			},
			[]Predicate[*operatorv1.Network]{
				func(network *operatorv1.Network) (done bool, reasons string, err error) {
					// Validate that OVN-Kubernetes is properly configured
					if network.Spec.DefaultNetwork.Type != operatorv1.NetworkTypeOVNKubernetes {
						return false, fmt.Sprintf("expected network type OVNKubernetes, got %s", network.Spec.DefaultNetwork.Type), nil
					}
					if network.Spec.DefaultNetwork.OVNKubernetesConfig == nil {
						return false, "OVNKubernetesConfig is nil in the reconciled Network CR", nil
					}
					if network.Spec.DefaultNetwork.OVNKubernetesConfig.IPv4 == nil {
						return false, "OVNKubernetesConfig.IPv4 is nil in the reconciled Network CR", nil
					}
					if network.Spec.DefaultNetwork.OVNKubernetesConfig.IPv4.InternalJoinSubnet != newJoinSubnet {
						return false, fmt.Sprintf("expected InternalJoinSubnet to be %s, but got %s", newJoinSubnet, network.Spec.DefaultNetwork.OVNKubernetesConfig.IPv4.InternalJoinSubnet), nil
					}
					if network.Spec.DefaultNetwork.OVNKubernetesConfig.IPv4.InternalTransitSwitchSubnet != newTransitSwitchSubnet {
						return false, fmt.Sprintf("expected InternalTransitSwitchSubnet to be %s, but got %s", newTransitSwitchSubnet, network.Spec.DefaultNetwork.OVNKubernetesConfig.IPv4.InternalTransitSwitchSubnet), nil
					}

					return true, "Successfully validated custom OVN subnets", nil
				},
			},
			WithTimeout(5*time.Minute),
		)
		EventuallyObject(t, ctx, "Network.config.openshift.io/cluster in guest cluster to be available",
			func(ctx context.Context) (*configv1.Network, error) {
				network := &configv1.Network{}
				err := guestClient.Get(ctx, types.NamespacedName{Name: "cluster"}, network)
				return network, err
			},
			[]Predicate[*configv1.Network]{
				func(network *configv1.Network) (done bool, reasons string, err error) {
					if network.Status.NetworkType == "" {
						return false, "NetworkType is not set in status", nil
					}
					if len(network.Status.ClusterNetwork) == 0 {
						return false, "ClusterNetwork is empty in status", nil
					}
					if len(network.Status.ServiceNetwork) == 0 {
						return false, "ServiceNetwork is empty in status", nil
					}
					return true, "", nil
				},
			},
			WithTimeout(3*time.Minute),
		)
	})
}

// ValidateConfigurationStatus validates that the HCP and HC configuration status
// matches the Authentication resource status from the hosted cluster
func ValidateConfigurationStatus(t *testing.T, ctx context.Context, mgmtClient crclient.Client, guestClient crclient.Client, hostedCluster *hyperv1.HostedCluster) {
	t.Run("ValidateConfigurationStatus", func(t *testing.T) {
		// Configuration status was added in 4.21
		AtLeast(t, Version421)
		g := NewWithT(t)

		// Wait for both HCP and HC configuration status to be populated and validate consistency
		hcpName := hostedCluster.Name
		hcpNamespace := manifests.HostedControlPlaneNamespace(hostedCluster.Namespace, hostedCluster.Name)

		g.Eventually(func() error {
			// Get Authentication resource from hosted cluster
			var guestAuth configv1.Authentication
			if err := guestClient.Get(ctx, crclient.ObjectKey{Name: "cluster"}, &guestAuth); err != nil {
				return fmt.Errorf("failed to get Authentication resource from hosted cluster: %w", err)
			}

			// Check HCP configuration status
			var hcp hyperv1.HostedControlPlane
			if err := mgmtClient.Get(ctx, crclient.ObjectKey{Name: hcpName, Namespace: hcpNamespace}, &hcp); err != nil {
				return fmt.Errorf("failed to get HCP: %w", err)
			}
			if hcp.Status.Configuration == nil {
				return fmt.Errorf("HCP configuration status not populated yet")
			}

			// Check HC configuration status
			var hc hyperv1.HostedCluster
			if err := mgmtClient.Get(ctx, crclient.ObjectKeyFromObject(hostedCluster), &hc); err != nil {
				return fmt.Errorf("failed to get HC: %w", err)
			}
			if hc.Status.Configuration == nil {
				return fmt.Errorf("HC configuration status not populated yet")
			}

			// Validate HCP authentication status matches guest cluster
			if !reflect.DeepEqual(hcp.Status.Configuration.Authentication, guestAuth.Status) {
				return fmt.Errorf("HCP authentication status doesn't match guest cluster Authentication resource")
			}

			// Validate HC authentication status matches guest cluster
			if !reflect.DeepEqual(hc.Status.Configuration.Authentication, guestAuth.Status) {
				return fmt.Errorf("HC authentication status doesn't match guest cluster Authentication resource")
			}

			// Validate HCP and HC have consistent configuration status
			if !reflect.DeepEqual(hcp.Status.Configuration.Authentication, hc.Status.Configuration.Authentication) {
				return fmt.Errorf("HCP and HC authentication status are inconsistent")
			}

			return nil
		}, 10*time.Minute, 10*time.Second).Should(Succeed(), "Configuration status should be consistent across HCP, HC, and guest cluster")

		t.Logf("Successfully validated configuration authentication status consistency across HCP, HC, and guest cluster")
	})
}
