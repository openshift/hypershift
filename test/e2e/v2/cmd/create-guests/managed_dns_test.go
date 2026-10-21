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

	"github.com/openshift/hypershift/support/releaseinfo"
	"github.com/openshift/hypershift/support/releaseinfo/fake"
	"github.com/openshift/hypershift/test/e2e/v2/lifecycle"
)

type dnsReleaseProvider struct {
	releaseinfo.Provider
	lookup func(context.Context, string, []byte) (*releaseinfo.ReleaseImage, error)
}

func (provider dnsReleaseProvider) Lookup(ctx context.Context, image string, pullSecret []byte) (*releaseinfo.ReleaseImage, error) {
	return provider.lookup(ctx, image, pullSecret)
}

func TestManagedDNSArgs(t *testing.T) {
	t.Parallel()
	for _, testCase := range []struct {
		name        string
		version     string
		extraArgs   []string
		wantEnabled bool
		wantError   bool
	}{
		{name: "When the release is 4.22, it should create a baseline fixture", version: "4.22.0"},
		{name: "When the release is a 4.22 nightly, it should create a baseline fixture", version: "4.22.0-0.nightly-2026-10-20-170000"},
		{name: "When the release is 5.0, it should create a baseline fixture", version: "5.0.7"},
		{name: "When the release is a 5.0 nightly, it should create a baseline fixture", version: "5.0.0-0.nightly-2026-10-20-170000"},
		{name: "When managed DNS uses an explicit true flag on 5.0, it should remove both opt-ins", version: "5.0.0", extraArgs: []string{"--managed-dns=true"}},
		{name: "When the release is 5.1, it should preserve managed DNS", version: "5.1.0", wantEnabled: true},
		{name: "When the release is a 5.1 nightly, it should preserve managed DNS", version: "5.1.0-0.nightly-2026-10-20-170000", wantEnabled: true},
		{name: "When the release is a 5.1 prerelease, it should preserve managed DNS", version: "5.1.0-rc.1", wantEnabled: true},
		{name: "When the release has build metadata, it should preserve managed DNS", version: "5.1.2+build.1", wantEnabled: true},
		{name: "When the release is later than 5.1, it should preserve managed DNS", version: "5.2.0-0.nightly-2026-10-20-170000", wantEnabled: true},
		{name: "When a newer CPO overrides an old payload, it should still use the payload boundary", version: "4.22.0", extraArgs: []string{"--control-plane-operator-image=registry.ci.openshift.org/ci/hypershift:new"}},
		{name: "When a CPO annotation overrides a supported payload, it should preserve the override and assertions", version: "5.1.0", extraArgs: []string{"--annotations=hypershift.openshift.io/control-plane-operator-image=registry.ci.openshift.org/ci/hypershift:old"}, wantEnabled: true},
		{name: "When a repeated release flag selects 5.0, it should gate the effective release", version: "5.0.0", extraArgs: []string{"--release-image=effective-release"}},
		{name: "When managed DNS is explicitly disabled, it should preserve arguments without metadata", extraArgs: []string{"--managed-dns=false"}},
		{name: "When the release version is invalid, it should fail preparation", version: "invalid", wantError: true},
		{name: "When the release version is empty, it should fail preparation", wantError: true},
		{name: "When the managed DNS flag is invalid, it should fail preparation", extraArgs: []string{"--managed-dns=invalid"}, wantError: true},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()
			pullSecretPath := filepath.Join(t.TempDir(), "auth.json")
			if err := os.WriteFile(pullSecretPath, []byte(`{}`), 0600); err != nil {
				t.Fatal(err)
			}
			args := append([]string{"create", "cluster", "aws", "--release-image=default-release", "--managed-dns", "--public-only", "--pull-secret=unused"}, testCase.extraArgs...)
			args = append(args, "--pull-secret="+pullSecretPath)
			original := slices.Clone(args)
			provider := dnsReleaseProvider{lookup: func(ctx context.Context, image string, auth []byte) (*releaseinfo.ReleaseImage, error) {
				if slices.Contains(testCase.extraArgs, "--managed-dns=false") {
					t.Fatal("disabled fixture looked up release metadata")
				}
				wantImage := "default-release"
				if slices.Contains(testCase.extraArgs, "--release-image=effective-release") {
					wantImage = "effective-release"
				}
				if image != wantImage || string(auth) != `{}` {
					t.Fatal("release lookup did not use effective image and pull secret")
				}
				release, err := (&fake.FakeReleaseProvider{Version: "5.1.0"}).Lookup(ctx, image, auth)
				if err == nil {
					release.ImageStream.Name = testCase.version
				}
				return release, err
			}}
			actual, err := managedDNSArgs(t.Context(), args, provider)
			if (err != nil) != testCase.wantError {
				t.Fatalf("error = %v, wantError = %v", err, testCase.wantError)
			}
			if testCase.wantError {
				return
			}
			if slices.Contains(testCase.extraArgs, "--managed-dns=false") {
				if !reflect.DeepEqual(actual, args) {
					t.Fatal("disabled arguments changed")
				}
				return
			}
			want := slices.Clone(args)
			if !testCase.wantEnabled {
				want = slices.DeleteFunc(want, func(arg string) bool { return arg == "--managed-dns" || strings.HasPrefix(arg, "--managed-dns=") })
			}
			if !reflect.DeepEqual(actual, want) || !reflect.DeepEqual(args, original) {
				t.Fatalf("args = %v, want %v; original mutated = %v", actual, want, !reflect.DeepEqual(args, original))
			}
		})
	}
}

