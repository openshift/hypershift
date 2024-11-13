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
			name: "empty tags",
		},
		{
			name: "single tag",
			tags: []hyperv1.NeutronTag{"tag1"},
			want: []capo.NeutronTag{"tag1"},
		},
		{
			name: "multiple tags",
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
			name:       "empty tags",
			tags:       []hyperv1.NeutronTag{},
			tagsAny:    []hyperv1.NeutronTag{},
			NotTags:    []hyperv1.NeutronTag{},
			NotTagsAny: []hyperv1.NeutronTag{},
			want:       capo.FilterByNeutronTags{},
		},
		{
			name:       "single tag in each category",
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
			name: "filled filter",
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
