package main

import (
	"testing"

	. "github.com/onsi/gomega"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"

	"k8s.io/apimachinery/pkg/runtime/schema"
)

func TestResolveNodeClassGroupKind(t *testing.T) {
	azureGroupKind := schema.GroupKind{
		Group: "karpenter.hypershift.openshift.io",
		Kind:  "OpenShiftAzureNodeClass",
	}
	awsGroupKind := schema.GroupKind{
		Group: "karpenter.hypershift.openshift.io",
		Kind:  "OpenshiftEC2NodeClass",
	}
	testCases := map[string]struct {
		platform          hyperv1.PlatformType
		expectedGroupKind schema.GroupKind
		expectedError     string
	}{
		"When the platform is Azure, it should return the Azure NodeClass group and kind": {
			platform:          hyperv1.AzurePlatform,
			expectedGroupKind: azureGroupKind,
		},
		"When the platform is AWS, it should return the AWS NodeClass group and kind": {
			platform:          hyperv1.AWSPlatform,
			expectedGroupKind: awsGroupKind,
		},
		"When the platform is unknown, it should return an unsupported-platform error": {
			platform:          hyperv1.PlatformType("Unknown"),
			expectedGroupKind: schema.GroupKind{},
			expectedError:     `unsupported platform "Unknown"`,
		},
		"When the platform is empty, it should return an unsupported-platform error": {
			expectedGroupKind: schema.GroupKind{},
			expectedError:     `unsupported platform ""`,
		},
		"When the platform is OpenStack, it should return an unsupported-platform error": {
			platform:          hyperv1.OpenStackPlatform,
			expectedGroupKind: schema.GroupKind{},
			expectedError:     `unsupported platform "OpenStack"`,
		},
	}

	for name, tc := range testCases {
		t.Run(name, func(t *testing.T) {
			g := NewWithT(t)

			groupKind, err := resolveNodeClassGroupKind(tc.platform)
			g.Expect(groupKind).To(Equal(tc.expectedGroupKind))
			if tc.expectedError != "" {
				g.Expect(err).To(MatchError(tc.expectedError))
			} else {
				g.Expect(err).NotTo(HaveOccurred())
			}
		})
	}
}
