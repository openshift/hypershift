package integration

import (
	"context"
	"fmt"

	cpomanifests "github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/manifests"
	"github.com/openshift/hypershift/control-plane-operator/hostedclusterconfigoperator/controllers/resources/manifests"
	"github.com/openshift/hypershift/control-plane-operator/hostedclusterconfigoperator/controllers/resources/oapi"

	corev1 "k8s.io/api/core/v1"
	utilerrors "k8s.io/apimachinery/pkg/util/errors"
	apiregistrationv1 "k8s.io/kube-aggregator/pkg/apis/apiregistration/v1"

	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// apiServerProjection describes the hosted-cluster projection of one aggregated API server
// running in the control plane. Manifest constructors stay in the manifests package.
type apiServerProjection struct {
	// name is the API server name used in error messages.
	name                string
	groups              []string
	apiService          func(group string) *apiregistrationv1.APIService
	clusterService      func() *corev1.Service
	controlPlaneService func(namespace string) *corev1.Service
	//nolint:staticcheck // SA1019: corev1.Endpoints is intentionally used for backward compatibility
	clusterEndpoints func() *corev1.Endpoints
}

var (
	openshiftAPIServer = apiServerProjection{
		name:                "openshift apiserver",
		groups:              manifests.OpenShiftAPIServerAPIServiceGroups(),
		apiService:          manifests.OpenShiftAPIServerAPIService,
		clusterService:      manifests.OpenShiftAPIServerClusterService,
		controlPlaneService: manifests.OpenShiftAPIServerService,
		clusterEndpoints:    manifests.OpenShiftAPIServerClusterEndpoints,
	}
	openshiftOAuthAPIServer = apiServerProjection{
		name:                "openshift oauth apiserver",
		groups:              manifests.OpenShiftOAuthAPIServerAPIServiceGroups(),
		apiService:          manifests.OpenShiftOAuthAPIServerAPIService,
		clusterService:      manifests.OpenShiftOAuthAPIServerClusterService,
		controlPlaneService: manifests.OpenShiftOAuthAPIServerService,
		clusterEndpoints:    manifests.OpenShiftOAuthAPIServerClusterEndpoints,
	}
)

// reconcileAPIServiceProjections preserves the original root order, log lines and error
// wrappers. Each failure is a separate entry so the root aggregate keeps them individually.
func reconcileAPIServiceProjections(ctx context.Context, hosted HostedCluster, controlPlane ControlPlane, params Params) []error {
	log := ctrl.LoggerFrom(ctx)
	var errs []error

	log.Info("reconciling openshift apiserver apiservices")
	if err := reconcileAPIServices(ctx, hosted, controlPlane, params.Namespace, openshiftAPIServer); err != nil {
		// The "service" wording predates the extraction and is kept for message stability.
		errs = append(errs, fmt.Errorf("failed to reconcile openshift apiserver service: %w", err))
	}

	log.Info("reconciling openshift apiserver service")
	if err := reconcileClusterService(ctx, hosted, openshiftAPIServer); err != nil {
		errs = append(errs, fmt.Errorf("failed to reconcile openshift apiserver service: %w", err))
	}

	log.Info("reconciling openshift apiserver endpoints")
	if err := reconcileEndpoints(ctx, hosted, controlPlane, params.Namespace, openshiftAPIServer); err != nil {
		errs = append(errs, fmt.Errorf("failed to reconcile openshift apiserver endpoints: %w", err))
	}

	if params.OAuthEnabled {
		log.Info("reconciling openshift oauth apiserver apiservices")
		if err := reconcileAPIServices(ctx, hosted, controlPlane, params.Namespace, openshiftOAuthAPIServer); err != nil {
			errs = append(errs, fmt.Errorf("failed to reconcile openshift oauth apiserver apiservices: %w", err))
		}

		log.Info("reconciling openshift oauth apiserver service")
		if err := reconcileClusterService(ctx, hosted, openshiftOAuthAPIServer); err != nil {
			errs = append(errs, fmt.Errorf("failed to reconcile openshift oauth apiserver service: %w", err))
		}

		log.Info("reconciling openshift oauth apiserver endpoints")
		if err := reconcileEndpoints(ctx, hosted, controlPlane, params.Namespace, openshiftOAuthAPIServer); err != nil {
			errs = append(errs, fmt.Errorf("failed to reconcile openshift oauth apiserver endpoints: %w", err))
		}

		log.Info("reconciling kubeadmin password hash secret")
		if err := reconcileKubeadminPasswordHashSecret(ctx, hosted, controlPlane, params.Namespace); err != nil {
			errs = append(errs, fmt.Errorf("failed to reconcile kubeadmin password hash secret: %w", err))
		}
	}

	return errs
}

// reconcileAPIServices projects every APIService group of the API server, trusting the
// control-plane root CA. A missing root CA skips all groups; group failures continue.
func reconcileAPIServices(ctx context.Context, hosted HostedCluster, controlPlane ControlPlane, namespace string, server apiServerProjection) error {
	rootCA := cpomanifests.RootCASecret(namespace)
	if err := controlPlane.get(ctx, client.ObjectKeyFromObject(rootCA), rootCA); err != nil {
		return fmt.Errorf("failed to get root ca from control plane: %w", err)
	}
	var errs []error
	for _, group := range server.groups {
		apiService := server.apiService(group)
		if _, err := hosted.upsert(ctx, apiService, func() error {
			oapi.ReconcileAPIService(apiService, server.clusterService(), rootCA, group)
			return nil
		}); err != nil {
			errs = append(errs, fmt.Errorf("failed to reconcile %s apiservice (%s): %w", server.name, group, err))
		}
	}
	return utilerrors.NewAggregate(errs)
}

func reconcileClusterService(ctx context.Context, hosted HostedCluster, server apiServerProjection) error {
	service := server.clusterService()
	_, err := hosted.upsert(ctx, service, func() error {
		oapi.ReconcileClusterService(service)
		return nil
	})
	return err
}

// reconcileEndpoints points the hosted-cluster endpoints at the control-plane service
// ClusterIP. Until the control-plane service has a ClusterIP, it reports not ready and
// leaves existing endpoints untouched.
func reconcileEndpoints(ctx context.Context, hosted HostedCluster, controlPlane ControlPlane, namespace string, server apiServerProjection) error {
	controlPlaneService := server.controlPlaneService(namespace)
	if err := controlPlane.get(ctx, client.ObjectKeyFromObject(controlPlaneService), controlPlaneService); err != nil {
		return fmt.Errorf("failed to get %s service from control plane: %w", server.name, err)
	}
	if len(controlPlaneService.Spec.ClusterIP) == 0 {
		return fmt.Errorf("%s service in control plane does not yet have a cluster IP", server.name)
	}
	endpoints := server.clusterEndpoints()
	_, err := hosted.upsert(ctx, endpoints, func() error {
		oapi.ReconcileEndpoints(endpoints, controlPlaneService.Spec.ClusterIP)
		return nil
	})
	return err
}
