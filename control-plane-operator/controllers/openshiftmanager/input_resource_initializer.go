package openshiftmanager

import (
	"context"
	"fmt"

	"github.com/openshift/multi-operator-manager/pkg/library/libraryinputresources"

	"k8s.io/apimachinery/pkg/api/equality"
	"k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/util/sets"

	"sigs.k8s.io/controller-runtime/pkg/cache"
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
				return fmt.Errorf("unable to find Kind for: %#v, for: %s operator, err: %w", exactResource, operator, err)
			}

			if registeredGVK.Has(gvk.String()) {
				continue
			}

			_, err = r.managementClusterCache.GetInformerForKind(ctx, gvk, cache.BlockUntilSynced(true))
			if err != nil {
				return fmt.Errorf("unable get an informer for gvk: %v, operator: %v, err: %w", gvk, operator, err)
			}
			// TODO: register informer event handlers

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
	isResourceListSupportedFunc := func(resList libraryinputresources.ResourceList, areExactResourcesSupported bool, fieldPath string) error {
		if !areExactResourcesSupported && len(resList.ExactResources) > 0 {
			return fmt.Errorf("%v.ExactResources are unsupported for now", fieldPath)
		}

		if !equality.Semantic.DeepEqual(resList, libraryinputresources.ResourceList{ExactResources: resList.ExactResources}) {
			if len(resList.GeneratedNameResources) > 0 {
				return fmt.Errorf("%v.GeneratedNameResources are unsupported for now", fieldPath)
			}
			if len(resList.LabelSelectedResources) > 0 {
				return fmt.Errorf("%v.LabelSelectedResources are unsupported for now", fieldPath)
			}
			if len(resList.ResourceReferences) > 0 {
				return fmt.Errorf("%v.ResourceReferences are unsupported for now", fieldPath)
			}
			return fmt.Errorf("%v has an unknown field(s) set", fieldPath)
		}
		return nil
	}

	toCommonErrMsgFunc := func(operator string, err error) error {
		return fmt.Errorf("unsupported input resources found for %s operator: %w", operator, err)
	}
	for operator, inputResource := range inputResources {
		if err := isResourceListSupportedFunc(inputResource.ApplyConfigurationResources, true, "ApplyConfigurationResources"); err != nil {
			return toCommonErrMsgFunc(operator, err)
		}
		if err := isResourceListSupportedFunc(inputResource.OperandResources.ConfigurationResources, false, "OperandResources.ConfigurationResources"); err != nil {
			return toCommonErrMsgFunc(operator, err)
		}
		if err := isResourceListSupportedFunc(inputResource.OperandResources.ManagementResources, false, "OperandResources.ManagementResources"); err != nil {
			return toCommonErrMsgFunc(operator, err)
		}
		if err := isResourceListSupportedFunc(inputResource.OperandResources.UserWorkloadResources, false, "OperandResources.UserWorkloadResources"); err != nil {
			return toCommonErrMsgFunc(operator, err)
		}
	}
	return nil
}
