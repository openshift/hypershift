package machineapprover

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/openshift/hypershift/support/config"
	component "github.com/openshift/hypershift/support/controlplane-component"
	"github.com/openshift/hypershift/support/omnicpo"
	"github.com/openshift/hypershift/support/podspec"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
)

func adaptDeployment(cpContext component.WorkloadContext, deployment *appsv1.Deployment) error {
	hcp := cpContext.HCP

	profile := hcp.Spec.Configuration.GetTLSSecurityProfile()
	tlsArgs, err := config.TLSArgs(profile)
	if err != nil {
		return err
	}
	// Render for main: machine-approver also takes --tls-curve-preferences (added at 5.1). OMNI-CPO
	// strips the TLS min-version/cipher args and/or the curve preferences on older operands (see
	// omniCPOTransforms).
	curveIDs, err := config.CurvePreferences(cpContext, profile)
	if err != nil {
		return err
	}
	if len(curveIDs) > 0 {
		curvePreferences := make([]string, 0, len(curveIDs))
		for _, curveID := range curveIDs {
			curvePreferences = append(curvePreferences, strconv.Itoa(int(curveID)))
		}
		tlsArgs = append(tlsArgs, fmt.Sprintf("--tls-curve-preferences=%s", strings.Join(curvePreferences, ",")))
	}

	podspec.UpdateContainer(ComponentName, deployment.Spec.Template.Spec.Containers, func(c *corev1.Container) {
		c.Args = append(c.Args, fmt.Sprintf("--machine-namespace=%s", hcp.Namespace))

		if len(tlsArgs) > 0 {
			c.Args = append(c.Args, tlsArgs...)
		}
	})

	return nil
}

// omniCPOTransforms lists the drift adjustments a main-built CPO applies to the machine-approver
// Deployment when it drives an older control plane. See support/omnicpo.
func omniCPOTransforms() []omnicpo.Transform[*appsv1.Deployment] {
	return []omnicpo.Transform[*appsv1.Deployment]{
		{
			// --tls-min-version/--tls-cipher-suites arrived at 4.23; a pre-4.23 machine-approver
			// rejects them.
			Reason: "pre-4.23 machine-approver rejects --tls-min-version/--tls-cipher-suites",
			When:   omnicpo.Below(config.Version423),
			Do:     omnicpo.StripArgs(ComponentName, "--tls-min-version", "--tls-cipher-suites"),
		},
		{
			// --tls-curve-preferences arrived at 5.1; a pre-5.1 machine-approver rejects it.
			Reason: "pre-5.1 machine-approver rejects --tls-curve-preferences",
			When:   omnicpo.Below(config.Version510),
			Do:     omnicpo.StripArgs(ComponentName, "--tls-curve-preferences"),
		},
	}
}
