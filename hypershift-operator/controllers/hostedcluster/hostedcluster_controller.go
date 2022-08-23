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

package hostedcluster

import (
	"bytes"
	"context"
	"crypto"
	"crypto/rsa"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"os"
	"reflect"
	"sort"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go/aws"
	"github.com/aws/aws-sdk-go/aws/awserr"
	"github.com/aws/aws-sdk-go/service/s3"
	"github.com/aws/aws-sdk-go/service/s3/s3iface"
	"github.com/blang/semver"
	"github.com/go-logr/logr"
	"github.com/google/uuid"
	configv1 "github.com/openshift/api/config/v1"
	routev1 "github.com/openshift/api/route/v1"
	agentv1 "github.com/openshift/cluster-api-provider-agent/api/v1alpha1"
	"github.com/openshift/hypershift/api"
	"github.com/openshift/hypershift/api/util/ipnet"
	hyperv1 "github.com/openshift/hypershift/api/v1alpha1"
	"github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/autoscaler"
	ignitionserverreconciliation "github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/ignitionserver"
	"github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/kas"
	"github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/machineapprover"
	"github.com/openshift/hypershift/hypershift-operator/controllers/hostedcluster/internal/platform"
	platformaws "github.com/openshift/hypershift/hypershift-operator/controllers/hostedcluster/internal/platform/aws"
	"github.com/openshift/hypershift/hypershift-operator/controllers/manifests"
	"github.com/openshift/hypershift/hypershift-operator/controllers/manifests/clusterapi"
	"github.com/openshift/hypershift/hypershift-operator/controllers/manifests/controlplaneoperator"
	"github.com/openshift/hypershift/hypershift-operator/controllers/manifests/ignitionserver"
	"github.com/openshift/hypershift/hypershift-operator/controllers/manifests/networkpolicy"
	"github.com/openshift/hypershift/support/capabilities"
	"github.com/openshift/hypershift/support/certs"
	"github.com/openshift/hypershift/support/config"
	"github.com/openshift/hypershift/support/globalconfig"
	"github.com/openshift/hypershift/support/images"
	"github.com/openshift/hypershift/support/infraid"
	"github.com/openshift/hypershift/support/metrics"
	"github.com/openshift/hypershift/support/proxy"
	"github.com/openshift/hypershift/support/releaseinfo"
	"github.com/openshift/hypershift/support/supportedversion"
	"github.com/openshift/hypershift/support/upsert"
	hyperutil "github.com/openshift/hypershift/support/util"
	prometheusoperatorv1 "github.com/prometheus-operator/prometheus-operator/pkg/apis/monitoring/v1"
	ini "gopkg.in/ini.v1"
	jose "gopkg.in/square/go-jose.v2"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	networkingv1 "k8s.io/api/networking/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	"k8s.io/apimachinery/pkg/api/equality"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	kjson "k8s.io/apimachinery/pkg/runtime/serializer/json"
	"k8s.io/apimachinery/pkg/types"
	utilerrors "k8s.io/apimachinery/pkg/util/errors"
	"k8s.io/apimachinery/pkg/util/intstr"
	"k8s.io/apimachinery/pkg/util/sets"
	"k8s.io/client-go/util/workqueue"
	"k8s.io/utils/clock"
	k8sutilspointer "k8s.io/utils/pointer"
	capiawsv1 "sigs.k8s.io/cluster-api-provider-aws/api/v1beta1" // Need this dep atm to satisfy IBM provider dep.
	capiibmv1 "sigs.k8s.io/cluster-api-provider-ibmcloud/api/v1beta1"
	capikubevirt "sigs.k8s.io/cluster-api-provider-kubevirt/api/v1alpha1"
	capiv1 "sigs.k8s.io/cluster-api/api/v1beta1"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/apiutil"
	"sigs.k8s.io/controller-runtime/pkg/controller"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
	"sigs.k8s.io/controller-runtime/pkg/source"
)

const (
	finalizer                      = "hypershift.openshift.io/finalizer"
	HostedClusterAnnotation        = "hypershift.openshift.io/cluster"
	clusterDeletionRequeueDuration = 5 * time.Second

	// Image built from https://github.com/openshift/cluster-api/tree/release-1.0
	// Upstream canonical image comes from https://console.cloud.google.com/gcr/images/k8s-staging-cluster-api/global/
	// us.gcr.io/k8s-artifacts-prod/cluster-api/cluster-api-controller:v1.0.0
	imageCAPI                              = "registry.ci.openshift.org/hypershift/cluster-api:v1.0.0"
	ImageStreamAutoscalerImage             = "cluster-autoscaler"
	ImageStreamClusterMachineApproverImage = "cluster-machine-approver"

	controlPlaneOperatorSubcommandsLabel = "io.openshift.hypershift.control-plane-operator-subcommands"
	ignitionServerHealthzHandlerLabel    = "io.openshift.hypershift.ignition-server-healthz-handler"

	controlplaneOperatorManagesIgnitionServerLabel = "io.openshift.hypershift.control-plane-operator-manages-ignition-server"
	controlPlaneOperatorManagesMachineApprover     = "io.openshift.hypershift.control-plane-operator-manages.cluster-machine-approver"
	controlPlaneOperatorManagesMachineAutoscaler   = "io.openshift.hypershift.control-plane-operator-manages.cluster-autoscaler"
)

// NoopReconcile is just a default mutation function that does nothing.
var NoopReconcile controllerutil.MutateFn = func() error { return nil }

// HostedClusterReconciler reconciles a HostedCluster object
type HostedClusterReconciler struct {
	client.Client

	// ManagementClusterCapabilities can be asked for support of optional management cluster capabilities
	ManagementClusterCapabilities capabilities.CapabiltyChecker

	// HypershiftOperatorImage is the image used to deploy the control plane operator if
	// 1) There is no hypershift.openshift.io/control-plane-operator-image annotation on the HostedCluster and
	// 2) The OCP version being deployed is the latest version supported by Hypershift
	HypershiftOperatorImage string

	// releaseProvider looks up the OCP version for the release images in HostedClusters
	ReleaseProvider releaseinfo.ProviderWithRegistryOverrides

	// SetDefaultSecurityContext is used to configure Security Context for containers
	SetDefaultSecurityContext bool

	// Clock is used to determine the time in a testable way.
	Clock clock.WithTickerAndDelayedExecution

	EnableOCPClusterMonitoring bool

	createOrUpdate func(reconcile.Request) upsert.CreateOrUpdateFN

	EnableCIDebugOutput bool

	PrivatePlatform hyperv1.PlatformType

	OIDCStorageProviderS3BucketName string
	S3Client                        s3iface.S3API

	ImageMetadataProvider hyperutil.ImageMetadataProvider

	MetricsSet metrics.MetricsSet

	overwriteReconcile func(ctx context.Context, req ctrl.Request, log logr.Logger, hcluster *hyperv1.HostedCluster) (ctrl.Result, error)
	now                func() metav1.Time
}

// +kubebuilder:rbac:groups=hypershift.openshift.io,resources=hostedclusters,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=hypershift.openshift.io,resources=hostedclusters/status,verbs=get;update;patch

func (r *HostedClusterReconciler) SetupWithManager(mgr ctrl.Manager, createOrUpdate upsert.CreateOrUpdateProvider) error {
	if r.Clock == nil {
		r.Clock = clock.RealClock{}
	}
	if r.now == nil {
		r.now = metav1.Now
	}
	r.createOrUpdate = createOrUpdateWithAnnotationFactory(createOrUpdate)
	// Set up watches for resource types the controller manages. The list basically
	// tracks types of the resources in the clusterapi, controlplaneoperator, and
	// ignitionserver manifests packages. Since we're receiving watch events across
	// namespaces, the events are filtered to enqueue only those resources which
	// are annotated as being associated with a hostedcluster (using an annotation).
	builder := ctrl.NewControllerManagedBy(mgr).
		For(&hyperv1.HostedCluster{}).
		WithOptions(controller.Options{
			RateLimiter:             workqueue.NewItemExponentialFailureRateLimiter(1*time.Second, 10*time.Second),
			MaxConcurrentReconciles: 10,
		})
	for _, managedResource := range r.managedResources() {
		builder.Watches(&source.Kind{Type: managedResource}, handler.EnqueueRequestsFromMapFunc(enqueueParentHostedCluster))
	}

	// TODO (alberto): drop this once this is fixed upstream https://github.com/kubernetes-sigs/cluster-api-provider-aws/pull/2864.
	builder.Watches(&source.Kind{Type: &hyperv1.NodePool{}}, handler.EnqueueRequestsFromMapFunc(func(obj client.Object) []reconcile.Request {
		nodePool, ok := obj.(*hyperv1.NodePool)
		if !ok {
			return []reconcile.Request{}
		}
		return []reconcile.Request{{NamespacedName: types.NamespacedName{Namespace: nodePool.GetNamespace(), Name: nodePool.Spec.ClusterName}}}
	}))

	// Set based on SCC capability
	// When SCC is available (OpenShift), the container's security context and UID range is automatically set
	// When SCC is not available (Kubernetes), we want to explicitly set a default (non-root) security context
	r.SetDefaultSecurityContext = !r.ManagementClusterCapabilities.Has(capabilities.CapabilitySecurityContextConstraint)

	return builder.Complete(r)
}

// managedResources are all the resources that are managed as childresources for a HostedCluster
func (r *HostedClusterReconciler) managedResources() []client.Object {
	managedResources := []client.Object{
		&capiawsv1.AWSCluster{},
		&hyperv1.HostedControlPlane{},
		&capiv1.Cluster{},
		&appsv1.Deployment{},
		&prometheusoperatorv1.PodMonitor{},
		&networkingv1.NetworkPolicy{},
		&rbacv1.ClusterRole{},
		&rbacv1.ClusterRoleBinding{},
		&rbacv1.Role{},
		&rbacv1.RoleBinding{},
		&corev1.ConfigMap{},
		&corev1.Secret{},
		&corev1.Namespace{},
		&corev1.ServiceAccount{},
		&corev1.Service{},
		&agentv1.AgentCluster{},
		&capiibmv1.IBMVPCCluster{},
		&capikubevirt.KubevirtCluster{},
	}
	// Watch based on Routes capability
	if r.ManagementClusterCapabilities.Has(capabilities.CapabilityRoute) {
		managedResources = append(managedResources, &routev1.Route{})
	}
	if r.ManagementClusterCapabilities.Has(capabilities.CapabilityIngress) {
		managedResources = append(managedResources, &configv1.Ingress{})
	}
	return managedResources
}

// serviceFirstNodePortAvailable checks if the first port in a service has a node port available. Utilized to
// check status of the ignition service
func serviceFirstNodePortAvailable(svc *corev1.Service) bool {
	return svc != nil && len(svc.Spec.Ports) > 0 && svc.Spec.Ports[0].NodePort > 0

}

// pauseHostedControlPlane will handle adding the pausedUntil field to the hostedControlPlane object if it exists.
// If it doesn't exist: it returns as there's no need to add it
func pauseHostedControlPlane(ctx context.Context, c client.Client, hcp *hyperv1.HostedControlPlane, pauseValue *string) error {
	err := c.Get(ctx, client.ObjectKeyFromObject(hcp), hcp)
	if err != nil {
		if apierrors.IsNotFound(err) {
			return nil
		} else {
			return fmt.Errorf("failed to get hostedcontrolplane: %w", err)
		}
	}

	if hcp.Spec.PausedUntil != pauseValue {
		hcp.Spec.PausedUntil = pauseValue
		if err := c.Update(ctx, hcp); err != nil {
			return fmt.Errorf("failed to pause hostedcontrolplane: %w", err)
		}
	}

	return nil
}

