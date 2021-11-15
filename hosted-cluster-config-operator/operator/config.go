package operator

import (
	"context"
	"fmt"
	"os"

	"github.com/go-logr/logr"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/informers"
	kubeclient "k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/cache"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/cluster"
	"sigs.k8s.io/controller-runtime/pkg/manager"

	configclient "github.com/openshift/client-go/config/clientset/versioned"
	configinformers "github.com/openshift/client-go/config/informers/externalversions"

	hyperv1 "github.com/openshift/hypershift/api/v1alpha1"
	common "github.com/openshift/hypershift/hosted-cluster-config-operator/controllers"
	"github.com/openshift/hypershift/support/upsert"
)

const (
	cacheLabelSelectorKey   = "hypershift.io/managed"
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

func NewHostedClusterConfigOperatorConfig(targetKubeconfig, namespace string, initialCA []byte, versions map[string]string, controllers []string, controllerFuncs map[string]ControllerSetupFunc, enableCIDebugOutput bool, platformType hyperv1.PlatformType, clusterSignerCA []byte, haProxyImage string, apiServerExternalAddress string, apiServerExternalPort int, apiServerInternalAddress string, apiServerInternalPort int) *HostedClusterConfigOperatorConfig {
	cfg := cfgFromFile(targetKubeconfig)
	mgr := mgr(cfg)
	return &HostedClusterConfigOperatorConfig{
		TargetCreateOrUpdateProvider: &labelEnforcingUpsertProvider{
			upstream:  upsert.New(enableCIDebugOutput),
			apiReader: mgr.GetAPIReader(),
		},
		targetConfig:    cfg,
		manager:         mgr,
		namespace:       namespace,
		initialCA:       initialCA,
		clusterSignerCA: clusterSignerCA,
		controllers:     controllers,
		controllerFuncs: controllerFuncs,
		versions:        versions,
		PlatformType:    platformType,

		HAProxyImage:             haProxyImage,
		APIServerExternalAddress: apiServerExternalAddress,
		APIServerExternalPort:    apiServerExternalPort,
		APIServerInternalAddress: apiServerInternalAddress,
		APIServerInternalPort:    apiServerInternalPort,
	}
}

type HostedClusterConfigOperatorConfig struct {
	manager                      ctrl.Manager
	config                       *rest.Config
	targetConfig                 *rest.Config
	targetKubeClient             kubeclient.Interface
	TargetCreateOrUpdateProvider upsert.CreateOrUpdateProvider
	kubeClient                   kubeclient.Interface
	logger                       logr.Logger

	versions            map[string]string
	namespace           string
	initialCA           []byte
	clusterSignerCA     []byte
	controllers         []string
	PlatformType        hyperv1.PlatformType
	controllerFuncs     map[string]ControllerSetupFunc
	namespacedInformers map[string]informers.SharedInformerFactory

	HAProxyImage             string
	APIServerExternalAddress string
	APIServerExternalPort    int
	APIServerInternalAddress string
	APIServerInternalPort    int
}

func (c *HostedClusterConfigOperatorConfig) Scheme() *runtime.Scheme {
	return c.manager.GetScheme()
}

func (c *HostedClusterConfigOperatorConfig) Manager() ctrl.Manager {
	return c.manager
}

func mgr(cfg *rest.Config) ctrl.Manager {
	cfg.UserAgent = "hosted-cluster-config-operator-manager"
	mgr, err := ctrl.NewManager(cfg, ctrl.Options{
		LeaderElection:          true,
		LeaderElectionNamespace: "openshift-config-managed",
		LeaderElectionID:        "hypershift-operator",
		NewClient: func(cache cache.Cache, config *rest.Config, options client.Options, uncachedObjects ...client.Object) (client.Client, error) {
			client, err := cluster.DefaultNewClient(cache, config, options, uncachedObjects...)
			if err != nil {
				return nil, err
			}
			return &labelEnforcingClient{
				Client: client,
				labels: map[string]string{cacheLabelSelectorKey: cacheLabelSelectorValue},
			}, nil
		},
		NewCache: cache.BuilderWithOptions(cache.Options{
			SelectorsByObject: cache.SelectorsByObject{
				// TODO @alvaroaleman: We want the same selector for all object types
				// but controller-runtime doesn't support that yet. Change  this to
				// use a default for everything once we have https://github.com/kubernetes-sigs/controller-runtime/pull/1710
				&corev1.ConfigMap{}: {Label: cacheLabelSelector()},
				&corev1.Secret{}:    {Label: cacheLabelSelector()},
				&appsv1.DaemonSet{}: {Label: cacheLabelSelector()},
			},
		}),
	})
	if err != nil {
		panic(fmt.Sprintf("failed t create controller manager: %v", err))
	}
	return mgr
}

func (c *HostedClusterConfigOperatorConfig) Namespace() string {
	return c.namespace
}

func (c *HostedClusterConfigOperatorConfig) Config() *rest.Config {
	if c.config == nil {
		c.config = ctrl.GetConfigOrDie()
	}
	return c.config
}

func (c *HostedClusterConfigOperatorConfig) Logger() logr.Logger {
	if c.logger == nil {
		c.logger = ctrl.Log.WithName("hypershift-operator")
	}
	return c.logger
}

func (c *HostedClusterConfigOperatorConfig) TargetConfig() *rest.Config {
	return c.targetConfig
}

func cfgFromFile(path string) *rest.Config {
	cfg, err := clientcmd.NewNonInteractiveDeferredLoadingClientConfig(
		&clientcmd.ClientConfigLoadingRules{ExplicitPath: path},
		&clientcmd.ConfigOverrides{}).ClientConfig()
	if err != nil {
		panic(fmt.Sprintf("failed to construct kubeconfig from path %s: %v", path, err))
	}
	return cfg
}

func (c *HostedClusterConfigOperatorConfig) TargetKubeClient() kubeclient.Interface {
	if c.targetKubeClient == nil {
		var err error
		c.targetKubeClient, err = kubeclient.NewForConfig(c.TargetConfig())
		if err != nil {
			c.Fatal(err, "cannot get target kube client")
		}
	}
	return c.targetKubeClient
}

func (c *HostedClusterConfigOperatorConfig) TargetConfigClient() configclient.Interface {
	client, err := configclient.NewForConfig(c.TargetConfig())
	if err != nil {
		c.Fatal(err, "cannot get target config client")
	}
	return client
}

func (c *HostedClusterConfigOperatorConfig) TargetConfigInformers() configinformers.SharedInformerFactory {
	informerFactory := configinformers.NewSharedInformerFactory(c.TargetConfigClient(), common.DefaultResync)
	c.Manager().Add(manager.RunnableFunc(func(ctx context.Context) error {
		informerFactory.Start(ctx.Done())
		return nil
	}))
	return informerFactory
}

func (c *HostedClusterConfigOperatorConfig) TargetKubeInformersForNamespace(namespace string) informers.SharedInformerFactory {
	informer, exists := c.namespacedInformers[namespace]
	if !exists {
		informer = informers.NewSharedInformerFactoryWithOptions(c.TargetKubeClient(), common.DefaultResync, informers.WithNamespace(namespace))
		if c.namespacedInformers == nil {
			c.namespacedInformers = map[string]informers.SharedInformerFactory{}
		}
		c.namespacedInformers[namespace] = informer
		c.Manager().Add(manager.RunnableFunc(func(ctx context.Context) error {
			informer.Start(ctx.Done())
			return nil
		}))
	}
	return informer
}

func (c *HostedClusterConfigOperatorConfig) KubeClient() kubeclient.Interface {
	if c.kubeClient == nil {
		var err error
		config := c.Config()
		config.UserAgent = "hosted-cluster-config-operator-kubeclient"
		c.kubeClient, err = kubeclient.NewForConfig(config)
		if err != nil {
			c.Fatal(err, "cannot get management kube client")
		}
	}
	return c.kubeClient
}

func (c *HostedClusterConfigOperatorConfig) Versions() map[string]string {
	return c.versions
}

func (c *HostedClusterConfigOperatorConfig) InitialCA() string {
	return string(c.initialCA)
}

func (c *HostedClusterConfigOperatorConfig) ClusterSignerCA() string {
	return string(c.clusterSignerCA)
}

func (c *HostedClusterConfigOperatorConfig) Fatal(err error, msg string) {
	c.Logger().Error(err, msg)
	os.Exit(1)
}

func (c *HostedClusterConfigOperatorConfig) Start(ctx context.Context) error {
	for _, controllerName := range c.controllers {
		setupFunc, ok := c.controllerFuncs[controllerName]
		if !ok {
			return fmt.Errorf("unknown controller specified: %s", controllerName)
		}
		if err := setupFunc(c); err != nil {
			return fmt.Errorf("cannot setup controller %s: %v", controllerName, err)
		}
	}
	// TODO: receive a context from the caller
	return c.Manager().Start(ctx)
}
