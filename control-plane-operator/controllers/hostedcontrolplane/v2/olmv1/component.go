package olmv1

import (
	"github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/v2/olmv1/catalogd"
	clusterolmoperator "github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/v2/olmv1/clusterolmoperator"
	"github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/v2/olmv1/consoleoperator"
	operatorcontroller "github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/v2/olmv1/operatorcontroller"
	component "github.com/openshift/hypershift/support/controlplane-component"
)

func NewComponents(capabilityImageStream bool) []component.ControlPlaneComponent {
	components := []component.ControlPlaneComponent{
		catalogd.NewComponent(),
		operatorcontroller.NewComponent(),
		consoleoperator.NewComponent(),
		clusterolmoperator.NewComponent(),
	}

	return components
}
