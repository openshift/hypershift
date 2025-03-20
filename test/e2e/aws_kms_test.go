//go:build e2e
// +build e2e

package e2e

import (
	"context"
	"testing"

	. "github.com/onsi/gomega"
	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	"github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/manifests"
	e2eutil "github.com/openshift/hypershift/test/e2e/util"
	crclient "sigs.k8s.io/controller-runtime/pkg/client"
)

const (
	encryptionConfigSecret           = "kas-secret-encryption-config"
	secretEncryptionConfigurationKey = "config.yaml"
)

func TestAWSClusterUsingKMSV1(t *testing.T) {
	if globalOpts.Platform != hyperv1.AWSPlatform {
		t.Skip("test only supported on platform AWS")
	}
	t.Parallel()

	ctx, cancel := context.WithCancel(testContext)
	defer cancel()

	clusterOpts := globalOpts.DefaultClusterOptions(t)

	kmsKeyArn, err := e2eutil.GetKMSKeyArn(clusterOpts.AWSPlatform.Credentials.AWSCredentialsFile, clusterOpts.AWSPlatform.Region, globalOpts.configurableClusterOptions.AWSKmsKeyAlias)
	if err != nil || kmsKeyArn == nil {
		t.Fatal("failed to retrieve kms key arn")
	}

	clusterOpts.AWSPlatform.EtcdKMSKeyARN = *kmsKeyArn

	e2eutil.NewHypershiftTest(t, ctx, func(t *testing.T, g Gomega, mgtClient crclient.Client, hostedCluster *hyperv1.HostedCluster) {
		/*encryptionConfig := &v1.EncryptionConfiguration{
			TypeMeta: metav1.TypeMeta{Kind: "EncryptionConfiguration", APIVersion: "apiserver.config.k8s.io/v1"},
			Resources: []v1.ResourceConfiguration{
				{
					Resources: config.KMSEncryptedObjects(),
					Providers: []v1.ProviderConfiguration{
						{
							KMS: &v1.KMSConfiguration{
								APIVersion: "v1",
								Name:       "awskmskey-v1-test",
								Endpoint:   "unix:///var/run/awskmsactive.sock",
								Timeout:    &metav1.Duration{Duration: 35 * time.Second},
								CacheSize:  ptr.To[int32](100),
							},
						},
						{
							Identity: &v1.IdentityConfiguration{},
						},
					},
				},
			},
		}
		encryptionConfigurationBytes := bytes.NewBuffer([]byte{})
		err = api.YamlSerializer.Encode(encryptionConfig, encryptionConfigurationBytes)
		g.Expect(err).NotTo(HaveOccurred())

		encryptionConfigSecret := manifests.KASSecretEncryptionConfigFile(hostedCluster.Namespace)
		encryptionConfigSecret.Data[secretEncryptionConfigurationKey] = encryptionConfigurationBytes.Bytes()*/

		g.Expect(hostedCluster.Spec.SecretEncryption.KMS.AWS.ActiveKey.ARN).To(Equal(*kmsKeyArn))
		g.Expect(hostedCluster.Spec.SecretEncryption.KMS.AWS.Auth.AWSKMSRoleARN).ToNot(BeEmpty())

		guestClient := e2eutil.WaitForGuestClient(t, testContext, mgtClient, hostedCluster)

		oldEncryptionConfigSecret := manifests.KASSecretEncryptionConfigFile(hostedCluster.Namespace)
		err = guestClient.Get(ctx, crclient.ObjectKeyFromObject(oldEncryptionConfigSecret), oldEncryptionConfigSecret)
		g.Expect(err).NotTo(HaveOccurred())
		t.Logf("old encryption secret: %v", encryptionConfigSecret)
		/*
			err = guestClient.Update(ctx, encryptionConfigSecret)
			g.Expect(err).NotTo(HaveOccurred())
			t.Logf("new encryption secret: %v", encryptionConfigSecret)
		*/
		e2eutil.EnsureSecretEncryptedUsingKMSV1(t, ctx, hostedCluster, guestClient)
		// test oauth with identity provider
		e2eutil.EnsureOAuthWithIdentityProvider(t, ctx, mgtClient, hostedCluster)
	}).Execute(&clusterOpts, globalOpts.Platform, globalOpts.ArtifactDir, globalOpts.ServiceAccountSigningKey)
}
