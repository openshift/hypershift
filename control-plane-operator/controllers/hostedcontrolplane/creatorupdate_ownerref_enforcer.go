package hostedcontrolplane

import (
	"context"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	"github.com/openshift/hypershift/support/config"
	"github.com/openshift/hypershift/support/upsert"

	crclient "sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
)

func createOrUpdateWithOwnerRefFactory(upstream upsert.CreateOrUpdateFN) func(*hyperv1.HostedControlPlane) upsert.CreateOrUpdateFN {
	return func(owner *hyperv1.HostedControlPlane) upsert.CreateOrUpdateFN {
		ownerRef := config.OwnerRefFrom(owner)
		return func(ctx context.Context, c crclient.Client, obj crclient.Object, f controllerutil.MutateFn) (controllerutil.OperationResult, error) {

			// Wrap f() to add the ownerRef at the end
			mutateFN := func() error {
				if err := f(); err != nil {
					return err
				}
				if obj.GetNamespace() == "" {
					// don't tag cluster scoped resources
					return nil
				}
				ownerRef.ApplyTo(obj)
				return nil
			}

			return upstream(ctx, c, obj, mutateFN)
		}
	}
}
