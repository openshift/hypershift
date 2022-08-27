package operator

import (
	"context"
	"fmt"
	"os"
	"time"

	"github.com/go-logr/logr"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/labels"
	kubeclient "k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/cache"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/cluster"
	"sigs.k8s.io/controller-runtime/pkg/healthz"

	configv1 "github.com/openshift/api/config/v1"
	imageregistryv1 "github.com/openshift/api/imageregistry/v1"
	operatorv1 "github.com/openshift/api/operator/v1"
	hyperv1 "github.com/openshift/hypershift/api/v1alpha1"
	"github.com/openshift/hypershift/control-plane-operator/hostedclusterconfigoperator/api"
	"github.com/openshift/hypershift/support/labelenforcingclient"
	"github.com/openshift/hypershift/support/releaseinfo"
	"github.com/openshift/hypershift/support/upsert"
)

const (
	cacheLabelSelectorKey   = "hypershift.openshift.io/managed"
	cacheLabelSelectorValue = "true"
)

func cacheLabelSelector() labels.Selector {
	selector, err := labels.ValidatedSelectorFromSet(labels.Set{cacheLabelSelectorKey: cacheLabelSelectorValue})
	if err != nil {
		panic(err)
	}
	return selector
}

type ControllerSetupFunc func(*HostedClusterConfigOperatorConfig) error

type HostedClusterConfigOperatorConfig struct {
	Manager                      ctrl.Manager
	Config                       *rest.Config
	TargetConfig                 *rest.Config
	TargetCreateOrUpdateProvider upsert.CreateOrUpdateProvider
	CPCluster                    cluster.Cluster
	Logger                       logr.Logger
	Versions                     map[string]string
	HCPName                      string
	Namespace                    string
	InitialCA                    string
	ClusterSignerCA              string
	Controllers                  []string
	PlatformType                 hyperv1.PlatformType
	ControllerFuncs              map[string]ControllerSetupFunc
	ReleaseProvider              releaseinfo.Provider
	KonnectivityAddress          string
	KonnectivityPort             int32
	OAuthAddress                 string
	OAuthPort                    int32
	OperateOnReleaseImage        string

	kubeClient kubeclient.Interface
}

func Mgr(cfg, cpConfig *rest.Config, namespace string) ctrl.Manager {
	cfg.UserAgent = "hosted-cluster-config-operator-manager"
	allSelector := cache.ObjectSelector{
		Label: labels.Everything(),
	}
	leaseDuration := time.Second * 60
	renewDeadline := time.Second * 40
	retryPeriod := time.Second * 15
	mgr, err := ctrl.NewManager(cfg, ctrl.Options{
		LeaderElection:                true,
		LeaderElectionResourceLock:    "leases",
		LeaderElectionNamespace:       namespace,
		LeaderElectionConfig:          cpConfig,
		LeaderElectionID:              "hosted-cluster-config-operator-leader-elect",
		LeaderElectionReleaseOnCancel: true,
		LeaseDuration:                 &leaseDuration,
		RenewDeadline:                 &renewDeadline,
		RetryPeriod:                   &retryPeriod,
		HealthProbeBindAddress:        ":6060",
		MetricsBindAddress:            "0.0.0.0:8080",

		NewClient: func(cache cache.Cache, config *rest.Config, options client.Options, uncachedObjects ...client.Object) (client.Client, error) {
			client, err := cluster.DefaultNewClient(cache, config, options, uncachedObjects...)
			if err != nil {
				return nil, err
			}
			return labelenforcingclient.New(
				client,
				map[string]string{cacheLabelSelectorKey: cacheLabelSelectorValue},
			), nil
		},
		NewCache: cache.BuilderWithOptions(cache.Options{
			SelectorsByObject: cache.SelectorsByObject{
				&corev1.Namespace{}:         allSelector,
				&configv1.Infrastructure{}:  allSelector,
				&configv1.DNS{}:             allSelector,
				&configv1.Ingress{}:         allSelector,
				&operatorv1.Network{}:       allSelector,
				&configv1.Network{}:         allSelector,
				&configv1.Proxy{}:           allSelector,
				&configv1.Build{}:           allSelector,
				&configv1.Image{}:           allSelector,
				&configv1.Project{}:         allSelector,
				&configv1.ClusterVersion{}:  allSelector,
				&configv1.FeatureGate{}:     allSelector,
				&configv1.ClusterOperator{}: allSelector,

				// Needed for inplace upgrader.
				&corev1.Node{}: allSelector,

				// Needed for resource cleanup
				&corev1.Service{}:               allSelector,
				&corev1.PersistentVolume{}:      allSelector,
				&corev1.PersistentVolumeClaim{}: allSelector,
				&operatorv1.IngressController{}: allSelector,
				&imageregistryv1.Config{}:       allSelector,
			},
			DefaultSelector: cache.ObjectSelector{Label: cacheLabelSelector()},
		}),
		Scheme: api.Scheme,
	})
	if err != nil {
		panic(fmt.Sprintf("failed to create controller manager: %v", err))
	}

	if err := mgr.AddHealthzCheck("healthz", healthz.Ping); err != nil {
		panic(fmt.Sprintf("unable to set up health check: %v", err))
	}
	if err := mgr.AddReadyzCheck("readyz", healthz.Ping); err != nil {
		panic(fmt.Sprintf("unable to set up ready check: %v", err))
	}

	return mgr
}

func CfgFromFile(path string) *rest.Config {
	cfg, err := clientcmd.NewNonInteractiveDeferredLoadingClientConfig(
		&clientcmd.ClientConfigLoadingRules{ExplicitPath: path},
		&clientcmd.ConfigOverrides{}).ClientConfig()
	if err != nil {
		panic(fmt.Sprintf("failed to construct kubeconfig from path %s: %v", path, err))
	}
	return cfg
}

func (c *HostedClusterConfigOperatorConfig) KubeClient() kubeclient.Interface {
	if c.kubeClient == nil {
		var err error
		c.kubeClient, err = kubeclient.NewForConfig(c.Config)
		if err != nil {
			c.Fatal(err, "cannot get management kube client")
		}
	}
	return c.kubeClient
}

func (c *HostedClusterConfigOperatorConfig) Fatal(err error, msg string) {
	c.Logger.Error(err, msg)
	os.Exit(1)
}

func (c *HostedClusterConfigOperatorConfig) Start(ctx context.Context) error {
	for _, controllerName := range c.Controllers {
		setupFunc, ok := c.ControllerFuncs[controllerName]
		if !ok {
			return fmt.Errorf("unknown controller specified: %s", controllerName)
		}
		if err := setupFunc(c); err != nil {
			return fmt.Errorf("cannot setup controller %s: %v", controllerName, err)
		}
	}
	return c.Manager.Start(ctx)
}
