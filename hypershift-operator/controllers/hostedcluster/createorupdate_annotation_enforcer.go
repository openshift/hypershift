package hostedcluster

import (
	"context"

	"github.com/openshift/hypershift/support/upsert"
	"github.com/openshift/hypershift/support/util"

	crclient "sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
)

func createOrUpdateWithAnnotationFactory(upstream upsert.CreateOrUpdateProvider) func(reconcile.Request) upsert.CreateOrUpdateFN {
	return func(req reconcile.Request) upsert.CreateOrUpdateFN {
		return func(ctx context.Context, c crclient.Client, obj crclient.Object, f controllerutil.MutateFn) (controllerutil.OperationResult, error) {

			// Wrap f() to add the annotation at the end
			mutateFN := func() error {
				if err := f(); err != nil {
					return err
				}
				if obj.GetNamespace() == "" {
					// don't tag cluster scoped resources
					return nil
				}
				annotations := obj.GetAnnotations()
				if annotations == nil {
					annotations = map[string]string{}
				}
				annotations[util.HostedClusterAnnotation] = req.String()
				obj.SetAnnotations(annotations)
				return nil
			}

			return upstream.CreateOrUpdate(ctx, c, obj, mutateFN)
		}
	}
}
