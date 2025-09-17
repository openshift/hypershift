package omoperator

import (
	"context"
	"fmt"

	"github.com/spf13/cobra"
	"go.uber.org/zap/zapcore"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/dynamic"

	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client/config"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"

	hypershiftv1beta1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	hyperclient "github.com/openshift/hypershift/client/clientset/clientset"

	"github.com/openshift/multi-operator-manager/pkg/library/libraryinputresources"
)

func NewCommand() *cobra.Command {
	operator := newOpenshiftManagerOperator()
	cmd := &cobra.Command{
		Use:   "om",
		Short: "Runs the OpenshiftManager Operator",
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := operator.Validate(); err != nil {
				return err
			}
			return operator.Run(ctrl.SetupSignalHandler())
		},
	}

	flags := cmd.Flags()
	flags.StringVar(&operator.Namespace, "namespace", operator.Namespace, "the namespace for control plane components on management cluster")
	flags.StringVar(&operator.HostedControlPlaneName, "hosted-control-plane", operator.HostedControlPlaneName, "Name of the hosted control plane that owns this operator")

	cmd.AddCommand(NewTransformDeploymentCommand())

	return cmd
}

func newOpenshiftManagerOperator() *OpenshiftManagerOperator {
	return &OpenshiftManagerOperator{}
}

type OpenshiftManagerOperator struct {
	// Namespace is the namespace on the management cluster where the control plane components run.
	Namespace string

	// HostedControlPlaneName is the name of the hosted control plane that owns this operator instance.
	HostedControlPlaneName string
}

func (o *OpenshiftManagerOperator) Validate() error {
	if len(o.Namespace) == 0 {
		return fmt.Errorf("the namespace for control plane components is required")
	}
	if len(o.HostedControlPlaneName) == 0 {
		return fmt.Errorf("the hosted control plane components is required")
	}
	return nil
}

func (o *OpenshiftManagerOperator) Run(ctx context.Context) error {
	ctrl.SetLogger(zap.New(zap.JSONEncoder(func(o *zapcore.EncoderConfig) {
		o.EncodeTime = zapcore.RFC3339TimeEncoder
	})))
	ctrl.Log.Info("Starting Openshift Manager Operator")

	cfg, err := config.GetConfig()
	if err != nil {
		return err
	}

	hcpClient, err := hyperclient.NewForConfig(cfg)
	if err != nil {
		return err
	}
	hostedControlPlane, err := hcpClient.HypershiftV1beta1().HostedControlPlanes(o.Namespace).Get(ctx, o.HostedControlPlaneName, metav1.GetOptions{})
	if err != nil {
		return err
	}

	// HCP uses controller-runtime but for experimentation
	// we are going to use the dynamic client
	client, err := dynamic.NewForConfig(cfg)
	if err != nil {
		return nil
	}

	return o.runInternal(ctx, client, hostedControlPlane)
}

func (o *OpenshiftManagerOperator) runInternal(ctx context.Context, client *dynamic.DynamicClient, hostedControlPlane *hypershiftv1beta1.HostedControlPlane) error {
	return nil
}

// normally, we would get that list by running the input-res command of the auth-operator.
// for now, just return a static list required to run controllers that manage the oauth-server.
func getStaticInputResourcesForAuthOperator() (*libraryinputresources.InputResources, error) {
	return &libraryinputresources.InputResources{
		ApplyConfigurationResources: libraryinputresources.ResourceList{
			ExactResources: []libraryinputresources.ExactResourceID{},
		},
	}, nil
}
