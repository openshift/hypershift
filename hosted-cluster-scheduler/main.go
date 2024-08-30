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

	"github.com/go-logr/logr"
	scheduler "github.com/openshift/hypershift/hosted-cluster-scheduler/controller"
	"github.com/openshift/hypershift/pkg/version"
	hyperapi "github.com/openshift/hypershift/support/api"
	"github.com/openshift/hypershift/support/upsert"
	"github.com/spf13/cobra"
	"go.uber.org/zap/zapcore"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/wait"
	_ "k8s.io/client-go/plugin/pkg/client/auth/gcp"
	ctrl "sigs.k8s.io/controller-runtime"
	crclient "sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"
)

func main() {
	// Configure the logger to development mode for debugging using Zap logger
	ctrl.SetLogger(zap.New(zap.UseDevMode(true), zap.JSONEncoder(func(o *zapcore.EncoderConfig) {
		o.EncodeTime = zapcore.RFC3339TimeEncoder
	})))

	// hosted-cluster-scheduler cmd
	cmd := &cobra.Command{
		Use: "hosted-cluster-scheduler",
		Run: func(cmd *cobra.Command, args []string) {
			cmd.Help()
			os.Exit(1)
		},
	}

	cmd.Version = version.String()

	// hosted-cluster-scheduler subcommand
	cmd.AddCommand(NewStartCommand())

	// handling any errors for cmd "hosted-cluster-scheduler"
	if err := cmd.Execute(); err != nil {
		fmt.Fprintf(os.Stderr, "%v\n", err)
		os.Exit(1)
	}
}

// StartOptions defines the options struct for the "run" command
type StartOptions struct {
	Namespace                 string
	DeploymentName            string
	PodName                   string
	ControlPlaneOperatorImage string
	EnableCIDebugOutput       bool
}

func NewStartCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "run",
		Short: "Runs the Hosted Cluster scheduler",
	}

	// Default options
	opts := StartOptions{
		Namespace:                 "hosted-cluster",
		DeploymentName:            "scheduler",
		ControlPlaneOperatorImage: "",
	}

	// Flags to be used with "run" command
	cmd.Flags().StringVar(&opts.Namespace, "namespace", opts.Namespace, "The namespace this scheduler lives in")
	cmd.Flags().StringVar(&opts.DeploymentName, "deployment-name", opts.DeploymentName, "Legacy flag, does nothing. Use --pod-name instead.")
	cmd.Flags().StringVar(&opts.PodName, "pod-name", opts.PodName, "The name of the pod the scheduler runs in")
	cmd.Flags().StringVar(&opts.ControlPlaneOperatorImage, "control-plane-operator-image", opts.ControlPlaneOperatorImage, "A control plane operator image to use (defaults to match this scheduler if running in a deployment)")
	cmd.Flags().BoolVar(&opts.EnableCIDebugOutput, "enable-ci-debug-output", false, "If extra CI debug output should be enabled")

	cmd.Run = func(cmd *cobra.Command, args []string) {
		ctx, cancel := context.WithCancel(ctrl.SetupSignalHandler())
		defer cancel()

		if err := run(ctx, &opts, ctrl.Log.WithName("scheduler-setup")); err != nil {
			fmt.Println(err, "unable to start hosted-cluster-scheduler manager")
			os.Exit(1)
		}
	}

	return cmd
}

