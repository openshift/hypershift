//go:build e2ev2

package tests

import (
	"context"

	"k8s.io/client-go/util/retry"

	crclient "sigs.k8s.io/controller-runtime/pkg/client"
)

func updateHostedFeatureObject[Object crclient.Object](ctx context.Context, client crclient.Client, object Object, update func(Object)) error {
	key := crclient.ObjectKeyFromObject(object)
	return retry.RetryOnConflict(retry.DefaultRetry, func() error {
		if err := client.Get(ctx, key, object); err != nil {
			return err
		}
		update(object)
		return client.Update(ctx, object)
	})
}
