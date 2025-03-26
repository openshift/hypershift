package testutil

import (
	"testing"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"

	"github.com/google/go-cmp/cmp"
)

func TestCompareRuntimObjectIgnoreRvTypeMeta(t *testing.T) {
	tests := []struct {
		name           string
		x              runtime.Object
		y              runtime.Object
		expectEquality bool
	}{
		{
			name:           "Different RV, equal",
			x:              &corev1.Pod{ObjectMeta: metav1.ObjectMeta{ResourceVersion: "1"}},
			y:              &corev1.Pod{},
			expectEquality: true,
		},
		{
			name: "Different obj and different RV, not equal",
			x:    &corev1.Pod{ObjectMeta: metav1.ObjectMeta{ResourceVersion: "1"}},
			y:    &corev1.Pod{ObjectMeta: metav1.ObjectMeta{Name: "other"}},
		},
		{
			name:           "Different TypeMeta, equal",
			x:              &corev1.Pod{TypeMeta: metav1.TypeMeta{Kind: "Pod"}},
			y:              &corev1.Pod{},
			expectEquality: true,
		},
		{
			name: "Different TypeMeta and object, not equal",
			x:    &corev1.Pod{TypeMeta: metav1.TypeMeta{Kind: "Pod"}},
			y:    &corev1.Pod{ObjectMeta: metav1.ObjectMeta{Name: "other"}},
		},
		{
			name: "Lists with items with different type meta and rv, equal",
			x: &corev1.PodList{Items: []corev1.Pod{
				{TypeMeta: metav1.TypeMeta{Kind: "Secret"}, ObjectMeta: metav1.ObjectMeta{ResourceVersion: "1"}},
			}},
			y:              &corev1.PodList{Items: []corev1.Pod{{}}},
			expectEquality: true,
		},
		{
			name: "Lists with different items, not equal",
			x: &corev1.PodList{Items: []corev1.Pod{
				{Spec: corev1.PodSpec{ServiceAccountName: "foo"}},
			}},
			y: &corev1.PodList{Items: []corev1.Pod{{}}},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			diff := cmp.Diff(tc.x, tc.y, RuntimeObjectIgnoreRvTypeMeta)
			if diff == "" != tc.expectEquality {
				t.Errorf("expectEquality: %t, got diff: %s", tc.expectEquality, diff)
			}
		})
	}
}