func run(ctx context.Context, opts *StartOptions, log logr.Logger) error {

	log.Info("Starting hosted-cluster-scheduler-manager", "version", version.String())

	restConfig := ctrl.GetConfigOrDie()
	restConfig.UserAgent = "hosted-cluster-scheduler-manager"
	leaseDuration := time.Second * 60
	renewDeadline := time.Second * 40
	retryPeriod := time.Second * 15

	mgr, err := ctrl.NewManager(restConfig, ctrl.Options{
		Scheme: hyperapi.Scheme,

		Client: crclient.Options{
			Cache: &crclient.CacheOptions{
				Unstructured: true,
			},
		},
		LeaderElection:                true,
		LeaderElectionID:              "hosted-cluster-scheduler-leader-elect",
		LeaderElectionResourceLock:    "leases",
		LeaderElectionReleaseOnCancel: true,
		LeaderElectionNamespace:       opts.Namespace,
		LeaseDuration:                 &leaseDuration,
		RenewDeadline:                 &renewDeadline,
		RetryPeriod:                   &retryPeriod,
	})

	if err != nil {
		return fmt.Errorf("unable to set up hosted-cluster-scheduler manager: %w", err)
	}

	lookupOperatorImage := func(userSpecifiedImage string) (string, error) {
		if len(userSpecifiedImage) > 0 {
			log.Info("using image from arguments", "image", userSpecifiedImage)
			return userSpecifiedImage, nil
		}
		me := &corev1.Pod{ObjectMeta: metav1.ObjectMeta{Namespace: opts.Namespace, Name: opts.PodName}}
		if err := mgr.GetAPIReader().Get(ctx, crclient.ObjectKeyFromObject(me), me); err != nil {
			return "", fmt.Errorf("failed to get hosted-cluster scheduler pod %s: %w", crclient.ObjectKeyFromObject(me), err)
		}

		for _, container := range me.Status.ContainerStatuses {
			if container.Name == "hosted-cluster-scheduler" {
				return strings.TrimPrefix(container.ImageID, "docker-pullable://"), nil
			}
		}
		return "", fmt.Errorf("couldn't locate hosted-cluster-scheduler container on deployment")
	}

	var operatorImage string
	if err := wait.PollImmediate(5*time.Second, 30*time.Second, func() (bool, error) {
		operatorImage, err = lookupOperatorImage(opts.ControlPlaneOperatorImage)
		if err != nil {
			return false, err
		}
		if operatorImage == "" {
			log.Info("hosted-cluster scheduler image is empty, retrying")
			return false, nil
		}
		return true, nil
	}); err != nil {
		return fmt.Errorf("failed to find hosted-cluster scheduler image: %w", err)
	}

	log.Info("using hosted control plane operator image", "operator-image", operatorImage)

	createOrUpdate := upsert.New(opts.EnableCIDebugOutput)

	enableSizeTagging := os.Getenv("ENABLE_SIZE_TAGGING") == "1"

	// Use the new scheduler if we support size tagging on hosted clusters
	if enableSizeTagging {
		// Start scheduler controllers
		hcScheduler := scheduler.DedicatedServingComponentSchedulerAndSizer{}
		if err := hcScheduler.SetupWithManager(ctx, mgr, createOrUpdate); err != nil {
			return fmt.Errorf("unable to create dedicated serving component scheduler/resizer controller: %w", err)
		}
		placeholderScheduler := scheduler.PlaceholderScheduler{}
		if err := placeholderScheduler.SetupWithManager(ctx, mgr); err != nil {
			return fmt.Errorf("unable to create placeholder scheduler controller: %w", err)
		}
		autoScaler := scheduler.RequestServingNodeAutoscaler{}
		if err := autoScaler.SetupWithManager(mgr); err != nil {
			return fmt.Errorf("unable to create autoscaler controller: %w", err)
		}
		deScaler := scheduler.MachineSetDescaler{}
		if err := deScaler.SetupWithManager(mgr); err != nil {
			return fmt.Errorf("unable to create machine set descaler controller: %w", err)
		}
		nonRequestServingNodeAutoscaler := scheduler.NonRequestServingNodeAutoscaler{}
		if err := nonRequestServingNodeAutoscaler.SetupWithManager(mgr); err != nil {
			return fmt.Errorf("unable to create non request serving node autoscaler controller: %w", err)
		}
	} else {
		nodeReaper := scheduler.DedicatedServingComponentNodeReaper{
			Client: mgr.GetClient(),
		}
		if err := nodeReaper.SetupWithManager(mgr); err != nil {
			return fmt.Errorf("unable to create dedicated serving component node reaper controller: %w", err)
		}
		hcScheduler := scheduler.DedicatedServingComponentScheduler{
			Client: mgr.GetClient(),
		}
		if err := hcScheduler.SetupWithManager(mgr, createOrUpdate); err != nil {
			return fmt.Errorf("unable to create dedicated serving component scheduler controller: %w", err)
		}
	}

	// Start the controllers
	log.Info("starting scheduler-manager")
	return mgr.Start(ctx)
}
