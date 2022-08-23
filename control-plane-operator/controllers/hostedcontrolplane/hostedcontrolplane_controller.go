package hostedcontrolplane

import (
	"context"
	crand "crypto/rand"
	"errors"
	"fmt"
	"math/big"
	"sort"
	"strings"
	"time"

	"github.com/blang/semver"
	"github.com/go-logr/logr"
	routev1 "github.com/openshift/api/route/v1"
	hyperv1 "github.com/openshift/hypershift/api/v1alpha1"
	"github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/autoscaler"
	"github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/cloud/aws"
	"github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/cloud/azure"
	"github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/cloud/powervs"
	"github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/clusterpolicy"
	"github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/cno"
	"github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/common"
	"github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/configoperator"
	"github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/cvo"
	"github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/etcd"
	"github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/ignition"
	"github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/ignitionserver"
	"github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/ingress"
	"github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/ingressoperator"
	"github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/kas"
	"github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/kcm"
	"github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/konnectivity"
	"github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/machineapprover"
	"github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/manifests"
	"github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/mcs"
	"github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/oapi"
	"github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/oauth"
	"github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/ocm"
	"github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/olm"
	"github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/pki"
	"github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/registryoperator"
	"github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/scheduler"
	"github.com/openshift/hypershift/support/capabilities"
	"github.com/openshift/hypershift/support/config"
	"github.com/openshift/hypershift/support/events"
	"github.com/openshift/hypershift/support/globalconfig"
	"github.com/openshift/hypershift/support/metrics"
	"github.com/openshift/hypershift/support/releaseinfo"
	"github.com/openshift/hypershift/support/upsert"
	"github.com/openshift/hypershift/support/util"
	prometheusoperatorv1 "github.com/prometheus-operator/prometheus-operator/pkg/apis/monitoring/v1"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	policyv1 "k8s.io/api/policy/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/types"
	utilerrors "k8s.io/apimachinery/pkg/util/errors"
	"k8s.io/client-go/util/workqueue"
	"k8s.io/utils/pointer"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
	"sigs.k8s.io/controller-runtime/pkg/source"
)

const (
	finalizer                  = "hypershift.openshift.io/finalizer"
	DefaultAdminKubeconfigName = "admin-kubeconfig"
	DefaultAdminKubeconfigKey  = "kubeconfig"

	ImageStreamAutoscalerImage             = "cluster-autoscaler"
	ImageStreamClusterMachineApproverImage = "cluster-machine-approver"
)

type InfrastructureStatus struct {
	APIHost                 string
	APIPort                 int32
	OAuthHost               string
	OAuthPort               int32
	KonnectivityHost        string
	KonnectivityPort        int32
	OpenShiftAPIHost        string
	OauthAPIServerHost      string
	PackageServerAPIAddress string
	Message                 string
}

func (s InfrastructureStatus) IsReady() bool {
	return len(s.APIHost) > 0 &&
		len(s.OAuthHost) > 0 &&
		len(s.KonnectivityHost) > 0 &&
		s.APIPort > 0 &&
		s.OAuthPort > 0 &&
		s.KonnectivityPort > 0
}

type HostedControlPlaneReconciler struct {
	client.Client

	// ManagementClusterCapabilities can be asked for support of optional management cluster capabilities
	ManagementClusterCapabilities capabilities.CapabiltyChecker

	// SetDefaultSecurityContext is used to configure Security Context for containers
	SetDefaultSecurityContext bool

	Log                           logr.Logger
	ReleaseProvider               releaseinfo.ProviderWithRegistryOverrides
	createOrUpdate                func(hcp *hyperv1.HostedControlPlane) upsert.CreateOrUpdateFN
	EnableCIDebugOutput           bool
	OperateOnReleaseImage         string
	DefaultIngressDomain          string
	MetricsSet                    metrics.MetricsSet
	reconcileInfrastructureStatus func(ctx context.Context, hcp *hyperv1.HostedControlPlane) (InfrastructureStatus, error)
}

func (r *HostedControlPlaneReconciler) SetupWithManager(mgr ctrl.Manager, createOrUpdate upsert.CreateOrUpdateFN) error {
	r.setup(createOrUpdate)
	b := ctrl.NewControllerManagedBy(mgr).
		For(&hyperv1.HostedControlPlane{}).
		WithOptions(controller.Options{
			RateLimiter: workqueue.NewItemExponentialFailureRateLimiter(1*time.Second, 10*time.Second),
		})
	for _, handler := range r.eventHandlers() {
		b.Watches(&source.Kind{Type: handler.obj}, handler.handler)
	}
	if _, err := b.Build(r); err != nil {
		return fmt.Errorf("failed setting up with a controller manager %w", err)
	}

	// Set based on SCC capability
	// When SCC is available (OpenShift), the container's security context and UID range is automatically set
	// When SCC is not available (Kubernetes), we want to explicitly set a default (non-root) security context
	r.SetDefaultSecurityContext = !r.ManagementClusterCapabilities.Has(capabilities.CapabilitySecurityContextConstraint)

	r.reconcileInfrastructureStatus = r.defaultReconcileInfrastructureStatus

	return nil
}

func (r *HostedControlPlaneReconciler) setup(createOrUpdate upsert.CreateOrUpdateFN) {
	r.createOrUpdate = createOrUpdateWithOwnerRefFactory(createOrUpdate)
}

type eventHandler struct {
	obj     client.Object
	handler handler.EventHandler
}

func (r *HostedControlPlaneReconciler) eventHandlers() []eventHandler {
	handlers := []eventHandler{
		{obj: &corev1.Event{}, handler: handler.EnqueueRequestsFromMapFunc(r.hostedControlPlaneInNamespace)},
		{obj: &corev1.Service{}, handler: &handler.EnqueueRequestForOwner{OwnerType: &hyperv1.HostedControlPlane{}}},
		{obj: &appsv1.Deployment{}, handler: &handler.EnqueueRequestForOwner{OwnerType: &hyperv1.HostedControlPlane{}}},
		{obj: &appsv1.StatefulSet{}, handler: &handler.EnqueueRequestForOwner{OwnerType: &hyperv1.HostedControlPlane{}}},
		{obj: &corev1.Secret{}, handler: &handler.EnqueueRequestForOwner{OwnerType: &hyperv1.HostedControlPlane{}}},
		{obj: &corev1.ConfigMap{}, handler: &handler.EnqueueRequestForOwner{OwnerType: &hyperv1.HostedControlPlane{}}},
		{obj: &corev1.ServiceAccount{}, handler: &handler.EnqueueRequestForOwner{OwnerType: &hyperv1.HostedControlPlane{}}},
		{obj: &policyv1.PodDisruptionBudget{}, handler: &handler.EnqueueRequestForOwner{OwnerType: &hyperv1.HostedControlPlane{}}},
		{obj: &prometheusoperatorv1.PodMonitor{}, handler: &handler.EnqueueRequestForOwner{OwnerType: &hyperv1.HostedControlPlane{}}},
		{obj: &prometheusoperatorv1.ServiceMonitor{}, handler: &handler.EnqueueRequestForOwner{OwnerType: &hyperv1.HostedControlPlane{}}},
		{obj: &rbacv1.Role{}, handler: &handler.EnqueueRequestForOwner{OwnerType: &hyperv1.HostedControlPlane{}}},
		{obj: &rbacv1.RoleBinding{}, handler: &handler.EnqueueRequestForOwner{OwnerType: &hyperv1.HostedControlPlane{}}},
	}
	if r.ManagementClusterCapabilities.Has(capabilities.CapabilityRoute) {
		handlers = append(handlers, eventHandler{obj: &routev1.Route{}, handler: &handler.EnqueueRequestForOwner{OwnerType: &hyperv1.HostedControlPlane{}}})
	}

	return handlers
}

func (r *HostedControlPlaneReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	r.Log = ctrl.LoggerFrom(ctx)
	r.Log.Info("Reconciling")

	// Fetch the hostedControlPlane instance
	hostedControlPlane := &hyperv1.HostedControlPlane{}
	err := r.Client.Get(ctx, req.NamespacedName, hostedControlPlane)
	if err != nil {
		if apierrors.IsNotFound(err) {
			return ctrl.Result{}, nil
		}
		return ctrl.Result{}, err
	}

	// Return early if deleted
	if !hostedControlPlane.DeletionTimestamp.IsZero() {
		if shouldCleanupCloudResources(hostedControlPlane) {
			done, err := r.removeCloudResources(ctx, hostedControlPlane)
			if err != nil {
				return ctrl.Result{}, fmt.Errorf("failed to ensure cloud resources are removed")
			}
			if !done {
				return ctrl.Result{}, nil
			}
		}
		if controllerutil.ContainsFinalizer(hostedControlPlane, finalizer) {
			originalHCP := hostedControlPlane.DeepCopy()
			controllerutil.RemoveFinalizer(hostedControlPlane, finalizer)
			if err := r.Patch(ctx, hostedControlPlane, client.MergeFromWithOptions(originalHCP, client.MergeFromWithOptimisticLock{})); err != nil {
				return ctrl.Result{}, fmt.Errorf("failed to remove finalizer from cluster: %w", err)
			}
		}
		return ctrl.Result{}, nil
	}

	// Ensure the hostedControlPlane has a finalizer for cleanup
	if !controllerutil.ContainsFinalizer(hostedControlPlane, finalizer) {
		originalHCP := hostedControlPlane.DeepCopy()
		controllerutil.AddFinalizer(hostedControlPlane, finalizer)
		if err := r.Patch(ctx, hostedControlPlane, client.MergeFromWithOptions(originalHCP, client.MergeFromWithOptimisticLock{})); err != nil {
			return ctrl.Result{}, fmt.Errorf("failed to add finalizer to hostedControlPlane: %w", err)
		}
	}

	if r.OperateOnReleaseImage != "" && r.OperateOnReleaseImage != hostedControlPlane.Spec.ReleaseImage {
		r.Log.Info("releaseImage is %s, but this operator is configured for %s, skipping reconciliation", hostedControlPlane.Spec.ReleaseImage, r.OperateOnReleaseImage)
		return ctrl.Result{}, nil
	}

	originalHostedControlPlane := hostedControlPlane.DeepCopy()

	// Reconcile global configuration validation status
	{
		condition := metav1.Condition{
			Type:               string(hyperv1.ValidHostedControlPlaneConfiguration),
			ObservedGeneration: hostedControlPlane.Generation,
		}
		if err := r.validateConfigAndClusterCapabilities(hostedControlPlane); err != nil {
			condition.Status = metav1.ConditionFalse
			condition.Message = err.Error()
			condition.Reason = hyperv1.InsufficientClusterCapabilitiesReason
		} else {
			condition.Status = metav1.ConditionTrue
			condition.Message = "Configuration passes validation"
			condition.Reason = hyperv1.HostedClusterAsExpectedReason
		}
		meta.SetStatusCondition(&hostedControlPlane.Status.Conditions, condition)
	}

	// Reconcile etcd cluster status
	{
		newCondition := metav1.Condition{
			Type:   string(hyperv1.EtcdAvailable),
			Status: metav1.ConditionUnknown,
			Reason: hyperv1.EtcdStatusUnknownReason,
		}
		switch hostedControlPlane.Spec.Etcd.ManagementType {
		case hyperv1.Managed:
			r.Log.Info("Reconciling etcd cluster status for managed strategy")
			sts := manifests.EtcdStatefulSet(hostedControlPlane.Namespace)
			if err := r.Get(ctx, client.ObjectKeyFromObject(sts), sts); err != nil {
				if apierrors.IsNotFound(err) {
					newCondition = metav1.Condition{
						Type:   string(hyperv1.EtcdAvailable),
						Status: metav1.ConditionFalse,
						Reason: hyperv1.EtcdStatefulSetNotFoundReason,
					}
				} else {
					return ctrl.Result{}, fmt.Errorf("failed to fetch etcd statefulset %s/%s: %w", sts.Namespace, sts.Name, err)
				}
			} else {
				conditionPtr, err := r.etcdStatefulSetCondition(ctx, sts)
				if err != nil {
					return ctrl.Result{}, fmt.Errorf("failed to get etcd statefulset status: %w", err)
				}
				newCondition = *conditionPtr
			}
		case hyperv1.Unmanaged:
			r.Log.Info("Assuming Etcd cluster is running in unmanaged etcd strategy")
			newCondition = metav1.Condition{
				Type:    string(hyperv1.EtcdAvailable),
				Status:  metav1.ConditionTrue,
				Reason:  "EtcdRunning",
				Message: "Etcd cluster is assumed to be running in unmanaged state",
			}
		}
		newCondition.ObservedGeneration = hostedControlPlane.Generation
		meta.SetStatusCondition(&hostedControlPlane.Status.Conditions, newCondition)
	}

	// Reconcile etcd restore status
	if hostedControlPlane.Spec.Etcd.ManagementType == hyperv1.Managed &&
		hostedControlPlane.Spec.Etcd.Managed != nil && len(hostedControlPlane.Spec.Etcd.Managed.Storage.RestoreSnapshotURL) > 0 {
		restoreCondition := meta.FindStatusCondition(hostedControlPlane.Status.Conditions, string(hyperv1.EtcdSnapshotRestored))
		if restoreCondition == nil {
			r.Log.Info("Reconciling etcd cluster restore status")
			sts := manifests.EtcdStatefulSet(hostedControlPlane.Namespace)
			if err := r.Get(ctx, client.ObjectKeyFromObject(sts), sts); err == nil {
				newCondition := metav1.Condition{}
				conditionPtr := r.etcdRestoredCondition(ctx, sts)
				if conditionPtr != nil {
					newCondition = *conditionPtr
					newCondition.ObservedGeneration = hostedControlPlane.Generation
					meta.SetStatusCondition(&hostedControlPlane.Status.Conditions, newCondition)
				}
			}
		}
	}

	// Reconcile Kube APIServer status
	{
		newCondition := metav1.Condition{
			Type:   string(hyperv1.KubeAPIServerAvailable),
			Status: metav1.ConditionUnknown,
			Reason: hyperv1.StatusUnknownReason,
		}
		deployment := manifests.KASDeployment(hostedControlPlane.Namespace)
		if err := r.Get(ctx, client.ObjectKeyFromObject(deployment), deployment); err != nil {
			if apierrors.IsNotFound(err) {
				newCondition = metav1.Condition{
					Type:    string(hyperv1.KubeAPIServerAvailable),
					Status:  metav1.ConditionFalse,
					Reason:  hyperv1.DeploymentNotFoundReason,
					Message: "Kube APIServer deployment not found",
				}
			} else {
				return ctrl.Result{}, fmt.Errorf("failed to fetch Kube APIServer deployment %s/%s: %w", deployment.Namespace, deployment.Name, err)
			}
		} else {
			// Assume the deployment is unavailable until proven otherwise.
			newCondition = metav1.Condition{
				Type:   string(hyperv1.KubeAPIServerAvailable),
				Status: metav1.ConditionFalse,
				Reason: hyperv1.DeploymentStatusUnknownReason,
			}
			for _, cond := range deployment.Status.Conditions {
				if cond.Type == appsv1.DeploymentAvailable {
					if cond.Status == corev1.ConditionTrue {
						newCondition = metav1.Condition{
							Type:    string(hyperv1.KubeAPIServerAvailable),
							Status:  metav1.ConditionTrue,
							Reason:  hyperv1.AsExpectedReason,
							Message: "Kube APIServer deployment is available",
						}
					} else {
						newCondition = metav1.Condition{
							Type:    string(hyperv1.KubeAPIServerAvailable),
							Status:  metav1.ConditionFalse,
							Reason:  hyperv1.DeploymentWaitingForAvailableReason,
							Message: "Waiting for Kube APIServer deployment to become available",
						}
					}
					break
				}
			}
		}
		newCondition.ObservedGeneration = hostedControlPlane.Generation
		meta.SetStatusCondition(&hostedControlPlane.Status.Conditions, newCondition)
	}

	// Reconcile Degraded status
	{
		condition := metav1.Condition{
			Type:               string(hyperv1.HostedControlPlaneDegraded),
			Status:             metav1.ConditionFalse,
			Reason:             hyperv1.AsExpectedReason,
			ObservedGeneration: hostedControlPlane.Generation,
		}
		cpoManagedDeploymentList := &appsv1.DeploymentList{}
		if err := r.List(ctx, cpoManagedDeploymentList, client.MatchingLabels{
			config.ManagedByLabel: "control-plane-operator",
		}, client.InNamespace(hostedControlPlane.Namespace)); err != nil {
			if !apierrors.IsNotFound(err) {
				return ctrl.Result{}, fmt.Errorf("failed to list managed deployments in namespace %s: %w", hostedControlPlane.Namespace, err)
			}
		}
		var errs []error
		sort.SliceStable(cpoManagedDeploymentList.Items, func(i, j int) bool {
			return cpoManagedDeploymentList.Items[i].Name < cpoManagedDeploymentList.Items[j].Name
		})
		for _, deployment := range cpoManagedDeploymentList.Items {
			if deployment.Status.UnavailableReplicas > 0 {
				errs = append(errs, fmt.Errorf("%s deployment has %d unavailable replicas", deployment.Name, deployment.Status.UnavailableReplicas))
			}
		}
		err := utilerrors.NewAggregate(errs)
		if err != nil {
			condition.Status = metav1.ConditionTrue
			condition.Reason = "UnavailableReplicas"
			condition.Message = err.Error()
		}
		meta.SetStatusCondition(&hostedControlPlane.Status.Conditions, condition)
	}

	// Reconcile infrastructure status
	{
		r.Log.Info("Reconciling infrastructure status")
		newCondition := metav1.Condition{
			Type:   string(hyperv1.InfrastructureReady),
			Status: metav1.ConditionUnknown,
			Reason: hyperv1.StatusUnknownReason,
		}
		infraStatus, err := r.reconcileInfrastructureStatus(ctx, hostedControlPlane)
		if err != nil {
			newCondition = metav1.Condition{
				Type:    string(hyperv1.InfrastructureReady),
				Status:  metav1.ConditionUnknown,
				Reason:  "InfraStatusFailure",
				Message: err.Error(),
			}
			r.Log.Error(err, "failed to determine infrastructure status")
		} else {
			if infraStatus.IsReady() {
				hostedControlPlane.Status.ControlPlaneEndpoint = hyperv1.APIEndpoint{
					Host: infraStatus.APIHost,
					Port: infraStatus.APIPort,
				}
				newCondition = metav1.Condition{
					Type:   string(hyperv1.InfrastructureReady),
					Status: metav1.ConditionTrue,
					Reason: hyperv1.AsExpectedReason,
				}
				hostedControlPlane.Status.OAuthCallbackURLTemplate = fmt.Sprintf("https://%s:%d/oauthcallback/[identity-provider-name]", infraStatus.OAuthHost, infraStatus.OAuthPort)
			} else {
				message := "Cluster infrastructure is still provisioning"
				if len(infraStatus.Message) > 0 {
					message = infraStatus.Message
				}
				newCondition = metav1.Condition{
					Type:    string(hyperv1.InfrastructureReady),
					Status:  metav1.ConditionFalse,
					Reason:  "WaitingOnInfrastructureReady",
					Message: message,
				}
				r.Log.Info("Infrastructure is not yet ready")
			}
		}
		newCondition.ObservedGeneration = hostedControlPlane.Generation
		meta.SetStatusCondition(&hostedControlPlane.Status.Conditions, newCondition)
	}

	// Reconcile hostedcontrolplane availability and Ready flag
	{
		infrastructureCondition := meta.FindStatusCondition(hostedControlPlane.Status.Conditions, string(hyperv1.InfrastructureReady))
		kubeConfigAvailable := hostedControlPlane.Status.KubeConfig != nil
		etcdCondition := meta.FindStatusCondition(hostedControlPlane.Status.Conditions, string(hyperv1.EtcdAvailable))
		kubeAPIServerCondition := meta.FindStatusCondition(hostedControlPlane.Status.Conditions, string(hyperv1.KubeAPIServerAvailable))

		status := metav1.ConditionFalse
		var reason, message string
		switch {
		case infrastructureCondition == nil && etcdCondition == nil && kubeAPIServerCondition == nil:
			reason = hyperv1.StatusUnknownReason
			message = ""
		case infrastructureCondition != nil && infrastructureCondition.Status == metav1.ConditionFalse:
			reason = infrastructureCondition.Reason
			message = infrastructureCondition.Message
		case !kubeConfigAvailable:
			reason = hyperv1.KubeconfigWaitingForCreateReason
			message = "Waiting for hosted control plane kubeconfig to be created"
		case etcdCondition != nil && etcdCondition.Status == metav1.ConditionFalse:
			reason = etcdCondition.Reason
			message = etcdCondition.Message
		case kubeAPIServerCondition != nil && kubeAPIServerCondition.Status == metav1.ConditionFalse:
			reason = kubeAPIServerCondition.Reason
			message = kubeAPIServerCondition.Message
		default:
			reason = hyperv1.AsExpectedReason
			message = ""
			status = metav1.ConditionTrue
		}
		hostedControlPlane.Status.Ready = (status == metav1.ConditionTrue)
		condition := metav1.Condition{
			Type:               string(hyperv1.HostedControlPlaneAvailable),
			Status:             status,
			Reason:             reason,
			Message:            message,
			ObservedGeneration: hostedControlPlane.Generation,
		}
		meta.SetStatusCondition(&hostedControlPlane.Status.Conditions, condition)
	}

	kubeconfig := manifests.KASExternalKubeconfigSecret(hostedControlPlane.Namespace, hostedControlPlane.Spec.KubeConfig)
	if err := r.Get(ctx, client.ObjectKeyFromObject(kubeconfig), kubeconfig); err != nil {
		if !apierrors.IsNotFound(err) {
			return reconcile.Result{}, err
		}
	} else {
		hostedControlPlane.Status.KubeConfig = &hyperv1.KubeconfigSecretRef{
			Name: kubeconfig.Name,
			Key:  DefaultAdminKubeconfigKey,
		}
		if hostedControlPlane.Spec.KubeConfig != nil {
			hostedControlPlane.Status.KubeConfig.Key = hostedControlPlane.Spec.KubeConfig.Key
		}
	}

	kubeadminPassword := common.KubeadminPasswordSecret(hostedControlPlane.Namespace)
	if err := r.Get(ctx, client.ObjectKeyFromObject(kubeadminPassword), kubeadminPassword); err != nil {
		if !apierrors.IsNotFound(err) {
			return reconcile.Result{}, fmt.Errorf("failed to get kubeadmin password: %w", err)
		}
	} else {
		hostedControlPlane.Status.KubeadminPassword = &corev1.LocalObjectReference{
			Name: kubeadminPassword.Name,
		}
	}

	hostedControlPlane.Status.Initialized = true

	meta.SetStatusCondition(&hostedControlPlane.Status.Conditions, util.GenerateReconciliationActiveCondition(hostedControlPlane.Spec.PausedUntil, hostedControlPlane.Generation))
	// Always update status based on the current state of the world.
	if err := r.Client.Status().Patch(ctx, hostedControlPlane, client.MergeFromWithOptions(originalHostedControlPlane, client.MergeFromWithOptimisticLock{})); err != nil {
		return ctrl.Result{}, fmt.Errorf("failed to update status: %w", err)
	}
	if isPaused, duration := util.IsReconciliationPaused(r.Log, hostedControlPlane.Spec.PausedUntil); isPaused {
		r.Log.Info("Reconciliation paused", "pausedUntil", *hostedControlPlane.Spec.PausedUntil)
		return ctrl.Result{
			RequeueAfter: duration,
		}, nil
	}

	// Perform the hosted control plane reconciliation
	result, err := r.update(ctx, hostedControlPlane)
	if err != nil {
		return ctrl.Result{}, fmt.Errorf("failed to update control plane: %w", err)
	}

	r.Log.Info("Successfully reconciled")
	return result, nil
}

