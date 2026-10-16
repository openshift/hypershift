package statuspatching

import "sigs.k8s.io/controller-runtime/pkg/client"

func PatchStatus[T any](ctx any, c client.Client, obj T, mutate func(T) error) error {
	return nil
}
