package openstackutil

import (
	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"

	capo "sigs.k8s.io/cluster-api-provider-openstack/api/v1beta1"
)

func convertHypershiftTagToCAPOTag(tags []hyperv1.NeutronTag) []capo.NeutronTag {
	var capoTags []capo.NeutronTag
	for i := range tags {
		capoTags = append(capoTags, capo.NeutronTag(tags[i]))
	}
	return capoTags
}

func CreateCAPOFilterTags(tags, tagsAny, NotTags, NotTagsAny []hyperv1.NeutronTag) capo.FilterByNeutronTags {
	return capo.FilterByNeutronTags{
		Tags:       convertHypershiftTagToCAPOTag(tags),
		TagsAny:    convertHypershiftTagToCAPOTag(tagsAny),
		NotTags:    convertHypershiftTagToCAPOTag(NotTags),
		NotTagsAny: convertHypershiftTagToCAPOTag(NotTagsAny),
	}
}

func CreateCAPONetworkFilter(filter *hyperv1.NetworkFilter) *capo.NetworkFilter {
	return &capo.NetworkFilter{
		Name:                filter.Name,
		Description:         filter.Description,
		ProjectID:           filter.ProjectID,
		FilterByNeutronTags: CreateCAPOFilterTags(filter.Tags, filter.TagsAny, filter.NotTags, filter.NotTagsAny),
	}
}