func (r *HostedControlPlaneReconciler) validateConfigAndClusterCapabilities(hc *hyperv1.HostedControlPlane) error {
	for _, svc := range hc.Spec.Services {
		if svc.Type == hyperv1.Route && !r.ManagementClusterCapabilities.Has(capabilities.CapabilityRoute) {
			return fmt.Errorf("cluster does not support Routes, but service %q is exposed via a Route", svc.Service)
		}
	}
	return nil
}

func (r *HostedControlPlaneReconciler) LookupReleaseImage(ctx context.Context, hcp *hyperv1.HostedControlPlane) (*releaseinfo.ReleaseImage, error) {
	pullSecret := common.PullSecret(hcp.Namespace)
	if err := r.Client.Get(ctx, client.ObjectKeyFromObject(pullSecret), pullSecret); err != nil {
		return nil, err
	}
	lookupCtx, lookupCancel := context.WithTimeout(ctx, 2*time.Minute)
	defer lookupCancel()
	return r.ReleaseProvider.Lookup(lookupCtx, hcp.Spec.ReleaseImage, pullSecret.Data[corev1.DockerConfigJsonKey])
}

func (r *HostedControlPlaneReconciler) update(ctx context.Context, hostedControlPlane *hyperv1.HostedControlPlane) (reconcile.Result, error) {

	// Block here if the cluster configuration does not pass validation
	{
		validConfig := meta.FindStatusCondition(hostedControlPlane.Status.Conditions, string(hyperv1.ValidHostedControlPlaneConfiguration))
		if validConfig != nil && validConfig.Status == metav1.ConditionFalse {
			r.Log.Info("Configuration is invalid, reconciliation is blocked")
			return reconcile.Result{}, nil
		}
	}

	r.Log.Info("Looking up release image metadata", "image", hostedControlPlane.Spec.ReleaseImage)
	releaseImage, err := r.LookupReleaseImage(ctx, hostedControlPlane)
	if err != nil {
		return reconcile.Result{}, fmt.Errorf("failed to look up release image metadata: %w", err)
	}
	componentVersions, err := releaseImage.ComponentVersions()
	if err != nil {
		return reconcile.Result{}, fmt.Errorf("invalid component versions found in release info: %w", err)
	}
	r.Log.Info("Found release info for image", "releaseImage", hostedControlPlane.Spec.ReleaseImage, "info", releaseImage, "componentImages", len(releaseImage.ComponentImages()), "componentVersions", componentVersions)

	createOrUpdate := r.createOrUpdate(hostedControlPlane)

	if util.IsPrivateHCP(hostedControlPlane) {
		r.Log.Info("Removing private IngressController")
		// Ensure that if an ingress controller exists from a previous version, it is removed
		if err = r.reconcilePrivateIngressController(ctx, hostedControlPlane); err != nil {
			return reconcile.Result{}, fmt.Errorf("failed to reconcile private ingresscontroller: %w", err)
		}
	}

	// Reconcile router
	kasServiceStrategy := servicePublishingStrategyByType(hostedControlPlane, hyperv1.APIServer)
	if util.IsPrivateHCP(hostedControlPlane) || util.HasPublicLoadBalancerForPrivateRouter(hostedControlPlane) {
		r.Log.Info("Reconciling router")
		if err := r.reconcileRouter(ctx, hostedControlPlane, releaseImage, createOrUpdate, kasServiceStrategy.Type == hyperv1.Route); err != nil {
			return reconcile.Result{}, fmt.Errorf("failed to reconcile router: %w", err)
		}
	}

	r.Log.Info("Reconciling autoscaler")
	if err := r.reconcileAutoscaler(ctx, hostedControlPlane, releaseImage.ComponentImages(), createOrUpdate); err != nil {
		return reconcile.Result{}, fmt.Errorf("failed to reconcile autoscaler: %w", err)
	}

	r.Log.Info("Reconciling machine approver")
	if err := r.reconcileMachineApprover(ctx, hostedControlPlane, releaseImage.ComponentImages(), createOrUpdate); err != nil {
		return reconcile.Result{}, fmt.Errorf("failed to reconcile machine approver: %w", err)
	}

	r.Log.Info("Reconciling infrastructure services")
	if err := r.reconcileInfrastructure(ctx, hostedControlPlane, createOrUpdate); err != nil {
		return reconcile.Result{}, fmt.Errorf("failed to ensure infrastructure: %w", err)
	}
	// Block here until infra status reports readiness
	// TODO(dmace): This seems a bit heavy handed vs. making more granular bits no-op if
	// they don't have the specific required inputs
	infraStatus, err := r.reconcileInfrastructureStatus(ctx, hostedControlPlane)
	if err != nil {
		return reconcile.Result{}, fmt.Errorf("failed to look up infra status: %w", err)
	}
	if !infraStatus.IsReady() {
		r.Log.Info("Waiting for infrastructure to be ready before proceeding")
		return reconcile.Result{RequeueAfter: time.Minute}, nil
	}

	return reconcile.Result{}, r.reconcile(ctx, hostedControlPlane, createOrUpdate, releaseImage, infraStatus)
}

func (r *HostedControlPlaneReconciler) reconcile(ctx context.Context, hostedControlPlane *hyperv1.HostedControlPlane, createOrUpdate upsert.CreateOrUpdateFN, releaseImage *releaseinfo.ReleaseImage, infraStatus InfrastructureStatus) error {
	r.Log.Info("Reconciling ignition server")
	if err := ignitionserver.ReconcileIgnitionServer(ctx,
		r.Client,
		createOrUpdate,
		releaseImage.ComponentImages()[util.CPOImageName],
		hostedControlPlane,
		r.DefaultIngressDomain,
		r.ManagementClusterCapabilities.Has(capabilities.CapabilitySecurityContextConstraint),
		r.ReleaseProvider.GetRegistryOverrides(),
		// The healthz handler was added before the CPO started to mange te ignition server and its the same binary,
		// so we know it always exists here.
		true,
	); err != nil {
		return fmt.Errorf("failed to reconcile ignition server: %w", err)
	}

	// Reconcile default service account
	r.Log.Info("Reconciling default service account")
	if err := r.reconcileDefaultServiceAccount(ctx, hostedControlPlane, createOrUpdate); err != nil {
		return fmt.Errorf("failed to reconcile default service account: %w", err)
	}

	// Reconcile PKI
	if _, exists := hostedControlPlane.Annotations[hyperv1.DisablePKIReconciliationAnnotation]; !exists {
		r.Log.Info("Reconciling PKI")
		if err := r.reconcilePKI(ctx, hostedControlPlane, infraStatus, createOrUpdate); err != nil {
			return fmt.Errorf("failed to reconcile PKI: %w", err)
		}
	}

	// Reconcile Cloud Provider Config
	r.Log.Info("Reconciling cloud provider config")
	if err := r.reconcileCloudProviderConfig(ctx, hostedControlPlane, createOrUpdate); err != nil {
		return fmt.Errorf("failed to reconcile cloud provider config: %w", err)
	}

	r.Log.Info("Looking up observed configuration")
	observedConfig := &globalconfig.ObservedConfig{}
	if err := globalconfig.ReadObservedConfig(ctx, r.Client, observedConfig, hostedControlPlane.Namespace); err != nil {
		return fmt.Errorf("failed to read observed global config: %w", err)
	}

	// Reconcile etcd
	r.Log.Info("Reconciling Etcd")

	switch hostedControlPlane.Spec.Etcd.ManagementType {
	case hyperv1.Managed:
		if err := r.reconcileManagedEtcd(ctx, hostedControlPlane, releaseImage, createOrUpdate); err != nil {
			return fmt.Errorf("failed to reconcile etcd: %w", err)
		}
	case hyperv1.Unmanaged:
		if err := r.reconcileUnmanagedEtcd(ctx, hostedControlPlane, createOrUpdate); err != nil {
			return fmt.Errorf("failed to reconcile etcd: %w", err)
		}
	default:
		return fmt.Errorf("unrecognized etcd management type: %s", hostedControlPlane.Spec.Etcd.ManagementType)
	}

	// Reconcile Konnectivity
	r.Log.Info("Reconciling Konnectivity")
	if err := r.reconcileKonnectivity(ctx, hostedControlPlane, releaseImage, infraStatus, createOrUpdate); err != nil {
		return fmt.Errorf("failed to reconcile konnectivity: %w", err)
	}

	// Reconcile kube apiserver
	r.Log.Info("Reconciling Kube API Server")
	if err := r.reconcileKubeAPIServer(ctx, hostedControlPlane, releaseImage, infraStatus.APIHost, infraStatus.APIPort, infraStatus.OAuthHost, infraStatus.OAuthPort, createOrUpdate); err != nil {
		return fmt.Errorf("failed to reconcile kube apiserver: %w", err)
	}

	// Block until kube apiserver is fully ready to enforce upgrade order of version skew policy
	// https://kubernetes.io/releases/version-skew-policy/#supported-component-upgrade-order
	ready, err := util.IsDeploymentReady(ctx, r, manifests.KASDeployment(hostedControlPlane.Namespace))
	if err != nil {
		return fmt.Errorf("failed to check kube apiserver availability: %w", err)
	}
	if !ready {
		r.Log.Info("Waiting for kube apiserver deployment to become ready")
		return nil
	}

	// Reconcile kube controller manager
	r.Log.Info("Reconciling Kube Controller Manager")
	if err := r.reconcileKubeControllerManager(ctx, hostedControlPlane, releaseImage, createOrUpdate); err != nil {
		return fmt.Errorf("failed to reconcile kube controller manager: %w", err)
	}

	// Reconcile kube scheduler
	r.Log.Info("Reconciling Kube Scheduler")
	if err := r.reconcileKubeScheduler(ctx, hostedControlPlane, releaseImage, createOrUpdate); err != nil {
		return fmt.Errorf("failed to reconcile kube controller manager: %w", err)
	}

	// Reconcile openshift apiserver
	r.Log.Info("Reconciling OpenShift API Server")
	if err := r.reconcileOpenShiftAPIServer(ctx, hostedControlPlane, observedConfig, releaseImage, infraStatus.OpenShiftAPIHost, createOrUpdate); err != nil {
		return fmt.Errorf("failed to reconcile openshift apiserver: %w", err)
	}

	// Block until openshift apiserver is fully ready to enforce upgrade order of version skew policy
	// https://github.com/openshift/enhancements/blob/master/enhancements/update/eus-upgrades-mvp.md
	ready, err = util.IsDeploymentReady(ctx, r, manifests.OpenShiftAPIServerDeployment(hostedControlPlane.Namespace))
	if err != nil {
		return fmt.Errorf("failed to check openshift apiserver availability: %w", err)
	}
	if !ready {
		r.Log.Info("Waiting for openshift apiserver deployment to become ready")
		return nil
	}

	// Reconcile openshift oauth apiserver
	r.Log.Info("Reconciling OpenShift OAuth API Server")
	if err := r.reconcileOpenShiftOAuthAPIServer(ctx, hostedControlPlane, observedConfig, releaseImage, infraStatus.OauthAPIServerHost, createOrUpdate); err != nil {
		return fmt.Errorf("failed to reconcile openshift oauth apiserver: %w", err)
	}

	// Reconcile oauth server
	r.Log.Info("Reconciling OAuth Server")
	if err = r.reconcileOAuthServer(ctx, hostedControlPlane, releaseImage, infraStatus.OAuthHost, infraStatus.OAuthPort, createOrUpdate); err != nil {
		return fmt.Errorf("failed to reconcile openshift oauth apiserver: %w", err)
	}

	// Reconcile openshift controller manager
	r.Log.Info("Reconciling OpenShift Controller Manager")
	if err = r.reconcileOpenShiftControllerManager(ctx, hostedControlPlane, observedConfig, releaseImage, createOrUpdate); err != nil {
		return fmt.Errorf("failed to reconcile openshift controller manager: %w", err)
	}

	// Reconcile cluster policy controller
	r.Log.Info("Reconciling Cluster Policy Controller")
	if err = r.reconcileClusterPolicyController(ctx, hostedControlPlane, releaseImage, createOrUpdate); err != nil {
		return fmt.Errorf("failed to reconcile cluster policy controller: %w", err)
	}

	// Reconcile cluster version operator
	r.Log.Info("Reconciling Cluster Version Operator")
	if err = r.reconcileClusterVersionOperator(ctx, hostedControlPlane, releaseImage, createOrUpdate); err != nil {
		return fmt.Errorf("failed to reconcile cluster version operator: %w", err)
	}

	r.Log.Info("Reconciling ClusterNetworkOperator")
	if err := r.reconcileClusterNetworkOperator(ctx, hostedControlPlane, releaseImage, createOrUpdate); err != nil {
		return fmt.Errorf("failed to reconcile cluster network operator: %w", err)
	}

	r.Log.Info("Reconciling IngressOperator")
	if err := r.reconcileIngressOperator(ctx, hostedControlPlane, releaseImage, createOrUpdate); err != nil {
		return fmt.Errorf("failed to reconcile ingress operator: %w", err)
	}

	// Reconcile hosted cluster config operator
	r.Log.Info("Reconciling Hosted Cluster Config Operator")
	if err = r.reconcileHostedClusterConfigOperator(ctx, hostedControlPlane, releaseImage, infraStatus, createOrUpdate); err != nil {
		return fmt.Errorf("failed to reconcile hosted cluster config operator: %w", err)
	}

	// Reconcile cloud controller manager
	r.Log.Info("Reconciling Cloud Controller Manager")
	if err := r.reconcileCloudControllerManager(ctx, hostedControlPlane, releaseImage, createOrUpdate); err != nil {
		return fmt.Errorf("failed to reconcile cloud controller manager: %w", err)
	}

	// Reconcile OLM
	r.Log.Info("Reconciling OLM")
	if err = r.reconcileOperatorLifecycleManager(ctx, hostedControlPlane, releaseImage, infraStatus.PackageServerAPIAddress, createOrUpdate); err != nil {
		return fmt.Errorf("failed to reconcile olm: %w", err)
	}

	// Reconcile image registry operator
	r.Log.Info("Reconciling Image Registry Operator")
	if err = r.reconcileImageRegistryOperator(ctx, hostedControlPlane, releaseImage, createOrUpdate); err != nil {
		return fmt.Errorf("failed to reconcile image registry operator: %w", err)
	}

	// Reconcile Ignition
	r.Log.Info("Reconciling core machine configs")
	if err = r.reconcileCoreIgnitionConfig(ctx, hostedControlPlane, createOrUpdate); err != nil {
		return fmt.Errorf("failed to reconcile ignition: %w", err)
	}

	// Reconcle machine config server config
	r.Log.Info("Reconciling machine config server config")
	if err = r.reconcileMachineConfigServerConfig(ctx, hostedControlPlane, createOrUpdate); err != nil {
		return fmt.Errorf("failed to reconcile mcs config: %w", err)
	}

	// Reconcile kubeadmin password
	r.Log.Info("Reconciling kubeadmin password secret")
	explicitOauthConfig := hostedControlPlane.Spec.Configuration != nil && hostedControlPlane.Spec.Configuration.OAuth != nil
	if err := r.reconcileKubeadminPassword(ctx, hostedControlPlane, explicitOauthConfig, createOrUpdate); err != nil {
		return fmt.Errorf("failed to ensure control plane: %w", err)
	}

	return nil
}

