package util

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"testing"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"

	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	crclient "sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"
)

func containsFlag(args []string, flag string) bool {
	return slices.Contains(args, flag)
}

func containsFlagValue(args []string, flag, value string) bool {
	for i, a := range args {
		if a == flag && i+1 < len(args) && args[i+1] == value {
			return true
		}
	}
	return false
}

func TestBuildInstallerArgs(t *testing.T) {
	tests := []struct {
		name           string
		opts           HyperShiftOperatorInstallOptions
		secrets        *credentialSecretNames
		expectFlags    []string
		expectPairs    [][2]string
		notExpectFlags []string
	}{
		{
			name: "When AWS platform with OIDC S3 and private platform and ExternalDNS, it should include all AWS flags",
			opts: HyperShiftOperatorInstallOptions{
				HyperShiftOperatorLatestImage:          "quay.io/openshift/hypershift:latest",
				Platform:                               hyperv1.AWSPlatform,
				AWSOidcS3BucketName:                    "my-oidc-bucket",
				AWSOidcS3Region:                        "us-east-1",
				PrivatePlatform:                        "AWS",
				AWSPrivateRegion:                       "us-east-1",
				ExternalDNSProvider:                    "aws",
				ExternalDNSDomainFilter:                "example.com",
				PlatformMonitoring:                     "All",
				EnableDedicatedRequestServingIsolation: true,
				EnableEtcdRecovery:                     true,
			},
			secrets: &credentialSecretNames{
				oidcS3:      "hypershift-installer-oidc-s3",
				awsPrivate:  "hypershift-installer-aws-private",
				externalDNS: "hypershift-installer-external-dns",
			},
			expectFlags: []string{
				"install",
				"--wait-until-available",
			},
			expectPairs: [][2]string{
				{"--hypershift-image", "quay.io/openshift/hypershift:latest"},
				{"--oidc-storage-provider-s3-bucket-name", "my-oidc-bucket"},
				{"--oidc-storage-provider-s3-region", "us-east-1"},
				{"--oidc-storage-provider-s3-secret", "hypershift-installer-oidc-s3"},
				{"--private-platform", "AWS"},
				{"--aws-private-secret", "hypershift-installer-aws-private"},
				{"--aws-private-region", "us-east-1"},
				{"--external-dns-provider", "aws"},
				{"--external-dns-domain-filter", "example.com"},
				{"--external-dns-secret", "hypershift-installer-external-dns"},
				{"--external-dns-interval", "3m"},
				{"--platform-monitoring", "All"},
			},
		},
		{
			name: "When AWS platform with only OIDC S3 and no private or ExternalDNS, it should include only OIDC flags",
			opts: HyperShiftOperatorInstallOptions{
				HyperShiftOperatorLatestImage: "quay.io/openshift/hypershift:latest",
				Platform:                      hyperv1.AWSPlatform,
				AWSOidcS3BucketName:           "my-oidc-bucket",
				AWSOidcS3Region:               "us-east-1",
			},
			secrets: &credentialSecretNames{
				oidcS3: "hypershift-installer-oidc-s3",
			},
			expectPairs: [][2]string{
				{"--oidc-storage-provider-s3-bucket-name", "my-oidc-bucket"},
				{"--oidc-storage-provider-s3-secret", "hypershift-installer-oidc-s3"},
			},
			notExpectFlags: []string{
				"--aws-private-secret",
				"--external-dns-provider",
				"--external-dns-secret",
			},
		},
		{
			name: "When Azure platform with private and ExternalDNS, it should include Azure-specific flags",
			opts: HyperShiftOperatorInstallOptions{
				HyperShiftOperatorLatestImage: "quay.io/openshift/hypershift:latest",
				Platform:                      hyperv1.AzurePlatform,
				PrivatePlatform:               "Azure",
				AzurePLSResourceGroup:         "my-rg",
				ExternalDNSProvider:           "azure",
				ExternalDNSDomainFilter:       "example.com",
			},
			secrets: &credentialSecretNames{
				azurePrivate: "hypershift-installer-azure-private",
				externalDNS:  "hypershift-installer-external-dns",
			},
			expectPairs: [][2]string{
				{"--private-platform", "Azure"},
				{"--azure-private-secret", "hypershift-installer-azure-private"},
				{"--azure-pls-resource-group", "my-rg"},
				{"--external-dns-provider", "azure"},
				{"--external-dns-secret", "hypershift-installer-external-dns"},
				{"--external-dns-interval", "3m"},
			},
			notExpectFlags: []string{
				"--oidc-storage-provider-s3-secret",
				"--aws-private-secret",
			},
		},
		{
			name: "When GCP platform with ExternalDNS and google project, it should include GCP-specific flags",
			opts: HyperShiftOperatorInstallOptions{
				HyperShiftOperatorLatestImage: "quay.io/openshift/hypershift:latest",
				Platform:                      hyperv1.GCPPlatform,
				PrivatePlatform:               "GCP",
				GCPProject:                    "my-gcp-project",
				GCPRegion:                     "us-central1",
				ExternalDNSProvider:           "google",
				ExternalDNSDomainFilter:       "example.com",
				ExternalDNSGoogleProject:      "dns-project",
			},
			secrets: &credentialSecretNames{
				externalDNS: "hypershift-installer-external-dns",
			},
			expectPairs: [][2]string{
				{"--private-platform", "GCP"},
				{"--gcp-project", "my-gcp-project"},
				{"--gcp-region", "us-central1"},
				{"--external-dns-provider", "google"},
				{"--external-dns-secret", "hypershift-installer-external-dns"},
				{"--external-dns-google-project", "dns-project"},
				{"--external-dns-interval", "3m"},
			},
			notExpectFlags: []string{
				"--oidc-storage-provider-s3-secret",
				"--aws-private-secret",
				"--azure-private-secret",
			},
		},
		{
			name: "When default platform with OIDC S3 bucket, it should include OIDC flags only",
			opts: HyperShiftOperatorInstallOptions{
				HyperShiftOperatorLatestImage: "quay.io/openshift/hypershift:latest",
				Platform:                      hyperv1.KubevirtPlatform,
				AWSOidcS3BucketName:           "my-oidc-bucket",
				AWSOidcS3Region:               "us-east-1",
			},
			secrets: &credentialSecretNames{
				oidcS3: "hypershift-installer-oidc-s3",
			},
			expectPairs: [][2]string{
				{"--oidc-storage-provider-s3-bucket-name", "my-oidc-bucket"},
				{"--oidc-storage-provider-s3-region", "us-east-1"},
				{"--oidc-storage-provider-s3-secret", "hypershift-installer-oidc-s3"},
			},
			notExpectFlags: []string{
				"--private-platform",
				"--aws-private-secret",
				"--external-dns-provider",
			},
		},
		{
			name: "When PlatformMonitoring is empty, it should not include platform-monitoring flag",
			opts: HyperShiftOperatorInstallOptions{
				HyperShiftOperatorLatestImage: "quay.io/openshift/hypershift:latest",
				Platform:                      hyperv1.AWSPlatform,
			},
			secrets:        &credentialSecretNames{},
			notExpectFlags: []string{"--platform-monitoring"},
		},
		{
			name: "When boolean flags are set, it should always include explicit true/false values",
			opts: HyperShiftOperatorInstallOptions{
				HyperShiftOperatorLatestImage:          "quay.io/openshift/hypershift:latest",
				Platform:                               hyperv1.AWSPlatform,
				EnableDedicatedRequestServingIsolation: true,
				EnableEtcdRecovery:                     false,
				EnableSizeTagging:                      true,
				EnableCPOOverrides:                     true,
				DisableCAPIMigration:                   true,
			},
			secrets: &credentialSecretNames{},
			expectFlags: []string{
				"--enable-dedicated-request-serving-isolation=true",
				"--enable-etcd-recovery=false",
				"--enable-size-tagging",
				"--enable-cpo-overrides",
				"--disable-capi-migration",
			},
		},
		{
			name: "When boolean flags are false, it should include explicit false values for always-present flags",
			opts: HyperShiftOperatorInstallOptions{
				HyperShiftOperatorLatestImage:          "quay.io/openshift/hypershift:latest",
				Platform:                               hyperv1.AWSPlatform,
				EnableDedicatedRequestServingIsolation: false,
				EnableEtcdRecovery:                     true,
			},
			secrets: &credentialSecretNames{},
			expectFlags: []string{
				"--enable-dedicated-request-serving-isolation=false",
				"--enable-etcd-recovery=true",
			},
			notExpectFlags: []string{
				"--enable-size-tagging",
				"--enable-cpo-overrides",
				"--disable-capi-migration",
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			args := buildInstallerArgs(tc.opts, tc.secrets)

			for _, flag := range tc.expectFlags {
				if !containsFlag(args, flag) {
					t.Errorf("expected flag %q not found in args: %v", flag, args)
				}
			}
			for _, pair := range tc.expectPairs {
				if !containsFlagValue(args, pair[0], pair[1]) {
					t.Errorf("expected flag pair %q=%q not found in args: %v", pair[0], pair[1], args)
				}
			}
			for _, flag := range tc.notExpectFlags {
				if containsFlag(args, flag) {
					t.Errorf("unexpected flag %q found in args: %v", flag, args)
				}
			}
		})
	}
}

