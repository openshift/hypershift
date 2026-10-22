//go:build e2ev2

package lifecycle

import (
	"bytes"
	"context"
	"encoding/json"
	"encoding/pem"
	"errors"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	supportawsutil "github.com/openshift/hypershift/support/awsutil"
	"github.com/openshift/hypershift/support/certs"
	"github.com/openshift/hypershift/support/oidc"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/iam"
	iamtypes "github.com/aws/aws-sdk-go-v2/service/iam/types"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/smithy-go"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"

	crclient "sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	"github.com/onsi/gomega"
)

type sharedOIDCFake struct {
	events            []string
	objects           map[string][]byte
	buckets           []string
	provider          *iam.CreateOpenIDConnectProviderInput
	providerARN       string
	putFailure        int
	createError       error
	listError         error
	deleteError       error
	objectDeleteError error
	cancel            context.CancelFunc
	cleanupCanceled   bool
	configured        []SharedOIDCOptions
}

func (cloud *sharedOIDCFake) clients(_ context.Context, options SharedOIDCOptions) (sharedOIDCIAM, sharedOIDCS3, error) {
	cloud.configured = append(cloud.configured, options)
	return cloud, cloud, nil
}

func (cloud *sharedOIDCFake) PutObject(_ context.Context, input *s3.PutObjectInput, _ ...func(*s3.Options)) (*s3.PutObjectOutput, error) {
	cloud.events = append(cloud.events, "put:"+aws.ToString(input.Key))
	cloud.buckets = append(cloud.buckets, aws.ToString(input.Bucket))
	if cloud.objects == nil {
		cloud.objects = map[string][]byte{}
	}
	body, err := io.ReadAll(input.Body)
	if err != nil {
		return nil, err
	}
	cloud.objects[aws.ToString(input.Key)] = body
	if cloud.putFailure == len(cloud.buckets) {
		if cloud.cancel != nil {
			cloud.cancel()
		}
		return nil, errSharedOIDCFake
	}
	return &s3.PutObjectOutput{}, nil
}

var errSharedOIDCFake = errors.New("injected shared OIDC failure")

func (cloud *sharedOIDCFake) CreateOpenIDConnectProvider(_ context.Context, input *iam.CreateOpenIDConnectProviderInput, _ ...func(*iam.Options)) (*iam.CreateOpenIDConnectProviderOutput, error) {
	cloud.events = append(cloud.events, "create-provider")
	cloud.provider = input
	cloud.providerARN = "arn:aws:iam::123456789012:oidc-provider/" + strings.TrimPrefix(aws.ToString(input.Url), "https://")
	if cloud.createError != nil {
		if cloud.cancel != nil {
			cloud.cancel()
		}
		return nil, cloud.createError
	}
	return &iam.CreateOpenIDConnectProviderOutput{OpenIDConnectProviderArn: aws.String(cloud.providerARN)}, nil
}

func (cloud *sharedOIDCFake) ListOpenIDConnectProviders(ctx context.Context, _ *iam.ListOpenIDConnectProvidersInput, _ ...func(*iam.Options)) (*iam.ListOpenIDConnectProvidersOutput, error) {
	cloud.cleanupCanceled = cloud.cleanupCanceled || ctx.Err() != nil
	cloud.events = append(cloud.events, "list-providers")
	if cloud.listError != nil {
		return nil, cloud.listError
	}
	providers := []iamtypes.OpenIDConnectProviderListEntry{{Arn: aws.String("arn:aws:iam::123456789012:oidc-provider/external.example.org/retain")}}
	if cloud.providerARN != "" {
		providers = append(providers, iamtypes.OpenIDConnectProviderListEntry{Arn: aws.String(cloud.providerARN)})
	}
	return &iam.ListOpenIDConnectProvidersOutput{OpenIDConnectProviderList: providers}, nil
}

