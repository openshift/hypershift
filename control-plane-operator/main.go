package main

import (
	"context"
	"fmt"
	"os"
	"time"

	availabilityprober "github.com/openshift/hypershift/availability-prober"
	"github.com/openshift/hypershift/control-plane-operator/controllers/awsprivatelink"
	"github.com/openshift/hypershift/control-plane-operator/controllers/hostedapicache"
	"github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/manifests"
	"github.com/openshift/hypershift/control-plane-operator/hostedclusterconfigoperator"
	ignitionserver "github.com/openshift/hypershift/ignition-server"
	konnectivitysocks5proxy "github.com/openshift/hypershift/konnectivity-socks5-proxy"
	"github.com/openshift/hypershift/support/capabilities"
	"github.com/openshift/hypershift/support/events"
	"github.com/openshift/hypershift/support/images"
	"github.com/openshift/hypershift/support/util"
	tokenminter "github.com/openshift/hypershift/token-minter"
	"go.uber.org/zap/zapcore"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/fields"
	"k8s.io/client-go/discovery"
	"k8s.io/client-go/kubernetes"
	"sigs.k8s.io/controller-runtime/pkg/cache"
	crclient "sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/healthz"

	"github.com/spf13/cobra"
	appsv1client "k8s.io/client-go/kubernetes/typed/apps/v1"

	_ "k8s.io/client-go/plugin/pkg/client/auth/gcp"

	"github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane"
	hyperapi "github.com/openshift/hypershift/support/api"
	"github.com/openshift/hypershift/support/releaseinfo"
	"github.com/openshift/hypershift/support/upsert"

	operatorv1 "github.com/openshift/api/operator/v1"

	ctrl "sigs.k8s.io/controller-runtime"
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
	cmd.AddCommand(hostedclusterconfigoperator.NewCommand())
	cmd.AddCommand(konnectivitysocks5proxy.NewStartCommand())
	cmd.AddCommand(availabilityprober.NewStartCommand())
	cmd.AddCommand(tokenminter.NewStartCommand())
	cmd.AddCommand(ignitionserver.NewStartCommand())

	if err := cmd.Execute(); err != nil {
		fmt.Fprintf(os.Stderr, "%v\n", err)
		os.Exit(1)
	}
}

const (
	// TODO: Include konnectivity image in release payload
	defaultKonnectivityImage = "registry.ci.openshift.org/hypershift/apiserver-network-proxy:latest"

	// Default AWS KMS provider image. Can be overriden with annotation on HostedCluster
	defaultAWSKMSProviderImage = "registry.ci.openshift.org/hypershift/aws-encryption-provider:latest"
)

func NewStartCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "run",
		Short: "Runs the HyperShift Control Plane Operator",
	}

	var (
		namespace                        string
		deploymentName                   string
		metricsAddr                      string
		healthProbeAddr                  string
		hostedClusterConfigOperatorImage string
		socks5ProxyImage                 string
		availabilityProberImage          string
		tokenMinterImage                 string
		inCluster                        bool
		enableCIDebugOutput              bool
		registryOverrides                map[string]string
	)

	cmd.Flags().StringVar(&namespace, "namespace", os.Getenv("MY_NAMESPACE"), "The namespace this operator lives in (required)")
	cmd.Flags().StringVar(&deploymentName, "deployment-name", "control-plane-operator", "The name of the deployment of this operator")
	cmd.Flags().StringVar(&metricsAddr, "metrics-addr", "0.0.0.0:8080", "The address the metric endpoint binds to.")
	cmd.Flags().StringVar(&healthProbeAddr, "health-probe-addr", "0.0.0.0:6060", "The address for the health probe endpoint.")
	cmd.Flags().StringVar(&hostedClusterConfigOperatorImage, "hosted-cluster-config-operator-image", "", "A specific operator image. (defaults to match this operator if running in a deployment)")
	cmd.Flags().StringVar(&socks5ProxyImage, "socks5-proxy-image", "", "Image to use for socks5-proxy. (defaults to match this operator if running in a deployment)")
	cmd.Flags().StringVar(&availabilityProberImage, "availability-prober-image", "", "Image to use for probing apiserver availability. (defaults to match this operator if running in a deployment)")
	cmd.Flags().StringVar(&tokenMinterImage, "token-minter-image", "", "Image to use for the token minter. (defaults to match this operator if running in a deployment)")
	cmd.Flags().BoolVar(&inCluster, "in-cluster", true, "If false, the operator will be assumed to be running outside a kube "+
		"cluster and will make some internal decisions to ease local development (e.g. using external endpoints where possible"+
		"to avoid assuming access to the service network)")
	cmd.Flags().BoolVar(&enableCIDebugOutput, "enable-ci-debug-output", false, "If extra CI debug output should be enabled")
	cmd.Flags().StringToStringVar(&registryOverrides, "registry-overrides", map[string]string{}, "registry-overrides contains the source registry string as a key and the destination registry string as value. Images before being applied are scanned for the source registry string and if found the string is replaced with the destination registry string. Format is: sr1=dr1,sr2=dr2")

	cmd.Run = func(cmd *cobra.Command, args []string) {
		ctrl.SetLogger(zap.New(zap.UseDevMode(true), zap.JSONEncoder(func(o *zapcore.EncoderConfig) {
			o.EncodeTime = zapcore.RFC3339TimeEncoder
		})))
		ctx := ctrl.SetupSignalHandler()

		restConfig := ctrl.GetConfigOrDie()
		restConfig.UserAgent = "hypershift-controlplane-manager"
		leaseDuration := time.Second * 60
		renewDeadline := time.Second * 40
		retryPeriod := time.Second * 15
		mgr, err := ctrl.NewManager(restConfig, ctrl.Options{
			Scheme:                        hyperapi.Scheme,
			MetricsBindAddress:            metricsAddr,
			Port:                          9443,
			LeaderElection:                true,
			LeaderElectionID:              "control-plane-operator-leader-elect",
			LeaderElectionResourceLock:    "leases",
			LeaderElectionNamespace:       namespace,
			LeaderElectionReleaseOnCancel: true,
			LeaseDuration:                 &leaseDuration,
			RenewDeadline:                 &renewDeadline,
			RetryPeriod:                   &retryPeriod,
			HealthProbeBindAddress:        healthProbeAddr,
			NewCache: cache.BuilderWithOptions(cache.Options{
				DefaultSelector: cache.ObjectSelector{Field: fields.OneTermEqualSelector("metadata.namespace", namespace)},
				SelectorsByObject: cache.SelectorsByObject{
					&operatorv1.IngressController{}: {Field: fields.OneTermEqualSelector("metadata.namespace", manifests.IngressPrivateIngressController("").Namespace)},
					// We watch warning events to be able to surface cloud provider errors as conditions
					// Surfacing cloud-provider specific status is discussed in:
					// https://github.com/kubernetes/kubernetes/issues/70159
					// https://github.com/kubernetes/kubernetes/issues/52670
					&corev1.Event{}: {Field: fields.AndSelectors(fields.OneTermEqualSelector("metadata.namespace", namespace), fields.OneTermEqualSelector("type", "warning"))},
				},
			}),
		})
		if err != nil {
			setupLog.Error(err, "unable to start manager")
			os.Exit(1)
		}
		if err = mgr.GetFieldIndexer().IndexField(ctx, &corev1.Event{}, events.EventInvolvedObjectUIDField, func(object crclient.Object) []string {
			event := object.(*corev1.Event)
			return []string{string(event.InvolvedObject.UID)}
		}); err != nil {
			setupLog.Error(err, "failed to setup event involvedObject.uid index")
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

		kubeDiscoveryClient, err := discovery.NewDiscoveryClientForConfig(mgr.GetConfig())
		if err != nil {
			setupLog.Error(err, "unable to create discovery client")
			os.Exit(1)
		}

		mgmtClusterCaps, err := capabilities.DetectManagementClusterCapabilities(kubeDiscoveryClient)
		if err != nil {
			setupLog.Error(err, "unable to detect cluster capabilities")
			os.Exit(1)
		}

		lookupOperatorImage := func(deployments appsv1client.DeploymentInterface, name, userSpecifiedImage string) (string, error) {
			if len(userSpecifiedImage) > 0 {
				setupLog.Info("using image from arguments", "image", userSpecifiedImage)
				return userSpecifiedImage, nil
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
		hostedClusterConfigOperatorImage, err = lookupOperatorImage(kubeClient.AppsV1().Deployments(namespace), deploymentName, hostedClusterConfigOperatorImage)
		if err != nil {
			setupLog.Error(err, fmt.Sprintf("failed to find operator image: %s", err), "controller", "hosted-control-plane")
			os.Exit(1)
		}
		setupLog.Info("using operator image", "image", hostedClusterConfigOperatorImage)

		socks5ProxyImage, err = lookupOperatorImage(kubeClient.AppsV1().Deployments(namespace), deploymentName, socks5ProxyImage)
		if err != nil {
			setupLog.Error(err, fmt.Sprintf("failed to find socks5 proxy image: %s", err), "controller", "hosted-control-plane")
			os.Exit(1)
		}
		setupLog.Info("using socks5 proxy image", "image", socks5ProxyImage)

		availabilityProberImage, err = lookupOperatorImage(kubeClient.AppsV1().Deployments(namespace), deploymentName, availabilityProberImage)
		if err != nil {
			setupLog.Error(err, fmt.Sprintf("failed to find availability prober image: %s", err), "controller", "hosted-control-plane")
			os.Exit(1)
		}
		setupLog.Info("using availability prober image", "image", availabilityProberImage)

		tokenMinterImage, err = lookupOperatorImage(kubeClient.AppsV1().Deployments(namespace), deploymentName, tokenMinterImage)
		if err != nil {
			setupLog.Error(err, fmt.Sprintf("failed to find token minter image: %s", err), "controller", "hosted-control-plane")
			os.Exit(1)
		}
		setupLog.Info("using token minter image", "image", tokenMinterImage)

		konnectivityServerImage := defaultKonnectivityImage
		konnectivityAgentImage := defaultKonnectivityImage
		if envImage := os.Getenv(images.KonnectivityEnvVar); len(envImage) > 0 {
			konnectivityServerImage = envImage
			konnectivityAgentImage = envImage
		}

		awsKMSProviderImage := defaultAWSKMSProviderImage
		if envImage := os.Getenv(images.AWSEncryptionProviderEnvVar); len(envImage) > 0 {
			awsKMSProviderImage = envImage
		}

		releaseProvider := &releaseinfo.RegistryMirrorProviderDecorator{
			Delegate: &releaseinfo.StaticProviderDecorator{
				Delegate: &releaseinfo.CachedProvider{
					Inner: &releaseinfo.RegistryClientProvider{},
					Cache: map[string]*releaseinfo.ReleaseImage{},
				},
				ComponentImages: map[string]string{
					util.AvailabilityProberImageName: availabilityProberImage,
					"hosted-cluster-config-operator": hostedClusterConfigOperatorImage,
					"konnectivity-server":            konnectivityServerImage,
					"konnectivity-agent":             konnectivityAgentImage,
					"socks5-proxy":                   socks5ProxyImage,
					"token-minter":                   tokenMinterImage,
					"aws-kms-provider":               awsKMSProviderImage,
				},
			},
			RegistryOverrides: registryOverrides,
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
			Client:                        mgr.GetClient(),
			ManagementClusterCapabilities: mgmtClusterCaps,
			ReleaseProvider:               releaseProvider,
			HostedAPICache:                apiCacheController.GetCache(),
			CreateOrUpdateProvider:        upsert.New(enableCIDebugOutput),
			EnableCIDebugOutput:           enableCIDebugOutput,
			OperateOnReleaseImage:         os.Getenv("OPERATE_ON_RELEASE_IMAGE"),
		}).SetupWithManager(mgr); err != nil {
			setupLog.Error(err, "unable to create controller", "controller", "hosted-control-plane")
			os.Exit(1)
		}

		if mgmtClusterCaps.Has(capabilities.CapabilityRoute) {
			controllerName := "PrivateKubeAPIServerServiceObserver"
			if err := (&awsprivatelink.PrivateServiceObserver{
				Client:                 mgr.GetClient(),
				ControllerName:         controllerName,
				ServiceNamespace:       namespace,
				ServiceName:            manifests.KubeAPIServerPrivateServiceName,
				HCPNamespace:           namespace,
				CreateOrUpdateProvider: upsert.New(enableCIDebugOutput),
			}).SetupWithManager(ctx, mgr); err != nil {
				controllerName := awsprivatelink.ControllerName(manifests.KubeAPIServerPrivateServiceName)
				setupLog.Error(err, "unable to create controller", "controller", controllerName)
				os.Exit(1)
			}

			controllerName = "PrivateIngressServiceObserver"
			if err := (&awsprivatelink.PrivateServiceObserver{
				Client:                 mgr.GetClient(),
				ControllerName:         controllerName,
				ServiceNamespace:       "openshift-ingress",
				ServiceName:            fmt.Sprintf("router-%s", namespace),
				HCPNamespace:           namespace,
				CreateOrUpdateProvider: upsert.New(enableCIDebugOutput),
			}).SetupWithManager(ctx, mgr); err != nil {
				controllerName := awsprivatelink.ControllerName(fmt.Sprintf("router-%s", namespace))
				setupLog.Error(err, "unable to create controller", "controller", controllerName)
				os.Exit(1)
			}

			if err := (&awsprivatelink.AWSEndpointServiceReconciler{}).SetupWithManager(mgr); err != nil {
				setupLog.Error(err, "unable to create controller", "controller", "aws-endpoint-service")
				os.Exit(1)
			}
		}

		if err := mgr.AddHealthzCheck("healthz", healthz.Ping); err != nil {
			setupLog.Error(err, "unable to set up health check")
			os.Exit(1)
		}
		if err := mgr.AddReadyzCheck("readyz", healthz.Ping); err != nil {
			setupLog.Error(err, "unable to set up ready check")
			os.Exit(1)
		}

		setupLog.Info("starting manager")
		if err := mgr.Start(ctx); err != nil {
			setupLog.Error(err, "problem running manager")
		}
	}

	return cmd
}
