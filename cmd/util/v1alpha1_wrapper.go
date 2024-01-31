package util

import (
	"context"
	"fmt"

	hyperv1alpha1 "github.com/openshift/hypershift/api/hypershift/v1alpha1"
	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	"github.com/openshift/hypershift/hypershift-operator/conversion"
	"k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	crclient "sigs.k8s.io/controller-runtime/pkg/client"
)

func v1alpha1Client(client crclient.Client) crclient.Client {
	return &v1alpha1Wrapper{
		innerClient: client,
	}
}

// v1alpha1Wrapper is a client-side converter that uses the v1beta1 version of the
// HyperShift API but translates it to v1alpha1 when sending/receiving from the cluster.
// This allows us to have client v1beta1 code but still be able to interact with clusters
// where v1beta1 does not exist yet.
type v1alpha1Wrapper struct {
	innerClient crclient.Client
}

func (w *v1alpha1Wrapper) SubResource(subResource string) crclient.SubResourceClient {
	return w.innerClient.SubResource(subResource)
}

func (w *v1alpha1Wrapper) GroupVersionKindFor(obj runtime.Object) (schema.GroupVersionKind, error) {
	return w.innerClient.GroupVersionKindFor(obj)
}

func (w *v1alpha1Wrapper) IsObjectNamespaced(obj runtime.Object) (bool, error) {
	return w.innerClient.IsObjectNamespaced(obj)
}

func (w *v1alpha1Wrapper) Scheme() *runtime.Scheme {
	return w.innerClient.Scheme()
}

func (w *v1alpha1Wrapper) RESTMapper() meta.RESTMapper {
	return w.innerClient.RESTMapper()
}

func (w *v1alpha1Wrapper) Get(ctx context.Context, key crclient.ObjectKey, obj crclient.Object, opts ...crclient.GetOption) error {
	serverResource := v1alpha1Resource(obj)
	if err := w.innerClient.Get(ctx, key, serverResource); err != nil {
		return err
	}
	if serverResource != obj {
		return convertToV1Beta1(serverResource, obj)
	}
	return nil
}

func (w *v1alpha1Wrapper) List(ctx context.Context, list crclient.ObjectList, opts ...crclient.ListOption) error {
	serverResource := v1alpha1ListResource(list)
	if err := w.innerClient.List(ctx, serverResource, opts...); err != nil {
		return err
	}
	if serverResource != list {
		return convertListToV1Beta1(serverResource, list)
	}
	return nil
}

func (w *v1alpha1Wrapper) Create(ctx context.Context, obj crclient.Object, opts ...crclient.CreateOption) error {
	serverResource := v1alpha1Resource(obj)
	if serverResource != obj {
		if err := convertToV1Alpha1(obj, serverResource); err != nil {
			return err
		}
	}
	if err := w.innerClient.Create(ctx, serverResource, opts...); err != nil {
		return err
	}
	if serverResource != obj {
		return convertToV1Beta1(serverResource, obj)
	}
	return nil
}

func (w *v1alpha1Wrapper) Delete(ctx context.Context, obj crclient.Object, opts ...crclient.DeleteOption) error {
	serverResource := v1alpha1Resource(obj)
	if serverResource != obj {
		if err := convertToV1Alpha1(obj, serverResource); err != nil {
			return err
		}
	}
	return w.innerClient.Delete(ctx, serverResource, opts...)
}

func (w *v1alpha1Wrapper) Update(ctx context.Context, obj crclient.Object, opts ...crclient.UpdateOption) error {
	serverResource := v1alpha1Resource(obj)
	if serverResource != obj {
		if err := convertToV1Alpha1(obj, serverResource); err != nil {
			return err
		}
	}
	if err := w.innerClient.Update(ctx, serverResource, opts...); err != nil {
		return err
	}
	if serverResource != obj {
		return convertToV1Beta1(serverResource, obj)
	}
	return nil
}

func (w *v1alpha1Wrapper) Patch(ctx context.Context, obj crclient.Object, patch crclient.Patch, opts ...crclient.PatchOption) error {
	serverResource := v1alpha1Resource(obj)
	if serverResource != obj {
		if err := convertToV1Alpha1(obj, serverResource); err != nil {
			return err
		}
	}
	if err := w.innerClient.Patch(ctx, serverResource, patch, opts...); err != nil {
		return err
	}
	if serverResource != obj {
		return convertToV1Beta1(serverResource, obj)
	}
	return nil
}

