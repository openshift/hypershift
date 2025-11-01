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
	"crypto/rand"
	"crypto/tls"
	"errors"
	"fmt"
	"maps"
	"net"
	"net/netip"
	"os"
	"reflect"
	"sort"
	"strconv"
	"strings"
	"time"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	hyperkarpenterv1 "github.com/openshift/hypershift/api/karpenter/v1beta1"
	"github.com/openshift/hypershift/api/util/configrefs"
	"github.com/openshift/hypershift/cmd/util"
	"github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/imageprovider"
	cpomanifests "github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/manifests"
	capimanagerv2 "github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/v2/capi_manager"
	capiproviderv2 "github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/v2/capi_provider"
	cpov2 "github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/v2/controlplaneoperator"
	"github.com/openshift/hypershift/control-plane-pki-operator/certificates"
	"github.com/openshift/hypershift/hypershift-operator/controllers/hostedcluster/internal/platform"
	platformaws "github.com/openshift/hypershift/hypershift-operator/controllers/hostedcluster/internal/platform/aws"
	hcmetrics "github.com/openshift/hypershift/hypershift-operator/controllers/hostedcluster/metrics"
	validations "github.com/openshift/hypershift/hypershift-operator/controllers/hostedcluster/validations"
	"github.com/openshift/hypershift/hypershift-operator/controllers/manifests"
	"github.com/openshift/hypershift/hypershift-operator/controllers/manifests/clusterapi"
	"github.com/openshift/hypershift/hypershift-operator/controllers/manifests/controlplaneoperator"
	controlplanepkioperatormanifests "github.com/openshift/hypershift/hypershift-operator/controllers/manifests/controlplanepkioperator"
	etcdrecoverymanifests "github.com/openshift/hypershift/hypershift-operator/controllers/manifests/etcdrecovery"
	"github.com/openshift/hypershift/hypershift-operator/controllers/manifests/ignitionserver"
	kvinfra "github.com/openshift/hypershift/kubevirtexternalinfra"
	"github.com/openshift/hypershift/support/api"
	"github.com/openshift/hypershift/support/azureutil"
	"github.com/openshift/hypershift/support/capabilities"
	"github.com/openshift/hypershift/support/certs"
	"github.com/openshift/hypershift/support/config"
	controlplanecomponent "github.com/openshift/hypershift/support/controlplane-component"
	"github.com/openshift/hypershift/support/globalconfig"
	"github.com/openshift/hypershift/support/infraid"
	"github.com/openshift/hypershift/support/metrics"
	"github.com/openshift/hypershift/support/oidc"
	"github.com/openshift/hypershift/support/releaseinfo"
	"github.com/openshift/hypershift/support/secretproviderclass"
	"github.com/openshift/hypershift/support/supportedversion"
	"github.com/openshift/hypershift/support/upsert"
	hyperutil "github.com/openshift/hypershift/support/util"
	supportvalidations "github.com/openshift/hypershift/support/validations"

	configv1 "github.com/openshift/api/config/v1"
	routev1 "github.com/openshift/api/route/v1"

	"github.com/aws/aws-sdk-go/aws"
	"github.com/aws/aws-sdk-go/aws/awserr"
	"github.com/aws/aws-sdk-go/service/s3"
	"github.com/aws/aws-sdk-go/service/s3/s3iface"

	appsv1 "k8s.io/api/apps/v1"
	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	networkingv1 "k8s.io/api/networking/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	"k8s.io/apimachinery/pkg/api/equality"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/types"
	utilerrors "k8s.io/apimachinery/pkg/util/errors"
	utilrand "k8s.io/apimachinery/pkg/util/rand"
	"k8s.io/apimachinery/pkg/util/sets"
	"k8s.io/apimachinery/pkg/util/validation"
	"k8s.io/apimachinery/pkg/util/validation/field"
	"k8s.io/client-go/util/workqueue"
	"k8s.io/utils/clock"
	"k8s.io/utils/ptr"

	capiv1 "sigs.k8s.io/cluster-api/api/v1beta1"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/apiutil"
	"sigs.k8s.io/controller-runtime/pkg/controller"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	ctrllog "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	"github.com/blang/semver"
	"github.com/go-logr/logr"
	"github.com/google/uuid"
	prometheusoperatorv1 "github.com/prometheus-operator/prometheus-operator/pkg/apis/monitoring/v1"
	"gopkg.in/ini.v1"
)

const (
	HostedClusterFinalizer              = "hypershift.openshift.io/finalizer"
	clusterDeletionRequeueDuration      = 5 * time.Second
	ReportingGracePeriodRequeueDuration = 25 * time.Second

	ImageStreamCAPI            = "cluster-capi-controllers"
	ImageStreamAutoscalerImage = "cluster-autoscaler"

	controlPlaneOperatorSubcommandsLabel                 = "io.openshift.hypershift.control-plane-operator-subcommands"
	ignitionServerHealthzHandlerLabel                    = "io.openshift.hypershift.ignition-server-healthz-handler"
	controlPlaneOperatorSupportsKASCustomKubeconfigLabel = "io.openshift.hypershift.control-plane-operator-supports-kas-custom-kubeconfig"

	controlPlaneOperatorAppliesManagementKASNetworkPolicyLabel = "io.openshift.hypershift.control-plane-operator-applies-management-kas-network-policy-label"
	controlPlanePKIOperatorSignsCSRsLabel                      = "io.openshift.hypershift.control-plane-pki-operator-signs-csrs"
	useRestrictedPodSecurityLabel                              = "io.openshift.hypershift.restricted-psa"
	defaultToControlPlaneV2Label                               = "io.openshift.hypershift.control-plane-operator.v2-isdefault"

	etcdEncKeyPostfix    = "-etcd-encryption-key"
	managedServiceEnvVar = "MANAGED_SERVICE"

	jobHostedClusterNameLabel      = "hypershift.openshift.io/cluster-name"
	jobHostedClusterNamespaceLabel = "hypershift.openshift.io/cluster-namespace"

	etcdCheckRequeueInterval = 10 * time.Second

	awsEndpointDeletionGracePeriod = 10 * time.Minute

	previouslySyncedRestartDateAnnotation = "hypershift.openshift.io/previous-restart-date"
	kasServingCertHashAnnotation          = "hypershift.openshift.io/kas-serving-cert-hash"
	referencedResourceAnnotationPrefix    = "referenced-resource.hypershift.openshift.io/"
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

	RegistryOverrides map[string]string

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

	MetricsSet    metrics.MetricsSet
	SREConfigHash string

	OperatorNamespace string

	RegistryProvider globalconfig.RegistryProvider

	overwriteReconcile   func(ctx context.Context, req ctrl.Request, log logr.Logger, hcluster *hyperv1.HostedCluster) (ctrl.Result, error)
	now                  func() metav1.Time
	KubevirtInfraClients kvinfra.KubevirtInfraClientMap

	MonitoringDashboards bool

	CertRotationScale time.Duration

	EnableCVOManagementClusterMetricsAccess bool

	EnableEtcdRecovery bool

	FeatureSet configv1.FeatureSet

	OpenShiftTrustedCAFilePath string
}

// +kubebuilder:rbac:groups=hypershift.openshift.io,resources=hostedclusters,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=hypershift.openshift.io,resources=hostedclusters/status,verbs=get;update;patch

func (r *HostedClusterReconciler) SetupWithManager(mgr ctrl.Manager, createOrUpdate upsert.CreateOrUpdateProvider, metricsSet metrics.MetricsSet, operatorNamespace string) error {
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
	bldr := ctrl.NewControllerManagedBy(mgr).
		For(&hyperv1.HostedCluster{}, builder.WithPredicates(hyperutil.PredicatesForHostedClusterAnnotationScoping(mgr.GetClient()))).
		WithOptions(controller.Options{
			RateLimiter:             workqueue.NewTypedItemExponentialFailureRateLimiter[reconcile.Request](1*time.Second, 10*time.Second),
			MaxConcurrentReconciles: 10,
		})
	for _, managedResource := range r.managedResources() {
		bldr.Watches(managedResource, handler.EnqueueRequestsFromMapFunc(enqueueHostedClustersFunc(metricsSet, operatorNamespace, mgr.GetClient())), builder.WithPredicates(hyperutil.PredicatesForHostedClusterAnnotationScoping(mgr.GetClient())))
	}

	// Set based on SCC capability
	// When SCC is available (OpenShift), the container's security context and UID range is automatically set
	// When SCC is not available (Kubernetes), we want to explicitly set a default (non-root) security context
	r.SetDefaultSecurityContext = !r.ManagementClusterCapabilities.Has(capabilities.CapabilitySecurityContextConstraint)

	return bldr.Complete(r)
}

