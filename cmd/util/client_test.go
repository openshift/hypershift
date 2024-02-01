package util

import (
	"reflect"
	"testing"
)

func TestParseTags(t *testing.T) {
	tests := []struct {
		name    string
		tags    []string
		want    map[string]string
		wantErr bool
	}{
		{
			name:    "Valid tags",
			tags:    []string{"key1=value1", "key2=value2", "key3=value3"},
			want:    map[string]string{"key1": "value1", "key2": "value2", "key3": "value3"},
			wantErr: false,
		},
		{
			name:    "Empty tags",
			tags:    []string{},
			want:    map[string]string{},
			wantErr: false,
		},
		{
			name:    "Invalid tag format",
			tags:    []string{"key1=value1", "key2value2", "key3=value3"},
			want:    nil,
			wantErr: true,
		},
		{
			name:    "Empty key",
			tags:    []string{"=value1", "key2=value2", "key3=value3"},
			want:    nil,
			wantErr: true,
		},
		{
			name:    "Empty value",
			tags:    []string{"key1=", "key2=value2", "key3=value3"},
			want:    nil,
			wantErr: true,
		},
		{
			name:    "Empty tag",
			tags:    []string{""},
			want:    nil,
			wantErr: true,
		},
		{
			name:    "Multiple equal signs",
			tags:    []string{"key1=value1", "key2=value2=value3"},
			want:    nil,
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ParseTags(tt.tags)
			if (err != nil) != tt.wantErr {
				t.Errorf("ParseTags() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("ParseTags() got = %v, want %v", got, tt.want)
			}
		})
	}
}
