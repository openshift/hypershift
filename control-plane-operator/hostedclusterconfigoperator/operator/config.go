package operator

import (
	"context"
	"fmt"
	"os"
	"time"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	"github.com/openshift/hypershift/control-plane-operator/hostedclusterconfigoperator/api"
	"github.com/openshift/hypershift/support/config"
	"github.com/openshift/hypershift/support/labelenforcingclient"
	"github.com/openshift/hypershift/support/releaseinfo"
	"github.com/openshift/hypershift/support/releaseinfo/registryclient"
	"github.com/openshift/hypershift/support/upsert"
	"github.com/openshift/hypershift/support/util"

	configv1 "github.com/openshift/api/config/v1"
	imageregistryv1 "github.com/openshift/api/imageregistry/v1"
	operatorv1 "github.com/openshift/api/operator/v1"

	admissionregistrationv1 "k8s.io/api/admissionregistration/v1"
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
	metricsserver "sigs.k8s.io/controller-runtime/pkg/metrics/server"

	"github.com/go-logr/logr"
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

type ControllerSetupFunc func(context.Context, *HostedClusterConfigOperatorConfig) error

type HostedClusterConfigOperatorConfig struct {
	Manager                      ctrl.Manager
	Config                       *rest.Config
	TargetConfig                 *rest.Config
	KubevirtInfraConfig          *rest.Config
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
	EnableCIDebugOutput          bool
	ListDigestsFN                registryclient.DigestListerFN
	ImageMetaDataProvider        util.RegistryClientImageMetadataProvider

	kubeClient kubeclient.Interface
}

func Mgr(cfg, cpConfig *rest.Config, namespace string) ctrl.Manager {
	cfg.UserAgent = config.HCCOUserAgent
	allSelector := cache.ByObject{
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
		Metrics: metricsserver.Options{
			BindAddress: "0.0.0.0:8080",
		},

		NewClient: func(config *rest.Config, options client.Options) (client.Client, error) {
			client, err := client.New(config, options)
			if err != nil {
				return nil, err
			}
			return labelenforcingclient.New(
				client,
				map[string]string{cacheLabelSelectorKey: cacheLabelSelectorValue},
			), nil
		},
		Cache: cache.Options{
			DefaultLabelSelector: cacheLabelSelector(),
			ByObject: map[client.Object]cache.ByObject{
				&corev1.Namespace{}:           allSelector,
				&configv1.Infrastructure{}:    allSelector,
				&configv1.DNS{}:               allSelector,
				&configv1.Ingress{}:           allSelector,
				&operatorv1.Network{}:         allSelector,
				&configv1.Network{}:           allSelector,
				&configv1.Proxy{}:             allSelector,
				&configv1.Build{}:             allSelector,
				&configv1.Image{}:             allSelector,
				&configv1.Project{}:           allSelector,
				&configv1.ClusterVersion{}:    allSelector,
				&configv1.FeatureGate{}:       allSelector,
				&configv1.ClusterOperator{}:   allSelector,
				&configv1.OperatorHub{}:       allSelector,
				&operatorv1.CloudCredential{}: allSelector,
				&admissionregistrationv1.ValidatingWebhookConfiguration{}: allSelector,
				&admissionregistrationv1.MutatingWebhookConfiguration{}:   allSelector,
				&operatorv1.Storage{}:               allSelector,
				&operatorv1.CSISnapshotController{}: allSelector,
				&operatorv1.ClusterCSIDriver{}:      allSelector,

				// Needed for inplace upgrader.
				&corev1.Node{}: allSelector,

				// Needed for resource cleanup
				&corev1.Service{}:               allSelector,
				&corev1.PersistentVolume{}:      allSelector,
				&corev1.PersistentVolumeClaim{}: allSelector,
				&operatorv1.IngressController{}: allSelector,
				&imageregistryv1.Config{}:       allSelector,
			},
		},
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
	for controllerName, setupFunc := range c.ControllerFuncs {
		c.Logger.Info("setting up controller", "controller", controllerName)
		if err := setupFunc(ctx, c); err != nil {
			return fmt.Errorf("cannot setup controller %s: %v", controllerName, err)
		}
	}
	return c.Manager.Start(ctx)
}
