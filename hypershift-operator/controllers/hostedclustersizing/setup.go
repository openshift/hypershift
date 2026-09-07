package hostedclustersizing

import (
	"context"
	"fmt"
	"time"

	hypershiftv1beta1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	schedulingv1alpha1 "github.com/openshift/hypershift/api/scheduling/v1alpha1"
	hypershiftclient "github.com/openshift/hypershift/client/clientset/clientset"
	"github.com/openshift/hypershift/hypershift-operator/controllers/manifests"
	"github.com/openshift/hypershift/support/releaseinfo"
	hyperutil "github.com/openshift/hypershift/support/util"

	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/util/workqueue"
	"k8s.io/utils/ptr"

	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
)

const (
	ControllerName           = "hostedclustersizing"
	ValidatingControllerName = "hostedclustersizingvalidator"

	// hostedControlPlaneForHostedClusterIndex is the name of the index that maps between HostedClusters and
	// HostedControlPlanes, to allow quick lookup of a HostedCluster when we have a HostedControlPlane event
	hostedControlPlaneForHostedClusterIndex = "hostedControlPlane"

	// hostedClusterForNodePoolIndex is the name of the index that maps between NodePools and HostedClusters,
	// to allow for quick lookup of all NodePools for a HostedCluster event
	hostedClusterForNodePoolIndex = ".spec.clusterName"
)