func (r *HostedClusterReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {

	log := ctrl.LoggerFrom(ctx)
	log.Info("reconciling")

	// Look up the HostedCluster instance to reconcile
	hcluster := &hyperv1.HostedCluster{}
	err := r.Get(ctx, req.NamespacedName, hcluster)
	if err != nil {
		if apierrors.IsNotFound(err) {
			log.Info("hostedcluster not found, aborting reconcile", "name", req.NamespacedName)
			return ctrl.Result{}, nil
		}
		return ctrl.Result{}, fmt.Errorf("failed to get cluster %q: %w", req.NamespacedName, err)
	}

	var res reconcile.Result
	if r.overwriteReconcile != nil {
		res, err = r.overwriteReconcile(ctx, req, log, hcluster)
	} else {
		res, err = r.reconcile(ctx, req, log, hcluster)
	}
	condition := metav1.Condition{
		Type:               string(hyperv1.ReconciliationSucceeded),
		ObservedGeneration: hcluster.Generation,
		Status:             metav1.ConditionTrue,
		Reason:             "ReconciliatonSucceeded",
		Message:            "Reconciliation completed succesfully",
		LastTransitionTime: r.now(),
	}
	if err != nil {
		condition.Status = metav1.ConditionFalse
		condition.Reason = "ReconciliationError"
		condition.Message = err.Error()
	}
	old := meta.FindStatusCondition(hcluster.Status.Conditions, string(hyperv1.ReconciliationSucceeded))
	if old != nil {
		old.LastTransitionTime = condition.LastTransitionTime
	}
	if !reflect.DeepEqual(old, &condition) {
		meta.SetStatusCondition(&hcluster.Status.Conditions, condition)
		return res, utilerrors.NewAggregate([]error{err, r.Client.Status().Update(ctx, hcluster)})
	}

	return res, err
}

func (r *HostedClusterReconciler) reconcile(ctx context.Context, req ctrl.Request, log logr.Logger, hcluster *hyperv1.HostedCluster) (ctrl.Result, error) {
	// If deleted, clean up and return early.
	if !hcluster.DeletionTimestamp.IsZero() {
		// Keep trying to delete until we know it's safe to finalize.
		completed, err := r.delete(ctx, hcluster)
		if err != nil {
			return ctrl.Result{}, fmt.Errorf("failed to delete hostedcluster: %w", err)
		}
		if !completed {
			log.Info("hostedcluster is still deleting", "name", req.NamespacedName)
			return ctrl.Result{RequeueAfter: clusterDeletionRequeueDuration}, nil
		}
		// Now we can remove the finalizer.
		if controllerutil.ContainsFinalizer(hcluster, finalizer) {
			controllerutil.RemoveFinalizer(hcluster, finalizer)
			if err := r.Update(ctx, hcluster); err != nil {
				return ctrl.Result{}, fmt.Errorf("failed to remove finalizer from hostedcluster: %w", err)
			}
		}
		log.Info("Deleted hostedcluster", "name", req.NamespacedName)
		return ctrl.Result{}, nil
	}

	// Part zero: Handle deprecated fields.
	// This is done before anything else to prevent other update calls from failing if e.g. they are missing a new required field.
	// TODO (alberto): drop this as we cut beta and only support >= GA clusters.
	originalSpec := hcluster.Spec.DeepCopy()

	// Reconcile deprecated global configuration.
	if err := r.reconcileDeprecatedGlobalConfig(ctx, hcluster); err != nil {
		return ctrl.Result{}, err
	}

	// Reconcile deprecated AWS roles.
	switch hcluster.Spec.Platform.Type {
	case hyperv1.AWSPlatform:
		if err := r.reconcileDeprecatedAWSRoles(ctx, hcluster); err != nil {
			return ctrl.Result{}, err
		}
	}

	// Reconcile deprecated network settings
	if err := r.reconcileDeprecatedNetworkSettings(hcluster); err != nil {
		return ctrl.Result{}, err
	}

	// Update fields if required.
	if !equality.Semantic.DeepEqual(&hcluster.Spec, originalSpec) {
		log.Info("Updating deprecated fields for hosted cluster")
		return ctrl.Result{}, r.Client.Update(ctx, hcluster)
	}

	// Part one: update status

	// Set kubeconfig status
	{
		kubeConfigSecret := manifests.KubeConfigSecret(hcluster.Namespace, hcluster.Name)
		err := r.Client.Get(ctx, client.ObjectKeyFromObject(kubeConfigSecret), kubeConfigSecret)
		if err != nil {
			if !apierrors.IsNotFound(err) {
				return ctrl.Result{}, fmt.Errorf("failed to reconcile kubeconfig secret: %w", err)
			}
		} else {
			hcluster.Status.KubeConfig = &corev1.LocalObjectReference{Name: kubeConfigSecret.Name}
		}
	}

	// Set kubeadminPassword status
	{
		kubeadminPasswordSecret := manifests.KubeadminPasswordSecret(hcluster.Namespace, hcluster.Name)
		err := r.Client.Get(ctx, client.ObjectKeyFromObject(kubeadminPasswordSecret), kubeadminPasswordSecret)
		if err != nil {
			if !apierrors.IsNotFound(err) {
				return ctrl.Result{}, fmt.Errorf("failed to reconcile kubeadmin password secret: %w", err)
			}
		} else {
			hcluster.Status.KubeadminPassword = &corev1.LocalObjectReference{Name: kubeadminPasswordSecret.Name}
		}
	}

	controlPlaneNamespace := manifests.HostedControlPlaneNamespace(hcluster.Namespace, hcluster.Name)
	hcp := controlplaneoperator.HostedControlPlane(controlPlaneNamespace.Name, hcluster.Name)
	err := r.Client.Get(ctx, client.ObjectKeyFromObject(hcp), hcp)
	if err != nil {
		if !apierrors.IsNotFound(err) {
			return ctrl.Result{}, fmt.Errorf("failed to get hostedcontrolplane: %w", err)
		} else {
			hcp = nil
		}
	}

	// Set version status
	{
		hcluster.Status.Version = computeClusterVersionStatus(r.Clock, hcluster, hcp)
	}

	// Set the ClusterVersionSucceeding based on the hostedcontrolplane
	{
		condition := metav1.Condition{
			Type:               string(hyperv1.ClusterVersionSucceeding),
			Status:             metav1.ConditionUnknown,
			Reason:             hyperv1.ClusterVersionStatusUnknownReason,
			ObservedGeneration: hcluster.Generation,
		}
		if hcp != nil {
			failingCond := meta.FindStatusCondition(hcp.Status.Conditions, string(hyperv1.ClusterVersionFailing))
			if failingCond != nil {
				condition.Reason = failingCond.Reason
				condition.Message = failingCond.Message
				if failingCond.Status == metav1.ConditionTrue {
					condition.Status = metav1.ConditionFalse
				} else {
					condition.Status = metav1.ConditionTrue
				}
			}
		}
		meta.SetStatusCondition(&hcluster.Status.Conditions, condition)
	}

	// Copy the ClusterVersionUpgradeable condition on the hostedcontrolplane
	{
		condition := &metav1.Condition{
			Type:               string(hyperv1.ClusterVersionUpgradeable),
			Status:             metav1.ConditionUnknown,
			Reason:             hyperv1.ClusterVersionStatusUnknownReason,
			Message:            "The hosted control plane is not found",
			ObservedGeneration: hcluster.Generation,
		}
		if hcp != nil {
			upgradeableCondition := meta.FindStatusCondition(hcp.Status.Conditions, string(hyperv1.ClusterVersionUpgradeable))
			if upgradeableCondition != nil {
				condition = upgradeableCondition
				if condition.Status == metav1.ConditionTrue {
					condition.Message = "The hosted cluster is upgradable"
				}
			}
		}
		meta.SetStatusCondition(&hcluster.Status.Conditions, *condition)
	}

	// Copy the Degraded condition on the hostedcontrolplane
	{
		condition := &metav1.Condition{
			Type:               string(hyperv1.HostedClusterDegraded),
			Status:             metav1.ConditionUnknown,
			Reason:             hyperv1.ClusterVersionStatusUnknownReason,
			Message:            "The hosted control plane is not found",
			ObservedGeneration: hcluster.Generation,
		}
		if hcp != nil {
			degradedCondition := meta.FindStatusCondition(hcp.Status.Conditions, string(hyperv1.HostedControlPlaneDegraded))
			if degradedCondition != nil {
				condition = degradedCondition
				condition.Type = string(hyperv1.HostedClusterDegraded)
				if condition.Status == metav1.ConditionFalse {
					condition.Message = "The hosted cluster is not degraded"
				}
			}
		}
		condition.ObservedGeneration = hcluster.Generation
		meta.SetStatusCondition(&hcluster.Status.Conditions, *condition)
	}

	// Reconcile unmanaged etcd client tls secret validation error status. Note only update status on validation error case to
	// provide clear status to the user on the resource without having to look at operator logs.
	{
		if hcluster.Spec.Etcd.ManagementType == hyperv1.Unmanaged {
			unmanagedEtcdTLSClientSecret := &corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{
					Namespace: hcluster.GetNamespace(),
					Name:      hcluster.Spec.Etcd.Unmanaged.TLS.ClientSecret.Name,
				},
			}
			if err := r.Client.Get(ctx, client.ObjectKeyFromObject(unmanagedEtcdTLSClientSecret), unmanagedEtcdTLSClientSecret); err != nil {
				if apierrors.IsNotFound(err) {
					unmanagedEtcdTLSClientSecret = nil
				} else {
					return ctrl.Result{}, fmt.Errorf("failed to get unmanaged etcd tls secret: %w", err)
				}
			}
			meta.SetStatusCondition(&hcluster.Status.Conditions, computeUnmanagedEtcdAvailability(hcluster, unmanagedEtcdTLSClientSecret))
		}
	}

	// Set the Available condition
	// TODO: This is really setting something that could be more granular like
	// HostedControlPlaneAvailable, and then the HostedCluster high-level Available
	// condition could be computed as a function of the granular ThingAvailable
	// conditions (so that it could incorporate e.g. HostedControlPlane and IgnitionServer
	// availability in the ultimate HostedCluster Available condition)
	{
		meta.SetStatusCondition(&hcluster.Status.Conditions, computeHostedClusterAvailability(hcluster, hcp))
	}

	// Set ValidConfiguration condition
	{
		condition := metav1.Condition{
			Type:               string(hyperv1.ValidHostedClusterConfiguration),
			ObservedGeneration: hcluster.Generation,
		}
		if err := r.validateConfigAndClusterCapabilities(ctx, hcluster); err != nil {
			condition.Status = metav1.ConditionFalse
			condition.Message = err.Error()
			condition.Reason = hyperv1.InvalidConfigurationReason
		} else {
			condition.Status = metav1.ConditionTrue
			condition.Message = "Configuration passes validation"
			condition.Reason = hyperv1.HostedClusterAsExpectedReason
		}
		meta.SetStatusCondition(&hcluster.Status.Conditions, condition)
	}

	// Set SupportedHostedCluster condition
	{
		condition := metav1.Condition{
			Type:               string(hyperv1.SupportedHostedCluster),
			ObservedGeneration: hcluster.Generation,
		}
		if err := r.validateHostedClusterSupport(hcluster); err != nil {
			condition.Status = metav1.ConditionFalse
			condition.Message = err.Error()
			condition.Reason = hyperv1.UnsupportedHostedClusterReason
		} else {
			condition.Status = metav1.ConditionTrue
			condition.Message = "HostedCluster is supported by operator configuration"
			condition.Reason = hyperv1.HostedClusterAsExpectedReason
		}
		meta.SetStatusCondition(&hcluster.Status.Conditions, condition)
	}

	// Set ValidHostedControlPlaneConfiguration condition
	{
		condition := metav1.Condition{
			Type:   string(hyperv1.ValidHostedControlPlaneConfiguration),
			Status: metav1.ConditionUnknown,
			Reason: hyperv1.StatusUnknownReason,
		}
		if hcp != nil {
			validConfigHCPCondition := meta.FindStatusCondition(hcp.Status.Conditions, string(hyperv1.ValidHostedControlPlaneConfiguration))
			if validConfigHCPCondition != nil {
				condition.Status = validConfigHCPCondition.Status
				condition.Message = validConfigHCPCondition.Message
				condition.Reason = validConfigHCPCondition.Reason
			}
		}
		meta.SetStatusCondition(&hcluster.Status.Conditions, condition)
	}

	// Set Ignition Server endpoint
	{
		serviceStrategy := servicePublishingStrategyByType(hcluster, hyperv1.Ignition)
		if serviceStrategy == nil {
			// We don't return the error here as reconciling won't solve the input problem.
			// An update event will trigger reconciliation.
			log.Error(fmt.Errorf("ignition server service strategy not specified"), "")
			return ctrl.Result{}, nil
		}
		switch serviceStrategy.Type {
		case hyperv1.Route:
			if serviceStrategy.Route != nil && serviceStrategy.Route.Hostname != "" {
				hcluster.Status.IgnitionEndpoint = serviceStrategy.Route.Hostname
			} else {
				ignitionServerRoute := ignitionserver.Route(controlPlaneNamespace.GetName())
				if err := r.Client.Get(ctx, client.ObjectKeyFromObject(ignitionServerRoute), ignitionServerRoute); err != nil {
					if !apierrors.IsNotFound(err) {
						return ctrl.Result{}, fmt.Errorf("failed to get ignitionServerRoute: %w", err)
					}
				}
				if err == nil && ignitionServerRoute.Spec.Host != "" {
					hcluster.Status.IgnitionEndpoint = ignitionServerRoute.Spec.Host
				}
			}
		case hyperv1.NodePort:
			if serviceStrategy.NodePort == nil {
				// We don't return the error here as reconciling won't solve the input problem.
				// An update event will trigger reconciliation.
				log.Error(fmt.Errorf("nodeport metadata not specified for ignition service"), "")
				return ctrl.Result{}, nil
			}
			ignitionService := ignitionserver.Service(controlPlaneNamespace.GetName())
			if err := r.Client.Get(ctx, client.ObjectKeyFromObject(ignitionService), ignitionService); err != nil {
				if !apierrors.IsNotFound(err) {
					return ctrl.Result{}, fmt.Errorf("failed to get ignition service: %w", err)
				}
			}
			if err == nil && serviceFirstNodePortAvailable(ignitionService) {
				hcluster.Status.IgnitionEndpoint = fmt.Sprintf("%s:%d", serviceStrategy.NodePort.Address, ignitionService.Spec.Ports[0].NodePort)
			}
		default:
			// We don't return the error here as reconciling won't solve the input problem.
			// An update event will trigger reconciliation.
			log.Error(fmt.Errorf("unknown service strategy type for ignition service: %s", serviceStrategy.Type), "")
			return ctrl.Result{}, nil
		}
	}

	// Set the OAuth URL
	{
		if hcp != nil {
			hcluster.Status.OAuthCallbackURLTemplate = hcp.Status.OAuthCallbackURLTemplate
		}
	}

	// Set the ignition server availability condition by checking its deployment.
	{
		// Assume the server is unavailable unless proven otherwise.
		newCondition := metav1.Condition{
			Type:   string(hyperv1.IgnitionEndpointAvailable),
			Status: metav1.ConditionUnknown,
			Reason: hyperv1.IgnitionServerDeploymentStatusUnknownReason,
		}
		// Check to ensure the deployment exists and is available.
		deployment := ignitionserver.Deployment(controlPlaneNamespace.Name)
		if err := r.Client.Get(ctx, client.ObjectKeyFromObject(deployment), deployment); err != nil {
			if apierrors.IsNotFound(err) {
				newCondition = metav1.Condition{
					Type:    string(hyperv1.IgnitionEndpointAvailable),
					Status:  metav1.ConditionFalse,
					Reason:  hyperv1.IgnitionServerDeploymentNotFoundReason,
					Message: "Ignition server deployment not found",
				}
			} else {
				return ctrl.Result{}, fmt.Errorf("failed to get ignition server deployment: %w", err)
			}
		} else {
			// Assume the deployment is unavailable until proven otherwise.
			newCondition = metav1.Condition{
				Type:    string(hyperv1.IgnitionEndpointAvailable),
				Status:  metav1.ConditionFalse,
				Reason:  hyperv1.IgnitionServerDeploymentUnavailableReason,
				Message: "Ignition server deployment is not yet available",
			}
			for _, cond := range deployment.Status.Conditions {
				if cond.Type == appsv1.DeploymentAvailable && cond.Status == corev1.ConditionTrue {
					newCondition = metav1.Condition{
						Type:    string(hyperv1.IgnitionEndpointAvailable),
						Status:  metav1.ConditionTrue,
						Reason:  hyperv1.IgnitionServerDeploymentAsExpectedReason,
						Message: "Ignition server deployent is available",
					}
					break
				}
			}
		}
		newCondition.ObservedGeneration = hcluster.Generation
		meta.SetStatusCondition(&hcluster.Status.Conditions, newCondition)
	}
	meta.SetStatusCondition(&hcluster.Status.Conditions, hyperutil.GenerateReconciliationActiveCondition(hcluster.Spec.PausedUntil, hcluster.Generation))

	// Set ValidReleaseImage condition
	{
		condition := meta.FindStatusCondition(hcluster.Status.Conditions, string(hyperv1.ValidReleaseImage))

		// This check can be expensive looking up release image versions
		// (hopefully they are cached).  Skip if we have already observed for
		// this generation.
		if condition == nil || condition.ObservedGeneration != hcluster.Generation || condition.Status != metav1.ConditionTrue {
			condition := metav1.Condition{
				Type:               string(hyperv1.ValidReleaseImage),
				ObservedGeneration: hcluster.Generation,
			}
			if err := r.validateReleaseImage(ctx, hcluster); err != nil {
				condition.Status = metav1.ConditionFalse
				condition.Message = err.Error()
				condition.Reason = hyperv1.InvalidImageReason
			} else {
				condition.Status = metav1.ConditionTrue
				condition.Message = "Release image is valid"
				condition.Reason = hyperv1.AsExpectedReason
			}
			meta.SetStatusCondition(&hcluster.Status.Conditions, condition)
		}
	}

	// Set Progressing condition
	{
		condition := metav1.Condition{
			Type:               string(hyperv1.HostedClusterProgressing),
			ObservedGeneration: hcluster.Generation,
			Status:             metav1.ConditionFalse,
			Message:            "HostedCluster is at expected version",
			Reason:             hyperv1.AsExpectedReason,
		}
		progressing, err := isProgressing(ctx, hcluster)
		if err != nil {
			condition.Status = metav1.ConditionFalse
			condition.Message = err.Error()
			condition.Reason = "Blocked"
		}
		if progressing {
			condition.Status = metav1.ConditionTrue
			condition.Message = "HostedCluster is deploying, upgrading, or reconfiguring"
			condition.Reason = "Progressing"
		}
		meta.SetStatusCondition(&hcluster.Status.Conditions, condition)
	}

	// Persist status updates
	if err := r.Client.Status().Update(ctx, hcluster); err != nil {
		if apierrors.IsConflict(err) {
			return ctrl.Result{Requeue: true}, nil
		}
		return ctrl.Result{}, fmt.Errorf("failed to update status: %w", err)
	}

	// Part two: reconcile the state of the world

	// Ensure the cluster has a finalizer for cleanup and update right away.
	if !controllerutil.ContainsFinalizer(hcluster, finalizer) {
		controllerutil.AddFinalizer(hcluster, finalizer)
		if err := r.Update(ctx, hcluster); err != nil {
			if apierrors.IsConflict(err) {
				return ctrl.Result{Requeue: true}, nil
			}
			return ctrl.Result{}, fmt.Errorf("failed to add finalizer to cluster: %w", err)
		}
	}

	// if paused: ensure associated HostedControlPlane (if it exists) is also paused and stop reconciliation
	if isPaused, duration := hyperutil.IsReconciliationPaused(log, hcluster.Spec.PausedUntil); isPaused {
		if err := pauseHostedControlPlane(ctx, r.Client, hcp, hcluster.Spec.PausedUntil); err != nil {
			return ctrl.Result{}, err
		}
		log.Info("Reconciliation paused", "name", req.NamespacedName, "pausedUntil", *hcluster.Spec.PausedUntil)
		return ctrl.Result{RequeueAfter: duration}, nil
	}

	if err := r.defaultClusterIDsIfNeeded(ctx, hcluster); err != nil {
		return ctrl.Result{}, err
	}

	if err := r.defaultAPIPortIfNeeded(ctx, hcluster); err != nil {
		return ctrl.Result{}, fmt.Errorf("failed to default the apiserver port: %w", err)
	}

	// Set the infraID as Tag on all created AWS
	if err := r.reconcileAWSResourceTags(ctx, hcluster); err != nil {
		return ctrl.Result{}, err
	}

	// Block here if the cluster configuration does not pass validation
	{
		validConfig := meta.FindStatusCondition(hcluster.Status.Conditions, string(hyperv1.ValidHostedClusterConfiguration))
		if validConfig != nil && validConfig.Status == metav1.ConditionFalse {
			log.Error(fmt.Errorf("configuration is invalid"), "reconciliation is blocked", "message", validConfig.Message)
			return ctrl.Result{}, nil
		}
		supportedHostedCluster := meta.FindStatusCondition(hcluster.Status.Conditions, string(hyperv1.SupportedHostedCluster))
		if supportedHostedCluster != nil && supportedHostedCluster.Status == metav1.ConditionFalse {
			log.Error(fmt.Errorf("not supported by operator configuration"), "reconciliation is blocked", "message", supportedHostedCluster.Message)
			return ctrl.Result{}, nil
		}
		validReleaseImage := meta.FindStatusCondition(hcluster.Status.Conditions, string(hyperv1.ValidReleaseImage))
		if validReleaseImage != nil && validReleaseImage.Status == metav1.ConditionFalse {
			log.Error(fmt.Errorf("release image is invalid"), "reconciliation is blocked", "message", validReleaseImage.Message)
			return ctrl.Result{}, nil
		}
		upgrading, msg, err := isUpgradeable(hcluster)
		if upgrading {
			if err != nil {
				log.Error(err, "reconciliation is blocked", "message", validReleaseImage.Message)
				return ctrl.Result{}, nil
			}
			if msg != "" {
				log.Info(msg)
			}
		}
	}

	createOrUpdate := r.createOrUpdate(req)

	// Reconcile the hosted cluster namespace
	_, err = createOrUpdate(ctx, r.Client, controlPlaneNamespace, func() error {
		if controlPlaneNamespace.Labels == nil {
			controlPlaneNamespace.Labels = make(map[string]string)
		}
		controlPlaneNamespace.Labels["hypershift.openshift.io/hosted-control-plane"] = ""
		if r.EnableOCPClusterMonitoring {
			controlPlaneNamespace.Labels["openshift.io/cluster-monitoring"] = "true"
		}
		return nil
	})
	if err != nil {
		return ctrl.Result{}, fmt.Errorf("failed to reconcile namespace: %w", err)
	}

	var pullSecret corev1.Secret
	if err := r.Client.Get(ctx, types.NamespacedName{Namespace: hcluster.Namespace, Name: hcluster.Spec.PullSecret.Name}, &pullSecret); err != nil {
		return ctrl.Result{}, fmt.Errorf("failed to get pull secret: %w", err)
	}
	pullSecretBytes, ok := pullSecret.Data[corev1.DockerConfigJsonKey]
	if !ok {
		return ctrl.Result{}, fmt.Errorf("expected %s key in pull secret", corev1.DockerConfigJsonKey)
	}
	controlPlaneOperatorImage, err := GetControlPlaneOperatorImage(ctx, hcluster, r.ReleaseProvider, r.HypershiftOperatorImage, pullSecretBytes)
	if err != nil {
		return ctrl.Result{}, fmt.Errorf("failed to get controlPlaneOperatorImage: %w", err)
	}
	controlPlaneOperatorImageMetadata, err := r.ImageMetadataProvider.ImageMetadata(ctx, controlPlaneOperatorImage, pullSecretBytes)
	if err != nil {
		return ctrl.Result{}, fmt.Errorf("failed to look up image metadata for %s: %w", controlPlaneOperatorImage, err)
	}
	cpoHasUtilities := false
	if _, hasLabel := hyperutil.ImageLabels(controlPlaneOperatorImageMetadata)[controlPlaneOperatorSubcommandsLabel]; hasLabel {
		cpoHasUtilities = true
	}
	utilitiesImage := controlPlaneOperatorImage
	if !cpoHasUtilities {
		utilitiesImage = r.HypershiftOperatorImage
	}
	_, ignitionServerHasHealthzHandler := hyperutil.ImageLabels(controlPlaneOperatorImageMetadata)[ignitionServerHealthzHandlerLabel]
	_, controlplaneOperatorManagesIgnitionServer := hyperutil.ImageLabels(controlPlaneOperatorImageMetadata)[controlplaneOperatorManagesIgnitionServerLabel]

	p, err := platform.GetPlatform(hcluster, utilitiesImage)
	if err != nil {
		return ctrl.Result{}, err
	}

	// Reconcile Platform specifics.
	{
		if err := p.ReconcileCredentials(ctx, r.Client, createOrUpdate, hcluster, controlPlaneNamespace.Name); err != nil {
			meta.SetStatusCondition(&hcluster.Status.Conditions, metav1.Condition{
				Type:               string(hyperv1.PlatformCredentialsFound),
				Status:             metav1.ConditionFalse,
				Reason:             hyperv1.PlatformCredentialsNotFoundReason,
				ObservedGeneration: hcluster.Generation,
				Message:            err.Error(),
			})
			if statusErr := r.Client.Status().Update(ctx, hcluster); statusErr != nil {
				return ctrl.Result{}, fmt.Errorf("failed to reconcile platform credentials: %s, failed to update status: %w", err, statusErr)
			}
			return ctrl.Result{}, fmt.Errorf("failed to reconcile platform credentials: %w", err)
		}
		if meta.IsStatusConditionFalse(hcluster.Status.Conditions, string(hyperv1.PlatformCredentialsFound)) {
			meta.SetStatusCondition(&hcluster.Status.Conditions, metav1.Condition{
				Type:               string(hyperv1.PlatformCredentialsFound),
				Status:             metav1.ConditionTrue,
				Reason:             hyperv1.AsExpectedReason,
				ObservedGeneration: hcluster.Generation,
				Message:            "Required platform credentials are found",
			})
			if statusErr := r.Client.Status().Update(ctx, hcluster); statusErr != nil {
				return ctrl.Result{}, fmt.Errorf("failed to reconcile platform credentials: %s, failed to update status: %w", err, statusErr)
			}
		}
	}

	// Reconcile the HostedControlPlane pull secret by resolving the source secret
	// reference from the HostedCluster and syncing the secret in the control plane namespace.
	{
		var src corev1.Secret
		if err := r.Client.Get(ctx, client.ObjectKey{Namespace: hcluster.GetNamespace(), Name: hcluster.Spec.PullSecret.Name}, &src); err != nil {
			return ctrl.Result{}, fmt.Errorf("failed to get pull secret %s: %w", hcluster.Spec.PullSecret.Name, err)
		}
		dst := controlplaneoperator.PullSecret(controlPlaneNamespace.Name)
		_, err = createOrUpdate(ctx, r.Client, dst, func() error {
			srcData, srcHasData := src.Data[".dockerconfigjson"]
			if !srcHasData {
				return fmt.Errorf("hostedcluster pull secret %q must have a .dockerconfigjson key", src.Name)
			}
			dst.Type = corev1.SecretTypeDockerConfigJson
			if dst.Data == nil {
				dst.Data = map[string][]byte{}
			}
			dst.Data[".dockerconfigjson"] = srcData
			return nil
		})
		if err != nil {
			return ctrl.Result{}, fmt.Errorf("failed to reconcile pull secret: %w", err)
		}
	}

	// Reconcile the HostedControlPlane Secret Encryption Info
	if hcluster.Spec.SecretEncryption != nil {
		log.Info("Reconciling secret encryption configuration")
		switch hcluster.Spec.SecretEncryption.Type {
		case hyperv1.AESCBC:
			if hcluster.Spec.SecretEncryption.AESCBC == nil || len(hcluster.Spec.SecretEncryption.AESCBC.ActiveKey.Name) == 0 {
				log.Error(fmt.Errorf("aescbc metadata  is nil"), "")
				// don't return error here as reconciling won't fix input error
				return ctrl.Result{}, nil
			}
			var src corev1.Secret
			if err := r.Client.Get(ctx, client.ObjectKey{Namespace: hcluster.GetNamespace(), Name: hcluster.Spec.SecretEncryption.AESCBC.ActiveKey.Name}, &src); err != nil {
				return ctrl.Result{}, fmt.Errorf("failed to get active aescbc secret %s: %w", hcluster.Spec.SecretEncryption.AESCBC.ActiveKey.Name, err)
			}
			if _, ok := src.Data[hyperv1.AESCBCKeySecretKey]; !ok {
				log.Error(fmt.Errorf("no key field %s specified for aescbc active key secret", hyperv1.AESCBCKeySecretKey), "")
				// don't return error here as reconciling won't fix input error
				return ctrl.Result{}, nil
			}
			hostedControlPlaneActiveKeySecret := &corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{
					Namespace: controlPlaneNamespace.Name,
					Name:      src.Name,
				},
			}
			_, err = createOrUpdate(ctx, r.Client, hostedControlPlaneActiveKeySecret, func() error {
				if hostedControlPlaneActiveKeySecret.Data == nil {
					hostedControlPlaneActiveKeySecret.Data = map[string][]byte{}
				}
				hostedControlPlaneActiveKeySecret.Data[hyperv1.AESCBCKeySecretKey] = src.Data[hyperv1.AESCBCKeySecretKey]
				hostedControlPlaneActiveKeySecret.Type = corev1.SecretTypeOpaque
				return nil
			})
			if err != nil {
				return ctrl.Result{}, fmt.Errorf("failed reconciling aescbc active key: %w", err)
			}
			if hcluster.Spec.SecretEncryption.AESCBC.BackupKey != nil && len(hcluster.Spec.SecretEncryption.AESCBC.BackupKey.Name) > 0 {
				var src corev1.Secret
				if err := r.Client.Get(ctx, client.ObjectKey{Namespace: hcluster.GetNamespace(), Name: hcluster.Spec.SecretEncryption.AESCBC.BackupKey.Name}, &src); err != nil {
					return ctrl.Result{}, fmt.Errorf("failed to get backup aescbc secret %s: %w", hcluster.Spec.SecretEncryption.AESCBC.BackupKey.Name, err)
				}
				if _, ok := src.Data[hyperv1.AESCBCKeySecretKey]; !ok {
					log.Error(fmt.Errorf("no key field %s specified for aescbc backup key secret", hyperv1.AESCBCKeySecretKey), "")
					// don't return error here as reconciling won't fix input error
					return ctrl.Result{}, nil
				}
				hostedControlPlaneBackupKeySecret := &corev1.Secret{
					ObjectMeta: metav1.ObjectMeta{
						Namespace: controlPlaneNamespace.Name,
						Name:      src.Name,
					},
				}
				_, err = createOrUpdate(ctx, r.Client, hostedControlPlaneBackupKeySecret, func() error {
					if hostedControlPlaneBackupKeySecret.Data == nil {
						hostedControlPlaneBackupKeySecret.Data = map[string][]byte{}
					}
					hostedControlPlaneBackupKeySecret.Data[hyperv1.AESCBCKeySecretKey] = src.Data[hyperv1.AESCBCKeySecretKey]
					hostedControlPlaneBackupKeySecret.Type = corev1.SecretTypeOpaque
					return nil
				})
				if err != nil {
					return ctrl.Result{}, fmt.Errorf("failed reconciling aescbc backup key: %w", err)
				}
			}
		case hyperv1.KMS:
			if hcluster.Spec.SecretEncryption.KMS == nil {
				log.Error(fmt.Errorf("kms metadata nil"), "")
				// don't return error here as reconciling won't fix input error
				return ctrl.Result{}, nil
			}
			if err := p.ReconcileSecretEncryption(ctx, r.Client, createOrUpdate,
				hcluster,
				controlPlaneNamespace.Name); err != nil {
				return ctrl.Result{}, err
			}
		default:
			log.Error(fmt.Errorf("unsupported encryption type %s", hcluster.Spec.SecretEncryption.Type), "")
			// don't return error here as reconciling won't fix input error
			return ctrl.Result{}, nil
		}
	}

	// Reconcile the HostedControlPlane audit webhook config if specified
	// reference from the HostedCluster and syncing the secret in the control plane namespace.
	{
		if hcluster.Spec.AuditWebhook != nil && len(hcluster.Spec.AuditWebhook.Name) > 0 {
			var src corev1.Secret
			if err := r.Client.Get(ctx, client.ObjectKey{Namespace: hcluster.GetNamespace(), Name: hcluster.Spec.AuditWebhook.Name}, &src); err != nil {
				return ctrl.Result{}, fmt.Errorf("failed to get audit webhook config %s: %w", hcluster.Spec.AuditWebhook.Name, err)
			}
			configData, ok := src.Data[hyperv1.AuditWebhookKubeconfigKey]
			if !ok {
				return ctrl.Result{}, fmt.Errorf("audit webhook secret does not contain key %s", hyperv1.AuditWebhookKubeconfigKey)
			}

			hostedControlPlaneAuditWebhookSecret := &corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{
					Namespace: controlPlaneNamespace.Name,
					Name:      src.Name,
				},
			}
			_, err = createOrUpdate(ctx, r.Client, hostedControlPlaneAuditWebhookSecret, func() error {
				if hostedControlPlaneAuditWebhookSecret.Data == nil {
					hostedControlPlaneAuditWebhookSecret.Data = map[string][]byte{}
				}
				hostedControlPlaneAuditWebhookSecret.Data[hyperv1.AuditWebhookKubeconfigKey] = configData
				hostedControlPlaneAuditWebhookSecret.Type = corev1.SecretTypeOpaque
				return nil
			})
			if err != nil {
				return ctrl.Result{}, fmt.Errorf("failed reconciling audit webhook secret: %w", err)
			}
		}
	}

	// Reconcile the HostedControlPlane SSH secret by resolving the source secret reference
	// from the HostedCluster and syncing the secret in the control plane namespace.
	if len(hcluster.Spec.SSHKey.Name) > 0 {
		var src corev1.Secret
		err = r.Client.Get(ctx, client.ObjectKey{Namespace: hcluster.Namespace, Name: hcluster.Spec.SSHKey.Name}, &src)
		if err != nil {
			return ctrl.Result{}, fmt.Errorf("failed to get hostedcluster SSH key secret %s: %w", hcluster.Spec.SSHKey.Name, err)
		}
		dest := controlplaneoperator.SSHKey(controlPlaneNamespace.Name)
		_, err = createOrUpdate(ctx, r.Client, dest, func() error {
			srcData, srcHasData := src.Data["id_rsa.pub"]
			if !srcHasData {
				return fmt.Errorf("hostedcluster ssh key secret %q must have a id_rsa.pub key", src.Name)
			}
			dest.Type = corev1.SecretTypeOpaque
			if dest.Data == nil {
				dest.Data = map[string][]byte{}
			}
			dest.Data["id_rsa.pub"] = srcData
			return nil
		})
		if err != nil {
			return ctrl.Result{}, fmt.Errorf("failed to reconcile controlplane ssh secret: %w", err)
		}
	}

	// Reconcile the HostedControlPlane AdditionalTrustBundle ConfigMap by resolving the source reference
	// from the HostedCluster and syncing the CM in the control plane namespace.
	if hcluster.Spec.AdditionalTrustBundle != nil {
		var src corev1.ConfigMap
		err = r.Client.Get(ctx, client.ObjectKey{Namespace: hcluster.Namespace, Name: hcluster.Spec.AdditionalTrustBundle.Name}, &src)
		if err != nil {
			return ctrl.Result{}, fmt.Errorf("failed to get hostedcluster AdditionalTrustBundle ConfigMap %s: %w", hcluster.Spec.AdditionalTrustBundle.Name, err)
		}
		dest := controlplaneoperator.UserCABundle(controlPlaneNamespace.Name)
		_, err = createOrUpdate(ctx, r.Client, dest, func() error {
			srcData, srcHasData := src.Data["ca-bundle.crt"]
			if !srcHasData {
				return fmt.Errorf("hostedcluster configmap %q must have a ca-bundle.crt key", src.Name)
			}
			if dest.Data == nil {
				dest.Data = map[string]string{}
			}
			dest.Data["ca-bundle.crt"] = srcData
			return nil
		})
		if err != nil {
			return ctrl.Result{}, fmt.Errorf("failed to reconcile controlplane AdditionalTrustBundle: %w", err)
		}
	}

	// Reconcile the service account signing key if set
	if hcluster.Spec.ServiceAccountSigningKey != nil {
		if err := r.reconcileServiceAccountSigningKey(ctx, hcluster, controlPlaneNamespace.Name, createOrUpdate); err != nil {
			return ctrl.Result{}, fmt.Errorf("failed to reconcile service account signing key: %w", err)
		}
	}

	// Reconcile etcd client MTLS secret if the control plane is using an unmanaged etcd cluster
	if hcluster.Spec.Etcd.ManagementType == hyperv1.Unmanaged {
		unmanagedEtcdTLSClientSecret := &corev1.Secret{
			ObjectMeta: metav1.ObjectMeta{
				Namespace: hcluster.GetNamespace(),
				Name:      hcluster.Spec.Etcd.Unmanaged.TLS.ClientSecret.Name,
			},
		}
		if err := r.Client.Get(ctx, client.ObjectKeyFromObject(unmanagedEtcdTLSClientSecret), unmanagedEtcdTLSClientSecret); err != nil {
			return ctrl.Result{}, fmt.Errorf("failed to get unmanaged etcd tls secret: %w", err)
		}
		hostedControlPlaneEtcdClientSecret := &corev1.Secret{
			ObjectMeta: metav1.ObjectMeta{
				Namespace: controlPlaneNamespace.Name,
				Name:      hcluster.Spec.Etcd.Unmanaged.TLS.ClientSecret.Name,
			},
		}
		if result, err := createOrUpdate(ctx, r.Client, hostedControlPlaneEtcdClientSecret, func() error {
			if hostedControlPlaneEtcdClientSecret.Data == nil {
				hostedControlPlaneEtcdClientSecret.Data = map[string][]byte{}
			}
			hostedControlPlaneEtcdClientSecret.Data = unmanagedEtcdTLSClientSecret.Data
			hostedControlPlaneEtcdClientSecret.Type = corev1.SecretTypeOpaque
			return nil
		}); err != nil {
			return ctrl.Result{}, fmt.Errorf("failed reconciling etcd client secret: %w", err)
		} else {
			log.Info("reconciled etcd client mtls secret to control plane namespace", "result", result)
		}
	}

	// Reconcile global config related configmaps and secrets
	{
		if hcluster.Spec.Configuration != nil {
			configMapRefs := globalconfig.ConfigMapRefs(hcluster.Spec.Configuration)
			for _, configMapRef := range configMapRefs {
				sourceCM := &corev1.ConfigMap{}
				if err := r.Get(ctx, client.ObjectKey{Namespace: hcluster.Namespace, Name: configMapRef}, sourceCM); err != nil {
					return ctrl.Result{}, fmt.Errorf("failed to get referenced configmap %s/%s: %w", hcluster.Namespace, configMapRef, err)
				}
				destCM := &corev1.ConfigMap{}
				destCM.Name = sourceCM.Name
				destCM.Namespace = controlPlaneNamespace.Name
				if _, err := createOrUpdate(ctx, r.Client, destCM, func() error {
					destCM.Annotations = sourceCM.Annotations
					destCM.Labels = sourceCM.Labels
					destCM.Data = sourceCM.Data
					destCM.BinaryData = sourceCM.BinaryData
					destCM.Immutable = sourceCM.Immutable
					return nil
				}); err != nil {
					return ctrl.Result{}, fmt.Errorf("failed to reconcile referenced config map %s/%s: %w", destCM.Namespace, destCM.Name, err)
				}
			}
			secretRefs := globalconfig.SecretRefs(hcluster.Spec.Configuration)
			for _, secretRef := range secretRefs {
				sourceSecret := &corev1.Secret{}
				if err := r.Get(ctx, client.ObjectKey{Namespace: hcluster.Namespace, Name: secretRef}, sourceSecret); err != nil {
					return ctrl.Result{}, fmt.Errorf("failed to get referenced secret %s/%s: %w", hcluster.Namespace, secretRef, err)
				}
				destSecret := &corev1.Secret{}
				destSecret.Name = sourceSecret.Name
				destSecret.Namespace = controlPlaneNamespace.Name
				if _, err := createOrUpdate(ctx, r.Client, destSecret, func() error {
					destSecret.Annotations = sourceSecret.Annotations
					destSecret.Labels = sourceSecret.Labels
					destSecret.Data = sourceSecret.Data
					destSecret.Immutable = sourceSecret.Immutable
					destSecret.Type = sourceSecret.Type
					return nil
				}); err != nil {
					return ctrl.Result{}, fmt.Errorf("failed to reconcile secret %s/%s: %w", destSecret.Namespace, destSecret.Name, err)
				}
			}
		}
	}

	// Reconcile the HostedControlPlane
	hcp = controlplaneoperator.HostedControlPlane(controlPlaneNamespace.Name, hcluster.Name)
	_, err = createOrUpdate(ctx, r.Client, hcp, func() error {
		return reconcileHostedControlPlane(hcp, hcluster)
	})
	if err != nil {
		return ctrl.Result{}, fmt.Errorf("failed to reconcile hostedcontrolplane: %w", err)
	}

	// Reconcile CAPI Infra CR.
	infraCR, err := p.ReconcileCAPIInfraCR(ctx, r.Client, createOrUpdate,
		hcluster,
		controlPlaneNamespace.Name,
		hcp.Status.ControlPlaneEndpoint)
	if err != nil {
		return ctrl.Result{}, err
	}

	if err := r.reconcileAWSSubnets(ctx, createOrUpdate, infraCR, req.Namespace, req.Name, controlPlaneNamespace.Name); err != nil {
		return ctrl.Result{}, err
	}

	// Reconcile CAPI Provider Deployment.
	capiProviderDeploymentSpec, err := p.CAPIProviderDeploymentSpec(hcluster, hcp)
	if err != nil {
		return ctrl.Result{}, err
	}
	if capiProviderDeploymentSpec != nil {
		proxy.SetEnvVars(&capiProviderDeploymentSpec.Template.Spec.Containers[0].Env)
	}

	// Reconcile cluster prometheus RBAC resources if enabled
	if r.EnableOCPClusterMonitoring {
		if err := r.reconcileClusterPrometheusRBAC(ctx, createOrUpdate, hcp.Namespace); err != nil {
			return ctrl.Result{}, fmt.Errorf("failed to reconcile RBAC for OCP cluster prometheus: %w", err)
		}
	}

	// Reconcile the CAPI Cluster resource
	// In the None platform case, there is no CAPI provider/resources so infraCR is nil
	if infraCR != nil {
		capiCluster := controlplaneoperator.CAPICluster(controlPlaneNamespace.Name, hcluster.Spec.InfraID)
		_, err = createOrUpdate(ctx, r.Client, capiCluster, func() error {
			return reconcileCAPICluster(capiCluster, hcluster, hcp, infraCR)
		})
		if err != nil {
			return ctrl.Result{}, fmt.Errorf("failed to reconcile capi cluster: %w", err)
		}
	}

	// Reconcile the HostedControlPlane kubeconfig if one is reported
	if hcp.Status.KubeConfig != nil {
		src := &corev1.Secret{
			ObjectMeta: metav1.ObjectMeta{
				Namespace: hcp.Namespace,
				Name:      hcp.Status.KubeConfig.Name,
			},
		}
		err := r.Client.Get(ctx, client.ObjectKeyFromObject(src), src)
		if err != nil {
			return ctrl.Result{}, fmt.Errorf("failed to get controlplane kubeconfig secret %q: %w", client.ObjectKeyFromObject(src), err)
		}
		dest := manifests.KubeConfigSecret(hcluster.Namespace, hcluster.Name)
		_, err = createOrUpdate(ctx, r.Client, dest, func() error {
			key := hcp.Status.KubeConfig.Key
			srcData, srcHasData := src.Data[key]
			if !srcHasData {
				return fmt.Errorf("controlplane kubeconfig secret %q must have a %q key", client.ObjectKeyFromObject(src), key)
			}
			dest.Labels = hcluster.Labels
			dest.Type = corev1.SecretTypeOpaque
			if dest.Data == nil {
				dest.Data = map[string][]byte{}
			}
			dest.Data["kubeconfig"] = srcData
			dest.SetOwnerReferences([]metav1.OwnerReference{{
				APIVersion: hyperv1.GroupVersion.String(),
				Kind:       "HostedCluster",
				Name:       hcluster.Name,
				UID:        hcluster.UID,
			}})
			return nil
		})
		if err != nil {
			return ctrl.Result{}, fmt.Errorf("failed to reconcile hostedcluster kubeconfig secret: %w", err)
		}
	}

	// Reconcile the HostedControlPlane kubeadminPassword
	if hcp.Status.KubeadminPassword != nil {
		src := &corev1.Secret{
			ObjectMeta: metav1.ObjectMeta{
				Namespace: hcp.Namespace,
				Name:      hcp.Status.KubeadminPassword.Name,
			},
		}
		err := r.Client.Get(ctx, client.ObjectKeyFromObject(src), src)
		if err != nil {
			return ctrl.Result{}, fmt.Errorf("failed to get controlplane kubeadmin password secret %q: %w", client.ObjectKeyFromObject(src), err)
		}
		dest := manifests.KubeadminPasswordSecret(hcluster.Namespace, hcluster.Name)
		_, err = createOrUpdate(ctx, r.Client, dest, func() error {
			dest.Type = corev1.SecretTypeOpaque
			dest.Data = map[string][]byte{}
			for k, v := range src.Data {
				dest.Data[k] = v
			}
			dest.SetOwnerReferences([]metav1.OwnerReference{{
				APIVersion: hyperv1.GroupVersion.String(),
				Kind:       "HostedCluster",
				Name:       hcluster.Name,
				UID:        hcluster.UID,
			}})
			return nil
		})
		if err != nil {
			return ctrl.Result{}, fmt.Errorf("failed to reconcile hostedcluster kubeconfig secret: %w", err)
		}
	}

	// Reconcile the CAPI manager components
	err = r.reconcileCAPIManager(ctx, createOrUpdate, hcluster, hcp)
	if err != nil {
		return ctrl.Result{}, fmt.Errorf("failed to reconcile capi manager: %w", err)
	}

	// Reconcile the CAPI provider components
	if err = r.reconcileCAPIProvider(ctx, createOrUpdate, hcluster, hcp, capiProviderDeploymentSpec, p); err != nil {
		return ctrl.Result{}, fmt.Errorf("failed to reconcile capi provider: %w", err)
	}

	// In >= 4.11 We want to move most of the components reconciliation down to the CPO https://issues.redhat.com/browse/HOSTEDCP-375.
	// For IBM existing clusters < 4.11 we need to stay consistent and keep deploying existing pods to satisfy validations.
	// TODO (alberto): drop this after dropping < 4.11 support.
	if _, hasLabel := hyperutil.ImageLabels(controlPlaneOperatorImageMetadata)[controlPlaneOperatorManagesMachineAutoscaler]; !hasLabel {
		// Reconcile the autoscaler.
		err = r.reconcileAutoscaler(ctx, createOrUpdate, hcluster, hcp, utilitiesImage)
		if err != nil {
			return ctrl.Result{}, fmt.Errorf("failed to reconcile autoscaler: %w", err)
		}
	}
	if _, hasLabel := hyperutil.ImageLabels(controlPlaneOperatorImageMetadata)[controlPlaneOperatorManagesMachineApprover]; !hasLabel {
		// Reconcile the machine approver.
		if err = r.reconcileMachineApprover(ctx, createOrUpdate, hcluster, hcp, utilitiesImage); err != nil {
			return ctrl.Result{}, fmt.Errorf("failed to reconcile machine approver: %w", err)
		}
	}

	defaultIngressDomain, err := r.defaultIngressDomain(ctx)
	if err != nil {
		return ctrl.Result{}, fmt.Errorf("failed to determine default ingress domain: %w", err)
	}

	// Reconcile the control plane operator
	err = r.reconcileControlPlaneOperator(ctx, createOrUpdate, hcluster, hcp, controlPlaneOperatorImage, utilitiesImage, defaultIngressDomain, cpoHasUtilities)
	if err != nil {
		return ctrl.Result{}, fmt.Errorf("failed to reconcile control plane operator: %w", err)
	}

	// Reconcile the Ignition server
	if !controlplaneOperatorManagesIgnitionServer {
		if err := ignitionserverreconciliation.ReconcileIgnitionServer(ctx,
			r.Client,
			createOrUpdate,
			utilitiesImage,
			hcp,
			defaultIngressDomain,
			ignitionServerHasHealthzHandler,
			r.ReleaseProvider.GetRegistryOverrides(),
			r.ManagementClusterCapabilities.Has(capabilities.CapabilitySecurityContextConstraint),
		); err != nil {
			return ctrl.Result{}, fmt.Errorf("failed to reconcile ignition server: %w", err)
		}
	}

	// Reconcile the machine config server
	if err = r.reconcileMachineConfigServer(ctx, createOrUpdate, hcluster); err != nil {
		return ctrl.Result{}, fmt.Errorf("failed to reconcile machine config server: %w", err)
	}

	// Reconcile the network policies
	if err = r.reconcileNetworkPolicies(ctx, createOrUpdate, hcluster); err != nil {
		return ctrl.Result{}, fmt.Errorf("failed to reconcile network policies: %w", err)
	}

	// Reconcile the AWS OIDC discovery
	switch hcluster.Spec.Platform.Type {
	case hyperv1.AWSPlatform:
		if err := r.reconcileAWSOIDCDocuments(ctx, log, hcluster, hcp); err != nil {
			meta.SetStatusCondition(&hcluster.Status.Conditions, metav1.Condition{
				Type:               string(hyperv1.ValidOIDCConfiguration),
				Status:             metav1.ConditionFalse,
				Reason:             hyperv1.OIDCConfigurationInvalidReason,
				ObservedGeneration: hcluster.Generation,
				Message:            err.Error(),
			})
			if statusErr := r.Client.Status().Update(ctx, hcluster); statusErr != nil {
				return ctrl.Result{}, fmt.Errorf("failed to reconcile AWS OIDC documents: %s, failed to update status: %w", err, statusErr)
			}
			return ctrl.Result{}, fmt.Errorf("failed to reconcile the AWS OIDC documents: %w", err)
		}
		if meta.IsStatusConditionFalse(hcluster.Status.Conditions, string(hyperv1.ValidOIDCConfiguration)) {
			meta.SetStatusCondition(&hcluster.Status.Conditions, metav1.Condition{
				Type:               string(hyperv1.ValidOIDCConfiguration),
				Status:             metav1.ConditionTrue,
				Reason:             hyperv1.AsExpectedReason,
				ObservedGeneration: hcluster.Generation,
				Message:            "OIDC configuration is valid",
			})
			if err := r.Client.Status().Update(ctx, hcluster); err != nil {
				return ctrl.Result{}, fmt.Errorf("failed to update status: %w", err)
			}
		}
	}

	log.Info("successfully reconciled")
	return ctrl.Result{}, nil
}

