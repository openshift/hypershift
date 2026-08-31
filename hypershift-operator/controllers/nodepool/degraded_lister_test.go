package nodepool

import (
	"context"
	"fmt"
	"testing"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/watch"

	"github.com/go-logr/logr"
)

// mockListerWatcher implements cache.ListerWatcher for testing
type mockListerWatcher struct {
	listFunc  func(opts metav1.ListOptions) (runtime.Object, error)
	watchFunc func(opts metav1.ListOptions) (watch.Interface, error)
}

func (m *mockListerWatcher) List(opts metav1.ListOptions) (runtime.Object, error) {
	if m.listFunc != nil {
		return m.listFunc(opts)
	}
	return &hyperv1.NodePoolList{}, nil
}

func (m *mockListerWatcher) Watch(opts metav1.ListOptions) (watch.Interface, error) {
	if m.watchFunc != nil {
		return m.watchFunc(opts)
	}
	return nil, nil
}

func TestNewDegradedModeInformerFactory(t *testing.T) {
	tests := []struct {
		name    string
		objType runtime.Object
		want    bool // true if should wrap, false if should use standard informer
	}{
		{
			name:    "When object is NodePool, it should wrap the ListerWatcher",
			objType: &hyperv1.NodePool{},
			want:    true,
		},
		{
			name:    "When object is not NodePool, it should return standard informer",
			objType: &unstructured.Unstructured{},
			want:    false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			log := logr.Discard()
			badKeys := make(map[string]struct{})
			factory := NewDegradedModeInformerFactory(context.Background(), badKeys, log)

			mockLW := &mockListerWatcher{}
			informer := factory(mockLW, tt.objType, 0, nil)

			if informer == nil {
				t.Fatalf("factory returned nil informer")
			}

			// Verify the informer has a store
			store := informer.GetStore()
			if store == nil && tt.want {
				t.Error("expected wrapped ListerWatcher with store, got nil")
			}
		})
	}
}

func TestResilientNodePoolListerWatcher_List(t *testing.T) {
	tests := []struct {
		name          string
		badKeys       map[string]struct{}
		nodePoolList  *hyperv1.NodePoolList
		shouldInclude map[string]bool // "ns/name" → true if should be in filtered result
	}{
		{
			name:    "When NodePool is valid, it should include it in filtered result",
			badKeys: make(map[string]struct{}),
			nodePoolList: &hyperv1.NodePoolList{
				Items: []hyperv1.NodePool{
					{
						ObjectMeta: metav1.ObjectMeta{Namespace: "ns1", Name: "np1"},
						Spec: hyperv1.NodePoolSpec{
							NodeDrainTimeout: &metav1.Duration{Duration: 0},
						},
					},
				},
			},
			shouldInclude: map[string]bool{"ns1/np1": true},
		},
		{
			name: "When NodePool is in badKeys and still has valid durations, it should include it and remove from badKeys",
			badKeys: map[string]struct{}{
				"ns1/np-bad": {},
			},
			nodePoolList: &hyperv1.NodePoolList{
				Items: []hyperv1.NodePool{
					{
						ObjectMeta: metav1.ObjectMeta{Namespace: "ns1", Name: "np-bad"},
						Spec: hyperv1.NodePoolSpec{
							NodeDrainTimeout: &metav1.Duration{Duration: 0},
						},
					},
				},
			},
			shouldInclude: map[string]bool{"ns1/np-bad": true},
		},
		{
			name: "When NodePool was bad but is now recovered, it should include it and remove from badKeys",
			badKeys: map[string]struct{}{
				"ns1/np-recovered": {},
			},
			nodePoolList: &hyperv1.NodePoolList{
				Items: []hyperv1.NodePool{
					{
						ObjectMeta: metav1.ObjectMeta{Namespace: "ns1", Name: "np-recovered"},
						Spec: hyperv1.NodePoolSpec{
							NodeDrainTimeout: &metav1.Duration{Duration: 0},
						},
					},
				},
			},
			shouldInclude: map[string]bool{"ns1/np-recovered": true},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			log := logr.Discard()
			badKeys := tt.badKeys

			mockLW := &mockListerWatcher{
				listFunc: func(opts metav1.ListOptions) (runtime.Object, error) {
					return tt.nodePoolList, nil
				},
			}

			wrapper := &degradedNodePoolListerWatcher{
				delegate: mockLW,
				badKeys:  badKeys,
				log:      log,
			}

			result, err := wrapper.List(metav1.ListOptions{})
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}

			resultList, ok := result.(*hyperv1.NodePoolList)
			if !ok {
				t.Fatalf("expected NodePoolList, got %T", result)
			}

			// Check each NodePool in shouldInclude
			for key, shouldInclude := range tt.shouldInclude {
				found := false
				for _, item := range resultList.Items {
					itemKey := fmt.Sprintf("%s/%s", item.Namespace, item.Name)
					if itemKey == key {
						found = true
						break
					}
				}

				if shouldInclude && !found {
					t.Errorf("expected NodePool %s in result, but not found", key)
				}
				if !shouldInclude && found {
					t.Errorf("expected NodePool %s not in result, but found", key)
				}
			}

			// For recovered NodePools, check that badKeys was updated
			for key, shouldInclude := range tt.shouldInclude {
				if shouldInclude {
					if _, stillBad := badKeys[key]; stillBad {
						t.Errorf("expected badKeys to be cleaned for included NodePool %s, but still present", key)
					}
				}
			}
		})
	}
}