func (w *v1alpha1Wrapper) DeleteAllOf(ctx context.Context, obj crclient.Object, opts ...crclient.DeleteAllOfOption) error {
	serverResource := v1alpha1Resource(obj)
	return w.innerClient.DeleteAllOf(ctx, serverResource, opts...)
}

// Status returns the inner client since no CLI code sets status on resources.
// If this client is ever used for anything other than the CLI, the Status client
// would also need to handle conversion.
func (w *v1alpha1Wrapper) Status() crclient.StatusWriter {
	return w.innerClient.Status()
}

func v1alpha1Resource(obj crclient.Object) crclient.Object {
	result := obj
	switch obj.(type) {
	case *hyperv1.HostedCluster:
		result = &hyperv1alpha1.HostedCluster{}
	case *hyperv1.NodePool:
		result = &hyperv1alpha1.NodePool{}
	case *hyperv1.AWSEndpointService:
		result = &hyperv1alpha1.AWSEndpointService{}
	case *hyperv1.HostedControlPlane:
		result = &hyperv1.HostedControlPlane{}
	}
	return result
}

func v1alpha1ListResource(list crclient.ObjectList) crclient.ObjectList {
	result := list
	switch list.(type) {
	case *hyperv1.HostedClusterList:
		result = &hyperv1alpha1.HostedClusterList{}
	case *hyperv1.NodePoolList:
		result = &hyperv1alpha1.NodePoolList{}
	case *hyperv1.AWSEndpointServiceList:
		result = &hyperv1alpha1.AWSEndpointServiceList{}
	case *hyperv1.HostedControlPlaneList:
		result = &hyperv1.HostedControlPlaneList{}
	}
	return result
}

func convertToV1Beta1(src, dest crclient.Object) error {
	return conversion.ConvertTo(src, dest)
}

func convertToV1Alpha1(src, dest crclient.Object) error {
	return conversion.ConvertFrom(src, dest)
}

func convertListToV1Beta1(src, dest crclient.ObjectList) error {
	switch srcList := src.(type) {
	case *hyperv1alpha1.HostedClusterList:
		destList, ok := dest.(*hyperv1.HostedClusterList)
		if !ok {
			return fmt.Errorf("unexpected destination list type: %T", dest)
		}
		for i := range srcList.Items {
			destItem := &hyperv1.HostedCluster{}
			conversion.Convert_HostedCluster_v1alpha1_to_v1beta1(&srcList.Items[i], destItem)
			destList.Items = append(destList.Items, *destItem)
		}
	case *hyperv1alpha1.NodePoolList:
		destList, ok := dest.(*hyperv1.NodePoolList)
		if !ok {
			return fmt.Errorf("unexpected destination list type: %T", dest)
		}
		for i := range srcList.Items {
			destItem := &hyperv1.NodePool{}
			conversion.Convert_NodePool_v1alpha1_to_v1beta1(&srcList.Items[i], destItem)
			destList.Items = append(destList.Items, *destItem)
		}
	case *hyperv1alpha1.AWSEndpointServiceList:
		destList, ok := dest.(*hyperv1.AWSEndpointServiceList)
		if !ok {
			return fmt.Errorf("unexpected destination list type: %T", dest)
		}
		for i := range srcList.Items {
			destItem := &hyperv1.AWSEndpointService{}
			conversion.Convert_AWSEndpointService_v1alpha1_to_v1beta1(&srcList.Items[i], destItem)
			destList.Items = append(destList.Items, *destItem)
		}
	case *hyperv1alpha1.HostedControlPlaneList:
		destList, ok := dest.(*hyperv1.HostedControlPlaneList)
		if !ok {
			return fmt.Errorf("unexpected destination list type: %T", dest)
		}
		for i := range srcList.Items {
			destItem := &hyperv1.HostedControlPlane{}
			conversion.Convert_HostedControlPlane_v1alpha1_to_v1beta1(&srcList.Items[i], destItem)
			destList.Items = append(destList.Items, *destItem)
		}
	}
	return nil
}
