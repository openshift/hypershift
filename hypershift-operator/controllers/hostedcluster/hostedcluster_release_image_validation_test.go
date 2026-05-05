package hostedcluster

import (
	"testing"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func TestShouldValidateReleaseImage_WhenReleaseImageValidationInputsChangeItShouldReturnExpectedResult(t *testing.T) {
	t.Parallel()

	testCases := []struct {
		name      string
		hcluster  *hyperv1.HostedCluster
		condition *metav1.Condition
		expected  bool
	}{
		{
			name: "When there is no existing condition, it should validate",
			hcluster: &hyperv1.HostedCluster{
				ObjectMeta: metav1.ObjectMeta{Generation: 3},
			},
			expected: true,
		},
		{
			name: "When the existing condition observed generation is stale, it should validate",
			hcluster: &hyperv1.HostedCluster{
				ObjectMeta: metav1.ObjectMeta{Generation: 3},
			},
			condition: &metav1.Condition{
				Type:               string(hyperv1.ValidReleaseImage),
				Status:             metav1.ConditionTrue,
				ObservedGeneration: 2,
				Reason:             hyperv1.AsExpectedReason,
			},
			expected: true,
		},
		{
			name: "When the existing condition is not true, it should validate",
			hcluster: &hyperv1.HostedCluster{
				ObjectMeta: metav1.ObjectMeta{Generation: 3},
			},
			condition: &metav1.Condition{
				Type:               string(hyperv1.ValidReleaseImage),
				Status:             metav1.ConditionFalse,
				ObservedGeneration: 3,
				Reason:             hyperv1.InvalidImageReason,
			},
			expected: true,
		},
		{
			name: "When the skip annotation remains present with a skipped condition, it should not validate",
			hcluster: &hyperv1.HostedCluster{
				ObjectMeta: metav1.ObjectMeta{
					Generation: 3,
					Annotations: map[string]string{
						hyperv1.SkipReleaseImageValidation: "true",
					},
				},
			},
			condition: &metav1.Condition{
				Type:               string(hyperv1.ValidReleaseImage),
				Status:             metav1.ConditionTrue,
				ObservedGeneration: 3,
				Reason:             releaseImageValidationSkippedReason,
			},
			expected: false,
		},
		{
			name: "When the skip annotation is removed after a skipped condition, it should validate",
			hcluster: &hyperv1.HostedCluster{
				ObjectMeta: metav1.ObjectMeta{Generation: 3},
			},
			condition: &metav1.Condition{
				Type:               string(hyperv1.ValidReleaseImage),
				Status:             metav1.ConditionTrue,
				ObservedGeneration: 3,
				Reason:             releaseImageValidationSkippedReason,
			},
			expected: true,
		},
		{
			name: "When the skip annotation is added after a valid condition, it should validate",
			hcluster: &hyperv1.HostedCluster{
				ObjectMeta: metav1.ObjectMeta{
					Generation: 3,
					Annotations: map[string]string{
						hyperv1.SkipReleaseImageValidation: "true",
					},
				},
			},
			condition: &metav1.Condition{
				Type:               string(hyperv1.ValidReleaseImage),
				Status:             metav1.ConditionTrue,
				ObservedGeneration: 3,
				Reason:             hyperv1.AsExpectedReason,
			},
			expected: true,
		},
		{
			name: "When the generation and validation inputs are unchanged, it should not validate",
			hcluster: &hyperv1.HostedCluster{
				ObjectMeta: metav1.ObjectMeta{Generation: 3},
			},
			condition: &metav1.Condition{
				Type:               string(hyperv1.ValidReleaseImage),
				Status:             metav1.ConditionTrue,
				ObservedGeneration: 3,
				Reason:             hyperv1.AsExpectedReason,
			},
			expected: false,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			if actual := shouldValidateReleaseImage(tc.hcluster, tc.condition); actual != tc.expected {
				t.Fatalf("expected shouldValidateReleaseImage to return %t, got %t", tc.expected, actual)
			}
		})
	}
}
