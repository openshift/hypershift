package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	availabilityprober "github.com/openshift/hypershift/availability-prober"
	"github.com/openshift/hypershift/control-plane-operator/controllers/awsprivatelink"
	"github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/manifests"
	"github.com/openshift/hypershift/control-plane-operator/hostedclusterconfigoperator"
	"github.com/openshift/hypershift/dnsresolver"
	ignitionserver "github.com/openshift/hypershift/ignition-server/cmd"
	konnectivitysocks5proxy "github.com/openshift/hypershift/konnectivity-socks5-proxy"
	kubernetesdefaultproxy "github.com/openshift/hypershift/kubernetes-default-proxy"
	"github.com/openshift/hypershift/pkg/version"
	"github.com/openshift/hypershift/support/capabilities"
	"github.com/openshift/hypershift/support/config"
	"github.com/openshift/hypershift/support/events"
	"github.com/openshift/hypershift/support/images"
	"github.com/openshift/hypershift/support/metrics"
	"github.com/openshift/hypershift/support/util"
	tokenminter "github.com/openshift/hypershift/token-minter"
	"go.uber.org/zap/zapcore"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/fields"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/client-go/discovery"
	"sigs.k8s.io/controller-runtime/pkg/cache"
	crclient "sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/healthz"

	"github.com/spf13/cobra"

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
	ctrl.SetLogger(zap.New(zap.UseDevMode(true), zap.JSONEncoder(func(o *zapcore.EncoderConfig) {
		o.EncodeTime = zapcore.RFC3339TimeEncoder
	})))
	basename := filepath.Base(os.Args[0])
	cmd := commandFor(basename)

	cmd.Version = version.GetRevision()

	if err := cmd.Execute(); err != nil {
		fmt.Fprintf(os.Stderr, "%v\n", err)
		os.Exit(1)
	}

}

func commandFor(name string) *cobra.Command {
	var cmd *cobra.Command
	switch name {
	case "ignition-server":
		cmd = ignitionserver.NewStartCommand()
	case "konnectivity-socks5-proxy":
		cmd = konnectivitysocks5proxy.NewStartCommand()
	case "availability-prober":
		cmd = availabilityprober.NewStartCommand()
	case "token-minter":
		cmd = tokenminter.NewStartCommand()
	default:
		// for the default case, there is no need
		// to convert flags, return immediately
		return defaultCommand()
	}
	convertArgsIfNecessary(cmd)
	return cmd
}

// convertArgsIfNecessary will convert go-style single dash flags
// to POSIX compliant flags that work with spf13/pflag
func convertArgsIfNecessary(cmd *cobra.Command) {
	commandArgs := []string{}
	for i := 1; i < len(os.Args); i++ {
		arg := os.Args[i]
		if !strings.HasPrefix(arg, "--") && strings.HasPrefix(arg, "-") {
			flagName := arg[1:]
			if strings.Contains(flagName, "=") {
				parts := strings.SplitN(flagName, "=", 2)
				flagName = parts[0]
			}
			if flag := cmd.Flags().Lookup(flagName); flag != nil {
				commandArgs = append(commandArgs, "-"+arg)
				continue
			}
		}
		commandArgs = append(commandArgs, arg)
	}
	cmd.SetArgs(commandArgs)
}

