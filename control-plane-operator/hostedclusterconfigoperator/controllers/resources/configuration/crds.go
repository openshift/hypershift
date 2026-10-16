package configuration

import (
	"context"
	"fmt"

	"github.com/openshift/hypershift/control-plane-operator/hostedclusterconfigoperator/controllers/resources/crd"
	"github.com/openshift/hypershift/control-plane-operator/hostedclusterconfigoperator/controllers/resources/manifests"
	"github.com/openshift/hypershift/support/upsert"

	utilerrors "k8s.io/apimachinery/pkg/util/errors"

	"sigs.k8s.io/controller-runtime/pkg/client"
)

func reconcileCRDs(ctx context.Context, hostedClusterClient client.Client, createOrUpdate upsert.CreateOrUpdateFN) error {
	var errs []error

	requestCount := manifests.RequestCountCRD()
	if _, err := createOrUpdate(ctx, hostedClusterClient, requestCount, func() error {
		return crd.ReconcileRequestCountCRD(requestCount)
	}); err != nil {
		errs = append(errs, fmt.Errorf("failed to reconcile request count crd: %w", err))
	}

	return utilerrors.NewAggregate(errs)
}
