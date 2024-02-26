package scheduler

import (
	"context"
	"fmt"
	"time"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	"github.com/openshift/hypershift/support/upsert"
	"github.com/openshift/hypershift/support/util"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/util/workqueue"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller"
	"sigs.k8s.io/controller-runtime/pkg/predicate"
)

const (
	ControlPlaneTaint                              = "hypershift.openshift.io/control-plane"
	ControlPlaneServingComponentTaint              = "hypershift.openshift.io/control-plane-serving-component"
	HostedClusterTaint                             = "hypershift.openshift.io/cluster"
	ControlPlaneServingComponentAvailableNodeTaint = "hypershift.openshift.io/control-plane-serving-component-available"

	ControlPlaneServingComponentLabel = "hypershift.openshift.io/control-plane-serving-component"
	OSDFleetManagerPairedNodesLabel   = "osd-fleet-manager.openshift.io/paired-nodes"
	HostedClusterNameLabel            = "hypershift.openshift.io/cluster-name"
	HostedClusterNamespaceLabel       = "hypershift.openshift.io/cluster-namespace"
)

type DedicatedServingComponentNodeReaper struct {
	client.Client
}

func (r *DedicatedServingComponentNodeReaper) SetupWithManager(mgr ctrl.Manager) error {
	servingComponentPredicate, err := predicate.LabelSelectorPredicate(metav1.LabelSelector{MatchLabels: map[string]string{hyperv1.RequestServingComponentLabel: "true"}})
	if err != nil {
		return err
	}
	builder := ctrl.NewControllerManagedBy(mgr).
		For(&corev1.Node{}).WithEventFilter(servingComponentPredicate).Named("DedicatedServingComponentNodeReaper")
	return builder.Complete(r)
}

func (r *DedicatedServingComponentNodeReaper) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	log := ctrl.LoggerFrom(ctx, "node", req.Name)
	node := &corev1.Node{}
	if err := r.Get(ctx, req.NamespacedName, node); err != nil {
		if apierrors.IsNotFound(err) {
			log.Info("node not found, aborting reconcile", "name", req.NamespacedName.String())
			return ctrl.Result{}, nil
		}
		return ctrl.Result{}, fmt.Errorf("failed to get node %q: %w", req.NamespacedName.String(), err)
	}
	log.Info("Reconciling node")

	if _, hasServingComponentLabel := node.Labels[hyperv1.RequestServingComponentLabel]; !hasServingComponentLabel {
		log.Info("Skipping node because it doesn't have the control plane serving component label")
		return ctrl.Result{}, nil
	}

	if _, hasHostedClusterLabel := node.Labels[hyperv1.HostedClusterLabel]; !hasHostedClusterLabel {
		log.Info("Skipping node because it has not been allocated to a hosted cluster")
		return ctrl.Result{}, nil
	}

	log.Info("Node has been allocated to a hosted cluster, checking whether hosted cluster still exists.")
	name := node.Labels[HostedClusterNameLabel]
	namespace := node.Labels[HostedClusterNamespaceLabel]
	hc := &hyperv1.HostedCluster{}
	if err := r.Get(ctx, client.ObjectKey{Namespace: namespace, Name: name}, hc); err != nil {
		if !apierrors.IsNotFound(err) {
			return ctrl.Result{}, fmt.Errorf("failed to get hosted cluster %s/%s: %w", namespace, name, err)
		}
		log.Info("The hosted cluster is not found. Deleting node.")
		if err := r.Delete(ctx, node); err != nil {
			return ctrl.Result{}, fmt.Errorf("failed to delete node: %w", err)
		}
		log.Info("Node deleted")
	} else {
		log.Info("The hosted cluster exists, will check again later.")
		return ctrl.Result{RequeueAfter: 1 * time.Minute}, nil
	}
	return ctrl.Result{}, nil
}

type DedicatedServingComponentScheduler struct {
	client.Client
	createOrUpdate upsert.CreateOrUpdateFN
}

func (r *DedicatedServingComponentScheduler) SetupWithManager(mgr ctrl.Manager, createOrUpdateProvider upsert.CreateOrUpdateProvider) error {

	r.createOrUpdate = createOrUpdateProvider.CreateOrUpdate
	builder := ctrl.NewControllerManagedBy(mgr).
		For(&hyperv1.HostedCluster{}, builder.WithPredicates(util.PredicatesForHostedClusterAnnotationScoping())).
		WithOptions(controller.Options{
			RateLimiter:             workqueue.NewItemExponentialFailureRateLimiter(1*time.Second, 10*time.Second),
			MaxConcurrentReconciles: 10,
		}).Named("DedicatedServingComponentScheduler")
	return builder.Complete(r)
}