func (cloud *sharedOIDCFake) DeleteOpenIDConnectProvider(ctx context.Context, input *iam.DeleteOpenIDConnectProviderInput, _ ...func(*iam.Options)) (*iam.DeleteOpenIDConnectProviderOutput, error) {
	cloud.cleanupCanceled = cloud.cleanupCanceled || ctx.Err() != nil
	cloud.events = append(cloud.events, "delete-provider:"+aws.ToString(input.OpenIDConnectProviderArn))
	if cloud.deleteError != nil {
		return nil, cloud.deleteError
	}
	cloud.providerARN = ""
	return &iam.DeleteOpenIDConnectProviderOutput{}, nil
}

func (cloud *sharedOIDCFake) DeleteObject(ctx context.Context, input *s3.DeleteObjectInput, _ ...func(*s3.Options)) (*s3.DeleteObjectOutput, error) {
	cloud.cleanupCanceled = cloud.cleanupCanceled || ctx.Err() != nil
	cloud.events = append(cloud.events, "delete-object:"+aws.ToString(input.Key))
	if cloud.objectDeleteError != nil {
		return nil, cloud.objectDeleteError
	}
	delete(cloud.objects, aws.ToString(input.Key))
	return &s3.DeleteObjectOutput{}, nil
}

func sharedOIDCTestOptions() SharedOIDCOptions {
	return SharedOIDCOptions{Bucket: "hypershift-ci-oidc", IAMRegion: "us-east-1", IAMCredentialsFile: "/fixtures/iam-credentials", S3Region: "us-west-2", S3CredentialsFile: "/fixtures/s3-credentials", ProwJobID: "12345", AdditionalTags: []string{"expirationDate=2026-10-22T20:00:00Z", supportawsutil.HypershiftInfraIDTagKey + "=wrong", supportawsutil.HypershiftSourceTagKey + "=wrong"}}
}