func SetupWithManager(ctx context.Context, mgr ctrl.Manager, hypershiftOperatorImage string, releaseProvider *releaseinfo.ProviderWithOpenShiftImageRegistryOverridesDecorator, imageMetadataProvider *hyperutil.RegistryClientImageMetadataProvider) error {
	hypershiftClient, err := hypershiftclient.NewForConfig(mgr.GetConfig())
	if err != nil {
		return err
	}

	if _, err := hypershiftClient.SchedulingV1alpha1().ClusterSizingConfigurations().Get(ctx, "cluster", metav1.GetOptions{}); err != nil {
		if !errors.IsNotFound(err) {
			return fmt.Errorf("failed to get sizing configuration: %w", err)
		}
		if _, err := hypershiftClient.SchedulingV1alpha1().ClusterSizingConfigurations().Create(ctx, DefaultSizingConfig(), metav1.CreateOptions{}); err != nil {
			return fmt.Errorf("failed to create sizing configuration: %w", err)
		}
	}

	if err := mgr.GetFieldIndexer().IndexField(ctx, &hypershiftv1beta1.HostedCluster{}, hostedControlPlaneForHostedClusterIndex, func(object client.Object) []string {
		return []string{types.NamespacedName{
			Namespace: manifests.HostedControlPlaneNamespace(object.GetNamespace(), object.GetName()),
			Name:      object.GetName(),
		}.String()}
	}); err != nil {
		return fmt.Errorf("could not set up hosted cluster -> hosted control plane indexer: %w", err)
	}

	if err := mgr.GetFieldIndexer().IndexField(ctx, &hypershiftv1beta1.NodePool{}, hostedClusterForNodePoolIndex, func(object client.Object) []string {
		nodePool, ok := object.(*hypershiftv1beta1.NodePool)
		if !ok {
			mgr.GetLogger().Error(fmt.Errorf("expected NodePool, got %T", object), "could not cast object to NodePool")
			return nil
		}
		return []string{types.NamespacedName{
			Namespace: nodePool.Namespace,
			Name:      nodePool.Spec.ClusterName,
		}.String()}
	}); err != nil {
		return fmt.Errorf("could not set up node pool -> hosted cluster indexer: %w", err)
	}

	if err := ctrl.NewControllerManagedBy(mgr).
		Named(ControllerName).
		For(&hypershiftv1beta1.HostedCluster{}).
		Watches(&schedulingv1alpha1.ClusterSizingConfiguration{}, handler.EnqueueRequestsFromMapFunc(func(ctx context.Context, _ client.Object) []reconcile.Request {
			// when the sizing configuration changes, we need to re-process every HostedCluster
			hostedClusters := hypershiftv1beta1.HostedClusterList{}
			if err := mgr.GetClient().List(ctx, &hostedClusters); err != nil {
				mgr.GetLogger().Error(err, "failed to list hosted clusters when enqueuing for sizing configuration change")
				return nil
			}
			var out []reconcile.Request
			for _, hc := range hostedClusters.Items {
				out = append(out, reconcile.Request{NamespacedName: types.NamespacedName{Namespace: hc.Namespace, Name: hc.Name}})
			}
			return out
		})).
		Watches(&hypershiftv1beta1.NodePool{}, handler.EnqueueRequestsFromMapFunc(func(ctx context.Context, object client.Object) []reconcile.Request {
			// when a NodePool changes, queue the HostedCluster for it
			nodePool, ok := object.(*hypershiftv1beta1.NodePool)
			if !ok {
				mgr.GetLogger().Error(fmt.Errorf("expected NodePool, got %T", object), "could not cast object to NodePool")
				return nil
			}
			return []reconcile.Request{{
				NamespacedName: types.NamespacedName{
					Namespace: nodePool.Namespace,
					Name:      nodePool.Spec.ClusterName,
				},
			}}
		})).
		Watches(&hypershiftv1beta1.HostedControlPlane{}, handler.EnqueueRequestsFromMapFunc(func(ctx context.Context, object client.Object) []reconcile.Request {
			// when a HostedControlPlane changes, queue the HostedCluster for it
			// n.b. controller-runtime implements indexers as field selectors on the client
			hostedClusters := hypershiftv1beta1.HostedClusterList{}
			if err := mgr.GetClient().List(ctx, &hostedClusters, client.MatchingFields{hostedControlPlaneForHostedClusterIndex: client.ObjectKeyFromObject(object).String()}); err != nil {
				mgr.GetLogger().Error(err, "failed to list hosted clusters when enqueuing for hosted control plane")
				return nil
			}
			if len(hostedClusters.Items) != 1 {
				mgr.GetLogger().Error(fmt.Errorf("expected one HostedCluster, got %d", len(hostedClusters.Items)), "failed to look up the hosted cluster for the hosted control plane")
				return nil
			}

			return []reconcile.Request{{
				NamespacedName: client.ObjectKeyFromObject(&hostedClusters.Items[0]),
			}}
		})).
		WithOptions(controller.Options{
			RateLimiter: workqueue.NewTypedItemExponentialFailureRateLimiter[reconcile.Request](1*time.Second, 10*time.Second),
		}).Complete(newReconciler(
		hypershiftClient, mgr.GetClient(), time.Now,
		hypershiftOperatorImage, releaseProvider, imageMetadataProvider,
	)); err != nil {
		return fmt.Errorf("failed to set up %s controller: %w", ControllerName, err)
	}

	if err := ctrl.NewControllerManagedBy(mgr).
		Named(ValidatingControllerName).
		For(&schedulingv1alpha1.ClusterSizingConfiguration{}).
		WithOptions(controller.Options{
			RateLimiter: workqueue.NewTypedItemExponentialFailureRateLimiter[reconcile.Request](1*time.Second, 10*time.Second),
		}).Complete(&validator{
		client: hypershiftClient,
		lister: mgr.GetClient(),
	}); err != nil {
		return fmt.Errorf("failed to set up %s controller: %w", ValidatingControllerName, err)
	}

	return nil
}

// SizeSmall, SizeMedium and SizeLarge are the names of the t-shirt sizes in the
// default ClusterSizingConfiguration.
const (
	SizeSmall  = "small"
	SizeMedium = "medium"
	SizeLarge  = "large"
)

// baselineRequest is a published baseline resource request for a single control
// plane container at a single t-shirt size.
type baselineRequest struct {
	deployment string
	container  string
	cpu        string
	memory     string
}

