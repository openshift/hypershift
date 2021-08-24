package operator

import (
	"context"
	"fmt"
	"os"

	"github.com/go-logr/logr"

	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/informers"
	kubeclient "k8s.io/client-go/kubernetes"
	kubescheme "k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/manager"

	configclient "github.com/openshift/client-go/config/clientset/versioned"
	configinformers "github.com/openshift/client-go/config/informers/externalversions"

	common "github.com/openshift/hypershift/hosted-cluster-config-operator/controllers"
)

type ControllerSetupFunc func(*HostedClusterConfigOperatorConfig) error

func NewHostedClusterConfigOperatorConfig(targetKubeconfig, namespace string, initialCA []byte, versions map[string]string, controllers []string, controllerFuncs map[string]ControllerSetupFunc) *HostedClusterConfigOperatorConfig {
	return &HostedClusterConfigOperatorConfig{
		targetKubeconfig: targetKubeconfig,
		namespace:        namespace,
		initialCA:        initialCA,
		controllers:      controllers,
		controllerFuncs:  controllerFuncs,
		versions:         versions,
	}
}

type HostedClusterConfigOperatorConfig struct {
	manager          ctrl.Manager
	config           *rest.Config
	targetConfig     *rest.Config
	targetKubeClient kubeclient.Interface
	kubeClient       kubeclient.Interface
	logger           logr.Logger
	scheme           *runtime.Scheme

	versions            map[string]string
	targetKubeconfig    string
	namespace           string
	initialCA           []byte
	controllers         []string
	controllerFuncs     map[string]ControllerSetupFunc
	namespacedInformers map[string]informers.SharedInformerFactory
}

func (c *HostedClusterConfigOperatorConfig) Scheme() *runtime.Scheme {
	if c.scheme == nil {
		c.scheme = runtime.NewScheme()
		kubescheme.AddToScheme(c.scheme)
	}
	return c.scheme
}

func (c *HostedClusterConfigOperatorConfig) Manager() ctrl.Manager {
	if c.manager == nil {
		var err error
		c.manager, err = ctrl.NewManager(c.TargetConfig(), ctrl.Options{
			Scheme:                  c.Scheme(),
			LeaderElection:          true,
			LeaderElectionNamespace: c.TargetNamespace(),
			LeaderElectionID:        "hypershift-operator",
			Namespace:               c.TargetNamespace(),
		})
		if err != nil {
			c.Fatal(err, "failed to create controller manager")
		}
	}
	return c.manager
}

func (c *HostedClusterConfigOperatorConfig) Namespace() string {
	return c.namespace
}

func (c *HostedClusterConfigOperatorConfig) TargetNamespace() string {
	return "openshift-config-managed"
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
	if c.targetConfig == nil {
		var err error
		c.targetConfig, err = clientcmd.NewNonInteractiveDeferredLoadingClientConfig(
			&clientcmd.ClientConfigLoadingRules{ExplicitPath: c.targetKubeconfig},
			&clientcmd.ConfigOverrides{}).ClientConfig()
		if err != nil {
			c.Fatal(err, "cannot get the target cluster's rest config")
		}
	}
	return c.targetConfig
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
		c.kubeClient, err = kubeclient.NewForConfig(c.Config())
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
