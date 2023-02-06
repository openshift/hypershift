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

	"github.com/aws/aws-sdk-go/service/s3"
	"github.com/go-logr/logr"
	operatorv1 "github.com/openshift/api/operator/v1"
	hyperapi "github.com/openshift/hypershift/api"
	hyperv1 "github.com/openshift/hypershift/api/v1beta1"
	awsutil "github.com/openshift/hypershift/cmd/infra/aws/util"
	"github.com/openshift/hypershift/hypershift-operator/controllers/hostedcluster"
	"github.com/openshift/hypershift/hypershift-operator/controllers/nodepool"
	"github.com/openshift/hypershift/hypershift-operator/controllers/platform/aws"
	"github.com/openshift/hypershift/hypershift-operator/controllers/proxy"
	"github.com/openshift/hypershift/hypershift-operator/controllers/supportedversion"
	"github.com/openshift/hypershift/hypershift-operator/controllers/uwmtelemetry"
	"github.com/openshift/hypershift/pkg/version"
	"github.com/openshift/hypershift/support/capabilities"
	"github.com/openshift/hypershift/support/metrics"
	"github.com/openshift/hypershift/support/releaseinfo"
	"github.com/openshift/hypershift/support/upsert"
	"github.com/openshift/hypershift/support/util"
	hyperutil "github.com/openshift/hypershift/support/util"
	"github.com/spf13/cobra"
	"go.uber.org/zap/zapcore"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/client-go/discovery"
	_ "k8s.io/client-go/plugin/pkg/client/auth/gcp"
	ctrl "sigs.k8s.io/controller-runtime"
	crclient "sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"
)

func main() {
	cmd := &cobra.Command{
		Use: "hypershift-operator",
		Run: func(cmd *cobra.Command, args []string) {
			cmd.Help()
			os.Exit(1)
		},
	}

	cmd.Version = version.GetRevision()

	cmd.AddCommand(NewStartCommand())

	if err := cmd.Execute(); err != nil {
		fmt.Fprintf(os.Stderr, "%v\n", err)
		os.Exit(1)
	}
}

type StartOptions struct {
	Namespace                        string
	DeploymentName                   string
	PodName                          string
	MetricsAddr                      string
	CertDir                          string
	EnableOCPClusterMonitoring       bool
	EnableCIDebugOutput              bool
	ControlPlaneOperatorImage        string
	RegistryOverrides                map[string]string
	PrivatePlatform                  string
	OIDCStorageProviderS3BucketName  string
	OIDCStorageProviderS3Region      string
	OIDCStorageProviderS3Credentials string
	EnableUWMTelemetryRemoteWrite    bool
}

