package openstackutil

import (
	"reflect"
	"testing"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"

	capo "sigs.k8s.io/cluster-api-provider-openstack/api/v1beta1"
)

func TestConvertHypershiftTagToCAPOTag(t *testing.T) {
	tests := []struct {
		name string
		tags []hyperv1.NeutronTag
		want []capo.NeutronTag
	}{
		{
			name: "When tags are empty, it should return empty CAPO tags",
		},
		{
			name: "When a single tag is provided, it should convert to CAPO tag",
			tags: []hyperv1.NeutronTag{"tag1"},
			want: []capo.NeutronTag{"tag1"},
		},
		{
			name: "When multiple tags are provided, it should convert all to CAPO tags",
			tags: []hyperv1.NeutronTag{"tag1", "tag2"},
			want: []capo.NeutronTag{"tag1", "tag2"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := convertHypershiftTagToCAPOTag(tt.tags); !reflect.DeepEqual(got, tt.want) {
				t.Errorf("convertHypershiftTagToCAPOTag() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestCreateCAPOFilterTags(t *testing.T) {
	tests := []struct {
		name       string
		tags       []hyperv1.NeutronTag
		tagsAny    []hyperv1.NeutronTag
		NotTags    []hyperv1.NeutronTag
		NotTagsAny []hyperv1.NeutronTag
		want       capo.FilterByNeutronTags
	}{
		{
			name:       "When all tag categories are empty, it should return empty filter",
			tags:       []hyperv1.NeutronTag{},
			tagsAny:    []hyperv1.NeutronTag{},
			NotTags:    []hyperv1.NeutronTag{},
			NotTagsAny: []hyperv1.NeutronTag{},
			want:       capo.FilterByNeutronTags{},
		},
		{
			name:       "When each tag category has a single tag, it should convert all categories",
			tags:       []hyperv1.NeutronTag{"tag1"},
			tagsAny:    []hyperv1.NeutronTag{"tag2"},
			NotTags:    []hyperv1.NeutronTag{"tag3"},
			NotTagsAny: []hyperv1.NeutronTag{"tag4"},
			want: capo.FilterByNeutronTags{
				Tags:       []capo.NeutronTag{"tag1"},
				TagsAny:    []capo.NeutronTag{"tag2"},
				NotTags:    []capo.NeutronTag{"tag3"},
				NotTagsAny: []capo.NeutronTag{"tag4"},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := CreateCAPOFilterTags(tt.tags, tt.tagsAny, tt.NotTags, tt.NotTagsAny); !reflect.DeepEqual(got, tt.want) {
				t.Errorf("CreateCAPOFilterTags() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestCreateCAPONetworkFilter(t *testing.T) {
	tests := []struct {
		name   string
		filter *hyperv1.NetworkFilter
		want   *capo.NetworkFilter
	}{
		{
			name: "When filter has all fields populated, it should convert to CAPO network filter",
			filter: &hyperv1.NetworkFilter{
				Name:        "test-name",
				Description: "test-description",
				ProjectID:   "test-project-id",
				FilterByNeutronTags: hyperv1.FilterByNeutronTags{
					Tags:       []hyperv1.NeutronTag{"tag1"},
					TagsAny:    []hyperv1.NeutronTag{"tag2"},
					NotTags:    []hyperv1.NeutronTag{"tag3"},
					NotTagsAny: []hyperv1.NeutronTag{"tag4"},
				},
			},
			want: &capo.NetworkFilter{
				Name:        "test-name",
				Description: "test-description",
				ProjectID:   "test-project-id",
				FilterByNeutronTags: capo.FilterByNeutronTags{
					Tags:       []capo.NeutronTag{"tag1"},
					TagsAny:    []capo.NeutronTag{"tag2"},
					NotTags:    []capo.NeutronTag{"tag3"},
					NotTagsAny: []capo.NeutronTag{"tag4"},
				},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := CreateCAPONetworkFilter(tt.filter); !reflect.DeepEqual(got, tt.want) {
				t.Errorf("CreateCAPONetworkFilter() = %v, want %v", got, tt.want)
			}
		})
	}
}