func servicePublishingStrategyByType(hcp *hyperv1.HostedControlPlane, svcType hyperv1.ServiceType) *hyperv1.ServicePublishingStrategy {
	for _, mapping := range hcp.Spec.Services {
		if mapping.Service == svcType {
			return &mapping.ServicePublishingStrategy
		}
	}
	return nil
}

func (r *HostedControlPlaneReconciler) reconcileDefaultServiceAccount(ctx context.Context, hcp *hyperv1.HostedControlPlane, createOrUpdate upsert.CreateOrUpdateFN) error {
	defaultSA := common.DefaultServiceAccount(hcp.Namespace)
	if _, err := createOrUpdate(ctx, r.Client, defaultSA, func() error {
		util.EnsurePullSecret(defaultSA, common.PullSecret(hcp.Namespace).Name)
		return nil
	}); err != nil {
		return err
	}
	return nil
}

func (r *HostedControlPlaneReconciler) reconcileAPIServerService(ctx context.Context, hcp *hyperv1.HostedControlPlane, createOrUpdate upsert.CreateOrUpdateFN) error {
	serviceStrategy := servicePublishingStrategyByType(hcp, hyperv1.APIServer)
	if serviceStrategy == nil {
		return errors.New("APIServer service strategy not specified")
	}
	p := kas.NewKubeAPIServerServiceParams(hcp)
	apiServerService := manifests.KubeAPIServerService(hcp.Namespace)
	if _, err := createOrUpdate(ctx, r.Client, apiServerService, func() error {
		return kas.ReconcileService(apiServerService, serviceStrategy, p.OwnerReference, p.APIServerPort, p.AllowedCIDRBlocks, util.IsPublicHCP(hcp))
	}); err != nil {
		return fmt.Errorf("failed to reconcile API server service: %w", err)
	}

	if serviceStrategy.Type == hyperv1.Route {
		if util.IsPublicHCP(hcp) {
			externalRoute := manifests.KubeAPIServerExternalRoute(hcp.Namespace)
			if _, err := createOrUpdate(ctx, r.Client, externalRoute, func() error {
				kas.ReconcileRoute(externalRoute, serviceStrategy.Route.Hostname)
				if externalRoute.Annotations == nil {
					externalRoute.Annotations = map[string]string{}
				}
				externalRoute.Annotations["external-dns.alpha.kubernetes.io/hostname"] = serviceStrategy.Route.Hostname
				return nil
			}); err != nil {
				return fmt.Errorf("failed to reconcile apiserver external route: %w", err)
			}
		}

		// We do not need to enumerate all possible addresses, because we use the KAS as default backend through a custom
		// router template. That in turn was needed to work around SNI not supporting IP addresses, only hostnames:
		// https://www.rfc-editor.org/rfc/rfc6066#section-3
		route := manifests.KubeAPIServerInternalRoute(hcp.Namespace)
		if _, err := createOrUpdate(ctx, r.Client, route, func() error {
			kas.ReconcileRoute(route, "kubernetes.default")
			return nil
		}); err != nil {
			return fmt.Errorf("failed to reconcile apiserver route %s: %w", route.Name, err)
		}

	} else if serviceStrategy.Type == hyperv1.LoadBalancer && util.IsPrivateHCP(hcp) {
		apiServerPrivateService := manifests.KubeAPIServerPrivateService(hcp.Namespace)
		if _, err := createOrUpdate(ctx, r.Client, apiServerPrivateService, func() error {
			return kas.ReconcilePrivateService(apiServerPrivateService, hcp, p.OwnerReference)
		}); err != nil {
			return fmt.Errorf("failed to reconcile API server private service: %w", err)
		}
	}

	return nil
}

func (r *HostedControlPlaneReconciler) reconcileKonnectivityServerService(ctx context.Context, hcp *hyperv1.HostedControlPlane, createOrUpdate upsert.CreateOrUpdateFN) error {
	p := konnectivity.NewKonnectivityServiceParams(hcp)
	serviceStrategy := servicePublishingStrategyByType(hcp, hyperv1.Konnectivity)
	if serviceStrategy == nil {
		//lint:ignore ST1005 Konnectivity is proper name
		return fmt.Errorf("Konnectivity service strategy not specified")
	}
	konnectivityServerService := manifests.KonnectivityServerService(hcp.Namespace)
	if _, err := createOrUpdate(ctx, r.Client, konnectivityServerService, func() error {
		return konnectivity.ReconcileServerService(konnectivityServerService, p.OwnerRef, serviceStrategy)
	}); err != nil {
		return fmt.Errorf("failed to reconcile Konnectivity service: %w", err)
	}
	if serviceStrategy.Type != hyperv1.Route {
		return nil
	}
	konnectivityRoute := manifests.KonnectivityServerRoute(hcp.Namespace)
	if _, err := createOrUpdate(ctx, r.Client, konnectivityRoute, func() error {
		return konnectivity.ReconcileRoute(konnectivityRoute, p.OwnerRef, util.IsPrivateHCP(hcp), serviceStrategy, r.DefaultIngressDomain)
	}); err != nil {
		return fmt.Errorf("failed to reconcile Konnectivity server route: %w", err)
	}
	return nil
}

func (r *HostedControlPlaneReconciler) reconcileOAuthServerService(ctx context.Context, hcp *hyperv1.HostedControlPlane, createOrUpdate upsert.CreateOrUpdateFN) error {
	serviceStrategy := servicePublishingStrategyByType(hcp, hyperv1.OAuthServer)
	if serviceStrategy == nil {
		return fmt.Errorf("OAuthServer service strategy not specified")
	}
	p := oauth.NewOAuthServiceParams(hcp)
	oauthServerService := manifests.OauthServerService(hcp.Namespace)
	if _, err := createOrUpdate(ctx, r.Client, oauthServerService, func() error {
		return oauth.ReconcileService(oauthServerService, p.OwnerRef, serviceStrategy)
	}); err != nil {
		return fmt.Errorf("failed to reconcile OAuth service: %w", err)
	}
	if serviceStrategy.Type != hyperv1.Route {
		return nil
	}
	oauthRoute := manifests.OauthServerRoute(hcp.Namespace)
	if _, err := createOrUpdate(ctx, r.Client, oauthRoute, func() error {
		return oauth.ReconcileRoute(oauthRoute, p.OwnerRef, serviceStrategy, r.DefaultIngressDomain, hcp)
	}); err != nil {
		return fmt.Errorf("failed to reconcile OAuth route: %w", err)
	}
	return nil
}

func (r *HostedControlPlaneReconciler) reconcileOpenshiftAPIServerService(ctx context.Context, hcp *hyperv1.HostedControlPlane, createOrUpdate upsert.CreateOrUpdateFN) error {
	svc := manifests.OpenshiftAPIServerService(hcp.Namespace)
	p := oapi.NewOpenShiftAPIServerServiceParams(hcp)
	if _, err := createOrUpdate(ctx, r.Client, svc, func() error {
		return oapi.ReconcileOpenShiftAPIService(svc, p.OwnerRef)
	}); err != nil {
		return fmt.Errorf("failed to reconcile OpenShift API server service: %w", err)
	}
	return nil
}

func (r *HostedControlPlaneReconciler) reconcileOAuthAPIServerService(ctx context.Context, hcp *hyperv1.HostedControlPlane, createOrUpdate upsert.CreateOrUpdateFN) error {
	svc := manifests.OauthAPIServerService(hcp.Namespace)
	p := oapi.NewOpenShiftAPIServerServiceParams(hcp)
	if _, err := createOrUpdate(ctx, r.Client, svc, func() error {
		return oapi.ReconcileOAuthAPIService(svc, p.OwnerRef)
	}); err != nil {
		return fmt.Errorf("failed to reconcile OAuth API server service: %w", err)
	}
	return nil
}

func (r *HostedControlPlaneReconciler) reconcileOLMPackageServerService(ctx context.Context, hcp *hyperv1.HostedControlPlane, createOrUpdate upsert.CreateOrUpdateFN) error {
	svc := manifests.OLMPackageServerService(hcp.Namespace)
	p := oapi.NewOpenShiftAPIServerServiceParams(hcp)
	_, err := createOrUpdate(ctx, r.Client, svc, func() error {
		return oapi.ReconcileOLMPackageServerService(svc, p.OwnerRef)
	})
	if err != nil {
		return err
	}
	return nil
}

func (r *HostedControlPlaneReconciler) reconcileInfrastructure(ctx context.Context, hcp *hyperv1.HostedControlPlane, createOrUpdate upsert.CreateOrUpdateFN) error {
	if hcp.Spec.Services == nil {
		return fmt.Errorf("service publishing strategy undefined")
	}
	if err := r.reconcileAPIServerService(ctx, hcp, createOrUpdate); err != nil {
		return fmt.Errorf("failed to reconcile API server service: %w", err)
	}
	if err := r.reconcileKonnectivityServerService(ctx, hcp, createOrUpdate); err != nil {
		return fmt.Errorf("failed to reconcile Konnectivity servier service: %w", err)
	}
	if err := r.reconcileOAuthServerService(ctx, hcp, createOrUpdate); err != nil {
		return fmt.Errorf("failed to reconcile OAuth server service: %w", err)
	}
	if err := r.reconcileOpenshiftAPIServerService(ctx, hcp, createOrUpdate); err != nil {
		return fmt.Errorf("failed to reconcile OpenShift api service: %w", err)
	}
	if err := r.reconcileOAuthAPIServerService(ctx, hcp, createOrUpdate); err != nil {
		return fmt.Errorf("failed to reconcile OpenShift OAuth api service: %w", err)
	}
	if err := r.reconcileOLMPackageServerService(ctx, hcp, createOrUpdate); err != nil {
		return fmt.Errorf("failed to reconcile OLM PackageServer service: %w", err)
	}

	return nil
}

func (r *HostedControlPlaneReconciler) defaultReconcileInfrastructureStatus(ctx context.Context, hcp *hyperv1.HostedControlPlane) (InfrastructureStatus, error) {
	var (
		infraStatus InfrastructureStatus
		errs        []error
		err         error
		msg         string
		messages    []string
	)
	if infraStatus.APIHost, infraStatus.APIPort, msg, err = r.reconcileAPIServerServiceStatus(ctx, hcp); err != nil {
		errs = append(errs, err)
	}
	if len(msg) > 0 {
		messages = append(messages, msg)
	}
	if infraStatus.KonnectivityHost, infraStatus.KonnectivityPort, msg, err = r.reconcileKonnectivityServiceStatus(ctx, hcp); err != nil {
		errs = append(errs, err)
	}
	if len(msg) > 0 {
		messages = append(messages, msg)
	}
	if infraStatus.OAuthHost, infraStatus.OAuthPort, msg, err = r.reconcileOAuthServiceStatus(ctx, hcp); err != nil {
		errs = append(errs, err)
	}
	if len(msg) > 0 {
		messages = append(messages, msg)
	}
	if infraStatus.OpenShiftAPIHost, err = r.reconcileOpenShiftAPIServerServiceStatus(ctx, hcp); err != nil {
		errs = append(errs, err)
	}
	if infraStatus.OauthAPIServerHost, err = r.reconcileOAuthAPIServerServiceStatus(ctx, hcp); err != nil {
		errs = append(errs, err)
	}
	if infraStatus.PackageServerAPIAddress, err = r.reconcileOLMPackageServerServiceStatus(ctx, hcp); err != nil {
		errs = append(errs, err)
	}

	if len(messages) > 0 {
		infraStatus.Message = strings.Join(messages, "; ")
	}

	return infraStatus, utilerrors.NewAggregate(errs)
}

func (r *HostedControlPlaneReconciler) reconcileAPIServerServiceStatus(ctx context.Context, hcp *hyperv1.HostedControlPlane) (host string, port int32, message string, err error) {
	serviceStrategy := servicePublishingStrategyByType(hcp, hyperv1.APIServer)
	if serviceStrategy == nil {
		return "", 0, "", errors.New("APIServer service strategy not specified")
	}

	if util.IsPublicHCP(hcp) {
		var svc *corev1.Service
		if serviceStrategy.Type == hyperv1.Route {
			svc = manifests.RouterPublicService(hcp.Namespace)
		} else {
			svc = manifests.KubeAPIServerService(hcp.Namespace)
		}
		if err = r.Get(ctx, client.ObjectKeyFromObject(svc), svc); err != nil {
			if apierrors.IsNotFound(err) {
				err = nil
				return
			}
			err = fmt.Errorf("failed to get kube apiserver service: %w", err)
			return
		}
		p := kas.NewKubeAPIServerServiceParams(hcp)
		return kas.ReconcileServiceStatus(svc, serviceStrategy, p.APIServerPort, events.NewMessageCollector(ctx, r.Client))
	}

	host, port, err = kas.ReconcilePrivateServiceStatus(hcp)
	return
}

func (r *HostedControlPlaneReconciler) reconcileKonnectivityServiceStatus(ctx context.Context, hcp *hyperv1.HostedControlPlane) (host string, port int32, message string, err error) {
	serviceStrategy := servicePublishingStrategyByType(hcp, hyperv1.Konnectivity)
	if serviceStrategy == nil {
		err = fmt.Errorf("konnectivity service strategy not specified")
		return
	}
	svc := manifests.KonnectivityServerService(hcp.Namespace)
	if err = r.Get(ctx, client.ObjectKeyFromObject(svc), svc); err != nil {
		if apierrors.IsNotFound(err) {
			err = nil
			return
		}
		err = fmt.Errorf("failed to get konnectivity service: %w", err)
		return
	}
	var route *routev1.Route
	if serviceStrategy.Type == hyperv1.Route {
		route = manifests.KonnectivityServerRoute(hcp.Namespace)
		if err = r.Get(ctx, client.ObjectKeyFromObject(route), route); err != nil {
			if apierrors.IsNotFound(err) {
				err = nil
				return
			}
			err = fmt.Errorf("failed to get konnectivity route: %w", err)
			return
		}
	}
	return konnectivity.ReconcileServerServiceStatus(svc, route, serviceStrategy, events.NewMessageCollector(ctx, r.Client))
}

func (r *HostedControlPlaneReconciler) reconcileOAuthServiceStatus(ctx context.Context, hcp *hyperv1.HostedControlPlane) (host string, port int32, message string, err error) {
	serviceStrategy := servicePublishingStrategyByType(hcp, hyperv1.OAuthServer)
	if serviceStrategy == nil {
		err = fmt.Errorf("OAuth strategy not specified")
		return
	}
	var route *routev1.Route
	svc := manifests.OauthServerService(hcp.Namespace)
	if err = r.Get(ctx, client.ObjectKeyFromObject(svc), svc); err != nil {
		if apierrors.IsNotFound(err) {
			err = nil
			return
		}
		err = fmt.Errorf("failed to get oauth service: %w", err)
		return
	}
	if serviceStrategy.Type == hyperv1.Route {
		route = manifests.OauthServerRoute(hcp.Namespace)
		if err = r.Get(ctx, client.ObjectKeyFromObject(route), route); err != nil {
			if apierrors.IsNotFound(err) {
				err = nil
				return
			}
			err = fmt.Errorf("failed to get oauth route: %w", err)
			return
		}
	}
	return oauth.ReconcileServiceStatus(svc, route, serviceStrategy)
}

func (r *HostedControlPlaneReconciler) reconcileOpenShiftAPIServerServiceStatus(ctx context.Context, hcp *hyperv1.HostedControlPlane) (string, error) {
	svc := manifests.OpenshiftAPIServerService(hcp.Namespace)
	return r.reconcileClusterIPServiceStatus(ctx, svc)
}

func (r *HostedControlPlaneReconciler) reconcileOAuthAPIServerServiceStatus(ctx context.Context, hcp *hyperv1.HostedControlPlane) (string, error) {
	svc := manifests.OauthAPIServerService(hcp.Namespace)
	return r.reconcileClusterIPServiceStatus(ctx, svc)
}

func (r *HostedControlPlaneReconciler) reconcileOLMPackageServerServiceStatus(ctx context.Context, hcp *hyperv1.HostedControlPlane) (string, error) {
	svc := manifests.OLMPackageServerService(hcp.Namespace)
	return r.reconcileClusterIPServiceStatus(ctx, svc)
}

func (r *HostedControlPlaneReconciler) reconcileClusterIPServiceStatus(ctx context.Context, svc *corev1.Service) (string, error) {
	if err := r.Get(ctx, client.ObjectKeyFromObject(svc), svc); err != nil {
		if apierrors.IsNotFound(err) {
			return "", nil
		}
		return "", fmt.Errorf("failed to get cluster ip service %s/%s: %w", svc.Namespace, svc.Name, err)
	}
	return svc.Spec.ClusterIP, nil
}

func (r *HostedControlPlaneReconciler) reconcileKubeadminPassword(ctx context.Context, hcp *hyperv1.HostedControlPlane, explicitOauthConfig bool, createOrUpdate upsert.CreateOrUpdateFN) error {
	if explicitOauthConfig {
		return nil
	}
	var kubeadminPassword string
	kubeadminPasswordSecret := common.KubeadminPasswordSecret(hcp.Namespace)
	if _, err := createOrUpdate(ctx, r, kubeadminPasswordSecret, func() error {
		return reconcileKubeadminPasswordSecret(kubeadminPasswordSecret, hcp, &kubeadminPassword)
	}); err != nil {
		return fmt.Errorf("failed to reconcile kubeadminPasswordSecret: %w", err)
	}
	return nil
}

