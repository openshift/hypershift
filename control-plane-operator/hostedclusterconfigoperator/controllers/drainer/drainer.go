package drainer

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/openshift/hypershift/support/upsert"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	kubeclient "k8s.io/client-go/kubernetes"
	"k8s.io/kubectl/pkg/drain"

	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
)

const (
	// DesiredDrainerAnnotationKey is set by the MCD to indicate drain/uncordon requests
	DesiredDrainerAnnotationKey = "machineconfiguration.openshift.io/desiredDrain"
	// LastAppliedDrainerAnnotationKey is set by the controller to indicate the last request applied
	LastAppliedDrainerAnnotationKey = "machineconfiguration.openshift.io/lastAppliedDrain"
	// DrainerStateUncordon is used for drainer annotation as a value to indicate needing an uncordon
	DrainerStateUncordon = "uncordon"
	// DrainerStateDrain = "drain"
	DrainerStateDrain = "drain"
	// drainFailureRequeueDuration is the delay until we retry draining the node, such that other nodes
	// get a chance in the queue
	drainFailureRequeueDuration = 1 * time.Minute
)

type Reconciler struct {
	client             client.Client
	guestClusterClient client.Client
	// This additional KubeClient is used for the drainer call from client-go
	// TODO (jerzhang): this may be redundant
	guestClusterKubeClient kubeclient.Interface
	upsert.CreateOrUpdateProvider
}

func (r *Reconciler) Reconcile(ctx context.Context, req reconcile.Request) (reconcile.Result, error) {
	log := ctrl.LoggerFrom(ctx)
	log.Info("Reconciling")

	node := &corev1.Node{}
	err := r.guestClusterClient.Get(ctx, req.NamespacedName, node)
	if err != nil {
		if apierrors.IsNotFound(err) {
			log.Info("not found", "request", req.String())
			return ctrl.Result{}, nil
		}
		log.Error(err, "error getting node")
		return ctrl.Result{}, err
	}

	if node.Annotations[DesiredDrainerAnnotationKey] != node.Annotations[LastAppliedDrainerAnnotationKey] {
		if err = r.handleNodeDrainRequest(ctx, node, node.Annotations[DesiredDrainerAnnotationKey]); err != nil {
			// TODO (jerzhang): properly handle failure and error detection
			// Right now for individual failures we will endlessly retry
			// Instead we should have some timeout (based on NodeDrainTimeout?)
			// and error reporting to the nodepool status
			log.Error(err, "failed to drain node")
			return ctrl.Result{RequeueAfter: drainFailureRequeueDuration}, err
		}
	}

	return ctrl.Result{}, nil
}

func (r *Reconciler) handleNodeDrainRequest(ctx context.Context, node *corev1.Node, desiredState string) error {
	log := ctrl.LoggerFrom(ctx)

	// TODO (jerzhang): this name is deterministic, but should make this a const somewhere
	daemonPodOnNodeName := fmt.Sprintf("machine-config-daemon-%s", node.Name)
	drainer := &drain.Helper{
		Client:              r.guestClusterKubeClient,
		Force:               true,
		IgnoreAllDaemonSets: true,
		DeleteEmptyDirData:  true,
		GracePeriodSeconds:  -1,
		Timeout:             90 * time.Second,
		OnPodDeletedOrEvicted: func(pod *corev1.Pod, usingEviction bool) {
			verbStr := "Deleted"
			if usingEviction {
				verbStr = "Evicted"
			}
			log.Info("Pod drained", "Action", verbStr, "Namespace", pod.Namespace, "Pod name", pod.Name)
		},
		AdditionalFilters: []drain.PodFilter{
			func(pod corev1.Pod) drain.PodDeleteStatus {
				if pod.Name == daemonPodOnNodeName {
					return drain.MakePodDeleteStatusSkip()
				}
				return drain.MakePodDeleteStatusOkay()
			},
		},
		// TODO (jerzhang): properly handle logging here, although this seems to work
		Out:    writer{log.Info},
		ErrOut: writer{log.Info},
		Ctx:    ctx,
	}

	if isNodeUnreachable(node) {
		// When the node is unreachable and some pods are not evicted for as long as this timeout, we ignore them.
		// This is copied from cluster-api to match the drain behavior there for machines
		drainer.SkipWaitForDeleteTimeoutSeconds = 60 * 5 // 5 minutes
	}

	desiredVerb := strings.Split(desiredState, "-")[0]
	switch desiredVerb {
	case DrainerStateUncordon:
		log.Info("Performing uncordon on node", "name", node.Name)
		if err := drain.RunCordonOrUncordon(drainer, node, false); err != nil {
			return fmt.Errorf("uncordon failed: %w", err)
		}
	case DrainerStateDrain:
		log.Info("Performing drain on node", "name", node.Name)
		if err := r.applyDrain(ctx, drainer, node); err != nil {
			return fmt.Errorf("drain failed: %w", err)
		}
	default:
		return fmt.Errorf("unknown drain verb in drain request %s: %s", desiredState, desiredVerb)
	}

	// The drain or uncordon has completed.
	if result, err := r.CreateOrUpdate(ctx, r.guestClusterClient, node, func() error {
		if node.Annotations == nil {
			node.Annotations = map[string]string{}
		}
		node.Annotations[LastAppliedDrainerAnnotationKey] = node.Annotations[DesiredDrainerAnnotationKey]
		return nil
	}); err != nil {
		return fmt.Errorf("failed to reconcile node drain annotations: %w", err)
	} else {
		log.Info("Reconciled Node drain annotations", "result", result)
	}
	return nil
}

func (r *Reconciler) applyDrain(ctx context.Context, drainer *drain.Helper, node *corev1.Node) error {
	log := ctrl.LoggerFrom(ctx)
	if err := drain.RunCordonOrUncordon(drainer, node, true); err != nil {
		return fmt.Errorf("cordon failed: %w", err)
	}
	if err := drain.RunNodeDrain(drainer, node.Name); err != nil {
		return fmt.Errorf("failed to drain node %s: %w", node.Name, err)
	}
	log.Info("Drain succeeded on node", "name", node.Name)
	return nil

}

// writer implements io.Writer interface as a pass-through for log
type writer struct {
	logFunc func(arg string, args ...interface{})
}

// Write passes string(p) into writer's logFunc and always returns len(p)
func (w writer) Write(p []byte) (n int, err error) {
	w.logFunc(string(p))
	return len(p), nil
}

// IsNodeUnreachable returns true if a node is unreachable.
// Node is considered unreachable when its ready status is "Unknown".
// Copied from cluster-api to reduce dependencies
func isNodeUnreachable(node *corev1.Node) bool {
	if node == nil {
		return false
	}
	for _, c := range node.Status.Conditions {
		if c.Type == corev1.NodeReady {
			return c.Status == corev1.ConditionUnknown
		}
	}
	return false
}
