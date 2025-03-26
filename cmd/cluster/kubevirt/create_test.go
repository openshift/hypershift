package kubevirt

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	. "github.com/onsi/gomega"

	"github.com/openshift/hypershift/cmd/cluster/core"
	"github.com/openshift/hypershift/support/certs"
	"github.com/openshift/hypershift/support/testutil"
	"github.com/openshift/hypershift/test/integration/framework"

	utilrand "k8s.io/apimachinery/pkg/util/rand"

	"github.com/google/go-cmp/cmp"
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
	utilrand.Seed(1234567890)
	certs.UnsafeSeed(1234567890)
	ctx := framework.InterruptableContext(context.Background())
	t.Setenv("FAKE_CLIENT", "true")

	for _, testCase := range []struct {
		name string
		args []string
	}{
		{
			name: "minimal flags necessary to render",
			args: []string{
				"--render-sensitive",
			},
		},
		{
			name: "test from dvossel",
			args: []string{
				"--name", "test1",
				"--etcd-storage-class=gp3-csi",
				"--control-plane-availability-policy", "HighlyAvailable",
				"--infra-availability-policy", "HighlyAvailable",
				"--node-pool-replicas", "2",
				"--memory", "12Gi",
				"--cores", "4",
				"--release-image", "fake",
				"--root-volume-access-modes", "ReadWriteOnce",
				"--root-volume-storage-class", "gp3-csi",
				"--root-volume-size=32",
				"--infra-storage-class-mapping=gp3-csi/gp3",
				"--infra-storage-class-mapping=ocs-storagecluster-ceph-rbd/ceph-rbd",
				"--vm-node-selector", "key=val",
				"--additional-network", "name:ns1/nad-foo",
				"--additional-network", "name:ns2/nad-foo2",
				"--infra-volumesnapshot-class-mapping=ocs-storagecluster-rbd-snap/rdb-snap",
				"--toleration", "key=key1,value=value1,effect=noSchedule",
				"--toleration", "key=key2,value=value2,effect=noSchedule",
				"--render-sensitive",
			},
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