// reconcileHostedControlPlane reconciles the given HostedControlPlane, which
// will be mutated.
func reconcileHostedControlPlane(hcp *hyperv1.HostedControlPlane, hcluster *hyperv1.HostedCluster) error {
	hcp.Annotations = map[string]string{
		HostedClusterAnnotation: client.ObjectKeyFromObject(hcluster).String(),
	}

	// These annotations are copied from the HostedCluster
	mirroredAnnotations := []string{
		hyperv1.DisablePKIReconciliationAnnotation,
		hyperv1.OauthLoginURLOverrideAnnotation,
		hyperv1.KonnectivityAgentImageAnnotation,
		hyperv1.KonnectivityServerImageAnnotation,
		hyperv1.RestartDateAnnotation,
		hyperv1.IBMCloudKMSProviderImage,
		hyperv1.AWSKMSProviderImage,
		hyperv1.PortierisImageAnnotation,
		hyperutil.DebugDeploymentsAnnotation,
		hyperv1.DisableProfilingAnnotation,
		hyperv1.PrivateIngressControllerAnnotation,
		hyperv1.CleanupCloudResourcesAnnotation,
	}
	for _, key := range mirroredAnnotations {
		val, hasVal := hcluster.Annotations[key]
		if hasVal {
			hcp.Annotations[key] = val
		}
	}

	// All annotations on the HostedCluster with this special prefix are copied
	for key, val := range hcluster.Annotations {
		if strings.HasPrefix(key, hyperv1.IdentityProviderOverridesAnnotationPrefix) {
			hcp.Annotations[key] = val
		}
	}

	hcp.Spec.ReleaseImage = hcluster.Spec.Release.Image

	hcp.Spec.PullSecret = corev1.LocalObjectReference{Name: controlplaneoperator.PullSecret(hcp.Namespace).Name}
	if len(hcluster.Spec.SSHKey.Name) > 0 {
		hcp.Spec.SSHKey = corev1.LocalObjectReference{Name: controlplaneoperator.SSHKey(hcp.Namespace).Name}
	}
	if hcluster.Spec.AuditWebhook != nil && len(hcluster.Spec.AuditWebhook.Name) > 0 {
		hcp.Spec.AuditWebhook = hcluster.Spec.AuditWebhook.DeepCopy()
	}

	hcp.Spec.FIPS = hcluster.Spec.FIPS
	hcp.Spec.IssuerURL = hcluster.Spec.IssuerURL
	hcp.Spec.ServiceAccountSigningKey = hcluster.Spec.ServiceAccountSigningKey

	hcp.Spec.Networking = hcluster.Spec.Networking
	// Populate deprecated fields for compatibility with older control plane operators
	populateDeprecatedNetworkingFields(hcp, hcluster)

	hcp.Spec.ClusterID = hcluster.Spec.ClusterID
	hcp.Spec.InfraID = hcluster.Spec.InfraID
	hcp.Spec.DNS = hcluster.Spec.DNS
	hcp.Spec.Services = hcluster.Spec.Services
	hcp.Spec.ControllerAvailabilityPolicy = hcluster.Spec.ControllerAvailabilityPolicy
	hcp.Spec.InfrastructureAvailabilityPolicy = hcluster.Spec.InfrastructureAvailabilityPolicy
	hcp.Spec.Etcd.ManagementType = hcluster.Spec.Etcd.ManagementType
	if hcluster.Spec.Etcd.ManagementType == hyperv1.Unmanaged && hcluster.Spec.Etcd.Unmanaged != nil {
		hcp.Spec.Etcd.Unmanaged = hcluster.Spec.Etcd.Unmanaged.DeepCopy()
	}
	if hcluster.Spec.Etcd.ManagementType == hyperv1.Managed && hcluster.Spec.Etcd.Managed != nil {
		hcp.Spec.Etcd.Managed = hcluster.Spec.Etcd.Managed.DeepCopy()
	}
	if hcluster.Spec.ImageContentSources != nil {
		hcp.Spec.ImageContentSources = hcluster.Spec.ImageContentSources
	}
	if hcluster.Spec.AdditionalTrustBundle != nil {
		hcp.Spec.AdditionalTrustBundle = &corev1.LocalObjectReference{Name: controlplaneoperator.UserCABundle(hcp.Namespace).Name}
	}
	if hcluster.Spec.SecretEncryption != nil {
		hcp.Spec.SecretEncryption = hcluster.Spec.SecretEncryption.DeepCopy()
	}

	hcp.Spec.PausedUntil = hcluster.Spec.PausedUntil
	hcp.Spec.OLMCatalogPlacement = hcluster.Spec.OLMCatalogPlacement
	hcp.Spec.Autoscaling = hcluster.Spec.Autoscaling
	hcp.Spec.NodeSelector = hcluster.Spec.NodeSelector

	// Pass through Platform spec.
	hcp.Spec.Platform = *hcluster.Spec.Platform.DeepCopy()
	switch hcluster.Spec.Platform.Type {
	case hyperv1.AgentPlatform:
		// Agent platform uses None platform for the hcp.
		hcp.Spec.Platform.Type = hyperv1.NonePlatform
	}

	// Backward compatible conversions.
	// TODO (alberto): Drop this as we go GA in only support targeted release.
	switch hcluster.Spec.Platform.Type {
	case hyperv1.AWSPlatform:
		// For compatibility with versions of the CPO < 4.12, the HCP KubeCloudControllerCreds secret ref
		// and the roles need to be populated so the old CPO can operate.
		ensureHCPAWSRolesBackwardCompatibility(hcluster, hcp)
	}

	if hcluster.Spec.Configuration != nil {
		hcp.Spec.Configuration = hcluster.Spec.Configuration.DeepCopy()
		// for compatibility with previous versions of the CPO, the hcp configuration should be
		// populated with individual fields *AND* the previous raw extension resources.
		items, err := configurationFieldsToRawExtensions(hcluster.Spec.Configuration)
		if err != nil {
			return fmt.Errorf("failed to convert configuration fields to raw extension: %w", err)
		}
		// TODO: cannot remove until IBM's production fleet (4.9_openshift, 4.10_openshift) of control-plane-operators
		// are upgraded to versions that read validation information from new sections
		hcp.Spec.Configuration.Items = items
		secretRef := []corev1.LocalObjectReference{}
		configMapRef := []corev1.LocalObjectReference{}
		for _, secretName := range globalconfig.SecretRefs(hcluster.Spec.Configuration) {
			secretRef = append(secretRef, corev1.LocalObjectReference{
				Name: secretName,
			})
		}
		for _, configMapName := range globalconfig.ConfigMapRefs(hcluster.Spec.Configuration) {
			configMapRef = append(configMapRef, corev1.LocalObjectReference{
				Name: configMapName,
			})
		}
		hcp.Spec.Configuration.SecretRefs = secretRef
		hcp.Spec.Configuration.ConfigMapRefs = configMapRef
	} else {
		hcp.Spec.Configuration = nil
	}

	return nil
}

