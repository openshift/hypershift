package etcdbackup

import (
	"testing"

	. "github.com/onsi/gomega"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func TestResolveAWSCredentials(t *testing.T) {
	t.Run("When Secret contains an ARN it should detect STS mode", func(t *testing.T) {
		g := NewGomegaWithT(t)
		secret := &corev1.Secret{
			ObjectMeta: metav1.ObjectMeta{Name: "aws-creds"},
			Data: map[string][]byte{
				"credentials": []byte("arn:aws:iam::123456789012:role/etcd-backup-role"),
			},
		}

		result := resolveAWSCredentials(secret)
		g.Expect(result.Mode).To(Equal(credentialModeAWSSTS))
		g.Expect(result.RoleARN).To(Equal("arn:aws:iam::123456789012:role/etcd-backup-role"))
		g.Expect(result.SecretName).To(Equal("aws-creds"))
	})

	t.Run("When Secret contains an ARN with whitespace it should detect STS mode", func(t *testing.T) {
		g := NewGomegaWithT(t)
		secret := &corev1.Secret{
			ObjectMeta: metav1.ObjectMeta{Name: "aws-creds"},
			Data: map[string][]byte{
				"credentials": []byte("  arn:aws:iam::123456789012:role/etcd-backup-role\n"),
			},
		}

		result := resolveAWSCredentials(secret)
		g.Expect(result.Mode).To(Equal(credentialModeAWSSTS))
		g.Expect(result.RoleARN).To(Equal("arn:aws:iam::123456789012:role/etcd-backup-role"))
	})

	t.Run("When Secret contains INI credentials it should detect static mode", func(t *testing.T) {
		g := NewGomegaWithT(t)
		secret := &corev1.Secret{
			ObjectMeta: metav1.ObjectMeta{Name: "aws-creds"},
			Data: map[string][]byte{
				"credentials": []byte("[default]\naws_access_key_id = AKIAIOSFODNN7EXAMPLE\naws_secret_access_key = wJalrXUtnFEMI/K7MDENG/bPxRfiCYEXAMPLEKEY\n"),
			},
		}

		result := resolveAWSCredentials(secret)
		g.Expect(result.Mode).To(Equal(credentialModeAWSStatic))
		g.Expect(result.RoleARN).To(BeEmpty())
		g.Expect(result.SecretName).To(Equal("aws-creds"))
	})

	t.Run("When Secret has empty credentials it should detect static mode", func(t *testing.T) {
		g := NewGomegaWithT(t)
		secret := &corev1.Secret{
			ObjectMeta: metav1.ObjectMeta{Name: "aws-creds"},
			Data:       map[string][]byte{"credentials": []byte("")},
		}

		result := resolveAWSCredentials(secret)
		g.Expect(result.Mode).To(Equal(credentialModeAWSStatic))
	})
}

func TestResolveAzureCredentials(t *testing.T) {
	t.Run("When Secret has cloud key with AZURE_CLIENT_ID it should detect workload-identity mode", func(t *testing.T) {
		g := NewGomegaWithT(t)
		secret := &corev1.Secret{
			ObjectMeta: metav1.ObjectMeta{Name: "azure-creds"},
			Data: map[string][]byte{
				"cloud": []byte("AZURE_SUBSCRIPTION_ID=sub-123\nAZURE_TENANT_ID=tenant-456\nAZURE_CLIENT_ID=client-789\nAZURE_RESOURCE_GROUP=rg-test\nAZURE_CLOUD_NAME=AzurePublicCloud\n"),
			},
		}

		result := resolveAzureCredentials(secret)
		g.Expect(result.Mode).To(Equal(credentialModeAzureWorkloadIdentity))
		g.Expect(result.ClientID).To(Equal("client-789"))
		g.Expect(result.SecretName).To(Equal("azure-creds"))
	})

	t.Run("When Secret has cloud key without AZURE_CLIENT_ID it should fall back to managed-identity mode", func(t *testing.T) {
		g := NewGomegaWithT(t)
		secret := &corev1.Secret{
			ObjectMeta: metav1.ObjectMeta{Name: "azure-creds"},
			Data: map[string][]byte{
				"cloud": []byte("AZURE_SUBSCRIPTION_ID=sub-123\nAZURE_TENANT_ID=tenant-456\n"),
			},
		}

		result := resolveAzureCredentials(secret)
		g.Expect(result.Mode).To(Equal(credentialModeAzureManagedIdentity))
		g.Expect(result.ClientID).To(BeEmpty())
	})

	t.Run("When Secret has credentials key with clientSecret JSON it should detect client-secret mode", func(t *testing.T) {
		g := NewGomegaWithT(t)
		secret := &corev1.Secret{
			ObjectMeta: metav1.ObjectMeta{Name: "azure-creds"},
			Data: map[string][]byte{
				"credentials": []byte(`{"subscriptionId":"sub-123","tenantId":"tenant-456","clientId":"client-789","clientSecret":"secret-abc"}`),
			},
		}

		result := resolveAzureCredentials(secret)
		g.Expect(result.Mode).To(Equal(credentialModeAzureClientSecret))
		g.Expect(result.SecretName).To(Equal("azure-creds"))
	})

	t.Run("When Secret has credentials key with JSON but empty clientSecret it should detect managed-identity mode", func(t *testing.T) {
		g := NewGomegaWithT(t)
		secret := &corev1.Secret{
			ObjectMeta: metav1.ObjectMeta{Name: "azure-creds"},
			Data: map[string][]byte{
				"credentials": []byte(`{"subscriptionId":"sub-123","tenantId":"tenant-456","clientId":"client-789","clientSecret":""}`),
			},
		}

		result := resolveAzureCredentials(secret)
		g.Expect(result.Mode).To(Equal(credentialModeAzureManagedIdentity))
	})

	t.Run("When Secret has credentials key with non-JSON content it should detect managed-identity mode", func(t *testing.T) {
		g := NewGomegaWithT(t)
		secret := &corev1.Secret{
			ObjectMeta: metav1.ObjectMeta{Name: "azure-creds"},
			Data: map[string][]byte{
				"credentials": []byte("certificate-data-here"),
			},
		}

		result := resolveAzureCredentials(secret)
		g.Expect(result.Mode).To(Equal(credentialModeAzureManagedIdentity))
	})

	t.Run("When Secret has no known keys it should detect managed-identity mode", func(t *testing.T) {
		g := NewGomegaWithT(t)
		secret := &corev1.Secret{
			ObjectMeta: metav1.ObjectMeta{Name: "azure-creds"},
			Data:       map[string][]byte{},
		}

		result := resolveAzureCredentials(secret)
		g.Expect(result.Mode).To(Equal(credentialModeAzureManagedIdentity))
	})

	t.Run("When Secret has cloud key with AZURE_CLIENT_ID= and whitespace value it should fall back to managed-identity mode", func(t *testing.T) {
		g := NewGomegaWithT(t)
		secret := &corev1.Secret{
			ObjectMeta: metav1.ObjectMeta{Name: "azure-creds"},
			Data: map[string][]byte{
				"cloud": []byte("AZURE_SUBSCRIPTION_ID=sub-123\nAZURE_CLIENT_ID=   \nAZURE_TENANT_ID=tenant-456\n"),
			},
		}

		result := resolveAzureCredentials(secret)
		g.Expect(result.Mode).To(Equal(credentialModeAzureManagedIdentity))
		g.Expect(result.ClientID).To(BeEmpty())
	})

	t.Run("When Secret has cloud key with Windows-style CRLF line endings it should trim and detect workload-identity mode", func(t *testing.T) {
		g := NewGomegaWithT(t)
		secret := &corev1.Secret{
			ObjectMeta: metav1.ObjectMeta{Name: "azure-creds"},
			Data: map[string][]byte{
				"cloud": []byte("AZURE_SUBSCRIPTION_ID=sub-123\r\nAZURE_CLIENT_ID=client-789\r\nAZURE_TENANT_ID=tenant-456\r\n"),
			},
		}

		result := resolveAzureCredentials(secret)
		g.Expect(result.Mode).To(Equal(credentialModeAzureWorkloadIdentity))
		g.Expect(result.ClientID).To(Equal("client-789"))
	})

	t.Run("When Secret has both cloud and credentials keys it should prioritize cloud key", func(t *testing.T) {
		g := NewGomegaWithT(t)
		secret := &corev1.Secret{
			ObjectMeta: metav1.ObjectMeta{Name: "azure-creds"},
			Data: map[string][]byte{
				"cloud":       []byte("AZURE_CLIENT_ID=wi-client\n"),
				"credentials": []byte(`{"clientSecret":"should-be-ignored"}`),
			},
		}

		result := resolveAzureCredentials(secret)
		g.Expect(result.Mode).To(Equal(credentialModeAzureWorkloadIdentity))
		g.Expect(result.ClientID).To(Equal("wi-client"))
	})
}

func TestResolveCredentials(t *testing.T) {
	tests := []struct {
		name        string
		storageType hyperv1.HCPEtcdBackupStorageType
		secretName  string
		secretData  map[string][]byte
		wantMode    credentialMode
	}{
		{
			name:        "When storage type is S3 it should delegate to AWS resolution",
			storageType: hyperv1.S3BackupStorage,
			secretName:  "aws-creds",
			secretData:  map[string][]byte{"credentials": []byte("arn:aws:iam::123456789012:role/test")},
			wantMode:    credentialModeAWSSTS,
		},
		{
			name:        "When storage type is AzureBlob it should delegate to Azure resolution",
			storageType: hyperv1.AzureBlobBackupStorage,
			secretName:  "azure-creds",
			secretData:  map[string][]byte{"cloud": []byte("AZURE_CLIENT_ID=client-123\n")},
			wantMode:    credentialModeAzureWorkloadIdentity,
		},
		{
			name:        "When storage type is unknown it should default to AWS static mode",
			storageType: "UnknownStorage",
			secretName:  "some-creds",
			secretData:  map[string][]byte{},
			wantMode:    credentialModeAWSStatic,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			g := NewGomegaWithT(t)
			secret := &corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{Name: tt.secretName},
				Data:       tt.secretData,
			}
			result := resolveCredentials(tt.storageType, secret)
			g.Expect(result.Mode).To(Equal(tt.wantMode))
			g.Expect(result.SecretName).To(Equal(tt.secretName))
		})
	}
}

