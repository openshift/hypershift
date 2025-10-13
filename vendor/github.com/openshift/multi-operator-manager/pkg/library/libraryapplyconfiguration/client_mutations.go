package libraryapplyconfiguration

import (
	"fmt"
	"github.com/openshift/library-go/pkg/manifestclient"
	"github.com/openshift/multi-operator-manager/pkg/library/libraryoutputresources"
	"k8s.io/apimachinery/pkg/runtime/schema"
)

type clientBasedClusterApplyResult struct {
	clusterType ClusterType

	mutationTracker *manifestclient.AllActionsTracker[manifestclient.TrackedSerializedRequest]
}

var (
	_ SingleClusterDesiredMutationGetter = &clientBasedClusterApplyResult{}
)

func (s *clientBasedClusterApplyResult) GetClusterType() ClusterType {
	return s.clusterType
}

func (s *clientBasedClusterApplyResult) Requests() MutationActionReader {
	return s.mutationTracker
}

func NewApplyConfigurationFromClient(
	mutationTracker *manifestclient.AllActionsTracker[manifestclient.TrackedSerializedRequest],
) *applyConfiguration {
	ret := &applyConfiguration{
		desiredMutationsByClusterType: map[ClusterType]SingleClusterDesiredMutationGetter{},
	}
	for clusterType := range AllClusterTypes {
		ret.desiredMutationsByClusterType[clusterType] = &clientBasedClusterApplyResult{
			clusterType:     clusterType,
			mutationTracker: mutationTracker,
		}
	}

	return ret
}

func FilterAllDesiredMutationsGetter(
	in AllDesiredMutationsGetter,
	allAllowedOutputResources *libraryoutputresources.OutputResources,
) AllDesiredMutationsGetter {
	ret := &applyConfiguration{
		desiredMutationsByClusterType: map[ClusterType]SingleClusterDesiredMutationGetter{},
	}

	for clusterType := range AllClusterTypes {
		var clusterTypeFilter *libraryoutputresources.ResourceList
		if allAllowedOutputResources != nil {
			switch clusterType {
			case ClusterTypeConfiguration:
				clusterTypeFilter = &allAllowedOutputResources.ConfigurationResources
			case ClusterTypeManagement:
				clusterTypeFilter = &allAllowedOutputResources.ManagementResources
			case ClusterTypeUserWorkload:
				clusterTypeFilter = &allAllowedOutputResources.UserWorkloadResources
			default:
				panic(fmt.Sprintf("coding error: %q", clusterType))
			}
		}

		ret.desiredMutationsByClusterType[clusterType] = &filteringSingleClusterDesiredMutationGetter{
			delegate:     in.MutationsForClusterType(clusterType),
			resourceList: clusterTypeFilter,
		}
	}

	return ret
}

type filteringSingleClusterDesiredMutationGetter struct {
	delegate     SingleClusterDesiredMutationGetter
	resourceList *libraryoutputresources.ResourceList
}

func (f filteringSingleClusterDesiredMutationGetter) GetClusterType() ClusterType {
	return f.delegate.GetClusterType()
}

func (f filteringSingleClusterDesiredMutationGetter) Requests() MutationActionReader {
	return &filteringMutationActionReader{
		delegate:     f.delegate.Requests(),
		resourceList: f.resourceList,
	}
}

var (
	_ SingleClusterDesiredMutationGetter = filteringSingleClusterDesiredMutationGetter{}
	_ MutationActionReader               = &filteringMutationActionReader{}
)

type filteringMutationActionReader struct {
	delegate     MutationActionReader
	resourceList *libraryoutputresources.ResourceList
}

func (f filteringMutationActionReader) ListActions() []manifestclient.Action {
	return f.delegate.ListActions()
}

func (f filteringMutationActionReader) RequestsForAction(action manifestclient.Action) []manifestclient.SerializedRequestish {
	return FilterSerializedRequests(f.delegate.RequestsForAction(action), f.resourceList)
}

func (f filteringMutationActionReader) AllRequests() []manifestclient.SerializedRequestish {
	return FilterSerializedRequests(f.delegate.AllRequests(), f.resourceList)
}

func RemoveEvents(requests []manifestclient.SerializedRequestish) []manifestclient.SerializedRequestish {
	filteredRequests := []manifestclient.SerializedRequestish{}

	for _, curr := range requests {
		metadata := curr.GetSerializedRequest().GetLookupMetadata()
		if metadata.ResourceType.GroupResource() == coreEventGR || metadata.ResourceType.GroupResource() == eventGR {
			continue
		}
		filteredRequests = append(filteredRequests, curr)
	}
	return filteredRequests
}

func FilterSerializedRequests(requests []manifestclient.SerializedRequestish, allowedResources *libraryoutputresources.ResourceList) []manifestclient.SerializedRequestish {
	filteredRequests := []manifestclient.SerializedRequestish{}

	for _, curr := range requests {
		metadata := curr.GetSerializedRequest().GetLookupMetadata()
		if metadataMatchesFilter(metadata, allowedResources) {
			filteredRequests = append(filteredRequests, curr)
		}
	}
	return filteredRequests
}

var (
	coreEventGR = schema.GroupResource{
		Group:    "",
		Resource: "events",
	}
	eventGR = schema.GroupResource{
		Group:    "events.k8s.io",
		Resource: "events",
	}
)

func metadataMatchesFilter(metadata manifestclient.ActionMetadata, allowedResources *libraryoutputresources.ResourceList) bool {
	if allowedResources == nil {
		return true
	}

	gr := metadata.ResourceType.GroupResource()
	if gr == coreEventGR || gr == eventGR {
		for _, curr := range allowedResources.EventingNamespaces {
			if metadata.Namespace == curr {
				return true
			}
		}
	}

	for _, curr := range allowedResources.ExactResources {
		if len(metadata.GenerateName) > 0 {
			continue
		}
		if metadata.ResourceType.Group == curr.Group &&
			metadata.ResourceType.Resource == curr.Resource &&
			metadata.Namespace == curr.Namespace &&
			metadata.Name == curr.Name {
			return true
		}
	}
	for _, curr := range allowedResources.GeneratedNameResources {
		if len(metadata.Name) > 0 {
			continue
		}
		if metadata.ResourceType.Group == curr.Group &&
			metadata.ResourceType.Resource == curr.Resource &&
			metadata.Namespace == curr.Namespace &&
			metadata.GenerateName == curr.GeneratedName {
			return true
		}
	}

	return false
}