func ensureHCPAWSRolesBackwardCompatibility(hc *hyperv1.HostedCluster, hcp *hyperv1.HostedControlPlane) {
	hcp.Spec.Platform.AWS.KubeCloudControllerCreds = corev1.LocalObjectReference{Name: platformaws.KubeCloudControllerCredsSecret("").Name}
	hcp.Spec.Platform.AWS.Roles = []hyperv1.AWSRoleCredentials{
		{
			ARN:       hc.Spec.Platform.AWS.RolesRef.IngressARN,
			Namespace: "openshift-ingress-operator",
			Name:      "cloud-credentials",
		},
		{
			ARN:       hc.Spec.Platform.AWS.RolesRef.ImageRegistryARN,
			Namespace: "openshift-image-registry",
			Name:      "installer-cloud-credentials",
		},
		{
			ARN:       hc.Spec.Platform.AWS.RolesRef.StorageARN,
			Namespace: "openshift-cluster-csi-drivers",
			Name:      "ebs-cloud-credentials",
		},
		{
			ARN:       hc.Spec.Platform.AWS.RolesRef.NetworkARN,
			Namespace: "openshift-cloud-network-config-controller",
			Name:      "cloud-credentials",
		},
	}

	hcp.Spec.Platform.AWS.RolesRef = hc.Spec.Platform.AWS.RolesRef
}

// reconcileCAPIManager orchestrates orchestrates of  all CAPI manager components.
func (r *HostedClusterReconciler) reconcileCAPIManager(ctx context.Context, createOrUpdate upsert.CreateOrUpdateFN, hcluster *hyperv1.HostedCluster, hcp *hyperv1.HostedControlPlane) error {
	controlPlaneNamespace := manifests.HostedControlPlaneNamespace(hcluster.Namespace, hcluster.Name)
	err := r.Client.Get(ctx, client.ObjectKeyFromObject(controlPlaneNamespace), controlPlaneNamespace)
	if err != nil {
		return fmt.Errorf("failed to get control plane namespace: %w", err)
	}

	// Reconcile CAPI webhooks TLS secret
	capiWebhooksTLSSecret := clusterapi.CAPIWebhooksTLSSecret(controlPlaneNamespace.Name)
	_, err = createOrUpdate(ctx, r.Client, capiWebhooksTLSSecret, func() error {
		_, hasTLSPrivateKeyKey := capiWebhooksTLSSecret.Data[corev1.TLSPrivateKeyKey]
		_, hasTLSCertKey := capiWebhooksTLSSecret.Data[corev1.TLSCertKey]
		if hasTLSPrivateKeyKey && hasTLSCertKey {
			return nil
		}

		// We currently don't expose CAPI webhooks but still they run as part of the manager
		// and it breaks without a cert https://github.com/kubernetes-sigs/cluster-api/pull/4709.
		cn := "capi-webhooks"
		ou := "openshift"
		cfg := &certs.CertCfg{
			Subject:   pkix.Name{CommonName: cn, OrganizationalUnit: []string{ou}},
			KeyUsages: x509.KeyUsageKeyEncipherment | x509.KeyUsageDigitalSignature | x509.KeyUsageCertSign,
			Validity:  certs.ValidityTenYears,
			IsCA:      true,
		}
		key, crt, err := certs.GenerateSelfSignedCertificate(cfg)
		if err != nil {
			return fmt.Errorf("failed to generate CA (cn=%s,ou=%s): %w", cn, ou, err)
		}
		if capiWebhooksTLSSecret.Data == nil {
			capiWebhooksTLSSecret.Data = map[string][]byte{}
		}
		capiWebhooksTLSSecret.Data[corev1.TLSCertKey] = certs.CertToPem(crt)
		capiWebhooksTLSSecret.Data[corev1.TLSPrivateKeyKey] = certs.PrivateKeyToPem(key)
		return nil
	})
	if err != nil {
		return fmt.Errorf("failed to reconcile capi webhook tls secret: %w", err)
	}

	// Reconcile CAPI manager service account
	capiManagerServiceAccount := clusterapi.CAPIManagerServiceAccount(controlPlaneNamespace.Name)
	_, err = createOrUpdate(ctx, r.Client, capiManagerServiceAccount, func() error {
		hyperutil.EnsurePullSecret(capiManagerServiceAccount, controlplaneoperator.PullSecret("").Name)
		return nil
	})
	if err != nil {
		return fmt.Errorf("failed to reconcile capi manager service account: %w", err)
	}

	// Reconcile CAPI manager cluster role
	capiManagerClusterRole := clusterapi.CAPIManagerClusterRole(controlPlaneNamespace.Name)
	_, err = createOrUpdate(ctx, r.Client, capiManagerClusterRole, func() error {
		return reconcileCAPIManagerClusterRole(capiManagerClusterRole)
	})
	if err != nil {
		return fmt.Errorf("failed to reconcile capi manager cluster role: %w", err)
	}

	// Reconcile CAPI manager cluster role binding
	capiManagerClusterRoleBinding := clusterapi.CAPIManagerClusterRoleBinding(controlPlaneNamespace.Name)
	_, err = createOrUpdate(ctx, r.Client, capiManagerClusterRoleBinding, func() error {
		return reconcileCAPIManagerClusterRoleBinding(capiManagerClusterRoleBinding, capiManagerClusterRole, capiManagerServiceAccount)
	})
	if err != nil {
		return fmt.Errorf("failed to reconcile capi manager cluster role binding: %w", err)
	}

	// Reconcile CAPI manager role
	capiManagerRole := clusterapi.CAPIManagerRole(controlPlaneNamespace.Name)
	_, err = createOrUpdate(ctx, r.Client, capiManagerRole, func() error {
		return reconcileCAPIManagerRole(capiManagerRole)
	})
	if err != nil {
		return fmt.Errorf("failed to reconcile capi manager role: %w", err)
	}

	// Reconcile CAPI manager role binding
	capiManagerRoleBinding := clusterapi.CAPIManagerRoleBinding(controlPlaneNamespace.Name)
	_, err = createOrUpdate(ctx, r.Client, capiManagerRoleBinding, func() error {
		return reconcileCAPIManagerRoleBinding(capiManagerRoleBinding, capiManagerRole, capiManagerServiceAccount)
	})
	if err != nil {
		return fmt.Errorf("failed to reconcile capi manager role: %w", err)
	}

	// Reconcile CAPI manager deployment
	capiImage := imageCAPI
	if envImage := os.Getenv(images.CAPIEnvVar); len(envImage) > 0 {
		capiImage = envImage
	}
	if _, ok := hcluster.Annotations[hyperv1.ClusterAPIManagerImage]; ok {
		capiImage = hcluster.Annotations[hyperv1.ClusterAPIManagerImage]
	}
	capiManagerDeployment := clusterapi.ClusterAPIManagerDeployment(controlPlaneNamespace.Name)
	_, err = createOrUpdate(ctx, r.Client, capiManagerDeployment, func() error {
		// TODO (alberto): This image builds from https://github.com/kubernetes-sigs/cluster-api/pull/4709
		// We need to build from main branch and push to quay.io/hypershift once this is merged or otherwise enable webhooks.
		return reconcileCAPIManagerDeployment(capiManagerDeployment, hcluster, hcp, capiManagerServiceAccount, capiImage, r.SetDefaultSecurityContext)
	})
	if err != nil {
		return fmt.Errorf("failed to reconcile capi manager deployment: %w", err)
	}

	return nil
}

// reconcileCAPIProvider orchestrates reconciliation of the CAPI provider
// components for a given platform.
func (r *HostedClusterReconciler) reconcileCAPIProvider(ctx context.Context, createOrUpdate upsert.CreateOrUpdateFN, hcluster *hyperv1.HostedCluster, hcp *hyperv1.HostedControlPlane,
	capiProviderDeploymentSpec *appsv1.DeploymentSpec, p platform.Platform) error {
	if capiProviderDeploymentSpec == nil {
		// If there's no capiProviderDeploymentSpec implementation return early.
		return nil
	}

	controlPlaneNamespace := manifests.HostedControlPlaneNamespace(hcluster.Namespace, hcluster.Name)
	err := r.Client.Get(ctx, client.ObjectKeyFromObject(controlPlaneNamespace), controlPlaneNamespace)
	if err != nil {
		return fmt.Errorf("failed to get control plane namespace: %w", err)
	}

	// Reconcile CAPI provider role
	capiProviderRole := clusterapi.CAPIProviderRole(controlPlaneNamespace.Name)
	_, err = createOrUpdate(ctx, r.Client, capiProviderRole, func() error {
		return reconcileCAPIProviderRole(capiProviderRole, p)
	})
	if err != nil {
		return fmt.Errorf("failed to reconcile capi provider role: %w", err)
	}

	// Reconcile CAPI provider service account
	capiProviderServiceAccount := clusterapi.CAPIProviderServiceAccount(controlPlaneNamespace.Name)
	_, err = createOrUpdate(ctx, r.Client, capiProviderServiceAccount, func() error {
		hyperutil.EnsurePullSecret(capiProviderServiceAccount, controlplaneoperator.PullSecret("").Name)
		return nil
	})
	if err != nil {
		return fmt.Errorf("failed to reconcile capi provider service account: %w", err)
	}

	// Reconcile CAPI provider role binding
	capiProviderRoleBinding := clusterapi.CAPIProviderRoleBinding(controlPlaneNamespace.Name)
	_, err = createOrUpdate(ctx, r.Client, capiProviderRoleBinding, func() error {
		return reconcileCAPIProviderRoleBinding(capiProviderRoleBinding, capiProviderRole, capiProviderServiceAccount)
	})
	if err != nil {
		return fmt.Errorf("failed to reconcile capi provider role binding: %w", err)
	}

	// Reconcile CAPI provider deployment
	deployment := clusterapi.CAPIProviderDeployment(controlPlaneNamespace.Name)
	labels := map[string]string{
		"control-plane":               "capi-provider-controller-manager",
		"app":                         "capi-provider-controller-manager",
		hyperv1.ControlPlaneComponent: "capi-provider-controller-manager",
	}
	_, err = createOrUpdate(ctx, r.Client, deployment, func() error {
		// Enforce provider specifics.
		deployment.Spec = *capiProviderDeploymentSpec

		// Enforce pull policy.
		for i := range deployment.Spec.Template.Spec.Containers {
			deployment.Spec.Template.Spec.Containers[i].ImagePullPolicy = corev1.PullIfNotPresent
		}

		// Enforce labels.
		deployment.Spec.Selector = &metav1.LabelSelector{
			MatchLabels: labels,
		}
		deployment.Spec.Template.Labels = labels

		// Enforce ServiceAccount.
		deployment.Spec.Template.Spec.ServiceAccountName = capiProviderServiceAccount.Name

		// TODO (alberto): Reconsider enable this back when we face a real need
		// with no better solution.
		// deploymentConfig.SetRestartAnnotation(hcp.ObjectMeta)
		deploymentConfig := config.DeploymentConfig{
			Scheduling: config.Scheduling{
				PriorityClass: config.DefaultPriorityClass,
			},
			SetDefaultSecurityContext: r.SetDefaultSecurityContext,
		}

		deploymentConfig.SetDefaults(hcp, nil, k8sutilspointer.Int(1))
		deploymentConfig.ApplyTo(deployment)

		return nil
	})
	if err != nil {
		return fmt.Errorf("failed to reconcile capi provider deployment: %w", err)
	}

	return nil
}

// reconcileControlPlaneOperator orchestrates reconciliation of the control plane
// operator components.
func (r *HostedClusterReconciler) reconcileControlPlaneOperator(ctx context.Context, createOrUpdate upsert.CreateOrUpdateFN, hcluster *hyperv1.HostedCluster, hostedControlPlane *hyperv1.HostedControlPlane, controlPlaneOperatorImage, utilitiesImage, defaultIngressDomain string, cpoHasUtilities bool) error {
	controlPlaneNamespace := manifests.HostedControlPlaneNamespace(hcluster.Namespace, hcluster.Name)
	err := r.Client.Get(ctx, client.ObjectKeyFromObject(controlPlaneNamespace), controlPlaneNamespace)
	if err != nil {
		return fmt.Errorf("failed to get control plane namespace: %w", err)
	}

	// Reconcile operator service account
	controlPlaneOperatorServiceAccount := controlplaneoperator.OperatorServiceAccount(controlPlaneNamespace.Name)
	_, err = createOrUpdate(ctx, r.Client, controlPlaneOperatorServiceAccount, NoopReconcile)
	if err != nil {
		return fmt.Errorf("failed to reconcile controlplane operator service account: %w", err)
	}

	// Reconcile operator role
	controlPlaneOperatorRole := controlplaneoperator.OperatorRole(controlPlaneNamespace.Name)
	_, err = createOrUpdate(ctx, r.Client, controlPlaneOperatorRole, func() error {
		return reconcileControlPlaneOperatorRole(controlPlaneOperatorRole)
	})
	if err != nil {
		return fmt.Errorf("failed to reconcile controlplane operator role: %w", err)
	}

	// Reconcile operator role binding
	controlPlaneOperatorRoleBinding := controlplaneoperator.OperatorRoleBinding(controlPlaneNamespace.Name)
	_, err = createOrUpdate(ctx, r.Client, controlPlaneOperatorRoleBinding, func() error {
		return reconcileControlPlaneOperatorRoleBinding(controlPlaneOperatorRoleBinding, controlPlaneOperatorRole, controlPlaneOperatorServiceAccount)
	})
	if err != nil {
		return fmt.Errorf("failed to reconcile controlplane operator rolebinding: %w", err)
	}

	// TODO: Remove this block after initial merge of this feature. It is not needed for latest CPO version
	if r.ManagementClusterCapabilities.Has(capabilities.CapabilityRoute) {
		// Reconcile operator role - for ingress
		controlPlaneOperatorIngressRole := controlplaneoperator.OperatorIngressRole("openshift-ingress", controlPlaneNamespace.Name)
		_, err = createOrUpdate(ctx, r.Client, controlPlaneOperatorIngressRole, func() error {
			return reconcileControlPlaneOperatorIngressRole(controlPlaneOperatorIngressRole)
		})
		if err != nil {
			return fmt.Errorf("failed to reconcile controlplane operator ingress role: %w", err)
		}

		// Reconcile operator role binding - for ingress
		controlPlaneOperatorIngressRoleBinding := controlplaneoperator.OperatorIngressRoleBinding("openshift-ingress", controlPlaneNamespace.Name)
		_, err = createOrUpdate(ctx, r.Client, controlPlaneOperatorIngressRoleBinding, func() error {
			return reconcileControlPlaneOperatorIngressRoleBinding(controlPlaneOperatorIngressRoleBinding, controlPlaneOperatorIngressRole, controlPlaneOperatorServiceAccount)
		})
		if err != nil {
			return fmt.Errorf("failed to reconcile controlplane operator ingress rolebinding: %w", err)
		}

		// Reconcile operator role - for ingress operator
		controlPlaneOperatorIngressOperatorRole := controlplaneoperator.OperatorIngressOperatorRole("openshift-ingress-operator", controlPlaneNamespace.Name)
		_, err = createOrUpdate(ctx, r.Client, controlPlaneOperatorIngressOperatorRole, func() error {
			return reconcilecontrolPlaneOperatorIngressOperatorRole(controlPlaneOperatorIngressOperatorRole)
		})
		if err != nil {
			return fmt.Errorf("failed to reconcile controlplane operator ingress operator role: %w", err)
		}

		// Reconcile operator role binding - for ingress operator
		controlPlaneOperatorIngressOperatorRoleBinding := controlplaneoperator.OperatorIngressOperatorRoleBinding("openshift-ingress-operator", controlPlaneNamespace.Name)
		_, err = createOrUpdate(ctx, r.Client, controlPlaneOperatorIngressOperatorRoleBinding, func() error {
			return reconcilecontrolPlaneOperatorIngressOperatorRoleBinding(controlPlaneOperatorIngressOperatorRoleBinding, controlPlaneOperatorIngressOperatorRole, controlPlaneOperatorServiceAccount)
		})
		if err != nil {
			return fmt.Errorf("failed to reconcile controlplane operator ingress operator rolebinding: %w", err)
		}
	}

	// Reconcile operator deployment
	controlPlaneOperatorDeployment := controlplaneoperator.OperatorDeployment(controlPlaneNamespace.Name)
	_, err = createOrUpdate(ctx, r.Client, controlPlaneOperatorDeployment, func() error {
		return reconcileControlPlaneOperatorDeployment(controlPlaneOperatorDeployment, hcluster, hostedControlPlane, controlPlaneOperatorImage, utilitiesImage, r.SetDefaultSecurityContext, controlPlaneOperatorServiceAccount, r.EnableCIDebugOutput, convertRegistryOverridesToCommandLineFlag(r.ReleaseProvider.GetRegistryOverrides()), defaultIngressDomain, cpoHasUtilities, r.MetricsSet)
	})
	if err != nil {
		return fmt.Errorf("failed to reconcile controlplane operator deployment: %w", err)
	}

	// Reconcile operator PodMonitor
	podMonitor := controlplaneoperator.PodMonitor(controlPlaneNamespace.Name)
	if _, err := createOrUpdate(ctx, r.Client, podMonitor, func() error {
		podMonitor.Spec.Selector = *controlPlaneOperatorDeployment.Spec.Selector
		podMonitor.Spec.PodMetricsEndpoints = []prometheusoperatorv1.PodMetricsEndpoint{{
			Interval:             "15s",
			Port:                 "metrics",
			MetricRelabelConfigs: metrics.ControlPlaneOperatorRelabelConfigs(r.MetricsSet),
		}}
		podMonitor.Spec.NamespaceSelector = prometheusoperatorv1.NamespaceSelector{MatchNames: []string{controlPlaneNamespace.Name}}
		podMonitor.SetOwnerReferences([]metav1.OwnerReference{{
			APIVersion: hyperv1.GroupVersion.String(),
			Kind:       "HostedControlPlane",
			Name:       hostedControlPlane.Name,
			UID:        hostedControlPlane.UID,
		}})
		if podMonitor.Annotations == nil {
			podMonitor.Annotations = map[string]string{}
		}
		podMonitor.Annotations[HostedClusterAnnotation] = client.ObjectKeyFromObject(hcluster).String()
		hyperutil.ApplyClusterIDLabelToPodMonitor(&podMonitor.Spec.PodMetricsEndpoints[0], hcluster.Spec.ClusterID)
		return nil
	}); err != nil {
		return fmt.Errorf("failed to reconcile controlplane operator pod monitor: %w", err)
	}

	return nil
}

func convertRegistryOverridesToCommandLineFlag(registryOverrides map[string]string) string {
	commandLineFlagArray := []string{}
	for registrySource, registryReplacement := range registryOverrides {
		commandLineFlagArray = append(commandLineFlagArray, fmt.Sprintf("%s=%s", registrySource, registryReplacement))
	}
	if len(commandLineFlagArray) > 0 {
		return strings.Join(commandLineFlagArray, ",")
	}
	// this is the equivalent of null on a StringToString command line variable.
	return "="
}

func servicePublishingStrategyByType(hcp *hyperv1.HostedCluster, svcType hyperv1.ServiceType) *hyperv1.ServicePublishingStrategy {
	for _, mapping := range hcp.Spec.Services {
		if mapping.Service == svcType {
			return &mapping.ServicePublishingStrategy
		}
	}
	return nil
}

// reconcileAutoscaler orchestrates reconciliation of autoscaler components using
// both the HostedCluster and the HostedControlPlane which the autoscaler takes
// inputs from.
func (r *HostedClusterReconciler) reconcileAutoscaler(ctx context.Context, createOrUpdate upsert.CreateOrUpdateFN, hcluster *hyperv1.HostedCluster, hcp *hyperv1.HostedControlPlane, utilitiesImage string) error {
	clusterAutoscalerImage, err := r.getPayloadImage(ctx, hcluster, ImageStreamAutoscalerImage)
	if err != nil {
		return fmt.Errorf("failed to get image for machine approver: %w", err)
	}
	// TODO: can remove this override when all IBM production clusters upgraded to a version that uses the release image
	if imageVal, ok := hcluster.Annotations[hyperv1.ClusterAutoscalerImage]; ok {
		clusterAutoscalerImage = imageVal
	}

	return autoscaler.ReconcileAutoscaler(ctx, r.Client, hcp, clusterAutoscalerImage, utilitiesImage, createOrUpdate, r.SetDefaultSecurityContext)
}

// GetControlPlaneOperatorImage resolves the appropriate control plane operator
// image based on the following order of precedence (from most to least
// preferred):
//
// 1. The image specified by the ControlPlaneOperatorImageAnnotation on the
//    HostedCluster resource itself
// 2. The hypershift image specified in the release payload indicated by the
//    HostedCluster's release field
// 3. The hypershift-operator's own image for release versions 4.9 and 4.10
// 4. The registry.ci.openshift.org/hypershift/hypershift:4.8 image for release
//    version 4.8
//
// If no image can be found according to these rules, an error is returned.
func GetControlPlaneOperatorImage(ctx context.Context, hc *hyperv1.HostedCluster, releaseProvider releaseinfo.Provider, hypershiftOperatorImage string, pullSecret []byte) (string, error) {
	if val, ok := hc.Annotations[hyperv1.ControlPlaneOperatorImageAnnotation]; ok {
		return val, nil
	}
	releaseInfo, err := releaseProvider.Lookup(ctx, hc.Spec.Release.Image, pullSecret)
	if err != nil {
		return "", err
	}
	version, err := semver.Parse(releaseInfo.Version())
	if err != nil {
		return "", err
	}

	if hypershiftImage, exists := releaseInfo.ComponentImages()["hypershift"]; exists {
		return hypershiftImage, nil
	}

	if version.Minor < 9 {
		return "", fmt.Errorf("unsupported release image with version %s", version.String())
	}
	return hypershiftOperatorImage, nil
}

