//go:build e2ev2 && backuprestore

package backuprestore

import (
	"testing"
)

func TestProviderIDs(t *testing.T) {
	tests := []struct {
		name     string
		machines []MachineIdentity
		want     []string
	}{
		{
			name:     "When input is empty, it should return nil",
			machines: nil,
			want:     nil,
		},
		{
			name: "When machines have provider IDs, it should extract and sort them",
			machines: []MachineIdentity{
				{Name: "m-2", ProviderID: "aws:///us-east-1a/i-222"},
				{Name: "m-1", ProviderID: "aws:///us-east-1a/i-111"},
			},
			want: []string{
				"aws:///us-east-1a/i-111",
				"aws:///us-east-1a/i-222",
			},
		},
		{
			name: "When some machines have empty provider IDs, it should skip them",
			machines: []MachineIdentity{
				{Name: "m-1", ProviderID: "aws:///us-east-1a/i-111"},
				{Name: "m-2", ProviderID: ""},
			},
			want: []string{
				"aws:///us-east-1a/i-111",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ProviderIDs(tt.machines)
			if len(got) != len(tt.want) {
				t.Fatalf("ProviderIDs() returned %d items, want %d", len(got), len(tt.want))
			}
			for i := range got {
				if got[i] != tt.want[i] {
					t.Errorf("ProviderIDs()[%d] = %q, want %q", i, got[i], tt.want[i])
				}
			}
		})
	}
}

func TestNodeNames(t *testing.T) {
	tests := []struct {
		name  string
		nodes []NodeIdentity
		want  []string
	}{
		{
			name:  "When input is empty, it should return nil",
			nodes: nil,
			want:  nil,
		},
		{
			name: "When nodes exist, it should extract and sort their names",
			nodes: []NodeIdentity{
				{Name: "node-b", UID: "uid-2"},
				{Name: "node-a", UID: "uid-1"},
			},
			want: []string{"node-a", "node-b"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := NodeNames(tt.nodes)
			if len(got) != len(tt.want) {
				t.Fatalf("NodeNames() returned %d items, want %d", len(got), len(tt.want))
			}
			for i := range got {
				if got[i] != tt.want[i] {
					t.Errorf("NodeNames()[%d] = %q, want %q", i, got[i], tt.want[i])
				}
			}
		})
	}
}

func TestNodeUIDs(t *testing.T) {
	tests := []struct {
		name  string
		nodes []NodeIdentity
		want  []string
	}{
		{
			name:  "When input is empty, it should return nil",
			nodes: nil,
			want:  nil,
		},
		{
			name: "When nodes have UIDs, it should extract and sort them",
			nodes: []NodeIdentity{
				{Name: "node-b", UID: "uid-2"},
				{Name: "node-a", UID: "uid-1"},
			},
			want: []string{"uid-1", "uid-2"},
		},
		{
			name: "When some nodes have empty UIDs, it should skip them",
			nodes: []NodeIdentity{
				{Name: "node-a", UID: "uid-1"},
				{Name: "node-b", UID: ""},
			},
			want: []string{"uid-1"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := NodeUIDs(tt.nodes)
			if len(got) != len(tt.want) {
				t.Fatalf("NodeUIDs() returned %d items, want %d", len(got), len(tt.want))
			}
			for i := range got {
				if got[i] != tt.want[i] {
					t.Errorf("NodeUIDs()[%d] = %q, want %q", i, got[i], tt.want[i])
				}
			}
		})
	}
}
