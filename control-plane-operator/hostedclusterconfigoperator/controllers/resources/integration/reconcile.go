package integration

import (
	"context"
	"fmt"

	utilerrors "k8s.io/apimachinery/pkg/util/errors"
)

// Stage selects which projections run; each stage is invoked at its existing root phase position.
type Stage int

const (
	// APIServiceProjections projects OpenShift and OAuth API server APIServices, Services, Endpoints
	// and the kubeadmin password hash. The root runs it after the connection conditions.
	APIServiceProjections Stage = iota
	// OAuthProjections projects the OAuth serving-cert CA bundle, OAuth clients and serving-cert RBAC.
	// The root runs it after the proxy CA bundle.
	OAuthProjections
)

// Reconcile runs one projection stage and returns a single aggregate of the root-visible errors,
// or nil when every projection succeeds. The aggregate is returned unwrapped so the root can
// flatten its entries into the root aggregate without losing context.
func Reconcile(ctx context.Context, hosted HostedCluster, controlPlane ControlPlane, params Params, stage Stage) error {
	var errs []error
	switch stage {
	case APIServiceProjections:
		errs = reconcileAPIServiceProjections(ctx, hosted, controlPlane, params)
	case OAuthProjections:
		errs = reconcileOAuthProjections(ctx, hosted, controlPlane, params)
	default:
		return fmt.Errorf("unknown integration reconciliation stage: %d", stage)
	}
	return utilerrors.NewAggregate(errs)
}
