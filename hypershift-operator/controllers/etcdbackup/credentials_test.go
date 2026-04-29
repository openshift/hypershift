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
}

func TestResolveCredentials(t *testing.T) {
	t.Run("When storage type is S3 it should delegate to AWS resolution", func(t *testing.T) {
		g := NewGomegaWithT(t)
		secret := &corev1.Secret{
			ObjectMeta: metav1.ObjectMeta{Name: "aws-creds"},
			Data: map[string][]byte{
				"credentials": []byte("arn:aws:iam::123456789012:role/test"),
			},
		}

		result := resolveCredentials(hyperv1.S3BackupStorage, secret)
		g.Expect(result.Mode).To(Equal(credentialModeAWSSTS))
	})

	t.Run("When storage type is AzureBlob it should delegate to Azure resolution", func(t *testing.T) {
		g := NewGomegaWithT(t)
		secret := &corev1.Secret{
			ObjectMeta: metav1.ObjectMeta{Name: "azure-creds"},
			Data: map[string][]byte{
				"cloud": []byte("AZURE_CLIENT_ID=client-123\n"),
			},
		}

		result := resolveCredentials(hyperv1.AzureBlobBackupStorage, secret)
		g.Expect(result.Mode).To(Equal(credentialModeAzureWorkloadIdentity))
	})
}

func TestNeedsCredentialsFile(t *testing.T) {
	t.Run("When mode is AWS static it should need credentials file", func(t *testing.T) {
		g := NewGomegaWithT(t)
		g.Expect(resolvedCredentials{Mode: credentialModeAWSStatic}.needsCredentialsFile()).To(BeTrue())
	})

	t.Run("When mode is AWS STS it should not need credentials file", func(t *testing.T) {
		g := NewGomegaWithT(t)
		g.Expect(resolvedCredentials{Mode: credentialModeAWSSTS}.needsCredentialsFile()).To(BeFalse())
	})

	t.Run("When mode is Azure client-secret it should need credentials file", func(t *testing.T) {
		g := NewGomegaWithT(t)
		g.Expect(resolvedCredentials{Mode: credentialModeAzureClientSecret}.needsCredentialsFile()).To(BeTrue())
	})

	t.Run("When mode is Azure workload-identity it should not need credentials file", func(t *testing.T) {
		g := NewGomegaWithT(t)
		g.Expect(resolvedCredentials{Mode: credentialModeAzureWorkloadIdentity}.needsCredentialsFile()).To(BeFalse())
	})

	t.Run("When mode is Azure managed-identity it should need credentials file", func(t *testing.T) {
		g := NewGomegaWithT(t)
		g.Expect(resolvedCredentials{Mode: credentialModeAzureManagedIdentity}.needsCredentialsFile()).To(BeTrue())
	})
}
