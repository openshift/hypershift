//go:build e2ev2

package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	"github.com/openshift/hypershift/support/capabilities"
	"github.com/openshift/hypershift/support/imageregistry"
	"github.com/openshift/hypershift/support/releaseinfo/fake"
	"github.com/openshift/hypershift/support/thirdparty/library-go/pkg/image/dockerv1client"
	"github.com/openshift/hypershift/test/e2e/v2/lifecycle"

	configv1 "github.com/openshift/api/config/v1"
	"github.com/openshift/api/image/docker10"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/version"
	fakediscovery "k8s.io/client-go/discovery/fake"
	kubetesting "k8s.io/client-go/testing"

	crclient "sigs.k8s.io/controller-runtime/pkg/client"
	clientfake "sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"
)

type dnsImageProvider struct {
	imageregistry.ImageMetadataProvider
	labels map[string]string
	image  string
	err    error
}

func (provider *dnsImageProvider) ImageMetadata(_ context.Context, image string, _ []byte) (*dockerv1client.DockerImageConfig, error) {
	provider.image = image
	if provider.err != nil {
		return nil, provider.err
	}
	return &dockerv1client.DockerImageConfig{Config: &docker10.DockerConfig{Labels: provider.labels}}, nil
}

func TestManagedDNSArgs(t *testing.T) {
	t.Parallel()
	const payloadCPO = "quay.io/openshift-release-dev/ocp-v4.0-art-dev:hypershift"
	const overrideCPO = "registry.ci.openshift.org/ci/hypershift:pr"
	for _, testCase := range []struct {
		name          string
		version       string
		labels        map[string]string
		extraArgs     []string
		metadataError error
		wantEnabled   bool
		wantError     bool
		wantImage     string
	}{
		{name: "When the 4.22 CPO has no capability label, it should leave managed DNS disabled", version: "4.22.0", wantImage: payloadCPO},
		{name: "When the 5.0 CPO has no capability label, it should leave managed DNS disabled", version: "5.0.0", wantImage: payloadCPO},
		{name: "When a new CPO has unknown capabilities, it should not infer support from its version", version: "5.1.0", wantImage: payloadCPO},
		{name: "When a CPO explicitly disables the capability, it should leave managed DNS disabled", version: "5.1.0", labels: map[string]string{managesAWSDNSLabel: "false"}, wantImage: payloadCPO},
		{name: "When a CPO advertises managed DNS, it should preserve the opt-in without requiring cluster status", version: "5.1.0", labels: map[string]string{managesAWSDNSLabel: "true"}, wantEnabled: true, wantImage: payloadCPO},
		{name: "When an older version advertises a backported capability, it should preserve the opt-in", version: "4.22.0", labels: map[string]string{managesAWSDNSLabel: "true"}, wantEnabled: true, wantImage: payloadCPO},
		{name: "When a CPO image override is set, it should inspect that image instead of the payload", version: "4.22.0", extraArgs: []string{"--control-plane-operator-image=" + overrideCPO}, labels: map[string]string{managesAWSDNSLabel: "true"}, wantEnabled: true, wantImage: overrideCPO},
		{name: "When a CPO annotation override is set, it should inspect that image instead of the payload", version: "4.22.0", extraArgs: []string{"--annotations=" + hyperv1.ControlPlaneOperatorImageAnnotation + "=" + overrideCPO}, wantImage: overrideCPO},
		{name: "When image labels are annotated, it should use the same override as the operator", version: "4.22.0", extraArgs: []string{"--annotations=" + hyperv1.ControlPlaneOperatorImageLabelsAnnotation + "=" + managesAWSDNSLabel + "=true"}, wantEnabled: true},
		{name: "When metadata lookup fails, it should fail rather than silently skip coverage", version: "5.1.0", metadataError: fmt.Errorf("registry unavailable"), wantError: true, wantImage: payloadCPO},
		{name: "When release metadata is invalid, it should fail rather than silently skip coverage", version: "unknown", wantError: true},
		{name: "When an annotation is invalid, it should fail rather than ignore the override", version: "5.1.0", extraArgs: []string{"--annotations=invalid"}, wantError: true},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()
			args := append([]string{"create", "cluster", "aws", "--release-image=quay.io/openshift-release-dev/ocp-release:" + testCase.version + "-x86_64", "--managed-dns", "--public-only", "--feature-set=TechPreviewNoUpgrade"}, testCase.extraArgs...)
			pullSecretPath := filepath.Join(t.TempDir(), "pull-secret.json")
			if err := os.WriteFile(pullSecretPath, []byte(`{}`), 0600); err != nil {
				t.Fatal(err)
			}
			args = append(args, "--pull-secret="+pullSecretPath)
			original := slices.Clone(args)
			images := &dnsImageProvider{labels: testCase.labels, err: testCase.metadataError}
			releases := &fake.FakeReleaseProvider{Version: testCase.version, Components: map[string]string{"hypershift": payloadCPO}}
			actual, err := managedDNSArgs(t.Context(), args, releases, images)
			if (err != nil) != testCase.wantError {
				t.Fatalf("error = %v, wantError = %v", err, testCase.wantError)
			}
			if images.image != testCase.wantImage {
				t.Errorf("inspected image = %q, want %q", images.image, testCase.wantImage)
			}
			if testCase.wantError {
				return
			}
			if slices.Contains(actual, "--managed-dns") != testCase.wantEnabled {
				t.Errorf("args = %v, want managed DNS = %v", actual, testCase.wantEnabled)
			}
			if !reflect.DeepEqual(args, original) {
				t.Fatal("input arguments were mutated")
			}
			if !slices.Contains(actual, "--public-only") || !slices.Contains(actual, "--feature-set=TechPreviewNoUpgrade") {
				t.Errorf("unrelated fixture arguments were removed: %v", actual)
			}
		})
	}
	t.Run("When managed DNS is not requested, it should preserve arguments without looking up images", func(t *testing.T) {
		t.Parallel()
		args := []string{"create", "cluster", "aws", "--managed-dns=false"}
		actual, err := managedDNSArgs(t.Context(), args, nil, nil)
		if err != nil || !reflect.DeepEqual(actual, args) {
			t.Fatalf("args = %v, error = %v", actual, err)
		}
	})
}

