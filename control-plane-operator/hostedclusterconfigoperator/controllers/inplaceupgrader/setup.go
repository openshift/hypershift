package inplaceupgrader

import (
	"fmt"

	"github.com/openshift/hypershift/control-plane-operator/hostedclusterconfigoperator/operator"
	corev1 "k8s.io/api/core/v1"
	capiv1 "sigs.k8s.io/cluster-api/api/v1beta1"
	"sigs.k8s.io/controller-runtime/pkg/controller"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/source"
)

func Setup(opts *operator.HostedClusterConfigOperatorConfig) error {
	r := &Reconciler{
		client:                 opts.CPCluster.GetClient(),
		guestClusterClient:     opts.Manager.GetClient(),
		releaseProvider:        opts.ReleaseProvider,
		hcpName:                opts.HCPName,
		hcpNamespace:           opts.Namespace,
		CreateOrUpdateProvider: opts.TargetCreateOrUpdateProvider,
	}
	c, err := controller.New("inplaceupgrader", opts.Manager, controller.Options{Reconciler: r})
	if err != nil {
		return fmt.Errorf("failed to construct controller: %w", err)
	}

	if err := c.Watch(&source.Kind{Type: &corev1.Node{}}, handler.EnqueueRequestsFromMapFunc(r.nodeToMachineSet)); err != nil {
		return fmt.Errorf("failed to watch Nodes: %w", err)
	}

	if err := c.Watch(source.NewKindWithCache(&capiv1.MachineSet{}, opts.CPCluster.GetCache()), &handler.EnqueueRequestForObject{}); err != nil {
		return fmt.Errorf("failed to watch MachineSets: %w", err)
	}

	return nil
}
