package util

import (
	. "github.com/onsi/gomega"
	hyperv1 "github.com/openshift/hypershift/api/v1alpha1"
	"testing"
)

func TestIsCPOCompatibleWithHCP(t *testing.T) {
	testImage := "myimage:1"
	tests := map[string]struct {
		inputAnnotations map[string]string
		inputActualImage string
		expectedResult   bool
	}{
		"when no annotations exist an incompatible result (false) is returned": {
			inputAnnotations: nil,
			inputActualImage: testImage,
			expectedResult:   false,
		},
		"when the cpo annotation does not match the actual image an incompatible result (false) is returned": {
			inputAnnotations: map[string]string{},
			inputActualImage: testImage,
			expectedResult:   false,
		},
		"when the cpo annotation matches the actual image a compatible result (true) is returned": {
			inputAnnotations: map[string]string{
				hyperv1.DesiredControlPlaneOperatorImageAnnotation: testImage,
			},
			inputActualImage: testImage,
			expectedResult:   true,
		},
	}
	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			g := NewGomegaWithT(t)
			isComptabile := IsCPOCompatibleWithHCP(test.inputAnnotations, test.inputActualImage)
			g.Expect(isComptabile).To(Equal(test.expectedResult))
		})
	}
}
