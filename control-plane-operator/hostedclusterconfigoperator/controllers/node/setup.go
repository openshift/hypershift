package node

import (
	"fmt"

	"github.com/openshift/hypershift/control-plane-operator/hostedclusterconfigoperator/operator"
	corev1 "k8s.io/api/core/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/source"
)

func Setup(opts *operator.HostedClusterConfigOperatorConfig) error {
	// if kubevirt infra config is not used, it is being set the same as the mgmt config
	kubevirtInfraClient, err := client.New(opts.KubevirtInfraConfig, client.Options{
		Scheme: opts.Manager.GetScheme(),
		Mapper: opts.Manager.GetRESTMapper(),
		WarningHandler: client.WarningHandlerOptions{
			SuppressWarnings: true,
		},
	})
	if err != nil {
		return fmt.Errorf("failed to create kubevirt infra uncached client: %w", err)
	}
	r := &reconciler{
		client:                 opts.CPCluster.GetClient(),
		guestClusterClient:     opts.Manager.GetClient(),
		kubevirtInfraClient:    kubevirtInfraClient,
		hcpName:                opts.HCPName,
		hcpNamespace:           opts.Namespace,
		CreateOrUpdateProvider: opts.TargetCreateOrUpdateProvider,
	}
	c, err := controller.New("node", opts.Manager, controller.Options{Reconciler: r})
	if err != nil {
		return fmt.Errorf("failed to construct controller: %w", err)
	}

	if err := c.Watch(source.Kind(opts.Manager.GetCache(), &corev1.Node{}), &handler.EnqueueRequestForObject{}); err != nil {
		return fmt.Errorf("failed to watch Nodes: %w", err)
	}

	return nil
}