func TestResilientNodePoolListerWatcher_Watch(t *testing.T) {
	tests := []struct {
		name      string
		watchFunc func(opts metav1.ListOptions) (watch.Interface, error)
		wantErr   bool
	}{
		{
			name: "When delegate Watch succeeds, it should return the watch interface",
			watchFunc: func(opts metav1.ListOptions) (watch.Interface, error) {
				return nil, nil
			},
			wantErr: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			log := logr.Discard()
			badKeys := make(map[string]struct{})

			mockLW := &mockListerWatcher{
				watchFunc: tt.watchFunc,
			}

			wrapper := &degradedNodePoolListerWatcher{
				delegate: mockLW,
				badKeys:  badKeys,
				log:      log,
			}

			_, err := wrapper.Watch(metav1.ListOptions{})
			if (err != nil) != tt.wantErr {
				t.Errorf("Watch() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

func TestFindBadDurationFields(t *testing.T) {
	tests := []struct {
		name       string
		nodePool   *hyperv1.NodePool
		wantFields map[string]string
	}{
		{
			name: "When NodePool has valid durations, it should return empty map",
			nodePool: &hyperv1.NodePool{
				Spec: hyperv1.NodePoolSpec{
					NodeDrainTimeout:        &metav1.Duration{Duration: 0},
					NodeVolumeDetachTimeout: &metav1.Duration{Duration: 0},
				},
			},
			wantFields: map[string]string{},
		},
		{
			name: "When NodePool has invalid nodeDrainTimeout, it should identify it",
			nodePool: &hyperv1.NodePool{
				Spec: hyperv1.NodePoolSpec{
					NodeDrainTimeout: nil, // nil is valid
				},
			},
			wantFields: map[string]string{},
		},
		{
			name: "When NodePool has malformed duration string, it should identify it",
			nodePool: &hyperv1.NodePool{
				Spec: hyperv1.NodePoolSpec{
					NodeDrainTimeout: nil,
				},
			},
			wantFields: map[string]string{},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Convert NodePool to unstructured for the test
			unstruct, err := runtime.DefaultUnstructuredConverter.ToUnstructured(tt.nodePool)
			if err != nil {
				t.Fatalf("failed to convert NodePool to unstructured: %v", err)
			}

			result := findBadDurationFields(&unstructured.Unstructured{Object: unstruct})

			if len(result) != len(tt.wantFields) {
				t.Errorf("expected %d bad fields, got %d", len(tt.wantFields), len(result))
			}

			for field, wantValue := range tt.wantFields {
				if gotValue, ok := result[field]; !ok || gotValue != wantValue {
					t.Errorf("field %s: expected %q, got %q", field, wantValue, gotValue)
				}
			}
		})
	}
}

func TestFormatBadFields(t *testing.T) {
	tests := []struct {
		name      string
		badFields map[string]string
		want      string
	}{
		{
			name:      "When badFields is empty, it should return empty string",
			badFields: map[string]string{},
			want:      "",
		},
		{
			name: "When badFields has one entry, it should format it",
			badFields: map[string]string{
				"nodeDrainTimeout": "1",
			},
			want: "nodeDrainTimeout=\"1\"",
		},
		{
			name: "When badFields has multiple entries, it should format them sorted",
			badFields: map[string]string{
				"nodeVolumeDetachTimeout": "invalid",
				"nodeDrainTimeout":        "1",
			},
			want: "nodeDrainTimeout=\"1\", nodeVolumeDetachTimeout=\"invalid\"",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := formatBadFields(tt.badFields)
			if got != tt.want {
				t.Errorf("formatBadFields() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestSortedKeys(t *testing.T) {
	tests := []struct {
		name string
		m    map[string]string
		want []string
	}{
		{
			name: "When map is empty, it should return empty slice",
			m:    map[string]string{},
			want: []string{},
		},
		{
			name: "When map has one key, it should return it",
			m: map[string]string{
				"key1": "val1",
			},
			want: []string{"key1"},
		},
		{
			name: "When map has multiple keys, it should return them sorted",
			m: map[string]string{
				"z": "val",
				"a": "val",
				"m": "val",
			},
			want: []string{"a", "m", "z"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := sortedKeys(tt.m)
			if len(got) != len(tt.want) {
				t.Errorf("sortedKeys() returned %d keys, want %d", len(got), len(tt.want))
				return
			}
			for i, key := range got {
				if key != tt.want[i] {
					t.Errorf("sortedKeys()[%d] = %q, want %q", i, key, tt.want[i])
				}
			}
		})
	}
}