func (r *HostedControlPlaneReconciler) reconcilePKI(ctx context.Context, hcp *hyperv1.HostedControlPlane, infraStatus InfrastructureStatus, createOrUpdate upsert.CreateOrUpdateFN) error {
	p := pki.NewPKIParams(hcp, infraStatus.APIHost, infraStatus.OAuthHost, infraStatus.KonnectivityHost)

	// Root CA
	rootCASecret := manifests.RootCASecret(hcp.Namespace)
	if _, err := createOrUpdate(ctx, r, rootCASecret, func() error {
		return pki.ReconcileRootCA(rootCASecret, p.OwnerRef)
	}); err != nil {
		return fmt.Errorf("failed to reconcile root CA: %w", err)
	}
	// Signer CA
	signerCASecret := manifests.ClusterSignerCASecret(hcp.Namespace)
	if _, err := createOrUpdate(ctx, r, signerCASecret, func() error {
		return pki.ReconcileClusterSignerCA(signerCASecret, p.OwnerRef)
	}); err != nil {
		return fmt.Errorf("failed to reconcile signer CA: %w", err)
	}
	// Combined CA
	combinedCA := manifests.CombinedCAConfigMap(hcp.Namespace)
	if _, err := createOrUpdate(ctx, r, combinedCA, func() error {
		return pki.ReconcileCombinedCA(combinedCA, p.OwnerRef, rootCASecret, signerCASecret)
	}); err != nil {
		return fmt.Errorf("failed to reconcile combined CA: %w", err)
	}

	// Metrics client cert
	metricsClientCert := manifests.MetricsClientCertSecret(hcp.Namespace)
	if _, err := createOrUpdate(ctx, r, metricsClientCert, func() error {
		return pki.ReconcileMetricsSAClientCertSecret(metricsClientCert, rootCASecret, p.OwnerRef)
	}); err != nil {
		return fmt.Errorf("failed to reconcile metrics client cert secret: %w", err)
	}

	// Etcd client secret
	etcdClientSecret := manifests.EtcdClientSecret(hcp.Namespace)
	if _, err := createOrUpdate(ctx, r, etcdClientSecret, func() error {
		return pki.ReconcileEtcdClientSecret(etcdClientSecret, rootCASecret, p.OwnerRef)
	}); err != nil {
		return fmt.Errorf("failed to reconcile etcd client secret: %w", err)
	}

	// Etcd server secret
	etcdServerSecret := manifests.EtcdServerSecret(hcp.Namespace)
	if _, err := createOrUpdate(ctx, r, etcdServerSecret, func() error {
		return pki.ReconcileEtcdServerSecret(etcdServerSecret, rootCASecret, p.OwnerRef)
	}); err != nil {
		return fmt.Errorf("failed to reconcile etcd server secret: %w", err)
	}

	// Etcd peer secret
	etcdPeerSecret := manifests.EtcdPeerSecret(hcp.Namespace)
	if _, err := createOrUpdate(ctx, r, etcdPeerSecret, func() error {
		return pki.ReconcileEtcdPeerSecret(etcdPeerSecret, rootCASecret, p.OwnerRef)
	}); err != nil {
		return fmt.Errorf("failed to reconcile etcd peer secret: %w", err)
	}

	// KAS server secret
	kasServerSecret := manifests.KASServerCertSecret(hcp.Namespace)
	if _, err := createOrUpdate(ctx, r, kasServerSecret, func() error {
		return pki.ReconcileKASServerCertSecret(kasServerSecret, rootCASecret, p.OwnerRef, p.ExternalAPIAddress, p.InternalAPIAddress, p.ServiceCIDR)
	}); err != nil {
		return fmt.Errorf("failed to reconcile kas server secret: %w", err)
	}

	// KAS kubelet client secret
	kasKubeletClientSecret := manifests.KASKubeletClientCertSecret(hcp.Namespace)
	if _, err := createOrUpdate(ctx, r, kasKubeletClientSecret, func() error {
		return pki.ReconcileKASKubeletClientCertSecret(kasKubeletClientSecret, rootCASecret, p.OwnerRef)
	}); err != nil {
		return fmt.Errorf("failed to reconcile kas kubelet client secret: %w", err)
	}

	// KAS aggregator cert secret
	kasAggregatorCertSecret := manifests.KASAggregatorCertSecret(hcp.Namespace)
	if _, err := createOrUpdate(ctx, r, kasAggregatorCertSecret, func() error {
		return pki.ReconcileKASAggregatorCertSecret(kasAggregatorCertSecret, rootCASecret, p.OwnerRef)
	}); err != nil {
		return fmt.Errorf("failed to reconcile kas aggregator secret: %w", err)
	}

	// KAS admin client cert secret
	kasAdminClientCertSecret := manifests.KASAdminClientCertSecret(hcp.Namespace)
	if _, err := createOrUpdate(ctx, r, kasAdminClientCertSecret, func() error {
		return pki.ReconcileKASAdminClientCertSecret(kasAdminClientCertSecret, rootCASecret, p.OwnerRef)
	}); err != nil {
		return fmt.Errorf("failed to reconcile kas admin client secret: %w", err)
	}

	// KAS bootstrap client cert secret
	kasBootstrapClientCertSecret := manifests.KASMachineBootstrapClientCertSecret(hcp.Namespace)
	if _, err := createOrUpdate(ctx, r, kasBootstrapClientCertSecret, func() error {
		return pki.ReconcileKASMachineBootstrapClientCertSecret(kasBootstrapClientCertSecret, signerCASecret, p.OwnerRef)
	}); err != nil {
		return fmt.Errorf("failed to reconcile kas bootstrap client secret: %w", err)
	}

	// Service account signing key secret
	serviceAccountSigningKeySecret := manifests.ServiceAccountSigningKeySecret(hcp.Namespace)
	if _, err := createOrUpdate(ctx, r, serviceAccountSigningKeySecret, func() error {
		return pki.ReconcileServiceAccountSigningKeySecret(serviceAccountSigningKeySecret, p.OwnerRef)
	}); err != nil {
		return fmt.Errorf("failed to reconcile api server service account key secret: %w", err)
	}

	// OpenShift APIServer
	openshiftAPIServerCertSecret := manifests.OpenShiftAPIServerCertSecret(hcp.Namespace)
	if _, err := createOrUpdate(ctx, r, openshiftAPIServerCertSecret, func() error {
		return pki.ReconcileOpenShiftAPIServerCertSecret(openshiftAPIServerCertSecret, rootCASecret, p.OwnerRef)
	}); err != nil {
		return fmt.Errorf("failed to reconcile kas admin client secret: %w", err)
	}

	// OpenShift OAuth APIServer
	openshiftOAuthAPIServerCertSecret := manifests.OpenShiftOAuthAPIServerCertSecret(hcp.Namespace)
	if _, err := createOrUpdate(ctx, r, openshiftOAuthAPIServerCertSecret, func() error {
		return pki.ReconcileOpenShiftOAuthAPIServerCertSecret(openshiftOAuthAPIServerCertSecret, rootCASecret, p.OwnerRef)
	}); err != nil {
		return fmt.Errorf("failed to reconcile openshift oauth apiserver cert: %w", err)
	}

	// OpenShift Authenticator
	openshiftAuthenticatorCertSecret := manifests.OpenshiftAuthenticatorCertSecret(hcp.Namespace)
	if _, err := createOrUpdate(ctx, r, openshiftAuthenticatorCertSecret, func() error {
		return pki.ReconcileOpenShiftAuthenticatorCertSecret(openshiftAuthenticatorCertSecret, rootCASecret, p.OwnerRef)
	}); err != nil {
		return fmt.Errorf("failed to reconcile openshift authenticator cert: %w", err)
	}

	// OpenShift ControllerManager Cert
	openshiftControllerManagerCertSecret := manifests.OpenShiftControllerManagerCertSecret(hcp.Namespace)
	if _, err := createOrUpdate(ctx, r, openshiftControllerManagerCertSecret, func() error {
		return pki.ReconcileOpenShiftControllerManagerCertSecret(openshiftControllerManagerCertSecret, rootCASecret, p.OwnerRef)
	}); err != nil {
		return fmt.Errorf("failed to reconcile openshift controller manager cert: %w", err)
	}

	// Cluster Policy Controller Cert
	clusterPolicyControllerCertSecret := manifests.ClusterPolicyControllerCertSecret(hcp.Namespace)
	if _, err := createOrUpdate(ctx, r, clusterPolicyControllerCertSecret, func() error {
		return pki.ReconcileOpenShiftControllerManagerCertSecret(clusterPolicyControllerCertSecret, rootCASecret, p.OwnerRef)
	}); err != nil {
		return fmt.Errorf("failed to reconcile cluster policy controller cert: %w", err)
	}

	// Konnectivity Server Cert
	konnectivityServerSecret := manifests.KonnectivityServerSecret(hcp.Namespace)
	if _, err := createOrUpdate(ctx, r, konnectivityServerSecret, func() error {
		return pki.ReconcileKonnectivityServerSecret(konnectivityServerSecret, rootCASecret, p.OwnerRef)
	}); err != nil {
		return fmt.Errorf("failed to reconcile konnectivity server cert: %w", err)
	}

	// Konnectivity Cluster Cert
	konnectivityClusterSecret := manifests.KonnectivityClusterSecret(hcp.Namespace)
	if _, err := createOrUpdate(ctx, r, konnectivityClusterSecret, func() error {
		return pki.ReconcileKonnectivityClusterSecret(konnectivityClusterSecret, rootCASecret, p.OwnerRef, p.ExternalKconnectivityAddress)
	}); err != nil {
		return fmt.Errorf("failed to reconcile konnectivity cluster cert: %w", err)
	}

	// Konnectivity Client Cert
	konnectivityClientSecret := manifests.KonnectivityClientSecret(hcp.Namespace)
	if _, err := createOrUpdate(ctx, r, konnectivityClientSecret, func() error {
		return pki.ReconcileKonnectivityClientSecret(konnectivityClientSecret, rootCASecret, p.OwnerRef)
	}); err != nil {
		return fmt.Errorf("failed to reconcile konnectivity client cert: %w", err)
	}

	// Konnectivity Agent Cert
	konnectivityAgentSecret := manifests.KonnectivityAgentSecret(hcp.Namespace)
	if _, err := createOrUpdate(ctx, r, konnectivityAgentSecret, func() error {
		return pki.ReconcileKonnectivityAgentSecret(konnectivityAgentSecret, rootCASecret, p.OwnerRef)
	}); err != nil {
		return fmt.Errorf("failed to reconcile konnectivity agent cert: %w", err)
	}

	// Ingress Cert
	ingressCert := manifests.IngressCert(hcp.Namespace)
	if _, err := createOrUpdate(ctx, r, ingressCert, func() error {
		return pki.ReconcileIngressCert(ingressCert, rootCASecret, p.OwnerRef, p.IngressSubdomain)
	}); err != nil {
		return fmt.Errorf("failed to reconcile ingress cert secret: %w", err)
	}

	// OAuth server Cert
	oauthServerCert := manifests.OpenShiftOAuthServerCert(hcp.Namespace)
	if _, err := createOrUpdate(ctx, r, oauthServerCert, func() error {
		return pki.ReconcileOAuthServerCert(oauthServerCert, rootCASecret, p.OwnerRef, p.ExternalOauthAddress)
	}); err != nil {
		return fmt.Errorf("failed to reconcile oauth cert secret: %w", err)
	}

	// MCS Cert
	machineConfigServerCert := manifests.MachineConfigServerCert(hcp.Namespace)
	if _, err := createOrUpdate(ctx, r, machineConfigServerCert, func() error {
		return pki.ReconcileMachineConfigServerCert(machineConfigServerCert, rootCASecret, p.OwnerRef)
	}); err != nil {
		return fmt.Errorf("failed to reconcile machine config server cert secret: %w", err)
	}

	// OLM PackageServer Cert
	packageServerCertSecret := manifests.OLMPackageServerCertSecret(hcp.Namespace)
	if _, err := createOrUpdate(ctx, r, packageServerCertSecret, func() error {
		return pki.ReconcileOLMPackageServerCertSecret(packageServerCertSecret, rootCASecret, p.OwnerRef)
	}); err != nil {
		return fmt.Errorf("failed to reconcile packageserver cert: %w", err)
	}

	// OLM Catalog Operator Serving Cert
	catalogOperatorServingCert := manifests.OLMCatalogOperatorServingCertSecret(hcp.Namespace)
	if _, err := createOrUpdate(ctx, r, catalogOperatorServingCert, func() error {
		return pki.ReconcileOLMCatalogOperatorServingCertSecret(catalogOperatorServingCert, rootCASecret, p.OwnerRef)
	}); err != nil {
		return fmt.Errorf("failed to reconcile olm catalog operator serving cert: %w", err)
	}

	// OLM Operator Serving Cert
	olmOperatorServingCert := manifests.OLMOperatorServingCertSecret(hcp.Namespace)
	if _, err := createOrUpdate(ctx, r, olmOperatorServingCert, func() error {
		return pki.ReconcileOLMOperatorServingCertSecret(olmOperatorServingCert, rootCASecret, p.OwnerRef)
	}); err != nil {
		return fmt.Errorf("failed to reconcile olm operator serving cert: %w", err)
	}

	// Image Registry Operator Serving Cert
	imageRegistryOperatorServingCert := manifests.ImageRegistryOperatorServingCert(hcp.Namespace)
	if _, err := createOrUpdate(ctx, r, imageRegistryOperatorServingCert, func() error {
		return pki.ReconcileRegistryOperatorServingCert(imageRegistryOperatorServingCert, rootCASecret, p.OwnerRef)
	}); err != nil {
		return fmt.Errorf("failed to reconcile image registry operator serving cert: %w", err)
	}

	kcmServerSecret := manifests.KCMServerCertSecret(hcp.Namespace)
	if _, err := createOrUpdate(ctx, r, kcmServerSecret, func() error {
		return pki.ReconcileKCMServerSecret(kcmServerSecret, rootCASecret, p.OwnerRef)
	}); err != nil {
		return fmt.Errorf("failed to reconcile olm operator serving cert: %w", err)
	}

	cvoServerCert := manifests.ClusterVersionOperatorServerCertSecret(hcp.Namespace)
	if _, err := createOrUpdate(ctx, r, cvoServerCert, func() error {
		return pki.ReconcileCVOServerSecret(cvoServerCert, rootCASecret, p.OwnerRef)
	}); err != nil {
		return fmt.Errorf("failed to reconcile cvo serving cert: %w", err)
	}

	if hcp.Spec.Platform.Type == hyperv1.AWSPlatform {
		awsPodIdentityWebhookServingCert := manifests.AWSPodIdentityWebhookServingCert(hcp.Namespace)
		if _, err := createOrUpdate(ctx, r, awsPodIdentityWebhookServingCert, func() error {
			return pki.ReconcileAWSPodIdentityWebhookServingCert(awsPodIdentityWebhookServingCert, rootCASecret, p.OwnerRef)
		}); err != nil {
			return fmt.Errorf("failed to reconcile %s secret: %w", awsPodIdentityWebhookServingCert.Name, err)
		}
	}

	return nil
}

func (r *HostedControlPlaneReconciler) reconcileCloudProviderConfig(ctx context.Context, hcp *hyperv1.HostedControlPlane, createOrUpdate upsert.CreateOrUpdateFN) error {
	switch hcp.Spec.Platform.Type {
	case hyperv1.AWSPlatform:
		p := aws.NewAWSParams(hcp)
		awsProviderConfig := manifests.AWSProviderConfig(hcp.Namespace)
		if _, err := createOrUpdate(ctx, r, awsProviderConfig, func() error {
			return p.ReconcileCloudConfig(awsProviderConfig)
		}); err != nil {
			return fmt.Errorf("failed to reconcile aws provider config: %w", err)
		}
	case hyperv1.AzurePlatform:
		credentialsSecret := &corev1.Secret{ObjectMeta: metav1.ObjectMeta{Namespace: hcp.Namespace, Name: hcp.Spec.Platform.Azure.Credentials.Name}}
		if err := r.Client.Get(ctx, client.ObjectKeyFromObject(credentialsSecret), credentialsSecret); err != nil {
			return fmt.Errorf("failed to get Azure credentials secret: %w", err)
		}

		// We need different configs for KAS/KCM and Kubelet in Nodes
		cfg := manifests.AzureProviderConfig(hcp.Namespace)
		if _, err := createOrUpdate(ctx, r, cfg, func() error {
			return azure.ReconcileCloudConfig(cfg, hcp, credentialsSecret)
		}); err != nil {
			return fmt.Errorf("failed to reconcile Azure cloud config: %w", err)
		}
		withSecrets := manifests.AzureProviderConfigWithCredentials(hcp.Namespace)
		if _, err := createOrUpdate(ctx, r, withSecrets, func() error {
			return azure.ReconcileCloudConfigWithCredentials(withSecrets, hcp, credentialsSecret)
		}); err != nil {
			return fmt.Errorf("failed to reconcile Azure cloud config with credentials: %w", err)
		}
	}
	return nil
}

func (r *HostedControlPlaneReconciler) reconcileManagedEtcd(ctx context.Context, hcp *hyperv1.HostedControlPlane, releaseImage *releaseinfo.ReleaseImage, createOrUpdate upsert.CreateOrUpdateFN) error {
	p := etcd.NewEtcdParams(hcp, releaseImage.ComponentImages())

	discoveryService := manifests.EtcdDiscoveryService(hcp.Namespace)
	if result, err := createOrUpdate(ctx, r, discoveryService, func() error {
		return etcd.ReconcileDiscoveryService(discoveryService, p.OwnerRef)
	}); err != nil {
		return fmt.Errorf("failed to reconcile etcd discovery service: %w", err)
	} else {
		r.Log.Info("reconciled etcd discovery service", "result", result)
	}

	clientService := manifests.EtcdClientService(hcp.Namespace)
	if result, err := createOrUpdate(ctx, r, clientService, func() error {
		return etcd.ReconcileClientService(clientService, p.OwnerRef)
	}); err != nil {
		return fmt.Errorf("failed to reconcile etcd client service: %w", err)
	} else {
		r.Log.Info("reconciled etcd client service", "result", result)
	}

	serviceMonitor := manifests.EtcdServiceMonitor(hcp.Namespace)
	if result, err := createOrUpdate(ctx, r, serviceMonitor, func() error {
		return etcd.ReconcileServiceMonitor(serviceMonitor, p.OwnerRef, hcp.Spec.ClusterID, r.MetricsSet)
	}); err != nil {
		return fmt.Errorf("failed to reconcile etcd servicemonitor: %w", err)
	} else {
		r.Log.Info("reconciled etcd servicemonitor", "result", result)
	}

	pdb := manifests.EtcdPodDisruptionBudget(hcp.Namespace)
	if result, err := createOrUpdate(ctx, r, pdb, func() error {
		return etcd.ReconcilePodDisruptionBudget(pdb, p)
	}); err != nil {
		return fmt.Errorf("failed to reconcile etcd pdb: %w", err)
	} else {
		r.Log.Info("reconciled etcd pdb", "result", result)
	}

	statefulSet := manifests.EtcdStatefulSet(hcp.Namespace)
	if result, err := createOrUpdate(ctx, r, statefulSet, func() error {
		return etcd.ReconcileStatefulSet(statefulSet, p)
	}); err != nil {
		return fmt.Errorf("failed to reconcile etcd statefulset: %w", err)
	} else {
		r.Log.Info("reconciled etcd statefulset", "result", result)
	}

	return nil
}