func TestCreateClustersParallel(t *testing.T) {
	t.Parallel()
	for _, testCase := range []struct {
		name        string
		variant     string
		label       string
		missingAuth bool
		wantEnabled bool
		wantError   bool
	}{
		{name: "When the AWS CPO lacks managed DNS, it should launch the baseline fixture", variant: "managed-dns", label: managesAWSDNSLabel + "=false"},
		{name: "When the AWS CPO supports managed DNS, it should launch with the opt-in and effective pull secret", variant: "managed-dns", label: managesAWSDNSLabel + "=true", wantEnabled: true},
		{name: "When capability preparation fails, it should not launch the CLI", variant: "managed-dns", label: "invalid", wantError: true},
		{name: "When the effective pull secret is missing, it should not launch the CLI", variant: "managed-dns", label: managesAWSDNSLabel + "=true", missingAuth: true, wantError: true},
		{name: "When another AWS fixture is selected, it should not require capability lookup", variant: "public", missingAuth: true},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()
			directory := t.TempDir()
			recordPath := filepath.Join(directory, "arguments")
			binaryPath := filepath.Join(directory, "hypershift")
			if err := os.WriteFile(binaryPath, []byte(fmt.Sprintf("#!/bin/sh\nprintf '%%s\\n' \"$@\" > %q\n", recordPath)), 0700); err != nil {
				t.Fatal(err)
			}
			pullSecretPath := filepath.Join(directory, "auth.json")
			if !testCase.missingAuth {
				if err := os.WriteFile(pullSecretPath, []byte(`{}`), 0600); err != nil {
					t.Fatal(err)
				}
			}
			cfg := envConfig{platform: &lifecycle.AWSPlatformConfig{}, hypershiftBinary: binaryPath, pullSecret: filepath.Join(directory, "unused-default.json")}
			args := []string{"--pull-secret=" + pullSecretPath}
			if testCase.variant == "managed-dns" {
				args = append(args, "--managed-dns", "--control-plane-operator-image=registry.ci.openshift.org/ci/hypershift:pr", "--annotations="+hyperv1.ControlPlaneOperatorImageLabelsAnnotation+"="+testCase.label)
			}
			results := createClustersParallel(t.Context(), cfg, []namedSpec{{ClusterSpec: lifecycle.ClusterSpec{Variant: testCase.variant, ExtraArgs: args}, name: "dns-fixture"}})
			if (results[testCase.variant] != nil) != testCase.wantError {
				t.Fatalf("results = %v, wantError = %v", results, testCase.wantError)
			}
			contents, err := os.ReadFile(recordPath)
			if testCase.wantError {
				if !os.IsNotExist(err) {
					t.Fatalf("CLI launched after preparation error: %v", err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if slices.Contains(strings.Split(string(contents), "\n"), "--managed-dns") != testCase.wantEnabled {
				t.Fatalf("unexpected CLI arguments: %s", contents)
			}
		})
	}
}

type dnsDiscoveryClient struct {
	capabilities.ManagementClusterDiscoveryClient
	err error
}

func (client dnsDiscoveryClient) ServerResourcesForGroupVersion(groupVersion string) (*metav1.APIResourceList, error) {
	if client.err != nil {
		return nil, client.err
	}
	return client.ManagementClusterDiscoveryClient.ServerResourcesForGroupVersion(groupVersion)
}

func TestManagedDNSRegistryProvider(t *testing.T) {
	t.Parallel()
	for _, testCase := range []struct {
		name      string
		mirrored  bool
		err       error
		listError bool
		wantError bool
	}{
		{name: "When management mirrors are absent, it should configure direct registry providers"},
		{name: "When management mirrors are configured, it should configure both providers to resolve mirror-only images", mirrored: true},
		{name: "When management discovery fails, it should return an error instead of bypassing mirrors", err: fmt.Errorf("discovery unavailable"), wantError: true},
		{name: "When mirror configuration cannot be read, it should return an error instead of bypassing mirrors", mirrored: true, listError: true, wantError: true},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()
			discoveryClient := &fakediscovery.FakeDiscovery{Fake: &kubetesting.Fake{}, FakedServerVersion: &version.Info{Major: "1", Minor: "35", GitVersion: "v1.35.0"}}
			cl := clientfake.NewClientBuilder().WithScheme(scheme)
			if testCase.mirrored {
				discoveryClient.Resources = []*metav1.APIResourceList{{GroupVersion: configv1.GroupVersion.String(), APIResources: []metav1.APIResource{{Name: "imagedigestmirrorsets"}}}}
				cl = cl.WithObjects(&configv1.ImageDigestMirrorSet{ObjectMeta: metav1.ObjectMeta{Name: "ci-mirror"}, Spec: configv1.ImageDigestMirrorSetSpec{ImageDigestMirrors: []configv1.ImageDigestMirrors{{Source: "quay.io/openshift-release-dev", Mirrors: []configv1.ImageMirror{"registry.ci.openshift.org/mirror"}}}}})
			}
			if testCase.listError {
				cl = cl.WithInterceptorFuncs(interceptor.Funcs{List: func(context.Context, crclient.WithWatch, crclient.ObjectList, ...crclient.ListOption) error {
					return fmt.Errorf("mirror list unavailable")
				}})
			}
			provider, err := managedDNSRegistryProvider(t.Context(), cl.Build(), dnsDiscoveryClient{ManagementClusterDiscoveryClient: discoveryClient, err: testCase.err})
			if (err != nil) != testCase.wantError {
				t.Fatalf("error = %v, wantError = %v", err, testCase.wantError)
			}
			if testCase.wantError {
				return
			}
			var want map[string][]string
			if testCase.mirrored {
				want = map[string][]string{"quay.io/openshift-release-dev": {"registry.ci.openshift.org/mirror"}}
			}
			if !reflect.DeepEqual(provider.ReleaseProvider.GetOpenShiftImageRegistryOverrides(), want) || !reflect.DeepEqual(provider.MetadataProvider.OpenShiftImageRegistryOverrides, want) {
				t.Fatalf("release and metadata providers did not retain mirrors: %+v", provider)
			}
		})
	}
}