func TestSetupSharedOIDC(t *testing.T) {
	t.Run("When suite inputs are valid, it should publish matching discovery and JWKS before a uniquely tagged provider", func(t *testing.T) {
		cloud := &sharedOIDCFake{}
		directory := t.TempDir()
		options := sharedOIDCTestOptions()
		inputs, err := setupSharedOIDC(t.Context(), directory, options, cloud.clients)
		if err != nil {
			t.Fatal(err)
		}
		keyData, err := os.ReadFile(inputs.SigningKeyFile)
		if err != nil {
			t.Fatal(err)
		}
		block, _ := pem.Decode(keyData)
		assertions := gomega.NewWithT(t)
		assertions.Expect(block).NotTo(gomega.BeNil(), "signing key is not PEM")
		assertions.Expect(block.Type).To(gomega.Equal("RSA PRIVATE KEY"), "signing key is not PKCS1")
		key, err := certs.PemToPrivateKey(keyData)
		if err != nil {
			t.Fatal(err)
		}
		if err := key.Validate(); err != nil {
			t.Fatal(err)
		}
		publicKey, err := certs.PublicKeyToPem(&key.PublicKey)
		if err != nil {
			t.Fatal(err)
		}
		providerID := filepath.Base(strings.TrimSuffix(inputs.SigningKeyFile, ".pem"))
		if inputs.IssuerURL != "https://hypershift-ci-oidc.s3.us-west-2.amazonaws.com/"+providerID || !strings.HasPrefix(providerID, "e2e-oidc-provider-") {
			t.Fatal("issuer identity/region differs")
		}
		params := oidc.OIDCGeneratorParams{IssuerURL: inputs.IssuerURL, PubKey: publicKey}
		for _, document := range sharedOIDCDocuments() {
			body, err := document.generate(params)
			if err != nil {
				t.Fatal(err)
			}
			want, err := io.ReadAll(body)
			if err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(want, cloud.objects[providerID+document.path]) {
				t.Fatalf("publication differs for %s", document.path)
			}
		}
		if !reflect.DeepEqual(cloud.buckets, []string{options.Bucket, options.Bucket}) || len(cloud.objects) != 2 {
			t.Fatal("publication bucket/count differs")
		}
		if len(cloud.events) != 3 || cloud.events[2] != "create-provider" {
			t.Fatalf("wrong publication order: %v", cloud.events)
		}
		tags := map[string]string{}
		for _, tag := range cloud.provider.Tags {
			tags[aws.ToString(tag.Key)] = aws.ToString(tag.Value)
		}
		wantTags := map[string]string{"expirationDate": "2026-10-22T20:00:00Z", supportawsutil.HypershiftInfraIDTagKey: providerID, supportawsutil.HypershiftSourceTagKey: "e2e", supportawsutil.HypershiftProwJobIDTagKey: "12345"}
		if !reflect.DeepEqual(tags, wantTags) || tags[supportawsutil.HypershiftClusterNameTagKey] != "" {
			t.Fatalf("wrong provider tags: %v", tags)
		}
		if !reflect.DeepEqual(cloud.provider.ClientIDList, []string{"openshift", "sts.amazonaws.com"}) || !reflect.DeepEqual(cloud.provider.ThumbprintList, []string{"A9D53002E97E00E043244F3D170D6F4C414104FD"}) {
			t.Fatal("provider audience/thumbprint differs")
		}
		for _, path := range []string{inputs.SigningKeyFile, filepath.Join(directory, SharedOIDCStateFileName)} {
			info, err := os.Stat(path)
			if err != nil || info.Mode().Perm() != 0600 {
				t.Fatalf("private file permissions: %v", err)
			}
		}
		stateData, err := os.ReadFile(filepath.Join(directory, SharedOIDCStateFileName))
		if err != nil {
			t.Fatal(err)
		}
		if bytes.Contains(stateData, keyData) || bytes.Contains(stateData, []byte("PRIVATE KEY")) {
			t.Fatal("ownership state contains signing key")
		}
		second, err := setupSharedOIDC(t.Context(), t.TempDir(), options, (&sharedOIDCFake{}).clients)
		if err != nil || second.IssuerURL == inputs.IssuerURL {
			t.Fatalf("providers are not independent: %v", err)
		}
	})
}

func TestSetupSharedOIDCCredentials(t *testing.T) {
	t.Run("When credential files are absent, it should permit SDK chain configuration for setup and cleanup", func(t *testing.T) {
		cloud := &sharedOIDCFake{}
		options := sharedOIDCTestOptions()
		options.IAMCredentialsFile, options.S3CredentialsFile = "", ""
		directory := t.TempDir()
		if _, err := setupSharedOIDC(t.Context(), directory, options, cloud.clients); err != nil {
			t.Fatal(err)
		}
		if err := cleanupSharedOIDC(t.Context(), directory, cloud.clients); err != nil {
			t.Fatal(err)
		}
		if len(cloud.configured) != 2 || cloud.configured[1].IAMCredentialsFile != "" || cloud.configured[1].S3CredentialsFile != "" {
			t.Fatal("cleanup did not retain SDK chain inputs")
		}
	})
	t.Run("When S3 overrides are absent, it should route both clients to infrastructure credentials and region", func(t *testing.T) {
		cloud := &sharedOIDCFake{}
		options := sharedOIDCTestOptions()
		options.S3CredentialsFile, options.S3Region = "", ""
		if _, err := setupSharedOIDC(t.Context(), t.TempDir(), options, cloud.clients); err != nil {
			t.Fatal(err)
		}
		got := cloud.configured[0]
		if got.S3CredentialsFile != options.IAMCredentialsFile || got.S3Region != options.IAMRegion {
			t.Fatal("S3 fallback differs")
		}
	})

}