func (r *HostedControlPlaneReconciler) reconcileUnmanagedEtcd(ctx context.Context, hcp *hyperv1.HostedControlPlane, createOrUpdate upsert.CreateOrUpdateFN) error {
	// reconcile client secret over
	if hcp.Spec.Etcd.Unmanaged == nil || len(hcp.Spec.Etcd.Unmanaged.TLS.ClientSecret.Name) == 0 || len(hcp.Spec.Etcd.Unmanaged.Endpoint) == 0 {
		return fmt.Errorf("etcd metadata not specified for unmanaged deployment")
	}
	r.Log.Info("Retrieving tls secret", "name", hcp.Spec.Etcd.Unmanaged.TLS.ClientSecret.Name)
	var src corev1.Secret
	if err := r.Client.Get(ctx, client.ObjectKey{Namespace: hcp.GetNamespace(), Name: hcp.Spec.Etcd.Unmanaged.TLS.ClientSecret.Name}, &src); err != nil {
		return fmt.Errorf("failed to get etcd client cert %s: %w", hcp.Spec.Etcd.Unmanaged.TLS.ClientSecret.Name, err)
	}
	if _, ok := src.Data["etcd-client.crt"]; !ok {
		return fmt.Errorf("etcd secret %s does not have client cert", hcp.Spec.Etcd.Unmanaged.TLS.ClientSecret.Name)
	}
	if _, ok := src.Data["etcd-client.key"]; !ok {
		return fmt.Errorf("etcd secret %s does not have client key", hcp.Spec.Etcd.Unmanaged.TLS.ClientSecret.Name)
	}
	if _, ok := src.Data["etcd-client-ca.crt"]; !ok {
		return fmt.Errorf("etcd secret %s does not have client ca", hcp.Spec.Etcd.Unmanaged.TLS.ClientSecret.Name)
	}
	kubeComponentEtcdClientSecret := manifests.EtcdClientSecret(hcp.GetNamespace())
	r.Log.Info("Reconciling openshift control plane etcd client tls secret", "name", kubeComponentEtcdClientSecret.Name)
	_, err := createOrUpdate(ctx, r.Client, kubeComponentEtcdClientSecret, func() error {
		if kubeComponentEtcdClientSecret.Data == nil {
			kubeComponentEtcdClientSecret.Data = map[string][]byte{}
		}
		kubeComponentEtcdClientSecret.Data = src.Data
		kubeComponentEtcdClientSecret.Type = corev1.SecretTypeOpaque
		return nil
	})
	return err
}

func (r *HostedControlPlaneReconciler) reconcileKonnectivity(ctx context.Context, hcp *hyperv1.HostedControlPlane, releaseImage *releaseinfo.ReleaseImage, infraStatus InfrastructureStatus, createOrUpdate upsert.CreateOrUpdateFN) error {
	r.Log.Info("Reconciling Konnectivity")
	p := konnectivity.NewKonnectivityParams(hcp, releaseImage.ComponentImages(), infraStatus.KonnectivityHost, infraStatus.KonnectivityPort, r.SetDefaultSecurityContext)
	serverDeployment := manifests.KonnectivityServerDeployment(hcp.Namespace)
	if _, err := createOrUpdate(ctx, r, serverDeployment, func() error {
		return konnectivity.ReconcileServerDeployment(serverDeployment, p.OwnerRef, p.ServerDeploymentConfig, p.KonnectivityServerImage)
	}); err != nil {
		return fmt.Errorf("failed to reconcile konnectivity server deployment: %w", err)
	}
	serverLocalService := manifests.KonnectivityServerLocalService(hcp.Namespace)
	if _, err := createOrUpdate(ctx, r, serverLocalService, func() error {
		return konnectivity.ReconcileServerLocalService(serverLocalService, p.OwnerRef)
	}); err != nil {
		return fmt.Errorf("failed to reconcile konnectivity server local service: %w", err)
	}
	agentDeployment := manifests.KonnectivityAgentDeployment(hcp.Namespace)
	ips := []string{
		infraStatus.OpenShiftAPIHost,
		infraStatus.OauthAPIServerHost,
		infraStatus.PackageServerAPIAddress,
	}
	if _, err := createOrUpdate(ctx, r, agentDeployment, func() error {
		return konnectivity.ReconcileAgentDeployment(agentDeployment, p.OwnerRef, p.AgentDeploymentConfig, p.KonnectivityAgentImage, ips)
	}); err != nil {
		return fmt.Errorf("failed to reconcile konnectivity agent deployment: %w", err)
	}
	return nil
}

func (r *HostedControlPlaneReconciler) reconcileKubeAPIServer(ctx context.Context, hcp *hyperv1.HostedControlPlane, releaseImage *releaseinfo.ReleaseImage, apiAddress string, apiPort int32, oauthAddress string, oauthPort int32, createOrUpdate upsert.CreateOrUpdateFN) error {
	ocpVersion, err := semver.Parse(releaseImage.Version())
	if err != nil {
		return fmt.Errorf("failed to parse release version: %w", err)
	}

	p := kas.NewKubeAPIServerParams(ctx, hcp, releaseImage.ComponentImages(), apiAddress, apiPort, oauthAddress, oauthPort, r.SetDefaultSecurityContext)

	rootCA := manifests.RootCASecret(hcp.Namespace)
	if err := r.Get(ctx, client.ObjectKeyFromObject(rootCA), rootCA); err != nil {
		return fmt.Errorf("failed to get root ca cert secret: %w", err)
	}

	clientCertSecret := manifests.KASAdminClientCertSecret(hcp.Namespace)
	if err := r.Get(ctx, client.ObjectKeyFromObject(clientCertSecret), clientCertSecret); err != nil {
		return fmt.Errorf("failed to get admin client cert secret: %w", err)
	}
	bootstrapClientCertSecret := manifests.KASMachineBootstrapClientCertSecret(hcp.Namespace)
	if err := r.Get(ctx, client.ObjectKeyFromObject(bootstrapClientCertSecret), bootstrapClientCertSecret); err != nil {
		return fmt.Errorf("failed to get bootstrap client cert secret: %w", err)
	}

	serviceKubeconfigSecret := manifests.KASServiceKubeconfigSecret(hcp.Namespace)
	if _, err := createOrUpdate(ctx, r, serviceKubeconfigSecret, func() error {
		return kas.ReconcileServiceKubeconfigSecret(serviceKubeconfigSecret, clientCertSecret, rootCA, p.OwnerRef, p.APIServerPort)
	}); err != nil {
		return fmt.Errorf("failed to reconcile service admin kubeconfig secret: %w", err)
	}

	// The client used by CAPI machine controller expects the kubeconfig to follow this naming and key convention
	// https://github.com/kubernetes-sigs/cluster-api/blob/5c85a0a01ee44ecf7c8a3c3fdc867a88af87d73c/util/secret/secret.go#L29-L33
	capiKubeconfigSecret := manifests.KASServiceCAPIKubeconfigSecret(hcp.Namespace, hcp.Spec.InfraID)
	if _, err := createOrUpdate(ctx, r, capiKubeconfigSecret, func() error {
		// TODO(alberto): This secret is currently using the cluster-admin kubeconfig for the guest cluster.
		// We should create a separate kubeconfig with a tight set of permissions for it to use.
		return kas.ReconcileServiceCAPIKubeconfigSecret(capiKubeconfigSecret, clientCertSecret, rootCA, p.OwnerRef, p.APIServerPort)
	}); err != nil {
		return fmt.Errorf("failed to reconcile CAPI service admin kubeconfig secret: %w", err)
	}

	localhostKubeconfigSecret := manifests.KASLocalhostKubeconfigSecret(hcp.Namespace)
	if _, err := createOrUpdate(ctx, r, localhostKubeconfigSecret, func() error {
		return kas.ReconcileLocalhostKubeconfigSecret(localhostKubeconfigSecret, clientCertSecret, rootCA, p.OwnerRef, p.APIServerPort)
	}); err != nil {
		return fmt.Errorf("failed to reconcile localhost kubeconfig secret: %w", err)
	}

	externalKubeconfigSecret := manifests.KASExternalKubeconfigSecret(hcp.Namespace, hcp.Spec.KubeConfig)
	if _, err := createOrUpdate(ctx, r, externalKubeconfigSecret, func() error {
		if util.IsPublicHCP(hcp) {
			return kas.ReconcileExternalKubeconfigSecret(externalKubeconfigSecret, clientCertSecret, rootCA, p.OwnerRef, p.ExternalURL(), p.ExternalKubeconfigKey())
		}
		return kas.ReconcileExternalKubeconfigSecret(externalKubeconfigSecret, clientCertSecret, rootCA, p.OwnerRef, p.InternalURL(), p.ExternalKubeconfigKey())
	}); err != nil {
		return fmt.Errorf("failed to reconcile external kubeconfig secret: %w", err)
	}

	bootstrapKubeconfigSecret := manifests.KASBootstrapKubeconfigSecret(hcp.Namespace)
	if _, err := createOrUpdate(ctx, r, bootstrapKubeconfigSecret, func() error {
		if util.IsPrivateHCP(hcp) {
			return kas.ReconcileBootstrapKubeconfigSecret(bootstrapKubeconfigSecret, bootstrapClientCertSecret, rootCA, p.OwnerRef, p.InternalURL())
		}
		return kas.ReconcileBootstrapKubeconfigSecret(bootstrapKubeconfigSecret, bootstrapClientCertSecret, rootCA, p.OwnerRef, p.ExternalURL())
	}); err != nil {
		return fmt.Errorf("failed to reconcile bootstrap kubeconfig secret: %w", err)
	}

	kubeAPIServerAuditConfig := manifests.KASAuditConfig(hcp.Namespace)
	if _, err := createOrUpdate(ctx, r, kubeAPIServerAuditConfig, func() error {
		return kas.ReconcileAuditConfig(kubeAPIServerAuditConfig, p.OwnerRef, p.AuditPolicyConfig())
	}); err != nil {
		return fmt.Errorf("failed to reconcile api server audit config: %w", err)
	}

	kubeAPIServerConfig := manifests.KASConfig(hcp.Namespace)
	if _, err := createOrUpdate(ctx, r, kubeAPIServerConfig, func() error {
		return kas.ReconcileConfig(kubeAPIServerConfig,
			p.OwnerRef,
			p.ConfigParams(),
			ocpVersion,
		)
	}); err != nil {
		return fmt.Errorf("failed to reconcile api server config: %w", err)
	}

	kubeAPIServerEgressSelectorConfig := manifests.KASEgressSelectorConfig(hcp.Namespace)
	if _, err := createOrUpdate(ctx, r, kubeAPIServerEgressSelectorConfig, func() error {
		return kas.ReconcileEgressSelectorConfig(kubeAPIServerEgressSelectorConfig, p.OwnerRef)
	}); err != nil {
		return fmt.Errorf("failed to reconcile api server egress selector config: %w", err)
	}

	oauthMetadata := manifests.KASOAuthMetadata(hcp.Namespace)
	if _, err := createOrUpdate(ctx, r, oauthMetadata, func() error {
		return kas.ReconcileOauthMetadata(oauthMetadata, p.OwnerRef, p.ExternalOAuthAddress, p.ExternalOAuthPort)
	}); err != nil {
		return fmt.Errorf("failed to reconcile oauth metadata: %w", err)
	}

	var aesCBCActiveKey, aesCBCBackupKey []byte

	if hcp.Spec.SecretEncryption != nil {
		r.Log.Info("Reconciling kube-apiserver secret encryption configuration")
		encryptionConfigFile := manifests.KASSecretEncryptionConfigFile(hcp.Namespace)
		switch hcp.Spec.SecretEncryption.Type {
		case hyperv1.AESCBC:
			if hcp.Spec.SecretEncryption.AESCBC == nil || len(hcp.Spec.SecretEncryption.AESCBC.ActiveKey.Name) == 0 {
				return fmt.Errorf("aescbc metadata not specified")
			}
			activeKeySecret := &corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{
					Name:      hcp.Spec.SecretEncryption.AESCBC.ActiveKey.Name,
					Namespace: hcp.Namespace,
				},
			}
			if err := r.Get(ctx, client.ObjectKeyFromObject(activeKeySecret), activeKeySecret); err != nil {
				return fmt.Errorf("failed to get aescbc active secret: %w", err)
			}
			if _, ok := activeKeySecret.Data[hyperv1.AESCBCKeySecretKey]; !ok {
				return fmt.Errorf("aescbc key field '%s' in active key secret not specified", hyperv1.AESCBCKeySecretKey)
			}
			aesCBCActiveKey = activeKeySecret.Data[hyperv1.AESCBCKeySecretKey]
			if hcp.Spec.SecretEncryption.AESCBC.BackupKey != nil && len(hcp.Spec.SecretEncryption.AESCBC.BackupKey.Name) > 0 {
				backupKeySecret := &corev1.Secret{
					ObjectMeta: metav1.ObjectMeta{
						Name:      hcp.Spec.SecretEncryption.AESCBC.BackupKey.Name,
						Namespace: hcp.Namespace,
					},
				}
				if err := r.Get(ctx, client.ObjectKeyFromObject(backupKeySecret), backupKeySecret); err != nil {
					return fmt.Errorf("failed to get aescbc backup key secret: %w", err)
				}
				if _, ok := backupKeySecret.Data[hyperv1.AESCBCKeySecretKey]; !ok {
					return fmt.Errorf("aescbc key field %s in backup key secret not specified", hyperv1.AESCBCKeySecretKey)
				}
				aesCBCBackupKey = backupKeySecret.Data[hyperv1.AESCBCKeySecretKey]
			}
			if _, err := createOrUpdate(ctx, r, encryptionConfigFile, func() error {
				return kas.ReconcileAESCBCEncryptionConfig(encryptionConfigFile, p.OwnerRef, aesCBCActiveKey, aesCBCBackupKey)
			}); err != nil {
				return fmt.Errorf("failed to reconcile aes encryption config secret: %w", err)
			}
		case hyperv1.KMS:
			if hcp.Spec.SecretEncryption.KMS == nil {
				return fmt.Errorf("kms metadata not specified")
			}
			if _, err := createOrUpdate(ctx, r, encryptionConfigFile, func() error {
				return kas.ReconcileKMSEncryptionConfig(encryptionConfigFile, p.OwnerRef, hcp.Spec.SecretEncryption.KMS)
			}); err != nil {
				return fmt.Errorf("failed to reconcile kms encryption config secret: %w", err)
			}
		}
	}

	openshiftAuthenticatorCertSecret := manifests.OpenshiftAuthenticatorCertSecret(hcp.Namespace)
	if err := r.Get(ctx, client.ObjectKeyFromObject(openshiftAuthenticatorCertSecret), openshiftAuthenticatorCertSecret); err != nil {
		return fmt.Errorf("failed to get authenticator cert secret: %w", err)
	}
	authenticationTokenWebhookConfigSecret := manifests.KASAuthenticationTokenWebhookConfigSecret(hcp.Namespace)
	if _, err := createOrUpdate(ctx, r, authenticationTokenWebhookConfigSecret, func() error {
		return kas.ReconcileAuthenticationTokenWebhookConfigSecret(authenticationTokenWebhookConfigSecret, p.OwnerRef, openshiftAuthenticatorCertSecret)
	}); err != nil {
		return fmt.Errorf("failed to reconcile authentication token webhook config: %w", err)
	}

	pdb := manifests.KASPodDisruptionBudget(hcp.Namespace)
	if result, err := createOrUpdate(ctx, r, pdb, func() error {
		return kas.ReconcilePodDisruptionBudget(pdb, p)
	}); err != nil {
		return fmt.Errorf("failed to reconcile api server pdb: %w", err)
	} else {
		r.Log.Info("Reconciled api server pdb", "result", result)
	}

	serviceMonitor := manifests.KASServiceMonitor(hcp.Namespace)
	if result, err := createOrUpdate(ctx, r, serviceMonitor, func() error {
		return kas.ReconcileServiceMonitor(serviceMonitor, int(p.APIServerPort), config.OwnerRefFrom(hcp), hcp.Spec.ClusterID, r.MetricsSet)
	}); err != nil {
		return fmt.Errorf("failed to reconcile kas service monitor: %w", err)
	} else {
		r.Log.Info("Reconciled api server service monitor", "result", result)
	}

	if hcp.Spec.Platform.Type == hyperv1.AWSPlatform {
		podIdentityWebhookSecret := manifests.AWSPodIdentityWebhookKubeconfig(hcp.Namespace)
		if _, err := createOrUpdate(ctx, r, podIdentityWebhookSecret, func() error {
			return pki.ReconcileServiceAccountKubeconfig(podIdentityWebhookSecret, rootCA, hcp, "openshift-authentication", "aws-pod-identity-webhook")
		}); err != nil {
			return fmt.Errorf("failecd to reconcile aws pod identity webhook kubeconfig secret: %w", err)
		}
	}

	kubeAPIServerDeployment := manifests.KASDeployment(hcp.Namespace)
	if _, err := createOrUpdate(ctx, r, kubeAPIServerDeployment, func() error {
		return kas.ReconcileKubeAPIServerDeployment(kubeAPIServerDeployment,
			hcp,
			p.OwnerRef,
			p.DeploymentConfig,
			p.NamedCertificates(),
			p.CloudProvider,
			p.CloudProviderConfig,
			p.CloudProviderCreds,
			p.Images,
			kubeAPIServerConfig,
			p.AuditWebhookRef,
			aesCBCActiveKey,
			aesCBCBackupKey,
			p.APIServerPort,
		)
	}); err != nil {
		return fmt.Errorf("failed to reconcile api server deployment: %w", err)
	}

	return nil
}

func (r *HostedControlPlaneReconciler) reconcileKubeControllerManager(ctx context.Context, hcp *hyperv1.HostedControlPlane, releaseImage *releaseinfo.ReleaseImage, createOrUpdate upsert.CreateOrUpdateFN) error {
	p := kcm.NewKubeControllerManagerParams(ctx, hcp, releaseImage.ComponentImages(), r.SetDefaultSecurityContext)

	service := manifests.KCMService(hcp.Namespace)
	if _, err := createOrUpdate(ctx, r, service, func() error {
		return kcm.ReconcileService(service, config.OwnerRefFrom(hcp))
	}); err != nil {
		return fmt.Errorf("failed to reconcile kcm service: %w", err)
	}

	combinedCA := manifests.CombinedCAConfigMap(hcp.Namespace)
	if err := r.Get(ctx, client.ObjectKeyFromObject(combinedCA), combinedCA); err != nil {
		return fmt.Errorf("failed to fetch combined ca configmap: %w", err)
	}
	serviceServingCA := manifests.ServiceServingCA(hcp.Namespace)
	if _, err := createOrUpdate(ctx, r, serviceServingCA, func() error {
		return kcm.ReconcileKCMServiceServingCA(serviceServingCA, combinedCA, p.OwnerRef)
	}); err != nil {
		return fmt.Errorf("failed to reconcile kcm serving ca: %w", err)
	}

	serviceMonitor := manifests.KCMServiceMonitor(hcp.Namespace)
	if result, err := createOrUpdate(ctx, r, serviceMonitor, func() error {
		return kcm.ReconcileServiceMonitor(serviceMonitor, config.OwnerRefFrom(hcp), hcp.Spec.ClusterID, r.MetricsSet)
	}); err != nil {
		return fmt.Errorf("failed to reconcile kcm service monitor: %w", err)
	} else {
		r.Log.Info("Reconciled kcm service monitor", "result", result)
	}

	kcmConfig := manifests.KCMConfig(hcp.Namespace)
	if _, err := createOrUpdate(ctx, r, kcmConfig, func() error {
		return kcm.ReconcileConfig(kcmConfig, serviceServingCA, p.OwnerRef)
	}); err != nil {
		return fmt.Errorf("failed to reconcile kcm config: %w", err)
	}

	kcmDeployment := manifests.KCMDeployment(hcp.Namespace)
	if _, err := createOrUpdate(ctx, r, kcmDeployment, func() error {
		return kcm.ReconcileDeployment(kcmDeployment, kcmConfig, serviceServingCA, p, util.APIPort(hcp))
	}); err != nil {
		return fmt.Errorf("failed to reconcile kcm deployment: %w", err)
	}

	return nil
}