func TestCreateClustersParallel(t *testing.T) {
	t.Parallel()
	for _, testCase := range []struct {
		name        string
		version     string
		variant     string
		missingAuth bool
		lookupError bool
		missingData bool
		wantEnabled bool
		wantError   bool
	}{
		{name: "When the payload is 4.22, it should launch baseline DNS", variant: "managed-dns", version: "4.22.0"},
		{name: "When the payload is 5.0, it should launch baseline DNS", variant: "managed-dns", version: "5.0.0"},
		{name: "When the payload is a 5.1 nightly, it should launch managed DNS", variant: "managed-dns", version: "5.1.0-0.nightly-2026-10-20-170000", wantEnabled: true},
		{name: "When release discovery fails, it should not launch the CLI", variant: "managed-dns", lookupError: true, wantError: true},
		{name: "When release metadata is missing, it should not launch the CLI", variant: "managed-dns", missingData: true, wantError: true},
		{name: "When the version is invalid, it should not launch the CLI", variant: "managed-dns", version: "invalid", wantError: true},
		{name: "When effective authentication is missing, it should not launch the CLI", variant: "managed-dns", missingAuth: true, wantError: true},
		{name: "When another fixture is selected, it should not look up release metadata", variant: "public", missingAuth: true},
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
			provider := dnsReleaseProvider{lookup: func(ctx context.Context, image string, auth []byte) (*releaseinfo.ReleaseImage, error) {
				if testCase.variant != "managed-dns" {
					t.Error("unrelated fixture looked up release metadata")
				}
				if testCase.lookupError {
					return nil, fmt.Errorf("release unavailable")
				}
				if testCase.missingData {
					return nil, nil
				}
				return (&fake.FakeReleaseProvider{Version: testCase.version}).Lookup(ctx, image, auth)
			}}
			cfg := envConfig{platform: &lifecycle.AWSPlatformConfig{}, hypershiftBinary: binaryPath, releaseProvider: provider, pullSecret: filepath.Join(directory, "unused-default.json")}
			args := []string{"--pull-secret=" + pullSecretPath}
			if testCase.variant == "managed-dns" {
				args = append(args, "--managed-dns")
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
