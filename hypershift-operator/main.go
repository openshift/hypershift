/*
Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package main

import (
	"context"
	"fmt"
	"os"
	"strings"
	"time"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	awsutil "github.com/openshift/hypershift/cmd/infra/aws/util"
	pkiconfig "github.com/openshift/hypershift/control-plane-pki-operator/config"
	etcdrecovery "github.com/openshift/hypershift/etcd-recovery"
	"github.com/openshift/hypershift/hypershift-operator/controllers/hostedcluster"
	hcmetrics "github.com/openshift/hypershift/hypershift-operator/controllers/hostedcluster/metrics"
	"github.com/openshift/hypershift/hypershift-operator/controllers/hostedclustersizing"
	"github.com/openshift/hypershift/hypershift-operator/controllers/nodepool"
	npmetrics "github.com/openshift/hypershift/hypershift-operator/controllers/nodepool/metrics"
	"github.com/openshift/hypershift/hypershift-operator/controllers/platform/aws"
	"github.com/openshift/hypershift/hypershift-operator/controllers/proxy"
	awsscheduler "github.com/openshift/hypershift/hypershift-operator/controllers/scheduler/aws"
	azurescheduler "github.com/openshift/hypershift/hypershift-operator/controllers/scheduler/azure"
	sharedingress "github.com/openshift/hypershift/hypershift-operator/controllers/sharedingress"
	"github.com/openshift/hypershift/hypershift-operator/controllers/supportedversion"
	"github.com/openshift/hypershift/hypershift-operator/controllers/uwmtelemetry"
	"github.com/openshift/hypershift/hypershift-operator/featuregate"
	kvinfra "github.com/openshift/hypershift/kubevirtexternalinfra"
	"github.com/openshift/hypershift/pkg/version"
	hyperapi "github.com/openshift/hypershift/support/api"
	"github.com/openshift/hypershift/support/azureutil"
	"github.com/openshift/hypershift/support/capabilities"
	"github.com/openshift/hypershift/support/config"
	"github.com/openshift/hypershift/support/globalconfig"
	"github.com/openshift/hypershift/support/metrics"
	"github.com/openshift/hypershift/support/upsert"
	hyperutil "github.com/openshift/hypershift/support/util"

	operatorv1 "github.com/openshift/api/operator/v1"

	"github.com/aws/aws-sdk-go/service/ec2"
	"github.com/aws/aws-sdk-go/service/ec2/ec2iface"
	"github.com/aws/aws-sdk-go/service/s3"

	admissionregistrationv1 "k8s.io/api/admissionregistration/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/client-go/discovery"
	_ "k8s.io/client-go/plugin/pkg/client/auth/gcp"

	ctrl "sigs.k8s.io/controller-runtime"
	crclient "sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"
	metricsserver "sigs.k8s.io/controller-runtime/pkg/metrics/server"
	"sigs.k8s.io/controller-runtime/pkg/webhook"

	"github.com/go-logr/logr"
	"github.com/spf13/cobra"
	"go.uber.org/zap/zapcore"
)

func main() {
	ctrl.SetLogger(zap.New(zap.JSONEncoder(func(o *zapcore.EncoderConfig) {
		o.EncodeTime = zapcore.RFC3339TimeEncoder
	})))

	cmd := &cobra.Command{
		Use: "hypershift-operator",
		Run: func(cmd *cobra.Command, args []string) {
			_ = cmd.Help()
			os.Exit(1)
		},
	}

	cmd.Version = version.String()

	cmd.AddCommand(NewStartCommand())
	cmd.AddCommand(NewInitCommand())
	cmd.AddCommand(etcdrecovery.NewRecoveryCommand())

	if err := cmd.Execute(); err != nil {
		_, _ = fmt.Fprintf(os.Stderr, "%v\n", err)
		os.Exit(1)
	}
}

type StartOptions struct {
	Namespace                              string
	DeploymentName                         string
	PodName                                string
	MetricsAddr                            string
	CertDir                                string
	EnableOCPClusterMonitoring             bool
	EnableCIDebugOutput                    bool
	ControlPlaneOperatorImage              string
	RegistryOverrides                      map[string]string
	PrivatePlatform                        string
	OIDCStorageProviderS3BucketName        string
	OIDCStorageProviderS3Region            string
	OIDCStorageProviderS3Credentials       string
	EnableUWMTelemetryRemoteWrite          bool
	EnableValidatingWebhook                bool
	EnableDedicatedRequestServingIsolation bool
}

func NewStartCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "run",
		Short: "Runs the Hypershift operator",
	}

	opts := StartOptions{
		Namespace:                        "hypershift",
		DeploymentName:                   "operator",
		MetricsAddr:                      "0",
		CertDir:                          "",
		ControlPlaneOperatorImage:        "",
		RegistryOverrides:                map[string]string{},
		PrivatePlatform:                  string(hyperv1.NonePlatform),
		OIDCStorageProviderS3Region:      "",
		OIDCStorageProviderS3Credentials: "",
	}

	cmd.Flags().StringVar(&opts.Namespace, "namespace", opts.Namespace, "The namespace this operator lives in")
	cmd.Flags().StringVar(&opts.DeploymentName, "deployment-name", opts.DeploymentName, "Legacy flag, does nothing. Use --pod-name instead.")
	cmd.Flags().StringVar(&opts.PodName, "pod-name", opts.PodName, "The name of the pod the operator runs in")
	cmd.Flags().StringVar(&opts.MetricsAddr, "metrics-addr", opts.MetricsAddr, "The address the metric endpoint binds to.")
	cmd.Flags().StringVar(&opts.CertDir, "cert-dir", opts.CertDir, "Path to the serving key and cert for manager")
	cmd.Flags().StringVar(&opts.ControlPlaneOperatorImage, "control-plane-operator-image", opts.ControlPlaneOperatorImage, "A control plane operator image to use (defaults to match this operator if running in a deployment)")
	cmd.Flags().BoolVar(&opts.EnableOCPClusterMonitoring, "enable-ocp-cluster-monitoring", opts.EnableOCPClusterMonitoring, "Development-only option that will make your OCP cluster unsupported: If the cluster Prometheus should be configured to scrape metrics")
	cmd.Flags().BoolVar(&opts.EnableCIDebugOutput, "enable-ci-debug-output", false, "If extra CI debug output should be enabled")
	cmd.Flags().StringToStringVar(&opts.RegistryOverrides, "registry-overrides", map[string]string{}, "registry-overrides contains the source registry string as a key and the destination registry string as value. Images before being applied are scanned for the source registry string and if found the string is replaced with the destination registry string. Format is: sr1=dr1,sr2=dr2")
	cmd.Flags().StringVar(&opts.PrivatePlatform, "private-platform", opts.PrivatePlatform, "Platform on which private clusters are supported by this operator (supports \"AWS\" or \"None\")")
	cmd.Flags().StringVar(&opts.OIDCStorageProviderS3BucketName, "oidc-storage-provider-s3-bucket-name", "", "Name of the bucket in which to store the clusters OIDC discovery information. Required for AWS guest clusters")
	cmd.Flags().StringVar(&opts.OIDCStorageProviderS3Region, "oidc-storage-provider-s3-region", opts.OIDCStorageProviderS3Region, "Region in which the OIDC bucket is located. Required for AWS guest clusters")
	cmd.Flags().StringVar(&opts.OIDCStorageProviderS3Credentials, "oidc-storage-provider-s3-credentials", opts.OIDCStorageProviderS3Credentials, "Location of the credentials file for the OIDC bucket. Required for AWS guest clusters.")
	cmd.Flags().BoolVar(&opts.EnableUWMTelemetryRemoteWrite, "enable-uwm-telemetry-remote-write", opts.EnableUWMTelemetryRemoteWrite, "If true, enables a controller that ensures user workload monitoring is enabled and that it is configured to remote write telemetry metrics from control planes")
	cmd.Flags().BoolVar(&opts.EnableValidatingWebhook, "enable-validating-webhook", false, "Enable webhook for validating hypershift API types")
	cmd.Flags().BoolVar(&opts.EnableDedicatedRequestServingIsolation, "enable-dedicated-request-serving-isolation", true, "If true, enables scheduling of request serving components to dedicated nodes")

	// Attempt to determine featureset prior to adding featuregate flags.
	// It is safe to get the empty string from this as the empty string is the default featureset.
	// This should _generally_ be safe to do here because any unknown featureset provided in the environment
	// variable should result in using the default featureset.
	featureSet := os.Getenv("HYPERSHIFT_FEATURESET")
	// Doing this call first should explicitly configure the default gate state
	// for the provided featureset. This makes it so that specifying feature gates
	// via the `--feature-gates` flag will always override whatever has been configured
	// by the featureset.
	featuregate.ConfigureFeatureSet(featureSet)
	featuregate.Gate().AddFlag(cmd.Flags())

	cmd.Run = func(cmd *cobra.Command, args []string) {
		ctx, cancel := context.WithCancel(ctrl.SetupSignalHandler())
		defer cancel()

		switch hyperv1.PlatformType(opts.PrivatePlatform) {
		case hyperv1.AWSPlatform, hyperv1.NonePlatform:
		default:
			fmt.Printf("Unsupported private platform: %q\n", opts.PrivatePlatform)
			os.Exit(1)
		}

		if err := run(ctx, &opts, ctrl.Log.WithName("setup")); err != nil {
			fmt.Println(err)
			os.Exit(1)
		}
	}

	return cmd
}

func run(ctx context.Context, opts *StartOptions, log logr.Logger) error {
	log.Info("Starting hypershift-operator-manager", "version", version.String())

	restConfig := ctrl.GetConfigOrDie()
	restConfig.UserAgent = "hypershift-operator-manager"
	leaseDuration := time.Second * 60
	renewDeadline := time.Second * 40
	retryPeriod := time.Second * 15
	mgr, err := ctrl.NewManager(restConfig, ctrl.Options{
		Scheme: hyperapi.Scheme,
		Metrics: metricsserver.Options{
			BindAddress: opts.MetricsAddr,
		},
		WebhookServer: webhook.NewServer(webhook.Options{
			Port:    9443,
			CertDir: opts.CertDir,
		}),
		Client: crclient.Options{
			Cache: &crclient.CacheOptions{
				Unstructured: true,
			},
		},
		LeaderElection:                true,
		LeaderElectionID:              "hypershift-operator-leader-elect",
		LeaderElectionResourceLock:    "leases",
		LeaderElectionReleaseOnCancel: true,
		LeaderElectionNamespace:       opts.Namespace,
		LeaseDuration:                 &leaseDuration,
		RenewDeadline:                 &renewDeadline,
		RetryPeriod:                   &retryPeriod,
	})
	if err != nil {
		return fmt.Errorf("unable to start manager: %w", err)
	}

	kubeDiscoveryClient, err := discovery.NewDiscoveryClientForConfig(mgr.GetConfig())
	if err != nil {
		return fmt.Errorf("unable to create discovery client: %w", err)
	}

	mgmtClusterCaps, err := capabilities.DetectManagementClusterCapabilities(kubeDiscoveryClient)
	if err != nil {
		return fmt.Errorf("unable to detect cluster capabilities: %w", err)
	}

	lookupOperatorImage := func(userSpecifiedImage string) (string, error) {
		if len(userSpecifiedImage) > 0 {
			log.Info("using image from arguments", "image", userSpecifiedImage)
			return userSpecifiedImage, nil
		}
		me := &corev1.Pod{ObjectMeta: metav1.ObjectMeta{Namespace: opts.Namespace, Name: opts.PodName}}
		if err := mgr.GetAPIReader().Get(ctx, crclient.ObjectKeyFromObject(me), me); err != nil {
			return "", fmt.Errorf("failed to get operator pod %s: %w", crclient.ObjectKeyFromObject(me), err)
		}
		// Use the container status to make sure we get the sha256 reference rather than a potentially
		// floating tag.
		for _, container := range me.Status.ContainerStatuses {
			// TODO: could use downward API for this too, overkill?
			if container.Name == "operator" {
				return strings.TrimPrefix(container.ImageID, "docker-pullable://"), nil
			}
		}
		return "", fmt.Errorf("couldn't locate operator container on deployment")
	}
	var operatorImage string
	if err := wait.PollUntilContextTimeout(ctx, 5*time.Second, 30*time.Second, true, func(ctx context.Context) (bool, error) {
		operatorImage, err = lookupOperatorImage(opts.ControlPlaneOperatorImage)
		if err != nil {
			return false, err
		}
		// Apparently this is occasionally set to an empty string
		if operatorImage == "" {
			log.Info("operator image is empty, retrying")
			return false, nil
		}
		return true, nil
	}); err != nil {
		return fmt.Errorf("failed to find operator image: %w", err)
	}

	log.Info("using hosted control plane operator image", "operator-image", operatorImage)

	createOrUpdate := upsert.New(opts.EnableCIDebugOutput)

	metricsSet, err := metrics.MetricsSetFromEnv()
	if err != nil {
		return err
	}
	log.Info("Using metrics set", "set", metricsSet.String())

	sreConfigHash := ""
	if metricsSet == metrics.MetricsSetSRE {
		readerClient := mgr.GetAPIReader()
		cm := metrics.SREMetricsSetConfigurationConfigMap(opts.Namespace)
		err := readerClient.Get(context.Background(), crclient.ObjectKeyFromObject(cm), cm)
		if err != nil {
			if apierrors.IsNotFound(err) {
				log.Info("WARNING: no configuration found for the SRE metrics set")
			} else {
				return fmt.Errorf("unable to read SRE metrics set configmap: %w", err)
			}
		} else {
			if err := metrics.LoadSREMetricsSetConfigurationFromConfigMap(cm); err != nil {
				return fmt.Errorf("unable to load SRE metrics configuration: %w", err)
			}
			sreConfigHash = metrics.SREMetricsSetConfigHash(cm)
		}
	}

	// The mgr and therefore the cache is not started yet, thus we have to construct a client that
	// directly reads from the api.
	apiReadingClient, err := crclient.New(mgr.GetConfig(), crclient.Options{Scheme: hyperapi.Scheme})
	if err != nil {
		return fmt.Errorf("failed to construct api reading client: %w", err)
	}

	// Create the registry provider for the release and image metadata providers
	registryProvider, err := globalconfig.NewCommonRegistryProvider(ctx, mgmtClusterCaps, apiReadingClient, opts.RegistryOverrides)

	monitoringDashboards := (os.Getenv("MONITORING_DASHBOARDS") == "1")
	enableCVOManagementClusterMetricsAccess := (os.Getenv(config.EnableCVOManagementClusterMetricsAccessEnvVar) == "1")

	enableEtcdRecovery := os.Getenv(config.EnableEtcdRecoveryEnvVar) == "1"

	certRotationScale, err := pkiconfig.GetCertRotationScale()
	if err != nil {
		return fmt.Errorf("could not load cert rotation scale: %w", err)
	}

	hostedClusterReconciler := &hostedcluster.HostedClusterReconciler{
		Client:                                  mgr.GetClient(),
		ManagementClusterCapabilities:           mgmtClusterCaps,
		HypershiftOperatorImage:                 operatorImage,
		RegistryOverrides:                       opts.RegistryOverrides,
		RegistryProvider:                        registryProvider,
		EnableOCPClusterMonitoring:              opts.EnableOCPClusterMonitoring,
		EnableCIDebugOutput:                     opts.EnableCIDebugOutput,
		MetricsSet:                              metricsSet,
		OperatorNamespace:                       opts.Namespace,
		SREConfigHash:                           sreConfigHash,
		KubevirtInfraClients:                    kvinfra.NewKubevirtInfraClientMap(),
		MonitoringDashboards:                    monitoringDashboards,
		CertRotationScale:                       certRotationScale,
		EnableCVOManagementClusterMetricsAccess: enableCVOManagementClusterMetricsAccess,
		EnableEtcdRecovery:                      enableEtcdRecovery,
		FeatureSet:                              featuregate.FeatureSet(),
		OpenShiftTrustedCAFilePath:              "/etc/pki/ca-trust/extracted/pem/tls-ca-bundle.pem",
	}
	if opts.OIDCStorageProviderS3BucketName != "" {
		awsSession := awsutil.NewSession("hypershift-operator-oidc-bucket", opts.OIDCStorageProviderS3Credentials, "", "", opts.OIDCStorageProviderS3Region)
		awsConfig := awsutil.NewConfig()
		s3Client := s3.New(awsSession, awsConfig)
		hostedClusterReconciler.S3Client = s3Client
		hostedClusterReconciler.OIDCStorageProviderS3BucketName = opts.OIDCStorageProviderS3BucketName
	}
	if err := hostedClusterReconciler.SetupWithManager(mgr, createOrUpdate, metricsSet, opts.Namespace); err != nil {
		return fmt.Errorf("unable to create controller: %w", err)
	}
	if opts.CertDir != "" {
		if err := hostedcluster.SetupWebhookWithManager(mgr, registryProvider.MetadataProvider, log); err != nil {
			return fmt.Errorf("unable to create webhook: %w", err)
		}
	}
	hcmetrics.CreateAndRegisterHostedClustersMetricsCollector(mgr.GetClient())

	// Since we dropped the validation webhook server we need to ensure this resource doesn't exist
	// otherwise it will intercept kas requests and fail.
	// TODO (alberto): dropped in 4.14.
	if !opts.EnableValidatingWebhook {
		validatingWebhookConfiguration := &admissionregistrationv1.ValidatingWebhookConfiguration{
			TypeMeta: metav1.TypeMeta{
				Kind:       "ValidatingWebhookConfiguration",
				APIVersion: admissionregistrationv1.SchemeGroupVersion.String(),
			},
			ObjectMeta: metav1.ObjectMeta{
				Namespace: opts.Namespace,
				Name:      hyperv1.GroupVersion.Group,
			},
		}
		if err := mgr.GetClient().Delete(ctx, validatingWebhookConfiguration); err != nil {
			if !apierrors.IsNotFound(err) {
				return err
			}
		}
	}

	var ec2Client ec2iface.EC2API

	if hyperv1.PlatformType(opts.PrivatePlatform) == hyperv1.AWSPlatform {
		awsSession := awsutil.NewSession("hypershift-operator", "", "", "", "")
		awsConfig := awsutil.NewConfig()
		ec2Client = ec2.New(awsSession, awsConfig)
	}

	npmetrics.CreateAndRegisterNodePoolsMetricsCollector(mgr.GetClient(), ec2Client)

	if err := (&nodepool.NodePoolReconciler{
		Client:                  mgr.GetClient(),
		ReleaseProvider:         registryProvider.ReleaseProvider,
		CreateOrUpdateProvider:  createOrUpdate,
		HypershiftOperatorImage: operatorImage,
		ImageMetadataProvider:   registryProvider.MetadataProvider,
		KubevirtInfraClients:    kvinfra.NewKubevirtInfraClientMap(),
		EC2Client:               ec2Client,
	}).SetupWithManager(mgr); err != nil {
		return fmt.Errorf("unable to create controller: %w", err)
	}

	if mgmtClusterCaps.Has(capabilities.CapabilityProxy) {
		if err := proxy.Setup(mgr, opts.Namespace, opts.DeploymentName); err != nil {
			return fmt.Errorf("failed to set up the proxy controller: %w", err)
		}
	}

	enableSizeTagging := os.Getenv("ENABLE_SIZE_TAGGING") == "1"
	if enableSizeTagging {
		if err := hostedclustersizing.SetupWithManager(ctx, mgr, operatorImage, registryProvider.ReleaseProvider, registryProvider.MetadataProvider); err != nil {
			return fmt.Errorf("failed to set up hosted cluster sizing operator: %w", err)
		}
	}

	// Start platform-specific controllers
	switch hyperv1.PlatformType(opts.PrivatePlatform) {
	case hyperv1.AWSPlatform:
		if err := (&aws.AWSEndpointServiceReconciler{
			Client:                 mgr.GetClient(),
			CreateOrUpdateProvider: createOrUpdate,
		}).SetupWithManager(mgr); err != nil {
			return fmt.Errorf("unable to create controller: %w", err)
		}
	}

	// Start controller to manage supported versions configmap
	if err := supportedversion.New(mgr.GetClient(), createOrUpdate, opts.Namespace).
		SetupWithManager(mgr); err != nil {
		return fmt.Errorf("unable to create supported version controller: %w", err)
	}

	// If enabled, start controller to ensure UWM stack is enabled and configured
	// to remotely write telemetry metrics.
	if opts.EnableUWMTelemetryRemoteWrite {
		if err := (&uwmtelemetry.Reconciler{
			Namespace:              opts.Namespace,
			Client:                 mgr.GetClient(),
			CreateOrUpdateProvider: createOrUpdate,
		}).SetupWithManager(mgr); err != nil {
			return fmt.Errorf("unable to create uwm telemetry controller: %w", err)
		}
		log.Info("UWM telemetry remote write controller enabled")
	} else {
		log.Info("UWM telemetry remote write controller disabled")
	}

	if sharedingress.UseSharedIngress() {
		sharedIngress := sharedingress.SharedIngressReconciler{
			Namespace:                     opts.Namespace,
			ManagementClusterCapabilities: mgmtClusterCaps,
		}
		if err := sharedIngress.SetupWithManager(mgr, createOrUpdate); err != nil {
			return fmt.Errorf("unable to create dedicated sharedingress controller: %w", err)
		}
	}

	// Start controllers to manage dedicated request serving isolation
	if opts.EnableDedicatedRequestServingIsolation && !azureutil.IsAroHCP() {
		// Use the new scheduler if we support size tagging on hosted clusters
		if enableSizeTagging {
			hcScheduler := awsscheduler.DedicatedServingComponentSchedulerAndSizer{}
			if err := hcScheduler.SetupWithManager(ctx, mgr, createOrUpdate); err != nil {
				return fmt.Errorf("unable to create dedicated serving component scheduler/resizer controller: %w", err)
			}
			placeholderScheduler := awsscheduler.PlaceholderScheduler{}
			if err := placeholderScheduler.SetupWithManager(ctx, mgr); err != nil {
				return fmt.Errorf("unable to create placeholder scheduler controller: %w", err)
			}
			autoScaler := awsscheduler.RequestServingNodeAutoscaler{}
			if err := autoScaler.SetupWithManager(mgr); err != nil {
				return fmt.Errorf("unable to create autoscaler controller: %w", err)
			}
			deScaler := awsscheduler.MachineSetDescaler{}
			if err := deScaler.SetupWithManager(mgr); err != nil {
				return fmt.Errorf("unable to create machine set descaler controller: %w", err)
			}
			nonRequestServingNodeAutoscaler := awsscheduler.NonRequestServingNodeAutoscaler{}
			if err := nonRequestServingNodeAutoscaler.SetupWithManager(mgr); err != nil {
				return fmt.Errorf("unable to create non request serving node autoscaler controller: %w", err)
			}
		} else {
			nodeReaper := awsscheduler.DedicatedServingComponentNodeReaper{
				Client: mgr.GetClient(),
			}
			if err := nodeReaper.SetupWithManager(mgr); err != nil {
				return fmt.Errorf("unable to create dedicated serving component node reaper controller: %w", err)
			}
			hcScheduler := awsscheduler.DedicatedServingComponentScheduler{
				Client: mgr.GetClient(),
			}
			if err := hcScheduler.SetupWithManager(mgr, createOrUpdate); err != nil {
				return fmt.Errorf("unable to create dedicated serving component scheduler controller: %w", err)
			}
		}
	} else {
		log.Info("Dedicated request serving isolation controllers disabled")
	}

	if enableSizeTagging && azureutil.IsAroHCP() {
		hcScheduler := azurescheduler.Scheduler{}
		if err := hcScheduler.SetupWithManager(mgr); err != nil {
			return fmt.Errorf("unable to create aro scheduler controller: %w", err)
		}
	}

	// If it exists, block default ingress controller from admitting HCP private routes
	ic := &operatorv1.IngressController{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "default",
			Namespace: "openshift-ingress-operator",
		},
	}
	if err := apiReadingClient.Get(ctx, types.NamespacedName{Namespace: ic.Namespace, Name: ic.Name}, ic); err == nil {
		if _, err := controllerutil.CreateOrUpdate(ctx, apiReadingClient, ic, func() error {
			if ic.Spec.RouteSelector == nil {
				ic.Spec.RouteSelector = &metav1.LabelSelector{}
			}
			if ic.Spec.RouteSelector.MatchExpressions == nil {
				ic.Spec.RouteSelector.MatchExpressions = []metav1.LabelSelectorRequirement{}
			}
			for _, requirement := range ic.Spec.RouteSelector.MatchExpressions {
				if requirement.Key != hyperutil.HCPRouteLabel {
					continue
				}
				requirement.Operator = metav1.LabelSelectorOpDoesNotExist
				return nil
			}
			ic.Spec.RouteSelector.MatchExpressions = append(ic.Spec.RouteSelector.MatchExpressions, metav1.LabelSelectorRequirement{
				Key:      hyperutil.HCPRouteLabel,
				Operator: metav1.LabelSelectorOpDoesNotExist,
			})
			return nil
		}); err != nil {
			return fmt.Errorf("failed to reconcile default ingress controller: %w", err)
		}
		log.Info("reconciled default ingress controller")
	}
	if err != nil && apierrors.IsNotFound(err) {
		return fmt.Errorf("failed to get ingress controller: %w", err)
	}

	if err := setupOperatorInfoMetric(mgr); err != nil {
		return fmt.Errorf("failed to setup metrics: %w", err)
	}

	// Start the controllers
	log.Info("starting manager")
	return mgr.Start(ctx)
}