func reconcileControlPlaneOperatorDeployment(deployment *appsv1.Deployment, hc *hyperv1.HostedCluster, hcp *hyperv1.HostedControlPlane, cpoImage, utilitiesImage string, setDefaultSecurityContext bool, sa *corev1.ServiceAccount, enableCIDebugOutput bool, registryOverrideCommandLine, defaultIngressDomain string, cpoHasUtilities bool, metricsSet metrics.MetricsSet) error {
	cpoResources := corev1.ResourceRequirements{
		Requests: corev1.ResourceList{
			corev1.ResourceMemory: resource.MustParse("80Mi"),
			corev1.ResourceCPU:    resource.MustParse("10m"),
		},
	}
	// preserve existing resource requirements for main cpo container
	mainContainer := hyperutil.FindContainer("control-plane-operator", deployment.Spec.Template.Spec.Containers)
	if mainContainer != nil {
		if len(mainContainer.Resources.Requests) > 0 || len(mainContainer.Resources.Limits) > 0 {
			cpoResources = mainContainer.Resources
		}
	}

	args := []string{
		"run",
		"--namespace", "$(MY_NAMESPACE)",
		"--deployment-name", "control-plane-operator",
		"--metrics-addr", "0.0.0.0:8080",
		fmt.Sprintf("--enable-ci-debug-output=%t", enableCIDebugOutput),
		fmt.Sprintf("--registry-overrides=%s", registryOverrideCommandLine),
	}
	if !cpoHasUtilities {
		args = append(args,
			"--socks5-proxy-image", utilitiesImage,
			"--availability-prober-image", utilitiesImage,
			"--token-minter-image", utilitiesImage,
		)
	}

	deployment.Spec = appsv1.DeploymentSpec{
		Selector: &metav1.LabelSelector{
			MatchLabels: map[string]string{
				"name": "control-plane-operator",
			},
		},
		Template: corev1.PodTemplateSpec{
			ObjectMeta: metav1.ObjectMeta{
				Labels: map[string]string{
					"name":                        "control-plane-operator",
					"app":                         "control-plane-operator",
					hyperv1.ControlPlaneComponent: "control-plane-operator",
				},
			},
			Spec: corev1.PodSpec{
				ImagePullSecrets: []corev1.LocalObjectReference{
					{
						Name: "pull-secret",
					},
				},
				ServiceAccountName: sa.Name,
				Containers: []corev1.Container{
					{
						Name:            "control-plane-operator",
						Image:           cpoImage,
						ImagePullPolicy: corev1.PullIfNotPresent,
						Env: []corev1.EnvVar{
							{
								Name: "MY_NAMESPACE",
								ValueFrom: &corev1.EnvVarSource{
									FieldRef: &corev1.ObjectFieldSelector{
										FieldPath: "metadata.namespace",
									},
								},
							},
							{
								Name: "POD_NAME",
								ValueFrom: &corev1.EnvVarSource{
									FieldRef: &corev1.ObjectFieldSelector{
										FieldPath: "metadata.name",
									},
								},
							},
							{
								Name:  "OPERATE_ON_RELEASE_IMAGE",
								Value: hc.Spec.Release.Image,
							},
							metrics.MetricsSetToEnv(metricsSet),
						},
						Command: []string{"/usr/bin/control-plane-operator"},
						Args:    args,
						Ports:   []corev1.ContainerPort{{Name: "metrics", ContainerPort: 8080}},
						LivenessProbe: &corev1.Probe{
							ProbeHandler: corev1.ProbeHandler{
								HTTPGet: &corev1.HTTPGetAction{
									Path:   "/healthz",
									Port:   intstr.FromInt(6060),
									Scheme: corev1.URISchemeHTTP,
								},
							},
							InitialDelaySeconds: 60,
							PeriodSeconds:       60,
							SuccessThreshold:    1,
							FailureThreshold:    5,
							TimeoutSeconds:      5,
						},
						ReadinessProbe: &corev1.Probe{
							ProbeHandler: corev1.ProbeHandler{
								HTTPGet: &corev1.HTTPGetAction{
									Path:   "/readyz",
									Port:   intstr.FromInt(6060),
									Scheme: corev1.URISchemeHTTP,
								},
							},
							InitialDelaySeconds: 15,
							PeriodSeconds:       60,
							SuccessThreshold:    1,
							FailureThreshold:    3,
							TimeoutSeconds:      5,
						},
						Resources: cpoResources,
					},
				},
			},
		},
	}

	if envImage := os.Getenv(images.KonnectivityEnvVar); len(envImage) > 0 {
		deployment.Spec.Template.Spec.Containers[0].Env = append(deployment.Spec.Template.Spec.Containers[0].Env,
			corev1.EnvVar{
				Name:  images.KonnectivityEnvVar,
				Value: envImage,
			},
		)
	}
	if envImage := os.Getenv(images.AWSEncryptionProviderEnvVar); len(envImage) > 0 {
		deployment.Spec.Template.Spec.Containers[0].Env = append(deployment.Spec.Template.Spec.Containers[0].Env,
			corev1.EnvVar{
				Name:  images.AWSEncryptionProviderEnvVar,
				Value: envImage,
			},
		)
	}
	if len(defaultIngressDomain) > 0 {
		deployment.Spec.Template.Spec.Containers[0].Env = append(deployment.Spec.Template.Spec.Containers[0].Env,
			corev1.EnvVar{
				Name:  config.DefaultIngressDomainEnvVar,
				Value: defaultIngressDomain,
			},
		)
	}

	mainContainer = hyperutil.FindContainer("control-plane-operator", deployment.Spec.Template.Spec.Containers)
	proxy.SetEnvVars(&mainContainer.Env)

	// Add platform specific settings
	switch hc.Spec.Platform.Type {
	case hyperv1.AWSPlatform:
		deployment.Spec.Template.Spec.Volumes = append(deployment.Spec.Template.Spec.Volumes,
			corev1.Volume{
				Name: "cloud-token",
				VolumeSource: corev1.VolumeSource{
					EmptyDir: &corev1.EmptyDirVolumeSource{
						Medium: corev1.StorageMediumMemory,
					},
				},
			},
			corev1.Volume{
				Name: "provider-creds",
				VolumeSource: corev1.VolumeSource{
					Secret: &corev1.SecretVolumeSource{
						SecretName: platformaws.ControlPlaneOperatorCredsSecret("").Name,
					},
				},
			})
		deployment.Spec.Template.Spec.Containers[0].Env = append(deployment.Spec.Template.Spec.Containers[0].Env,
			corev1.EnvVar{
				Name:  "AWS_SHARED_CREDENTIALS_FILE",
				Value: "/etc/provider/credentials",
			},
			corev1.EnvVar{
				Name:  "AWS_REGION",
				Value: hc.Spec.Platform.AWS.Region,
			},
			corev1.EnvVar{
				Name:  "AWS_SDK_LOAD_CONFIG",
				Value: "true",
			})
		deployment.Spec.Template.Spec.Containers[0].VolumeMounts = append(deployment.Spec.Template.Spec.Containers[0].VolumeMounts,
			corev1.VolumeMount{
				Name:      "cloud-token",
				MountPath: "/var/run/secrets/openshift/serviceaccount",
			},
			corev1.VolumeMount{
				Name:      "provider-creds",
				MountPath: "/etc/provider",
			})
		deployment.Spec.Template.Spec.Containers = append(deployment.Spec.Template.Spec.Containers, corev1.Container{
			Name:            "token-minter",
			Image:           utilitiesImage,
			ImagePullPolicy: corev1.PullIfNotPresent,
			Command:         []string{"/usr/bin/control-plane-operator", "token-minter"},
			Args: []string{
				"--service-account-namespace=kube-system",
				"--service-account-name=control-plane-operator",
				"--token-audience=openshift",
				"--token-file=/var/run/secrets/openshift/serviceaccount/token",
				fmt.Sprintf("--kubeconfig-secret-namespace=%s", deployment.Namespace),
				"--kubeconfig-secret-name=service-network-admin-kubeconfig",
			},
			Resources: corev1.ResourceRequirements{
				Requests: corev1.ResourceList{
					corev1.ResourceCPU:    resource.MustParse("10m"),
					corev1.ResourceMemory: resource.MustParse("10Mi"),
				},
			},
			VolumeMounts: []corev1.VolumeMount{
				{
					Name:      "cloud-token",
					MountPath: "/var/run/secrets/openshift/serviceaccount",
				},
			},
		})
	}

	if hc.Spec.AdditionalTrustBundle != nil {
		// Add trusted-ca mount with optional configmap
		hyperutil.DeploymentAddTrustBundleVolume(hc.Spec.AdditionalTrustBundle, deployment)
	}

	// set security context
	if setDefaultSecurityContext {
		deployment.Spec.Template.Spec.SecurityContext = &corev1.PodSecurityContext{
			RunAsUser: k8sutilspointer.Int64Ptr(config.DefaultSecurityContextUser),
		}
	}

	deploymentConfig := config.DeploymentConfig{
		Scheduling: config.Scheduling{
			PriorityClass: config.DefaultPriorityClass,
		},
		SetDefaultSecurityContext: setDefaultSecurityContext,
	}

	deploymentConfig.SetDefaults(hcp, nil, k8sutilspointer.Int(1))
	deploymentConfig.ApplyTo(deployment)
	deploymentConfig.SetRestartAnnotation(hc.ObjectMeta)
	return nil
}

func reconcileControlPlaneOperatorRole(role *rbacv1.Role) error {
	role.Rules = []rbacv1.PolicyRule{
		{
			APIGroups: []string{"hypershift.openshift.io"},
			Resources: []string{"*"},
			Verbs:     []string{"*"},
		},
		{
			APIGroups: []string{
				"bootstrap.cluster.x-k8s.io",
				"controlplane.cluster.x-k8s.io",
				"infrastructure.cluster.x-k8s.io",
				"machines.cluster.x-k8s.io",
				"exp.infrastructure.cluster.x-k8s.io",
				"addons.cluster.x-k8s.io",
				"exp.cluster.x-k8s.io",
				"cluster.x-k8s.io",
				"monitoring.coreos.com",
			},
			Resources: []string{"*"},
			Verbs:     []string{"*"},
		},
		{
			APIGroups: []string{"rbac.authorization.k8s.io"},
			Resources: []string{"roles", "rolebindings"},
			Verbs: []string{
				"get",
				"list",
				"watch",
				"create",
				"update",
				"patch",
				"delete",
			},
		},
		{
			APIGroups: []string{"route.openshift.io"},
			Resources: []string{"*"},
			Verbs:     []string{"*"},
		},
		{
			APIGroups: []string{""},
			Resources: []string{
				"events",
				"configmaps",
				"persistentvolumeclaims",
				"pods",
				"pods/log",
				"secrets",
				"nodes",
				"serviceaccounts",
				"services",
				"endpoints",
			},
			Verbs: []string{"*"},
		},
		{
			APIGroups: []string{"apps"},
			Resources: []string{"deployments", "statefulsets"},
			Verbs:     []string{"*"},
		},
		{
			APIGroups: []string{"batch"},
			Resources: []string{"cronjobs", "jobs"},
			Verbs:     []string{"*"},
		},
		{
			APIGroups: []string{"policy"},
			Resources: []string{"poddisruptionbudgets"},
			Verbs:     []string{"*"},
		},
		{
			APIGroups: []string{"coordination.k8s.io"},
			Resources: []string{
				"leases",
			},
			Verbs: []string{"*"},
		},
		{
			APIGroups: []string{
				"discovery.k8s.io",
			},
			Resources: []string{
				"endpointslices",
			},
			Verbs: []string{
				"list",
				"watch",
			},
		},
		{
			APIGroups:     []string{"security.openshift.io"},
			ResourceNames: []string{"hostnetwork"},
			Resources:     []string{"securitycontextconstraints"},
			Verbs:         []string{"use"},
		},
		// This is needed for CPO to grant Autoscaler its RBAC policy.
		{
			APIGroups: []string{"cluster.x-k8s.io"},
			Resources: []string{
				"machinedeployments",
				"machinedeployments/scale",
				"machines",
				"machinesets",
				"machinesets/scale",
			},
			Verbs: []string{"*"},
		},
		{
			APIGroups: []string{"apiextensions.k8s.io"},
			Resources: []string{"customresourcedefinitions"},
			Verbs:     []string{"get", "list", "watch"},
		},
	}
	return nil
}

func reconcileControlPlaneOperatorRoleBinding(binding *rbacv1.RoleBinding, role *rbacv1.Role, sa *corev1.ServiceAccount) error {
	binding.RoleRef = rbacv1.RoleRef{
		APIGroup: "rbac.authorization.k8s.io",
		Kind:     "Role",
		Name:     role.Name,
	}

	binding.Subjects = []rbacv1.Subject{
		{
			Kind:      "ServiceAccount",
			Name:      sa.Name,
			Namespace: sa.Namespace,
		},
	}

	return nil
}

func reconcileControlPlaneOperatorIngressRole(role *rbacv1.Role) error {
	role.Rules = []rbacv1.PolicyRule{
		{
			APIGroups: []string{""},
			Resources: []string{"services"},
			Verbs:     []string{"get", "list", "watch"},
		},
	}
	return nil
}

func reconcileControlPlaneOperatorIngressRoleBinding(binding *rbacv1.RoleBinding, role *rbacv1.Role, sa *corev1.ServiceAccount) error {
	binding.RoleRef = rbacv1.RoleRef{
		APIGroup: "rbac.authorization.k8s.io",
		Kind:     "Role",
		Name:     role.Name,
	}

	binding.Subjects = []rbacv1.Subject{
		{
			Kind:      "ServiceAccount",
			Name:      sa.Name,
			Namespace: sa.Namespace,
		},
	}

	return nil
}

func reconcilecontrolPlaneOperatorIngressOperatorRole(role *rbacv1.Role) error {
	role.Rules = []rbacv1.PolicyRule{
		{
			APIGroups: []string{"operator.openshift.io"},
			Resources: []string{"ingresscontrollers"},
			Verbs:     []string{"*"},
		},
	}
	return nil
}

func reconcilecontrolPlaneOperatorIngressOperatorRoleBinding(binding *rbacv1.RoleBinding, role *rbacv1.Role, sa *corev1.ServiceAccount) error {
	binding.RoleRef = rbacv1.RoleRef{
		APIGroup: "rbac.authorization.k8s.io",
		Kind:     "Role",
		Name:     role.Name,
	}

	binding.Subjects = []rbacv1.Subject{
		{
			Kind:      "ServiceAccount",
			Name:      sa.Name,
			Namespace: sa.Namespace,
		},
	}

	return nil
}

func reconcileCAPICluster(cluster *capiv1.Cluster, hcluster *hyperv1.HostedCluster, hcp *hyperv1.HostedControlPlane, infraCR client.Object) error {
	// We only create this resource once and then let CAPI own it
	if !cluster.CreationTimestamp.IsZero() {
		return nil
	}
	infraCRGVK, err := apiutil.GVKForObject(infraCR, api.Scheme)
	if err != nil {
		return fmt.Errorf("failed to get gvk for %T: %w", infraCR, err)
	}

	cluster.Annotations = map[string]string{
		HostedClusterAnnotation: client.ObjectKeyFromObject(hcluster).String(),
	}
	cluster.Spec = capiv1.ClusterSpec{
		ControlPlaneEndpoint: capiv1.APIEndpoint{},
		ControlPlaneRef: &corev1.ObjectReference{
			APIVersion: "hypershift.openshift.io/v1alpha1",
			Kind:       "HostedControlPlane",
			Namespace:  hcp.Namespace,
			Name:       hcp.Name,
		},
		InfrastructureRef: &corev1.ObjectReference{
			APIVersion: infraCRGVK.GroupVersion().String(),
			Kind:       infraCRGVK.Kind,
			Namespace:  infraCR.GetNamespace(),
			Name:       infraCR.GetName(),
		},
	}

	return nil
}

func reconcileCAPIManagerDeployment(deployment *appsv1.Deployment, hc *hyperv1.HostedCluster, hcp *hyperv1.HostedControlPlane, sa *corev1.ServiceAccount, capiManagerImage string, setDefaultSecurityContext bool) error {
	defaultMode := int32(416)
	capiManagerLabels := map[string]string{
		"name":                        "cluster-api",
		"app":                         "cluster-api",
		hyperv1.ControlPlaneComponent: "cluster-api",
	}
	deployment.Spec = appsv1.DeploymentSpec{
		Selector: &metav1.LabelSelector{
			MatchLabels: capiManagerLabels,
		},
		Template: corev1.PodTemplateSpec{
			ObjectMeta: metav1.ObjectMeta{
				Labels: capiManagerLabels,
			},
			Spec: corev1.PodSpec{
				ServiceAccountName: sa.Name,
				Volumes: []corev1.Volume{
					{
						Name: "capi-webhooks-tls",
						VolumeSource: corev1.VolumeSource{
							Secret: &corev1.SecretVolumeSource{
								DefaultMode: &defaultMode,
								SecretName:  "capi-webhooks-tls",
							},
						},
					},
				},
				Containers: []corev1.Container{
					{
						Name:            "manager",
						Image:           capiManagerImage,
						ImagePullPolicy: corev1.PullIfNotPresent,
						Env: []corev1.EnvVar{
							{
								Name: "MY_NAMESPACE",
								ValueFrom: &corev1.EnvVarSource{
									FieldRef: &corev1.ObjectFieldSelector{
										FieldPath: "metadata.namespace",
									},
								},
							},
						},
						Command: []string{"/manager"},
						Args: []string{"--namespace", "$(MY_NAMESPACE)",
							"--alsologtostderr",
							"--v=4",
							"--leader-elect=true",
							fmt.Sprintf("--leader-elect-lease-duration=%s", config.RecommendedLeaseDuration),
							fmt.Sprintf("--leader-elect-retry-period=%s", config.RecommendedRetryPeriod),
							fmt.Sprintf("--leader-elect-renew-deadline=%s", config.RecommendedRenewDeadline),
						},
						LivenessProbe: &corev1.Probe{
							ProbeHandler: corev1.ProbeHandler{
								HTTPGet: &corev1.HTTPGetAction{
									Path:   "/healthz",
									Port:   intstr.FromInt(9440),
									Scheme: corev1.URISchemeHTTP,
								},
							},
							InitialDelaySeconds: 60,
							PeriodSeconds:       60,
							SuccessThreshold:    1,
							FailureThreshold:    5,
							TimeoutSeconds:      5,
						},
						ReadinessProbe: &corev1.Probe{
							ProbeHandler: corev1.ProbeHandler{
								HTTPGet: &corev1.HTTPGetAction{
									Path:   "/readyz",
									Port:   intstr.FromInt(9440),
									Scheme: corev1.URISchemeHTTP,
								},
							},
							InitialDelaySeconds: 15,
							PeriodSeconds:       60,
							SuccessThreshold:    1,
							FailureThreshold:    3,
							TimeoutSeconds:      5,
						},
						Resources: corev1.ResourceRequirements{
							Requests: corev1.ResourceList{
								corev1.ResourceMemory: resource.MustParse("20Mi"),
								corev1.ResourceCPU:    resource.MustParse("10m"),
							},
						},
						VolumeMounts: []corev1.VolumeMount{
							{
								Name:      "capi-webhooks-tls",
								ReadOnly:  true,
								MountPath: "/tmp/k8s-webhook-server/serving-certs",
							},
						},
					},
				},
			},
		},
	}
	// set security context
	if setDefaultSecurityContext {
		deployment.Spec.Template.Spec.SecurityContext = &corev1.PodSecurityContext{
			RunAsUser: k8sutilspointer.Int64Ptr(config.DefaultSecurityContextUser),
		}
	}

	deploymentConfig := config.DeploymentConfig{
		Scheduling: config.Scheduling{
			PriorityClass: config.DefaultPriorityClass,
		},
		SetDefaultSecurityContext: setDefaultSecurityContext,
	}

	deploymentConfig.SetDefaults(hcp, nil, k8sutilspointer.Int(1))
	deploymentConfig.ApplyTo(deployment)
	deploymentConfig.SetRestartAnnotation(hc.ObjectMeta)
	return nil
}

func reconcileCAPIManagerClusterRole(role *rbacv1.ClusterRole) error {
	role.Rules = []rbacv1.PolicyRule{
		{
			APIGroups: []string{"apiextensions.k8s.io"},
			Resources: []string{"customresourcedefinitions"},
			Verbs:     []string{"get", "list", "watch"},
		},
	}
	return nil
}

func reconcileCAPIManagerClusterRoleBinding(binding *rbacv1.ClusterRoleBinding, role *rbacv1.ClusterRole, sa *corev1.ServiceAccount) error {
	binding.RoleRef = rbacv1.RoleRef{
		APIGroup: "rbac.authorization.k8s.io",
		Kind:     "ClusterRole",
		Name:     role.Name,
	}

	binding.Subjects = []rbacv1.Subject{
		{
			Kind:      "ServiceAccount",
			Name:      sa.Name,
			Namespace: sa.Namespace,
		},
	}
	return nil
}

func reconcileCAPIManagerRole(role *rbacv1.Role) error {
	role.Rules = []rbacv1.PolicyRule{
		{
			APIGroups: []string{
				"bootstrap.cluster.x-k8s.io",
				"controlplane.cluster.x-k8s.io",
				"infrastructure.cluster.x-k8s.io",
				"machines.cluster.x-k8s.io",
				"exp.infrastructure.cluster.x-k8s.io",
				"addons.cluster.x-k8s.io",
				"exp.cluster.x-k8s.io",
				"cluster.x-k8s.io",
			},
			Resources: []string{"*"},
			Verbs:     []string{"*"},
		},
		{
			APIGroups: []string{"hypershift.openshift.io"},
			Resources: []string{
				"hostedcontrolplanes",
				"hostedcontrolplanes/status",
			},
			Verbs: []string{"*"},
		},
		{
			APIGroups: []string{""},
			Resources: []string{
				"configmaps",
				"events",
				"nodes",
				"secrets",
			},
			Verbs: []string{"*"},
		},
		{
			APIGroups: []string{"coordination.k8s.io"},
			Resources: []string{
				"leases",
			},
			Verbs: []string{"*"},
		},
		{
			APIGroups: []string{"capi-provider.agent-install.openshift.io"},
			Resources: []string{"*"},
			Verbs:     []string{"*"},
		},
	}
	return nil
}

func reconcileCAPIManagerRoleBinding(binding *rbacv1.RoleBinding, role *rbacv1.Role, sa *corev1.ServiceAccount) error {
	binding.RoleRef = rbacv1.RoleRef{
		APIGroup: "rbac.authorization.k8s.io",
		Kind:     "Role",
		Name:     role.Name,
	}

	binding.Subjects = []rbacv1.Subject{
		{
			Kind:      "ServiceAccount",
			Name:      sa.Name,
			Namespace: sa.Namespace,
		},
	}

	return nil
}

func reconcileCAPIProviderRole(role *rbacv1.Role, p platform.Platform) error {
	rules := []rbacv1.PolicyRule{
		{
			APIGroups: []string{""},
			Resources: []string{
				"events",
				"secrets",
				"configmaps",
			},
			Verbs: []string{"*"},
		},
		{
			APIGroups: []string{
				"bootstrap.cluster.x-k8s.io",
				"controlplane.cluster.x-k8s.io",
				"infrastructure.cluster.x-k8s.io",
				"machines.cluster.x-k8s.io",
				"exp.infrastructure.cluster.x-k8s.io",
				"addons.cluster.x-k8s.io",
				"exp.cluster.x-k8s.io",
				"cluster.x-k8s.io",
			},
			Resources: []string{"*"},
			Verbs:     []string{"*"},
		},
		{
			APIGroups: []string{"hypershift.openshift.io"},
			Resources: []string{"*"},
			Verbs:     []string{"*"},
		},
		{
			APIGroups: []string{"coordination.k8s.io"},
			Resources: []string{
				"leases",
			},
			Verbs: []string{"*"},
		},
	}
	if platformRules := p.CAPIProviderPolicyRules(); platformRules != nil {
		rules = append(rules, platformRules...)
	}
	role.Rules = rules
	return nil
}

func reconcileCAPIProviderRoleBinding(binding *rbacv1.RoleBinding, role *rbacv1.Role, sa *corev1.ServiceAccount) error {
	binding.RoleRef = rbacv1.RoleRef{
		APIGroup: "rbac.authorization.k8s.io",
		Kind:     "Role",
		Name:     role.Name,
	}

	binding.Subjects = []rbacv1.Subject{
		{
			Kind:      "ServiceAccount",
			Name:      sa.Name,
			Namespace: sa.Namespace,
		},
	}
	return nil
}

// computeClusterVersionStatus determines the ClusterVersionStatus of the
// given HostedCluster and returns it.
func computeClusterVersionStatus(clock clock.WithTickerAndDelayedExecution, hcluster *hyperv1.HostedCluster, hcp *hyperv1.HostedControlPlane) *hyperv1.ClusterVersionStatus {
	// If there's no history, rebuild it from scratch.
	if hcluster.Status.Version == nil || len(hcluster.Status.Version.History) == 0 {
		return &hyperv1.ClusterVersionStatus{
			Desired:            hcluster.Spec.Release,
			ObservedGeneration: hcluster.Generation,
			History: []configv1.UpdateHistory{
				{
					State:       configv1.PartialUpdate,
					Image:       hcluster.Spec.Release.Image,
					StartedTime: metav1.NewTime(clock.Now()),
				},
			},
		}
	}

	// Reconcile the current version with the latest resource states.
	version := hcluster.Status.Version.DeepCopy()

	// If the hosted control plane doesn't exist, there's no way to assess the
	// rollout so return early.
	if hcp == nil {
		return version
	}

	// If a rollout is in progress, we need to wait before updating.
	// TODO: This is a potentially weak check. Conditions checks don't seem
	// quite right because the intent here is to identify a terminal rollout
	// state. For now it assumes when status.releaseImage matches, that rollout
	// is definitely done.
	hcpRolloutComplete := (hcp.Spec.ReleaseImage == hcp.Status.ReleaseImage) && (version.Desired.Image == hcp.Status.ReleaseImage)
	if !hcpRolloutComplete {
		return version
	}

	// The rollout is complete, so update the current history entry
	version.History[0].State = configv1.CompletedUpdate
	version.History[0].Version = hcp.Status.Version
	if hcp.Status.LastReleaseImageTransitionTime != nil {
		version.History[0].CompletionTime = hcp.Status.LastReleaseImageTransitionTime.DeepCopy()
	}

	// If a new rollout is needed, update the desired version and prepend a new
	// partial history entry to unblock rollouts.
	rolloutNeeded := hcluster.Spec.Release.Image != hcluster.Status.Version.Desired.Image
	if rolloutNeeded {
		version.Desired.Image = hcluster.Spec.Release.Image
		version.ObservedGeneration = hcluster.Generation
		// TODO: leaky
		version.History = append([]configv1.UpdateHistory{
			{
				State:       configv1.PartialUpdate,
				Image:       hcluster.Spec.Release.Image,
				StartedTime: metav1.NewTime(clock.Now()),
			},
		}, version.History...)
	}

	return version
}