func (r *HostedControlPlaneReconciler) reconcileKubeScheduler(ctx context.Context, hcp *hyperv1.HostedControlPlane, releaseImage *releaseinfo.ReleaseImage, createOrUpdate upsert.CreateOrUpdateFN) error {
	p := scheduler.NewKubeSchedulerParams(ctx, hcp, releaseImage.ComponentImages(), r.SetDefaultSecurityContext)

	schedulerConfig := manifests.SchedulerConfig(hcp.Namespace)
	if _, err := createOrUpdate(ctx, r, schedulerConfig, func() error {
		return scheduler.ReconcileConfig(schedulerConfig, p.OwnerRef)
	}); err != nil {
		return fmt.Errorf("failed to reconcile scheduler config: %w", err)
	}

	schedulerDeployment := manifests.SchedulerDeployment(hcp.Namespace)
	if _, err := createOrUpdate(ctx, r, schedulerDeployment, func() error {
		return scheduler.ReconcileDeployment(schedulerDeployment, p.OwnerRef, p.DeploymentConfig, p.HyperkubeImage, p.FeatureGates(), p.SchedulerPolicy(), p.AvailabilityProberImage, util.APIPort(hcp), p.CipherSuites(), p.MinTLSVersion(), p.DisableProfiling)
	}); err != nil {
		return fmt.Errorf("failed to reconcile scheduler deployment: %w", err)
	}
	return nil
}

func (r *HostedControlPlaneReconciler) reconcileOpenShiftAPIServer(ctx context.Context, hcp *hyperv1.HostedControlPlane, observedConfig *globalconfig.ObservedConfig, releaseImage *releaseinfo.ReleaseImage, serviceClusterIP string, createOrUpdate upsert.CreateOrUpdateFN) error {
	p := oapi.NewOpenShiftAPIServerParams(hcp, observedConfig, releaseImage.ComponentImages(), r.SetDefaultSecurityContext)
	oapicfg := manifests.OpenShiftAPIServerConfig(hcp.Namespace)
	if _, err := createOrUpdate(ctx, r, oapicfg, func() error {
		return oapi.ReconcileConfig(oapicfg, p.OwnerRef, p.EtcdURL, p.IngressDomain(), p.MinTLSVersion(), p.CipherSuites(), p.Image, p.Project)
	}); err != nil {
		return fmt.Errorf("failed to reconcile openshift apiserver config: %w", err)
	}

	auditCfg := manifests.OpenShiftAPIServerAuditConfig(hcp.Namespace)
	if _, err := createOrUpdate(ctx, r, auditCfg, func() error {
		return oapi.ReconcileAuditConfig(auditCfg, p.OwnerRef)
	}); err != nil {
		return fmt.Errorf("failed to reconcile openshift apiserver audit config: %w", err)
	}

	pdb := manifests.OpenShiftAPIServerPodDisruptionBudget(hcp.Namespace)
	if result, err := createOrUpdate(ctx, r, pdb, func() error {
		return oapi.ReconcilePodDisruptionBudget(pdb, p)
	}); err != nil {
		return fmt.Errorf("failed to reconcile openshift apiserver pdb: %w", err)
	} else {
		r.Log.Info("Reconciled openshift apiserver pdb", "result", result)
	}

	serviceMonitor := manifests.OpenShiftAPIServerServiceMonitor(hcp.Namespace)
	if result, err := createOrUpdate(ctx, r, serviceMonitor, func() error {
		return oapi.ReconcileServiceMonitor(serviceMonitor, p.OwnerRef, hcp.Spec.ClusterID, r.MetricsSet)
	}); err != nil {
		return fmt.Errorf("failed to reconcile openshift apiserver servicemonitor: %w", err)
	} else {
		r.Log.Info("reconciled openshift apiserver servicemonitor", "result", result)
	}

	deployment := manifests.OpenShiftAPIServerDeployment(hcp.Namespace)
	if _, err := createOrUpdate(ctx, r, deployment, func() error {
		return oapi.ReconcileDeployment(deployment, p.OwnerRef, oapicfg, p.OpenShiftAPIServerDeploymentConfig, p.OpenShiftAPIServerImage, p.ProxyImage, p.EtcdURL, p.AvailabilityProberImage, util.APIPort(hcp))
	}); err != nil {
		return fmt.Errorf("failed to reconcile openshift apiserver deployment: %w", err)
	}

	return nil
}

func (r *HostedControlPlaneReconciler) reconcileOpenShiftOAuthAPIServer(ctx context.Context, hcp *hyperv1.HostedControlPlane, observedConfig *globalconfig.ObservedConfig, releaseImage *releaseinfo.ReleaseImage, serviceClusterIP string, createOrUpdate upsert.CreateOrUpdateFN) error {
	p := oapi.NewOpenShiftAPIServerParams(hcp, observedConfig, releaseImage.ComponentImages(), r.SetDefaultSecurityContext)
	auditCfg := manifests.OpenShiftOAuthAPIServerAuditConfig(hcp.Namespace)
	if _, err := createOrUpdate(ctx, r, auditCfg, func() error {
		return oapi.ReconcileAuditConfig(auditCfg, p.OwnerRef)
	}); err != nil {
		return fmt.Errorf("failed to reconcile openshift oauth apiserver audit config: %w", err)
	}

	pdb := manifests.OpenShiftOAuthAPIServerDisruptionBudget(hcp.Namespace)
	if result, err := createOrUpdate(ctx, r, pdb, func() error {
		return oapi.ReconcileOpenShiftOAuthAPIServerPodDisruptionBudget(pdb, p.OAuthAPIServerDeploymentParams())
	}); err != nil {
		return fmt.Errorf("failed to reconcile openshift oauth apiserver pdb: %w", err)
	} else {
		r.Log.Info("Reconciled openshift oauth apiserver pdb", "result", result)
	}

	deployment := manifests.OpenShiftOAuthAPIServerDeployment(hcp.Namespace)
	if _, err := createOrUpdate(ctx, r, deployment, func() error {
		return oapi.ReconcileOAuthAPIServerDeployment(deployment, p.OwnerRef, p.OAuthAPIServerDeploymentParams(), util.APIPort(hcp))
	}); err != nil {
		return fmt.Errorf("failed to reconcile openshift oauth apiserver deployment: %w", err)
	}
	return nil
}

func (r *HostedControlPlaneReconciler) reconcileOAuthServer(ctx context.Context, hcp *hyperv1.HostedControlPlane, releaseImage *releaseinfo.ReleaseImage, oauthHost string, oauthPort int32, createOrUpdate upsert.CreateOrUpdateFN) error {
	p := oauth.NewOAuthServerParams(hcp, releaseImage.ComponentImages(), oauthHost, oauthPort, r.SetDefaultSecurityContext)

	sessionSecret := manifests.OAuthServerServiceSessionSecret(hcp.Namespace)
	if _, err := createOrUpdate(ctx, r, sessionSecret, func() error {
		return oauth.ReconcileSessionSecret(sessionSecret, p.OwnerRef)
	}); err != nil {
		return fmt.Errorf("failed to reconcile oauth session secret: %w", err)
	}

	loginTemplate := manifests.OAuthServerDefaultLoginTemplateSecret(hcp.Namespace)
	if _, err := createOrUpdate(ctx, r, loginTemplate, func() error {
		return oauth.ReconcileLoginTemplateSecret(loginTemplate, p.OwnerRef)
	}); err != nil {
		return fmt.Errorf("failed to reconcile login template secret: %w", err)
	}

	providersTemplate := manifests.OAuthServerDefaultProviderSelectionTemplateSecret(hcp.Namespace)
	if _, err := createOrUpdate(ctx, r, providersTemplate, func() error {
		return oauth.ReconcileProviderSelectionTemplateSecret(providersTemplate, p.OwnerRef)
	}); err != nil {
		return fmt.Errorf("failed to reconcile provider selection template secret: %w", err)
	}

	errorTemplate := manifests.OAuthServerDefaultErrorTemplateSecret(hcp.Namespace)
	if _, err := createOrUpdate(ctx, r, errorTemplate, func() error {
		return oauth.ReconcileErrorTemplateSecret(errorTemplate, p.OwnerRef)
	}); err != nil {
		return fmt.Errorf("failed to reconcile error template secret: %w", err)
	}

	oauthServingCert := manifests.OpenShiftOAuthServerCert(hcp.Namespace)
	if err := r.Get(ctx, client.ObjectKeyFromObject(oauthServingCert), oauthServingCert); err != nil {
		return fmt.Errorf("cannot get oauth serving cert: %w", err)
	}

	oauthConfig := manifests.OAuthServerConfig(hcp.Namespace)
	if _, err := createOrUpdate(ctx, r, oauthConfig, func() error {
		return oauth.ReconcileOAuthServerConfig(ctx, oauthConfig, p.OwnerRef, r.Client, p.ConfigParams(oauthServingCert))
	}); err != nil {
		return fmt.Errorf("failed to reconcile oauth server config: %w", err)
	}

	pdb := manifests.OAuthServerPodDisruptionBudget(hcp.Namespace)
	if result, err := createOrUpdate(ctx, r, pdb, func() error {
		return oauth.ReconcilePodDisruptionBudget(pdb, p)
	}); err != nil {
		return fmt.Errorf("failed to reconcile oauth pdb: %w", err)
	} else {
		r.Log.V(2).Info("Reconciled oauth pdb", "result", result)
	}

	deployment := manifests.OAuthServerDeployment(hcp.Namespace)
	if _, err := createOrUpdate(ctx, r, deployment, func() error {
		return oauth.ReconcileDeployment(ctx, r, deployment, p.OwnerRef, oauthConfig, p.OAuthServerImage, p.DeploymentConfig, p.IdentityProviders(), p.OauthConfigOverrides, p.AvailabilityProberImage, util.APIPort(hcp), p.NamedCertificates(), p.Socks5ProxyImage)
	}); err != nil {
		return fmt.Errorf("failed to reconcile oauth deployment: %w", err)
	}
	return nil
}

func (r *HostedControlPlaneReconciler) reconcileOpenShiftControllerManager(ctx context.Context, hcp *hyperv1.HostedControlPlane, observedConfig *globalconfig.ObservedConfig, releaseImage *releaseinfo.ReleaseImage, createOrUpdate upsert.CreateOrUpdateFN) error {
	p := ocm.NewOpenShiftControllerManagerParams(hcp, observedConfig, releaseImage.ComponentImages(), r.SetDefaultSecurityContext)
	config := manifests.OpenShiftControllerManagerConfig(hcp.Namespace)
	if _, err := createOrUpdate(ctx, r, config, func() error {
		return ocm.ReconcileOpenShiftControllerManagerConfig(config, p.OwnerRef, p.DeployerImage, p.DockerBuilderImage, p.MinTLSVersion(), p.CipherSuites(), p.Image, p.Build, p.Network)
	}); err != nil {
		return fmt.Errorf("failed to reconcile openshift controller manager config: %w", err)
	}

	service := manifests.OpenShiftControllerService(hcp.Namespace)
	if _, err := createOrUpdate(ctx, r, service, func() error {
		return ocm.ReconcileService(service, p.OwnerRef)
	}); err != nil {
		return fmt.Errorf("failed to reconcile openshift controller manager service: %w", err)
	}

	serviceMonitor := manifests.OpenShiftControllerServiceMonitor(hcp.Namespace)
	if _, err := createOrUpdate(ctx, r, serviceMonitor, func() error {
		return ocm.ReconcileServiceMonitor(serviceMonitor, p.OwnerRef, hcp.Spec.ClusterID, r.MetricsSet)
	}); err != nil {
		return fmt.Errorf("failed to reconcile openshift controller manager service monitor: %w", err)
	}

	deployment := manifests.OpenShiftControllerManagerDeployment(hcp.Namespace)
	if _, err := createOrUpdate(ctx, r, deployment, func() error {
		return ocm.ReconcileDeployment(deployment, p.OwnerRef, p.OpenShiftControllerManagerImage, config, p.DeploymentConfig)
	}); err != nil {
		return fmt.Errorf("failed to reconcile openshift controller manager deployment: %w", err)
	}
	return nil
}

func (r *HostedControlPlaneReconciler) reconcileClusterPolicyController(ctx context.Context, hcp *hyperv1.HostedControlPlane, releaseImage *releaseinfo.ReleaseImage, createOrUpdate upsert.CreateOrUpdateFN) error {
	p := clusterpolicy.NewClusterPolicyControllerParams(hcp, releaseImage.ComponentImages(), r.SetDefaultSecurityContext)

	config := manifests.ClusterPolicyControllerConfig(hcp.Namespace)
	if _, err := createOrUpdate(ctx, r, config, func() error {
		return clusterpolicy.ReconcileClusterPolicyControllerConfig(config, p.OwnerRef, p.MinTLSVersion(), p.CipherSuites())
	}); err != nil {
		return fmt.Errorf("failed to reconcile cluster policy controller config: %w", err)
	}

	deployment := manifests.ClusterPolicyControllerDeployment(hcp.Namespace)
	if _, err := createOrUpdate(ctx, r, deployment, func() error {
		return clusterpolicy.ReconcileDeployment(deployment, p.OwnerRef, p.Image, p.DeploymentConfig, p.AvailabilityProberImage, util.APIPort(hcp))
	}); err != nil {
		return fmt.Errorf("failed to reconcile cluster policy controller deployment: %w", err)
	}
	return nil
}

func (r *HostedControlPlaneReconciler) reconcileClusterVersionOperator(ctx context.Context, hcp *hyperv1.HostedControlPlane, releaseImage *releaseinfo.ReleaseImage, createOrUpdate upsert.CreateOrUpdateFN) error {
	p := cvo.NewCVOParams(hcp, releaseImage.ComponentImages(), r.SetDefaultSecurityContext)

	service := manifests.ClusterVersionOperatorService(hcp.Namespace)
	if _, err := createOrUpdate(ctx, r, service, func() error {
		return cvo.ReconcileService(service, p.OwnerRef)
	}); err != nil {
		return fmt.Errorf("failed to reconcile cluster version operator service: %w", err)
	}

	serviceMonitor := manifests.ClusterVersionOperatorServiceMonitor(hcp.Namespace)
	if _, err := createOrUpdate(ctx, r, serviceMonitor, func() error {
		return cvo.ReconcileServiceMonitor(serviceMonitor, p.OwnerRef, hcp.Spec.ClusterID, r.MetricsSet)
	}); err != nil {
		return fmt.Errorf("failed to reconcile cluster version operator service monitor: %w", err)
	}

	deployment := manifests.ClusterVersionOperatorDeployment(hcp.Namespace)
	if _, err := createOrUpdate(ctx, r, deployment, func() error {
		return cvo.ReconcileDeployment(deployment, p.OwnerRef, p.DeploymentConfig, p.Image, p.CLIImage, p.AvailabilityProberImage, p.ClusterID, util.APIPort(hcp))
	}); err != nil {
		return fmt.Errorf("failed to reconcile cluster version operator deployment: %w", err)
	}
	return nil
}

func (r *HostedControlPlaneReconciler) reconcileClusterNetworkOperator(ctx context.Context, hcp *hyperv1.HostedControlPlane, releaseImage *releaseinfo.ReleaseImage, createOrUpdate upsert.CreateOrUpdateFN) error {
	p := cno.NewParams(hcp, releaseImage.Version(), releaseImage.ComponentImages(), r.SetDefaultSecurityContext)

	role := manifests.ClusterNetworkOperatorRole(hcp.Namespace)
	if _, err := createOrUpdate(ctx, r.Client, role, func() error {
		return cno.ReconcileRole(role, p.OwnerRef)
	}); err != nil {
		return fmt.Errorf("failed to reconcile cluster network operator role: %w", err)
	}

	rb := manifests.ClusterNetworkOperatorRoleBinding(hcp.Namespace)
	if _, err := createOrUpdate(ctx, r.Client, rb, func() error {
		return cno.ReconcileRoleBinding(rb, p.OwnerRef)
	}); err != nil {
		return fmt.Errorf("failed to reconcile cluster network operator rolebinding: %w", err)
	}

	deployment := manifests.ClusterNetworkOperatorDeployment(hcp.Namespace)
	if _, err := createOrUpdate(ctx, r, deployment, func() error {
		return cno.ReconcileDeployment(deployment, p, util.APIPort(hcp))
	}); err != nil {
		return fmt.Errorf("failed to reconcile cluster network operator deployment: %w", err)
	}
	return nil
}

func (r *HostedControlPlaneReconciler) reconcileIngressOperator(ctx context.Context, hcp *hyperv1.HostedControlPlane, releaseImage *releaseinfo.ReleaseImage, createOrUpdate upsert.CreateOrUpdateFN) error {
	p := ingressoperator.NewParams(hcp, releaseImage.Version(), releaseImage.ComponentImages(), r.SetDefaultSecurityContext, hcp.Spec.Platform.Type)

	rootCASecret := manifests.RootCASecret(hcp.Namespace)
	if err := r.Client.Get(ctx, client.ObjectKeyFromObject(rootCASecret), rootCASecret); err != nil {
		return err
	}

	kubeconfig := manifests.IngressOperatorKubeconfig(hcp.Namespace)
	if _, err := createOrUpdate(ctx, r, kubeconfig, func() error {
		return pki.ReconcileServiceAccountKubeconfig(kubeconfig, rootCASecret, hcp, "openshift-ingress-operator", "ingress-operator")
	}); err != nil {
		return fmt.Errorf("failed to reconcile ingressoperator kubeconfig: %w", err)
	}

	deployment := manifests.IngressOperatorDeployment(hcp.Namespace)
	if _, err := createOrUpdate(ctx, r, deployment, func() error {
		ingressoperator.ReconcileDeployment(deployment, p, util.APIPort(hcp))
		return nil
	}); err != nil {
		return fmt.Errorf("failed to reconcile ingressoperator deployment: %w", err)
	}

	return nil
}

