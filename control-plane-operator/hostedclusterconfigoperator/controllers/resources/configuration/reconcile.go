package configuration

import (
	"context"
	"fmt"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	"github.com/openshift/hypershift/support/upsert"

	configv1 "github.com/openshift/api/config/v1"

	utilerrors "k8s.io/apimachinery/pkg/util/errors"

	"sigs.k8s.io/controller-runtime/pkg/client"
)

type ReconcileParams struct {
	ClusterID     string
	UpdateService configv1.URL
	Channel       string
	Capabilities  *hyperv1.Capabilities
	Versions      map[string]string
}
type Stage int

const (
	CRDs Stage = iota
	ClusterVersion
	ClusterOperators
	Namespaces
)

func Reconcile(ctx context.Context, hostedClusterClient client.Client, createOrUpdate upsert.CreateOrUpdateFN, params ReconcileParams, stage Stage) error {
	switch stage {
	case CRDs:
		return reconcileCRDs(ctx, hostedClusterClient, createOrUpdate)
	case ClusterVersion:
		return utilerrors.NewAggregate([]error{reconcileClusterVersion(ctx, hostedClusterClient, createOrUpdate, params)})
	case ClusterOperators:
		return reconcileClusterOperators(ctx, hostedClusterClient, createOrUpdate, params)
	case Namespaces:
		return reconcileNamespaces(ctx, hostedClusterClient, createOrUpdate, params)
	default:
		return fmt.Errorf("unknown configuration reconciliation stage: %d", stage)
	}
}