// baselineResourceRequests holds the published baseline control plane resource
// requests per t-shirt size. This is the single place to adjust sizing defaults:
// entries here are translated into ClusterSizingConfiguration size effects, which
// the sizing controller turns into resource-request-override annotations on each
// HostedCluster. That means the values below can be re-tuned per release without
// touching the reconciliation logic, and management cluster administrators can
// override them at runtime by editing the ClusterSizingConfiguration directly.
//
// The "small" entries intentionally mirror the requests baked into the CPO asset
// manifests under control-plane-operator/controllers/hostedcontrolplane/v2/assets/,
// so that a small cluster is sized identically whether or not size tagging is
// enabled. TestBaselineResourceRequestsMatchAssets enforces that invariant.
//
// See docs/content/how-to/cluster-sizing-guidance.md for the rationale behind
// these numbers and for guidance on tuning them.
var baselineResourceRequests = map[string][]baselineRequest{
	SizeSmall: {
		{deployment: "kube-apiserver", container: "kube-apiserver", cpu: "500m", memory: "3Gi"},
		{deployment: "etcd", container: "etcd", cpu: "300m", memory: "1Gi"},
		{deployment: "openshift-apiserver", container: "openshift-apiserver", cpu: "250m", memory: "500Mi"},
	},
	SizeMedium: {
		{deployment: "kube-apiserver", container: "kube-apiserver", cpu: "2", memory: "8Gi"},
		{deployment: "etcd", container: "etcd", cpu: "1", memory: "4Gi"},
		{deployment: "openshift-apiserver", container: "openshift-apiserver", cpu: "500m", memory: "1Gi"},
	},
	SizeLarge: {
		{deployment: "kube-apiserver", container: "kube-apiserver", cpu: "4", memory: "16Gi"},
		{deployment: "etcd", container: "etcd", cpu: "2", memory: "8Gi"},
		{deployment: "openshift-apiserver", container: "openshift-apiserver", cpu: "1", memory: "2Gi"},
	},
}

// resourceRequestsForSize renders the baseline requests for a t-shirt size into
// the API representation used by ClusterSizingConfiguration size effects.
func resourceRequestsForSize(size string) []schedulingv1alpha1.ResourceRequest {
	baselines := baselineResourceRequests[size]
	requests := make([]schedulingv1alpha1.ResourceRequest, 0, len(baselines))
	for _, baseline := range baselines {
		requests = append(requests, schedulingv1alpha1.ResourceRequest{
			DeploymentName: baseline.deployment,
			ContainerName:  baseline.container,
			CPU:            ptr.To(resource.MustParse(baseline.cpu)),
			Memory:         ptr.To(resource.MustParse(baseline.memory)),
		})
	}
	return requests
}

func DefaultSizingConfig() *schedulingv1alpha1.ClusterSizingConfiguration {
	return &schedulingv1alpha1.ClusterSizingConfiguration{
		ObjectMeta: metav1.ObjectMeta{
			Name: "cluster",
		},
		Spec: schedulingv1alpha1.ClusterSizingConfigurationSpec{
			Sizes: []schedulingv1alpha1.SizeConfiguration{
				{
					Name: SizeSmall,
					Criteria: schedulingv1alpha1.NodeCountCriteria{
						From: 0,
						To:   ptr.To(uint32(10)),
					},
					Effects: &schedulingv1alpha1.Effects{
						ResourceRequests: resourceRequestsForSize(SizeSmall),
					},
				},
				{
					Name: SizeMedium,
					Criteria: schedulingv1alpha1.NodeCountCriteria{
						From: 11,
						To:   ptr.To(uint32(100)),
					},
					Effects: &schedulingv1alpha1.Effects{
						ResourceRequests: resourceRequestsForSize(SizeMedium),
					},
				},
				{
					Name: SizeLarge,
					Criteria: schedulingv1alpha1.NodeCountCriteria{
						From: 101,
					},
					Effects: &schedulingv1alpha1.Effects{
						ResourceRequests: resourceRequestsForSize(SizeLarge),
					},
				},
			},
		},
	}
}