// computeHostedClusterAvailability determines the Available condition for the
// given HostedCluster and returns it.
func computeHostedClusterAvailability(hcluster *hyperv1.HostedCluster, hcp *hyperv1.HostedControlPlane) metav1.Condition {
	// Determine whether the hosted control plane is available.
	hcpAvailableStatus := metav1.ConditionFalse
	hcpAvailableMessage := "Waiting for hosted control plane to be healthy"
	hcpAvailableReason := hyperv1.HostedClusterWaitingForAvailableReason
	var hcpAvailableCondition *metav1.Condition
	if hcp != nil {
		hcpAvailableCondition = meta.FindStatusCondition(hcp.Status.Conditions, string(hyperv1.HostedControlPlaneAvailable))
	}
	if hcpAvailableCondition != nil {
		hcpAvailableStatus = hcpAvailableCondition.Status
		hcpAvailableMessage = hcpAvailableCondition.Message
		if hcpAvailableStatus == metav1.ConditionTrue {
			hcpAvailableReason = hyperv1.HostedClusterAsExpectedReason
			hcpAvailableMessage = "The hosted control plane is available"
		}
	}
	return metav1.Condition{
		Type:               string(hyperv1.HostedClusterAvailable),
		Status:             hcpAvailableStatus,
		ObservedGeneration: hcluster.Generation,
		Reason:             hcpAvailableReason,
		Message:            hcpAvailableMessage,
	}
}

// computeUnmanagedEtcdAvailability calculates the current status of unmanaged etcd.
func computeUnmanagedEtcdAvailability(hcluster *hyperv1.HostedCluster, unmanagedEtcdTLSClientSecret *corev1.Secret) metav1.Condition {
	if unmanagedEtcdTLSClientSecret == nil {
		return metav1.Condition{
			Type:    string(hyperv1.UnmanagedEtcdAvailable),
			Status:  metav1.ConditionFalse,
			Reason:  hyperv1.UnmanagedEtcdMisconfiguredReason,
			Message: fmt.Sprintf("missing TLS client secret %s", hcluster.Spec.Etcd.Unmanaged.TLS.ClientSecret.Name),
		}
	}
	if hcluster.Spec.Etcd.Unmanaged == nil || len(hcluster.Spec.Etcd.Unmanaged.TLS.ClientSecret.Name) == 0 || len(hcluster.Spec.Etcd.Unmanaged.Endpoint) == 0 {
		return metav1.Condition{
			Type:    string(hyperv1.UnmanagedEtcdAvailable),
			Status:  metav1.ConditionFalse,
			Reason:  hyperv1.UnmanagedEtcdMisconfiguredReason,
			Message: "etcd metadata not specified for unmanaged deployment",
		}
	}
	if _, ok := unmanagedEtcdTLSClientSecret.Data["etcd-client.crt"]; !ok {
		return metav1.Condition{
			Type:    string(hyperv1.UnmanagedEtcdAvailable),
			Status:  metav1.ConditionFalse,
			Reason:  hyperv1.UnmanagedEtcdMisconfiguredReason,
			Message: fmt.Sprintf("etcd secret %s does not have client cert", hcluster.Spec.Etcd.Unmanaged.TLS.ClientSecret.Name),
		}
	}
	if _, ok := unmanagedEtcdTLSClientSecret.Data["etcd-client.key"]; !ok {
		return metav1.Condition{
			Type:    string(hyperv1.UnmanagedEtcdAvailable),
			Status:  metav1.ConditionFalse,
			Reason:  hyperv1.UnmanagedEtcdMisconfiguredReason,
			Message: fmt.Sprintf("etcd secret %s does not have client key", hcluster.Spec.Etcd.Unmanaged.TLS.ClientSecret.Name),
		}
	}
	if _, ok := unmanagedEtcdTLSClientSecret.Data["etcd-client-ca.crt"]; !ok {
		return metav1.Condition{
			Type:    string(hyperv1.UnmanagedEtcdAvailable),
			Status:  metav1.ConditionFalse,
			Reason:  hyperv1.UnmanagedEtcdMisconfiguredReason,
			Message: fmt.Sprintf("etcd secret %s does not have client ca", hcluster.Spec.Etcd.Unmanaged.TLS.ClientSecret.Name),
		}
	}
	return metav1.Condition{
		Type:   string(hyperv1.UnmanagedEtcdAvailable),
		Status: metav1.ConditionTrue,
		Reason: hyperv1.UnmanagedEtcdAsExpected,
	}
}

func listNodePools(ctx context.Context, c client.Client, clusterNamespace, clusterName string) ([]hyperv1.NodePool, error) {
	nodePoolList := &hyperv1.NodePoolList{}
	if err := c.List(ctx, nodePoolList); err != nil {
		return nil, fmt.Errorf("failed getting nodePool list: %v", err)
	}
	// TODO: do a label association or something
	filtered := []hyperv1.NodePool{}
	for i, nodePool := range nodePoolList.Items {
		if nodePool.Namespace == clusterNamespace && nodePool.Spec.ClusterName == clusterName {
			filtered = append(filtered, nodePoolList.Items[i])
		}
	}
	return filtered, nil
}

func (r *HostedClusterReconciler) deleteNodePools(ctx context.Context, c client.Client, namespace, name string) error {
	nodePools, err := listNodePools(ctx, c, namespace, name)
	if err != nil {
		return fmt.Errorf("failed to get NodePools by cluster name for cluster %q: %w", name, err)
	}
	for key, nodePool := range nodePools {
		if nodePool.DeletionTimestamp != nil {
			continue
		}
		if err := c.Delete(ctx, &nodePools[key]); err != nil && !apierrors.IsNotFound(err) {
			return fmt.Errorf("failed to delete NodePool %q for cluster %q: %w", nodePool.GetName(), name, err)
		}
	}
	return nil
}

func deleteAWSEndpointServices(ctx context.Context, c client.Client, namespace string) (bool, error) {
	var awsEndpointServiceList hyperv1.AWSEndpointServiceList
	if err := c.List(ctx, &awsEndpointServiceList, &client.ListOptions{Namespace: namespace}); err != nil && !apierrors.IsNotFound(err) {
		return false, fmt.Errorf("error listing awsendpointservices in namespace %s: %w", namespace, err)
	}
	for _, ep := range awsEndpointServiceList.Items {
		if ep.DeletionTimestamp != nil {
			continue
		}
		if err := c.Delete(ctx, &ep); err != nil && !apierrors.IsNotFound(err) {
			return false, fmt.Errorf("error deleting awsendpointservices %s in namespace %s: %w", ep.Name, namespace, err)
		}
	}
	if len(awsEndpointServiceList.Items) != 0 {
		// The CPO puts a finalizer on AWSEndpointService resources and should
		// not be terminated until the resources are removed from the API server
		return true, nil
	}
	return false, nil
}

func deleteControlPlaneOperatorRBAC(ctx context.Context, c client.Client, rbacNamespace string, controlPlaneNamespace string) error {
	if _, err := hyperutil.DeleteIfNeeded(ctx, c, &rbacv1.Role{ObjectMeta: metav1.ObjectMeta{Name: "control-plane-operator-" + controlPlaneNamespace, Namespace: rbacNamespace}}); err != nil {
		return err
	}
	if _, err := hyperutil.DeleteIfNeeded(ctx, c, &rbacv1.RoleBinding{ObjectMeta: metav1.ObjectMeta{Name: "control-plane-operator-" + controlPlaneNamespace, Namespace: rbacNamespace}}); err != nil {
		return err
	}
	return nil
}

func (r *HostedClusterReconciler) delete(ctx context.Context, hc *hyperv1.HostedCluster) (bool, error) {
	controlPlaneNamespace := manifests.HostedControlPlaneNamespace(hc.Namespace, hc.Name).Name
	log := ctrl.LoggerFrom(ctx)

	// ensure that the cleanup annotation has been propagated to the hcp if it is set
	if hc.Annotations[hyperv1.CleanupCloudResourcesAnnotation] == "true" {
		hcp := controlplaneoperator.HostedControlPlane(controlPlaneNamespace, hc.Name)
		err := r.Get(ctx, client.ObjectKeyFromObject(hcp), hcp)
		if err != nil && !apierrors.IsNotFound(err) {
			return false, fmt.Errorf("cannot get hosted control plane: %w", err)
		}
		if err == nil && hcp.Annotations[hyperv1.CleanupCloudResourcesAnnotation] != "true" {
			original := hcp.DeepCopy()
			if hcp.Annotations == nil {
				hcp.Annotations = map[string]string{}
			}
			hcp.Annotations[hyperv1.CleanupCloudResourcesAnnotation] = "true"
			if err := r.Patch(ctx, hcp, client.MergeFromWithOptions(original)); err != nil {
				return false, fmt.Errorf("cannot patch hosted control plane with cleanup annotation: %w", err)
			}
		}
	}

	err := r.deleteNodePools(ctx, r.Client, hc.Namespace, hc.Name)
	if err != nil {
		return false, err
	}

	if hc != nil && len(hc.Spec.InfraID) > 0 {
		exists, err := hyperutil.DeleteIfNeeded(ctx, r.Client, &capiv1.Cluster{
			ObjectMeta: metav1.ObjectMeta{
				Name:      hc.Spec.InfraID,
				Namespace: controlPlaneNamespace,
			},
		})
		if err != nil {
			return false, err
		}
		if exists {
			log.Info("Waiting for cluster deletion", "clusterName", hc.Spec.InfraID, "controlPlaneNamespace", controlPlaneNamespace)
			return false, nil
		}
	}

	// Cleanup Platform specifics.
	p, err := platform.GetPlatform(hc, "")
	if err != nil {
		return false, err
	}

	if err = p.DeleteCredentials(ctx, r.Client, hc,
		controlPlaneNamespace); err != nil {
		return false, err
	}

	exists, err := deleteAWSEndpointServices(ctx, r.Client, controlPlaneNamespace)
	if err != nil {
		return false, err
	}
	if exists {
		log.Info("Waiting for awsendpointservice deletion", "controlPlaneNamespace", controlPlaneNamespace)
		return false, nil
	}

	if r.ManagementClusterCapabilities.Has(capabilities.CapabilityRoute) {
		err = deleteControlPlaneOperatorRBAC(ctx, r.Client, "openshift-ingress", controlPlaneNamespace)
		if err != nil {
			return false, fmt.Errorf("failed to clean up control plane operator ingress RBAC: %w", err)
		}

		err = deleteControlPlaneOperatorRBAC(ctx, r.Client, "openshift-ingress-operator", controlPlaneNamespace)
		if err != nil {
			return false, fmt.Errorf("failed to clean up control plane operator ingress operator RBAC: %w", err)
		}
	}

	_, err = hyperutil.DeleteIfNeeded(ctx, r.Client, clusterapi.CAPIManagerClusterRoleBinding(controlPlaneNamespace))
	if err != nil {
		return false, err
	}

	// There are scenarios where CAPI might not be operational e.g None Platform.
	// We want to ensure the HCP resource is deleted before deleting the Namespace.
	// Otherwise the CPO will be deleted leaving the HCP in a perpetual terminating state preventing further progress.
	// NOTE: The advancing case is when Get() or Delete() returns an error that the HCP is not found
	exists, err = hyperutil.DeleteIfNeeded(ctx, r.Client, controlplaneoperator.HostedControlPlane(controlPlaneNamespace, hc.Name))
	if err != nil {
		return false, err
	}
	if exists {
		log.Info("Waiting for hostedcontrolplane deletion", "controlPlaneNamespace", controlPlaneNamespace)
		return false, nil
	}

	if err := r.cleanupOIDCBucketData(ctx, log, hc); err != nil {
		return false, fmt.Errorf("failed to clean up OIDC bucket data: %w", err)
	}

	// Block until the namespace is deleted, so that if a hostedcluster is deleted and then re-created with the same name
	// we don't error initially because we can not create new content in a namespace that is being deleted.
	exists, err = hyperutil.DeleteIfNeeded(ctx, r.Client, &corev1.Namespace{
		ObjectMeta: metav1.ObjectMeta{Name: controlPlaneNamespace},
	})
	if err != nil {
		return false, err
	}
	if exists {
		log.Info("Waiting for namespace deletion", "controlPlaneNamespace", controlPlaneNamespace)
		return false, nil
	}

	return true, nil
}

func enqueueParentHostedCluster(obj client.Object) []reconcile.Request {
	var hostedClusterName string
	if obj.GetAnnotations() != nil {
		hostedClusterName = obj.GetAnnotations()[HostedClusterAnnotation]
	}
	if hostedClusterName == "" {
		return []reconcile.Request{}
	}
	return []reconcile.Request{
		{NamespacedName: hyperutil.ParseNamespacedName(hostedClusterName)},
	}
}

func (r *HostedClusterReconciler) reconcileMachineConfigServer(ctx context.Context, createOrUpdate upsert.CreateOrUpdateFN, hcluster *hyperv1.HostedCluster) error {

	controlPlaneNamespace := manifests.HostedControlPlaneNamespace(hcluster.Namespace, hcluster.Name)
	if err := r.Client.Get(ctx, client.ObjectKeyFromObject(controlPlaneNamespace), controlPlaneNamespace); err != nil {
		return fmt.Errorf("failed to get control plane namespace: %w", err)
	}

	// Reconcile service
	mcsService := ignitionserver.MCSService(controlPlaneNamespace.Name)
	if _, err := createOrUpdate(ctx, r.Client, mcsService, func() error {
		return reconcileMachineConfigServerService(mcsService)
	}); err != nil {
		return fmt.Errorf("failed to reconcile machine config server service: %w", err)
	}

	return nil
}

func reconcileMachineConfigServerService(svc *corev1.Service) error {
	svc.Spec.Selector = map[string]string{
		"app": "machine-config-server",
	}
	var portSpec corev1.ServicePort
	if len(svc.Spec.Ports) > 0 {
		portSpec = svc.Spec.Ports[0]
	} else {
		svc.Spec.Ports = []corev1.ServicePort{portSpec}
	}
	portSpec.Port = int32(8443)
	portSpec.Name = "https"
	portSpec.Protocol = corev1.ProtocolTCP
	portSpec.TargetPort = intstr.FromInt(8443)
	svc.Spec.Ports[0] = portSpec
	svc.Spec.Type = corev1.ServiceTypeClusterIP
	svc.Spec.ClusterIP = corev1.ClusterIPNone
	return nil
}

func (r *HostedClusterReconciler) reconcileClusterPrometheusRBAC(ctx context.Context, createOrUpdate upsert.CreateOrUpdateFN, namespace string) error {
	role := &rbacv1.Role{ObjectMeta: metav1.ObjectMeta{Namespace: namespace, Name: "openshift-prometheus"}}
	if _, err := createOrUpdate(ctx, r.Client, role, func() error {
		role.Rules = []rbacv1.PolicyRule{{
			APIGroups: []string{""},
			Resources: []string{
				"services",
				"endpoints",
				"pods",
			},
			Verbs: []string{
				"get",
				"list",
				"watch",
			},
		}}
		return nil
	}); err != nil {
		return fmt.Errorf("failed to ensure the %s role: %w", role.Name, err)
	}

	binding := &rbacv1.RoleBinding{ObjectMeta: metav1.ObjectMeta{Namespace: namespace, Name: "openshift-prometheus"}}
	if _, err := createOrUpdate(ctx, r.Client, binding, func() error {
		binding.RoleRef.APIGroup = "rbac.authorization.k8s.io"
		binding.RoleRef.Kind = "Role"
		binding.RoleRef.Name = role.Name
		binding.Subjects = []rbacv1.Subject{{
			Kind:      "ServiceAccount",
			Name:      "prometheus-k8s",
			Namespace: "openshift-monitoring",
		}}
		return nil
	}); err != nil {
		return fmt.Errorf("failed to ensure the %s rolebinding: %w", binding.Name, err)
	}

	return nil
}

func (r *HostedClusterReconciler) reconcileMachineApprover(ctx context.Context, createOrUpdate upsert.CreateOrUpdateFN, hcluster *hyperv1.HostedCluster, hcp *hyperv1.HostedControlPlane, utilitiesImage string) error {
	machineApproverImage, err := r.getPayloadImage(ctx, hcluster, ImageStreamClusterMachineApproverImage)
	if err != nil {
		return fmt.Errorf("failed to get image for machine approver: %w", err)
	}
	// TODO: can remove this override when all IBM production clusters upgraded to a version that uses the release image
	if imageVal, ok := hcluster.Annotations[hyperv1.MachineApproverImage]; ok {
		machineApproverImage = imageVal
	}

	return machineapprover.ReconcileMachineApprover(ctx, r.Client, hcp, machineApproverImage, utilitiesImage, createOrUpdate, r.SetDefaultSecurityContext)
}

func (r *HostedClusterReconciler) reconcileNetworkPolicies(ctx context.Context, createOrUpdate upsert.CreateOrUpdateFN, hcluster *hyperv1.HostedCluster) error {
	controlPlaneNamespaceName := manifests.HostedControlPlaneNamespace(hcluster.Namespace, hcluster.Name).Name

	// Reconcile openshift-ingress Network Policy
	policy := networkpolicy.OpenshiftIngressNetworkPolicy(controlPlaneNamespaceName)
	if _, err := createOrUpdate(ctx, r.Client, policy, func() error {
		return reconcileOpenshiftIngressNetworkPolicy(policy)
	}); err != nil {
		return fmt.Errorf("failed to reconcile ingress network policy: %w", err)
	}

	// Reconcile same-namespace Network Policy
	policy = networkpolicy.SameNamespaceNetworkPolicy(controlPlaneNamespaceName)
	if _, err := createOrUpdate(ctx, r.Client, policy, func() error {
		return reconcileSameNamespaceNetworkPolicy(policy)
	}); err != nil {
		return fmt.Errorf("failed to reconcile same namespace network policy: %w", err)
	}

	// Reconcile KAS Network Policy
	policy = networkpolicy.KASNetworkPolicy(controlPlaneNamespaceName)
	if _, err := createOrUpdate(ctx, r.Client, policy, func() error {
		return reconcileKASNetworkPolicy(policy, hcluster)
	}); err != nil {
		return fmt.Errorf("failed to reconcile kube-apiserver network policy: %w", err)
	}

	// Reconcile openshift-monitoring Network Policy
	policy = networkpolicy.OpenshiftMonitoringNetworkPolicy(controlPlaneNamespaceName)
	if _, err := createOrUpdate(ctx, r.Client, policy, func() error {
		return reconcileOpenshiftMonitoringNetworkPolicy(policy, hcluster)
	}); err != nil {
		return fmt.Errorf("failed to reconcile monitoring network policy: %w", err)
	}

	// Reconcile private-router Network Policy
	if hcluster.Spec.Platform.Type == hyperv1.AWSPlatform {
		policy = networkpolicy.PrivateRouterNetworkPolicy(controlPlaneNamespaceName)
		if _, err := createOrUpdate(ctx, r.Client, policy, func() error {
			return reconcilePrivateRouterNetworkPolicy(policy, hcluster)
		}); err != nil {
			return fmt.Errorf("failed to reconcile private router network policy: %w", err)
		}
	}

	for _, svc := range hcluster.Spec.Services {
		switch svc.Service {
		case hyperv1.OAuthServer:
			if svc.ServicePublishingStrategy.Type == hyperv1.NodePort {
				// Reconcile nodeport-oauth Network Policy
				policy = networkpolicy.NodePortOauthNetworkPolicy(controlPlaneNamespaceName)
				if _, err := createOrUpdate(ctx, r.Client, policy, func() error {
					return reconcileNodePortOauthNetworkPolicy(policy, hcluster)
				}); err != nil {
					return fmt.Errorf("failed to reconcile oauth server nodeport network policy: %w", err)
				}
			}
		case hyperv1.Ignition:
			if svc.ServicePublishingStrategy.Type == hyperv1.NodePort {
				// Reconcile nodeport-ignition Network Policy
				policy = networkpolicy.NodePortIgnitionNetworkPolicy(controlPlaneNamespaceName)
				if _, err := createOrUpdate(ctx, r.Client, policy, func() error {
					return reconcileNodePortIgnitionNetworkPolicy(policy, hcluster)
				}); err != nil {
					return fmt.Errorf("failed to reconcile ignition nodeport network policy: %w", err)
				}
			}
		case hyperv1.Konnectivity:
			if svc.ServicePublishingStrategy.Type == hyperv1.NodePort {
				// Reconcile nodeport-konnectivity Network Policy
				policy = networkpolicy.NodePortKonnectivityNetworkPolicy(controlPlaneNamespaceName)
				if _, err := createOrUpdate(ctx, r.Client, policy, func() error {
					return reconcileNodePortKonnectivityNetworkPolicy(policy, hcluster)
				}); err != nil {
					return fmt.Errorf("failed to reconcile konnectivity nodeport network policy: %w", err)
				}
			}
		}
	}

	return nil
}

func (r *HostedClusterReconciler) validateConfigAndClusterCapabilities(ctx context.Context, hc *hyperv1.HostedCluster) error {
	var errs []error
	for _, svc := range hc.Spec.Services {
		if svc.Type == hyperv1.Route && !r.ManagementClusterCapabilities.Has(capabilities.CapabilityRoute) {
			errs = append(errs, fmt.Errorf("cluster does not support Routes, but service %q is exposed via a Route", svc.Service))
		}
	}

	if err := r.validateServiceAccountSigningKey(ctx, hc); err != nil {
		errs = append(errs, fmt.Errorf("invalid service account signing key: %w", err))
	}

	if err := r.validateAzureConfig(ctx, hc); err != nil {
		errs = append(errs, err)
	}

	if err := r.validateAgentConfig(ctx, hc); err != nil {
		errs = append(errs, err)
	}

	if err := validateClusterID(hc); err != nil {
		errs = append(errs, err)
	}

	// TODO: Drop when we no longer need to support versions < 4.11
	if hc.Spec.Configuration != nil {
		_, err := globalconfig.ParseGlobalConfig(ctx, hc.Spec.Configuration)
		if err != nil {
			errs = append(errs, fmt.Errorf("failed to parse cluster configuration: %w", err))
		}
	}

	return utilerrors.NewAggregate(errs)
}

