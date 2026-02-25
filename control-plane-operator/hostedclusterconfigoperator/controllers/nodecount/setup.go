package nodecount

import (
	"context"
	"time"

	hypershiftclient "github.com/openshift/hypershift/client/clientset/clientset"
	"github.com/openshift/hypershift/control-plane-operator/hostedclusterconfigoperator/operator"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/client-go/util/workqueue"

	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
	karpenterv1 "sigs.k8s.io/karpenter/pkg/apis/v1"
)

const ControllerName = "nodecount"

func Setup(ctx context.Context, opts *operator.HostedClusterConfigOperatorConfig) error {
	hypershiftClient, err := hypershiftclient.NewForConfig(opts.CPCluster.GetConfig())
	if err != nil {
		return err
	}
	return ctrl.NewControllerManagedBy(opts.Manager).
		Named(ControllerName).
		For(&corev1.Node{}).
		Watches(&karpenterv1.NodeClaim{}, handler.EnqueueRequestsFromMapFunc(
			func(_ context.Context, _ client.Object) []reconcile.Request {
				return []reconcile.Request{{}}
			},
		)).
		WithOptions(controller.Options{
			RateLimiter: workqueue.NewTypedItemExponentialFailureRateLimiter[reconcile.Request](1*time.Second, 10*time.Second),
		}).Complete(&reconciler{
		hcpName:            opts.HCPName,
		hcpNamespace:       opts.Namespace,
		client:             hypershiftClient,
		lister:             opts.CPCluster.GetClient(),
		guestClusterClient: opts.Manager.GetClient(),
	})
}
