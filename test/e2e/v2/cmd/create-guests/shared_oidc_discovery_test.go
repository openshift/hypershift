//go:build e2ev2

package main

import (
	"context"
	"errors"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/openshift/hypershift/test/e2e/v2/lifecycle"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"

	crclient "sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

type sharedOIDCDiscoveryReader struct {
	crclient.Reader
	calls int
	err   error
}

func (reader *sharedOIDCDiscoveryReader) Get(ctx context.Context, key crclient.ObjectKey, object crclient.Object, options ...crclient.GetOption) error {
	reader.calls++
	if key != (crclient.ObjectKey{Namespace: "kube-public", Name: "oidc-storage-provider-s3-config"}) {
		return errors.New("unexpected OIDC discovery object")
	}
	if reader.err != nil {
		return reader.err
	}
	return reader.Reader.Get(ctx, key, object, options...)
}

func TestPrepareAutomaticSharedOIDCManagementConfiguration(t *testing.T) {
	for _, testCase := range []struct {
		name, bucket, region, iamRegion, iamCredentials, s3Credentials string
		data                                                           map[string]string
		readError                                                      error
		external, nonAWS                                               bool
		wantReads                                                      int
		wantError                                                      string
	}{
		{name: "When CI supplies no bucket, it should discover the installed bucket and its independent region", data: map[string]string{"name": "installed-bucket", "region": "us-west-2"}, wantReads: 1},
		{name: "When IAM credentials and region differ, discovery should preserve their authority", data: map[string]string{"name": "installed-bucket", "region": "us-west-2"}, iamRegion: "us-east-2", iamCredentials: "/fixtures/iam-profile", wantReads: 1},
		{name: "When S3 credentials are explicit, discovery should preserve them", data: map[string]string{"name": "installed-bucket", "region": "us-west-2"}, s3Credentials: "/fixtures/s3-profile", wantReads: 1},
		{name: "When explicit bucket and region exist, it should not require management discovery", bucket: "explicit-bucket", region: "eu-west-1"},
		{name: "When explicit bucket uses IAM region fallback, it should preserve that configuration", bucket: "explicit-bucket", iamRegion: "us-east-2"},
		{name: "When only S3 region is explicit, it should discover the authoritative bucket region pair", region: "eu-west-1", data: map[string]string{"name": "installed-bucket", "region": "us-west-2"}, wantReads: 1},
		{name: "When the management configuration is missing, it should fail before cloud setup", wantReads: 1, wantError: "not found"},
		{name: "When the management bucket is absent, it should reject incomplete configuration", data: map[string]string{"region": "us-west-2"}, wantReads: 1, wantError: "name and region"},
		{name: "When the management region is absent, it should reject incomplete configuration", data: map[string]string{"name": "installed-bucket"}, wantReads: 1, wantError: "name and region"},
		{name: "When discovery is forbidden, it should retain the error instead of provisioning", readError: errors.New("forbidden discovery"), wantReads: 1, wantError: "forbidden discovery"},
		{name: "When discovery is canceled, it should retain cancellation instead of provisioning", readError: context.Canceled, wantReads: 1, wantError: "context canceled"},
		{name: "When an explicit issuer and key exist, it should not discover or acquire resources", external: true},
		{name: "When the platform is not AWS, it should not discover AWS configuration", nonAWS: true},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			for name, value := range map[string]string{
				"HYPERSHIFT_PLATFORM": "aws", "SHARED_DIR": t.TempDir(), "ARTIFACT_DIR": t.TempDir(), "PROW_JOB_ID": "12345",
				"RELEASE_IMAGE_LATEST": "release", "AWS_OIDC_S3_BUCKET_NAME": testCase.bucket, "AWS_OIDC_S3_REGION": testCase.region,
				"HYPERSHIFT_AWS_REGION": testCase.iamRegion, "AWS_GUEST_INFRA_CREDENTIALS_FILE": testCase.iamCredentials,
				"AWS_OIDC_S3_CREDENTIALS_FILE": testCase.s3Credentials, "AWS_SHARED_CREDENTIALS_FILE": "/fixtures/default-profile",
				"E2E_AWS_SHARED_OIDC_ISSUER_URL": "", "E2E_AWS_SHARED_OIDC_SIGNING_KEY_FILE": "",
			} {
				t.Setenv(name, value)
			}
			cfg := loadEnvConfig()
			keyPath, _ := sharedOIDCTestKey(t)
			if testCase.external {
				cfg.sharedOIDCIssuer, cfg.sharedOIDCKeyFile = "https://external.example.org/suite", keyPath
			}
			if testCase.nonAWS {
				cfg.platform = lifecycle.NewAzurePlatformConfig(cfg.sharedDir)
			}
			scheme := runtime.NewScheme()
			if err := corev1.AddToScheme(scheme); err != nil {
				t.Fatal(err)
			}
			builder := fake.NewClientBuilder().WithScheme(scheme)
			if testCase.data != nil {
				builder.WithObjects(&corev1.ConfigMap{ObjectMeta: metav1.ObjectMeta{Namespace: "kube-public", Name: "oidc-storage-provider-s3-config"}, Data: testCase.data})
			}
			reader := &sharedOIDCDiscoveryReader{Reader: builder.Build(), err: testCase.readError}
			called := false
			configured, specs, err := prepareAutomaticSharedOIDC(t.Context(), cfg, []lifecycle.ClusterSpec{{Variant: "public"}}, reader, func(ctx context.Context, directory string, options lifecycle.SharedOIDCOptions) (lifecycle.SharedOIDCInputs, error) {
				called = true
				want := cfg.sharedOIDCOptions
				if want.Bucket == "" && testCase.data != nil {
					want.Bucket, want.S3Region = testCase.data["name"], testCase.data["region"]
				}
				if !reflect.DeepEqual(options, want) || ctx != t.Context() || filepath.Clean(directory) != filepath.Clean(cfg.sharedDir) {
					t.Fatalf("automatic setup options=%+v, want %+v", options, want)
				}
				return lifecycle.SharedOIDCInputs{IssuerURL: "https://oidc.example.org/suite", SigningKeyFile: keyPath}, nil
			})
			if reader.calls != testCase.wantReads || (err != nil) != (testCase.wantError != "") {
				t.Fatalf("discovery reads=%d error=%v; want reads=%d error=%q", reader.calls, err, testCase.wantReads, testCase.wantError)
			}
			if testCase.wantError != "" {
				if called || !strings.Contains(err.Error(), testCase.wantError) || (testCase.readError != nil && !errors.Is(err, testCase.readError)) {
					t.Fatalf("cloud setup=%v discovery error=%v", called, err)
				}
				return
			}
			if called != (!testCase.external && !testCase.nonAWS) || len(specs) != 1 || (called && configured.sharedOIDCKeyFile != keyPath) {
				t.Fatalf("setup=%v configured key=%q specs=%v", called, configured.sharedOIDCKeyFile, specs)
			}
		})
	}
}
