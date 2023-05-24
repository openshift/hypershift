package manifests

import "testing"

func TestEmptyDirVolumeAggregator(t *testing.T) {
	type args struct {
		volumes []string
	}
	tests := []struct {
		name string
		args args
		want string
	}{
		{
			name: "Sending an slice of strings, it should return string with the same content separated by comma",
			args: args{
				volumes: []string{
					"test1",
					"test2",
					"test3",
				},
			},
			want: `"test1,test2,test3"`,
		},
		{
			name: "Sending a slice with one string, it should return a string with the same content wihout commas",
			args: args{
				volumes: []string{"test1"},
			},
			want: `"test1"`,
		},
		{
			name: "Sending an empty string, it should return another empty string",
			args: args{},
			want: `""`,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := EmptyDirVolumeAggregator(tt.args.volumes...); got != tt.want {
				t.Errorf("EmptyDirVolumeAggregator() = %v, want %v", got, tt.want)
			}
		})
	}
}