func (r *DedicatedServingComponentScheduler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	hcluster := &hyperv1.HostedCluster{}
	log := ctrl.LoggerFrom(ctx, "hostedcluster", req.NamespacedName.String())
	err := r.Get(ctx, req.NamespacedName, hcluster)
	if err != nil {
		if apierrors.IsNotFound(err) {
			log.Info("hostedcluster not found, aborting reconcile", "name", req.NamespacedName)
			return ctrl.Result{}, nil
		}
		return ctrl.Result{}, fmt.Errorf("failed to get cluster %q: %w", req.NamespacedName, err)
	}
	if !hcluster.DeletionTimestamp.IsZero() {
		log.Info("hostedcluster is deleted, nothing to do")
		return ctrl.Result{}, nil
	}
	if hcTopology := hcluster.Annotations[hyperv1.TopologyAnnotation]; hcTopology != hyperv1.DedicatedRequestServingComponentsTopology {
		log.Info("hostedcluster does not use isolated request serving components, nothing to do")
		return ctrl.Result{}, nil
	}

	// Find existing dedicated serving content Nodes for this HC.
	dedicatedNodesForHC := &corev1.NodeList{}
	if err := r.List(ctx, dedicatedNodesForHC,
		client.HasLabels{hyperv1.RequestServingComponentLabel},
		client.MatchingLabels{
			hyperv1.HostedClusterLabel: fmt.Sprintf("%s-%s", hcluster.Namespace, hcluster.Name),
		}); err != nil {
		return ctrl.Result{}, fmt.Errorf("failed to list nodes: %w", err)
	}
	if len(dedicatedNodesForHC.Items) > 2 {
		return ctrl.Result{}, fmt.Errorf("found too many dedicated nodes for HC: %v", len(dedicatedNodesForHC.Items))
	}

	// We check existing dedicated Nodes are 2. If not e.g. some was deleted, continue.
	if scheduled := hcluster.Annotations[hyperv1.HostedClusterScheduledAnnotation]; scheduled == "true" && len(dedicatedNodesForHC.Items) == 2 {
		log.Info("hosted cluster is already scheduled, nothing to do")
		return ctrl.Result{}, nil
	}

	nodeList := &corev1.NodeList{}
	if err := r.List(ctx, nodeList, client.HasLabels{hyperv1.RequestServingComponentLabel}); err != nil {
		return ctrl.Result{}, fmt.Errorf("failed to list nodes: %w", err)
	}

	nodesToUse := map[string]*corev1.Node{}
	// first, find any existing nodes already labeled for this hostedcluster
	for i := range nodeList.Items {
		node := &nodeList.Items[i]
		zone, hasZoneLabel := node.Labels["topology.kubernetes.io/zone"]
		if !hasZoneLabel {
			continue
		}
		hcLabel, hasHCLabel := node.Labels[hyperv1.HostedClusterLabel]
		if !hasHCLabel {
			continue
		}
		if hcLabel == fmt.Sprintf("%s-%s", hcluster.Namespace, hcluster.Name) {
			nodesToUse[zone] = node
			log.Info("Found existing node for hosted cluster", "node", node.Name, "zone", zone)
		}
	}

	if len(nodesToUse) < 2 {
		for i := range nodeList.Items {
			node := &nodeList.Items[i]
			zone, hasZoneLabel := node.Labels["topology.kubernetes.io/zone"]
			if !hasZoneLabel {
				// No zone has been set on the node, we cannot use it
				continue
			}

			_, hasHCLabel := node.Labels[hyperv1.HostedClusterLabel]
			if hasHCLabel {
				// The node has been allocated to a different hosted cluster, skip it
				continue
			}

			if nodesToUse[zone] == nil {

				// if the candidate Node is not paired with the existing node to use then skip.
				paired := false
				if len(nodesToUse) > 0 {
					for _, n := range nodesToUse {
						if n.Labels[OSDFleetManagerPairedNodesLabel] == node.Labels[OSDFleetManagerPairedNodesLabel] {
							paired = true
						}
					}
					if !paired {
						continue
					}
				}

				log.Info("Found node to allocate for hosted cluster", "node", node.Name, "zone", zone)
				nodesToUse[zone] = node
			}

			if len(nodesToUse) == 2 {
				break
			}
		}
	}
	if len(nodesToUse) < 2 {
		return ctrl.Result{}, fmt.Errorf("failed to find enough available nodes for cluster, found %d", len(nodesToUse))
	}
	for _, node := range nodesToUse {
		originalNode := node.DeepCopy()

		// Add taint and labels for specific hosted cluster
		hasTaint := false
		hcNameValue := fmt.Sprintf("%s-%s", hcluster.Namespace, hcluster.Name)
		for i := range node.Spec.Taints {
			if node.Spec.Taints[i].Key == HostedClusterTaint {
				node.Spec.Taints[i].Value = hcNameValue
				node.Spec.Taints[i].Effect = corev1.TaintEffectNoSchedule
				hasTaint = true
				break
			}
		}
		if !hasTaint {
			node.Spec.Taints = append(node.Spec.Taints, corev1.Taint{
				Key:    HostedClusterTaint,
				Value:  hcNameValue,
				Effect: corev1.TaintEffectNoSchedule,
			})
		}
		node.Labels[hyperv1.HostedClusterLabel] = hcNameValue
		node.Labels[HostedClusterNameLabel] = hcluster.Name
		node.Labels[HostedClusterNamespaceLabel] = hcluster.Namespace

		if err := r.Patch(ctx, node, client.MergeFrom(originalNode)); err != nil {
			return ctrl.Result{}, fmt.Errorf("failed to update labels and taints on node %s: %w", node.Name, err)
		}
		log.Info("Node tainted and labeled for hosted cluster", "node", node.Name)
	}

	// finally update HostedCluster with new annotation
	log.Info("Setting scheduled annotation on hosted cluster")
	originalHcluster := hcluster.DeepCopy()
	hcluster.Annotations[hyperv1.HostedClusterScheduledAnnotation] = "true"
	if err := r.Patch(ctx, hcluster, client.MergeFrom(originalHcluster)); err != nil {
		return ctrl.Result{}, fmt.Errorf("failed to update hostedcluster annotation: %w", err)
	}

	return ctrl.Result{}, nil
}