func TestCredentialBehavioralMethods(t *testing.T) {
	tests := []struct {
		name                      string
		mode                      credentialMode
		wantNeedsCredentialsFile  bool
		wantNeedsProjectedToken   bool
		wantNeedsWorkloadIdentity bool
		wantAzureAuthType         string
	}{
		{
			name:                      "When mode is AWS static it should need credentials file only",
			mode:                      credentialModeAWSStatic,
			wantNeedsCredentialsFile:  true,
			wantNeedsProjectedToken:   false,
			wantNeedsWorkloadIdentity: false,
			wantAzureAuthType:         "",
		},
		{
			name:                      "When mode is AWS STS it should need projected token only",
			mode:                      credentialModeAWSSTS,
			wantNeedsCredentialsFile:  false,
			wantNeedsProjectedToken:   true,
			wantNeedsWorkloadIdentity: false,
			wantAzureAuthType:         "",
		},
		{
			name:                      "When mode is Azure client-secret it should need credentials file and report auth type",
			mode:                      credentialModeAzureClientSecret,
			wantNeedsCredentialsFile:  true,
			wantNeedsProjectedToken:   false,
			wantNeedsWorkloadIdentity: false,
			wantAzureAuthType:         "client-secret",
		},
		{
			name:                      "When mode is Azure workload-identity it should need workload identity label only",
			mode:                      credentialModeAzureWorkloadIdentity,
			wantNeedsCredentialsFile:  false,
			wantNeedsProjectedToken:   false,
			wantNeedsWorkloadIdentity: true,
			wantAzureAuthType:         "",
		},
		{
			name:                      "When mode is Azure managed-identity it should need credentials file and report auth type",
			mode:                      credentialModeAzureManagedIdentity,
			wantNeedsCredentialsFile:  true,
			wantNeedsProjectedToken:   false,
			wantNeedsWorkloadIdentity: false,
			wantAzureAuthType:         "managed-identity",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			g := NewGomegaWithT(t)
			creds := resolvedCredentials{Mode: tt.mode}
			g.Expect(creds.needsCredentialsFile()).To(Equal(tt.wantNeedsCredentialsFile))
			g.Expect(creds.needsProjectedToken()).To(Equal(tt.wantNeedsProjectedToken))
			g.Expect(creds.needsWorkloadIdentityLabel()).To(Equal(tt.wantNeedsWorkloadIdentity))
			g.Expect(creds.azureAuthType()).To(Equal(tt.wantAzureAuthType))
		})
	}
}
