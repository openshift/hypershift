package k8sutil

import (
	"context"
	"errors"
	"fmt"
	"strings"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/client-go/util/retry"

	"sigs.k8s.io/controller-runtime/pkg/client"
)

// CopyConfigMap copies the .Data field of configMap source into configmap cm.
func CopyConfigMap(cm, source *corev1.ConfigMap) {
	cm.Data = map[string]string{}
	for k, v := range source.Data {
		cm.Data[k] = v
	}
}

func UpdateObject[T client.Object](ctx context.Context, c client.Client, obj T, mutate func() error) error {
	return retry.RetryOnConflict(retry.DefaultBackoff, func() error {
		if err := c.Get(ctx, client.ObjectKeyFromObject(obj), obj); err != nil {
			return err
		}

		original := obj.DeepCopyObject().(T)
		if err := mutate(); err != nil {
			return err
		}

		return c.Patch(ctx, obj, client.MergeFrom(original))
	})
}

func DeleteIfNeededWithOptions(ctx context.Context, c client.Client, o client.Object, opts ...client.DeleteOption) (exists bool, err error) {
	if err := c.Get(ctx, client.ObjectKeyFromObject(o), o); err != nil {
		if apierrors.IsNotFound(err) || meta.IsNoMatchError(err) {
			return false, nil
		}
		return false, fmt.Errorf("error getting %T: %w", o, err)
	}
	if o.GetDeletionTimestamp() != nil {
		return true, nil
	}
	if err := c.Delete(ctx, o, opts...); err != nil {
		if apierrors.IsNotFound(err) {
			return false, nil
		}
		return false, fmt.Errorf("error deleting %T: %w", o, err)
	}

	return true, nil
}

func DeleteIfNeededWithPredicate[T client.Object](ctx context.Context, c client.Client, o T, predicate func(T) bool) (exists bool, err error) {
	if err := c.Get(ctx, client.ObjectKeyFromObject(o), o); err != nil {
		if apierrors.IsNotFound(err) || meta.IsNoMatchError(err) {
			return false, nil
		}
		return false, fmt.Errorf("error getting %T: %w", o, err)
	}
	if o.GetDeletionTimestamp() != nil {
		return true, nil
	}
	if !predicate(o) {
		return true, nil
	}
	if err := c.Delete(ctx, o); err != nil {
		if apierrors.IsNotFound(err) {
			return false, nil
		}
		return false, fmt.Errorf("error deleting %T: %w", o, err)
	}

	return true, nil
}

func DeleteAllIfNeeded(ctx context.Context, c client.Client, o ...client.Object) error {
	errs := []error{}
	for _, obj := range o {
		_, err := DeleteIfNeededWithOptions(ctx, c, obj)
		if err != nil {
			errs = append(errs, err)
		}
	}
	if len(errs) > 0 {
		return errors.Join(errs...)
	}
	return nil
}

func DeleteIfNeeded(ctx context.Context, c client.Client, o client.Object) (exists bool, err error) {
	return DeleteIfNeededWithOptions(ctx, c, o)
}

// ParseNodeSelector parses a comma separated string of key=value pairs into a map.
func ParseNodeSelector(str string) map[string]string {
	if len(str) == 0 {
		return nil
	}
	parts := strings.Split(str, ",")
	result := make(map[string]string)
	for _, part := range parts {
		kv := strings.SplitN(part, "=", 2)
		if len(kv) != 2 {
			continue
		}
		if len(kv[0]) == 0 || len(kv[1]) == 0 {
			continue
		}
		result[kv[0]] = kv[1]
	}
	return result
}

func ApplyAWSLoadBalancerTargetNodesAnnotation(svc *corev1.Service, hcp *hyperv1.HostedControlPlane) {
	if hcp.Spec.Platform.Type != hyperv1.AWSPlatform {
		return
	}
	if svc.Annotations == nil {
		svc.Annotations = make(map[string]string)
	}
	selectors, ok := hcp.Annotations[hyperv1.AWSLoadBalancerTargetNodesAnnotation]
	if ok {
		svc.Annotations["service.beta.kubernetes.io/aws-load-balancer-target-node-labels"] = selectors
	}
}