func (r *HostedClusterReconciler) validateReleaseImage(ctx context.Context, hc *hyperv1.HostedCluster) error {
	var pullSecret corev1.Secret
	if err := r.Client.Get(ctx, types.NamespacedName{Namespace: hc.Namespace, Name: hc.Spec.PullSecret.Name}, &pullSecret); err != nil {
		return fmt.Errorf("failed to get pull secret: %w", err)
	}
	pullSecretBytes, ok := pullSecret.Data[corev1.DockerConfigJsonKey]
	if !ok {
		return fmt.Errorf("expected %s key in pull secret", corev1.DockerConfigJsonKey)
	}

	releaseInfo, err := r.ReleaseProvider.Lookup(ctx, hc.Spec.Release.Image, pullSecretBytes)
	if err != nil {
		return fmt.Errorf("failed to lookup release image: %w", err)
	}
	version, err := semver.Parse(releaseInfo.Version())
	if err != nil {
		return err
	}

	var currentVersion *semver.Version
	if hc.Status.Version != nil && hc.Status.Version.Desired.Image != hc.Spec.Release.Image {
		releaseInfo, err := r.ReleaseProvider.Lookup(ctx, hc.Status.Version.Desired.Image, pullSecretBytes)
		if err != nil {
			return fmt.Errorf("failed to lookup release image: %w", err)
		}
		version, err := semver.Parse(releaseInfo.Version())
		if err != nil {
			return err
		}
		currentVersion = &version
	}
	minSupportedVersion := supportedversion.MinSupportedVersion
	if hc.Spec.Platform.Type == hyperv1.IBMCloudPlatform {
		//IBM Cloud is allowed to manage 4.9 clusters
		minSupportedVersion = semver.MustParse("4.9.0")
	}

	return isValidReleaseVersion(&version, currentVersion, &supportedversion.LatestSupportedVersion, &minSupportedVersion, hc.Spec.Networking.NetworkType, hc.Spec.Platform.Type)
}

func isProgressing(ctx context.Context, hc *hyperv1.HostedCluster) (bool, error) {
	for _, condition := range hc.Status.Conditions {
		switch string(condition.Type) {
		case string(hyperv1.SupportedHostedCluster), string(hyperv1.ValidHostedClusterConfiguration), string(hyperv1.ValidReleaseImage), string(hyperv1.ReconciliationActive):
			if condition.Status == metav1.ConditionFalse {
				return false, fmt.Errorf("%s condition is false", string(condition.Type))
			}
		case string(hyperv1.ClusterVersionUpgradeable):
			_, _, err := isUpgradeable(hc)
			if err != nil {
				return false, fmt.Errorf("ClusterVersionUpgradeable condition is false: %w", err)
			}
		}
	}

	if hc.Status.Version == nil || hc.Spec.Release.Image != hc.Status.Version.Desired.Image {
		// cluster is doing initial rollout or upgrading
		return true, nil
	}

	// cluster is conditions are good and is at desired release
	return false, nil
}

func isValidReleaseVersion(version, currentVersion, latestVersionSupported, minSupportedVersion *semver.Version, networkType hyperv1.NetworkType, platformType hyperv1.PlatformType) error {
	if version.LT(semver.MustParse("4.8.0")) {
		return fmt.Errorf("releases before 4.8 are not supported")
	}

	if currentVersion != nil && currentVersion.Minor > version.Minor {
		return fmt.Errorf("y-stream downgrade is not supported")
	}

	if networkType == hyperv1.OpenShiftSDN && currentVersion != nil && currentVersion.Minor < version.Minor {
		return fmt.Errorf("y-stream upgrade is not for OpenShiftSDN")
	}

	versionMinorOnly := &semver.Version{Major: version.Major, Minor: version.Minor}
	if networkType == hyperv1.OpenShiftSDN && currentVersion == nil && versionMinorOnly.GT(semver.MustParse("4.10.0")) && platformType != hyperv1.PowerVSPlatform {
		return fmt.Errorf("cannot use OpenShiftSDN with OCP version > 4.10")
	}

	if networkType == hyperv1.OVNKubernetes && currentVersion == nil && versionMinorOnly.LTE(semver.MustParse("4.10.0")) {
		return fmt.Errorf("cannot use OVNKubernetes with OCP version < 4.11")
	}

	if (version.Major == latestVersionSupported.Major && version.Minor > latestVersionSupported.Minor) || version.Major > latestVersionSupported.Major {
		return fmt.Errorf("the latest HostedCluster version supported by this Operator is: %q. Attempting to use: %q", supportedversion.LatestSupportedVersion, version)
	}

	if (version.Major == minSupportedVersion.Major && version.Minor < minSupportedVersion.Minor) || version.Major < minSupportedVersion.Major {
		return fmt.Errorf("the minimum HostedCluster version supported by this Operator is: %q. Attempting to use: %q", supportedversion.MinSupportedVersion, version)
	}

	return nil
}

func (r *HostedClusterReconciler) validateAzureConfig(ctx context.Context, hc *hyperv1.HostedCluster) error {
	if hc.Spec.Platform.Type != hyperv1.AzurePlatform {
		return nil
	}

	if hc.Spec.Platform.Azure == nil {
		return errors.New("azurecluster needs .spec.platform.azure to be filled")
	}

	credentialsSecret := &corev1.Secret{ObjectMeta: metav1.ObjectMeta{
		Namespace: hc.Namespace,
		Name:      hc.Spec.Platform.Azure.Credentials.Name,
	}}
	if err := r.Get(ctx, client.ObjectKeyFromObject(credentialsSecret), credentialsSecret); err != nil {
		return fmt.Errorf("failed to get credentials secret for cluster: %w", err)
	}

	var errs []error
	for _, expectedKey := range []string{"AZURE_CLIENT_ID", "AZURE_CLIENT_SECRET", "AZURE_SUBSCRIPTION_ID", "AZURE_TENANT_ID"} {
		if _, found := credentialsSecret.Data[expectedKey]; !found {
			errs = append(errs, fmt.Errorf("credentials secret for cluster doesn't have required key %s", expectedKey))
		}
	}

	return utilerrors.NewAggregate(errs)
}

func (r *HostedClusterReconciler) validateAgentConfig(ctx context.Context, hc *hyperv1.HostedCluster) error {
	if hc.Spec.Platform.Type != hyperv1.AgentPlatform {
		return nil
	}

	if hc.Spec.Platform.Agent == nil {
		return errors.New("agentcluster needs .spec.platform.agent to be filled")
	}

	// Validate that the agent namespace exists
	agentNamespace := &corev1.Namespace{
		ObjectMeta: metav1.ObjectMeta{
			Name: hc.Spec.Platform.Agent.AgentNamespace,
		},
	}

	if err := r.Get(ctx, client.ObjectKeyFromObject(agentNamespace), agentNamespace); err != nil {
		return fmt.Errorf("failed to get agent namespace: %w", err)
	}

	return nil
}

func (r *HostedClusterReconciler) validateHostedClusterSupport(hc *hyperv1.HostedCluster) error {
	switch hc.Spec.Platform.Type {
	case hyperv1.AWSPlatform:
		if hc.Spec.Platform.AWS == nil {
			return nil
		}
		if hc.Spec.Platform.AWS.EndpointAccess == hyperv1.Public {
			return nil
		}
		region := os.Getenv("AWS_REGION")
		if region == "" {
			return fmt.Errorf("AWS_REGION environment variable is not set for the operator")
		}
		credFile := os.Getenv("AWS_SHARED_CREDENTIALS_FILE")
		if credFile == "" {
			return fmt.Errorf("AWS_SHARED_CREDENTIALS_FILE environment variable is not set for the operator")
		}
		if hc.Spec.Platform.AWS.Region != region {
			return fmt.Errorf("operator only supports private clusters in region %s", region)
		}
	}
	return nil
}

type ClusterMachineApproverConfig struct {
	NodeClientCert NodeClientCert `json:"nodeClientCert,omitempty"`
}
type NodeClientCert struct {
	Disabled bool `json:"disabled,omitempty"`
}

func reconcileOpenshiftIngressNetworkPolicy(policy *networkingv1.NetworkPolicy) error {
	policy.Spec.Ingress = []networkingv1.NetworkPolicyIngressRule{
		{
			From: []networkingv1.NetworkPolicyPeer{
				{
					NamespaceSelector: &metav1.LabelSelector{
						MatchLabels: map[string]string{
							"network.openshift.io/policy-group": "ingress",
						},
					},
				},
			},
		},
	}
	policy.Spec.PodSelector = metav1.LabelSelector{}
	policy.Spec.PolicyTypes = []networkingv1.PolicyType{networkingv1.PolicyTypeIngress}
	return nil
}

func reconcileSameNamespaceNetworkPolicy(policy *networkingv1.NetworkPolicy) error {
	policy.Spec.Ingress = []networkingv1.NetworkPolicyIngressRule{
		{
			From: []networkingv1.NetworkPolicyPeer{
				{
					PodSelector: &metav1.LabelSelector{},
				},
			},
		},
	}
	policy.Spec.PodSelector = metav1.LabelSelector{}
	policy.Spec.PolicyTypes = []networkingv1.PolicyType{networkingv1.PolicyTypeIngress}
	return nil
}

func reconcileKASNetworkPolicy(policy *networkingv1.NetworkPolicy, hcluster *hyperv1.HostedCluster) error {
	port := intstr.FromInt(kas.APIServerListenPort)
	protocol := corev1.ProtocolTCP
	policy.Spec.Ingress = []networkingv1.NetworkPolicyIngressRule{
		{
			From: []networkingv1.NetworkPolicyPeer{},
			Ports: []networkingv1.NetworkPolicyPort{
				{
					Port:     &port,
					Protocol: &protocol,
				},
			},
		},
	}

	// We have to keep this in order to support 4.11 clusters where the KAS listen port == the external port
	if hcluster.Spec.Networking.APIServer != nil && hcluster.Spec.Networking.APIServer.Port != nil {
		externalPort := intstr.FromInt(int(*hcluster.Spec.Networking.APIServer.Port))
		policy.Spec.Ingress = append(policy.Spec.Ingress, networkingv1.NetworkPolicyIngressRule{
			From: []networkingv1.NetworkPolicyPeer{},
			Ports: []networkingv1.NetworkPolicyPort{
				{
					Port:     &externalPort,
					Protocol: &protocol,
				},
			},
		})
	}

	policy.Spec.PodSelector = metav1.LabelSelector{
		MatchLabels: map[string]string{
			"app": "kube-apiserver",
		},
	}
	policy.Spec.PolicyTypes = []networkingv1.PolicyType{networkingv1.PolicyTypeIngress}
	return nil
}

func reconcilePrivateRouterNetworkPolicy(policy *networkingv1.NetworkPolicy, hcluster *hyperv1.HostedCluster) error {
	httpPort := intstr.FromInt(8080)
	httpsPort := intstr.FromInt(8443)
	protocol := corev1.ProtocolTCP
	policy.Spec.Ingress = []networkingv1.NetworkPolicyIngressRule{
		{
			From: []networkingv1.NetworkPolicyPeer{},
			Ports: []networkingv1.NetworkPolicyPort{
				{
					Port:     &httpPort,
					Protocol: &protocol,
				},
				{
					Port:     &httpsPort,
					Protocol: &protocol,
				},
			},
		},
	}
	policy.Spec.PodSelector = metav1.LabelSelector{
		MatchLabels: map[string]string{
			"app": "private-router",
		},
	}
	policy.Spec.PolicyTypes = []networkingv1.PolicyType{networkingv1.PolicyTypeIngress}
	return nil
}

func reconcileNodePortOauthNetworkPolicy(policy *networkingv1.NetworkPolicy, hcluster *hyperv1.HostedCluster) error {
	port := intstr.FromInt(6443)
	protocol := corev1.ProtocolTCP
	policy.Spec.Ingress = []networkingv1.NetworkPolicyIngressRule{
		{
			From: []networkingv1.NetworkPolicyPeer{},
			Ports: []networkingv1.NetworkPolicyPort{
				{
					Port:     &port,
					Protocol: &protocol,
				},
			},
		},
	}
	policy.Spec.PodSelector = metav1.LabelSelector{
		MatchLabels: map[string]string{
			"app": "oauth-openshift",
		},
	}
	policy.Spec.PolicyTypes = []networkingv1.PolicyType{networkingv1.PolicyTypeIngress}
	return nil
}

func reconcileNodePortIgnitionNetworkPolicy(policy *networkingv1.NetworkPolicy, hcluster *hyperv1.HostedCluster) error {
	port := intstr.FromInt(9090)
	protocol := corev1.ProtocolTCP
	policy.Spec.Ingress = []networkingv1.NetworkPolicyIngressRule{
		{
			From: []networkingv1.NetworkPolicyPeer{},
			Ports: []networkingv1.NetworkPolicyPort{
				{
					Port:     &port,
					Protocol: &protocol,
				},
			},
		},
	}
	policy.Spec.PodSelector = metav1.LabelSelector{
		MatchLabels: map[string]string{
			"app": "ignition-server",
		},
	}
	policy.Spec.PolicyTypes = []networkingv1.PolicyType{networkingv1.PolicyTypeIngress}
	return nil
}

func reconcileNodePortKonnectivityNetworkPolicy(policy *networkingv1.NetworkPolicy, hcluster *hyperv1.HostedCluster) error {
	port := intstr.FromInt(8091)
	protocol := corev1.ProtocolTCP
	policy.Spec.Ingress = []networkingv1.NetworkPolicyIngressRule{
		{
			From: []networkingv1.NetworkPolicyPeer{},
			Ports: []networkingv1.NetworkPolicyPort{
				{
					Port:     &port,
					Protocol: &protocol,
				},
			},
		},
	}
	policy.Spec.PodSelector = metav1.LabelSelector{
		MatchLabels: map[string]string{
			"app": "konnectivity-server",
		},
	}
	policy.Spec.PolicyTypes = []networkingv1.PolicyType{networkingv1.PolicyTypeIngress}
	return nil
}

func reconcileOpenshiftMonitoringNetworkPolicy(policy *networkingv1.NetworkPolicy, hcluster *hyperv1.HostedCluster) error {
	policy.Spec.Ingress = []networkingv1.NetworkPolicyIngressRule{
		{
			From: []networkingv1.NetworkPolicyPeer{
				{
					NamespaceSelector: &metav1.LabelSelector{
						MatchLabels: map[string]string{
							"network.openshift.io/policy-group": "monitoring",
						},
					},
				},
			},
		},
	}
	policy.Spec.PodSelector = metav1.LabelSelector{}
	policy.Spec.PolicyTypes = []networkingv1.PolicyType{networkingv1.PolicyTypeIngress}
	return nil
}

const (
	oidcDocumentsFinalizer         = "hypershift.io/aws-oidc-discovery"
	serviceAccountSigningKeySecret = "sa-signing-key"
	serviceSignerPublicKey         = "service-account.pub"
	jwksURI                        = "/openid/v1/jwks"
	discoveryTemplate              = `{
	"issuer": "%s",
	"jwks_uri": "%s%s",
	"response_types_supported": [
		"id_token"
	],
	"subject_types_supported": [
		"public"
	],
	"id_token_signing_alg_values_supported": [
		"RS256"
	]
}`
)

type oidcGeneratorParams struct {
	issuerURL string
	pubKey    []byte
}

type oidcDocumentGeneratorFunc func(params oidcGeneratorParams) (io.ReadSeeker, error)

func generateConfigurationDocument(params oidcGeneratorParams) (io.ReadSeeker, error) {
	return strings.NewReader(fmt.Sprintf(discoveryTemplate, params.issuerURL, params.issuerURL, jwksURI)), nil
}

type KeyResponse struct {
	Keys []jose.JSONWebKey `json:"keys"`
}

func generateJWKSDocument(params oidcGeneratorParams) (io.ReadSeeker, error) {
	block, _ := pem.Decode(params.pubKey)
	if block == nil || block.Type != "RSA PUBLIC KEY" {
		return nil, fmt.Errorf("failed to decode PEM block containing RSA public key")
	}
	pubKey, err := x509.ParsePKIXPublicKey(block.Bytes)
	if err != nil {
		return nil, fmt.Errorf("failed to parse public key: %w", err)
	}
	rsaPubKey, ok := pubKey.(*rsa.PublicKey)
	if !ok {
		return nil, fmt.Errorf("public key is not RSA")
	}

	hasher := crypto.SHA256.New()
	hasher.Write(block.Bytes)
	hash := hasher.Sum(nil)
	kid := base64.RawURLEncoding.EncodeToString(hash)

	var keys []jose.JSONWebKey
	keys = append(keys, jose.JSONWebKey{
		Key:       rsaPubKey,
		KeyID:     kid,
		Algorithm: string(jose.RS256),
		Use:       "sig",
	})

	jwks, err := json.MarshalIndent(KeyResponse{Keys: keys}, "", "  ")
	if err != nil {
		return nil, err
	}

	return bytes.NewReader(jwks), nil
}

func oidcDocumentGenerators() map[string]oidcDocumentGeneratorFunc {
	return map[string]oidcDocumentGeneratorFunc{
		"/.well-known/openid-configuration": generateConfigurationDocument,
		jwksURI:                             generateJWKSDocument,
	}
}

func (r *HostedClusterReconciler) reconcileAWSOIDCDocuments(ctx context.Context, log logr.Logger, hcluster *hyperv1.HostedCluster, hcp *hyperv1.HostedControlPlane) error {
	if hcp.Status.KubeConfig == nil {
		return nil
	}

	// Skip creating documents if a service account signer key was set. Although technically it is possible for us
	// to use a specified service account signing key and still create discovery documents, the presence of the
	// signing key is what will indicate that the documents are generated and stored elsewhere.
	if hcluster.Spec.ServiceAccountSigningKey != nil && hcluster.Spec.ServiceAccountSigningKey.Name != "" {
		return nil
	}

	// We use the presence of the finalizer to short-circuit the document upload to avoid
	// constantly re-uploading it.
	if controllerutil.ContainsFinalizer(hcluster, oidcDocumentsFinalizer) {
		return nil
	}

	if r.OIDCStorageProviderS3BucketName == "" || r.S3Client == nil {
		return errors.New("hypershift wasn't configured with a S3 bucket or credentials, this makes it unable to set up OIDC for AWS clusters. Please install hypershift with the --aws-oidc-bucket-name, --aws-oidc-bucket-region and --aws-oidc-bucket-creds-file flags set. The bucket must pre-exist and the credentials must be authorized to write into it")
	}

	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: hcp.Namespace,
			Name:      serviceAccountSigningKeySecret,
		},
	}
	if err := r.Client.Get(ctx, client.ObjectKeyFromObject(secret), secret); err != nil {
		return fmt.Errorf("failed to get controlplane service account signing key %q: %w", client.ObjectKeyFromObject(secret), err)
	}

	if !sets.StringKeySet(secret.Data).HasAll(serviceSignerPublicKey) {
		return fmt.Errorf("controlplane service account signing key secret %q missing required key %s", client.ObjectKeyFromObject(secret), serviceSignerPublicKey)
	}

	params := oidcGeneratorParams{
		issuerURL: hcp.Spec.IssuerURL,
		pubKey:    secret.Data[serviceSignerPublicKey],
	}

	for path, generator := range oidcDocumentGenerators() {
		bodyReader, err := generator(params)
		if err != nil {
			return fmt.Errorf("failed to generate OIDC document %s: %w", path, err)
		}
		_, err = r.S3Client.PutObject(&s3.PutObjectInput{
			ACL:    aws.String("public-read"),
			Body:   bodyReader,
			Bucket: aws.String(r.OIDCStorageProviderS3BucketName),
			Key:    aws.String(hcluster.Spec.InfraID + path),
		})
		if err != nil {
			wrapped := fmt.Errorf("failed to upload %s to the %s s3 bucket", path, r.OIDCStorageProviderS3BucketName)
			if awsErr := awserr.Error(nil); errors.As(err, &awsErr) {
				switch awsErr.Code() {
				case s3.ErrCodeNoSuchBucket:
					wrapped = fmt.Errorf("%w: %s: this could be a misconfiguration of the hypershift operator; check the --aws-oidc-bucket-name flag", wrapped, awsErr.Code())
				default:
					// Generally, the underlying message from AWS has unique per-request
					// info not suitable for publishing as condition messages, so just
					// return the code. If other specific error types can be handled, add
					// new switch cases and try to provide more actionable info to the
					// user.
					wrapped = fmt.Errorf("%w: aws returned an error: %s", wrapped, awsErr.Code())
				}
			}
			return wrapped
		}
	}

	hcluster.Finalizers = append(hcluster.Finalizers, oidcDocumentsFinalizer)
	if err := r.Client.Update(ctx, hcluster); err != nil {
		return fmt.Errorf("failed to update the hosted cluster after adding the %s finalizer: %w", oidcDocumentsFinalizer, err)
	}

	log.Info("Successfully uploaded the OIDC documents to the S3 bucket")

	return nil
}

func (r *HostedClusterReconciler) cleanupOIDCBucketData(ctx context.Context, log logr.Logger, hcluster *hyperv1.HostedCluster) error {
	if !controllerutil.ContainsFinalizer(hcluster, oidcDocumentsFinalizer) {
		return nil
	}

	if r.OIDCStorageProviderS3BucketName == "" || r.S3Client == nil {
		return fmt.Errorf("hypershift wasn't configured with AWS credentials and a bucket, can not clean up OIDC documents from bucket. Please either set those up or clean up manually and then remove the %s finalizer from the hosted cluster", oidcDocumentsFinalizer)
	}

	var objectsToDelete []*s3.ObjectIdentifier
	for path := range oidcDocumentGenerators() {
		objectsToDelete = append(objectsToDelete, &s3.ObjectIdentifier{
			Key: aws.String(hcluster.Spec.InfraID + path),
		})
	}

	if _, err := r.S3Client.DeleteObjects(&s3.DeleteObjectsInput{
		Bucket: aws.String(r.OIDCStorageProviderS3BucketName),
		Delete: &s3.Delete{Objects: objectsToDelete},
	}); err != nil {
		return fmt.Errorf("failed to delete OIDC objects from %s S3 bucket: %w", r.OIDCStorageProviderS3BucketName, err)
	}

	controllerutil.RemoveFinalizer(hcluster, oidcDocumentsFinalizer)
	if err := r.Client.Update(ctx, hcluster); err != nil {
		return fmt.Errorf("failed to update hostedcluster after removing %s finalizer: %w", oidcDocumentsFinalizer, err)
	}

	log.Info("Successfully deleted the OIDC documents from the S3 bucket")
	return nil
}

func (r *HostedClusterReconciler) reconcileAWSResourceTags(ctx context.Context, hcluster *hyperv1.HostedCluster) error {
	if hcluster.Spec.Platform.AWS == nil {
		return nil
	}
	if hcluster.Spec.InfraID == "" {
		return nil
	}

	var existing *hyperv1.AWSResourceTag
	for idx, tag := range hcluster.Spec.Platform.AWS.ResourceTags {
		if tag.Key == "kubernetes.io/cluster/"+hcluster.Spec.InfraID {
			existing = &hcluster.Spec.Platform.AWS.ResourceTags[idx]
			break
		}
	}
	if existing != nil && existing.Value == "owned" {
		return nil
	}

	if existing != nil {
		existing.Value = "owned"
	} else {
		hcluster.Spec.Platform.AWS.ResourceTags = append(hcluster.Spec.Platform.AWS.ResourceTags, hyperv1.AWSResourceTag{
			Key:   "kubernetes.io/cluster/" + hcluster.Spec.InfraID,
			Value: "owned",
		})
	}

	if err := r.Client.Update(ctx, hcluster); err != nil {
		return fmt.Errorf("failed to update AWS resource tags: %w", err)
	}

	return nil
}

