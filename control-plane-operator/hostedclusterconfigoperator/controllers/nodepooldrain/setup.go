package nodepooldrain

import (
	"context"
	"fmt"
	"os"
	"time"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	"github.com/openshift/hypershift/control-plane-operator/hostedclusterconfigoperator/operator"
	"github.com/openshift/hypershift/support/pdbdrain"

	corev1 "k8s.io/api/core/v1"
	policyv1 "k8s.io/api/policy/v1"
	"k8s.io/apimachinery/pkg/types"

	crclient "sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/predicate"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
	"sigs.k8s.io/controller-runtime/pkg/source"
)

const ControllerName = "nodepooldrain"

func Setup(ctx context.Context, opts *operator.HostedClusterConfigOperatorConfig) error {
	r := &reconciler{
		mgtClusterClient:   opts.CPCluster.GetClient(),  // management cluster (HCP namespace)
		guestClient:        opts.Manager.GetClient(),    // guest, cached (nodes/PDBs are watched)
		guestAPIReader:     opts.Manager.GetAPIReader(), // guest, UNCACHED (pods: avoid a fleet-wide informer)
		recorder:           opts.Manager.GetEventRecorderFor(ControllerName),
		hcpNamespace:       opts.Namespace,
		hcpName:            opts.HCPName,
		platformNSPrefixes: pdbdrain.PlatformNamespacePrefixes(os.Getenv(pdbdrain.AdditionalPlatformNamespacePrefixesEnv)),
		// Seed lastSuccess so a transient guest error immediately after (re)start does not
		// trip the staleness grace window on the very first reconcile.
		lastSuccess:  time.Now(),
		lastBlocking: map[string]map[string]bool{},
	}
	c, err := controller.New(ControllerName, opts.Manager, controller.Options{Reconciler: r})
	if err != nil {
		return fmt.Errorf("failed to construct controller: %w", err)
	}

	toHCP := func(context.Context, crclient.Object) []reconcile.Request {
		return []reconcile.Request{{NamespacedName: types.NamespacedName{Namespace: opts.Namespace, Name: opts.HCPName}}}
	}

	// Only react to node changes that can affect node->pool mapping — not kubelet heartbeats.
	nodeRelevant := predicate.Funcs{
		CreateFunc:  func(event.CreateEvent) bool { return true },
		DeleteFunc:  func(event.DeleteEvent) bool { return true },
		GenericFunc: func(event.GenericEvent) bool { return false },
		UpdateFunc: func(e event.UpdateEvent) bool {
			return e.ObjectOld.GetLabels()[hyperv1.NodePoolLabel] != e.ObjectNew.GetLabels()[hyperv1.NodePoolLabel]
		},
	}
	if err := c.Watch(source.Kind[crclient.Object](opts.Manager.GetCache(), &corev1.Node{},
		handler.EnqueueRequestsFromMapFunc(toHCP), nodeRelevant)); err != nil {
		return fmt.Errorf("failed to watch guest Nodes: %w", err)
	}
	if err := c.Watch(source.Kind[crclient.Object](opts.Manager.GetCache(), &policyv1.PodDisruptionBudget{},
		handler.EnqueueRequestsFromMapFunc(toHCP))); err != nil {
		return fmt.Errorf("failed to watch guest PodDisruptionBudgets: %w", err)
	}
	return nil
}