func TestSetupSharedOIDCInvalidInputs(t *testing.T) {
	for _, test := range []struct {
		name   string
		mutate func(*SharedOIDCOptions)
	}{
		{name: "When bucket is missing, it should reject setup without AWS calls", mutate: func(options *SharedOIDCOptions) { options.Bucket = "" }},
		{name: "When IAM region is missing, it should reject setup without AWS calls", mutate: func(options *SharedOIDCOptions) { options.IAMRegion = "" }},
		{name: "When tags are invalid, it should reject setup without AWS calls", mutate: func(options *SharedOIDCOptions) { options.AdditionalTags = []string{"invalid"} }},
	} {
		t.Run(test.name, func(t *testing.T) {
			options := sharedOIDCTestOptions()
			test.mutate(&options)
			cloud := &sharedOIDCFake{}
			if _, err := setupSharedOIDC(t.Context(), t.TempDir(), options, cloud.clients); err == nil || len(cloud.configured) != 0 {
				t.Fatalf("invalid inputs reached AWS: %v", err)
			}
		})
	}

}

func TestSetupSharedOIDCRollback(t *testing.T) {
	for _, test := range []struct {
		name  string
		cloud sharedOIDCFake
	}{
		{name: "When first publication fails, it should remove potentially written objects", cloud: sharedOIDCFake{putFailure: 1}},
		{name: "When second publication fails, it should roll back both public objects", cloud: sharedOIDCFake{putFailure: 2}},
		{name: "When IAM returns an ambiguous failure, it should recover and delete only the exact owned provider first", cloud: sharedOIDCFake{createError: errSharedOIDCFake}},
	} {
		t.Run(test.name, func(t *testing.T) {
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			cloud := test.cloud
			cloud.cancel = cancel
			directory := t.TempDir()
			_, err := setupSharedOIDC(ctx, directory, sharedOIDCTestOptions(), cloud.clients)
			if !errors.Is(err, errSharedOIDCFake) || cloud.cleanupCanceled || len(cloud.objects) != 0 {
				t.Fatalf("rollback lost original error or canceled cleanup: %v; %v", err, cloud.events)
			}
			if _, err := os.Stat(filepath.Join(directory, SharedOIDCStateFileName)); !errors.Is(err, os.ErrNotExist) {
				t.Fatalf("rollback retained ownership state: %v", err)
			}
			if cloud.createError != nil {
				joined := strings.Join(cloud.events, "\n")
				if strings.Index(joined, "delete-provider:") > strings.Index(joined, "delete-object:") || strings.Contains(joined, "delete-provider:arn:aws:iam::123456789012:oidc-provider/external.example.org") {
					t.Fatalf("unsafe rollback: %v", cloud.events)
				}
			}
		})
	}
}

func TestSetupSharedOIDCExistingOwnership(t *testing.T) {
	t.Run("When ownership already exists, it should preserve the prior key and resources", func(t *testing.T) {
		cloud := &sharedOIDCFake{}
		directory := t.TempDir()
		inputs, err := setupSharedOIDC(t.Context(), directory, sharedOIDCTestOptions(), cloud.clients)
		if err != nil {
			t.Fatal(err)
		}
		before := append([]string{}, cloud.events...)
		if _, err := setupSharedOIDC(t.Context(), directory, sharedOIDCTestOptions(), cloud.clients); err == nil {
			t.Fatal("accepted reused state")
		}
		if !reflect.DeepEqual(before, cloud.events) {
			t.Fatal("reused state mutated cloud resources")
		}
		if _, err := os.Stat(inputs.SigningKeyFile); err != nil {
			t.Fatal(err)
		}
	})
}

