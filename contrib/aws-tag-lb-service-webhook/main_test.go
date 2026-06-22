package main

import (
	"testing"

	"github.com/google/go-cmp/cmp"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func TestPatchAnnotation(t *testing.T) {
	for _, testCase := range []struct {
		name          string
		input         *corev1.Service
		expected      string
		expectedError bool
	}{
		{
			name: "no existing annotations",
			input: &corev1.Service{
				ObjectMeta: metav1.ObjectMeta{
					Annotations: map[string]string{},
				},
			},
			expected: `[{"op":"add","path":"/metadata/annotations","value":{"service.beta.kubernetes.io/aws-load-balancer-additional-resource-tags":"red-hat-managed=true"}}]`,
		},
		{
			name: "existing unrelated annotations",
			input: &corev1.Service{
				ObjectMeta: metav1.ObjectMeta{
					Annotations: map[string]string{
						"foo": "bar",
					},
				},
			},
			expected: `[{"op":"add","path":"/metadata/annotations/service.beta.kubernetes.io~1aws-load-balancer-additional-resource-tags","value":"red-hat-managed=true"}]`,
		},
		{
			name: "existing tag annotations needing update",
			input: &corev1.Service{
				ObjectMeta: metav1.ObjectMeta{
					Annotations: map[string]string{
						"service.beta.kubernetes.io/aws-load-balancer-additional-resource-tags": "other=unrelated",
					},
				},
			},
			expected: `[{"op":"replace","path":"/metadata/annotations/service.beta.kubernetes.io~1aws-load-balancer-additional-resource-tags","value":"other=unrelated,red-hat-managed=true"}]`,
		},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			got, err := patchAnnotation(testCase.input)
			if err == nil && testCase.expectedError {
				t.Fatalf("expected an error and got none")
			}
			if err != nil && !testCase.expectedError {
				t.Fatalf("expected no error and got one: %v", err)
			}

			if diff := cmp.Diff(string(got), testCase.expected); diff != "" {
				t.Fatalf("got incorrect patch: %v", diff)
			}
		})
	}
}