func writeCredentialFile(t *testing.T, dir, name, content string) string {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte(content), 0600); err != nil {
		t.Fatalf("writing credential file: %v", err)
	}
	return path
}

func TestCreateCredentialSecrets(t *testing.T) {
	tests := []struct {
		name              string
		opts              func(dir string) HyperShiftOperatorInstallOptions
		existingObjects   []crclient.Object
		expectSecrets     []string
		notExpectSecrets  []string
		expectUpdatedData map[string]string
	}{
		{
			name: "When AWS platform with all credentials, it should create OIDC private and ExternalDNS secrets",
			opts: func(dir string) HyperShiftOperatorInstallOptions {
				return HyperShiftOperatorInstallOptions{
					Platform:                  hyperv1.AWSPlatform,
					AWSOidcS3Credentials:      writeCredentialFile(t, dir, "oidc", "oidc-cred-data"),
					PrivatePlatform:           string(hyperv1.AWSPlatform),
					AWSPrivateCredentialsFile: writeCredentialFile(t, dir, "aws-private", "aws-private-data"),
					ExternalDNSProvider:       "aws",
					ExternalDNSCredentials:    writeCredentialFile(t, dir, "external-dns", "dns-cred-data"),
				}
			},
			expectSecrets: []string{
				installerName + "-oidc-s3",
				installerName + "-aws-private",
				installerName + "-external-dns",
			},
		},
		{
			name: "When PrivatePlatform is None, it should not create AWS private secret",
			opts: func(dir string) HyperShiftOperatorInstallOptions {
				return HyperShiftOperatorInstallOptions{
					Platform:             hyperv1.AWSPlatform,
					AWSOidcS3Credentials: writeCredentialFile(t, dir, "oidc", "oidc-cred-data"),
					PrivatePlatform:      "None",
				}
			},
			expectSecrets:    []string{installerName + "-oidc-s3"},
			notExpectSecrets: []string{installerName + "-aws-private"},
		},
		{
			name: "When ExternalDNSProvider is empty, it should not create ExternalDNS secret",
			opts: func(dir string) HyperShiftOperatorInstallOptions {
				return HyperShiftOperatorInstallOptions{
					Platform:             hyperv1.AWSPlatform,
					AWSOidcS3Credentials: writeCredentialFile(t, dir, "oidc", "oidc-cred-data"),
				}
			},
			expectSecrets:    []string{installerName + "-oidc-s3"},
			notExpectSecrets: []string{installerName + "-external-dns"},
		},
		{
			name: "When secret already exists, it should update its data",
			opts: func(dir string) HyperShiftOperatorInstallOptions {
				return HyperShiftOperatorInstallOptions{
					Platform:             hyperv1.AWSPlatform,
					AWSOidcS3Credentials: writeCredentialFile(t, dir, "oidc", "updated-oidc-data"),
				}
			},
			existingObjects: []crclient.Object{
				&corev1.Secret{
					ObjectMeta: metav1.ObjectMeta{
						Name:      installerName + "-oidc-s3",
						Namespace: installerNamespace,
					},
					Data: map[string][]byte{"credentials": []byte("old-data")},
				},
			},
			expectSecrets:     []string{installerName + "-oidc-s3"},
			expectUpdatedData: map[string]string{installerName + "-oidc-s3": "updated-oidc-data"},
		},
		{
			name: "When Azure platform with private, it should create Azure private secret",
			opts: func(dir string) HyperShiftOperatorInstallOptions {
				return HyperShiftOperatorInstallOptions{
					Platform:                    hyperv1.AzurePlatform,
					PrivatePlatform:             string(hyperv1.AzurePlatform),
					AzurePrivateCredentialsFile: writeCredentialFile(t, dir, "azure-private", "azure-cred-data"),
				}
			},
			expectSecrets:    []string{installerName + "-azure-private"},
			notExpectSecrets: []string{installerName + "-oidc-s3", installerName + "-aws-private"},
		},
		{
			name: "When GCP platform with ExternalDNS, it should create ExternalDNS secret",
			opts: func(dir string) HyperShiftOperatorInstallOptions {
				return HyperShiftOperatorInstallOptions{
					Platform:               hyperv1.GCPPlatform,
					ExternalDNSProvider:    "google",
					ExternalDNSCredentials: writeCredentialFile(t, dir, "dns", "gcp-dns-data"),
				}
			},
			expectSecrets:    []string{installerName + "-external-dns"},
			notExpectSecrets: []string{installerName + "-oidc-s3"},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			opts := tc.opts(dir)
			client := GetFakeClient(tc.existingObjects...)
			ctx := context.Background()

			names, err := createCredentialSecrets(ctx, client, opts)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}

			for _, secretName := range tc.expectSecrets {
				secret := &corev1.Secret{}
				if err := client.Get(ctx, crclient.ObjectKey{Name: secretName, Namespace: installerNamespace}, secret); err != nil {
					t.Errorf("expected secret %q to exist: %v", secretName, err)
				}
			}

			for _, secretName := range tc.notExpectSecrets {
				secret := &corev1.Secret{}
				err := client.Get(ctx, crclient.ObjectKey{Name: secretName, Namespace: installerNamespace}, secret)
				if err == nil {
					t.Errorf("expected secret %q to not exist but it does", secretName)
				}
			}

			for secretName, expectedData := range tc.expectUpdatedData {
				secret := &corev1.Secret{}
				if err := client.Get(ctx, crclient.ObjectKey{Name: secretName, Namespace: installerNamespace}, secret); err != nil {
					t.Fatalf("getting secret %q: %v", secretName, err)
				}
				if string(secret.Data["credentials"]) != expectedData {
					t.Errorf("secret %q data = %q, want %q", secretName, string(secret.Data["credentials"]), expectedData)
				}
			}

			_ = names
		})
	}
}