// managedResources are all the resources that are managed as childresources for a HostedCluster
func (r *HostedClusterReconciler) managedResources() []client.Object {
	managedResources := []client.Object{
		&hyperv1.HostedControlPlane{},
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
		&corev1.Endpoints{},
		&hyperv1.NodePool{},
	}

	// Watch based on platforms installed
	if platformsInstalled := os.Getenv("PLATFORMS_INSTALLED"); len(platformsInstalled) > 0 {
		managedResources = append(managedResources, hyperutil.GetHostedClusterManagedResources(platformsInstalled)...)
	} else {
		managedResources = append(managedResources, hyperutil.BaseResources...)
		managedResources = append(managedResources, hyperutil.AWSResources...)
		managedResources = append(managedResources, hyperutil.AzureResources...)
		managedResources = append(managedResources, hyperutil.IBMCloudResources...)
		managedResources = append(managedResources, hyperutil.KubevirtResources...)
		managedResources = append(managedResources, hyperutil.AgentResources...)
		managedResources = append(managedResources, hyperutil.OpenStackResources...)
	}

	// Only watch managed Azure resources if the HO is explicitly configured to do so. Otherwise, the HO will fail to
	// reconcile HostedClusters since some CRs are only installed in the managed Azure use case.
	if azureutil.IsAroHCP() {
		managedResources = append(managedResources, hyperutil.ManagedAzure...)
	}

	// Watch if etcd recovery is enabled
	if r.EnableEtcdRecovery {
		managedResources = append(managedResources, []client.Object{
			&appsv1.StatefulSet{},
			&batchv1.Job{},
		}...)
	}

	// Watch based on Routes capability
	if r.ManagementClusterCapabilities.Has(capabilities.CapabilityRoute) {
		managedResources = append(managedResources, &routev1.Route{})
	}

	// Watch based on Ingress capability
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
	// At the initial hosted cluster creation time, there is no HCP.
	if hcp == nil {
		return nil
	}

	err := c.Get(ctx, client.ObjectKeyFromObject(hcp), hcp)
	if err != nil {
		if !apierrors.IsNotFound(err) {
			return fmt.Errorf("failed to get hostedcontrolplane: %w", err)
		}
		return nil
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
		Message:            "Reconciliation completed successfully",
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
	controlPlaneNamespace := manifests.HostedControlPlaneNamespaceObject(hcluster.Namespace, hcluster.Name)
	hcp := controlplaneoperator.HostedControlPlane(controlPlaneNamespace.Name, hcluster.Name)
	err := r.Client.Get(ctx, client.ObjectKeyFromObject(hcp), hcp)
	if err != nil {
		if !apierrors.IsNotFound(err) {
			return ctrl.Result{}, fmt.Errorf("failed to get hostedcontrolplane: %w", err)
		} else {
			hcp = nil
		}
	}

	// Bubble up ValidIdentityProvider condition from the hostedControlPlane.
	// We set this condition even if the HC is being deleted. Otherwise, a hostedCluster with a conflicted identity provider
	// would fail to complete deletion forever with no clear signal for consumers.
	if hcluster.Spec.Platform.Type == hyperv1.AWSPlatform {
		freshCondition := &metav1.Condition{
			Type:               string(hyperv1.ValidAWSIdentityProvider),
			Status:             metav1.ConditionUnknown,
			Reason:             hyperv1.StatusUnknownReason,
			ObservedGeneration: hcluster.Generation,
		}
		if hcp != nil {
			validIdentityProviderCondition := meta.FindStatusCondition(hcp.Status.Conditions, string(hyperv1.ValidAWSIdentityProvider))
			if validIdentityProviderCondition != nil {
				freshCondition = validIdentityProviderCondition
			}
		}

		oldCondition := meta.FindStatusCondition(hcluster.Status.Conditions, string(hyperv1.ValidAWSIdentityProvider))

		// Preserve previous status if we can no longer determine the status (for example when the hostedcontrolplane has been deleted)
		if oldCondition != nil && freshCondition.Status == metav1.ConditionUnknown {
			freshCondition.Status = oldCondition.Status
		}
		if oldCondition == nil || oldCondition.Status != freshCondition.Status {
			freshCondition.ObservedGeneration = hcluster.Generation
			meta.SetStatusCondition(&hcluster.Status.Conditions, *freshCondition)
			// Persist status updates
			if err := r.Client.Status().Update(ctx, hcluster); err != nil {
				return ctrl.Result{}, fmt.Errorf("failed to update status: %w", err)
			}
		}
	}

	// Bubble up AWSDefaultSecurityGroupDeleted condition from the hostedControlPlane.
	// We set this condition even if the HC is being deleted, so we can report blocking objects on deletion.
	{
		if hcp != nil && hcp.DeletionTimestamp != nil {
			freshCondition := &metav1.Condition{
				Type:               string(hyperv1.AWSDefaultSecurityGroupDeleted),
				Status:             metav1.ConditionUnknown,
				Reason:             hyperv1.StatusUnknownReason,
				ObservedGeneration: hcluster.Generation,
			}

			securityGroupDeletionCondition := meta.FindStatusCondition(hcp.Status.Conditions, string(hyperv1.AWSDefaultSecurityGroupDeleted))
			if securityGroupDeletionCondition != nil {
				freshCondition = securityGroupDeletionCondition
			}

			oldCondition := meta.FindStatusCondition(hcluster.Status.Conditions, string(hyperv1.AWSDefaultSecurityGroupDeleted))
			if oldCondition == nil || oldCondition.Message != freshCondition.Message {
				freshCondition.ObservedGeneration = hcluster.Generation
				meta.SetStatusCondition(&hcluster.Status.Conditions, *freshCondition)
				// Persist status updates
				if err := r.Client.Status().Update(ctx, hcluster); err != nil {
					return ctrl.Result{}, fmt.Errorf("failed to update status: %w", err)
				}
			}

			// Remove any referenced resource annotations for this hosted cluster from secrets and configmaps
			deleteReferencedResourceAnnotation := func(obj client.Object) error {
				annotations := obj.GetAnnotations()
				if annotations == nil {
					return nil
				}
				key := referencedResourceAnnotationPrefix + hcluster.Name
				if _, ok := annotations[key]; !ok {
					return nil
				}
				delete(annotations, key)
				obj.SetAnnotations(annotations)
				if err := r.Update(ctx, obj); err != nil {
					return err
				}
				return nil
			}

			var secretList corev1.SecretList
			if err := r.List(ctx, &secretList, client.InNamespace(hcluster.Namespace)); err != nil {
				return ctrl.Result{}, fmt.Errorf("failed to list secrets: %w", err)
			}
			for _, secret := range secretList.Items {
				if err := deleteReferencedResourceAnnotation(&secret); err != nil {
					return ctrl.Result{}, fmt.Errorf("failed to delete referenced resource annotation on secret: %w", err)
				}
			}

			var configmapList corev1.ConfigMapList
			if err := r.List(ctx, &configmapList, client.InNamespace(hcluster.Namespace)); err != nil {
				return ctrl.Result{}, fmt.Errorf("failed to list configmaps: %w", err)
			}
			for _, configmap := range configmapList.Items {
				if err := deleteReferencedResourceAnnotation(&configmap); err != nil {
					return ctrl.Result{}, fmt.Errorf("failed to delete referenced resource annotation on configmap: %w", err)
				}
			}
		}
	}

	// Bubble up CloudResourcesDestroyed condition from the hostedControlPlane.
	// We set this condition even if the HC is being deleted, so we can construct SLIs for deletion times.
	{
		if hcp != nil && hcp.DeletionTimestamp != nil {
			freshCondition := &metav1.Condition{
				Type:               string(hyperv1.CloudResourcesDestroyed),
				Status:             metav1.ConditionUnknown,
				Reason:             hyperv1.StatusUnknownReason,
				ObservedGeneration: hcluster.Generation,
			}

			cloudResourcesDestroyedCondition := meta.FindStatusCondition(hcp.Status.Conditions, string(hyperv1.CloudResourcesDestroyed))
			if cloudResourcesDestroyedCondition != nil {
				freshCondition = cloudResourcesDestroyedCondition
			}

			oldCondition := meta.FindStatusCondition(hcluster.Status.Conditions, string(hyperv1.CloudResourcesDestroyed))
			if oldCondition == nil || oldCondition.Message != freshCondition.Message {
				freshCondition.ObservedGeneration = hcluster.Generation
				meta.SetStatusCondition(&hcluster.Status.Conditions, *freshCondition)
				// Persist status updates
				if err := r.Client.Status().Update(ctx, hcluster); err != nil {
					return ctrl.Result{}, fmt.Errorf("failed to update status: %w", err)
				}
			}
		}
	}

	var hcDestroyGracePeriod time.Duration

	if gracePeriodString := hcluster.Annotations[hyperv1.HCDestroyGracePeriodAnnotation]; len(gracePeriodString) > 0 {
		hcDestroyGracePeriod, err = time.ParseDuration(gracePeriodString)
		if err != nil {
			return ctrl.Result{}, fmt.Errorf("failed to parse %s annotation: %w", hyperv1.HCDestroyGracePeriodAnnotation, err)
		}
	}

	// If deleted, clean up and return early.
	if !hcluster.DeletionTimestamp.IsZero() {
		// This new condition is necessary for OCM personnel to report any cloud dangling objects to the user.
		// The grace period is customizable using an annotation called HCDestroyGracePeriodAnnotation. It's a time.Duration annotation.
		// This annotation will create a new condition called HostedClusterDestroyed which in conjunction with CloudResourcesDestroyed
		// a SRE could determine if there are dangling objects once the HostedCluster is deleted. These cloud dangling objects will remain
		// in AWS, and SRE will report them to the final user.
		hostedClusterDestroyedCondition := meta.FindStatusCondition(hcluster.Status.Conditions, string(hyperv1.HostedClusterDestroyed))
		if hostedClusterDestroyedCondition == nil || hostedClusterDestroyedCondition.Status != metav1.ConditionTrue {
			// Keep trying to delete until we know it's safe to finalize.
			completed, err := r.delete(ctx, hcluster)
			if err != nil {
				return ctrl.Result{}, fmt.Errorf("failed to delete hostedcluster: %w", err)
			}
			if !completed {
				log.Info("hostedcluster is still deleting", "name", req.NamespacedName)
				return ctrl.Result{RequeueAfter: clusterDeletionRequeueDuration}, nil
			}
		}

		// Once the deletion has occurred, we need to clean up cluster-wide resources
		selector := client.MatchingLabelsSelector{Selector: labels.SelectorFromSet(labels.Set{
			controlplanepkioperatormanifests.OwningHostedClusterNamespaceLabel: hcluster.Namespace,
			controlplanepkioperatormanifests.OwningHostedClusterNameLabel:      hcluster.Name,
		})}
		var crs rbacv1.ClusterRoleList
		if err := r.List(ctx, &crs, selector); err != nil {
			return ctrl.Result{}, fmt.Errorf("failed to list cluster roles: %w", err)
		}
		if len(crs.Items) > 0 {
			if err := r.DeleteAllOf(ctx, &rbacv1.ClusterRole{}, selector); err != nil {
				return ctrl.Result{}, fmt.Errorf("failed to delete cluster roles: %w", err)
			}
		}
		var crbs rbacv1.ClusterRoleBindingList
		if err := r.List(ctx, &crbs, selector); err != nil {
			return ctrl.Result{}, fmt.Errorf("failed to list cluster role bindings: %w", err)
		}
		if len(crbs.Items) > 0 {
			if err := r.DeleteAllOf(ctx, &rbacv1.ClusterRoleBinding{}, selector); err != nil {
				return ctrl.Result{}, fmt.Errorf("failed to delete cluster role bindings: %w", err)
			}
		}

		if hcDestroyGracePeriod > 0 {
			if hostedClusterDestroyedCondition == nil {
				hostedClusterDestroyedCondition = &metav1.Condition{
					Type:               string(hyperv1.HostedClusterDestroyed),
					Status:             metav1.ConditionTrue,
					Message:            fmt.Sprintf("Grace period set: %v", hcDestroyGracePeriod),
					Reason:             hyperv1.WaitingForGracePeriodReason,
					LastTransitionTime: metav1.NewTime(time.Now()),
					ObservedGeneration: hcluster.Generation,
				}

				meta.SetStatusCondition(&hcluster.Status.Conditions, *hostedClusterDestroyedCondition)
				if err := r.Client.Status().Update(ctx, hcluster); err != nil {
					return ctrl.Result{}, fmt.Errorf("failed to update status: %w", err)
				}
				log.Info("Waiting for grace period", "gracePeriod", hcDestroyGracePeriod)
				return ctrl.Result{RequeueAfter: hcDestroyGracePeriod}, nil
			}

			if time.Since(hostedClusterDestroyedCondition.LastTransitionTime.Time) < hcDestroyGracePeriod {
				log.Info("Waiting for grace period", "gracePeriod", hcDestroyGracePeriod)
				return ctrl.Result{RequeueAfter: hcDestroyGracePeriod - time.Since(hostedClusterDestroyedCondition.LastTransitionTime.Time)}, nil
			}
			log.Info("grace period finished", "gracePeriod", hcDestroyGracePeriod)
		}

		// Now we can remove the finalizer.
		if controllerutil.ContainsFinalizer(hcluster, HostedClusterFinalizer) {
			controllerutil.RemoveFinalizer(hcluster, HostedClusterFinalizer)
			if err := r.Update(ctx, hcluster); err != nil {
				return ctrl.Result{}, fmt.Errorf("failed to remove finalizer from hostedcluster: %w", err)
			}
		}

		log.Info("Deleted hostedcluster", "name", req.NamespacedName)
		return ctrl.Result{}, nil
	}

	// Part zero: fix up conversion
	originalSpec := hcluster.Spec.DeepCopy()

	// Reconcile converted AWS roles.
	if hcluster.Spec.Platform.AWS != nil {
		if err := r.dereferenceAWSRoles(ctx, hcluster.Name, &hcluster.Spec.Platform.AWS.RolesRef, hcluster.Namespace); err != nil {
			return ctrl.Result{}, err
		}
	}
	if hcluster.Spec.SecretEncryption != nil && hcluster.Spec.SecretEncryption.KMS != nil && hcluster.Spec.SecretEncryption.KMS.AWS != nil {
		if strings.HasPrefix(hcluster.Spec.SecretEncryption.KMS.AWS.Auth.AWSKMSRoleARN, "arn-from-secret::") {
			secretName := strings.TrimPrefix(hcluster.Spec.SecretEncryption.KMS.AWS.Auth.AWSKMSRoleARN, "arn-from-secret::")
			arn, err := r.getARNFromSecret(ctx, hcluster.Name, secretName, hcluster.Namespace)
			if err != nil {
				return ctrl.Result{}, fmt.Errorf("failed to get ARN from secret %s/%s: %w", hcluster.Namespace, secretName, err)
			}
			hcluster.Spec.SecretEncryption.KMS.AWS.Auth.AWSKMSRoleARN = arn
		}
	}

	createOrUpdate := r.createOrUpdate(req)

	// Reconcile platform defaults
	if err := r.reconcilePlatformDefaultSettings(ctx, hcluster, createOrUpdate, log); err != nil {
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

	// Reconcile the ICSP/IDMS from the management cluster
	err = r.RegistryProvider.Reconcile(ctx, r.Client)
	if err != nil {
		return ctrl.Result{}, err
	}
	releaseProvider := r.RegistryProvider.GetReleaseProvider()
	registryClientImageMetadataProvider := r.RegistryProvider.GetMetadataProvider()

	pullSecretBytes, err := hyperutil.GetPullSecretBytes(ctx, r.Client, hcluster)
	if err != nil {
		return ctrl.Result{}, err
	}

	controlPlaneOperatorImage, err := hyperutil.GetControlPlaneOperatorImage(ctx, hcluster, releaseProvider, r.HypershiftOperatorImage, pullSecretBytes)
	if err != nil {
		return ctrl.Result{}, fmt.Errorf("failed to get controlPlaneOperatorImage: %w", err)
	}
	controlPlaneOperatorImageLabels, err := hyperutil.GetControlPlaneOperatorImageLabels(ctx, hcluster, controlPlaneOperatorImage, pullSecretBytes, registryClientImageMetadataProvider)
	if err != nil {
		return ctrl.Result{}, fmt.Errorf("failed to get controlPlaneOperatorImageLabels: %w", err)
	}

	_, cpoSupportsKASCustomKubeconfig := controlPlaneOperatorImageLabels[controlPlaneOperatorSupportsKASCustomKubeconfigLabel]

	if cpoSupportsKASCustomKubeconfig {
		if len(hcluster.Spec.KubeAPIServerDNSName) > 0 {
			CustomKubeconfigSecret := manifests.KubeConfigExternalSecret(hcluster.Namespace, hcluster.Name)
			err := r.Client.Get(ctx, client.ObjectKeyFromObject(CustomKubeconfigSecret), CustomKubeconfigSecret)
			if err != nil {
				if !apierrors.IsNotFound(err) {
					return ctrl.Result{}, fmt.Errorf("failed to reconcile external kubeconfig secret: %w", err)
				}
			} else {
				hcluster.Status.CustomKubeconfig = &corev1.LocalObjectReference{Name: CustomKubeconfigSecret.Name}
			}
		}
	}

	// Set kubeadminPassword status
	{
		explicitOauthConfig := hcluster.Spec.Configuration != nil && hcluster.Spec.Configuration.OAuth != nil
		if explicitOauthConfig {
			hcluster.Status.KubeadminPassword = nil
		} else {
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
	}

	// Set version status
	hcluster.Status.Version = computeClusterVersionStatus(r.Clock, hcluster, hcp)

	// Copy the CVO conditions from the HCP.
	hcpCVOConditions := map[hyperv1.ConditionType]*metav1.Condition{
		hyperv1.ClusterVersionSucceeding:       nil,
		hyperv1.ClusterVersionProgressing:      nil,
		hyperv1.ClusterVersionReleaseAccepted:  nil,
		hyperv1.ClusterVersionRetrievedUpdates: nil,
		hyperv1.ClusterVersionUpgradeable:      nil,
		hyperv1.ClusterVersionAvailable:        nil,
	}
	if hcp != nil {
		hcpCVOConditions = map[hyperv1.ConditionType]*metav1.Condition{
			hyperv1.ClusterVersionSucceeding:       meta.FindStatusCondition(hcp.Status.Conditions, string(hyperv1.ClusterVersionFailing)),
			hyperv1.ClusterVersionProgressing:      meta.FindStatusCondition(hcp.Status.Conditions, string(hyperv1.ClusterVersionProgressing)),
			hyperv1.ClusterVersionReleaseAccepted:  meta.FindStatusCondition(hcp.Status.Conditions, string(hyperv1.ClusterVersionReleaseAccepted)),
			hyperv1.ClusterVersionRetrievedUpdates: meta.FindStatusCondition(hcp.Status.Conditions, string(hyperv1.ClusterVersionRetrievedUpdates)),
			hyperv1.ClusterVersionUpgradeable:      meta.FindStatusCondition(hcp.Status.Conditions, string(hyperv1.ClusterVersionUpgradeable)),
			hyperv1.ClusterVersionAvailable:        meta.FindStatusCondition(hcp.Status.Conditions, string(hyperv1.ClusterVersionAvailable)),
		}
	}

	for conditionType := range hcpCVOConditions {
		var hcCVOCondition *metav1.Condition
		// Set unknown status.
		var unknownStatusMessage string
		if hcpCVOConditions[conditionType] == nil {
			unknownStatusMessage = "Condition not found in the CVO."
		}

		hcCVOCondition = &metav1.Condition{
			Type:               string(conditionType),
			Status:             metav1.ConditionUnknown,
			Reason:             hyperv1.StatusUnknownReason,
			Message:            unknownStatusMessage,
			ObservedGeneration: hcluster.Generation,
		}

		if hcp != nil && hcpCVOConditions[conditionType] != nil {
			// Bubble up info from HCP.
			hcCVOCondition = hcpCVOConditions[conditionType]
			hcCVOCondition.ObservedGeneration = hcluster.Generation

			// Inverse ClusterVersionFailing condition into ClusterVersionSucceeding
			// So consumers e.g. UI can categorize as good (True) / bad (False).
			if conditionType == hyperv1.ClusterVersionSucceeding {
				hcCVOCondition.Type = string(hyperv1.ClusterVersionSucceeding)
				var status metav1.ConditionStatus
				switch hcpCVOConditions[conditionType].Status {
				case metav1.ConditionTrue:
					status = metav1.ConditionFalse
				case metav1.ConditionFalse:
					status = metav1.ConditionTrue
				}
				hcCVOCondition.Status = status
			}
		}

		if hcCVOCondition.Type == string(hyperv1.ClusterVersionRetrievedUpdates) && hcCVOCondition.Reason == hyperv1.StatusUnknownReason {
			// until all HostedControlPlane controllers understand how to propagate this condition, avoid bothering folks with unknown status in HostedCluster conditions.
			meta.RemoveStatusCondition(&hcluster.Status.Conditions, string(hyperv1.ClusterVersionRetrievedUpdates))
			continue
		}

		meta.SetStatusCondition(&hcluster.Status.Conditions, *hcCVOCondition)
	}

	// Copy the Degraded condition on the hostedcontrolplane
	{
		condition := &metav1.Condition{
			Type:               string(hyperv1.HostedClusterDegraded),
			Status:             metav1.ConditionUnknown,
			Reason:             hyperv1.StatusUnknownReason,
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

	// Copy the ValidKubeVirtInfraNetworkMTU condition from the HostedControlPlane
	if hcluster.Spec.Platform.Type == hyperv1.KubevirtPlatform {
		if hcp != nil {
			validMtuCondCreated := meta.FindStatusCondition(hcp.Status.Conditions, string(hyperv1.ValidKubeVirtInfraNetworkMTU))
			if validMtuCondCreated != nil {
				validMtuCondCreated.ObservedGeneration = hcluster.Generation
				meta.SetStatusCondition(&hcluster.Status.Conditions, *validMtuCondCreated)
			}
		}
		if err := r.syncKVLiveMigratableCondition(ctx, hcluster); err != nil {
			return ctrl.Result{}, fmt.Errorf("failed to update condition: %w", err)
		}
	}

	// Copy conditions from hostedcontrolplane
	{
		hcpConditions := []hyperv1.ConditionType{
			hyperv1.EtcdAvailable,
			hyperv1.KubeAPIServerAvailable,
			hyperv1.InfrastructureReady,
			hyperv1.ExternalDNSReachable,
			hyperv1.ValidHostedControlPlaneConfiguration,
			hyperv1.ValidReleaseInfo,
			hyperv1.ValidIDPConfiguration,
			hyperv1.HostedClusterRestoredFromBackup,
		}

		for _, conditionType := range hcpConditions {
			condition := &metav1.Condition{
				Type:               string(conditionType),
				Status:             metav1.ConditionUnknown,
				Reason:             hyperv1.StatusUnknownReason,
				Message:            "The hosted control plane is not found",
				ObservedGeneration: hcluster.Generation,
			}
			if hcp != nil {
				hcpCondition := meta.FindStatusCondition(hcp.Status.Conditions, string(conditionType))
				if hcpCondition != nil {
					condition = hcpCondition
				} else {
					condition.Message = "Condition not found in the HCP"
				}
			}
			condition.ObservedGeneration = hcluster.Generation
			meta.SetStatusCondition(&hcluster.Status.Conditions, *condition)
		}
	}

	// Copy the platform status from the hostedcontrolplane
	if hcp != nil {
		hcluster.Status.Platform = hcp.Status.Platform
	}

	// Copy the AWSDefaultSecurityGroupCreated condition from the hostedcontrolplane
	if hcluster.Spec.Platform.Type == hyperv1.AWSPlatform {
		if hcp != nil {
			sgCreated := meta.FindStatusCondition(hcp.Status.Conditions, string(hyperv1.AWSDefaultSecurityGroupCreated))
			if sgCreated != nil {
				sgCreated.ObservedGeneration = hcluster.Generation
				meta.SetStatusCondition(&hcluster.Status.Conditions, *sgCreated)
			}

			validKMSConfig := meta.FindStatusCondition(hcp.Status.Conditions, string(hyperv1.ValidAWSKMSConfig))
			if validKMSConfig != nil {
				validKMSConfig.ObservedGeneration = hcluster.Generation
				meta.SetStatusCondition(&hcluster.Status.Conditions, *validKMSConfig)
			}
		}
	}

	if hcluster.Spec.Platform.Type == hyperv1.AzurePlatform {
		if hcp != nil {
			validKMSConfig := meta.FindStatusCondition(hcp.Status.Conditions, string(hyperv1.ValidAzureKMSConfig))
			if validKMSConfig != nil {
				validKMSConfig.ObservedGeneration = hcluster.Generation
				meta.SetStatusCondition(&hcluster.Status.Conditions, *validKMSConfig)
			}
		}
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
		availableCondition := computeHostedClusterAvailability(hcluster, hcp)
		_, isHasBeenAvailableAnnotationSet := hcluster.Annotations[hcmetrics.HasBeenAvailableAnnotation]

		meta.SetStatusCondition(&hcluster.Status.Conditions, availableCondition)

		if availableCondition.Status == metav1.ConditionTrue && !isHasBeenAvailableAnnotationSet {
			original := hcluster.DeepCopy()

			if hcluster.Annotations == nil {
				hcluster.Annotations = make(map[string]string)
			}

			hcluster.Annotations[hcmetrics.HasBeenAvailableAnnotation] = "true"

			if err := r.Patch(ctx, hcluster, client.MergeFromWithOptions(original)); err != nil {
				return ctrl.Result{}, fmt.Errorf("cannot patch hosted cluster with has been available annotation: %w", err)
			}
		}
	}

	// Copy AWSEndpointAvailable and AWSEndpointServiceAvailable conditions from the AWSEndpointServices.
	if hcluster.Spec.Platform.Type == hyperv1.AWSPlatform {
		hcpNamespace := manifests.HostedControlPlaneNamespace(hcluster.Namespace, hcluster.Name)
		var awsEndpointServiceList hyperv1.AWSEndpointServiceList
		if err := r.List(ctx, &awsEndpointServiceList, &client.ListOptions{Namespace: hcpNamespace}); err != nil {
			condition := metav1.Condition{
				Type:    string(hyperv1.AWSEndpointAvailable),
				Status:  metav1.ConditionUnknown,
				Reason:  hyperv1.NotFoundReason,
				Message: fmt.Sprintf("error listing awsendpointservices in namespace %s: %v", hcpNamespace, err),
			}
			meta.SetStatusCondition(&hcluster.Status.Conditions, condition)
		} else {
			meta.SetStatusCondition(&hcluster.Status.Conditions, computeAWSEndpointServiceCondition(awsEndpointServiceList, hyperv1.AWSEndpointAvailable))
			meta.SetStatusCondition(&hcluster.Status.Conditions, computeAWSEndpointServiceCondition(awsEndpointServiceList, hyperv1.AWSEndpointServiceAvailable))
		}
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
			condition.Reason = hyperv1.AsExpectedReason
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
			condition.Reason = hyperv1.AsExpectedReason
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
				if ignitionServerRoute.Spec.Host != "" {
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
			ignitionService := ignitionserver.ProxyService(controlPlaneNamespace.GetName())
			if err = r.Client.Get(ctx, client.ObjectKeyFromObject(ignitionService), ignitionService); err != nil {
				if !apierrors.IsNotFound(err) {
					return ctrl.Result{}, fmt.Errorf("failed to get ignition proxy service: %w", err)
				} else {
					// ignition-server-proxy service not found, possible IBM platform or older CPO that doesn't create the service
					ignitionService = ignitionserver.Service(controlPlaneNamespace.GetName())
					if err = r.Client.Get(ctx, client.ObjectKeyFromObject(ignitionService), ignitionService); err != nil {
						if !apierrors.IsNotFound(err) {
							return ctrl.Result{}, fmt.Errorf("failed to get ignition service: %w", err)
						}
					}
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

	// Set the Control Plane and OAuth endpoints URL
	{
		if hcp != nil {
			hcluster.Status.ControlPlaneEndpoint = hcp.Status.ControlPlaneEndpoint

			// TODO: (cewong) Remove this hack when we no longer need to support HostedControlPlanes that report
			// the wrong port for the route strategy.
			if isAPIServerRoute(hcluster) {
				hcluster.Status.ControlPlaneEndpoint.Port = 443
			}
			hcluster.Status.OAuthCallbackURLTemplate = hcp.Status.OAuthCallbackURLTemplate
		}
	}

	// Set the ignition server availability condition by checking its deployment.
	{
		// Assume the server is unavailable unless proven otherwise.
		newCondition := metav1.Condition{
			Type:   string(hyperv1.IgnitionEndpointAvailable),
			Status: metav1.ConditionUnknown,
			Reason: hyperv1.StatusUnknownReason,
		}
		// Check to ensure the deployment exists and is available.
		deployment := ignitionserver.Deployment(controlPlaneNamespace.Name)
		if err := r.Client.Get(ctx, client.ObjectKeyFromObject(deployment), deployment); err != nil {
			if apierrors.IsNotFound(err) {
				newCondition = metav1.Condition{
					Type:    string(hyperv1.IgnitionEndpointAvailable),
					Status:  metav1.ConditionFalse,
					Reason:  hyperv1.NotFoundReason,
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
				Reason:  hyperv1.WaitingForAvailableReason,
				Message: "Ignition server deployment is not yet available",
			}
			for _, cond := range deployment.Status.Conditions {
				if cond.Type == appsv1.DeploymentAvailable && cond.Status == corev1.ConditionTrue {
					newCondition = metav1.Condition{
						Type:    string(hyperv1.IgnitionEndpointAvailable),
						Status:  metav1.ConditionTrue,
						Reason:  hyperv1.AsExpectedReason,
						Message: "Ignition server deployment is available",
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
			err := r.validateReleaseImage(ctx, hcluster, releaseProvider)
			if err != nil {
				condition.Status = metav1.ConditionFalse
				condition.Message = err.Error()

				if apierrors.IsNotFound(err) {
					condition.Reason = hyperv1.SecretNotFoundReason
				} else {
					condition.Reason = hyperv1.InvalidImageReason
				}
			} else {
				condition.Status = metav1.ConditionTrue
				condition.Message = "Release image is valid"
				condition.Reason = hyperv1.AsExpectedReason
			}
			meta.SetStatusCondition(&hcluster.Status.Conditions, condition)
		}
	}

	// Set HostedCluster payload arch
	payloadArch, err := hyperutil.DetermineHostedClusterPayloadArch(ctx, r.Client, hcluster, registryClientImageMetadataProvider)
	if err != nil {
		condition := metav1.Condition{
			Type:               string(hyperv1.ValidReleaseImage),
			ObservedGeneration: hcluster.Generation,
		}
		condition.Status = metav1.ConditionFalse
		condition.Message = err.Error()
		condition.Reason = hyperv1.PayloadArchNotFoundReason
		meta.SetStatusCondition(&hcluster.Status.Conditions, condition)

		return ctrl.Result{}, err
	}

	hcluster.Status.PayloadArch = payloadArch

	releaseImage, err := r.lookupReleaseImage(ctx, hcluster, releaseProvider)
	if err != nil {
		return ctrl.Result{}, fmt.Errorf("failed to lookup release image: %w", err)
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
		refWithDigest := func() (string, error) {
			_, ref, err := registryClientImageMetadataProvider.GetDigest(ctx, hcluster.Spec.Release.Image, pullSecretBytes)
			if err != nil {
				return "", err
			}
			return ref.String(), nil
		}

		progressing, err := isProgressing(hcluster, releaseImage, refWithDigest)
		if err != nil {
			condition.Status = metav1.ConditionFalse
			condition.Message = err.Error()
			condition.Reason = hyperv1.BlockedReason
		}
		if progressing {
			condition.Status = metav1.ConditionTrue
			condition.Message = "HostedCluster is deploying, upgrading, or reconfiguring"
			condition.Reason = "Progressing"
		}
		meta.SetStatusCondition(&hcluster.Status.Conditions, condition)
	}

	// Copy the configuration status from the hostedcontrolplane
	if hcp != nil {
		hcluster.Status.Configuration = hcp.Status.Configuration
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
	if !controllerutil.ContainsFinalizer(hcluster, HostedClusterFinalizer) {
		controllerutil.AddFinalizer(hcluster, HostedClusterFinalizer)
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
		if err := pauseCAPICluster(ctx, r.Client, hcp); err != nil {
			return ctrl.Result{}, err
		}
		log.Info("Reconciliation paused", "name", req.NamespacedName, "pausedUntil", *hcluster.Spec.PausedUntil)
		return ctrl.Result{RequeueAfter: duration}, nil
	}

	if err := r.defaultClusterIDsIfNeeded(ctx, hcluster); err != nil {
		return ctrl.Result{}, err
	}

	if err = r.reconcileCLISecrets(ctx, createOrUpdate, hcluster); err != nil {
		return ctrl.Result{}, fmt.Errorf("failed to reconcile the CLI secrets: %w", err)
	}

	// Set the infraID as Tag on all created AWS
	if err := r.reconcileAWSResourceTags(ctx, hcluster); err != nil {
		return ctrl.Result{}, err
	}

	// Block here if the cluster configuration does not pass validation
	{
		validConfig := meta.FindStatusCondition(hcluster.Status.Conditions, string(hyperv1.ValidHostedClusterConfiguration))
		if validConfig != nil && validConfig.Status == metav1.ConditionFalse {
			// an error should be returned here because the ValidHostedClusterConfiguration status may be transient
			return ctrl.Result{}, fmt.Errorf("configuration is invalid: %s", validConfig.Message)
		}
		supportedHostedCluster := meta.FindStatusCondition(hcluster.Status.Conditions, string(hyperv1.SupportedHostedCluster))
		if supportedHostedCluster != nil && supportedHostedCluster.Status == metav1.ConditionFalse {
			log.Error(fmt.Errorf("not supported by operator configuration"), "reconciliation is blocked", "message", supportedHostedCluster.Message)
			return ctrl.Result{}, nil
		}
		validReleaseImage := meta.FindStatusCondition(hcluster.Status.Conditions, string(hyperv1.ValidReleaseImage))
		if validReleaseImage != nil && validReleaseImage.Status == metav1.ConditionFalse {
			if validReleaseImage.Reason == hyperv1.SecretNotFoundReason {
				return ctrl.Result{}, fmt.Errorf("%s", validReleaseImage.Message)
			}
			log.Error(fmt.Errorf("release image is invalid"), "reconciliation is blocked", "message", validReleaseImage.Message)
			return ctrl.Result{}, nil
		}
		upgrading, msg, err := isUpgrading(hcluster, releaseImage)
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

	cpoHasUtilities := false
	if _, hasLabel := controlPlaneOperatorImageLabels[controlPlaneOperatorSubcommandsLabel]; hasLabel {
		cpoHasUtilities = true
	}
	utilitiesImage := controlPlaneOperatorImage
	if !cpoHasUtilities {
		utilitiesImage = r.HypershiftOperatorImage
	}

	_, controlPlaneOperatorAppliesManagementKASNetworkPolicyLabel := controlPlaneOperatorImageLabels[controlPlaneOperatorAppliesManagementKASNetworkPolicyLabel]
	_, controlPlanePKIOperatorSignsCSRs := controlPlaneOperatorImageLabels[controlPlanePKIOperatorSignsCSRsLabel]
	_, useRestrictedPSA := controlPlaneOperatorImageLabels[useRestrictedPodSecurityLabel]
	_, defaultToControlPlaneV2 := controlPlaneOperatorImageLabels[defaultToControlPlaneV2Label]

	// Reconcile the hosted cluster namespace
	_, err = createOrUpdate(ctx, r.Client, controlPlaneNamespace, func() error {
		if controlPlaneNamespace.Labels == nil {
			controlPlaneNamespace.Labels = make(map[string]string)
		}
		controlPlaneNamespace.Labels[ControlPlaneNamespaceLabelKey] = "true"

		// Set pod security labels on HCP namespace
		psaOverride := hcluster.Annotations[hyperv1.PodSecurityAdmissionLabelOverrideAnnotation]
		if psaOverride != "" {
			controlPlaneNamespace.Labels["pod-security.kubernetes.io/enforce"] = psaOverride
			controlPlaneNamespace.Labels["pod-security.kubernetes.io/audit"] = psaOverride
			controlPlaneNamespace.Labels["pod-security.kubernetes.io/warn"] = psaOverride
		} else if useRestrictedPSA {
			controlPlaneNamespace.Labels["pod-security.kubernetes.io/enforce"] = "restricted"
			controlPlaneNamespace.Labels["pod-security.kubernetes.io/audit"] = "restricted"
			controlPlaneNamespace.Labels["pod-security.kubernetes.io/warn"] = "restricted"
		} else {
			controlPlaneNamespace.Labels["pod-security.kubernetes.io/enforce"] = "privileged"
			controlPlaneNamespace.Labels["pod-security.kubernetes.io/audit"] = "privileged"
			controlPlaneNamespace.Labels["pod-security.kubernetes.io/warn"] = "privileged"
		}
		controlPlaneNamespace.Labels["security.openshift.io/scc.podSecurityLabelSync"] = "false"

		// Enable monitoring for hosted control plane namespaces
		if r.EnableOCPClusterMonitoring {
			controlPlaneNamespace.Labels["openshift.io/cluster-monitoring"] = "true"
		}

		if r.SetDefaultSecurityContext {
			// Only set the SecurtyContext UID annotation if it's not already set.
			_, ok := controlPlaneNamespace.Annotations[DefaultSecurityContextUIDAnnnotation]
			if !ok {
				uid, err := getNextAvailableSecurityContextUID(ctx, r.Client)
				if err != nil {
					return fmt.Errorf("failed to get next available SecurityContext UID: %w", err)
				}
				if controlPlaneNamespace.Annotations == nil {
					controlPlaneNamespace.Annotations = make(map[string]string)
				}
				controlPlaneNamespace.Annotations[DefaultSecurityContextUIDAnnnotation] = strconv.FormatInt(uid, 10)
			}
		}

		// Enable observability operator monitoring
		metrics.EnableOBOMonitoring(controlPlaneNamespace)

		return nil
	})
	if err != nil {
		return ctrl.Result{}, fmt.Errorf("failed to reconcile namespace: %w", err)
	}

	p, err := platform.GetPlatform(ctx, hcluster, releaseProvider, utilitiesImage, pullSecretBytes)
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
		if !meta.IsStatusConditionTrue(hcluster.Status.Conditions, string(hyperv1.PlatformCredentialsFound)) {
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

	// Set the HostedCluster restored from backup condition
	{
		if _, exists := hcluster.Annotations[hyperv1.HostedClusterRestoredFromBackupAnnotation]; exists {
			freshCondition := &metav1.Condition{
				Type:               string(hyperv1.HostedClusterRestoredFromBackup),
				Reason:             hyperv1.RecoveryFinishedReason,
				Status:             metav1.ConditionUnknown,
				ObservedGeneration: hcluster.Generation,
			}

			if hcp != nil {
				hostedClusterRestoredFromBackupCondition := meta.FindStatusCondition(hcp.Status.Conditions, string(hyperv1.HostedClusterRestoredFromBackup))
				if hostedClusterRestoredFromBackupCondition != nil {
					freshCondition = hostedClusterRestoredFromBackupCondition
				}
			}

			oldCondition := meta.FindStatusCondition(hcluster.Status.Conditions, string(hyperv1.HostedClusterRestoredFromBackup))

			// Preserve previous status if we can no longer determine the status
			if oldCondition != nil && freshCondition.Status == metav1.ConditionUnknown {
				freshCondition.Status = oldCondition.Status
			}

			// If the condition is not set, or the status is different, set the condition
			if oldCondition == nil || oldCondition.Status != freshCondition.Status {
				freshCondition.ObservedGeneration = hcluster.Generation
			}

			// If the condition is true, delete the hc annotation. It will be eventually bubbled down to the hcp.
			if freshCondition.Status == metav1.ConditionTrue {
				hclusterAnnotations := hcluster.GetAnnotations()
				delete(hclusterAnnotations, hyperv1.HostedClusterRestoredFromBackupAnnotation)
				hcluster.SetAnnotations(hclusterAnnotations)
				if err := r.Client.Update(ctx, hcluster); err != nil {
					return ctrl.Result{}, fmt.Errorf("failed to remove annotations %v: %w", string(hyperv1.HostedClusterRestoredFromBackup), err)
				}
			}

			// Persist status updates
			meta.SetStatusCondition(&hcluster.Status.Conditions, *freshCondition)
			if err := r.Client.Status().Update(ctx, hcluster); err != nil {
				return ctrl.Result{}, fmt.Errorf("failed to update status %v: %w", string(hyperv1.HostedClusterRestoredFromBackup), err)
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
		if err := ensureReferencedResourceAnnotation(ctx, r.Client, hcluster.Name, &src); err != nil {
			return ctrl.Result{}, fmt.Errorf("failed to set referenced resource annotation: %w", err)
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
			if err := ensureReferencedResourceAnnotation(ctx, r.Client, hcluster.Name, &src); err != nil {
				return ctrl.Result{}, fmt.Errorf("failed to set referenced resource annotation: %w", err)
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
				if err := ensureReferencedResourceAnnotation(ctx, r.Client, hcluster.Name, &src); err != nil {
					return ctrl.Result{}, fmt.Errorf("failed to set referenced resource annotation: %w", err)
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
			if err := ensureReferencedResourceAnnotation(ctx, r.Client, hcluster.Name, &src); err != nil {
				return ctrl.Result{}, fmt.Errorf("failed to set referenced resource annotation: %w", err)
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
			return ctrl.Result{}, fmt.Errorf("failed to get hostedcluster SSHKey secret %s: %w", hcluster.Spec.SSHKey.Name, err)
		}
		if err := ensureReferencedResourceAnnotation(ctx, r.Client, hcluster.Name, &src); err != nil {
			return ctrl.Result{}, fmt.Errorf("failed to set referenced resource annotation: %w", err)
		}
		dest := controlplaneoperator.SSHKey(controlPlaneNamespace.Name)
		_, err = createOrUpdate(ctx, r.Client, dest, func() error {
			srcData, srcHasData := src.Data["id_rsa.pub"]
			if !srcHasData {
				return fmt.Errorf("hostedcluster SSHKey secret %q must have a id_rsa.pub key", src.Name)
			}
			dest.Type = corev1.SecretTypeOpaque
			if dest.Data == nil {
				dest.Data = map[string][]byte{}
			}
			dest.Data["id_rsa.pub"] = srcData
			return nil
		})
		if err != nil {
			return ctrl.Result{}, fmt.Errorf("failed to reconcile controlplane SSHKey secret: %w", err)
		}
	}

	// Reconcile the HostedControlPlane AdditionalTrustBundle ConfigMap by resolving the source reference
	// from the HostedCluster and syncing the CM in the control plane namespace.
	if err := r.reconcileAdditionalTrustBundle(ctx, hcluster, createOrUpdate, controlPlaneNamespace.Name); err != nil {
		return ctrl.Result{}, err
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
		if err := ensureReferencedResourceAnnotation(ctx, r.Client, hcluster.Name, unmanagedEtcdTLSClientSecret); err != nil {
			return ctrl.Result{}, fmt.Errorf("failed to set referenced resource annotation: %w", err)
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

	// Reconcile the ETCD member recovery
	var requeueAfter *time.Duration
	if r.EnableEtcdRecovery &&
		hcluster.Spec.Etcd.ManagementType == hyperv1.Managed &&
		hcluster.Spec.ControllerAvailabilityPolicy == hyperv1.HighlyAvailable {
		var err error
		if requeueAfter, err = r.reconcileETCDMemberRecovery(ctx, hcluster, createOrUpdate); err != nil {
			return ctrl.Result{}, fmt.Errorf("failed to perform etcd member recovery: %w", err)
		}
	}

	// Reconcile global config related configmaps and secrets
	{
		if hcluster.Spec.Configuration != nil {
			configMapRefs := configrefs.ConfigMapRefs(hcluster.Spec.Configuration)
			for _, configMapRef := range configMapRefs {
				sourceCM := &corev1.ConfigMap{}
				if err := r.Get(ctx, client.ObjectKey{Namespace: hcluster.Namespace, Name: configMapRef}, sourceCM); err != nil {
					return ctrl.Result{}, fmt.Errorf("failed to get referenced configmap %s/%s: %w", hcluster.Namespace, configMapRef, err)
				}
				if err := ensureReferencedResourceAnnotation(ctx, r.Client, hcluster.Name, sourceCM); err != nil {
					return ctrl.Result{}, fmt.Errorf("failed to set referenced resource annotation: %w", err)
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
			secretRefs := configrefs.SecretRefs(hcluster.Spec.Configuration)
			for _, secretRef := range secretRefs {
				sourceSecret := &corev1.Secret{}
				if err := r.Get(ctx, client.ObjectKey{Namespace: hcluster.Namespace, Name: secretRef}, sourceSecret); err != nil {
					return ctrl.Result{}, fmt.Errorf("failed to get referenced secret %s/%s: %w", hcluster.Namespace, secretRef, err)
				}
				if err := ensureReferencedResourceAnnotation(ctx, r.Client, hcluster.Name, sourceSecret); err != nil {
					return ctrl.Result{}, fmt.Errorf("failed to set referenced resource annotation: %w", err)
				}
				if err := ensureHostedResourcesAreEmpty(ctx, r.Client, hcluster, sourceSecret); err != nil {
					return ctrl.Result{}, fmt.Errorf("failed to validate referenced secret %s/%s: %w", hcluster.Namespace, secretRef, err)
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

	// Get release image version
	var releaseImageVersion semver.Version
	releaseImageVersion, err = semver.Parse(releaseImage.Version())
	if err != nil {
		return ctrl.Result{}, fmt.Errorf("failed to parse release image version: %w", err)
	}

	// Reconcile the HostedControlPlane
	isAutoscalingNeeded, err := r.isAutoscalingNeeded(ctx, hcluster)
	if err != nil {
		return ctrl.Result{}, fmt.Errorf("failed to determine if autoscaler is needed: %w", err)
	}
	hcp = controlplaneoperator.HostedControlPlane(controlPlaneNamespace.Name, hcluster.Name)
	_, err = createOrUpdate(ctx, r.Client, hcp, func() error {
		return reconcileHostedControlPlane(hcp, hcluster, isAutoscalingNeeded,
			annotationsForCertRenewal(log,
				hcp,
				shouldCheckForStaleCerts(hcluster, defaultToControlPlaneV2),
				r.kasServingCertHashFromSecret(ctx, hcp),
				r.kasServingCertHashFromEndpoint(kasHostAndPortFromHCP(hcp))))
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

	// Reconcile the monitoring dashboard if configured
	if r.MonitoringDashboards {
		if err := r.reconcileMonitoringDashboard(ctx, createOrUpdate, hcluster); err != nil {
			return ctrl.Result{}, fmt.Errorf("failed to reconcile monitoring dashboard: %w", err)
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

	if cpoSupportsKASCustomKubeconfig {
		// Reconcile the HostedControlPlane external kubeconfig if one is reported
		if len(hcp.Spec.KubeAPIServerDNSName) > 0 {
			if hcp.Status.CustomKubeconfig != nil {
				src := &corev1.Secret{
					ObjectMeta: metav1.ObjectMeta{
						Namespace: hcp.Namespace,
						Name:      hcp.Status.CustomKubeconfig.Name,
					},
				}
				err := r.Client.Get(ctx, client.ObjectKeyFromObject(src), src)
				if err != nil {
					return ctrl.Result{}, fmt.Errorf("failed to get controlplane custom external kubeconfig secret %q: %w", client.ObjectKeyFromObject(src), err)
				}
				dest := manifests.KubeConfigExternalSecret(hcluster.Namespace, hcluster.Name)
				_, err = createOrUpdate(ctx, r.Client, dest, func() error {
					key := hcp.Status.CustomKubeconfig.Key
					srcData, srcHasData := src.Data[key]
					if !srcHasData {
						return fmt.Errorf("controlplane custom external kubeconfig secret %q must have a %q key", client.ObjectKeyFromObject(src), key)
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
					return ctrl.Result{}, fmt.Errorf("failed to reconcile hostedcluster custom external kubeconfig secret: %w", err)
				}
			}
		} else {
			// Delete the custom external kubeconfig secret if it exists and the external name is not set
			if hcluster.Status.CustomKubeconfig != nil {
				customKubeconfig := &corev1.Secret{
					ObjectMeta: metav1.ObjectMeta{
						Namespace: hcluster.Namespace,
						Name:      hcluster.Status.CustomKubeconfig.Name,
					},
				}
				if _, err := hyperutil.DeleteIfNeeded(ctx, r.Client, customKubeconfig); err != nil {
					return ctrl.Result{}, fmt.Errorf("failed to delete custom external kubeconfig secret %q: %w", client.ObjectKeyFromObject(customKubeconfig), err)
				}
				hcluster.Status.CustomKubeconfig = nil
			}
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
	} else {
		KubeadminPasswordSecret := manifests.KubeadminPasswordSecret(hcluster.Namespace, hcluster.Name)
		if err := r.Client.Get(ctx, client.ObjectKeyFromObject(KubeadminPasswordSecret), KubeadminPasswordSecret); err != nil {
			if !apierrors.IsNotFound(err) {
				return ctrl.Result{}, fmt.Errorf("failed to get hostedcluster kubeadmin password secret %q: %w", client.ObjectKeyFromObject(KubeadminPasswordSecret), err)
			}
		} else {
			if err := r.Client.Delete(ctx, KubeadminPasswordSecret); err != nil {
				return ctrl.Result{}, fmt.Errorf("failed to delete hostedcluster kubeadmin password secret %q: %w", client.ObjectKeyFromObject(KubeadminPasswordSecret), err)
			}
		}
	}

	defaultIngressDomain, err := r.defaultIngressDomain(ctx)
	if err != nil {
		return ctrl.Result{}, fmt.Errorf("failed to determine default ingress domain: %w", err)
	}

	// Reconcile SRE metrics config
	if err := r.reconcileSREMetricsConfig(ctx, createOrUpdate, hcp); err != nil {
		return ctrl.Result{}, fmt.Errorf("failed to reconcile SRE metrics config: %w", err)
	}

	_, err = r.reconcileOpenShiftTrustedCAs(ctx, hcp)
	if err != nil {
		return ctrl.Result{}, fmt.Errorf("failed to reconcile OpenShift trusted CAs: %w", err)
	}

	imageProvider := imageprovider.New(releaseImage)
	imageProvider.ComponentImages()["token-minter"] = utilitiesImage
	imageProvider.ComponentImages()[hyperutil.AvailabilityProberImageName] = utilitiesImage

	securityContextUID := controlplanecomponent.DefaultSecurityContextUID
	if r.SetDefaultSecurityContext {
		securityContextUID, err = strconv.ParseInt(controlPlaneNamespace.Annotations[DefaultSecurityContextUIDAnnnotation], 10, 64)
		if err != nil {
			return ctrl.Result{}, fmt.Errorf("failed to parse SecurityContext UID: %w", err)
		}
	}
	cpContext := controlplanecomponent.ControlPlaneContext{
		Context:                   ctx,
		Client:                    r.Client,
		ApplyProvider:             upsert.NewApplyProvider(r.EnableCIDebugOutput),
		HCP:                       hcp,
		SetDefaultSecurityContext: r.SetDefaultSecurityContext,
		DefaultSecurityContextUID: securityContextUID,
		EnableCIDebugOutput:       r.EnableCIDebugOutput,
		MetricsSet:                r.MetricsSet,
		ReleaseImageProvider:      imageProvider,
		OmitOwnerReference:        true,
	}

	// Reconcile the control plane operator
	err = r.reconcileControlPlaneOperator(cpContext, createOrUpdate, hcluster, controlPlaneOperatorImage, utilitiesImage, defaultIngressDomain, cpoHasUtilities, r.CertRotationScale, releaseImageVersion, releaseProvider)
	if err != nil {
		return ctrl.Result{}, fmt.Errorf("failed to reconcile control plane operator: %w", err)
	}

	// Reconcile the CAPI manager components
	err = r.reconcileCAPIManager(cpContext, createOrUpdate, hcluster)
	if err != nil {
		return ctrl.Result{}, fmt.Errorf("failed to reconcile capi manager: %w", err)
	}

	// Reconcile the CAPI provider components
	if err = r.reconcileCAPIProvider(cpContext, hcluster, hcp, p); err != nil {
		return ctrl.Result{}, fmt.Errorf("failed to reconcile capi provider: %w", err)
	}

	if _, pkiDisabled := hcp.Annotations[hyperv1.DisablePKIReconciliationAnnotation]; controlPlanePKIOperatorSignsCSRs && !pkiDisabled {
		// Reconcile the control plane PKI operator RBAC - the CPO does not have rights to do this itself
		err = r.reconcileControlPlanePKIOperatorRBAC(ctx, createOrUpdate, hcluster)
		if err != nil {
			return ctrl.Result{}, fmt.Errorf("failed to reconcile control plane PKI operator RBAC: %w", err)
		}
	}

	// Reconcile the network policies
	if err = r.reconcileNetworkPolicies(ctx, log, createOrUpdate, hcluster, hcp, releaseImageVersion, controlPlaneOperatorAppliesManagementKASNetworkPolicyLabel); err != nil {
		return ctrl.Result{}, fmt.Errorf("failed to reconcile network policies: %w", err)
	}

	// Reconcile platform specific items
	switch hcluster.Spec.Platform.Type {
	case hyperv1.KubevirtPlatform:
		err = r.reconcileKubevirtCSIClusterRBAC(ctx, createOrUpdate, hcluster)
		if err != nil {
			return ctrl.Result{}, fmt.Errorf("failed to reconcile kubevirt CSI cluster wide RBAC: %w", err)
		}
	case hyperv1.AWSPlatform:
		// Reconcile the AWS OIDC discovery
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
	case hyperv1.AzurePlatform:
		if azureutil.IsAroHCP() {
			// Reconcile CPO SecretProviderClass CR
			cpoSecretProviderClass := cpomanifests.ManagedAzureSecretProviderClass(config.ManagedAzureCPOSecretProviderClassName, hcp.Namespace)
			if _, err = createOrUpdate(ctx, r, cpoSecretProviderClass, func() error {
				secretproviderclass.ReconcileManagedAzureSecretProviderClass(cpoSecretProviderClass, hcp, hcp.Spec.Platform.Azure.AzureAuthenticationConfig.ManagedIdentities.ControlPlane.ControlPlaneOperator)
				return nil
			}); err != nil {
				return ctrl.Result{}, fmt.Errorf("failed to reconcile control plane operator secret provider class: %w", err)
			}

			// Reconcile CAPZ SecretProviderClass CR
			nodepoolMgmtSecretProviderClass := cpomanifests.ManagedAzureSecretProviderClass(config.ManagedAzureNodePoolMgmtSecretProviderClassName, hcp.Namespace)
			if _, err = createOrUpdate(ctx, r, nodepoolMgmtSecretProviderClass, func() error {
				secretproviderclass.ReconcileManagedAzureSecretProviderClass(nodepoolMgmtSecretProviderClass, hcp, hcp.Spec.Platform.Azure.AzureAuthenticationConfig.ManagedIdentities.ControlPlane.NodePoolManagement)
				return nil
			}); err != nil {
				return ctrl.Result{}, fmt.Errorf("failed to reconcile nodepool management secret provider class: %w", err)
			}
		}

		if hcluster.Spec.SecretEncryption != nil && hcluster.Spec.SecretEncryption.KMS != nil {
			if azureutil.IsAroHCP() {
				// Reconcile KMS SecretProviderClass CR
				kmsSecretProviderClass := cpomanifests.ManagedAzureSecretProviderClass(config.ManagedAzureKMSSecretProviderClassName, hcp.Namespace)
				if _, err := createOrUpdate(ctx, r, kmsSecretProviderClass, func() error {
					secretproviderclass.ReconcileManagedAzureSecretProviderClass(kmsSecretProviderClass, hcp, hcp.Spec.SecretEncryption.KMS.Azure.KMS)
					return nil
				}); err != nil {
					return ctrl.Result{}, fmt.Errorf("failed to reconcile KMS SecretProviderClass: %w", err)
				}
			}
		}
	}

	if err := r.reconcileKarpenterOperator(cpContext, createOrUpdate, hcluster, r.HypershiftOperatorImage, controlPlaneOperatorImage); err != nil {
		return ctrl.Result{}, fmt.Errorf("failed to reconcile karpenter operator: %w", err)
	}

	log.Info("successfully reconciled")
	result := ctrl.Result{}
	if requeueAfter != nil {
		result.RequeueAfter = *requeueAfter
	}
	return result, nil
}

const (
	ControlPlaneNamespaceLabelKey = "hypershift.openshift.io/hosted-control-plane"
)

// annotationsForCertRenewal returns a set of annotations to set based on the current state of the KAS
// serving certificate. These could include the annotation to restart control plane pods.
// It will always return an empty map whenever the HostedCluster should not be checked for
// stale certs (see shouldCheckForStaleCerts)
func annotationsForCertRenewal(
	log logr.Logger,
	hcp *hyperv1.HostedControlPlane,
	shouldCheckForStaleCerts func() bool,
	kasServingCertHashFromSecret func() (string, error),
	kasServingCertHashFromEndpoint func() (string, error),
) func() (map[string]string, error) {
	return func() (map[string]string, error) {
		if !shouldCheckForStaleCerts() {
			return nil, nil
		}

		// The hash from the KAS serving secret is the source of truth for what
		// certificate we should be using for KAS
		// We can check whether we need to restart pods in one of 2 ways:
		// 1. We have previously saved an annotation on the HCP that says which was the last
		//    observed certificate hash. If they don't match, we need to restart.
		// 2. We have not previously saved an annotation, and therefore we make a request
		//    to the KAS endpoint to determine if the current secret matches what the KAS
		//    pod is using as its serving cert. We then save the observed secret hash as an
		//    annotation for next time. If they also don't match, we add the annotation to restart.
		hashFromSecret, err := kasServingCertHashFromSecret()
		if err != nil {
			return nil, err
		}

		// The simplest check is to see if the hash of the KAS serving cert secret has changed
		// since we last saw it.
		if annotationServingCertHash := hcp.Annotations[kasServingCertHashAnnotation]; annotationServingCertHash != "" {
			if annotationServingCertHash != hashFromSecret {
				log.Info("WARNING: A change in the KAS server certificate hash detected. Setting the annotation to restart control plane workloads.")
				return map[string]string{
					kasServingCertHashAnnotation:  hashFromSecret,
					hyperv1.RestartDateAnnotation: fmt.Sprintf("CertHash:%s", hashFromSecret),
				}, nil
			}
			return nil, nil
		}

		// If we've never stored the kasServingCertHash, check the actual serving cert hash from the endpoint
		// against the one from the secret
		hashFromServerEndpoint, err := kasServingCertHashFromEndpoint()
		if err != nil {
			return nil, err
		}
		// If they match, we still need to store the hash for future comparisons
		if hashFromServerEndpoint == hashFromSecret {
			return map[string]string{
				kasServingCertHashAnnotation: hashFromSecret,
			}, nil
		}
		// If they don't match, we store the hash AND initiate a restart
		log.Info("WARNING: The KAS endpoint server certificate does not match the serving certificate secret. Setting the annotation to restart control plane workloads.")
		return map[string]string{
			kasServingCertHashAnnotation:  hashFromSecret,
			hyperv1.RestartDateAnnotation: fmt.Sprintf("CertHash:%s", hashFromSecret),
		}, nil
	}
}

func kasHostAndPortFromHCP(hcp *hyperv1.HostedControlPlane) string {
	// NOTE: On IBM Cloud platform, the service port is different than the default. However, in that platform
	// PKI is not managed by the CPO, therefore this check would never run. For simplicity, we only use the default
	// port here.
	return fmt.Sprintf("kube-apiserver.%s.svc:%d", hcp.Namespace, config.KASSVCPort)
}

func (r *HostedClusterReconciler) kasServingCertHashFromSecret(ctx context.Context, hcp *hyperv1.HostedControlPlane) func() (string, error) {
	return func() (string, error) {
		servingCertSecret := manifests.KASServingCertSecret(hcp.Namespace)
		if err := r.Get(ctx, client.ObjectKeyFromObject(servingCertSecret), servingCertSecret); err != nil {
			return "", err
		}
		if value := servingCertSecret.Data[corev1.TLSCertKey]; len(value) == 0 {
			return "", fmt.Errorf("no value for KAS serving certificate in %s", client.ObjectKeyFromObject(servingCertSecret).String())
		} else {
			return hyperutil.HashSimple(value), nil
		}
	}
}

func (r *HostedClusterReconciler) kasServingCertHashFromEndpoint(kasHostAndPort string) func() (string, error) {
	return func() (string, error) {
		conn, err := tls.Dial("tcp", kasHostAndPort, &tls.Config{
			InsecureSkipVerify: true,
			ServerName:         "kubernetes",
		})
		if err != nil {
			return "", fmt.Errorf("failed to dial %s: %w", kasHostAndPort, err)
		}
		defer conn.Close()
		kasCerts := conn.ConnectionState().PeerCertificates
		if len(kasCerts) == 0 {
			return "", fmt.Errorf("no certificate found on KAS endpoint %s", kasHostAndPort)
		}
		pemBytes := certs.CertToPem(kasCerts[0])
		return hyperutil.HashSimple(pemBytes), nil
	}
}

// shouldCheckForStaleCerts returns true if a HostedCluster should be checked for stale certs
// The following pre-conditions must be met:
// - The HostedCluster must be managing its own PKI (the hyperv1.DisablePKIReconciliationAnnotation is not present)
// - The HostedCluster has been available once
// - The cluster is using ControlPlaneOperator v2
func shouldCheckForStaleCerts(hc *hyperv1.HostedCluster, defaultingToControlPlaneV2 bool) func() bool {
	return func() bool {
		if hc.Annotations[hcmetrics.HasBeenAvailableAnnotation] != "true" {
			return false
		}
		if hc.Annotations[hyperv1.DisablePKIReconciliationAnnotation] == "true" {
			return false
		}
		if defaultingToControlPlaneV2 {
			return false
		}
		return true
	}
}

func reconcileHostedControlPlaneAnnotations(hcp *hyperv1.HostedControlPlane, hcluster *hyperv1.HostedCluster, isAutoscalingNeeded bool, certRenewalAnnotations func() (map[string]string, error)) error {
	if hcp.Annotations == nil {
		hcp.Annotations = map[string]string{}
	}

	hcp.Annotations[hyperutil.HostedClusterAnnotation] = client.ObjectKeyFromObject(hcluster).String()

	// These annotations are copied from the HostedCluster
	mirroredAnnotations := []string{
		hyperv1.DisablePKIReconciliationAnnotation,
		hyperv1.OauthLoginURLOverrideAnnotation,
		hyperv1.KonnectivityAgentImageAnnotation,
		hyperv1.KonnectivityServerImageAnnotation,
		hyperv1.ClusterAutoscalerImage,
		hyperv1.IBMCloudKMSProviderImage,
		hyperv1.AWSKMSProviderImage,
		hyperv1.PortierisImageAnnotation,
		hyperutil.DebugDeploymentsAnnotation,
		hyperv1.DisableProfilingAnnotation,
		hyperv1.PrivateIngressControllerAnnotation,
		hyperv1.IngressControllerLoadBalancerScope,
		hyperv1.CleanupCloudResourcesAnnotation,
		hyperv1.ControlPlanePriorityClass,
		hyperv1.APICriticalPriorityClass,
		hyperv1.EtcdPriorityClass,
		hyperv1.EnsureExistsPullSecretReconciliation,
		hyperv1.TopologyAnnotation,
		hyperv1.DisableMachineManagement,
		hyperv1.CertifiedOperatorsCatalogImageAnnotation,
		hyperv1.CommunityOperatorsCatalogImageAnnotation,
		hyperv1.RedHatMarketplaceCatalogImageAnnotation,
		hyperv1.RedHatOperatorsCatalogImageAnnotation,
		hyperv1.OLMCatalogsISRegistryOverridesAnnotation,
		hyperv1.KubeAPIServerGOGCAnnotation,
		hyperv1.KubeAPIServerGOMemoryLimitAnnotation,
		hyperv1.RequestServingNodeAdditionalSelectorAnnotation,
		hyperv1.AWSLoadBalancerSubnetsAnnotation,
		hyperv1.AWSLoadBalancerTargetNodesAnnotation,
		hyperv1.ManagementPlatformAnnotation,
		hyperv1.KubeAPIServerVerbosityLevelAnnotation,
		hyperv1.KubeAPIServerMaximumRequestsInFlight,
		hyperv1.KubeAPIServerMaximumMutatingRequestsInFlight,
		hyperv1.DisableIgnitionServerAnnotation,
		hyperv1.AWSMachinePublicIPs,
		hyperkarpenterv1.KarpenterProviderAWSImage,
		hyperv1.KubeAPIServerGoAwayChance,
		hyperv1.HostedClusterRestoredFromBackupAnnotation,
	}
	for _, key := range mirroredAnnotations {
		val, hasVal := hcluster.Annotations[key]
		if hasVal {
			hcp.Annotations[key] = val
		} else {
			delete(hcp.Annotations, key)
		}
	}

	prefixesToSync := []string{
		hyperv1.IdentityProviderOverridesAnnotationPrefix,
		hyperv1.ResourceRequestOverrideAnnotationPrefix,
	}

	// All annotations on the HostedCluster with prefixes to sync
	// should be synced
	for key := range hcp.Annotations {
		for _, prefix := range prefixesToSync {
			if strings.HasPrefix(key, prefix) {
				delete(hcp.Annotations, key)
			}
		}
	}
	for key, val := range hcluster.Annotations {
		for _, prefix := range prefixesToSync {
			if strings.HasPrefix(key, prefix) {
				hcp.Annotations[key] = val
			}
		}
	}

	// Only set the restart date annotation if it has not been previously synced.
	// This allows other values to be set on the HCP restart date annotation (for cert rotation) without
	// causing a constant reconcile of the annotation value set on the HostedCluster.
	if hcluster.Annotations[hyperv1.RestartDateAnnotation] != "" &&
		hcp.Annotations[previouslySyncedRestartDateAnnotation] != hcluster.Annotations[hyperv1.RestartDateAnnotation] {
		hcp.Annotations[previouslySyncedRestartDateAnnotation] = hcluster.Annotations[hyperv1.RestartDateAnnotation]
		hcp.Annotations[hyperv1.RestartDateAnnotation] = hcluster.Annotations[hyperv1.RestartDateAnnotation]
	}

	// Determine which certRenewalAnnotations to set based on the current state of the KAS serving certificate.
	// These could include an override of the RestartDateAnnotation
	certAnnotations, err := certRenewalAnnotations()
	if err != nil {
		return err
	}
	maps.Copy(hcp.Annotations, certAnnotations)

	// Set the DisableClusterAutoscalerAnnotation if autoscaling is not needed
	if !isAutoscalingNeeded {
		hcp.Annotations[hyperv1.DisableClusterAutoscalerAnnotation] = "true"
	} else {
		delete(hcp.Annotations, hyperv1.DisableClusterAutoscalerAnnotation)
	}

	return nil
}

// reconcileHostedControlPlane reconciles the given HostedControlPlane, which
// will be mutated.
func reconcileHostedControlPlane(hcp *hyperv1.HostedControlPlane, hcluster *hyperv1.HostedCluster, isAutoscalingNeeded bool, certRenewalAnnotations func() (map[string]string, error)) error {
	if err := reconcileHostedControlPlaneAnnotations(hcp, hcluster, isAutoscalingNeeded, certRenewalAnnotations); err != nil {
		return err
	}

	if hcp.Labels == nil {
		hcp.Labels = make(map[string]string)
	}
	// All labels on the HostedCluster with this special prefix are copied
	// Those are labels set by OCM
	for key, val := range hcluster.Labels {
		if strings.HasPrefix(key, "api.openshift.com") {
			hcp.Labels[key] = val
		}
	}

	hcp.Spec.UpdateService = hcluster.Spec.UpdateService
	hcp.Spec.Channel = hcluster.Spec.Channel
	hcp.Spec.ReleaseImage = hcluster.Spec.Release.Image
	if hcluster.Spec.ControlPlaneRelease != nil {
		hcp.Spec.ControlPlaneReleaseImage = &hcluster.Spec.ControlPlaneRelease.Image
	} else {
		hcp.Spec.ControlPlaneReleaseImage = nil
	}

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
	} else {
		hcp.Spec.AdditionalTrustBundle = nil
	}
	if hcluster.Spec.SecretEncryption != nil {
		hcp.Spec.SecretEncryption = hcluster.Spec.SecretEncryption.DeepCopy()
	}

	hcp.Spec.KubeAPIServerDNSName = hcluster.Spec.KubeAPIServerDNSName
	hcp.Spec.PausedUntil = hcluster.Spec.PausedUntil
	hcp.Spec.OLMCatalogPlacement = hcluster.Spec.OLMCatalogPlacement
	hcp.Spec.Autoscaling = hcluster.Spec.Autoscaling
	hcp.Spec.AutoNode = hcluster.Spec.AutoNode
	hcp.Spec.NodeSelector = hcluster.Spec.NodeSelector
	hcp.Spec.Tolerations = hcluster.Spec.Tolerations
	hcp.Spec.Labels = hcluster.Spec.Labels
	hcp.Spec.ImageContentSources = hcluster.Spec.ImageContentSources

	// Pass through Platform spec.
	hcp.Spec.Platform = *hcluster.Spec.Platform.DeepCopy()
	switch hcluster.Spec.Platform.Type {
	case hyperv1.AgentPlatform:
		// Agent platform uses None platform for the hcp.
		hcp.Spec.Platform.Type = hyperv1.NonePlatform
	}

	if hcluster.Spec.Configuration != nil {
		hcp.Spec.Configuration = hcluster.Spec.Configuration.DeepCopy()
	} else {
		hcp.Spec.Configuration = nil
	}

	hcp.Spec.Capabilities = hcluster.Spec.Capabilities

	if hcluster.Spec.OperatorConfiguration != nil {
		hcp.Spec.OperatorConfiguration = hcluster.Spec.OperatorConfiguration.DeepCopy()
	} else {
		hcp.Spec.OperatorConfiguration = nil
	}

	return nil
}

// reconcileCAPIManager orchestrates orchestrates of  all CAPI manager components.
func (r *HostedClusterReconciler) reconcileCAPIManager(cpContext controlplanecomponent.ControlPlaneContext, createOrUpdate upsert.CreateOrUpdateFN, hcluster *hyperv1.HostedCluster) error {
	controlPlaneNamespace := manifests.HostedControlPlaneNamespaceObject(hcluster.Namespace, hcluster.Name)
	err := r.Client.Get(cpContext, client.ObjectKeyFromObject(controlPlaneNamespace), controlPlaneNamespace)
	if err != nil {
		return fmt.Errorf("failed to get control plane namespace: %w", err)
	}

	capiManagerServiceAccount := clusterapi.CAPIManagerServiceAccount(controlPlaneNamespace.Name)

	// Reconcile CAPI manager cluster role
	capiManagerClusterRole := clusterapi.CAPIManagerClusterRole(controlPlaneNamespace.Name)
	_, err = createOrUpdate(cpContext, r.Client, capiManagerClusterRole, func() error {
		return reconcileCAPIManagerClusterRole(capiManagerClusterRole)
	})
	if err != nil {
		return fmt.Errorf("failed to reconcile capi manager cluster role: %w", err)
	}

	// Reconcile CAPI manager cluster role binding
	capiManagerClusterRoleBinding := clusterapi.CAPIManagerClusterRoleBinding(controlPlaneNamespace.Name)
	_, err = createOrUpdate(cpContext, r.Client, capiManagerClusterRoleBinding, func() error {
		return reconcileCAPIManagerClusterRoleBinding(capiManagerClusterRoleBinding, capiManagerClusterRole, capiManagerServiceAccount)
	})
	if err != nil {
		return fmt.Errorf("failed to reconcile capi manager cluster role binding: %w", err)
	}

	imageOverride := hcluster.Annotations[hyperv1.ClusterAPIManagerImage]
	capiManager := capimanagerv2.NewComponent(imageOverride)
	if err := capiManager.Reconcile(cpContext); err != nil {
		return fmt.Errorf("failed to reconcile capi manager component: %w", err)
	}

	return nil
}

// reconcileCAPIProvider orchestrates reconciliation of the CAPI provider
// components for a given platform.
func (r *HostedClusterReconciler) reconcileCAPIProvider(cpContext controlplanecomponent.ControlPlaneContext, hcluster *hyperv1.HostedCluster, hcp *hyperv1.HostedControlPlane, p platform.Platform,
) error {
	capiProviderDeploymentSpec, err := p.CAPIProviderDeploymentSpec(hcluster, hcp)
	if err != nil {
		return fmt.Errorf("failed to get capi provider deployment spec: %w", err)
	}

	if capiProviderDeploymentSpec == nil {
		// If there's no capiProviderDeploymentSpec implementation return early.
		return nil
	}

	// Fix: Remove existing CAPI provider deployment if it contains outdated labels
	controlPlaneNamespace := manifests.HostedControlPlaneNamespaceObject(hcluster.Namespace, hcluster.Name)
	capiProviderDeployment := &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "capi-provider",
			Namespace: controlPlaneNamespace.Name,
		},
	}
	err = cpContext.Client.Get(cpContext, client.ObjectKeyFromObject(capiProviderDeployment), capiProviderDeployment)
	if err != nil {
		if !apierrors.IsNotFound(err) {
			return fmt.Errorf("failed to fetch capi provider deployment: %w", err)
		}
	}
	if err == nil {
		if capiProviderDeployment.Spec.Template.ObjectMeta.Labels["hypershift.openshift.io/control-plane-component"] != "capi-provider" {
			_, err = hyperutil.DeleteIfNeeded(cpContext, cpContext.Client, capiProviderDeployment)
			// Always return an error so we can retry when the cache is updated
			return fmt.Errorf("provider with outdated labels exists, delete result: %w", err)
		}
	}

	capi := capiproviderv2.NewComponent(capiProviderDeploymentSpec, p.CAPIProviderPolicyRules())
	if err := capi.Reconcile(cpContext); err != nil {
		return fmt.Errorf("failed to reconcile capi provider component: %w", err)
	}

	return nil
}

// reconcileControlPlaneOperator orchestrates reconciliation of the control plane
// operator components.
func (r *HostedClusterReconciler) reconcileControlPlaneOperator(cpContext controlplanecomponent.ControlPlaneContext, createOrUpdate upsert.CreateOrUpdateFN, hcluster *hyperv1.HostedCluster, controlPlaneOperatorImage, utilitiesImage, defaultIngressDomain string, cpoHasUtilities bool, certRotationScale time.Duration, releaseVersion semver.Version, releaseProvider releaseinfo.ProviderWithOpenShiftImageRegistryOverrides) error {
	controlPlaneNamespace := manifests.HostedControlPlaneNamespaceObject(hcluster.Namespace, hcluster.Name)
	err := r.Client.Get(cpContext, client.ObjectKeyFromObject(controlPlaneNamespace), controlPlaneNamespace)
	if err != nil {
		return fmt.Errorf("failed to get control plane namespace: %w", err)
	}

	// TODO: Remove this block after initial merge of this feature. It is not needed for latest CPO version
	if r.ManagementClusterCapabilities.Has(capabilities.CapabilityRoute) && releaseVersion.Major == 4 && releaseVersion.Minor <= 14 {
		controlPlaneOperatorServiceAccount := controlplaneoperator.OperatorServiceAccount(controlPlaneNamespace.Name)

		// Reconcile operator role - for ingress
		controlPlaneOperatorIngressRole := controlplaneoperator.OperatorIngressRole("openshift-ingress", controlPlaneNamespace.Name)
		_, err = createOrUpdate(cpContext, r.Client, controlPlaneOperatorIngressRole, func() error {
			return reconcileControlPlaneOperatorIngressRole(controlPlaneOperatorIngressRole)
		})
		if err != nil {
			return fmt.Errorf("failed to reconcile controlplane operator ingress role: %w", err)
		}

		// Reconcile operator role binding - for ingress
		controlPlaneOperatorIngressRoleBinding := controlplaneoperator.OperatorIngressRoleBinding("openshift-ingress", controlPlaneNamespace.Name)
		_, err = createOrUpdate(cpContext, r.Client, controlPlaneOperatorIngressRoleBinding, func() error {
			return reconcileControlPlaneOperatorIngressRoleBinding(controlPlaneOperatorIngressRoleBinding, controlPlaneOperatorIngressRole, controlPlaneOperatorServiceAccount)
		})
		if err != nil {
			return fmt.Errorf("failed to reconcile controlplane operator ingress rolebinding: %w", err)
		}

		// Reconcile operator role - for ingress operator
		controlPlaneOperatorIngressOperatorRole := controlplaneoperator.OperatorIngressOperatorRole("openshift-ingress-operator", controlPlaneNamespace.Name)
		_, err = createOrUpdate(cpContext, r.Client, controlPlaneOperatorIngressOperatorRole, func() error {
			return reconcilecontrolPlaneOperatorIngressOperatorRole(controlPlaneOperatorIngressOperatorRole)
		})
		if err != nil {
			return fmt.Errorf("failed to reconcile controlplane operator ingress operator role: %w", err)
		}

		// Reconcile operator role binding - for ingress operator
		controlPlaneOperatorIngressOperatorRoleBinding := controlplaneoperator.OperatorIngressOperatorRoleBinding("openshift-ingress-operator", controlPlaneNamespace.Name)
		_, err = createOrUpdate(cpContext, r.Client, controlPlaneOperatorIngressOperatorRoleBinding, func() error {
			return reconcilecontrolPlaneOperatorIngressOperatorRoleBinding(controlPlaneOperatorIngressOperatorRoleBinding, controlPlaneOperatorIngressOperatorRole, controlPlaneOperatorServiceAccount)
		})
		if err != nil {
			return fmt.Errorf("failed to reconcile controlplane operator ingress operator rolebinding: %w", err)
		}
	}

	// Reconcile ControlPlaneOperator deployment and resources.
	cpo := cpov2.NewComponent(&cpov2.ControlPlaneOperatorOptions{
		HostedCluster:               hcluster,
		Image:                       controlPlaneOperatorImage,
		UtilitiesImage:              utilitiesImage,
		HasUtilities:                cpoHasUtilities,
		CertRotationScale:           certRotationScale,
		RegistryOverrideCommandLine: hyperutil.ConvertRegistryOverridesToCommandLineFlag(releaseProvider.GetRegistryOverrides()),
		OpenShiftRegistryOverrides:  hyperutil.ConvertOpenShiftImageRegistryOverridesToCommandLineFlag(releaseProvider.GetOpenShiftImageRegistryOverrides()),
		DefaultIngressDomain:        defaultIngressDomain,
		FeatureSet:                  r.FeatureSet,
	})

	if err := cpo.Reconcile(cpContext); err != nil {
		return fmt.Errorf("failed to reconcile controlplane operator component: %w", err)
	}

	return nil
}

func (r *HostedClusterReconciler) reconcileControlPlanePKIOperatorRBAC(ctx context.Context, createOrUpdate upsert.CreateOrUpdateFN, hcluster *hyperv1.HostedCluster) error {
	// We don't create this ServiceAccount, the CPO does, but we can reference it in RBAC before it's created as the system is eventually consistent
	serviceAccount := cpomanifests.PKIOperatorServiceAccount(manifests.HostedControlPlaneNamespace(hcluster.Namespace, hcluster.Name))

	// Reconcile controlplane PKI operator CSR approver cluster role
	controlPlanePKIOperatorCSRApproverClusterRole := controlplanepkioperatormanifests.CSRApproverClusterRole(hcluster)
	_, err := createOrUpdate(ctx, r.Client, controlPlanePKIOperatorCSRApproverClusterRole, func() error {
		return controlplanepkioperatormanifests.ReconcileCSRApproverClusterRole(controlPlanePKIOperatorCSRApproverClusterRole, hcluster, certificates.CustomerBreakGlassSigner, certificates.SREBreakGlassSigner)
	})
	if err != nil {
		return fmt.Errorf("failed to reconcile controlplane PKI operator CSR approver cluster role: %w", err)
	}

	// Reconcile controlplane PKI operator CSR approver cluster role binding
	controlPlanePKIOperatorCSRApproverClusterRoleBinding := controlplanepkioperatormanifests.ClusterRoleBinding(hcluster, controlPlanePKIOperatorCSRApproverClusterRole)
	_, err = createOrUpdate(ctx, r.Client, controlPlanePKIOperatorCSRApproverClusterRoleBinding, func() error {
		return controlplanepkioperatormanifests.ReconcileClusterRoleBinding(controlPlanePKIOperatorCSRApproverClusterRoleBinding, controlPlanePKIOperatorCSRApproverClusterRole, serviceAccount)
	})
	if err != nil {
		return fmt.Errorf("failed to reconcile controlplane PKI operator CSR approver cluster role binding: %w", err)
	}

	// Reconcile controlplane PKI operator CSR signer cluster role
	controlPlanePKIOperatorCSRSignerClusterRole := controlplanepkioperatormanifests.CSRSignerClusterRole(hcluster)
	_, err = createOrUpdate(ctx, r.Client, controlPlanePKIOperatorCSRSignerClusterRole, func() error {
		return controlplanepkioperatormanifests.ReconcileCSRSignerClusterRole(controlPlanePKIOperatorCSRSignerClusterRole, hcluster, certificates.CustomerBreakGlassSigner, certificates.SREBreakGlassSigner)
	})
	if err != nil {
		return fmt.Errorf("failed to reconcile controlplane PKI operator CSR signer cluster role: %w", err)
	}

	// Reconcile controlplane PKI operator CSR signer cluster role binding
	controlPlanePKIOperatorCSRSignerClusterRoleBinding := controlplanepkioperatormanifests.ClusterRoleBinding(hcluster, controlPlanePKIOperatorCSRSignerClusterRole)
	_, err = createOrUpdate(ctx, r.Client, controlPlanePKIOperatorCSRSignerClusterRoleBinding, func() error {
		return controlplanepkioperatormanifests.ReconcileClusterRoleBinding(controlPlanePKIOperatorCSRSignerClusterRoleBinding, controlPlanePKIOperatorCSRSignerClusterRole, serviceAccount)
	})
	if err != nil {
		return fmt.Errorf("failed to reconcile controlplane PKI operator CSR signer cluster role binding: %w", err)
	}

	return nil
}

func (r *HostedClusterReconciler) reconcileKubevirtCSIClusterRBAC(ctx context.Context, createOrUpdate upsert.CreateOrUpdateFN, hcluster *hyperv1.HostedCluster) error {
	// We don't create this ServiceAccount, it's part of the kubevirt CSI manifests, but we can reference it due to eventual consistency
	hcpns := manifests.HostedControlPlaneNamespace(hcluster.Namespace, hcluster.Name)
	serviceAccount := cpomanifests.KubevirtCSIDriverInfraSA(hcpns)

	kubevirtCSIClusterRole := &rbacv1.ClusterRole{
		ObjectMeta: metav1.ObjectMeta{
			Name: fmt.Sprintf("%s-kubevirt-csi-cluster", hcpns),
			Labels: map[string]string{
				controlplanepkioperatormanifests.OwningHostedClusterNamespaceLabel: hcluster.Namespace,
				controlplanepkioperatormanifests.OwningHostedClusterNameLabel:      hcluster.Name,
			},
		},
	}
	_, err := createOrUpdate(ctx, r.Client, kubevirtCSIClusterRole, func() error {
		kubevirtCSIClusterRole.Rules = []rbacv1.PolicyRule{
			{
				APIGroups: []string{""},
				Resources: []string{"persistentvolumes"},
				Verbs:     []string{"get"},
			},
		}
		return nil
	})
	if err != nil {
		return fmt.Errorf("failed to reconcile kubevirt CSI cluster role: %w", err)
	}

	kubevirtCSIClusterRoleBinding := &rbacv1.ClusterRoleBinding{
		ObjectMeta: metav1.ObjectMeta{
			Name: kubevirtCSIClusterRole.Name,
			Labels: map[string]string{
				controlplanepkioperatormanifests.OwningHostedClusterNamespaceLabel: hcluster.Namespace,
				controlplanepkioperatormanifests.OwningHostedClusterNameLabel:      hcluster.Name,
			},
		},
	}
	_, err = createOrUpdate(ctx, r.Client, kubevirtCSIClusterRoleBinding, func() error {
		kubevirtCSIClusterRoleBinding.RoleRef = rbacv1.RoleRef{
			APIGroup: "rbac.authorization.k8s.io",
			Kind:     "ClusterRole",
			Name:     kubevirtCSIClusterRole.Name,
		}
		kubevirtCSIClusterRoleBinding.Subjects = []rbacv1.Subject{
			{
				Kind:      "ServiceAccount",
				Name:      serviceAccount.Name,
				Namespace: serviceAccount.Namespace,
			},
		}
		return nil
	})
	if err != nil {
		return fmt.Errorf("failed to reconcile kubevirt CSI cluster role binding: %w", err)
	}

	return nil
}

// reconcileOpenShiftTrustedCAs checks for the existence of /etc/pki/ca-trust/extracted/pem/tls-ca-bundle.pem, if it exists,
// creates a new ConfigMap to be mounted in the CPO deployment utilizing the file
func (r *HostedClusterReconciler) reconcileOpenShiftTrustedCAs(ctx context.Context, hostedControlPlane *hyperv1.HostedControlPlane) (bool, error) {
	trustedCABundle := new(bytes.Buffer)
	var trustCABundleFile []byte

	_, err := os.Stat(r.OpenShiftTrustedCAFilePath)
	if err == nil {
		trustCABundleFile, err = os.ReadFile(r.OpenShiftTrustedCAFilePath)
		if err != nil {
			return false, fmt.Errorf("unable to read trust bundle file: %w", err)
		}
	}
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return false, nil
		}

		return false, err
	}

	if _, err = trustedCABundle.Write(trustCABundleFile); err != nil {
		return false, fmt.Errorf("unable to write trust bundle to buffer: %w", err)
	}

	// Next, save the contents to a new ConfigMap in the hosted control plane's namespace
	openShiftTrustedCABundleConfigMapForCPO := manifests.OpenShiftTrustedCABundleForNamespace(hostedControlPlane.Namespace)
	openShiftTrustedCABundleConfigMapForCPO.Data["ca-bundle.crt"] = trustedCABundle.String()
	if _, err = controllerutil.CreateOrUpdate(ctx, r.Client, openShiftTrustedCABundleConfigMapForCPO, NoopReconcile); err != nil {
		return false, fmt.Errorf("failed to create openshift-config-managed-trusted-ca-bundle for CPO deployment %T: %w", trustedCABundle.String(), err)
	}

	return true, nil
}

func servicePublishingStrategyByType(hcp *hyperv1.HostedCluster, svcType hyperv1.ServiceType) *hyperv1.ServicePublishingStrategy {
	for _, mapping := range hcp.Spec.Services {
		if mapping.Service == svcType {
			return &mapping.ServicePublishingStrategy
		}
	}
	return nil
}

// reconcileCLISecrets makes sure the secrets that were created by the cli, and are safe to be deleted with the
// hosted cluster, has an owner reference of the hosted cluster.
func (r *HostedClusterReconciler) reconcileCLISecrets(ctx context.Context, createOrUpdate upsert.CreateOrUpdateFN, hcluster *hyperv1.HostedCluster) error {
	log := ctrl.LoggerFrom(ctx)
	secrets := &corev1.SecretList{}
	err := r.List(ctx, secrets, client.InNamespace(hcluster.Namespace), client.MatchingLabels{
		util.DeleteWithClusterLabelName: "true",
		util.AutoInfraLabelName:         hcluster.Spec.InfraID,
	})
	if err != nil {
		return fmt.Errorf("failed to retrieve cli created secrets: %v", err)
	}

	ownerRef := config.OwnerRefFrom(hcluster)
	for _, secret := range secrets.Items {
		res, err := createOrUpdate(ctx, r.Client, &secret, func() error {
			ownerRef.ApplyTo(&secret)
			return nil
		})
		if err != nil {
			return fmt.Errorf("failed to set '%s' secret's owner reference: %v", secret.Name, err)
		}
		if res == controllerutil.OperationResultUpdated {
			log.Info("added owner reference of the Hosted cluster, to the secret", "secret", secret.Name)
		}
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
		// make sure cluster is not paused.
		cluster.Spec.Paused = false
		return nil
	}
	infraCRGVK, err := apiutil.GVKForObject(infraCR, api.Scheme)
	if err != nil {
		return fmt.Errorf("failed to get gvk for %T: %w", infraCR, err)
	}

	cluster.Annotations = map[string]string{
		hyperutil.HostedClusterAnnotation: client.ObjectKeyFromObject(hcluster).String(),
	}
	cluster.Spec = capiv1.ClusterSpec{
		ControlPlaneEndpoint: capiv1.APIEndpoint{},
		ControlPlaneRef: &corev1.ObjectReference{
			APIVersion: "hypershift.openshift.io/v1beta1",
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

func pauseCAPICluster(ctx context.Context, c client.Client, hcp *hyperv1.HostedControlPlane) error {
	if hcp == nil {
		return nil
	}

	capiCluster := controlplaneoperator.CAPICluster(hcp.Namespace, hcp.Spec.InfraID)
	err := c.Get(ctx, client.ObjectKeyFromObject(capiCluster), capiCluster)
	if err != nil {
		if !apierrors.IsNotFound(err) {
			return fmt.Errorf("failed to get CAPI Cluster: %w", err)
		}
		return nil
	}

	if !capiCluster.Spec.Paused {
		capiCluster.Spec.Paused = true
		if err := c.Update(ctx, capiCluster); err != nil {
			return fmt.Errorf("failed to update CAPI Cluster: %w", err)
		}
	}
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

// computeClusterVersionStatus determines the ClusterVersionStatus of the
// given HostedCluster and returns it.
func computeClusterVersionStatus(clock clock.WithTickerAndDelayedExecution, hcluster *hyperv1.HostedCluster, hcp *hyperv1.HostedControlPlane) *hyperv1.ClusterVersionStatus {
	if hcp != nil && hcp.Status.VersionStatus != nil {
		return hcp.Status.VersionStatus
	}

	// The following code is legacy support to preserve
	// compatibility with older HostedControlPlane controllers, which
	// may not be populating hcp.Status.VersionStatus.
	//
	// It is also used before the HostedControlPlane is created to bootstrap
	// the ClusterVersionStatus.

	releaseImage := hyperutil.HCControlPlaneReleaseImage(hcluster)

	// If there's no history, rebuild it from scratch.
	if hcluster.Status.Version == nil || len(hcluster.Status.Version.History) == 0 {
		return &hyperv1.ClusterVersionStatus{
			Desired: configv1.Release{
				Image: releaseImage,
			},
			ObservedGeneration: hcluster.Generation,
			History: []configv1.UpdateHistory{
				{
					State:       configv1.PartialUpdate,
					Image:       releaseImage,
					StartedTime: metav1.NewTime(clock.Now()),
				},
			},
		}
	}

	// Assume the previous status is still current.
	version := hcluster.Status.Version.DeepCopy()

	// If a new rollout is needed, update the desired version and prepend a new
	// partial history entry to unblock rollouts.
	if releaseImage != hcluster.Status.Version.Desired.Image {
		version.Desired.Image = releaseImage
		version.ObservedGeneration = hcluster.Generation
		// TODO: leaky
		version.History = append([]configv1.UpdateHistory{
			{
				State:       configv1.PartialUpdate,
				Image:       releaseImage,
				StartedTime: metav1.NewTime(clock.Now()),
			},
		}, version.History...)
	}

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
	//lint:ignore SA1019 consume the deprecated property until we can drop compatibility with HostedControlPlane controllers that do not populate hcp.Status.VersionStatus.
	hcpRolloutComplete := (hyperutil.HCPControlPlaneReleaseImage(hcp) == hcp.Status.ReleaseImage) && (version.Desired.Image == hcp.Status.ReleaseImage)
	if !hcpRolloutComplete {
		return version
	}

	// The rollout is complete, so update the current history entry
	version.History[0].State = configv1.CompletedUpdate
	//lint:ignore SA1019 consume the deprecated property until we can drop compatibility with HostedControlPlane controllers that do not populate hcp.Status.VersionStatus.
	version.History[0].Version = hcp.Status.Version
	//lint:ignore SA1019 consume the deprecated property until we can drop compatibility with HostedControlPlane controllers that do not populate hcp.Status.VersionStatus.
	if hcp.Status.LastReleaseImageTransitionTime != nil {
		//lint:ignore SA1019 consume the deprecated property until we can drop compatibility with HostedControlPlane controllers that do not populate hcp.Status.VersionStatus.
		version.History[0].CompletionTime = hcp.Status.LastReleaseImageTransitionTime.DeepCopy()
	}

	return version
}

// computeHostedClusterAvailability determines the Available condition for the
// given HostedCluster and returns it.
func computeHostedClusterAvailability(hcluster *hyperv1.HostedCluster, hcp *hyperv1.HostedControlPlane) metav1.Condition {
	// Determine whether the hosted control plane is available.
	hcpAvailableStatus := metav1.ConditionFalse
	hcpAvailableMessage := "Waiting for hosted control plane to be healthy"
	hcpAvailableReason := hyperv1.WaitingForAvailableReason
	var hcpAvailableCondition *metav1.Condition
	if hcp != nil {
		hcpAvailableCondition = meta.FindStatusCondition(hcp.Status.Conditions, string(hyperv1.HostedControlPlaneAvailable))
	}
	if hcpAvailableCondition != nil {
		hcpAvailableStatus = hcpAvailableCondition.Status
		hcpAvailableMessage = hcpAvailableCondition.Message
		hcpAvailableReason = hcpAvailableCondition.Reason
		if hcpAvailableStatus == metav1.ConditionTrue {
			hcpAvailableReason = hyperv1.AsExpectedReason
			hcpAvailableMessage = "The hosted control plane is available"
		}
	} else {
		// This catches and bubbles up validation errors that prevent the HCP from being created.
		hcProgressingCondition := meta.FindStatusCondition(hcluster.Status.Conditions, string(hyperv1.HostedClusterProgressing))
		if hcProgressingCondition != nil && hcProgressingCondition.Reason == hyperv1.BlockedReason {
			hcpAvailableMessage = hcProgressingCondition.Message
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

func computeAWSEndpointServiceCondition(awsEndpointServiceList hyperv1.AWSEndpointServiceList, conditionType hyperv1.ConditionType) metav1.Condition {
	var messages []string
	var conditions []metav1.Condition

	for _, awsEndpoint := range awsEndpointServiceList.Items {
		condition := meta.FindStatusCondition(awsEndpoint.Status.Conditions, string(conditionType))
		if condition != nil {
			conditions = append(conditions, *condition)

			if condition.Status == metav1.ConditionFalse {
				messages = append(messages, condition.Message)
			}
		}
	}

	if len(conditions) == 0 {
		return metav1.Condition{
			Type:    string(conditionType),
			Status:  metav1.ConditionUnknown,
			Reason:  hyperv1.StatusUnknownReason,
			Message: "AWSEndpointService conditions not found",
		}
	}

	if len(messages) > 0 {
		return metav1.Condition{
			Type:    string(conditionType),
			Status:  metav1.ConditionFalse,
			Reason:  hyperv1.AWSErrorReason,
			Message: strings.Join(messages, "; "),
		}
	}

	return metav1.Condition{
		Type:    string(conditionType),
		Status:  metav1.ConditionTrue,
		Reason:  hyperv1.AWSSuccessReason,
		Message: hyperv1.AllIsWellMessage,
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

// deleteAWSEndpointServices loops over AWSEndpointServiceList items and sends a delete request for each.
// If the HC has no valid aws credentials it removes the CPO finalizer for each AWSEndpointService.
// It returns true if len(awsEndpointServiceList.Items) != 0.
func deleteAWSEndpointServices(ctx context.Context, c client.Client, hc *hyperv1.HostedCluster, namespace string) (bool, error) {
	log := ctrl.LoggerFrom(ctx)
	var awsEndpointServiceList hyperv1.AWSEndpointServiceList
	if err := c.List(ctx, &awsEndpointServiceList, &client.ListOptions{Namespace: namespace}); err != nil && !apierrors.IsNotFound(err) {
		return false, fmt.Errorf("error listing awsendpointservices in namespace %s: %w", namespace, err)
	}
	for _, ep := range awsEndpointServiceList.Items {
		if ep.DeletionTimestamp != nil {
			if platformaws.GetCredentialStatus(hc) == platformaws.CredentialStatusValid && time.Since(ep.DeletionTimestamp.Time) < awsEndpointDeletionGracePeriod {
				continue
			}

			// We remove the CPO finalizer if there's no valid credentials so deletion can proceed.
			cpoFinalizer := "hypershift.openshift.io/control-plane-operator-finalizer"
			if controllerutil.ContainsFinalizer(&ep, cpoFinalizer) {
				controllerutil.RemoveFinalizer(&ep, cpoFinalizer)
				if err := c.Update(ctx, &ep); err != nil {
					return false, fmt.Errorf("failed to remove finalizer from awsendpointservice: %w", err)
				}
			}
			log.Info("Removed CPO finalizer for awsendpointservice because the HC has no valid aws credentials", "name", ep.Name, "endpoint-id", ep.Status.EndpointID)
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
	controlPlaneNamespace := manifests.HostedControlPlaneNamespace(hc.Namespace, hc.Name)
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

	p, err := platform.GetPlatform(ctx, hc, nil, "", nil)
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

		if od, ok := p.(platform.OrphanDeleter); ok {
			if err = od.DeleteOrphanedMachines(ctx, r.Client, hc, controlPlaneNamespace); err != nil {
				return false, err
			}
		}

		if exists {
			log.Info("Waiting for cluster deletion", "clusterName", hc.Spec.InfraID, "controlPlaneNamespace", controlPlaneNamespace)
			return false, nil
		}
	}

	if r.MonitoringDashboards {
		// Delete the monitoring dashboard cm
		monitoringDashboard := manifests.MonitoringDashboard(hc.Namespace, hc.Name)
		if err := r.Get(ctx, client.ObjectKeyFromObject(monitoringDashboard), monitoringDashboard); err != nil {
			if !apierrors.IsNotFound(err) {
				return false, fmt.Errorf("failed to get monitoring dashboard: %w", err)
			}
		} else {
			if err := r.Delete(ctx, monitoringDashboard); err != nil {
				if !apierrors.IsNotFound(err) {
					return false, fmt.Errorf("failed to delete monitoring dashboard: %w", err)
				}
			}
		}
	}

	// Cleanup Platform specifics.

	if err = p.DeleteCredentials(ctx, r.Client, hc,
		controlPlaneNamespace); err != nil {
		return false, err
	}

	if hc.Spec.Platform.Type == hyperv1.AWSPlatform {
		exists, err := deleteAWSEndpointServices(ctx, r.Client, hc, controlPlaneNamespace)
		if err != nil {
			return false, err
		}
		if exists {
			log.Info("Waiting for awsendpointservice deletion", "controlPlaneNamespace", controlPlaneNamespace)
			return false, nil
		}
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
	exists, err := hyperutil.DeleteIfNeeded(ctx, r.Client, controlplaneoperator.HostedControlPlane(controlPlaneNamespace, hc.Name))
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

	r.KubevirtInfraClients.Delete(hc.Spec.InfraID)

	if skipNSDeletion := hc.Annotations[hyperv1.SkipControlPlaneNamespaceDeletionAnnotation]; skipNSDeletion == "true" {
		return true, nil
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

func enqueueHostedClustersFunc(metricsSet metrics.MetricsSet, operatorNamespace string, c client.Client) handler.MapFunc {
	return func(ctx context.Context, obj client.Object) []reconcile.Request {
		log := ctrllog.Log

		handleDefault := func(obj client.Object) []reconcile.Request {
			requests := []reconcile.Request{}
			annotations := obj.GetAnnotations()
			if annotations != nil {
				hostedClusterName := obj.GetAnnotations()[hyperutil.HostedClusterAnnotation]
				if hostedClusterName != "" {
					return []reconcile.Request{
						{NamespacedName: hyperutil.ParseNamespacedName(hostedClusterName)},
					}
				}
				for k := range annotations {
					if strings.HasPrefix(k, referencedResourceAnnotationPrefix) {
						requests = append(requests, reconcile.Request{
							NamespacedName: types.NamespacedName{
								Name:      strings.TrimPrefix(k, referencedResourceAnnotationPrefix),
								Namespace: obj.GetNamespace(),
							},
						})
					}
				}
			}
			return requests
		}

		switch typedObj := obj.(type) {
		case *corev1.ConfigMap:
			if metricsSet == metrics.MetricsSetSRE && typedObj.Name == metrics.SREConfigurationConfigMapName && typedObj.Namespace == operatorNamespace {
				// A change has occurred to the SRE metrics set configuration. We should requeue all HostedClusters
				hcList := &hyperv1.HostedClusterList{}
				if err := c.List(ctx, hcList); err != nil {
					// An error occurred, report it.
					log.Error(err, "failed to list hosted clusters while processing SRE config event")
				}
				requests := make([]reconcile.Request, 0, len(hcList.Items))
				for _, hc := range hcList.Items {
					requests = append(requests, reconcile.Request{NamespacedName: types.NamespacedName{Name: hc.Name, Namespace: hc.Namespace}})
				}
				return requests
			}
			return handleDefault(typedObj)
		case *appsv1.StatefulSet:
			if typedObj.Name != "etcd" {
				return []reconcile.Request{}
			}
			hcpList := &hyperv1.HostedControlPlaneList{}
			if err := c.List(ctx, hcpList, client.InNamespace(typedObj.Namespace)); err != nil {
				log.Error(err, "failed to list hcp")
				return []reconcile.Request{}
			}
			if len(hcpList.Items) == 1 {
				hcAnnotation := hcpList.Items[0].Annotations[hyperutil.HostedClusterAnnotation]
				if hcAnnotation != "" {
					return []reconcile.Request{{NamespacedName: hyperutil.ParseNamespacedName(hcAnnotation)}}
				}
			}
			return []reconcile.Request{}
		case *batchv1.Job:
			if typedObj.Name != etcdrecoverymanifests.EtcdRecoveryJob("").Name {
				return []reconcile.Request{}
			}
			name := typedObj.Labels[jobHostedClusterNameLabel]
			namespace := typedObj.Labels[jobHostedClusterNamespaceLabel]
			if name != "" && namespace != "" {
				return []reconcile.Request{{NamespacedName: types.NamespacedName{Name: name, Namespace: namespace}}}
			}
			return []reconcile.Request{}
		case *hyperv1.NodePool:
			return []reconcile.Request{{NamespacedName: types.NamespacedName{Name: typedObj.Spec.ClusterName, Namespace: typedObj.Namespace}}}
		case *corev1.Secret:
			// reconcile the karpenter token rotations to the userData secret, since the ignition-server/tokensecret_controller does not control it
			if hcAnnotation, exists := typedObj.Annotations[hyperutil.HostedClusterAnnotation]; exists {
				if nodePoolAnnotation, exists := typedObj.Annotations[hyperkarpenterv1.TokenSecretNodePoolAnnotation]; exists {
					// only match tokenSecret with nodepool name karpenter and ignore changes to userDataSecret to avoid duplicate updates
					if hyperutil.ParseNamespacedName(nodePoolAnnotation).Name == hyperkarpenterv1.KarpenterNodePool && strings.HasPrefix(typedObj.Name, "token-") {
						// we still need to map the request to it's hostedcluster to trigger the reconcile
						return []reconcile.Request{{NamespacedName: hyperutil.ParseNamespacedName(hcAnnotation)}}
					}
				}
			}
			if typedObj.Name == manifests.KASServingCertSecret("").Name {
				for _, ownerRef := range typedObj.OwnerReferences {
					if ownerRef.Kind == "HostedControlPlane" {
						hcp := &hyperv1.HostedControlPlane{
							ObjectMeta: metav1.ObjectMeta{
								Namespace: typedObj.Namespace,
								Name:      ownerRef.Name,
							},
						}
						if err := c.Get(ctx, client.ObjectKeyFromObject(hcp), hcp); err != nil {
							log.Error(err, "failed to get hcp")
							return []reconcile.Request{}
						}
						if hcAnnotation := hcp.Annotations[hyperutil.HostedClusterAnnotation]; hcAnnotation != "" {
							return []reconcile.Request{{NamespacedName: hyperutil.ParseNamespacedName(hcAnnotation)}}
						}
						return []reconcile.Request{}
					}
				}
			}
			return handleDefault(typedObj)
		default:
			return handleDefault(typedObj)
		}
	}
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

	if err := r.validateAWSConfig(hc); err != nil {
		errs = append(errs, err)
	}

	if err := r.validateKubevirtConfig(ctx, hc); err != nil {
		errs = append(errs, err)
	}

	if err := r.validateAzureConfig(hc); err != nil {
		errs = append(errs, err)
	}

	if err := r.validateAgentConfig(ctx, hc); err != nil {
		errs = append(errs, err)
	}

	if err := validateClusterID(hc); err != nil {
		errs = append(errs, err)
	}

	if err := r.validatePublishingStrategyMapping(hc); err != nil {
		errs = append(errs, err)
	}

	// TODO(IBM): Revisit after fleets no longer use conflicting network CIDRs
	if hc.Spec.Platform.Type != hyperv1.IBMCloudPlatform {
		if err := r.validateNetworks(hc); err != nil {
			errs = append(errs, err)
		}
	}

	if err := r.validateUserCAConfigMaps(ctx, hc); err != nil {
		errs = append(errs, err...)
	}

	// TODO: remove when API CEL validation is enabled.
	if err := validateLabels(hc); err != nil {
		errs = append(errs, err...)
	}

	if err := r.validateOCPConfigurations(ctx, hc, r.Client); err != nil {
		errs = append(errs, err)
	}

	return utilerrors.NewAggregate(errs)
}

func validateLabels(hc *hyperv1.HostedCluster) []error {
	var errs []error
	for key, value := range hc.Spec.Labels {
		if validationErrs := validation.IsQualifiedName(key); len(errs) != 0 {
			errs = append(errs, errors.New(strings.Join(validationErrs, ", ")))
		}

		if validationErrs := validation.IsValidLabelValue(value); len(errs) != 0 {
			errs = append(errs, errors.New(strings.Join(validationErrs, ", ")))
		}
	}

	return errs
}

func (r *HostedClusterReconciler) validateUserCAConfigMaps(ctx context.Context, hc *hyperv1.HostedCluster) []error {
	var userCABundles []client.ObjectKey
	if hc.Spec.AdditionalTrustBundle != nil {
		userCABundles = append(userCABundles, client.ObjectKey{Namespace: hc.Namespace, Name: hc.Spec.AdditionalTrustBundle.Name})
	}
	if hc.Spec.Configuration != nil && hc.Spec.Configuration.Proxy != nil && hc.Spec.Configuration.Proxy.TrustedCA.Name != "" {
		userCABundles = append(userCABundles, client.ObjectKey{Namespace: hc.Namespace, Name: hc.Spec.Configuration.Proxy.TrustedCA.Name})
	}

	var errs []error
	for _, key := range userCABundles {
		cm := &corev1.ConfigMap{}
		if err := r.Get(ctx, key, cm); err != nil {
			errs = append(errs, fmt.Errorf("failed to get configMap %s: %w", key.Name, err))
			continue
		}
		_, hasData := cm.Data[certs.UserCABundleMapKey]
		if !hasData {
			errs = append(errs, fmt.Errorf("configMap %s must have a %s key", cm.Name, certs.UserCABundleMapKey))
		}
	}

	return errs
}

func (r *HostedClusterReconciler) validateReleaseImage(ctx context.Context, hc *hyperv1.HostedCluster, releaseProvider releaseinfo.ProviderWithOpenShiftImageRegistryOverrides) error {
	if _, exists := hc.Annotations[hyperv1.SkipReleaseImageValidation]; exists {
		return nil
	}
	pullSecretBytes, err := hyperutil.GetPullSecretBytes(ctx, r.Client, hc)
	if err != nil {
		return err
	}

	releaseInfo, err := r.lookupReleaseImage(ctx, hc, releaseProvider)
	if err != nil {
		return fmt.Errorf("failed to lookup release image: %w", err)
	}
	version, err := semver.Parse(releaseInfo.Version())
	if err != nil {
		return err
	}

	var currentVersion *semver.Version
	if hc.Status.Version != nil && hc.Status.Version.Desired.Image != hyperutil.HCControlPlaneReleaseImage(hc) {
		releaseInfo, err := releaseProvider.Lookup(ctx, hc.Status.Version.Desired.Image, pullSecretBytes)
		if err != nil {
			return fmt.Errorf("failed to lookup release image: %w", err)
		}
		version, err := semver.Parse(releaseInfo.Version())
		if err != nil {
			return err
		}
		currentVersion = &version
	}

	minSupportedVersion := supportedversion.GetMinSupportedVersion(hc)

	return supportedversion.IsValidReleaseVersion(&version, currentVersion, &supportedversion.LatestSupportedVersion, &minSupportedVersion, hc.Spec.Networking.NetworkType, hc.Spec.Platform.Type)
}

func isProgressing(hc *hyperv1.HostedCluster, releaseImage *releaseinfo.ReleaseImage, refWithDigest func() (string, error)) (bool, error) {
	for _, condition := range hc.Status.Conditions {
		switch string(condition.Type) {
		case string(hyperv1.SupportedHostedCluster), string(hyperv1.ValidHostedClusterConfiguration), string(hyperv1.ValidReleaseImage), string(hyperv1.ReconciliationActive):
			if condition.Status == metav1.ConditionFalse {
				return false, fmt.Errorf("%s condition is false: %s", string(condition.Type), condition.Message)
			}
		case string(hyperv1.ClusterVersionUpgradeable):
			_, _, err := isUpgrading(hc, releaseImage)
			if err != nil {
				return false, fmt.Errorf("ClusterVersionUpgradeable condition is false: %w", err)
			}
		}
	}

	withDigest, err := refWithDigest()
	if err != nil {
		return false, err
	}

	if hc.Status.Version == nil || (hc.Spec.Release.Image != hc.Status.Version.Desired.Image && withDigest != hc.Status.Version.Desired.Image) {
		// cluster is doing initial rollout or upgrading
		return true, nil
	}

	// cluster is conditions are good and is at desired release
	return false, nil
}

// validateAWSConfig validates all serviceTypes have a supported servicePublishingStrategy.
// All endpoints but the KAS should be exposed as Routes. KAS can be Route or Load Balancer.
//
// Depending on the awsEndpointAccessType, the routes will be exposed through a HCP router exposed via load balancer external or internal,
// or through the management cluster ingress.
// 1 - When Public
//
//	If the HO has external DNS support:
//		All serviceTypes including KAS should be Routes (with RoutePublishingStrategy.hostname != "").
//		They will be exposed through a common HCP router exposed via Service type LB external.
//	If the HO has no external DNS support:
//		The KAS serviceType should be LoadBalancer. It will be exposed through a dedicated Service type LB external.
//		All other serviceTypes should be Routes. They will be exposed by the management cluster default ingress.
//
// 2 - When PublicAndPrivate
//
//	If the HO has external DNS support:
//		All serviceTypes including KAS should be Routes (with RoutePublishingStrategy.hostname != "").
//		They will be exposed through a common HCP router exposed via both Service type LB internal and external.
//	If the HO has no external DNS support:
//		The KAS serviceType should be LoadBalancer. It will be exposed through a dedicated Service type LB external.
//		All other serviceTypes should be Routes. They will be exposed by a common HCP router is exposed via Service type LB internal.
//
// 3 - When Private
//
//	The KAS serviceType should be Route or Load balancer. TODO (alberto): remove Load balancer choice for private.
//	All other serviceTypes should be Routes. They will be exposed by a common HCP router exposed via Service type LB internal.
func (r *HostedClusterReconciler) validateAWSConfig(hc *hyperv1.HostedCluster) error {
	if hc.Spec.Platform.Type != hyperv1.AWSPlatform {
		return nil
	}

	if hc.Spec.Platform.AWS == nil {
		return errors.New("aws cluster needs .spec.platform.aws to be filled")
	}

	var errs []error
	for _, serviceType := range []hyperv1.ServiceType{
		hyperv1.Konnectivity,
		hyperv1.OAuthServer,
		hyperv1.Ignition,
	} {
		servicePublishingStrategy := hyperutil.ServicePublishingStrategyByTypeByHC(hc, serviceType)
		if servicePublishingStrategy == nil {
			errs = append(errs, fmt.Errorf("service type %v not found", serviceType))
		}

		if servicePublishingStrategy != nil && servicePublishingStrategy.Type != hyperv1.Route {
			errs = append(errs, fmt.Errorf("service type %v with publishing strategy %v is not supported, use Route", serviceType, servicePublishingStrategy.Type))
		}
	}

	kasPublishingStrategy := hyperutil.ServicePublishingStrategyByTypeByHC(hc, hyperv1.APIServer)
	if kasPublishingStrategy == nil {
		errs = append(errs, fmt.Errorf("service type %v not found", hyperv1.APIServer))
		return utilerrors.NewAggregate(errs)
	}

	if kasPublishingStrategy.Type == hyperv1.Route && !hyperutil.UseDedicatedDNSForKASByHC(hc) {
		errs = append(errs, fmt.Errorf("if serviceType is 'APIServer' and publishing strategy is 'Route', then hostname must be set"))
		return utilerrors.NewAggregate(errs)
	}

	if hc.Spec.Platform.AWS.EndpointAccess == hyperv1.Private {
		if kasPublishingStrategy.Type != hyperv1.Route && kasPublishingStrategy.Type != hyperv1.LoadBalancer {
			errs = append(errs, fmt.Errorf("service type %v with publishing strategy %v is not supported, use Route", hyperv1.APIServer, kasPublishingStrategy.Type))
		}
	} else {
		if !hyperutil.UseDedicatedDNSForKASByHC(hc) && kasPublishingStrategy.Type != hyperv1.LoadBalancer {
			errs = append(errs, fmt.Errorf("service type %v with publishing strategy %v is not supported, use Route or LoadBalancer", hyperv1.APIServer, kasPublishingStrategy.Type))
		}
		// When using dedicated DNS, the KAS should be exposed as Route.
		if hyperutil.IsPublicWithDNSByHC(hc) && hyperutil.IsLBKASByHC(hc) {
			errs = append(errs, fmt.Errorf("service type %v with publishing strategy %v is not supported when any service specifies external DNS, use Route", hyperv1.APIServer, kasPublishingStrategy.Type))
		}
	}

	return utilerrors.NewAggregate(errs)
}

func (r *HostedClusterReconciler) validateKubevirtConfig(ctx context.Context, hc *hyperv1.HostedCluster) error {
	if hc.Spec.Platform.Type != hyperv1.KubevirtPlatform {
		return nil
	}

	val, exists := hc.Annotations[hyperv1.AllowUnsupportedKubeVirtRHCOSVariantsAnnotation]
	if exists {
		if isTrue, _ := strconv.ParseBool(val); isTrue {
			// This is an unsupported escape hatch annotation for internal use
			// Some HCP users are using the kubevirt platform in unconventional ways
			// and we need to maintain the ability to use unsupported versions
			return nil
		}
	}

	var creds *hyperv1.KubevirtPlatformCredentials

	if hc.Spec.Platform.Kubevirt != nil && hc.Spec.Platform.Kubevirt.Credentials != nil {
		creds = hc.Spec.Platform.Kubevirt.Credentials
	}

	kvInfraClient, err := r.KubevirtInfraClients.DiscoverKubevirtClusterClient(ctx,
		r.Client,
		hc.Spec.InfraID,
		creds,
		hc.Namespace,
		hc.Namespace)
	if err != nil {
		return err
	}

	return kvinfra.ValidateClusterVersions(ctx, kvInfraClient)
}

// validatePublishingStrategyMapping validates that each published serviceType has a unique hostname.
func (r *HostedClusterReconciler) validatePublishingStrategyMapping(hc *hyperv1.HostedCluster) error {
	hostnameServiceMap := make(map[string]string, len(hc.Spec.Services))
	for _, svc := range hc.Spec.Services {
		hostname := ""
		if svc.Type == hyperv1.LoadBalancer && svc.LoadBalancer != nil {
			hostname = svc.LoadBalancer.Hostname
		}
		if svc.Type == hyperv1.Route && svc.Route != nil {
			hostname = svc.Route.Hostname
		}

		if hostname == "" {
			continue
		}

		serviceType, exists := hostnameServiceMap[hostname]
		if exists {
			return fmt.Errorf("service type %s can't be published with the same hostname %s as service type %s", svc.Service, hostname, serviceType)
		}

		hostnameServiceMap[hostname] = string(svc.Service)
	}

	return nil
}

func (r *HostedClusterReconciler) validateAzureConfig(hc *hyperv1.HostedCluster) error {
	if hc.Spec.Platform.Type != hyperv1.AzurePlatform {
		return nil
	}

	// Verify the platform is at least initialized
	if hc.Spec.Platform.Azure == nil {
		return errors.New("azurecluster needs .spec.platform.azure to be filled")
	}

	return nil
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

func (r *HostedClusterReconciler) validateNetworks(hc *hyperv1.HostedCluster) error {
	var errs field.ErrorList
	errs = append(errs, validateNetworkStackAddresses(hc)...)
	errs = append(errs, validateSliceNetworkCIDRs(hc)...)
	errs = append(errs, checkAdvertiseAddressOverlapping(hc)...)
	errs = append(errs, validateNodePortVsServiceNetwork(hc)...)

	return errs.ToAggregate()
}

// validateOCPConfigurations validates OpenShift-specific configurations for a HostedCluster.
// It's worth to abstract this validation to a separate function per API to have them organized.
// Currently validates:
// - API Server configuration
//
// TODO: Add validation for other OpenShift components (e.g. OAuth, Ingress, etc.)
// Jira: https://issues.redhat.com/browse/CNTRLPLANE-382
func (r *HostedClusterReconciler) validateOCPConfigurations(ctx context.Context, hc *hyperv1.HostedCluster, client client.Client) error {
	var errs field.ErrorList
	errs = append(errs, validations.ValidateOCPAPIServerSANs(ctx, hc, client)...)

	if hc.Spec.Configuration != nil && hc.Spec.Configuration.Authentication != nil {
		err := supportvalidations.ValidateAuthenticationSpec(ctx, client, hc.Spec.Configuration.Authentication, hc.Namespace, []string{hc.Spec.IssuerURL})
		if err != nil {
			fieldErr := &field.Error{
				Type:     field.ErrorTypeInvalid,
				Field:    field.NewPath("spec", "configuration", "authentication").String(),
				BadValue: hc.Spec.Configuration.Authentication,
				Detail:   err.Error(),
			}
			errs = append(errs, fieldErr)
		}
	}

	return errs.ToAggregate()
}

// findAdvertiseAddress function returns a string and an error indicating the AdvertiseAddress for the hostedcluster.
// if the advertise address is properly set, it will return that value and nil, otherwise will return an error.
// if the advertise address is not set, it will return the default one based on the network primary stack.
func findAdvertiseAddress(hc *hyperv1.HostedCluster) (netip.Addr, *field.Error) {
	var advertiseAddress netip.Addr
	if hc.Spec.Networking.APIServer != nil && hc.Spec.Networking.APIServer.AdvertiseAddress != nil {
		ipaddr, err := netip.ParseAddr(*hc.Spec.Networking.APIServer.AdvertiseAddress)
		if err != nil {
			return ipaddr, field.Invalid(field.NewPath("hc.Spec.Networking.APIServer.AdvertiseAddress"),
				ptr.To(ipaddr.String()),
				fmt.Sprintf("advertise address set in HostedCluster %s is not parseable", *hc.Spec.Networking.APIServer.AdvertiseAddress),
			)
		}

		return ipaddr, nil
	}

	ipaddr, err := netip.ParseAddr(hc.Spec.Networking.ClusterNetwork[0].CIDR.IP.String())
	if err != nil {
		return ipaddr, field.Invalid(field.NewPath("hc.Spec.Networking.ClusterNetwork[0].CIDR.IP"),
			ptr.To(ipaddr.String()),
			fmt.Sprintf("Cluster Network ip address %s is not parseable", hc.Spec.Networking.ClusterNetwork[0].CIDR.IP.String()),
		)
	}

	if strings.Contains(hc.Spec.Networking.ClusterNetwork[0].CIDR.IP.String(), ".") {
		advertiseAddress = netip.MustParseAddr(config.DefaultAdvertiseIPv4Address)
	}

	if strings.Contains(hc.Spec.Networking.ClusterNetwork[0].CIDR.IP.String(), ":") {
		advertiseAddress = netip.MustParseAddr(config.DefaultAdvertiseIPv6Address)
	}

	return advertiseAddress, nil
}

// validateNetworkStackAddresses validates that Networks defined in the HostedCluster are in the same network stack
// between each other against the primary IP using ClusterNetwork as a base.
func validateNetworkStackAddresses(hc *hyperv1.HostedCluster) field.ErrorList {
	var errs field.ErrorList
	ipv4 := make([]string, 0)
	ipv6 := make([]string, 0)

	networks := make(map[string]string, 0)

	if len(hc.Spec.Networking.ClusterNetwork) > 0 {
		networks["spec.networking.ClusterNetwork"] = hc.Spec.Networking.ClusterNetwork[0].CIDR.IP.String()
	}

	if len(hc.Spec.Networking.ServiceNetwork) > 0 {
		networks["spec.networking.ServiceNetwork"] = hc.Spec.Networking.ServiceNetwork[0].CIDR.IP.String()
	}

	if len(hc.Spec.Networking.MachineNetwork) > 0 {
		networks["spec.networking.MachineNetwork"] = hc.Spec.Networking.MachineNetwork[0].CIDR.IP.String()
	}

	advAddr, err := findAdvertiseAddress(hc)
	if err != nil {
		errs = append(errs, err)
	}

	networks["spec.networking.APIServerNetworking.AdvertiseAddress"] = advAddr.String()

	for fieldpath, ipaddr := range networks {
		checkIP := net.ParseIP(ipaddr)

		if checkIP != nil && strings.Contains(ipaddr, ".") {
			ipv4 = append(ipv4, ipaddr)
		}

		if checkIP != nil && strings.Contains(ipaddr, ":") {
			ipv6 = append(ipv6, ipaddr)
		}

		// This check ensures that the IPv6 and IPv4 is a valid ip
		if checkIP == nil {
			errs = append(errs, field.Invalid(field.NewPath(fieldpath),
				ptr.To(ipaddr),
				fmt.Sprintf("error checking network stack of %s with ip %s", fieldpath, ipaddr),
			))
		}
	}

	if len(ipv4) > 0 && len(ipv6) > 0 {
		// Invalid result, means that there are mixed stacks in the primary position of the stack
		errs = append(errs, field.Forbidden(field.NewPath("spec.networking"),
			fmt.Sprintf("declare multiple network stacks as primary network in the cluster definition is not allowed, ipv4: %v, ipv6: %v", ipv4, ipv6),
		))
	}

	return errs
}

// checkAdvertiseAddressOverlapping validates that the AdvertiseAddress defined does not overlap with
// the ClusterNetwork, ServiceNetwork and MachineNetwork
func checkAdvertiseAddressOverlapping(hc *hyperv1.HostedCluster) field.ErrorList {
	var errs field.ErrorList

	networks := make(map[string]string, 0)

	if len(hc.Spec.Networking.ClusterNetwork) > 0 {
		networks["spec.networking.ClusterNetwork"] = hc.Spec.Networking.ClusterNetwork[0].CIDR.String()
	}

	if len(hc.Spec.Networking.ServiceNetwork) > 0 {
		networks["spec.networking.ServiceNetwork"] = hc.Spec.Networking.ServiceNetwork[0].CIDR.String()
	}

	if len(hc.Spec.Networking.MachineNetwork) > 0 {
		networks["spec.networking.MachineNetwork"] = hc.Spec.Networking.MachineNetwork[0].CIDR.String()
	}

	advAddr, fieldErr := findAdvertiseAddress(hc)
	if fieldErr != nil {
		errs = append(errs, fieldErr)
		return errs
	}

	for fieldPath, cidr := range networks {
		network, err := netip.ParsePrefix(cidr)
		if err != nil {
			errs = append(errs, field.Invalid(field.NewPath(fieldPath),
				ptr.To(cidr),
				fmt.Sprintf("error parsing field %s prefix: %v", fieldPath, err),
			))
		}

		if network.Contains(advAddr) {
			errs = append(errs, field.Invalid(field.NewPath(fieldPath),
				ptr.To(cidr),
				fmt.Sprintf("the field %s with content %s overlaps with the defined AdvertiseAddress %s prefix: %v", fieldPath, cidr, advAddr.String(), err),
			))
		}
	}
	return errs
}

// Validate that the nodeport IP is not within the ServiceNetwork CIDR.
func validateNodePortVsServiceNetwork(hc *hyperv1.HostedCluster) field.ErrorList {
	var errs field.ErrorList

	ip := getNodePortIP(hc)
	if ip != nil {
		// Validate that the nodeport IP is not within the ServiceNetwork CIDR.
		for _, cidr := range hc.Spec.Networking.ServiceNetwork {
			netCIDR := (net.IPNet)(cidr.CIDR)
			if netCIDR.Contains(ip) {
				errs = append(errs, field.Invalid(field.NewPath("spec.networking.ServiceNetwork"), cidr.CIDR.String(), fmt.Sprintf("Nodeport IP is within the service network range: %s is within %s", ip, cidr.CIDR.String())))
			}
		}
	}
	return errs
}

func validateSliceNetworkCIDRs(hc *hyperv1.HostedCluster) field.ErrorList {
	var cidrEntries []cidrEntry

	for _, cidr := range hc.Spec.Networking.MachineNetwork {
		ce := cidrEntry{(net.IPNet)(cidr.CIDR), *field.NewPath("spec.networking.MachineNetwork")}
		cidrEntries = append(cidrEntries, ce)
	}
	for _, cidr := range hc.Spec.Networking.ServiceNetwork {
		ce := cidrEntry{(net.IPNet)(cidr.CIDR), *field.NewPath("spec.networking.ServiceNetwork")}
		cidrEntries = append(cidrEntries, ce)
	}
	for _, cidr := range hc.Spec.Networking.ClusterNetwork {
		ce := cidrEntry{(net.IPNet)(cidr.CIDR), *field.NewPath("spec.networking.ClusterNetwork")}
		cidrEntries = append(cidrEntries, ce)
	}

	if hc.Spec.Networking.NetworkType == hyperv1.OVNKubernetes &&
		hc.Spec.OperatorConfiguration != nil && hc.Spec.OperatorConfiguration.ClusterNetworkOperator != nil &&
		hc.Spec.OperatorConfiguration.ClusterNetworkOperator.OVNKubernetesConfig != nil &&
		hc.Spec.OperatorConfiguration.ClusterNetworkOperator.OVNKubernetesConfig.IPv4 != nil {
		ovnConfig := hc.Spec.OperatorConfiguration.ClusterNetworkOperator.OVNKubernetesConfig.IPv4
		if ovnConfig.InternalJoinSubnet != "" {
			_, cidr, err := net.ParseCIDR(ovnConfig.InternalJoinSubnet)
			if err == nil {
				ce := cidrEntry{*cidr, *field.NewPath("spec", "operatorConfiguration", "clusterNetworkOperator", "ovnKubernetesConfig", "ipv4", "internalJoinSubnet")}
				cidrEntries = append(cidrEntries, ce)
			}
		}
		if ovnConfig.InternalTransitSwitchSubnet != "" {
			_, cidr, err := net.ParseCIDR(ovnConfig.InternalTransitSwitchSubnet)
			if err == nil {
				ce := cidrEntry{*cidr, *field.NewPath("spec", "operatorConfiguration", "clusterNetworkOperator", "ovnKubernetesConfig", "ipv4", "internalTransitSwitchSubnet")}
				cidrEntries = append(cidrEntries, ce)
			}
		}
	}
	return compareCIDREntries(cidrEntries)
}

type cidrEntry struct {
	net  net.IPNet
	path field.Path
}

func cidrsOverlap(net1 *net.IPNet, net2 *net.IPNet) error {
	if net1.Contains(net2.IP) || net2.Contains(net1.IP) {
		return fmt.Errorf("%s and %s", net1.String(), net2.String())
	}
	return nil
}

func compareCIDREntries(ce []cidrEntry) field.ErrorList {
	var errs field.ErrorList

	for o := range ce {
		for i := o + 1; i < len(ce); i++ {
			if err := cidrsOverlap(&ce[o].net, &ce[i].net); err != nil {
				errs = append(errs, field.Invalid(&ce[o].path, ce[o].net.String(), fmt.Sprintf("%s and %s overlap: %s", ce[o].path.String(), ce[i].path.String(), err)))
			}
		}
	}
	return errs
}

const (
	oidcDocumentsFinalizer         = "hypershift.io/aws-oidc-discovery"
	serviceAccountSigningKeySecret = "sa-signing-key"
	serviceSignerPublicKey         = "service-account.pub"
)

func oidcDocumentGenerators() map[string]oidc.OIDCDocumentGeneratorFunc {
	return map[string]oidc.OIDCDocumentGeneratorFunc{
		"/.well-known/openid-configuration": oidc.GenerateConfigurationDocument,
		oidc.JWKSURI:                        oidc.GenerateJWKSDocument,
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
		return errors.New("hypershift wasn't configured with a S3 bucket or credentials, this makes it unable to set up OIDC for AWS clusters. Please install hypershift with the --oidc-storage-provider-s3-bucket-name, --oidc-storage-provider-s3-region and --oidc-storage-provider-s3-credentials flags set. The bucket must pre-exist and the credentials must be authorized to write into it")
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

	params := oidc.OIDCGeneratorParams{
		IssuerURL: hcp.Spec.IssuerURL,
		PubKey:    secret.Data[serviceSignerPublicKey],
	}

	for path, generator := range oidcDocumentGenerators() {
		bodyReader, err := generator(params)
		if err != nil {
			return fmt.Errorf("failed to generate OIDC document %s: %w", path, err)
		}
		_, err = r.S3Client.PutObject(&s3.PutObjectInput{
			Body:   bodyReader,
			Bucket: aws.String(r.OIDCStorageProviderS3BucketName),
			Key:    aws.String(hcluster.Spec.InfraID + path),
		})
		if err != nil {
			wrapped := fmt.Errorf("failed to upload %s to the %s s3 bucket", path, r.OIDCStorageProviderS3BucketName)
			if awsErr := awserr.Error(nil); errors.As(err, &awsErr) {
				switch awsErr.Code() {
				case s3.ErrCodeNoSuchBucket:
					wrapped = fmt.Errorf("%w: %s: this could be a misconfiguration of the hypershift operator; check the --oidc-storage-provider-s3-bucket-name flag", wrapped, awsErr.Code())
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
		if awsErr := awserr.Error(nil); !errors.As(err, &awsErr) || awsErr.Code() != s3.ErrCodeNoSuchBucket {
			return fmt.Errorf("failed to delete OIDC objects from %s S3 bucket: %w", r.OIDCStorageProviderS3BucketName, err)
		}
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
	infraCR client.Object, namespace, clusterName, hcpNamespace string,
) error {
	nodePools, err := listNodePools(ctx, r.Client, namespace, clusterName)
	if err != nil {
		return fmt.Errorf("failed to get nodePools by cluster name for cluster %q: %w", clusterName, err)
	}
	subnetIDs := []string{}
	for _, nodePool := range nodePools {
		if nodePool.Spec.Platform.AWS != nil &&
			nodePool.Spec.Platform.AWS.Subnet.ID != nil {
			subnetIDs = append(subnetIDs, *nodePool.Spec.Platform.AWS.Subnet.ID)
		}
	}
	// Sort for stable update detection (is this needed?)
	sort.Strings(subnetIDs)
	return nil
}

func (r *HostedClusterReconciler) lookupReleaseImage(ctx context.Context, hcluster *hyperv1.HostedCluster, releaseProvider releaseinfo.ProviderWithOpenShiftImageRegistryOverrides) (*releaseinfo.ReleaseImage, error) {
	pullSecretBytes, err := hyperutil.GetPullSecretBytes(ctx, r.Client, hcluster)
	if err != nil {
		return nil, err
	}
	return releaseProvider.Lookup(ctx, hyperutil.HCControlPlaneReleaseImage(hcluster), pullSecretBytes)
}

func (r *HostedClusterReconciler) isAutoscalingNeeded(ctx context.Context, hcluster *hyperv1.HostedCluster) (bool, error) {
	nodePools, err := listNodePools(ctx, r.Client, hcluster.Namespace, hcluster.Name)
	if err != nil {
		return false, fmt.Errorf("failed to get nodePools by cluster name for cluster %q: %w", hcluster.Name, err)
	}
	for _, nodePool := range nodePools {
		if nodePool.Spec.AutoScaling != nil {
			return true, nil
		}
	}
	return false, nil
}

func (r *HostedClusterReconciler) syncKVLiveMigratableCondition(ctx context.Context, hcluster *hyperv1.HostedCluster) error {
	if hcluster.Spec.Platform.Type != hyperv1.KubevirtPlatform {
		return nil
	}
	nodePools, err := listNodePools(ctx, r.Client, hcluster.Namespace, hcluster.Name)
	if err != nil {
		return fmt.Errorf("failed to get node pools by cluster name for cluster %q: %w", hcluster.Name, err)
	}
	var nonMigratableNodePools []string
	for _, nodePool := range nodePools {
		lmNpCondition := FindNodePoolStatusCondition(nodePool.Status.Conditions, string(hyperv1.KubeVirtNodesLiveMigratable))
		if lmNpCondition != nil && lmNpCondition.Status == corev1.ConditionFalse {
			nonMigratableNodePools = append(nonMigratableNodePools, nodePool.Name)
		}
	}

	var lmHcCondition metav1.Condition

	if len(nonMigratableNodePools) == 0 {
		lmHcCondition = metav1.Condition{
			Type:               string(hyperv1.KubeVirtNodesLiveMigratable),
			Status:             metav1.ConditionTrue,
			Reason:             hyperv1.AsExpectedReason,
			Message:            hyperv1.AllIsWellMessage,
			ObservedGeneration: hcluster.Generation,
		}
	} else {
		lmHcCondition = metav1.Condition{
			Type:   string(hyperv1.KubeVirtNodesLiveMigratable),
			Status: metav1.ConditionFalse,
			Reason: hyperv1.KubeVirtNodesLiveMigratableReason,
			Message: fmt.Sprintf("Non-live-migratable node(s) have been found on the following NodePool(s) attached to "+
				"this Hosted Cluster: %s. It is recommended to use live-migratable nodes in order to improve cluster stability "+
				"and downtime. Check the reason for the non-live-migratability in the corresponding NodePool(s).",
				strings.Join(nonMigratableNodePools, ", ")),
			ObservedGeneration: hcluster.Generation,
		}
	}
	meta.SetStatusCondition(&hcluster.Status.Conditions, lmHcCondition)

	return nil
}

// isUpgrading returns
// 1) bool indicating whether the HostedCluster is upgrading
// 2) non-error message about the condition of the upgrade
// 3) error indicating that the upgrade is not allowed or we were not able to determine
func isUpgrading(hcluster *hyperv1.HostedCluster, releaseImage *releaseinfo.ReleaseImage) (bool, string, error) {
	if hcluster.Status.Version == nil || hcluster.Status.Version.Desired.Image == hyperutil.HCControlPlaneReleaseImage(hcluster) {
		// cluster is either installing or at the version requested by the spec, no upgrade in progress
		return false, "", nil
	}
	upgradeable := meta.FindStatusCondition(hcluster.Status.Conditions, string(hyperv1.ClusterVersionUpgradeable))
	if upgradeable == nil || upgradeable.Status == metav1.ConditionTrue {
		// CVO reports Upgradeable is true, upgrade is allowed to proceed
		return true, "", nil
	}

	// Check if the upgrade is being forced
	upgradeImage, exists := hcluster.Annotations[hyperv1.ForceUpgradeToAnnotation]
	if exists {
		if upgradeImage != hyperutil.HCControlPlaneReleaseImage(hcluster) {
			return true, "", fmt.Errorf("force upgrade annotation is present but does not match desired release image")
		} else {
			return true, "upgrade is forced by annotation", nil
		}
	}

	// Check if ControlPlaneRelease is set.  ControlPlaneRelease should be considered a forced upgrade.
	if hcluster.Spec.ControlPlaneRelease != nil {
		return true, "upgrade is forced by ControlPlaneRelease being set", nil
	}

	// Check if upgrade is a z-stream upgrade.  These are allowed even when Upgradeable is false
	currentTargetVersion, err := semver.Parse(hcluster.Status.Version.Desired.Version)
	if err != nil {
		return true, "", fmt.Errorf("cluster is %s=%s (%s: %s), and failed to parse the current target %s as a Semantic Version: %w", upgradeable.Type, upgradeable.Status, upgradeable.Reason, upgradeable.Message, hcluster.Status.Version.Desired.Version, err)
	}
	requestedVersion, err := semver.Parse(releaseImage.Version())
	if err != nil {
		return true, "", fmt.Errorf("failed to parse release image version: %w", err)
	}
	if requestedVersion.Major == currentTargetVersion.Major && requestedVersion.Minor == currentTargetVersion.Minor {
		// z-stream upgrades should be allowed even when ClusterVersionUpgradeable is false
		return true, "", nil
	}

	// Upgradeable is false and no exception criteria were met, cluster is not upgradable
	return true, "", fmt.Errorf("cluster version is not upgradeable")
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
	controlPlaneNamespace := manifests.HostedControlPlaneNamespace(hc.Namespace, hc.Name)
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
	if err := ensureReferencedResourceAnnotation(ctx, r.Client, hc.Name, signingKeySecret); err != nil {
		return nil, nil, fmt.Errorf("failed to set referenced resource annotation: %w", err)
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

func (r *HostedClusterReconciler) reconcileKubevirtPlatformDefaultSettings(ctx context.Context, hc *hyperv1.HostedCluster, createOrUpdate upsert.CreateOrUpdateFN, logger logr.Logger) error {
	if hc.Spec.Platform.Kubevirt == nil {
		hc.Spec.Platform.Kubevirt = &hyperv1.KubevirtPlatformSpec{}
	}

	if hc.Spec.Platform.Kubevirt.GenerateID == "" {
		hc.Spec.Platform.Kubevirt.GenerateID = utilrand.String(10)
	}
	// auto generate the basedomain by retrieving the default ingress *.apps dns.
	if hc.Spec.Platform.Kubevirt.BaseDomainPassthrough != nil && *hc.Spec.Platform.Kubevirt.BaseDomainPassthrough {
		if hc.Spec.DNS.BaseDomain == "" {
			kvInfraClient, err := r.KubevirtInfraClients.DiscoverKubevirtClusterClient(ctx, r.Client, hc.Spec.InfraID, hc.Spec.Platform.Kubevirt.Credentials, hc.Namespace, hc.Namespace)
			if err != nil {
				return err
			}
			// kubevirtInfraTempRoute is used to resolve the base domain of the infra cluster without accessing IngressController
			kubevirtInfraTempRoute := manifests.KubevirtInfraTempRoute(kvInfraClient.GetInfraNamespace())

			createOrUpdateProvider := upsert.New(r.EnableCIDebugOutput)
			_, err = createOrUpdateProvider.CreateOrUpdate(ctx, kvInfraClient.GetInfraClient(), kubevirtInfraTempRoute, func() error {
				return manifests.ReconcileKubevirtInfraTempRoute(kubevirtInfraTempRoute)
			})
			if err != nil {
				return fmt.Errorf("unable to create a temporary route to detect kubevirt platform base domain: %w", err)
			}

			host := kubevirtInfraTempRoute.Spec.Host
			if host != "" {
				hostParts := strings.Split(host, ".")
				baseDomain := strings.Join(hostParts[1:], ".")

				// For the KubeVirt platform, the basedomain can be autogenerated using
				// the *.apps domain of the management/infra hosting cluster
				// This makes the guest cluster's base domain a subdomain of the
				// hypershift infra/mgmt cluster's base domain.
				//
				// Example:
				//   Infra/Mgmt cluster's DNS
				//     Base: example.com
				//     Cluster: mgmt-cluster.example.com
				//     Apps:    *.apps.mgmt-cluster.example.com
				//   KubeVirt Guest cluster's DNS
				//     Base: apps.mgmt-cluster.example.com
				//     Cluster: guest.apps.mgmt-cluster.example.com
				//     Apps: *.apps.guest.apps.mgmt-cluster.example.com
				//
				// This is possible using OCP wildcard routes
				hc.Spec.DNS.BaseDomain = baseDomain

				if err := kvInfraClient.GetInfraClient().Delete(ctx, kubevirtInfraTempRoute); err != nil {
					return err
				}
			} else {
				return fmt.Errorf("unable to autodetect kubevirt platform base domain, temporary route is not giving a host value")
			}
		}
	}

	if hc.Spec.SecretEncryption == nil ||
		len(hc.Spec.SecretEncryption.Type) == 0 ||
		(hc.Spec.SecretEncryption.Type == hyperv1.AESCBC &&
			(hc.Spec.SecretEncryption.AESCBC == nil || len(hc.Spec.SecretEncryption.AESCBC.ActiveKey.Name) == 0)) {

		logger.Info("no etcd encryption key configuration found; adding", "hostedCluster name", hc.Name, "hostedCluster namespace", hc.Namespace)
		etcdEncSec := &corev1.Secret{
			ObjectMeta: metav1.ObjectMeta{
				Namespace: hc.Namespace,
				Name:      hc.Name + etcdEncKeyPostfix,
			},
		}

		_, err := createOrUpdate(ctx, r.Client, etcdEncSec, func() error {
			// don't override existing key just in case something weird happened
			_, exists := etcdEncSec.Data[hyperv1.AESCBCKeySecretKey]
			if exists {
				return nil
			}

			generatedKey := make([]byte, 32)
			_, err := rand.Read(generatedKey)
			if err != nil {
				return fmt.Errorf("failed to generate the etcd encryption key; %w", err)
			}

			if etcdEncSec.Data == nil {
				etcdEncSec.Data = map[string][]byte{}
			}
			etcdEncSec.Data[hyperv1.AESCBCKeySecretKey] = generatedKey
			etcdEncSec.Type = corev1.SecretTypeOpaque

			ownerRef := config.OwnerRefFrom(hc)
			ownerRef.ApplyTo(etcdEncSec)
			return nil
		})
		if err != nil {
			return fmt.Errorf("failed to create ETCD SecretEncryption key for KubeVirt platform HostedCluster: %w", err)
		}

		hc.Spec.SecretEncryption = &hyperv1.SecretEncryptionSpec{
			Type: hyperv1.AESCBC,
			AESCBC: &hyperv1.AESCBCSpec{
				ActiveKey: corev1.LocalObjectReference{
					Name: etcdEncSec.Name,
				},
			},
		}
	}

	// Reconcile management infrastructure annotation
	if _, exists := hc.Annotations[hyperv1.ManagementPlatformAnnotation]; !exists {
		if hc.Annotations == nil {
			hc.Annotations = map[string]string{}
		}
		mgmtInfraKey := client.ObjectKey{Name: "cluster"}
		mgmtInfra := &configv1.Infrastructure{}

		if err := r.Get(ctx, mgmtInfraKey, mgmtInfra); err != nil {
			return fmt.Errorf("failed to get infrastructure.config.openshift.io status: %w", err)
		}
		if mgmtInfra.Status.PlatformStatus != nil {
			mgmtPlatformType := mgmtInfra.Status.PlatformStatus.Type
			hc.Annotations[hyperv1.ManagementPlatformAnnotation] = string(mgmtPlatformType)
			if err := r.Client.Update(ctx, hc); err != nil {
				return fmt.Errorf("failed to update hostedcluster %s annotation: %w", hc.Name, err)
			}
		}
	}

	return nil
}

func (r *HostedClusterReconciler) reconcilePlatformDefaultSettings(ctx context.Context, hc *hyperv1.HostedCluster, createOrUpdate upsert.CreateOrUpdateFN, logger logr.Logger) error {
	switch hc.Spec.Platform.Type {
	case hyperv1.KubevirtPlatform:
		return r.reconcileKubevirtPlatformDefaultSettings(ctx, hc, createOrUpdate, logger)
	}
	return nil
}

func (r *HostedClusterReconciler) getARNFromSecret(ctx context.Context, hcName, name, namespace string) (string, error) {
	creds := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: namespace,
		},
	}
	if err := r.Get(ctx, client.ObjectKeyFromObject(creds), creds); err != nil {
		return "", fmt.Errorf("failed to get secret: %w", err)
	}
	if err := ensureReferencedResourceAnnotation(ctx, r.Client, hcName, creds); err != nil {
		return "", fmt.Errorf("failed to set referenced resource annotation: %w", err)
	}
	credContent, err := ini.Load(creds.Data["credentials"])
	if err != nil {
		return "", fmt.Errorf("cannot parse credentials: %w", err)
	}
	return credContent.Section("default").Key("role_arn").String(), nil
}

func (r *HostedClusterReconciler) dereferenceAWSRoles(ctx context.Context, hcName string, rolesRef *hyperv1.AWSRolesRef, ns string) error {
	if strings.HasPrefix(rolesRef.NodePoolManagementARN, "arn-from-secret::") {
		secretName := strings.TrimPrefix(rolesRef.NodePoolManagementARN, "arn-from-secret::")
		arn, err := r.getARNFromSecret(ctx, hcName, secretName, ns)
		if err != nil {
			return fmt.Errorf("failed to get ARN from secret %s/%s: %w", ns, secretName, err)
		}
		rolesRef.NodePoolManagementARN = arn
	}

	if strings.HasPrefix(rolesRef.ControlPlaneOperatorARN, "arn-from-secret::") {
		secretName := strings.TrimPrefix(rolesRef.ControlPlaneOperatorARN, "arn-from-secret::")
		arn, err := r.getARNFromSecret(ctx, hcName, secretName, ns)
		if err != nil {
			return fmt.Errorf("failed to get ARN from secret %s/%s: %w", ns, secretName, err)
		}
		rolesRef.ControlPlaneOperatorARN = arn
	}

	if strings.HasPrefix(rolesRef.KubeCloudControllerARN, "arn-from-secret::") {
		secretName := strings.TrimPrefix(rolesRef.KubeCloudControllerARN, "arn-from-secret::")
		arn, err := r.getARNFromSecret(ctx, hcName, secretName, ns)
		if err != nil {
			return fmt.Errorf("failed to get ARN from secret %s/%s: %w", ns, secretName, err)
		}
		rolesRef.KubeCloudControllerARN = arn
	}

	return nil
}

type DashboardTemplateData struct {
	Name                  string
	Namespace             string
	ID                    string
	ControlPlaneNamespace string
}

func (r *HostedClusterReconciler) reconcileMonitoringDashboard(ctx context.Context, createOrUpdate upsert.CreateOrUpdateFN, hc *hyperv1.HostedCluster) error {
	log := ctrl.LoggerFrom(ctx)
	dashboardTemplate := manifests.MonitoringDashboardTemplate(r.OperatorNamespace)
	if err := r.Get(ctx, client.ObjectKeyFromObject(dashboardTemplate), dashboardTemplate); err != nil {
		if apierrors.IsNotFound(err) {
			log.Info("WARNING: monitoring dashboard template is not found. No dashboard will be generated")
			return nil
		}
		return fmt.Errorf("failed to read monitoring dashboard template: %w", err)
	}
	dashboard := dashboardTemplate.Data["template"]
	varsToReplace := map[string]string{
		"__NAME__":                    hc.Name,
		"__NAMESPACE__":               hc.Namespace,
		"__CONTROL_PLANE_NAMESPACE__": manifests.HostedControlPlaneNamespace(hc.Namespace, hc.Name),
		"__CLUSTER_ID__":              hc.Spec.ClusterID,
	}
	for k, v := range varsToReplace {
		dashboard = strings.ReplaceAll(dashboard, k, v)
	}

	dashboardCM := manifests.MonitoringDashboard(hc.Namespace, hc.Name)
	if _, err := createOrUpdate(ctx, r.Client, dashboardCM, func() error {
		if dashboardCM.Labels == nil {
			dashboardCM.Labels = map[string]string{}
		}
		dashboardCM.Labels["console.openshift.io/dashboard"] = "true"

		if dashboardCM.Data == nil {
			dashboardCM.Data = map[string]string{}
		}
		dashboardCM.Data["hostedcluster-dashboard.json"] = dashboard
		return nil
	}); err != nil {
		return fmt.Errorf("failed to reconcile monitoring dashboard: %w", err)
	}
	return nil
}

// reconcileSREMetricsConfig loads the SRE metrics configuration (avoids parsing if the content is the same)
// and ensures that it is synced to the hosted control plane
func (r *HostedClusterReconciler) reconcileSREMetricsConfig(ctx context.Context, createOrUpdate upsert.CreateOrUpdateFN, hcp *hyperv1.HostedControlPlane) error {
	log := ctrl.LoggerFrom(ctx)
	if r.MetricsSet != metrics.MetricsSetSRE {
		return nil
	}
	log.Info("Reconciling SRE metrics configuration")
	cm := metrics.SREMetricsSetConfigurationConfigMap(r.OperatorNamespace)
	if err := r.Get(ctx, client.ObjectKeyFromObject(cm), cm); err != nil {
		if apierrors.IsNotFound(err) {
			log.Info("SRE configuration does not exist")
			return nil
		}
		return fmt.Errorf("failed to get SRE configuration configmap: %w", err)
	}
	currentMetricsSetConfigHash := metrics.SREMetricsSetConfigHash(cm)
	if currentMetricsSetConfigHash != r.SREConfigHash {
		// Only load a new config if configuration content has changed
		if err := metrics.LoadSREMetricsSetConfigurationFromConfigMap(cm); err != nil {
			return fmt.Errorf("failed to load SRE configuration: %w", err)
		}
		r.SREConfigHash = currentMetricsSetConfigHash
	}
	destinationCM := metrics.SREMetricsSetConfigurationConfigMap(hcp.Namespace)
	ownerRef := config.OwnerRefFrom(hcp)
	if _, err := createOrUpdate(ctx, r.Client, destinationCM, func() error {
		ownerRef.ApplyTo(destinationCM)
		destinationCM.Data = cm.Data
		return nil
	}); err != nil {
		return fmt.Errorf("failed to update hosted cluster SRE metrics configuration: %w", err)
	}
	return nil
}

func getNodePortIP(hcluster *hyperv1.HostedCluster) net.IP {
	for _, svc := range hcluster.Spec.Services {
		if svc.Service == hyperv1.APIServer && svc.Type == hyperv1.NodePort {
			return net.ParseIP(svc.NodePort.Address)
		}
	}
	return nil
}

func ensureReferencedResourceAnnotation(ctx context.Context, client client.Client, hcName string, obj client.Object) error {
	if obj.GetNamespace() == "" {
		panic("program error: ensureReferencedResourceAnnotation called on cluster-scoped object")
	}
	existing := obj.GetAnnotations()
	if existing == nil {
		existing = map[string]string{}
	}
	key := referencedResourceAnnotationPrefix + hcName
	if _, ok := existing[key]; ok {
		return nil
	}
	existing[key] = "true"
	obj.SetAnnotations(existing)
	if err := client.Update(ctx, obj); err != nil {
		return err
	}
	return nil
}

func ensureHostedResourcesAreEmpty(ctx context.Context, crclient client.Client, hcluster *hyperv1.HostedCluster, obj client.Object) error {
	if !azureutil.IsAroHCP() || !hyperutil.HasAnnotationWithValue(obj, hyperv1.HostedClusterSourcedAnnotation, "true") {
		return nil
	}
	var cm corev1.Secret
	if err := crclient.Get(ctx, client.ObjectKey{Namespace: hcluster.Namespace, Name: obj.GetName()}, &cm); err != nil {
		return fmt.Errorf("failed to retrieve secret %s: %w", obj.GetName(), err)
	}
	if len(cm.Data) != 0 || len(cm.StringData) != 0 {
		return fmt.Errorf("secret %s is not empty. Secrets annotated with %s must be empty", cm.GetName(), hyperv1.HostedClusterSourcedAnnotation)
	}
	return nil
}

func isAPIServerRoute(hcluster *hyperv1.HostedCluster) bool {
	for _, svc := range hcluster.Spec.Services {
		if svc.Service == hyperv1.APIServer {
			return svc.Type == hyperv1.Route
		}
	}
	return false
}

// FindStatusCondition finds the conditionType in conditions.
func FindNodePoolStatusCondition(conditions []hyperv1.NodePoolCondition, conditionType string) *hyperv1.NodePoolCondition {
	for i := range conditions {
		if conditions[i].Type == conditionType {
			return &conditions[i]
		}
	}

	return nil
}

// reconcileAdditionalTrustBundle reconciles the HostedControlPlane AdditionalTrustBundle ConfigMap by resolving
// the source reference from the HostedCluster and syncing the CM in the control plane namespace.
func (r *HostedClusterReconciler) reconcileAdditionalTrustBundle(ctx context.Context, hcluster *hyperv1.HostedCluster, createOrUpdate upsert.CreateOrUpdateFN, controlPlaneNamespace string) error {
	dest := controlplaneoperator.UserCABundle(controlPlaneNamespace)
	if hcluster.Spec.AdditionalTrustBundle == nil {
		// If the HostedCluster has no additional trust bundle, delete the destination ConfigMap if it exists
		if _, err := hyperutil.DeleteIfNeeded(ctx, r.Client, dest); err != nil {
			return fmt.Errorf("failed to delete unused additionalTrustBundle: %w", err)
		}
		return nil
	}

	var src corev1.ConfigMap
	err := r.Client.Get(ctx, client.ObjectKey{Namespace: hcluster.Namespace, Name: hcluster.Spec.AdditionalTrustBundle.Name}, &src)
	if err != nil {
		return fmt.Errorf("failed to get hostedcluster AdditionalTrustBundle ConfigMap %s: %w", hcluster.Spec.AdditionalTrustBundle.Name, err)
	}
	if err := ensureReferencedResourceAnnotation(ctx, r.Client, hcluster.Name, &src); err != nil {
		return fmt.Errorf("failed to set referenced resource annotation: %w", err)
	}
	_, err = createOrUpdate(ctx, r.Client, dest, func() error {
		srcData, srcHasData := src.Data["ca-bundle.crt"]
		if !srcHasData {
			return fmt.Errorf("hostedcluster AdditionalTrustBundle configmap %q must have a ca-bundle.crt key", src.Name)
		}
		if dest.Data == nil {
			dest.Data = map[string]string{}
		}
		dest.Data["ca-bundle.crt"] = srcData
		return nil
	})
	if err != nil {
		return fmt.Errorf("failed to reconcile controlplane AdditionalTrustBundle configmap: %w", err)
	}

	return nil
}