func (r *HostedControlPlaneReconciler) reconcileOperatorLifecycleManager(ctx context.Context, hcp *hyperv1.HostedControlPlane, releaseImage *releaseinfo.ReleaseImage, packageServerAddress string, createOrUpdate upsert.CreateOrUpdateFN) error {
	p := olm.NewOperatorLifecycleManagerParams(hcp, releaseImage.ComponentImages(), releaseImage.Version(), r.SetDefaultSecurityContext)

	if hcp.Spec.OLMCatalogPlacement == hyperv1.ManagementOLMCatalogPlacement {
		certifiedOperatorsService := manifests.CertifiedOperatorsService(hcp.Namespace)
		if _, err := createOrUpdate(ctx, r, certifiedOperatorsService, func() error {
			return olm.ReconcileCertifiedOperatorsService(certifiedOperatorsService, p.OwnerRef)
		}); err != nil {
			return fmt.Errorf("failed to reconcile certified operators service: %w", err)
		}
		communityOperatorsService := manifests.CommunityOperatorsService(hcp.Namespace)
		if _, err := createOrUpdate(ctx, r, communityOperatorsService, func() error {
			return olm.ReconcileCommunityOperatorsService(communityOperatorsService, p.OwnerRef)
		}); err != nil {
			return fmt.Errorf("failed to reconcile community operators service: %w", err)
		}
		marketplaceOperatorsService := manifests.RedHatMarketplaceOperatorsService(hcp.Namespace)
		if _, err := createOrUpdate(ctx, r, marketplaceOperatorsService, func() error {
			return olm.ReconcileRedHatMarketplaceOperatorsService(marketplaceOperatorsService, p.OwnerRef)
		}); err != nil {
			return fmt.Errorf("failed to reconcile marketplace operators service: %w", err)
		}
		redHatOperatorsService := manifests.RedHatOperatorsService(hcp.Namespace)
		if _, err := createOrUpdate(ctx, r, redHatOperatorsService, func() error {
			return olm.ReconcileRedHatOperatorsService(redHatOperatorsService, p.OwnerRef)
		}); err != nil {
			return fmt.Errorf("failed to reconcile red hat operators service: %w", err)
		}

		certifiedOperatorsDeployment := manifests.CertifiedOperatorsDeployment(hcp.Namespace)
		if _, err := createOrUpdate(ctx, r, certifiedOperatorsDeployment, func() error {
			return olm.ReconcileCertifiedOperatorsDeployment(certifiedOperatorsDeployment, p.OwnerRef, p.DeploymentConfig)
		}); err != nil {
			return fmt.Errorf("failed to reconcile certified operators deployment: %w", err)
		}
		communityOperatorsDeployment := manifests.CommunityOperatorsDeployment(hcp.Namespace)
		if _, err := createOrUpdate(ctx, r, communityOperatorsDeployment, func() error {
			return olm.ReconcileCommunityOperatorsDeployment(communityOperatorsDeployment, p.OwnerRef, p.DeploymentConfig)
		}); err != nil {
			return fmt.Errorf("failed to reconcile community operators deployment: %w", err)
		}
		marketplaceOperatorsDeployment := manifests.RedHatMarketplaceOperatorsDeployment(hcp.Namespace)
		if _, err := createOrUpdate(ctx, r, marketplaceOperatorsDeployment, func() error {
			return olm.ReconcileRedHatMarketplaceOperatorsDeployment(marketplaceOperatorsDeployment, p.OwnerRef, p.DeploymentConfig)
		}); err != nil {
			return fmt.Errorf("failed to reconcile marketplace operators deployment: %w", err)
		}
		redHatOperatorsDeployment := manifests.RedHatOperatorsDeployment(hcp.Namespace)
		if _, err := createOrUpdate(ctx, r, redHatOperatorsDeployment, func() error {
			return olm.ReconcileRedHatOperatorsDeployment(redHatOperatorsDeployment, p.OwnerRef, p.DeploymentConfig)
		}); err != nil {
			return fmt.Errorf("failed to reconcile red hat operators deployment: %w", err)
		}

		catalogRolloutSA := manifests.CatalogRolloutServiceAccount(hcp.Namespace)
		if _, err := createOrUpdate(ctx, r, catalogRolloutSA, func() error {
			return olm.ReconcileCatalogRolloutServiceAccount(catalogRolloutSA, p.OwnerRef)
		}); err != nil {
			return fmt.Errorf("failed to reconcile catalog rollout service account: %w", err)
		}
		catalogRolloutRole := manifests.CatalogRolloutRole(hcp.Namespace)
		if _, err := createOrUpdate(ctx, r, catalogRolloutRole, func() error {
			return olm.ReconcileCatalogRolloutRole(catalogRolloutRole, p.OwnerRef)
		}); err != nil {
			return fmt.Errorf("failed to reconcile catalog rollout role: %w", err)
		}
		catalogRolloutRoleBinding := manifests.CatalogRolloutRoleBinding(hcp.Namespace)
		if _, err := createOrUpdate(ctx, r, catalogRolloutRoleBinding, func() error {
			return olm.ReconcileCatalogRolloutRoleBinding(catalogRolloutRoleBinding, p.OwnerRef)
		}); err != nil {
			return fmt.Errorf("failed to reconcile catalog rollout rolebinding: %w", err)
		}

		certifiedOperatorsCronJob := manifests.CertifiedOperatorsCronJob(hcp.Namespace)
		if _, err := createOrUpdate(ctx, r, certifiedOperatorsCronJob, func() error {
			return olm.ReconcileCertifiedOperatorsCronJob(certifiedOperatorsCronJob, p.OwnerRef, p.CLIImage)
		}); err != nil {
			return fmt.Errorf("failed to reconcile certified operators cronjob: %w", err)
		}
		communityOperatorsCronJob := manifests.CommunityOperatorsCronJob(hcp.Namespace)
		if _, err := createOrUpdate(ctx, r, communityOperatorsCronJob, func() error {
			return olm.ReconcileCommunityOperatorsCronJob(communityOperatorsCronJob, p.OwnerRef, p.CLIImage)
		}); err != nil {
			return fmt.Errorf("failed to reconcile community operators cronjob: %w", err)
		}
		marketplaceOperatorsCronJob := manifests.RedHatMarketplaceOperatorsCronJob(hcp.Namespace)
		if _, err := createOrUpdate(ctx, r, marketplaceOperatorsCronJob, func() error {
			return olm.ReconcileRedHatMarketplaceOperatorsCronJob(marketplaceOperatorsCronJob, p.OwnerRef, p.CLIImage)
		}); err != nil {
			return fmt.Errorf("failed to reconcile marketplace operators cronjob: %w", err)
		}
		redHatOperatorsCronJob := manifests.RedHatOperatorsCronJob(hcp.Namespace)
		if _, err := createOrUpdate(ctx, r, redHatOperatorsCronJob, func() error {
			return olm.ReconcileRedHatOperatorsCronJob(redHatOperatorsCronJob, p.OwnerRef, p.CLIImage)
		}); err != nil {
			return fmt.Errorf("failed to reconcile red hat operators cronjob: %w", err)
		}
	}

	catalogOperatorMetricsService := manifests.CatalogOperatorMetricsService(hcp.Namespace)
	if _, err := createOrUpdate(ctx, r, catalogOperatorMetricsService, func() error {
		return olm.ReconcileCatalogOperatorMetricsService(catalogOperatorMetricsService, p.OwnerRef)
	}); err != nil {
		return fmt.Errorf("failed to reconcile catalog operator metrics service: %w", err)
	}
	catalogOperatorServiceMonitor := manifests.CatalogOperatorServiceMonitor(hcp.Namespace)
	if _, err := createOrUpdate(ctx, r, catalogOperatorServiceMonitor, func() error {
		return olm.ReconcileCatalogServiceMonitor(catalogOperatorServiceMonitor, p.OwnerRef, hcp.Spec.ClusterID, r.MetricsSet)
	}); err != nil {
		return fmt.Errorf("failed to reconcile catalog operator service monitor: %w", err)
	}
	catalogOperatorDeployment := manifests.CatalogOperatorDeployment(hcp.Namespace)
	if _, err := createOrUpdate(ctx, r, catalogOperatorDeployment, func() error {
		return olm.ReconcileCatalogOperatorDeployment(catalogOperatorDeployment, p.OwnerRef, p.OLMImage, p.ProxyImage, p.OperatorRegistryImage, p.ReleaseVersion, p.DeploymentConfig, p.AvailabilityProberImage, util.APIPort(hcp), p.NoProxy)
	}); err != nil {
		return fmt.Errorf("failed to reconcile catalog operator deployment: %w", err)
	}

	olmOperatorMetricsService := manifests.OLMOperatorMetricsService(hcp.Namespace)
	if _, err := createOrUpdate(ctx, r, olmOperatorMetricsService, func() error {
		return olm.ReconcileOLMOperatorMetricsService(olmOperatorMetricsService, p.OwnerRef)
	}); err != nil {
		return fmt.Errorf("failed to reconcile olm operator metrics service: %w", err)
	}

	olmOperatorServiceMonitor := manifests.OLMOperatorServiceMonitor(hcp.Namespace)
	if _, err := createOrUpdate(ctx, r, olmOperatorServiceMonitor, func() error {
		return olm.ReconcileOLMOperatorServiceMonitor(olmOperatorServiceMonitor, p.OwnerRef, hcp.Spec.ClusterID, r.MetricsSet)
	}); err != nil {
		return fmt.Errorf("failed to reconcile olm operator service monitor: %w", err)
	}

	olmOperatorDeployment := manifests.OLMOperatorDeployment(hcp.Namespace)
	if _, err := createOrUpdate(ctx, r, olmOperatorDeployment, func() error {
		return olm.ReconcileOLMOperatorDeployment(olmOperatorDeployment, p.OwnerRef, p.OLMImage, p.ProxyImage, p.ReleaseVersion, p.DeploymentConfig, p.AvailabilityProberImage, util.APIPort(hcp), p.NoProxy)
	}); err != nil {
		return fmt.Errorf("failed to reconcile olm operator deployment: %w", err)
	}

	packageServerDeployment := manifests.OLMPackageServerDeployment(hcp.Namespace)
	if _, err := createOrUpdate(ctx, r, packageServerDeployment, func() error {
		return olm.ReconcilePackageServerDeployment(packageServerDeployment, p.OwnerRef, p.OLMImage, p.ProxyImage, p.ReleaseVersion, p.PackageServerConfig, p.AvailabilityProberImage, util.APIPort(hcp), p.NoProxy)
	}); err != nil {
		return fmt.Errorf("failed to reconcile packageserver deployment: %w", err)
	}

	// Collect Profiles
	collectProfilesConfigMap := manifests.CollectProfilesConfigMap(hcp.Namespace)
	if _, err := createOrUpdate(ctx, r, collectProfilesConfigMap, func() error {
		olm.ReconcileCollectProfilesConfigMap(collectProfilesConfigMap, p.OwnerRef)
		return nil
	}); err != nil {
		return fmt.Errorf("failed to reconcile collect profiles config map: %w", err)
	}

	collectProfilesCronJob := manifests.CollectProfilesCronJob(hcp.Namespace)
	if _, err := createOrUpdate(ctx, r, collectProfilesCronJob, func() error {
		olm.ReconcileCollectProfilesCronJob(collectProfilesCronJob, p.OwnerRef, p.OLMImage, hcp.Namespace)
		return nil
	}); err != nil {
		return fmt.Errorf("failed to reconcile collect profiles cronjob: %w", err)
	}

	collectProfilesRole := manifests.CollectProfilesRole(hcp.Namespace)
	if _, err := createOrUpdate(ctx, r, collectProfilesRole, func() error {
		olm.ReconcileCollectProfilesRole(collectProfilesRole, p.OwnerRef)
		return nil
	}); err != nil {
		return fmt.Errorf("failed to reconcile collect profiles role: %w", err)
	}

	collectProfilesRoleBinding := manifests.CollectProfilesRoleBinding(hcp.Namespace)
	if _, err := createOrUpdate(ctx, r, collectProfilesRoleBinding, func() error {
		olm.ReconcileCollectProfilesRoleBinding(collectProfilesRoleBinding, p.OwnerRef)
		return nil
	}); err != nil {
		return fmt.Errorf("failed to reconcile collect profiles rolebinding: %w", err)
	}

	collectProfilesSecret := manifests.CollectProfilesSecret(hcp.Namespace)
	if _, err := createOrUpdate(ctx, r, collectProfilesSecret, func() error {
		olm.ReconcileCollectProfilesSecret(collectProfilesSecret, p.OwnerRef)
		return nil
	}); err != nil {
		return fmt.Errorf("failed to reconcile collect profiles secret: %w", err)
	}

	collectProfilesServiceAccount := manifests.CollectProfilesServiceAccount(hcp.Namespace)
	if _, err := createOrUpdate(ctx, r, collectProfilesServiceAccount, func() error {
		olm.ReconcileCollectProfilesServiceAccount(collectProfilesServiceAccount, p.OwnerRef)
		return nil
	}); err != nil {
		return fmt.Errorf("failed to reconcile collect profiles serviceaccount: %w", err)
	}
	return nil
}

func (r *HostedControlPlaneReconciler) reconcileImageRegistryOperator(ctx context.Context, hcp *hyperv1.HostedControlPlane, releaseImage *releaseinfo.ReleaseImage, createOrUpdate upsert.CreateOrUpdateFN) error {
	params := registryoperator.NewParams(hcp, releaseImage.Version(), releaseImage.ComponentImages(), r.SetDefaultSecurityContext)
	deployment := manifests.ImageRegistryOperatorDeployment(hcp.Namespace)
	if _, err := createOrUpdate(ctx, r, deployment, func() error {
		return registryoperator.ReconcileDeployment(deployment, params)
	}); err != nil {
		return fmt.Errorf("failed to reconcile image registry operator deployment: %w", err)
	}

	pm := manifests.ImageRegistryOperatorPodMonitor(hcp.Namespace)
	if _, err := createOrUpdate(ctx, r, pm, func() error {
		registryoperator.ReconcilePodMonitor(pm, hcp.Spec.ClusterID, r.MetricsSet)
		return nil
	}); err != nil {
		return fmt.Errorf("failed to reconcile image registry operator pod monitor: %w", err)
	}

	return nil
}

func (r *HostedControlPlaneReconciler) reconcileMachineConfigServerConfig(ctx context.Context, hcp *hyperv1.HostedControlPlane, createOrUpdate upsert.CreateOrUpdateFN) error {
	rootCA := manifests.RootCASecret(hcp.Namespace)
	if err := r.Get(ctx, client.ObjectKeyFromObject(rootCA), rootCA); err != nil {
		return fmt.Errorf("failed to get root ca: %w", err)
	}
	combinedCA := manifests.CombinedCAConfigMap(hcp.Namespace)
	if err := r.Get(ctx, client.ObjectKeyFromObject(combinedCA), combinedCA); err != nil {
		return fmt.Errorf("failed to get combined ca: %w", err)
	}
	pullSecret := common.PullSecret(hcp.Namespace)
	if err := r.Get(ctx, client.ObjectKeyFromObject(pullSecret), pullSecret); err != nil {
		return fmt.Errorf("failed to get pull secret: %w", err)
	}

	var userCA *corev1.ConfigMap
	if hcp.Spec.AdditionalTrustBundle != nil {
		userCA = manifests.UserCAConfigMap(hcp.Namespace)
		if err := r.Get(ctx, client.ObjectKeyFromObject(userCA), userCA); err != nil {
			return fmt.Errorf("failed to get user ca: %w", err)
		}
	}

	p := mcs.NewMCSParams(hcp, rootCA, pullSecret, combinedCA, userCA)

	cm := manifests.MachineConfigServerConfig(hcp.Namespace)
	if _, err := createOrUpdate(ctx, r, cm, func() error {
		return mcs.ReconcileMachineConfigServerConfig(cm, p)
	}); err != nil {
		return fmt.Errorf("failed to reconcile machine config server config: %w", err)
	}
	return nil
}

func (r *HostedControlPlaneReconciler) reconcileCoreIgnitionConfig(ctx context.Context, hcp *hyperv1.HostedControlPlane, createOrUpdate upsert.CreateOrUpdateFN) error {
	sshKey := ""
	if len(hcp.Spec.SSHKey.Name) > 0 {
		var sshKeySecret corev1.Secret
		err := r.Client.Get(ctx, client.ObjectKey{Namespace: hcp.Namespace, Name: hcp.Spec.SSHKey.Name}, &sshKeySecret)
		if err != nil {
			return fmt.Errorf("failed to get SSH key secret %s: %w", hcp.Spec.SSHKey.Name, err)
		}
		data, hasSSHKeyData := sshKeySecret.Data["id_rsa.pub"]
		if !hasSSHKeyData {
			return fmt.Errorf("SSH key secret secret %s is missing the id_rsa.pub key", hcp.Spec.SSHKey.Name)
		}
		sshKey = string(data)
	}

	p := ignition.NewIgnitionConfigParams(hcp, sshKey)

	fipsConfig := manifests.IgnitionFIPSConfig(hcp.Namespace)
	if _, err := createOrUpdate(ctx, r, fipsConfig, func() error {
		return ignition.ReconcileFIPSIgnitionConfig(fipsConfig, p.OwnerRef, p.FIPSEnabled)
	}); err != nil {
		return fmt.Errorf("failed to reconcile fips ignition config: %w", err)
	}

	sshKeyConfig := manifests.IgnitionWorkerSSHConfig(hcp.Namespace)
	if _, err := createOrUpdate(ctx, r, sshKeyConfig, func() error {
		return ignition.ReconcileWorkerSSHIgnitionConfig(sshKeyConfig, p.OwnerRef, sshKey)
	}); err != nil {
		return fmt.Errorf("failed to reconcile ssh key ignition config: %w", err)
	}

	imageContentSourceIgnitionConfig := manifests.ImageContentSourcePolicyIgnitionConfig(hcp.GetNamespace())
	if !p.HasImageContentSourcePolicy {
		// ensure the icsp configmap has been removed if no longer needed
		err := r.Get(ctx, client.ObjectKeyFromObject(imageContentSourceIgnitionConfig), imageContentSourceIgnitionConfig)
		if err != nil {
			if !apierrors.IsNotFound(err) {
				return fmt.Errorf("failed to check whether image content source policy configuration configmap exists: %w", err)
			}
		} else {
			if err := r.Delete(ctx, imageContentSourceIgnitionConfig); err != nil {
				return fmt.Errorf("failed to delete image content source policy configuration configmap: %w", err)
			}
		}
		return nil
	}

	icsp := globalconfig.ImageContentSourcePolicy()
	if err := globalconfig.ReconcileImageContentSourcePolicy(icsp, hcp); err != nil {
		return fmt.Errorf("failed to reconcile image content source policy: %w", err)
	}

	if _, err := createOrUpdate(ctx, r, imageContentSourceIgnitionConfig, func() error {
		return ignition.ReconcileImageContentSourcePolicyIgnitionConfig(imageContentSourceIgnitionConfig, p.OwnerRef, icsp)
	}); err != nil {
		return fmt.Errorf("failed to reconcile image content source policy ignition config: %w", err)
	}

	return nil
}

func (r *HostedControlPlaneReconciler) reconcilePrivateIngressController(ctx context.Context, hcp *hyperv1.HostedControlPlane) error {
	ic := manifests.IngressPrivateIngressController(hcp.Namespace)
	if err := r.Get(ctx, client.ObjectKeyFromObject(ic), ic); err == nil {
		if err = r.Delete(ctx, ic); err != nil {
			return fmt.Errorf("failed to delete private ingress controller: %w", err)
		}
	} else if !apierrors.IsNotFound(err) {
		return fmt.Errorf("failed to get private ingress controller: %w", err)
	}
	return nil
}