func defaultCommand() *cobra.Command {
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
	cmd.AddCommand(kubernetesdefaultproxy.NewStartCommand())
	cmd.AddCommand(dnsresolver.NewCommand())

	return cmd

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
		imageOverrides                   map[string]string
	)

	cmd.Flags().StringVar(&namespace, "namespace", os.Getenv("MY_NAMESPACE"), "The namespace this operator lives in (required)")
	cmd.Flags().StringVar(&deploymentName, "deployment-name", "control-plane-operator", "The name of the deployment of this operator. If possible, submit the podName through the POD_NAME env var instead to allow resolving to a sha256 reference.")
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
	cmd.Flags().StringToStringVar(&imageOverrides, "image-overrides", map[string]string{},
		"List of images that should be used for a hosted cluster control plane instead of images from OpenShift release specified in HostedCluster. "+
			"Format is: name1=image1,name2=image2. \"nameX\" is name of an image in OpenShift release (e.g. \"cluster-network-operator\"). "+
			"\"imageX\" is container image name (e.g. \"quay.io/foo/my-network-operator:latest\"). The container image name is still subject of registry name "+
			"replacement when --registry-overrides is used.")

	cmd.Run = func(cmd *cobra.Command, args []string) {
		setupLog.Info("Starting hypershift-controlplane-manager", "version", version.String())
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

		// For now, since the hosted cluster config operator is treated like any other
		// release payload component but isn't actually part of a release payload,
		// enable the user to specify an image directly as a flag, and otherwise
		// try and detect the control plane operator's image to use instead.
		lookupOperatorImage := func(userSpecifiedImage string) (string, error) {
			if len(userSpecifiedImage) > 0 {
				setupLog.Info("using image from arguments", "image", userSpecifiedImage)
				return userSpecifiedImage, nil
			}
			if podName := os.Getenv("POD_NAME"); podName != "" {
				me := &corev1.Pod{ObjectMeta: metav1.ObjectMeta{Namespace: namespace, Name: podName}}
				if err := mgr.GetAPIReader().Get(ctx, crclient.ObjectKeyFromObject(me), me); err != nil {
					return "", fmt.Errorf("failed to get operator pod %s: %w", crclient.ObjectKeyFromObject(me), err)
				}
				// Use the container status to make sure we get the sha256 reference rather than a potentially
				// floating tag.
				for _, container := range me.Status.ContainerStatuses {
					// TODO: could use downward API for this too, overkill?
					if container.Name == "control-plane-operator" {
						return strings.TrimPrefix(container.ImageID, "docker-pullable://"), nil
					}
				}
			}
			deployment := &appsv1.Deployment{ObjectMeta: metav1.ObjectMeta{Namespace: namespace, Name: deploymentName}}
			if err := mgr.GetAPIReader().Get(ctx, crclient.ObjectKeyFromObject(deployment), deployment); err != nil {
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

		if err := wait.PollImmediate(5*time.Second, 30*time.Second, func() (bool, error) {
			hostedClusterConfigOperatorImage, err = lookupOperatorImage(hostedClusterConfigOperatorImage)
			if err != nil {
				return false, err
			}
			// Apparently this is occasionally set to an empty string
			if hostedClusterConfigOperatorImage == "" {
				setupLog.Info("hosted cluster config operator image is empty, retrying")
				return false, nil
			}
			return true, nil
		}); err != nil {
			setupLog.Error(err, fmt.Sprintf("failed to find operator image: %s", err), "controller", "hosted-control-plane")
			os.Exit(1)
		}
		setupLog.Info("using operator image", "image", hostedClusterConfigOperatorImage)

		socks5ProxyImage, err = lookupOperatorImage(socks5ProxyImage)
		if err != nil {
			setupLog.Error(err, fmt.Sprintf("failed to find socks5 proxy image: %s", err), "controller", "hosted-control-plane")
			os.Exit(1)
		}
		setupLog.Info("using socks5 proxy image", "image", socks5ProxyImage)

		availabilityProberImage, err = lookupOperatorImage(availabilityProberImage)
		if err != nil {
			setupLog.Error(err, fmt.Sprintf("failed to find availability prober image: %s", err), "controller", "hosted-control-plane")
			os.Exit(1)
		}
		setupLog.Info("using availability prober image", "image", availabilityProberImage)

		tokenMinterImage, err = lookupOperatorImage(tokenMinterImage)
		if err != nil {
			setupLog.Error(err, fmt.Sprintf("failed to find token minter image: %s", err), "controller", "hosted-control-plane")
			os.Exit(1)
		}
		setupLog.Info("using token minter image", "image", tokenMinterImage)

		cpoImage, err := lookupOperatorImage("")
		if err != nil {
			setupLog.Error(err, "failed to find controlplane-operator-image")
			os.Exit(1)
		}
		setupLog.Info("Using CPO image", "image", cpoImage)

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

		componentImages := map[string]string{
			util.AvailabilityProberImageName: availabilityProberImage,
			"hosted-cluster-config-operator": hostedClusterConfigOperatorImage,
			"konnectivity-server":            konnectivityServerImage,
			"konnectivity-agent":             konnectivityAgentImage,
			"socks5-proxy":                   socks5ProxyImage,
			"token-minter":                   tokenMinterImage,
			"aws-kms-provider":               awsKMSProviderImage,
			util.CPOImageName:                cpoImage,
		}
		for name, image := range imageOverrides {
			componentImages[name] = image
		}

		releaseProvider := &releaseinfo.RegistryMirrorProviderDecorator{
			Delegate: &releaseinfo.StaticProviderDecorator{
				Delegate: &releaseinfo.CachedProvider{
					Inner: &releaseinfo.RegistryClientProvider{},
					Cache: map[string]*releaseinfo.ReleaseImage{},
				},
				ComponentImages: componentImages,
			},
			RegistryOverrides: registryOverrides,
		}

		defaultIngressDomain := os.Getenv(config.DefaultIngressDomainEnvVar)

		metricsSet, err := metrics.MetricsSetFromEnv()
		if err != nil {
			setupLog.Error(err, "invalid metrics set")
			os.Exit(1)
		}
		setupLog.Info("Using metrics set", "set", metricsSet.String())
		if err := (&hostedcontrolplane.HostedControlPlaneReconciler{
			Client:                        mgr.GetClient(),
			ManagementClusterCapabilities: mgmtClusterCaps,
			ReleaseProvider:               releaseProvider,
			EnableCIDebugOutput:           enableCIDebugOutput,
			OperateOnReleaseImage:         os.Getenv("OPERATE_ON_RELEASE_IMAGE"),
			DefaultIngressDomain:          defaultIngressDomain,
			MetricsSet:                    metricsSet,
		}).SetupWithManager(mgr, upsert.New(enableCIDebugOutput).CreateOrUpdate); err != nil {
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
				ServiceNamespace:       namespace,
				ServiceName:            manifests.PrivateRouterService("").Name,
				HCPNamespace:           namespace,
				CreateOrUpdateProvider: upsert.New(enableCIDebugOutput),
			}).SetupWithManager(ctx, mgr); err != nil {
				controllerName := awsprivatelink.ControllerName(manifests.PrivateRouterService("").Name)
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
