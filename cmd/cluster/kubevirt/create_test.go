package kubevirt

import (
	"context"
	"testing"

	"github.com/google/go-cmp/cmp"
	. "github.com/onsi/gomega"
)

func TestCreateOptions_Validate(t *testing.T) {
	for _, test := range []struct {
		name          string
		input         CreateOptions
		expectedError string
	}{
		{
			name: "unsupported publishing strategy",
			input: CreateOptions{
				ServicePublishingStrategy: "whatever",
			},
			expectedError: "service publish strategy whatever is not supported, supported options: Ingress, NodePort",
		},
		{
			name: "api server address invalid for ingress",
			input: CreateOptions{
				ServicePublishingStrategy: IngressServicePublishingStrategy,
				APIServerAddress:          "whatever",
			},
			expectedError: "external-api-server-address is supported only for NodePort service publishing strategy, service publishing strategy Ingress is used",
		},
		{
			name: "invalid infra storage class mappings",
			input: CreateOptions{
				ServicePublishingStrategy: IngressServicePublishingStrategy,
				InfraStorageClassMappings: []string{"bad"},
			},
			expectedError: "invalid infra storageclass mapping [bad]",
		},
		{
			name: "kubeconfig present without namespace",
			input: CreateOptions{
				ServicePublishingStrategy: IngressServicePublishingStrategy,
				InfraKubeConfigFile:       "something",
			},
			expectedError: "external infra cluster kubeconfig was provided but an infra namespace is missing",
		},
		{
			name: "kubeconfig missing with namespace",
			input: CreateOptions{
				ServicePublishingStrategy: IngressServicePublishingStrategy,
				InfraNamespace:            "something",
			},
			expectedError: "external infra cluster namespace was provided but a kubeconfig is missing",
		},
	} {
		var errString string
		if err := test.input.Validate(context.Background(), nil); err != nil {
			errString = err.Error()
		}
		if diff := cmp.Diff(test.expectedError, errString); diff != "" {
			t.Errorf("got incorrect error: %v", diff)
		}
	}
}

func TestParseTenantClassString(t *testing.T) {
	testsCases := []struct {
		name          string
		optionString  string
		expectedName  string
		expectedGroup string
	}{
		{
			name:          "straight class name, no options",
			optionString:  "tenant1",
			expectedName:  "tenant1",
			expectedGroup: "",
		},
		{
			name:          "class name with group option",
			optionString:  "tenant1,group=group1",
			expectedName:  "tenant1",
			expectedGroup: "group1",
		},
		{
			name:          "ignore invalid option",
			optionString:  "tenant1,invalid=invalid",
			expectedName:  "tenant1",
			expectedGroup: "",
		},
		{
			name:          "class name with group option, and ignore invalid options",
			optionString:  "tenant1, group=group1,invalid=invalid",
			expectedName:  "tenant1",
			expectedGroup: "group1",
		},
	}
	for _, tc := range testsCases {
		t.Run(tc.name, func(t *testing.T) {
			g := NewWithT(t)
			res, options := parseTenantClassString(tc.optionString)
			g.Expect(res).To(Equal(tc.expectedName))
			g.Expect(options).To(Equal(tc.expectedGroup))
		})
	}
}