func (r *HostedControlPlaneReconciler) reconcileRouter(ctx context.Context, hcp *hyperv1.HostedControlPlane, releaseInfo *releaseinfo.ReleaseImage, createOrUpdate upsert.CreateOrUpdateFN, exposeAPIThroughRouter bool) error {
	sa := manifests.RouterServiceAccount(hcp.Namespace)
	if _, err := createOrUpdate(ctx, r.Client, sa, func() error {
		return ingress.ReconcileRouterServiceAccount(sa, config.OwnerRefFrom(hcp))
	}); err != nil {
		return fmt.Errorf("failed to reconcile router service account: %w", err)
	}
	role := manifests.RouterRole(hcp.Namespace)
	if _, err := createOrUpdate(ctx, r.Client, role, func() error {
		return ingress.ReconcileRouterRole(role, config.OwnerRefFrom(hcp))
	}); err != nil {
		return fmt.Errorf("failed to reconcile router role: %w", err)
	}
	rb := manifests.RouterRoleBinding(hcp.Namespace)
	if _, err := createOrUpdate(ctx, r.Client, rb, func() error {
		return ingress.ReconcileRouterRoleBinding(rb, config.OwnerRefFrom(hcp))
	}); err != nil {
		return fmt.Errorf("failed to reconcile router rolebinding: %w", err)
	}

	var canonicalHostname string
	if util.IsPrivateHCP(hcp) {
		svc := manifests.PrivateRouterService(hcp.Namespace)
		if _, err := createOrUpdate(ctx, r.Client, svc, func() error {
			return ingress.ReconcileRouterService(svc, config.OwnerRefFrom(hcp), util.APIPortWithDefault(hcp, config.DefaultAPIServerPort), true)
		}); err != nil {
			return fmt.Errorf("failed to reconcile router service: %w", err)
		}
		if (!util.IsPublicHCP(hcp) || !exposeAPIThroughRouter) && len(svc.Status.LoadBalancer.Ingress) > 0 {
			canonicalHostname = svc.Status.LoadBalancer.Ingress[0].Hostname
		}
	}

	if util.IsPublicHCP(hcp) && exposeAPIThroughRouter {
		pubSvc := manifests.RouterPublicService(hcp.Namespace)
		if _, err := createOrUpdate(ctx, r.Client, pubSvc, func() error {
			return ingress.ReconcileRouterService(pubSvc, config.OwnerRefFrom(hcp), util.APIPortWithDefault(hcp, config.DefaultAPIServerPort), false)
		}); err != nil {
			return fmt.Errorf("failed to reconcile router service: %w", err)
		}
		if len(pubSvc.Status.LoadBalancer.Ingress) > 0 {
			canonicalHostname = pubSvc.Status.LoadBalancer.Ingress[0].Hostname
		}
	}

	if exposeAPIThroughRouter {
		routerTemplate := manifests.RouterTemplateConfigMap(hcp.Namespace)
		if _, err := createOrUpdate(ctx, r.Client, routerTemplate, func() error {
			ingress.ReconcileRouterTemplateConfigmap(routerTemplate)
			return nil
		}); err != nil {
			return fmt.Errorf("failed to reconcile router template configmap: %w", err)
		}

		// We have to wait for the LB to be ready, otherwise the router might already admit the KAS route but not populate
		// the routerCanonicalHostname field, causing external DNS to not create the DNS entry. It doesn't add that field
		// later for already-admitted routes.
		if canonicalHostname == "" {
			r.Log.Info("Waiting for load balancer to be ready before creating router deployment")
			return nil
		}
	}

	if util.IsPrivateHCP(hcp) || exposeAPIThroughRouter {
		deployment := manifests.RouterDeployment(hcp.Namespace)
		if _, err := createOrUpdate(ctx, r.Client, deployment, func() error {
			return ingress.ReconcileRouterDeployment(deployment,
				config.OwnerRefFrom(hcp),
				ingress.PrivateRouterConfig(hcp, r.SetDefaultSecurityContext),
				ingress.PrivateRouterImage(releaseInfo.ComponentImages()),
				canonicalHostname,
				exposeAPIThroughRouter,
			)
		}); err != nil {
			return fmt.Errorf("failed to reconcile router deployment: %w", err)
		}
	}

	oldRouterResources := []client.Object{
		&appsv1.Deployment{ObjectMeta: metav1.ObjectMeta{Namespace: hcp.Namespace, Name: "private-router"}},
		&rbacv1.RoleBinding{ObjectMeta: metav1.ObjectMeta{Namespace: hcp.Namespace, Name: "private-router"}},
		&rbacv1.Role{ObjectMeta: metav1.ObjectMeta{Namespace: hcp.Namespace, Name: "private-router"}},
		&corev1.ServiceAccount{ObjectMeta: metav1.ObjectMeta{Namespace: hcp.Namespace, Name: "private-router"}},
	}
	for _, resource := range oldRouterResources {
		if err := deleteIfExists(ctx, r.Client, resource); err != nil {
			return fmt.Errorf("failed to delete %T %s: %w", resource, resource.GetName(), err)
		}
	}

	return nil
}

func deleteIfExists(ctx context.Context, c client.Client, o client.Object) error {
	if err := c.Get(ctx, client.ObjectKeyFromObject(o), o); err != nil {
		if apierrors.IsNotFound(err) {
			return nil
		}
		return fmt.Errorf("failed to get object: %w", err)
	}
	if err := c.Delete(ctx, o); err != nil {
		return fmt.Errorf("failed to delete object: %w", err)
	}
	return nil
}

func (r *HostedControlPlaneReconciler) reconcileHostedClusterConfigOperator(ctx context.Context, hcp *hyperv1.HostedControlPlane, releaseInfo *releaseinfo.ReleaseImage, infraStatus InfrastructureStatus, createOrUpdate upsert.CreateOrUpdateFN) error {
	versions, err := releaseInfo.ComponentVersions()
	if err != nil {
		return fmt.Errorf("failed to get component versions: %w", err)
	}
	p := configoperator.NewHostedClusterConfigOperatorParams(ctx, hcp, releaseInfo.ComponentImages(), releaseInfo.Version(), versions["kubernetes"], r.SetDefaultSecurityContext)

	sa := manifests.ConfigOperatorServiceAccount(hcp.Namespace)
	if _, err = createOrUpdate(ctx, r.Client, sa, func() error {
		return configoperator.ReconcileServiceAccount(sa, p.OwnerRef)
	}); err != nil {
		return fmt.Errorf("failed to reconcile config operator service account: %w", err)
	}

	role := manifests.ConfigOperatorRole(hcp.Namespace)
	if _, err = createOrUpdate(ctx, r.Client, role, func() error {
		return configoperator.ReconcileRole(role, p.OwnerRef)
	}); err != nil {
		return fmt.Errorf("failed to reconcile config operator role: %w", err)
	}

	rb := manifests.ConfigOperatorRoleBinding(hcp.Namespace)
	if _, err = createOrUpdate(ctx, r.Client, rb, func() error {
		return configoperator.ReconcileRoleBinding(rb, p.OwnerRef)
	}); err != nil {
		return fmt.Errorf("failed to reconcile config operator rolebinding: %w", err)
	}

	deployment := manifests.ConfigOperatorDeployment(hcp.Namespace)
	if _, err = createOrUpdate(ctx, r.Client, deployment, func() error {
		return configoperator.ReconcileDeployment(deployment, p.Image, hcp.Name, p.OpenShiftVersion, p.KubernetesVersion, p.OwnerRef, &p.DeploymentConfig, p.AvailabilityProberImage, r.EnableCIDebugOutput, hcp.Spec.Platform.Type, util.APIPort(hcp), infraStatus.KonnectivityHost, infraStatus.KonnectivityPort, infraStatus.OAuthHost, infraStatus.OAuthPort, hcp.Spec.ReleaseImage, hcp.Spec.AdditionalTrustBundle)
	}); err != nil {
		return fmt.Errorf("failed to reconcile config operator deployment: %w", err)
	}

	podMonitor := manifests.ConfigOperatorPodMonitor(hcp.Namespace)
	if _, err := createOrUpdate(ctx, r.Client, podMonitor, func() error {
		podMonitor.Spec.Selector = *deployment.Spec.Selector
		podMonitor.Spec.PodMetricsEndpoints = []prometheusoperatorv1.PodMetricsEndpoint{{
			Interval:             "15s",
			Port:                 "metrics",
			MetricRelabelConfigs: metrics.HostedClusterConfigOperatorRelabelConfigs(r.MetricsSet),
		}}
		podMonitor.Spec.NamespaceSelector = prometheusoperatorv1.NamespaceSelector{MatchNames: []string{hcp.Namespace}}
		podMonitor.SetOwnerReferences([]metav1.OwnerReference{{
			APIVersion: hyperv1.GroupVersion.String(),
			Kind:       "HostedControlPlane",
			Name:       hcp.Name,
			UID:        hcp.UID,
		}})
		util.ApplyClusterIDLabelToPodMonitor(&podMonitor.Spec.PodMetricsEndpoints[0], hcp.Spec.ClusterID)
		return nil
	}); err != nil {
		return fmt.Errorf("failed to reconcile pod monitor for config operator: %w", err)
	}

	return nil
}

func generateKubeadminPassword() (string, error) {
	const (
		lowerLetters = "abcdefghijkmnopqrstuvwxyz"
		upperLetters = "ABCDEFGHIJKLMNPQRSTUVWXYZ"
		digits       = "23456789"
		all          = lowerLetters + upperLetters + digits
		length       = 23
	)
	var password string
	for i := 0; i < length; i++ {
		n, err := crand.Int(crand.Reader, big.NewInt(int64(len(all))))
		if err != nil {
			return "", err
		}
		newchar := string(all[n.Int64()])
		if password == "" {
			password = newchar
		}
		if i < length-1 {
			n, err = crand.Int(crand.Reader, big.NewInt(int64(len(password)+1)))
			if err != nil {
				return "", err
			}
			j := n.Int64()
			password = password[0:j] + newchar + password[j:]
		}
	}
	pw := []rune(password)
	for _, replace := range []int{5, 11, 17} {
		pw[replace] = '-'
	}
	return string(pw), nil
}

func reconcileKubeadminPasswordSecret(secret *corev1.Secret, hcp *hyperv1.HostedControlPlane, password *string) error {
	ownerRef := config.OwnerRefFrom(hcp)
	ownerRef.ApplyTo(secret)
	existingPassword, exists := secret.Data["password"]
	if !exists || len(existingPassword) == 0 {
		generated, err := generateKubeadminPassword()
		if err != nil {
			return fmt.Errorf("failed to generate kubeadmin password: %w", err)
		}
		*password = generated
		secret.Data = map[string][]byte{"password": []byte(generated)}
	} else {
		*password = string(existingPassword)
	}
	return nil
}

func (r *HostedControlPlaneReconciler) hostedControlPlaneInNamespace(resource client.Object) []reconcile.Request {
	hcpList := &hyperv1.HostedControlPlaneList{}
	if err := r.List(context.Background(), hcpList, &client.ListOptions{
		Namespace: resource.GetNamespace(),
	}); err != nil {
		r.Log.Error(err, "failed to list hosted control planes in namespace", "namespace", resource.GetNamespace())
		return nil
	}
	if len(hcpList.Items) > 1 {
		r.Log.Error(fmt.Errorf("more than one HostedControlPlane resource found in namespace %s", resource.GetNamespace()), "unexpected number of HostedControlPlane resources")
		return nil
	}
	var result []reconcile.Request
	for _, hcp := range hcpList.Items {
		result = append(result, reconcile.Request{NamespacedName: types.NamespacedName{Namespace: hcp.Namespace, Name: hcp.Name}})
	}
	return result
}

func (r *HostedControlPlaneReconciler) etcdRestoredCondition(ctx context.Context, sts *appsv1.StatefulSet) *metav1.Condition {
	if sts.Status.ReadyReplicas == *sts.Spec.Replicas {
		// Check that all etcd pods have initContainers that started
		podList := &corev1.PodList{}
		initContainerCount := int32(0)
		if err := r.List(ctx, podList, &client.ListOptions{
			Namespace:     sts.Namespace,
			LabelSelector: labels.SelectorFromValidatedSet(labels.Set{"app": "etcd"}),
		}); err == nil {
			for _, pod := range podList.Items {
				for _, status := range pod.Status.InitContainerStatuses {
					if status.Name == "etcd-init" {
						if status.Ready {
							initContainerCount += 1
						} else if status.LastTerminationState.Terminated != nil {
							if status.LastTerminationState.Terminated.ExitCode != 0 {
								return &metav1.Condition{
									Type:   string(hyperv1.EtcdSnapshotRestored),
									Status: metav1.ConditionFalse,
									Reason: status.LastTerminationState.Terminated.Reason,
								}
							}
						}
					}
				}
			}
		}

		if initContainerCount == *sts.Spec.Replicas {
			return &metav1.Condition{
				Type:   string(hyperv1.EtcdSnapshotRestored),
				Status: metav1.ConditionTrue,
				Reason: hyperv1.AsExpectedReason,
			}
		}
	}
	return nil
}

func (r *HostedControlPlaneReconciler) etcdStatefulSetCondition(ctx context.Context, sts *appsv1.StatefulSet) (*metav1.Condition, error) {
	if sts.Status.ReadyReplicas >= *sts.Spec.Replicas/2+1 {
		return &metav1.Condition{
			Type:   string(hyperv1.EtcdAvailable),
			Status: metav1.ConditionTrue,
			Reason: hyperv1.EtcdQuorumAvailableReason,
		}, nil
	}

	var message string

	// Check that any etcd PVCs have been provisioned
	pvcList := &corev1.PersistentVolumeClaimList{}
	if err := r.List(ctx, pvcList, &client.ListOptions{
		Namespace:     sts.Namespace,
		LabelSelector: labels.SelectorFromValidatedSet(labels.Set{"app": "etcd"}),
	}); err != nil {
		return nil, err
	}

	messageCollector := events.NewMessageCollector(ctx, r.Client)
	for _, pvc := range pvcList.Items {
		if pvc.Status.Phase != corev1.ClaimBound {
			eventMessages, err := messageCollector.ErrorMessages(&pvc)
			if err != nil {
				return nil, err
			}
			if len(eventMessages) > 0 {
				message = fmt.Sprintf("Etcd volume claim %s pending: %s", pvc.Name, strings.Join(eventMessages, "; "))
				break
			}
		}
	}

	if len(message) == 0 {
		message = "Waiting for etcd to reach quorum"
	}
	return &metav1.Condition{
		Type:    string(hyperv1.EtcdAvailable),
		Status:  metav1.ConditionFalse,
		Reason:  hyperv1.EtcdWaitingForQuorumReason,
		Message: message,
	}, nil

}

func (r *HostedControlPlaneReconciler) reconcileCloudControllerManager(ctx context.Context, hcp *hyperv1.HostedControlPlane, releaseImage *releaseinfo.ReleaseImage, createOrUpdate upsert.CreateOrUpdateFN) error {
	switch hcp.Spec.Platform.Type {
	case hyperv1.PowerVSPlatform:
		ccmConfig := manifests.PowerVSCCMConfigMap(hcp.Namespace)
		if _, err := createOrUpdate(ctx, r, ccmConfig, func() error {
			return powervs.ReconcileCCMConfigMap(ccmConfig, hcp)
		}); err != nil {
			return fmt.Errorf("failed to reconcile cloud controller manager config: %w", err)
		}

		deployment := manifests.PowerVSCCMDeployment(hcp.Namespace)
		if _, err := createOrUpdate(ctx, r, deployment, func() error {
			return powervs.ReconcileCCMDeployment(deployment, hcp, ccmConfig, releaseImage)
		}); err != nil {
			return fmt.Errorf("failed to reconcile cloud controller manager deployment: %w", err)
		}
	}
	return nil
}

// reconcileAutoscaler orchestrates reconciliation of autoscaler components using
func (r *HostedControlPlaneReconciler) reconcileAutoscaler(ctx context.Context, hcp *hyperv1.HostedControlPlane, images map[string]string, createOrUpdate upsert.CreateOrUpdateFN) error {
	autoscalerImage, ok := images[ImageStreamAutoscalerImage]
	if !ok {
		return fmt.Errorf("autoscaler image not found")
	}

	availabilityProberImage, ok := images[util.AvailabilityProberImageName]
	if !ok {
		return fmt.Errorf("availability prober image not found")
	}

	return autoscaler.ReconcileAutoscaler(ctx, r.Client, hcp, autoscalerImage, availabilityProberImage, createOrUpdate, r.SetDefaultSecurityContext)
}

func (r *HostedControlPlaneReconciler) reconcileMachineApprover(ctx context.Context, hcp *hyperv1.HostedControlPlane, images map[string]string, createOrUpdate upsert.CreateOrUpdateFN) error {
	machineApproverImage, ok := images[ImageStreamClusterMachineApproverImage]
	if !ok {
		return fmt.Errorf("autoscaler image not found")
	}

	availabilityProberImage, ok := images[util.AvailabilityProberImageName]
	if !ok {
		return fmt.Errorf("availability prober image not found")
	}

	return machineapprover.ReconcileMachineApprover(ctx, r.Client, hcp, machineApproverImage, availabilityProberImage, createOrUpdate, r.SetDefaultSecurityContext)
}

func shouldCleanupCloudResources(hcp *hyperv1.HostedControlPlane) bool {
	return hcp.Annotations[hyperv1.CleanupCloudResourcesAnnotation] == "true"
}

func (r *HostedControlPlaneReconciler) removeCloudResources(ctx context.Context, hcp *hyperv1.HostedControlPlane) (bool, error) {

	log := ctrl.LoggerFrom(ctx)
	log.Info("Removing cloud resources")

	// check if resources have been destroyed
	if meta.IsStatusConditionTrue(hcp.Status.Conditions, string(hyperv1.CloudResourcesDestroyed)) {
		log.Info("Guest resources have been destroyed")
		return true, nil
	}
	// if CVO has been scaled down, we're just waiting for resources to be destroyed
	if meta.IsStatusConditionTrue(hcp.Status.Conditions, string(hyperv1.CVOScaledDown)) {
		log.Info("Waiting for guest resources to be destroyed")
		return false, nil
	}

	// ensure CVO has been scaled down
	cvoDeployment := manifests.ClusterVersionOperatorDeployment(hcp.Namespace)
	err := r.Get(ctx, client.ObjectKeyFromObject(cvoDeployment), cvoDeployment)
	if err != nil && !apierrors.IsNotFound(err) {
		return false, fmt.Errorf("failed get CVO deployment: %w", err)
	}
	if err == nil && cvoDeployment.Spec.Replicas != nil && *cvoDeployment.Spec.Replicas > 0 {
		log.Info("Scaling down cluster version operator deployment")
		cvoDeployment.Spec.Replicas = pointer.Int32(0)
		if err := r.Update(ctx, cvoDeployment); err != nil {
			return false, fmt.Errorf("failed to scale down CVO deployment: %w", err)
		}
	}
	if cvoDeployment.Status.Replicas > 0 {
		log.Info("Waiting for CVO to scale down to 0")
		return false, nil
	}
	cvoScaledDownCond := meta.FindStatusCondition(hcp.Status.Conditions, string(hyperv1.CVOScaledDown))
	if cvoScaledDownCond == nil || cvoScaledDownCond.Status != metav1.ConditionTrue {
		cvoScaledDownCond = &metav1.Condition{
			Type:   string(hyperv1.CVOScaledDown),
			Status: metav1.ConditionTrue,
			Reason: "CVOScaledDown",
		}
		meta.SetStatusCondition(&hcp.Status.Conditions, *cvoScaledDownCond)
		if err := r.Status().Update(ctx, hcp); err != nil {
			return false, fmt.Errorf("failed to set CVO scaled down condition: %w", err)
		}
	}
	return false, nil
}
