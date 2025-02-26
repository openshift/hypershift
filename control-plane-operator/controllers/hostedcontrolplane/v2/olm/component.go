package olm

import (
	catalogoperator "github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/v2/olm/catalog_operator"
	"github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/v2/olm/catalogs"
	collectprofiles "github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/v2/olm/collect_profiles"
	olmoperator "github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/v2/olm/olm_operator"
	"github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/v2/olm/packageserver"
	component "github.com/openshift/hypershift/support/controlplane-component"
)

func NewComponents(capabilityImageStream bool) []component.ControlPlaneComponent {
	components := []component.ControlPlaneComponent{
		catalogoperator.NewComponent(),
		olmoperator.NewComponent(),
		packageserver.NewComponent(),
		collectprofiles.NewComponent(),
	}
	components = append(components, catalogs.NewCatalogComponents(capabilityImageStream)...)

	return components
}
