package main

import (
	"context"
	"fmt"
	"os"

	"github.com/openshift/hypershift/karpenter-operator/controllers/karpenter"
	hyperapi "github.com/openshift/hypershift/support/api"

	awskarpenterapis "github.com/aws/karpenter-provider-aws/pkg/apis"
	awskarpenterv1 "github.com/aws/karpenter-provider-aws/pkg/apis/v1"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"

	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/cache"
	"sigs.k8s.io/controller-runtime/pkg/cluster"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"
	karpenterapis "sigs.k8s.io/karpenter/pkg/apis"
	karpenterv1 "sigs.k8s.io/karpenter/pkg/apis/v1"

	"github.com/spf13/cobra"
)

var (
	setupLog = ctrl.Log.WithName("setup")

	targetKubeconfig          string
	namespace                 string
	controlPlaneOperatorImage string
)

func main() {
	var rootCmd = &cobra.Command{
		Use:   "karpenter-operator",
		Short: "Karpenter Operator is a Kubernetes operator for managing Karpenter",
		Run: func(cmd *cobra.Command, args []string) {
			opts := zap.Options{
				Development: true,
			}
			// opts.BindFlags(flag.CommandLine)
			// flag.Parse()
			ctrl.SetLogger(zap.New(zap.UseFlagOptions(&opts)))

			if err := run(ctrl.SetupSignalHandler()); err != nil {
				setupLog.Error(err, "unable to start manager")
				os.Exit(1)
			}
		},
	}

	rootCmd.PersistentFlags().StringVar(&targetKubeconfig, "target-kubeconfig", "", "Path to guest side kubeconfig file. Where the karpenter CRs (nodeClaim, nodePool, nodeClass) live")
	rootCmd.PersistentFlags().StringVar(&namespace, "namespace", "", "The namespace to infer input for reconciliation, e.g the userData secret")
	rootCmd.PersistentFlags().StringVar(&controlPlaneOperatorImage, "control-plane-operator-image", "", "The image to run the tokenMinter and the availability prober")
	rootCmd.MarkPersistentFlagRequired("target-kubeconfig")
	rootCmd.MarkPersistentFlagRequired("namespace")
	rootCmd.MarkPersistentFlagRequired("control-plane-operator-image")

	if err := rootCmd.Execute(); err != nil {
		setupLog.Error(err, "problem executing command")
		os.Exit(1)
	}
}

func run(ctx context.Context) error {
	managementKubeconfig, err := ctrl.GetConfig()
	if err != nil {
		return err
	}

	scheme := hyperapi.Scheme

	awsKarpanterGroupVersion := schema.GroupVersion{Group: awskarpenterapis.Group, Version: "v1"}
	metav1.AddToGroupVersion(scheme, awsKarpanterGroupVersion)
	scheme.AddKnownTypes(awsKarpanterGroupVersion, &awskarpenterv1.EC2NodeClass{})
	scheme.AddKnownTypes(awsKarpanterGroupVersion, &awskarpenterv1.EC2NodeClassList{})

	karpanterGroupVersion := schema.GroupVersion{Group: karpenterapis.Group, Version: "v1"}
	metav1.AddToGroupVersion(scheme, karpanterGroupVersion)
	scheme.AddKnownTypes(karpanterGroupVersion, &karpenterv1.NodeClaim{})
	scheme.AddKnownTypes(karpanterGroupVersion, &karpenterv1.NodeClaimList{})

	managementCluster, err := cluster.New(managementKubeconfig, func(opt *cluster.Options) {
		opt.Cache = cache.Options{
			DefaultNamespaces: map[string]cache.Config{namespace: {}},
			Scheme:            scheme,
		}
		opt.Scheme = scheme
	})
	if err != nil {
		return err
	}

	guestKubeconfig, err := kubeconfigFromFile(targetKubeconfig)
	if err != nil {
		return fmt.Errorf("failed to create guest kubeconfig: %w", err)
	}

	mgr, err := ctrl.NewManager(guestKubeconfig, ctrl.Options{
		Scheme:         scheme,
		LeaderElection: false,
	})
	if err != nil {
		return fmt.Errorf("failed to create manager: %w", err)
	}

	if err := mgr.Add(managementCluster); err != nil {
		return fmt.Errorf("failed to add managementCluster to controller runtime manager: %v", err)
	}

	r := karpenter.Reconciler{
		Namespace:                 namespace,
		ControlPlaneOperatorImage: controlPlaneOperatorImage,
	}
	if err := r.SetupWithManager(ctx, mgr, managementCluster); err != nil {
		return fmt.Errorf("failed to setup controller with manager: %w", err)
	}

	mac := karpenter.MachineApproverController{}
	if err := mac.SetupWithManager(mgr); err != nil {
		return fmt.Errorf("failed to setup controller with manager: %w", err)
	}

	if err := mgr.Start(ctx); err != nil {
		return fmt.Errorf("failed to start manager: %w", err)
	}

	return nil
}

func kubeconfigFromFile(path string) (*rest.Config, error) {
	cfg, err := clientcmd.NewNonInteractiveDeferredLoadingClientConfig(
		&clientcmd.ClientConfigLoadingRules{ExplicitPath: path},
		&clientcmd.ConfigOverrides{}).ClientConfig()
	if err != nil {
		return nil, fmt.Errorf("failed to construct kubeconfig from path %s: %w", path, err)
	}
	return cfg, nil
}
