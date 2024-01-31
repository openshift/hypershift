package machine

import (
	"fmt"

	"github.com/openshift/hypershift/control-plane-operator/hostedclusterconfigoperator/operator"
	"github.com/openshift/hypershift/support/upsert"
	corev1 "k8s.io/api/core/v1"
	discoveryv1 "k8s.io/api/discovery/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/rest"
	capiv1 "sigs.k8s.io/cluster-api/api/v1beta1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/apiutil"
	"sigs.k8s.io/controller-runtime/pkg/controller"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/source"

	kubevirtv1 "kubevirt.io/api/core/v1"
)

type reconciler struct {
	client              client.Client
	kubevirtInfraClient client.Client
	hcpKey              client.ObjectKey
	upsert.CreateOrUpdateProvider
}

func Setup(opts *operator.HostedClusterConfigOperatorConfig) error {
	kubevirtScheme := runtime.NewScheme()
	corev1.AddToScheme(kubevirtScheme)
	kubevirtv1.AddToScheme(kubevirtScheme)
	discoveryv1.AddToScheme(kubevirtScheme)

	kubevirtHttpClient, err := rest.HTTPClientFor(opts.KubevirtInfraConfig)
	if err != nil {
		return fmt.Errorf("failed creating kubevirt cluster http client: %w", err)
	}

	kubevirtMapper, err := apiutil.NewDynamicRESTMapper(opts.KubevirtInfraConfig, kubevirtHttpClient)
	if err != nil {
		return fmt.Errorf("failed creating kubevirt cluster rest mapper: %w", err)
	}

	// if kubevirt infra config is not used, it is being set the same as the mgmt config
	kubevirtInfraClient, err := client.New(opts.KubevirtInfraConfig, client.Options{
		Scheme: kubevirtScheme,
		Mapper: kubevirtMapper,
		WarningHandler: client.WarningHandlerOptions{
			SuppressWarnings: true,
		},
	})
	if err != nil {
		return fmt.Errorf("failed to create kubevirt infra uncached client: %w", err)
	}
	r := &reconciler{
		client:              opts.CPCluster.GetClient(),
		kubevirtInfraClient: kubevirtInfraClient,
		hcpKey: client.ObjectKey{
			Namespace: opts.Namespace,
			Name:      opts.HCPName,
		},
		CreateOrUpdateProvider: opts.TargetCreateOrUpdateProvider,
	}
	c, err := controller.New("machine", opts.Manager, controller.Options{Reconciler: r})
	if err != nil {
		return fmt.Errorf("failed to construct controller: %w", err)
	}

	if err := c.Watch(source.Kind(opts.CPCluster.GetCache(), &capiv1.Machine{}), &handler.EnqueueRequestForObject{}); err != nil {
		return fmt.Errorf("failed to watch Machines: %w", err)
	}

	return nil
}
