package main

import (
	"context"
	"fmt"
	"os"
	"time"

	hypershiftclient "github.com/openshift/hypershift/client/clientset/clientset"
	hypershiftinformers "github.com/openshift/hypershift/client/informers/externalversions"
	"github.com/openshift/hypershift/control-plane-pki-operator/certificaterevocationcontroller"
	"github.com/openshift/hypershift/control-plane-pki-operator/certificates"
	"github.com/openshift/hypershift/control-plane-pki-operator/certificatesigningcontroller"
	"github.com/openshift/hypershift/control-plane-pki-operator/certificatesigningrequestapprovalcontroller"
	"github.com/openshift/hypershift/control-plane-pki-operator/certrotationcontroller"
	"github.com/openshift/hypershift/control-plane-pki-operator/config"
	"github.com/openshift/hypershift/control-plane-pki-operator/manifests"
	"github.com/openshift/hypershift/control-plane-pki-operator/targetconfigcontroller"

	"github.com/openshift/library-go/pkg/controller/controllercmd"
	"github.com/openshift/library-go/pkg/operator/v1helpers"

	corev1 "k8s.io/api/core/v1"
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
	hypershiftClient, err := hypershiftclient.NewForConfig(controllerContext.KubeConfig)
	if err != nil {
		return err
	}
	kubeInformersForNamespaces := v1helpers.NewKubeInformersForNamespaces(
		kubeClient,
		namespace,
		corev1.NamespaceAll,
	)
	hypershiftInformerFactory := hypershiftinformers.NewSharedInformerFactoryWithOptions(hypershiftClient, 10*time.Minute, hypershiftinformers.WithNamespace(namespace))

	hcp, err := hypershiftClient.HypershiftV1beta1().HostedControlPlanes(namespace).Get(ctx, name, metav1.GetOptions{})
	if err != nil {
		return err
	}

	targetConfigReconciler := targetconfigcontroller.NewTargetConfigController(
		hcp,
		hypershiftClient.HypershiftV1beta1(),
		kubeInformersForNamespaces,
		kubeClient,
		controllerContext.EventRecorder,
	)

	certRotationController, err := certrotationcontroller.NewCertRotationController(
		hcp,
		kubeClient,
		hypershiftClient.HypershiftV1beta1(),
		kubeInformersForNamespaces,
		controllerContext.EventRecorder,
		certRotationScale,
	)
	if err != nil {
		return err
	}

	customerCertSigningRequestApprovalController := certificatesigningrequestapprovalcontroller.NewCertificateSigningRequestApprovalController(
		hcp,
		certificates.CustomerBreakGlassSigner,
		kubeInformersForNamespaces,
		hypershiftInformerFactory,
		kubeClient,
		controllerContext.EventRecorder,
	)
	sreCertSigningRequestApprovalController := certificatesigningrequestapprovalcontroller.NewCertificateSigningRequestApprovalController(
		hcp,
		certificates.SREBreakGlassSigner,
		kubeInformersForNamespaces,
		hypershiftInformerFactory,
		kubeClient,
		controllerContext.EventRecorder,
	)

	certRevocationController := certificaterevocationcontroller.NewCertificateRevocationController(
		hcp,
		kubeInformersForNamespaces,
		hypershiftInformerFactory,
		kubeClient,
		hypershiftClient,
		controllerContext.EventRecorder,
	)

	customerSystemAdminSigner := manifests.CustomerSystemAdminSigner(namespace)
	currentCustomerCA, customerCertLoadingController := certificatesigningcontroller.NewCertificateLoadingController(
		customerSystemAdminSigner.Namespace, customerSystemAdminSigner.Name,
		kubeInformersForNamespaces,
		controllerContext.EventRecorder,
	)

	customerBreakGlassCertSigningController := certificatesigningcontroller.NewCertificateSigningController(
		hcp,
		certificates.CustomerBreakGlassSigner,
		currentCustomerCA,
		kubeInformersForNamespaces,
		kubeClient,
		controllerContext.EventRecorder,
		36*certRotationScale/24,
	)

	sreSystemAdminSigner := manifests.SRESystemAdminSigner(namespace)
	currentSRECA, sreCertLoadingController := certificatesigningcontroller.NewCertificateLoadingController(
		sreSystemAdminSigner.Namespace, sreSystemAdminSigner.Name,
		kubeInformersForNamespaces,
		controllerContext.EventRecorder,
	)
	sreBreakGlassCertSigningController := certificatesigningcontroller.NewCertificateSigningController(
		hcp,
		certificates.SREBreakGlassSigner,
		currentSRECA,
		kubeInformersForNamespaces,
		kubeClient,
		controllerContext.EventRecorder,
		36*certRotationScale/24,
	)

	kubeInformersForNamespaces.Start(ctx.Done())
	hypershiftInformerFactory.Start(ctx.Done())

	go targetConfigReconciler.Run(ctx, 1)
	go certRotationController.Run(ctx, 1)
	go customerCertSigningRequestApprovalController.Run(ctx, 1)
	go sreCertSigningRequestApprovalController.Run(ctx, 1)
	go customerCertLoadingController.Run(ctx, 1)
	go sreCertLoadingController.Run(ctx, 1)
	go customerBreakGlassCertSigningController.Run(ctx, 1)
	go sreBreakGlassCertSigningController.Run(ctx, 1)
	go certRevocationController.Run(ctx, 1)

	<-ctx.Done()
	return nil
}
