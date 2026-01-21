package openshiftmanager

import (
	"context"
	"fmt"

	"github.com/openshift/multi-operator-manager/pkg/library/libraryinputresources"

	"k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/apimachinery/pkg/runtime/schema"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	"k8s.io/apimachinery/pkg/util/sets"
	k8scache "k8s.io/client-go/tools/cache"

	"sigs.k8s.io/controller-runtime/pkg/cache"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// inputResourceInitializer is responsible for discovering input resources
// required by operators and starting the corresponding informers.
//
// once all informers are successfully started and fully synced,
// the initializer completes its execution.
//
// TODO: after informer synchronization send readiness signal to the main controller.
type inputResourceInitializer struct {
	managementClusterRESTMapper meta.RESTMapper
	managementClusterCache      cache.Cache

	inputResourcesDispatcher *inputResourceDispatcher
}

func newInputResourceInitializer(mgmtClusterRESTMapper meta.RESTMapper, mgmtClusterCache cache.Cache) *inputResourceInitializer {
	return &inputResourceInitializer{
		managementClusterRESTMapper: mgmtClusterRESTMapper,
		managementClusterCache:      mgmtClusterCache,
	}
}

func (r *inputResourceInitializer) Start(ctx context.Context) error {
	inputResources, err := r.discoverInputResources()
	if err != nil {
		return err
	}
	if err = r.checkSupportedInputResources(inputResources); err != nil {
		return err
	}
	inputResFilters, err := r.buildInputResourceFilters(inputResources)
	if err != nil {
		return err
	}
	r.inputResourcesDispatcher = newInputResourceDispatcher(inputResFilters)
	return r.startAndWaitForInformersFor(ctx, inputResources)
}

func (r *inputResourceInitializer) discoverInputResources() (map[string]*libraryinputresources.InputResources, error) {
	return nil, fmt.Errorf("not implemented")
}

func (r *inputResourceInitializer) startAndWaitForInformersFor(ctx context.Context, inputResources map[string]*libraryinputresources.InputResources) error {
	for operator, resources := range inputResources {
		// note that for the POC we are only interested in ApplyConfigurationResources.ExactResources
		// the checkSupportedInputResources ensures no other resources were provided.
		//
		// TODO: in the future we need to extend to full list
		registeredGVK := sets.NewString()
		for _, exactResource := range resources.ApplyConfigurationResources.ExactResources {
			gvr := schema.GroupVersionResource{Group: exactResource.Group, Version: exactResource.Version, Resource: exactResource.Resource}
			gvk, err := r.managementClusterRESTMapper.KindFor(gvr)
			if err != nil {
				return fmt.Errorf("unable to find Kind for %#v, for %s operator, err: %w", exactResource, operator, err)
			}

			if registeredGVK.Has(gvk.String()) {
				continue
			}

			informer, err := r.managementClusterCache.GetInformerForKind(ctx, gvk, cache.BlockUntilSynced(true))
			if err != nil {
				return err
			}

			if _, err = informer.AddEventHandler(k8scache.ResourceEventHandlerFuncs{
				AddFunc: func(obj interface{}) {
					cObj, ok := obj.(client.Object)
					if !ok {
						utilruntime.HandleError(fmt.Errorf("added object: %#v is not client.Object", obj))
						return
					}
					r.inputResourcesDispatcher.Handle(gvk, cObj)
				},
				UpdateFunc: func(_, newObj interface{}) {
					cObj, ok := newObj.(client.Object)
					if !ok {
						utilruntime.HandleError(fmt.Errorf("updated object: %#v is not client.Object", newObj))
						return
					}
					r.inputResourcesDispatcher.Handle(gvk, cObj)
				},
				DeleteFunc: func(obj interface{}) {
					if cObj, ok := obj.(client.Object); ok {
						r.inputResourcesDispatcher.Handle(gvk, cObj)
						return
					}
					tombstone, ok := obj.(k8scache.DeletedFinalStateUnknown)
					if ok {
						cObj, ok := tombstone.Obj.(client.Object)
						if ok {
							r.inputResourcesDispatcher.Handle(gvk, cObj)
							return
						}
					}
					utilruntime.HandleError(fmt.Errorf("deleted object: %#v is not client.Object", obj))
				},
			}); err != nil {
				return err
			}

			registeredGVK.Insert(gvk.String())
		}
	}

	if !r.managementClusterCache.WaitForCacheSync(ctx) {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return fmt.Errorf("caches did not sync")
	}
	return nil
}

// checkSupportedInputResources ensures only supported resources are present.
// this method is useful only for the POC purposes.
// in the future we will not need this method.
func (r *inputResourceInitializer) checkSupportedInputResources(inputResources map[string]*libraryinputresources.InputResources) error {
	isResourceListSupported := func(resList libraryinputresources.ResourceList, areExactResourcesSupported bool, fieldPath string) error {
		if !areExactResourcesSupported && len(resList.ExactResources) > 0 {
			return fmt.Errorf("%v.ExactResources are unsupported for now", fieldPath)
		}
		if len(resList.GeneratedNameResources) > 0 {
			return fmt.Errorf("%v.GeneratedNameResources are unsupported for now", fieldPath)
		}
		if len(resList.LabelSelectedResources) > 0 {
			return fmt.Errorf("%v.LabelSelectedResources are unsupported for now", fieldPath)
		}
		if len(resList.ResourceReferences) > 0 {
			return fmt.Errorf("%v.ResourceReferences are unsupported for now", fieldPath)
		}
		return nil
	}

	toCommonErrMsgFunc := func(operator string, err error) error {
		return fmt.Errorf("unsupported input resources found for %s operator: %w", operator, err)
	}
	for operator, inputResource := range inputResources {
		if err := isResourceListSupported(inputResource.ApplyConfigurationResources, true, "ApplyConfigurationResources"); err != nil {
			return toCommonErrMsgFunc(operator, err)
		}
		if err := isResourceListSupported(inputResource.OperandResources.ConfigurationResources, false, "OperandResources.ConfigurationResources"); err != nil {
			return toCommonErrMsgFunc(operator, err)
		}
		if err := isResourceListSupported(inputResource.OperandResources.ManagementResources, false, "OperandResources.ManagementResources"); err != nil {
			return toCommonErrMsgFunc(operator, err)
		}
		if err := isResourceListSupported(inputResource.OperandResources.UserWorkloadResources, false, "OperandResources.UserWorkloadResources"); err != nil {
			return toCommonErrMsgFunc(operator, err)
		}
	}
	return nil
}

// buildInputResourceFilters prepares matchers to filter cluster(s) resources based on inputResources
func (r *inputResourceInitializer) buildInputResourceFilters(inputResources map[string]*libraryinputresources.InputResources) (map[schema.GroupVersionKind][]inputResourceEventFilter, error) {
	filters := make(map[schema.GroupVersionKind][]inputResourceEventFilter)
	for operator, resources := range inputResources {
		// note that for the POC we are only interested in ApplyConfigurationResources.ExactResources
		// the checkSupportedInputResources ensures no other resources were provided.
		//
		// TODO: in the future we need to extend to full list
		for _, exactResource := range resources.ApplyConfigurationResources.ExactResources {
			gvr := schema.GroupVersionResource{
				Group:    exactResource.Group,
				Version:  exactResource.Version,
				Resource: exactResource.Resource,
			}
			gvk, err := r.managementClusterRESTMapper.KindFor(gvr)
			if err != nil {
				return nil, fmt.Errorf("unable to find Kind for %#v, for %s operator, err: %w", exactResource, operator, err)
			}
			filters[gvk] = append(filters[gvk], matchExactResourceFilter(exactResource))
		}
	}
	return filters, nil
}

// matchExactResourceFilter returns a matcher that checks namespace and name when provided
func matchExactResourceFilter(def libraryinputresources.ExactResourceID) inputResourceEventFilter {
	return func(obj client.Object) bool {
		if def.Namespace != "" && obj.GetNamespace() != def.Namespace {
			return false
		}
		if def.Name != "" && obj.GetName() != def.Name {
			return false
		}
		return true
	}
}