func TestEnsureInstallerRBAC(t *testing.T) {
	tests := []struct {
		name        string
		objects     []crclient.Object
		interceptor *interceptor.Funcs
		wantErr     bool
	}{
		{
			name:    "When SA and CRB do not exist, it should create them",
			objects: nil,
			wantErr: false,
		},
		{
			name: "When SA and CRB already exist, it should succeed without error",
			objects: []crclient.Object{
				&corev1.ServiceAccount{
					ObjectMeta: metav1.ObjectMeta{
						Name:      installerName,
						Namespace: installerNamespace,
					},
				},
				&rbacv1.ClusterRoleBinding{
					ObjectMeta: metav1.ObjectMeta{
						Name: installerName,
					},
					RoleRef: rbacv1.RoleRef{
						APIGroup: "rbac.authorization.k8s.io",
						Kind:     "ClusterRole",
						Name:     "cluster-admin",
					},
				},
			},
			wantErr: false,
		},
		{
			name: "When client Get returns non-NotFound error, it should return error",
			interceptor: &interceptor.Funcs{
				Get: func(ctx context.Context, cl crclient.WithWatch, key crclient.ObjectKey, obj crclient.Object, opts ...crclient.GetOption) error {
					return fmt.Errorf("simulated API server error")
				},
			},
			wantErr: true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			builder := fake.NewClientBuilder().WithScheme(scheme)
			if len(tc.objects) > 0 {
				builder = builder.WithObjects(tc.objects...)
			}
			if tc.interceptor != nil {
				builder = builder.WithInterceptorFuncs(*tc.interceptor)
			}
			client := builder.Build()

			err := ensureInstallerRBAC(context.Background(), client)
			if (err != nil) != tc.wantErr {
				t.Errorf("ensureInstallerRBAC() error = %v, wantErr %v", err, tc.wantErr)
				return
			}

			if !tc.wantErr && tc.interceptor == nil {
				sa := &corev1.ServiceAccount{}
				if err := client.Get(context.Background(), crclient.ObjectKey{Name: installerName, Namespace: installerNamespace}, sa); err != nil {
					t.Errorf("expected ServiceAccount to exist: %v", err)
				}
				crb := &rbacv1.ClusterRoleBinding{}
				if err := client.Get(context.Background(), crclient.ObjectKey{Name: installerName}, crb); err != nil {
					t.Errorf("expected ClusterRoleBinding to exist: %v", err)
				}
			}
		})
	}
}

