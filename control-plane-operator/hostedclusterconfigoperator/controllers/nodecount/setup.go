package nodecount

import (
	"fmt"
	"time"

	hypershiftclient "github.com/openshift/hypershift/client/clientset/clientset"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/client-go/util/workqueue"
	ctrl "sigs.k8s.io/controller-runtime"

	"github.com/openshift/hypershift/control-plane-operator/hostedclusterconfigoperator/operator"
	"sigs.k8s.io/controller-runtime/pkg/controller"
)

const ControllerName = "nodecount"

func Setup(opts *operator.HostedClusterConfigOperatorConfig) error {
	hypershiftClient, err := hypershiftclient.NewForConfig(opts.CPCluster.GetConfig())
	if err != nil {
		return err
	}
	if _, err := ctrl.NewControllerManagedBy(opts.Manager).
		Named(ControllerName).
		For(&corev1.Node{}).
		WithOptions(controller.Options{
			RateLimiter: workqueue.NewItemExponentialFailureRateLimiter(1*time.Second, 10*time.Second),
		}).Build(&reconciler{
		hcpName:            opts.HCPName,
		hcpNamespace:       opts.Namespace,
		client:             hypershiftClient,
		lister:             opts.CPCluster.GetClient(),
		guestClusterClient: opts.Manager.GetClient(),
	}); err != nil {
		return fmt.Errorf("failed setting up with a controller manager %w", err)
	}

	return nil
}