func TestSharedOIDCClientsWithLoader(t *testing.T) {
	for _, failAt := range []int{0, 1, 2} {
		name := "When separate credentials and regions are configured, it should pass each to its production AWS client"
		if failAt != 0 {
			name = "When configuration loading fails at client " + string(rune('0'+failAt)) + ", it should retain the originating error"
		}
		t.Run(name, func(t *testing.T) {
			var loads []config.LoadOptions
			loader := func(_ context.Context, options ...func(*config.LoadOptions) error) (aws.Config, error) {
				loaded := config.LoadOptions{}
				for _, option := range options {
					if err := option(&loaded); err != nil {
						return aws.Config{}, err
					}
				}
				loads = append(loads, loaded)
				if len(loads) == failAt {
					return aws.Config{}, errSharedOIDCFake
				}
				return aws.Config{Region: loaded.Region}, nil
			}
			options := sharedOIDCTestOptions()
			iamClient, s3Client, err := sharedOIDCClientsWithLoader(t.Context(), options, loader)
			if failAt != 0 {
				if !errors.Is(err, errSharedOIDCFake) {
					t.Fatalf("lost config error: %v", err)
				}
				return
			}
			if err != nil || iamClient == nil || s3Client == nil || len(loads) != 2 {
				t.Fatalf("client construction failed: %v", err)
			}
			for index, want := range []struct{ region, file string }{{options.IAMRegion, options.IAMCredentialsFile}, {options.S3Region, options.S3CredentialsFile}} {
				got := loads[index]
				if got.Region != want.region || got.SharedConfigProfile != "" || !reflect.DeepEqual(got.SharedCredentialsFiles, []string{want.file}) || !reflect.DeepEqual(got.SharedConfigFiles, []string{want.file}) {
					t.Fatalf("client %d routed incorrectly", index)
				}
			}
		})
	}
}

func TestCleanupSharedOIDC(t *testing.T) {
	t.Run("When no owned state exists, it should never construct clients or remove external resources", func(t *testing.T) {
		cloud := &sharedOIDCFake{}
		if err := cleanupSharedOIDC(t.Context(), t.TempDir(), cloud.clients); err != nil || len(cloud.configured) != 0 {
			t.Fatalf("external input ownership failure: %v", err)
		}
	})
	for _, test := range []struct {
		name                                string
		listError, deleteError, objectError error
		retained                            bool
	}{
		{name: "When normal teardown succeeds, it should delete provider before documents and private local state"},
		{name: "When provider is already absent, it should tolerate NoSuchEntity", deleteError: &smithy.GenericAPIError{Code: "NoSuchEntity"}},
		{name: "When the public bucket was already removed, it should tolerate NoSuchBucket", objectError: &smithy.GenericAPIError{Code: "NoSuchBucket"}},
		{name: "When provider lookup fails, it should preserve all resources for retry", listError: errSharedOIDCFake, retained: true},
		{name: "When provider deletion fails, it should retain public documents and state", deleteError: errSharedOIDCFake, retained: true},
		{name: "When object deletion fails, it should retain the signing key and state for retry", objectError: errSharedOIDCFake, retained: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			cloud := &sharedOIDCFake{}
			directory := t.TempDir()
			inputs, err := setupSharedOIDC(t.Context(), directory, sharedOIDCTestOptions(), cloud.clients)
			if err != nil {
				t.Fatal(err)
			}
			cloud.events = nil
			cloud.listError, cloud.deleteError, cloud.objectDeleteError = test.listError, test.deleteError, test.objectError
			err = cleanupSharedOIDC(t.Context(), directory, cloud.clients)
			if (err != nil) != test.retained {
				t.Fatalf("unexpected cleanup result: %v", err)
			}
			_, keyErr := os.Stat(inputs.SigningKeyFile)
			_, stateErr := os.Stat(filepath.Join(directory, SharedOIDCStateFileName))
			if test.retained {
				if keyErr != nil || stateErr != nil {
					t.Fatal("retry state was removed")
				}
				if test.objectError == nil && len(cloud.objects) != 2 {
					t.Fatal("objects removed before provider deletion succeeded")
				}
				cloud.listError, cloud.deleteError, cloud.objectDeleteError = nil, nil, nil
				if err := cleanupSharedOIDC(t.Context(), directory, cloud.clients); err != nil {
					t.Fatal(err)
				}
			} else if !errors.Is(keyErr, os.ErrNotExist) || !errors.Is(stateErr, os.ErrNotExist) {
				t.Fatal("private state retained after success")
			}
			if err := cleanupSharedOIDC(t.Context(), directory, cloud.clients); err != nil {
				t.Fatal("cleanup is not idempotent")
			}
			joined := strings.Join(cloud.events, "\n")
			if strings.Contains(joined, "delete-provider:arn:aws:iam::123456789012:oidc-provider/external.example.org") {
				t.Fatal("deleted external provider")
			}
			if !test.retained && (len(cloud.events) < 4 || !strings.HasPrefix(cloud.events[1], "delete-provider:") || !strings.HasPrefix(cloud.events[2], "delete-object:")) {
				t.Fatalf("wrong normal teardown order: %v", cloud.events)
			}
		})
	}
	t.Run("When owned identity is malformed, it should reject cleanup before constructing clients", func(t *testing.T) {
		directory := t.TempDir()
		state := sharedOIDCState{Options: sharedOIDCTestOptions(), ProviderID: "../external-key", IssuerURL: "https://external.example.org"}
		data, err := json.Marshal(state)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(directory, SharedOIDCStateFileName), data, 0600); err != nil {
			t.Fatal(err)
		}
		cloud := &sharedOIDCFake{}
		if err := cleanupSharedOIDC(t.Context(), directory, cloud.clients); err == nil || len(cloud.configured) != 0 {
			t.Fatalf("invalid owned identity reached AWS: %v", err)
		}
	})
}