func TestCleanupInstaller(t *testing.T) {
	t.Run("When called after successful install, it should delete Job SA CRB but not credential Secrets", func(t *testing.T) {
		ctx := context.Background()

		oidcSecret := &corev1.Secret{
			ObjectMeta: metav1.ObjectMeta{
				Name:      installerName + "-oidc-s3",
				Namespace: installerNamespace,
			},
			Data: map[string][]byte{"credentials": []byte("oidc-data")},
		}
		awsSecret := &corev1.Secret{
			ObjectMeta: metav1.ObjectMeta{
				Name:      installerName + "-aws-private",
				Namespace: installerNamespace,
			},
			Data: map[string][]byte{"credentials": []byte("aws-data")},
		}
		dnsSecret := &corev1.Secret{
			ObjectMeta: metav1.ObjectMeta{
				Name:      installerName + "-external-dns",
				Namespace: installerNamespace,
			},
			Data: map[string][]byte{"credentials": []byte("dns-data")},
		}

		job := &batchv1.Job{
			ObjectMeta: metav1.ObjectMeta{
				Name:      installerName,
				Namespace: installerNamespace,
			},
		}
		sa := &corev1.ServiceAccount{
			ObjectMeta: metav1.ObjectMeta{
				Name:      installerName,
				Namespace: installerNamespace,
			},
		}
		crb := &rbacv1.ClusterRoleBinding{
			ObjectMeta: metav1.ObjectMeta{
				Name: installerName,
			},
			RoleRef: rbacv1.RoleRef{
				APIGroup: "rbac.authorization.k8s.io",
				Kind:     "ClusterRole",
				Name:     "cluster-admin",
			},
		}

		client := GetFakeClient(oidcSecret, awsSecret, dnsSecret, job, sa, crb)

		cleanupInstaller(ctx, client)

		// Job, SA, CRB should be deleted
		err := client.Get(ctx, crclient.ObjectKeyFromObject(job), &batchv1.Job{})
		if !apierrors.IsNotFound(err) {
			t.Errorf("expected Job to be deleted, got err: %v", err)
		}
		err = client.Get(ctx, crclient.ObjectKeyFromObject(sa), &corev1.ServiceAccount{})
		if !apierrors.IsNotFound(err) {
			t.Errorf("expected ServiceAccount to be deleted, got err: %v", err)
		}
		err = client.Get(ctx, crclient.ObjectKeyFromObject(crb), &rbacv1.ClusterRoleBinding{})
		if !apierrors.IsNotFound(err) {
			t.Errorf("expected ClusterRoleBinding to be deleted, got err: %v", err)
		}

		// Credential Secrets must NOT be deleted — Deployment volumes reference them
		for _, secret := range []*corev1.Secret{oidcSecret, awsSecret, dnsSecret} {
			got := &corev1.Secret{}
			if err := client.Get(ctx, crclient.ObjectKeyFromObject(secret), got); err != nil {
				t.Errorf("credential Secret %q should still exist after cleanup: %v", secret.Name, err)
			}
		}
	})
}

func TestCreateInstallerJob(t *testing.T) {
	tests := []struct {
		name    string
		objects []crclient.Object
	}{
		{
			name:    "When no previous job exists, it should create the job",
			objects: nil,
		},
		{
			name: "When previous job exists, it should delete it and create new one",
			objects: []crclient.Object{
				&batchv1.Job{
					ObjectMeta: metav1.ObjectMeta{
						Name:      installerName,
						Namespace: installerNamespace,
					},
				},
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			client := GetFakeClient(tc.objects...)
			ctx := context.Background()

			err := createInstallerJob(ctx, client, "quay.io/openshift/hypershift:latest", []string{"install", "--wait-until-available"})
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}

			job := &batchv1.Job{}
			if err := client.Get(ctx, crclient.ObjectKey{Name: installerName, Namespace: installerNamespace}, job); err != nil {
				t.Fatalf("expected job to exist: %v", err)
			}
			if job.Spec.Template.Spec.Containers[0].Image != "quay.io/openshift/hypershift:latest" {
				t.Errorf("job image = %q, want %q", job.Spec.Template.Spec.Containers[0].Image, "quay.io/openshift/hypershift:latest")
			}
		})
	}
}