func (r *HostedClusterReconciler) reconcileAWSSubnets(ctx context.Context, createOrUpdate upsert.CreateOrUpdateFN,
	infraCR client.Object, namespace, clusterName, hcpNamespace string) error {

	nodePools, err := listNodePools(ctx, r.Client, namespace, clusterName)
	if err != nil {
		return fmt.Errorf("failed to get nodePools by cluster name for cluster %q: %w", clusterName, err)
	}
	subnetIDs := []string{}
	for _, nodePool := range nodePools {
		if nodePool.Spec.Platform.AWS != nil &&
			nodePool.Spec.Platform.AWS.Subnet != nil &&
			nodePool.Spec.Platform.AWS.Subnet.ID != nil {
			subnetIDs = append(subnetIDs, *nodePool.Spec.Platform.AWS.Subnet.ID)
		}
	}
	// Sort for stable update detection (is this needed?)
	sort.Strings(subnetIDs)

	// Reconcile subnet IDs in AWSCluster
	// TODO (alberto): drop this once this is fixed upstream https://github.com/kubernetes-sigs/cluster-api-provider-aws/pull/2864.
	awsInfraCR, ok := infraCR.(*capiawsv1.AWSCluster)
	if !ok {
		return nil
	}
	subnets := capiawsv1.Subnets{}
	for _, subnetID := range subnetIDs {
		subnets = append(subnets, capiawsv1.SubnetSpec{ID: subnetID})
	}
	_, err = createOrUpdate(ctx, r.Client, awsInfraCR, func() error {
		awsInfraCR.Spec.NetworkSpec.Subnets = subnets
		return nil
	})
	if err != nil {
		return fmt.Errorf("failed to reconcile networks for CAPA Infra CR: %w", err)
	}
	return nil
}

func isUpgradeable(hcluster *hyperv1.HostedCluster) (bool, string, error) {
	if hcluster.Status.Version != nil && hcluster.Status.Version.Desired.Image != hcluster.Spec.Release.Image {
		upgradeable := meta.FindStatusCondition(hcluster.Status.Conditions, string(hyperv1.ClusterVersionUpgradeable))
		if upgradeable != nil && upgradeable.Status == metav1.ConditionFalse {
			releaseImage, exists := hcluster.Annotations[hyperv1.ForceUpgradeToAnnotation]
			if !exists {
				return true, "", fmt.Errorf("cluster version is not upgradeable")
			} else if releaseImage != hcluster.Spec.Release.Image {
				return true, "", fmt.Errorf("cluster version is not upgradeable, force annotation is present but does not match desired release image")
			} else {
				return true, "cluster version is not upgradeable, upgrade is forced by annotation", nil
			}
		}
		return true, "", nil
	}
	return false, "", nil
}

// defaultAPIPortIfNeeded defaults the apiserver port on Azure management clusters as a workaround
// for https://bugzilla.redhat.com/show_bug.cgi?id=2060650: Azure LBs with port 6443 don't work
func (r *HostedClusterReconciler) defaultAPIPortIfNeeded(ctx context.Context, hcluster *hyperv1.HostedCluster) error {
	if hcluster.Spec.Networking.APIServer != nil && hcluster.Spec.Networking.APIServer.Port != nil {
		return nil
	}
	for _, publishingStrategy := range hcluster.Spec.Services {
		if publishingStrategy.Service != hyperv1.APIServer {
			continue
		}
		if publishingStrategy.Type == hyperv1.Route {
			if hcluster.Spec.Networking.APIServer == nil {
				hcluster.Spec.Networking.APIServer = &hyperv1.APIServerNetworking{}
			}

			hcluster.Spec.Networking.APIServer.Port = k8sutilspointer.Int32(443)
			if err := r.Update(ctx, hcluster); err != nil {
				return fmt.Errorf("failed to update hostedcluster after defaulting the apiserver port: %w", err)
			}
		}
		break
	}

	if !r.ManagementClusterCapabilities.Has(capabilities.CapabilityInfrastructure) {
		return nil
	}
	infra := &configv1.Infrastructure{ObjectMeta: metav1.ObjectMeta{Name: "cluster"}}
	if err := r.Client.Get(ctx, client.ObjectKeyFromObject(infra), infra); err != nil {
		return fmt.Errorf("failed to retrieve infra: %w", err)
	}

	if infra.Spec.PlatformSpec.Type != configv1.AzurePlatformType {
		return nil
	}
	if hcluster.Spec.Networking.APIServer == nil {
		hcluster.Spec.Networking.APIServer = &hyperv1.APIServerNetworking{}
	}

	hcluster.Spec.Networking.APIServer.Port = k8sutilspointer.Int32Ptr(7443)
	if err := r.Update(ctx, hcluster); err != nil {
		return fmt.Errorf("failed to update hostedcluster after defaulting the apiserver port: %w", err)
	}

	return nil
}

func (r *HostedClusterReconciler) defaultIngressDomain(ctx context.Context) (string, error) {
	if !r.ManagementClusterCapabilities.Has(capabilities.CapabilityIngress) {
		return "", nil
	}
	ingress := &configv1.Ingress{
		ObjectMeta: metav1.ObjectMeta{
			Name: "cluster",
		},
	}
	if err := r.Client.Get(ctx, client.ObjectKeyFromObject(ingress), ingress); err != nil {
		return "", fmt.Errorf("failed to retrieve ingress: %w", err)
	}
	return ingress.Spec.Domain, nil
}

func (r *HostedClusterReconciler) defaultClusterIDsIfNeeded(ctx context.Context, hcluster *hyperv1.HostedCluster) error {
	// Default the ClusterID if unset
	needsUpdate := false
	if hcluster.Spec.ClusterID == "" {
		hcluster.Spec.ClusterID = uuid.NewString()
		needsUpdate = true
	}

	// Default the infraID if unset
	if hcluster.Spec.InfraID == "" {
		hcluster.Spec.InfraID = infraid.New(hcluster.Name)
		needsUpdate = true
	}

	if needsUpdate {
		if err := r.Update(ctx, hcluster); err != nil {
			return fmt.Errorf("failed to update hostedcluster after defaulting IDs: %w", err)
		}
	}
	return nil
}

func validateClusterID(hc *hyperv1.HostedCluster) error {
	if len(hc.Spec.ClusterID) > 0 {
		_, err := uuid.Parse(hc.Spec.ClusterID)
		if err != nil {
			return fmt.Errorf("cannot parse cluster ID %q: %w", hc.Spec.ClusterID, err)
		}
	}
	return nil
}

// getReleaseImage get the releaseInfo releaseImage for a given HC release image reference.
func (r *HostedClusterReconciler) getReleaseImage(ctx context.Context, hc *hyperv1.HostedCluster) (*releaseinfo.ReleaseImage, error) {
	var pullSecret corev1.Secret
	if err := r.Client.Get(ctx, types.NamespacedName{Namespace: hc.Namespace, Name: hc.Spec.PullSecret.Name}, &pullSecret); err != nil {
		return nil, fmt.Errorf("failed to get pull secret: %w", err)
	}
	pullSecretBytes, ok := pullSecret.Data[corev1.DockerConfigJsonKey]
	if !ok {
		return nil, fmt.Errorf("expected %s key in pull secret", corev1.DockerConfigJsonKey)
	}
	return r.ReleaseProvider.Lookup(ctx, hc.Spec.Release.Image, pullSecretBytes)
}

// getPayloadImage get an image from the payload for a particular component.
func (r *HostedClusterReconciler) getPayloadImage(ctx context.Context, hc *hyperv1.HostedCluster, component string) (string, error) {
	releaseImage, err := r.getReleaseImage(ctx, hc)
	if err != nil {
		return "", fmt.Errorf("failed to lookup release image: %w", err)
	}

	image, exists := releaseImage.ComponentImages()[component]
	if !exists {
		return "", fmt.Errorf("image does not exist for release: %q", image)
	}
	return image, nil
}

func (r *HostedClusterReconciler) reconcileServiceAccountSigningKey(ctx context.Context, hc *hyperv1.HostedCluster, targetNamespace string, createOrUpdate upsert.CreateOrUpdateFN) error {
	privateBytes, publicBytes, err := r.serviceAccountSigningKeyBytes(ctx, hc)
	if err != nil {
		return err
	}
	cpSigningKeySecret := controlplaneoperator.ServiceAccountSigningKeySecret(targetNamespace)
	_, err = createOrUpdate(ctx, r.Client, cpSigningKeySecret, func() error {
		// Only set the signing key when the key does not already exist
		if _, hasKey := cpSigningKeySecret.Data[controlplaneoperator.ServiceSignerPrivateKey]; hasKey {
			return nil
		}
		if cpSigningKeySecret.Data == nil {
			cpSigningKeySecret.Data = map[string][]byte{}
		}
		cpSigningKeySecret.Data[controlplaneoperator.ServiceSignerPrivateKey] = privateBytes
		cpSigningKeySecret.Data[controlplaneoperator.ServiceSignerPublicKey] = publicBytes
		return nil
	})
	return err
}

func (r *HostedClusterReconciler) validateServiceAccountSigningKey(ctx context.Context, hc *hyperv1.HostedCluster) error {
	// Skip if service account signing key is not set
	if hc.Spec.ServiceAccountSigningKey == nil || hc.Spec.ServiceAccountSigningKey.Name == "" {
		return nil
	}
	if hc.Spec.IssuerURL == "" {
		return fmt.Errorf("the IssuerURL must be set when specifying a service account signing key")
	}

	privateBytes, _, err := r.serviceAccountSigningKeyBytes(ctx, hc)
	if err != nil {
		return err
	}
	controlPlaneNamespace := manifests.HostedControlPlaneNamespace(hc.Namespace, hc.Name).Name
	cpSigningKeySecret := controlplaneoperator.ServiceAccountSigningKeySecret(controlPlaneNamespace)
	if err := r.Client.Get(ctx, client.ObjectKeyFromObject(cpSigningKeySecret), cpSigningKeySecret); err != nil {
		if !apierrors.IsNotFound(err) {
			return fmt.Errorf("cannot get control plane signing key secret %s/%s: %w", cpSigningKeySecret.Namespace, cpSigningKeySecret.Name, err)
		}
		return nil
	}
	if cpSigningKeySecret.Data != nil {
		existingKeyBytes, hasKey := cpSigningKeySecret.Data[controlplaneoperator.ServiceSignerPrivateKey]
		if !hasKey {
			return nil
		}
		if !bytes.Equal(existingKeyBytes, privateBytes) {
			return fmt.Errorf("existing control plane service account signing key does not match private key")
		}
	}
	return nil
}

func (r *HostedClusterReconciler) serviceAccountSigningKeyBytes(ctx context.Context, hc *hyperv1.HostedCluster) ([]byte, []byte, error) {
	signingKeySecret := &corev1.Secret{}
	if err := r.Client.Get(ctx, client.ObjectKey{Namespace: hc.Namespace, Name: hc.Spec.ServiceAccountSigningKey.Name}, signingKeySecret); err != nil {
		return nil, nil, fmt.Errorf("failed to get hostedcluster ServiceAccountSigningKey secret %s: %w", hc.Spec.ServiceAccountSigningKey.Name, err)
	}
	privateKeyPEMBytes, hasKey := signingKeySecret.Data[hyperv1.ServiceAccountSigningKeySecretKey]
	if !hasKey {
		return nil, nil, fmt.Errorf("cannot find service account key %q in secret %s", hyperv1.ServiceAccountSigningKeySecretKey, signingKeySecret.Name)
	}
	privateKey, err := certs.PemToPrivateKey(privateKeyPEMBytes)
	if err != nil {
		return nil, nil, fmt.Errorf("cannot decode private key in secret %s: %w", signingKeySecret.Name, err)
	}
	publicKeyPEMBytes, err := certs.PublicKeyToPem(&privateKey.PublicKey)
	if err != nil {
		return nil, nil, fmt.Errorf("cannot serialize public key from private key %s: %w", signingKeySecret.Name, err)
	}
	return privateKeyPEMBytes, publicKeyPEMBytes, nil
}

// reconcileDeprecatedGlobalConfig converts previously specified configuration in RawExtension format to
// the new configuration fields. It clears the previous, deprecated configuration.
// TODO: drop when we no longer need to support versions < 4.11
func (r *HostedClusterReconciler) reconcileDeprecatedGlobalConfig(ctx context.Context, hc *hyperv1.HostedCluster) error {

	// Skip if no deprecated configuration is set
	if hc.Spec.Configuration == nil || len(hc.Spec.Configuration.Items) == 0 {
		return nil
	}

	gconfig, err := globalconfig.ParseGlobalConfig(ctx, hc.Spec.Configuration)
	if err != nil {
		// This should never happen because at this point, the global configuration
		// should be valid
		return err
	}

	// Copy over config from the raw extension
	if gconfig.APIServer != nil {
		hc.Spec.Configuration.APIServer = &gconfig.APIServer.Spec
	}
	if gconfig.Authentication != nil {
		hc.Spec.Configuration.Authentication = &gconfig.Authentication.Spec
	}
	if gconfig.FeatureGate != nil {
		hc.Spec.Configuration.FeatureGate = &gconfig.FeatureGate.Spec
	}
	if gconfig.Image != nil {
		hc.Spec.Configuration.Image = &gconfig.Image.Spec
	}
	if gconfig.Ingress != nil {
		hc.Spec.Configuration.Ingress = &gconfig.Ingress.Spec
	}
	if gconfig.Network != nil {
		hc.Spec.Configuration.Network = &gconfig.Network.Spec
	}
	if gconfig.OAuth != nil {
		hc.Spec.Configuration.OAuth = &gconfig.OAuth.Spec
	}
	if gconfig.Scheduler != nil {
		hc.Spec.Configuration.Scheduler = &gconfig.Scheduler.Spec
	}
	if gconfig.Proxy != nil {
		hc.Spec.Configuration.Proxy = &gconfig.Proxy.Spec
	}

	return nil
}

// reconcileDeprecatedAWSRoles converts previously specified input in .aws.roles format to
// the new the rolesRef field. It clears the previous, deprecated configuration.
// TODO: drop when we no longer need to support versions < 4.12
func (r *HostedClusterReconciler) reconcileDeprecatedAWSRoles(ctx context.Context, hc *hyperv1.HostedCluster) error {
	// Migrate ARNs from slice into typed fields.
	log := ctrl.LoggerFrom(ctx)
	for _, v := range hc.Spec.Platform.AWS.Roles {
		switch v.Namespace {
		case "openshift-image-registry":
			hc.Spec.Platform.AWS.RolesRef.ImageRegistryARN = v.ARN
		case "openshift-ingress-operator":
			hc.Spec.Platform.AWS.RolesRef.IngressARN = v.ARN
		case "openshift-cloud-network-config-controller":
			hc.Spec.Platform.AWS.RolesRef.NetworkARN = v.ARN
		case "openshift-cluster-csi-drivers":
			hc.Spec.Platform.AWS.RolesRef.StorageARN = v.ARN
		default:
			log.Info("Invalid namespace for deprecated role", "namespace", v.Namespace)
		}
	}
	hc.Spec.Platform.AWS.Roles = nil

	// Migrate ARNs from secrets into typed fields.
	if hc.Spec.Platform.AWS.NodePoolManagementCreds.Name != "" {
		nodePoolManagementARN, err := r.getARNFromSecret(ctx, hc.Spec.Platform.AWS.NodePoolManagementCreds.Name, hc.Namespace)
		if err != nil {
			return fmt.Errorf("failed to get ARN from secret: %w", err)
		}
		hc.Spec.Platform.AWS.RolesRef.NodePoolManagementARN = nodePoolManagementARN
		hc.Spec.Platform.AWS.NodePoolManagementCreds = corev1.LocalObjectReference{}
	}

	if hc.Spec.Platform.AWS.ControlPlaneOperatorCreds.Name != "" {
		controlPlaneOperatorARN, err := r.getARNFromSecret(ctx, hc.Spec.Platform.AWS.ControlPlaneOperatorCreds.Name, hc.Namespace)
		if err != nil {
			return fmt.Errorf("failed to get ARN from secret: %w", err)
		}
		hc.Spec.Platform.AWS.RolesRef.ControlPlaneOperatorARN = controlPlaneOperatorARN
		hc.Spec.Platform.AWS.ControlPlaneOperatorCreds = corev1.LocalObjectReference{}
	}

	if hc.Spec.Platform.AWS.KubeCloudControllerCreds.Name != "" {
		kubeCloudControllerARN, err := r.getARNFromSecret(ctx, hc.Spec.Platform.AWS.KubeCloudControllerCreds.Name, hc.Namespace)
		if err != nil {
			return fmt.Errorf("failed to get ARN from secret: %w", err)
		}
		hc.Spec.Platform.AWS.RolesRef.KubeCloudControllerARN = kubeCloudControllerARN
		hc.Spec.Platform.AWS.KubeCloudControllerCreds = corev1.LocalObjectReference{}
	}

	return nil
}

func (r *HostedClusterReconciler) reconcileDeprecatedNetworkSettings(hc *hyperv1.HostedCluster) error {
	if hc.Spec.Networking.MachineCIDR != "" {
		cidr, err := ipnet.ParseCIDR(hc.Spec.Networking.MachineCIDR)
		if err != nil {
			return fmt.Errorf("failed to parse machine CIDR: %w", err)
		}
		hc.Spec.Networking.MachineNetwork = []hyperv1.MachineNetworkEntry{
			{
				CIDR: *cidr,
			},
		}
		hc.Spec.Networking.MachineCIDR = ""
	}
	if hc.Spec.Networking.PodCIDR != "" {
		cidr, err := ipnet.ParseCIDR(hc.Spec.Networking.PodCIDR)
		if err != nil {
			return fmt.Errorf("failed to parse pod CIDR: %w", err)
		}
		hc.Spec.Networking.ClusterNetwork = []hyperv1.ClusterNetworkEntry{
			{
				CIDR: *cidr,
			},
		}
		hc.Spec.Networking.PodCIDR = ""
	}
	if hc.Spec.Networking.ServiceCIDR != "" {
		cidr, err := ipnet.ParseCIDR(hc.Spec.Networking.ServiceCIDR)
		if err != nil {
			return fmt.Errorf("failed to parse service CIDR: %w", err)
		}
		hc.Spec.Networking.ServiceNetwork = []hyperv1.ServiceNetworkEntry{
			{
				CIDR: *cidr,
			},
		}
		hc.Spec.Networking.ServiceCIDR = ""
	}
	return nil
}

func populateDeprecatedNetworkingFields(hcp *hyperv1.HostedControlPlane, hc *hyperv1.HostedCluster) {
	hcp.Spec.ServiceCIDR = hyperutil.FirstServiceCIDR(hc.Spec.Networking.ServiceNetwork)
	hcp.Spec.PodCIDR = hyperutil.FirstClusterCIDR(hc.Spec.Networking.ClusterNetwork)
	hcp.Spec.MachineCIDR = hyperutil.FirstMachineCIDR(hc.Spec.Networking.MachineNetwork)
	hcp.Spec.NetworkType = hc.Spec.Networking.NetworkType
	if hc.Spec.Networking.APIServer != nil {
		hcp.Spec.APIAdvertiseAddress = hc.Spec.Networking.APIServer.AdvertiseAddress
		hcp.Spec.APIPort = hc.Spec.Networking.APIServer.Port
		hcp.Spec.APIAllowedCIDRBlocks = hc.Spec.Networking.APIServer.AllowedCIDRBlocks
	} else {
		hcp.Spec.APIAdvertiseAddress = nil
		hcp.Spec.APIPort = nil
		hcp.Spec.APIAllowedCIDRBlocks = nil
	}
}

func (r *HostedClusterReconciler) getARNFromSecret(ctx context.Context, name, namespace string) (string, error) {
	creds := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: namespace,
		},
	}
	if err := r.Get(ctx, client.ObjectKeyFromObject(creds), creds); err != nil {
		return "", fmt.Errorf("failed to get secret: %w", err)
	}
	credContent, err := ini.Load(creds.Data["credentials"])
	if err != nil {
		return "", fmt.Errorf("cannot parse credentials: %w", err)
	}
	return credContent.Section("default").Key("role_arn").String(), nil
}

func configurationFieldsToRawExtensions(config *hyperv1.ClusterConfiguration) ([]runtime.RawExtension, error) {
	var result []runtime.RawExtension
	if config == nil {
		return result, nil
	}
	if config.APIServer != nil {
		result = append(result, runtime.RawExtension{
			Object: &configv1.APIServer{
				Spec: *config.APIServer,
			},
		})
	}
	if config.Authentication != nil {
		result = append(result, runtime.RawExtension{
			Object: &configv1.Authentication{
				Spec: *config.Authentication,
			},
		})
	}
	if config.FeatureGate != nil {
		result = append(result, runtime.RawExtension{
			Object: &configv1.FeatureGate{
				Spec: *config.FeatureGate,
			},
		})
	}
	if config.Image != nil {
		result = append(result, runtime.RawExtension{
			Object: &configv1.Image{
				Spec: *config.Image,
			},
		})
	}
	if config.Ingress != nil {
		result = append(result, runtime.RawExtension{
			Object: &configv1.Ingress{
				Spec: *config.Ingress,
			},
		})
	}
	if config.Network != nil {
		result = append(result, runtime.RawExtension{
			Object: &configv1.Network{
				Spec: *config.Network,
			},
		})
	}
	if config.OAuth != nil {
		result = append(result, runtime.RawExtension{
			Object: &configv1.OAuth{
				Spec: *config.OAuth,
			},
		})
	}
	if config.Scheduler != nil {
		result = append(result, runtime.RawExtension{
			Object: &configv1.Scheduler{
				Spec: *config.Scheduler,
			},
		})
	}
	if config.Proxy != nil {
		result = append(result, runtime.RawExtension{
			Object: &configv1.Proxy{
				Spec: *config.Proxy,
			},
		})
	}

	serializer := kjson.NewSerializerWithOptions(
		kjson.DefaultMetaFactory, api.Scheme, api.Scheme,
		kjson.SerializerOptions{Yaml: false, Pretty: false, Strict: true},
	)
	for idx := range result {
		gvk, err := apiutil.GVKForObject(result[idx].Object, api.Scheme)
		if err != nil {
			return nil, fmt.Errorf("failed to get gvk for %T: %w", result[idx].Object, err)
		}
		result[idx].Object.GetObjectKind().SetGroupVersionKind(gvk)

		// We do a DeepEqual in the upsert func, so we must match the deserialized version from
		// the server which has Raw set and Object unset.
		b := &bytes.Buffer{}
		if err := serializer.Encode(result[idx].Object, b); err != nil {
			return nil, fmt.Errorf("failed to marshal %+v: %w", result[idx].Object, err)
		}

		// Remove the status part of the serialized resource. We only have
		// spec to begin with and status causes incompatibilities with previous
		// versions of the CPO
		unstructuredObject := &unstructured.Unstructured{}
		if _, _, err := unstructured.UnstructuredJSONScheme.Decode(b.Bytes(), nil, unstructuredObject); err != nil {
			return nil, fmt.Errorf("failed to decode resource into unstructured: %w", err)
		}
		unstructured.RemoveNestedField(unstructuredObject.Object, "status")
		b = &bytes.Buffer{}
		if err := unstructured.UnstructuredJSONScheme.Encode(unstructuredObject, b); err != nil {
			return nil, fmt.Errorf("failed to serialize unstructured resource: %w", err)
		}

		result[idx].Raw = bytes.TrimSuffix(b.Bytes(), []byte("\n"))
		result[idx].Object = nil
	}

	return result, nil
}
