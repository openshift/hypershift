package kubevirt

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/google/go-cmp/cmp"
	. "github.com/onsi/gomega"
	"github.com/openshift/hypershift/cmd/cluster/core"
	"github.com/openshift/hypershift/support/testutil"
	"github.com/openshift/hypershift/test/integration/framework"
	"github.com/spf13/pflag"
)

func TestRawCreateOptions_Validate(t *testing.T) {
	for _, test := range []struct {
		name          string
		input         RawCreateOptions
		expectedError string
	}{
		{
			name: "unsupported publishing strategy",
			input: RawCreateOptions{
				ServicePublishingStrategy: "whatever",
			},
			expectedError: "service publish strategy whatever is not supported, supported options: Ingress, NodePort",
		},
		{
			name: "api server address invalid for ingress",
			input: RawCreateOptions{
				ServicePublishingStrategy: IngressServicePublishingStrategy,
				APIServerAddress:          "whatever",
			},
			expectedError: "external-api-server-address is supported only for NodePort service publishing strategy, service publishing strategy Ingress is used",
		},
		{
			name: "invalid infra storage class mappings",
			input: RawCreateOptions{
				ServicePublishingStrategy: IngressServicePublishingStrategy,
				InfraStorageClassMappings: []string{"bad"},
			},
			expectedError: "invalid infra storageclass mapping [bad]",
		},
		{
			name: "kubeconfig present without namespace",
			input: RawCreateOptions{
				ServicePublishingStrategy: IngressServicePublishingStrategy,
				InfraKubeConfigFile:       "something",
			},
			expectedError: "external infra cluster kubeconfig was provided but an infra namespace is missing",
		},
		{
			name: "kubeconfig missing with namespace",
			input: RawCreateOptions{
				ServicePublishingStrategy: IngressServicePublishingStrategy,
				InfraNamespace:            "something",
			},
			expectedError: "external infra cluster namespace was provided but a kubeconfig is missing",
		},
	} {
		var errString string
		if _, err := test.input.Validate(context.Background(), nil); err != nil {
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

func TestCreateCluster(t *testing.T) {
	ctx := framework.InterruptableContext(context.Background())

	for _, testCase := range []struct {
		name string
		args []string
	}{
		{
			name: "minimal flags necessary to render",
			args: []string{},
		},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			flags := pflag.NewFlagSet(testCase.name, pflag.ContinueOnError)
			coreOpts := core.DefaultOptions()
			core.BindDeveloperOptions(coreOpts, flags)
			kubevirtOpts := DefaultOptions()
			BindDeveloperOptions(kubevirtOpts, flags)
			if err := flags.Parse(testCase.args); err != nil {
				t.Fatalf("failed to parse flags: %v", err)
			}

			tempDir := t.TempDir()
			manifestsFile := filepath.Join(tempDir, "manifests.yaml")
			coreOpts.Render = true
			coreOpts.RenderInto = manifestsFile

			if err := core.CreateCluster(ctx, coreOpts, kubevirtOpts); err != nil {
				t.Fatalf("failed to create cluster: %v", err)
			}

			manifests, err := os.ReadFile(manifestsFile)
			if err != nil {
				t.Fatalf("failed to read manifests file: %v", err)
			}
			testutil.CompareWithFixture(t, manifests)
		})
	}
}
