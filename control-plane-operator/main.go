package main

import (
	"context"
	"fmt"
	"os"

	"github.com/openshift/hypershift/control-plane-operator/controllers/hostedapicache"
	"github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/manifests"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/spf13/cobra"
	appsv1client "k8s.io/client-go/kubernetes/typed/apps/v1"

	_ "k8s.io/client-go/plugin/pkg/client/auth/gcp"

	hyperapi "github.com/openshift/hypershift/control-plane-operator/api"
	"github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane"
	"github.com/openshift/hypershift/support/releaseinfo"

	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/cache"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"
	// +kubebuilder:scaffold:imports
)

var (
	setupLog = ctrl.Log.WithName("setup")
)

func main() {
	cmd := &cobra.Command{
		Use: "control-plane-operator",
		Run: func(cmd *cobra.Command, args []string) {
			cmd.Help()
			os.Exit(1)
		},
	}
	cmd.AddCommand(NewStartCommand())

	if err := cmd.Execute(); err != nil {
		fmt.Fprintf(os.Stderr, "%v\n", err)
		os.Exit(1)
	}
}

const (
	// FIXME: Set to upstream image when DNS resolution is fixed for etcd service
	etcdOperatorImage = "quay.io/hypershift/etcd-operator:v0.9.4-patched"
	// TODO: Include konnectivity image in release payload
	konnectivityServerImage = "registry.ci.openshift.org/hypershift/apiserver-network-proxy:latest"
	konnectivityAgentImage  = "registry.ci.openshift.org/hypershift/apiserver-network-proxy:latest"
)

func NewStartCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "run",
		Short: "Runs the HyperShift Control Plane Operator",
	}

	var namespace string
	var deploymentName string
	var metricsAddr string
	var enableLeaderElection bool
	var hostedClusterConfigOperatorImage string
	var inCluster bool

	cmd.Flags().StringVar(&namespace, "namespace", "", "The namespace this operator lives in (required)")
	cmd.Flags().StringVar(&deploymentName, "deployment-name", "", "The name of the deployment of this operator")
	cmd.Flags().StringVar(&metricsAddr, "metrics-addr", "0", "The address the metric endpoint binds to.")
	cmd.Flags().BoolVar(&enableLeaderElection, "enable-leader-election", false,
		"Enable leader election for controller manager. "+
			"Enabling this will ensure there is only one active controller manager.")
	cmd.Flags().StringVar(&hostedClusterConfigOperatorImage, "hosted-cluster-config-operator-image", "", "A specific operator image. (defaults to match this operator if running in a deployment)")
	cmd.Flags().BoolVar(&inCluster, "in-cluster", true, "If false, the operator will be assumed to be running outside a kube "+
		"cluster and will make some internal decisions to ease local development (e.g. using external endpoints where possible"+
		"to avoid assuming access to the service network)")

	cmd.MarkFlagRequired("namespace")

	cmd.Run = func(cmd *cobra.Command, args []string) {
		ctrl.SetLogger(zap.New(zap.UseDevMode(true)))
		ctx := ctrl.SetupSignalHandler()

		mgr, err := ctrl.NewManager(ctrl.GetConfigOrDie(), ctrl.Options{
			Scheme:             hyperapi.Scheme,
			MetricsBindAddress: metricsAddr,
			Port:               9443,
			LeaderElection:     enableLeaderElection,
			LeaderElectionID:   "b2ed43cb.hypershift.openshift.io",
			Namespace:          namespace,
			// Use a non-caching client everywhere. The default split client does not
			// promise to invalidate the cache during writes (nor does it promise
			// sequential create/get coherence), and we have code which (probably
			// incorrectly) assumes a get immediately following a create/update will
			// return the updated resource. All client consumers will need audited to
			// ensure they are tolerant of stale data (or we need a cache or client that
			// makes stronger coherence guarantees).
			NewClient: uncachedNewClient,
		})
		if err != nil {
			setupLog.Error(err, "unable to start manager")
			os.Exit(1)
		}

		// For now, since the hosted cluster config operator is treated like any other
		// release payload component but isn't actually part of a release payload,
		// enable the user to specify an image directly as a flag, and otherwise
		// try and detect the control plane operator's image to use instead.
		kubeClient, err := kubernetes.NewForConfig(mgr.GetConfig())
		if err != nil {
			setupLog.Error(err, "unable to create kube client")
			os.Exit(1)
		}
		lookupOperatorImage := func(deployments appsv1client.DeploymentInterface, name string) (string, error) {
			if len(hostedClusterConfigOperatorImage) > 0 {
				setupLog.Info("using operator image from arguments")
				return hostedClusterConfigOperatorImage, nil
			}
			deployment, err := deployments.Get(context.TODO(), name, metav1.GetOptions{})
			if err != nil {
				return "", fmt.Errorf("failed to get operator deployment: %w", err)
			}
			for _, container := range deployment.Spec.Template.Spec.Containers {
				// TODO: could use downward API for this too, overkill?
				if container.Name == "control-plane-operator" {
					setupLog.Info("using operator image from deployment")
					return container.Image, nil
				}
			}
			return "", fmt.Errorf("couldn't locate operator container on deployment")
		}
		hostedClusterConfigOperatorImage, err := lookupOperatorImage(kubeClient.AppsV1().Deployments(namespace), deploymentName)
		if err != nil {
			setupLog.Error(err, fmt.Sprintf("failed to find operator image: %s", err), "controller", "hosted-control-plane")
			os.Exit(1)
		}
		setupLog.Info("using operator image", "operator-image", hostedClusterConfigOperatorImage)

		releaseProvider := &releaseinfo.StaticProviderDecorator{
			Delegate: &releaseinfo.CachedProvider{
				Inner: &releaseinfo.RegistryClientProvider{},
				Cache: map[string]*releaseinfo.ReleaseImage{},
			},
			ComponentImages: map[string]string{
				"hosted-cluster-config-operator": hostedClusterConfigOperatorImage,
				"etcd-operator":                  etcdOperatorImage,
				"konnectivity-server":            konnectivityServerImage,
				"konnectivity-agent":             konnectivityAgentImage,
			},
		}

		var hostedKubeconfigScope manifests.KubeconfigScope
		if inCluster {
			hostedKubeconfigScope = manifests.KubeconfigScopeLocal
		} else {
			hostedKubeconfigScope = manifests.KubeconfigScopeExternal
		}
		apiCacheController, err := hostedapicache.RegisterHostedAPICacheReconciler(mgr, ctx, ctrl.Log.WithName("hosted-api-cache"), hostedKubeconfigScope)
		if err != nil {
			setupLog.Error(err, "failed to create controller", "controller", "hosted-api-cache")
			os.Exit(1)
		}

		if err := (&hostedcontrolplane.HostedControlPlaneReconciler{
			Client:          mgr.GetClient(),
			ReleaseProvider: releaseProvider,
			HostedAPICache:  apiCacheController.GetCache(),
		}).SetupWithManager(mgr); err != nil {
			setupLog.Error(err, "unable to create controller", "controller", "hosted-control-plane")
			os.Exit(1)
		}

		setupLog.Info("starting manager")
		if err := mgr.Start(ctx); err != nil {
			setupLog.Error(err, "problem running manager")
			os.Exit(1)
		}
	}

	return cmd
}

func uncachedNewClient(_ cache.Cache, config *rest.Config, options client.Options, uncachedObjects ...client.Object) (client.Client, error) {
	c, err := client.New(config, options)
	if err != nil {
		return nil, err
	}
	return c, nil
}
