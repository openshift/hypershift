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

	"github.com/go-logr/logr"
	hyperapi "github.com/openshift/hypershift/api"
	"github.com/openshift/hypershift/hypershift-operator/controllers/hostedcluster"
	"github.com/openshift/hypershift/hypershift-operator/controllers/nodepool"
	"github.com/openshift/hypershift/support/releaseinfo"
	"github.com/spf13/cobra"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/exporters/otlp"
	"go.opentelemetry.io/otel/exporters/otlp/otlpgrpc"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/semconv"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	appsv1client "k8s.io/client-go/kubernetes/typed/apps/v1"
	_ "k8s.io/client-go/plugin/pkg/client/auth/gcp"
	"k8s.io/client-go/rest"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/cache"
	"sigs.k8s.io/controller-runtime/pkg/client"
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
	cmd.AddCommand(NewStartCommand())

	if err := cmd.Execute(); err != nil {
		fmt.Fprintf(os.Stderr, "%v\n", err)
		os.Exit(1)
	}
}

type StartOptions struct {
	Namespace             string
	DeploymentName        string
	MetricsAddr           string
	EnableLeaderElection  bool
	OperatorImage         string
	IgnitionServerImage   string
	OpenTelemetryEndpoint string
}

func NewStartCommand() *cobra.Command {
	ctrl.SetLogger(zap.New(zap.UseDevMode(true)))

	cmd := &cobra.Command{
		Use:   "run",
		Short: "Runs the Hypershift operator",
	}

	opts := StartOptions{
		Namespace:             "hypershift",
		DeploymentName:        "operator",
		MetricsAddr:           "0",
		EnableLeaderElection:  false,
		OperatorImage:         "",
		IgnitionServerImage:   "",
		OpenTelemetryEndpoint: "",
	}

	cmd.Flags().StringVar(&opts.Namespace, "namespace", opts.Namespace, "The namespace this operator lives in")
	cmd.Flags().StringVar(&opts.DeploymentName, "deployment-name", opts.DeploymentName, "The name of the deployment of this operator")
	cmd.Flags().StringVar(&opts.MetricsAddr, "metrics-addr", opts.MetricsAddr, "The address the metric endpoint binds to.")
	cmd.Flags().BoolVar(&opts.EnableLeaderElection, "enable-leader-election", opts.EnableLeaderElection,
		"Enable leader election for controller manager. "+
			"Enabling this will ensure there is only one active controller manager.")
	cmd.Flags().StringVar(&opts.OperatorImage, "operator-image", opts.OperatorImage, "A control plane operator image to use (defaults to match this operator if running in a deployment)")
	cmd.Flags().StringVar(&opts.IgnitionServerImage, "ignition-server-image", opts.IgnitionServerImage, "An ignition server image to use (defaults to match this operator if running in a deployment)")
	cmd.Flags().StringVar(&opts.OpenTelemetryEndpoint, "otlp-endpoint", opts.OpenTelemetryEndpoint, "An OpenTelemetry collector endpoint (e.g. localhost:4317). If specified, OTLP traces will be exported to this endpoint.")

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
	mgr, err := ctrl.NewManager(ctrl.GetConfigOrDie(), ctrl.Options{
		Scheme:             hyperapi.Scheme,
		MetricsBindAddress: opts.MetricsAddr,
		Port:               9443,
		LeaderElection:     opts.EnableLeaderElection,
		LeaderElectionID:   "b2ed43ca.hypershift.openshift.io",
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
		return fmt.Errorf("unable to start manager: %w", err)
	}

	// Add some flexibility to getting the operator image. Use the flag if given,
	// but if that's empty and we're running in a deployment, use the
	// hypershift operator's image by default.
	// TODO: There needs to be some strategy for specifying images everywhere
	kubeClient, err := kubernetes.NewForConfig(mgr.GetConfig())
	if err != nil {
		return fmt.Errorf("unable to create kube client: %w", err)
	}
	lookupOperatorImage := func(deployments appsv1client.DeploymentInterface, name string, userSpecifiedImage string) (string, error) {
		if len(userSpecifiedImage) > 0 {
			log.Info("using image from arguments", "image", userSpecifiedImage)
			return userSpecifiedImage, nil
		}
		deployment, err := deployments.Get(context.TODO(), name, metav1.GetOptions{})
		if err != nil {
			return "", fmt.Errorf("failed to get operator deployment: %w", err)
		}
		for _, container := range deployment.Spec.Template.Spec.Containers {
			// TODO: could use downward API for this too, overkill?
			if container.Name == "operator" {
				log.Info("using image from operator deployment", "image", container.Image)
				return container.Image, nil
			}
		}
		return "", fmt.Errorf("couldn't locate operator container on deployment")
	}
	operatorImage, err := lookupOperatorImage(kubeClient.AppsV1().Deployments(opts.Namespace), opts.DeploymentName, opts.OperatorImage)
	if err != nil {
		return fmt.Errorf("failed to find operator image: %w", err)
	}
	log.Info("using hosted control plane operator image", "operator-image", operatorImage)

	ignitionServerImage, err := lookupOperatorImage(kubeClient.AppsV1().Deployments(opts.Namespace), opts.DeploymentName, opts.IgnitionServerImage)
	if err != nil {
		return fmt.Errorf("failed to find operator image: %w", err)
	}
	log.Info("using ignition server image", "image", ignitionServerImage)

	if err = (&hostedcluster.HostedClusterReconciler{
		Client:                          mgr.GetClient(),
		HostedControlPlaneOperatorImage: operatorImage,
		IgnitionServerImage:             ignitionServerImage,
	}).SetupWithManager(mgr); err != nil {
		return fmt.Errorf("unable to create controller: %w", err)
	}

	if err := (&nodepool.NodePoolReconciler{
		Client: mgr.GetClient(),
		ReleaseProvider: &releaseinfo.CachedProvider{
			Inner: &releaseinfo.RegistryClientProvider{},
			Cache: map[string]*releaseinfo.ReleaseImage{},
		},
	}).SetupWithManager(mgr); err != nil {
		return fmt.Errorf("unable to create controller: %w", err)
	}

	// Configure OpenTelemetry
	var tracerOpts []sdktrace.TracerProviderOption
	tracerOpts = append(tracerOpts, sdktrace.WithResource(resource.NewWithAttributes(
		semconv.ServiceNameKey.String("hypershift-operator"),
	)))

	// Export to an OTLP endpoint if specified
	if len(opts.OpenTelemetryEndpoint) > 0 {
		exporter, err := otlp.NewExporter(ctx,
			otlpgrpc.NewDriver(
				otlpgrpc.WithEndpoint("localhost:4317"),
				otlpgrpc.WithInsecure(),
			))
		if err != nil {
			return fmt.Errorf("failed to initialize export pipeline: %w", err)
		}
		tracerOpts = append(tracerOpts, sdktrace.WithBatcher(exporter))
	}

	tp := sdktrace.NewTracerProvider(tracerOpts...)
	defer func() { _ = tp.Shutdown(ctx) }()

	otel.SetTracerProvider(tp)
	otel.SetTextMapPropagator(propagation.NewCompositeTextMapPropagator(propagation.Baggage{}, propagation.TraceContext{}))

	// Start the controllers
	log.Info("starting manager")
	return mgr.Start(ctx)
}

func uncachedNewClient(_ cache.Cache, config *rest.Config, options client.Options, uncachedObjects ...client.Object) (client.Client, error) {
	c, err := client.New(config, options)
	if err != nil {
		return nil, err
	}
	return c, nil
}
