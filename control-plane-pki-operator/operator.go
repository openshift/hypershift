package main

import (
	"context"
	"fmt"
	"os"

	hypershiftv1beta1 "github.com/openshift/hypershift/client/clientset/clientset/typed/hypershift/v1beta1"
	"github.com/openshift/hypershift/control-plane-pki-operator/certrotationcontroller"
	"github.com/openshift/hypershift/control-plane-pki-operator/config"
	"github.com/openshift/hypershift/control-plane-pki-operator/targetconfigcontroller"
	"github.com/openshift/library-go/pkg/controller/controllercmd"
	"github.com/openshift/library-go/pkg/operator/v1helpers"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
)

func RunOperator(ctx context.Context, controllerContext *controllercmd.ControllerContext) error {
	var namespace, name string
	for env, target := range map[string]*string{
		"HOSTED_CONTROL_PLANE_NAMESPACE": &namespace,
		"HOSTED_CONTROL_PLANE_NAME":      &name,
	} {
		value := os.Getenv(env)
		if value == "" {
			return fmt.Errorf("$%s is required", env)
		}
		*target = value
	}

	certRotationScale, err := config.GetCertRotationScale()
	if err != nil {
		return fmt.Errorf("could not load cert rotation scale: %w", err)
	}

	// This kube client use protobuf, do not use it for CR
	kubeClient, err := kubernetes.NewForConfig(controllerContext.ProtoKubeConfig)
	if err != nil {
		return err
	}
	hypershiftClient, err := hypershiftv1beta1.NewForConfig(controllerContext.KubeConfig)
	if err != nil {
		return err
	}
	kubeInformersForNamespaces := v1helpers.NewKubeInformersForNamespaces(
		kubeClient,
		namespace,
	)

	hcp, err := hypershiftClient.HostedControlPlanes(namespace).Get(ctx, name, metav1.GetOptions{})
	if err != nil {
		return err
	}

	targetConfigReconciler := targetconfigcontroller.NewTargetConfigController(
		hcp,
		hypershiftClient,
		kubeInformersForNamespaces,
		kubeClient,
		controllerContext.EventRecorder,
	)

	certRotationController, err := certrotationcontroller.NewCertRotationController(
		hcp,
		kubeClient,
		hypershiftClient,
		kubeInformersForNamespaces,
		controllerContext.EventRecorder.WithComponentSuffix("cert-rotation-controller"),
		certRotationScale,
	)
	if err != nil {
		return err
	}

	kubeInformersForNamespaces.Start(ctx.Done())

	go targetConfigReconciler.Run(ctx, 1)
	go certRotationController.Run(ctx, 1)

	<-ctx.Done()
	return nil
}