func NewStartCommand() *cobra.Command {
	ctrl.SetLogger(zap.New(zap.UseDevMode(true), zap.JSONEncoder(func(o *zapcore.EncoderConfig) {
		o.EncodeTime = zapcore.RFC3339TimeEncoder
	})))

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

	cmd.Run = func(cmd *cobra.Command, args []string) {
		ctx, cancel := context.WithCancel(ctrl.SetupSignalHandler())
		defer cancel()
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
		Scheme:                        hyperapi.Scheme,
		MetricsBindAddress:            opts.MetricsAddr,
		Port:                          9443,
		CertDir:                       opts.CertDir,
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
	if err := wait.PollImmediate(5*time.Second, 30*time.Second, func() (bool, error) {
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
		if err != nil {
			return fmt.Errorf("failed to find operator image: %w", err)
		}
	}

	log.Info("using hosted control plane operator image", "operator-image", operatorImage)

	createOrUpdate := upsert.New(opts.EnableCIDebugOutput)

	metricsSet, err := metrics.MetricsSetFromEnv()
	if err != nil {
		return err
	}
	log.Info("Using metrics set", "set", metricsSet.String())

	hostedClusterReconciler := &hostedcluster.HostedClusterReconciler{
		Client:                        mgr.GetClient(),
		ManagementClusterCapabilities: mgmtClusterCaps,
		HypershiftOperatorImage:       operatorImage,
		ReleaseProvider: &releaseinfo.RegistryMirrorProviderDecorator{
			Delegate: &releaseinfo.CachedProvider{
				Inner: &releaseinfo.RegistryClientProvider{},
				Cache: map[string]*releaseinfo.ReleaseImage{},
			},
			RegistryOverrides: opts.RegistryOverrides,
		},
		EnableOCPClusterMonitoring: opts.EnableOCPClusterMonitoring,
		EnableCIDebugOutput:        opts.EnableCIDebugOutput,
		ImageMetadataProvider:      &util.RegistryClientImageMetadataProvider{},
		MetricsSet:                 metricsSet,
	}
	if opts.OIDCStorageProviderS3BucketName != "" {
		awsSession := awsutil.NewSession("hypershift-operator-oidc-bucket", opts.OIDCStorageProviderS3Credentials, "", "", opts.OIDCStorageProviderS3Region)
		awsConfig := awsutil.NewConfig()
		s3Client := s3.New(awsSession, awsConfig)
		hostedClusterReconciler.S3Client = s3Client
		hostedClusterReconciler.OIDCStorageProviderS3BucketName = opts.OIDCStorageProviderS3BucketName
	}
	if err := hostedClusterReconciler.SetupWithManager(mgr, createOrUpdate); err != nil {
		return fmt.Errorf("unable to create controller: %w", err)
	}
	if opts.CertDir != "" {
		if err := hostedcluster.SetupWebhookWithManager(mgr); err != nil {
			return fmt.Errorf("unable to create webhook: %w", err)
		}
	}

	if err := (&nodepool.NodePoolReconciler{
		Client: mgr.GetClient(),
		ReleaseProvider: &releaseinfo.RegistryMirrorProviderDecorator{
			Delegate: &releaseinfo.CachedProvider{
				Inner: &releaseinfo.RegistryClientProvider{},
				Cache: map[string]*releaseinfo.ReleaseImage{},
			},
			RegistryOverrides: opts.RegistryOverrides,
		},
		CreateOrUpdateProvider:  createOrUpdate,
		HypershiftOperatorImage: operatorImage,
		ImageMetadataProvider:   &util.RegistryClientImageMetadataProvider{},
	}).SetupWithManager(mgr); err != nil {
		return fmt.Errorf("unable to create controller: %w", err)
	}

	if mgmtClusterCaps.Has(capabilities.CapabilityProxy) {
		if err := proxy.Setup(mgr, opts.Namespace, opts.DeploymentName); err != nil {
			return fmt.Errorf("failed to set up the proxy controller: %w", err)
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
	if err := (supportedversion.New(mgr.GetClient(), createOrUpdate, opts.Namespace).
		SetupWithManager(mgr)); err != nil {
		return fmt.Errorf("unable to create supported version controller: %w", err)
	}

	// If enabled, start controller to ensure UWM stack is enabled and configured
	// to remote write telemetry metrics
	if opts.EnableUWMTelemetryRemoteWrite {
		if err := (&uwmtelemetry.Reconciler{
			Namespace:              opts.Namespace,
			Client:                 mgr.GetClient(),
			CreateOrUpdateProvider: createOrUpdate,
		}).SetupWithManager(mgr); err != nil {
			return fmt.Errorf("unable to create uwm telemetry controller: %w", err)
		}
	}

	// The mgr and therefore the cache is not started yet, thus we have to construct a client that
	// directly reads from the api.
	apiReadingClient, err := crclient.NewDelegatingClient(crclient.NewDelegatingClientInput{
		CacheReader: mgr.GetAPIReader(),
		Client:      mgr.GetClient(),
	})
	if err != nil {
		return fmt.Errorf("failed to construct api reading client: %w", err)
	}

	// If it exsists, block default ingress controller from admitting HCP private routes
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

	if err := setupMetrics(mgr); err != nil {
		return fmt.Errorf("failed to setup metrics: %w", err)
	}

	// Start the controllers
	log.Info("starting manager")
	return mgr.Start(ctx)
}