type sharedOIDCFailingClient struct{ crclient.Client }

func (client sharedOIDCFailingClient) Get(context.Context, crclient.ObjectKey, crclient.Object, ...crclient.GetOption) error {
	return errSharedOIDCFake
}

func TestCleanupSharedOIDCIfUnused(t *testing.T) {
	for _, test := range []struct {
		name                                         string
		state, present, clientError, missingManifest bool
		wantCleanup, wantError                       bool
	}{
		{name: "When external inputs have no ownership state, it should not query the management cluster"},
		{name: "When no HostedCluster remains, it should invoke normal shared cleanup", state: true, wantCleanup: true},
		{name: "When a HostedCluster remains after cancellation, it should retain the shared provider", state: true, present: true},
		{name: "When management lookup fails, it should fail closed without deleting resources", state: true, clientError: true, wantError: true},
		{name: "When manifest is unavailable, it should fail closed without deleting resources", state: true, missingManifest: true, wantError: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			directory := t.TempDir()
			if test.state {
				if err := os.WriteFile(filepath.Join(directory, SharedOIDCStateFileName), []byte("owned"), 0600); err != nil {
					t.Fatal(err)
				}
			}
			if !test.missingManifest {
				if err := WriteManifest(directory, &ClusterManifest{Clusters: []ClusterEntry{{Name: "public", Namespace: "clusters"}, {Name: "private", Namespace: "clusters"}}}); err != nil {
					t.Fatal(err)
				}
			}
			scheme := runtime.NewScheme()
			if err := hyperv1.AddToScheme(scheme); err != nil {
				t.Fatal(err)
			}
			builder := fake.NewClientBuilder().WithScheme(scheme)
			if test.present {
				builder = builder.WithObjects(&hyperv1.HostedCluster{ObjectMeta: metav1.ObjectMeta{Name: "private", Namespace: "clusters"}})
			}
			var client crclient.Client = builder.Build()
			if test.clientError || !test.state {
				client = sharedOIDCFailingClient{client}
			}
			called := false
			err := cleanupSharedOIDCIfUnused(t.Context(), directory, client, func(ctx context.Context, gotDir string) error {
				called = true
				if ctx.Err() != nil || gotDir != directory {
					t.Fatal("cleanup inputs differ")
				}
				return nil
			})
			if (err != nil) != test.wantError || called != test.wantCleanup {
				t.Fatalf("unsafe cleanup decision: called=%t err=%v", called, err)
			}
		})
	}
}
