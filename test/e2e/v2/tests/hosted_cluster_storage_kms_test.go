//go:build e2ev2

/*
Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package tests

import (
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	awsutil "github.com/openshift/hypershift/cmd/infra/aws/util"
	e2eutil "github.com/openshift/hypershift/test/e2e/util"
	"github.com/openshift/hypershift/test/e2e/v2/internal"

	operatorv1 "github.com/openshift/api/operator/v1"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/ec2"
	"github.com/aws/aws-sdk-go-v2/service/kms"

	corev1 "k8s.io/api/core/v1"
	storagev1 "k8s.io/api/storage/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/utils/ptr"

	crclient "sigs.k8s.io/controller-runtime/pkg/client"
)

const (
	storageKMSStorageClassName = "gp3-csi"
	storageKMSEBSDriverName    = "ebs.csi.aws.com"
)

// StorageKMSPropagationTest validates that a day-1 initialKMSKeyARN propagates to
// the hosted cluster ClusterCSIDriver and default StorageClass, and that volumes provisioned
// by that StorageClass are encrypted with the configured KMS key.
func StorageKMSPropagationTest(getTestCtx internal.TestContextGetter) {
	Context("Day-1 KMS key configuration", func() {
		It("should propagate initialKMSKeyARN to the hosted cluster ClusterCSIDriver and StorageClass, and encrypt provisioned volumes", Label("AWS"), func() {
			tc := getTestCtx()
			tc.SkipIfNotPlatform(hyperv1.AWSPlatform)
			// initialKMSKeyARN and its control-plane propagation landed in 5.1;
			// older release payloads (including upgrade-start N-1 images) lack the
			// reconciler, so skip rather than fail there.
			tc.SkipIfVersionBelow(e2eutil.Version51)

			hc, err := tc.GetHostedCluster()
			Expect(err).NotTo(HaveOccurred(), "failed to get HostedCluster")

			// SkipIfNotPlatform only checks Platform.Type; guard the AWS spec
			// pointer before dereferencing it (e.g. for Region below).
			Expect(hc.Spec.Platform.AWS).NotTo(BeNil(), "AWS platform spec must be set on an AWS HostedCluster")

			// This test only applies to a hosted cluster provisioned with a day-1
			// initialKMSKeyARN. When it is not configured, skip rather than fail:
			// the KMS key is supplied by the create-guests step via
			// HYPERSHIFT_STORAGE_KMS_KEY_ALIAS, which is not set in every job.
			if hc.Spec.OperatorConfiguration == nil {
				Skip("HostedCluster.spec.operatorConfiguration is not set; no day-1 KMS key to verify")
			}
			expectedKMSKeyARN := hc.Spec.OperatorConfiguration.CSIDriverOperator.AWS.InitialKMSKeyARN
			if expectedKMSKeyARN == "" {
				Skip("initialKMSKeyARN is not configured on this hosted cluster; set HYPERSHIFT_STORAGE_KMS_KEY_ALIAS in the create-guests step to exercise this test")
			}

			// The EBS encryption check needs AWS credentials for the guest infra
			// account. Treat their absence as a precondition (skip), not a failure.
			awsCredsFile := internal.GetEnvVarValue("AWS_GUEST_INFRA_CREDENTIALS_FILE")
			if awsCredsFile == "" {
				Skip("AWS_GUEST_INFRA_CREDENTIALS_FILE is not set; cannot verify EBS volume encryption")
			}

			hostedClusterClient, err := tc.GetHostedClusterClient(hc)
			Expect(err).NotTo(HaveOccurred(), "failed to build hosted cluster client")

			By("verifying the hosted cluster ClusterCSIDriver carries the KMS key")
			var driverKMSKeyARN string
			Eventually(func(g Gomega) {
				driver := &operatorv1.ClusterCSIDriver{}
				g.Expect(hostedClusterClient.Get(tc.Context, crclient.ObjectKey{Name: storageKMSEBSDriverName}, driver)).To(Succeed())
				g.Expect(driver.Spec.DriverConfig.AWS).NotTo(BeNil(),
					"ClusterCSIDriver.spec.driverConfig.aws must be set")
				driverKMSKeyARN = driver.Spec.DriverConfig.AWS.KMSKeyARN
				g.Expect(driverKMSKeyARN).To(Equal(expectedKMSKeyARN),
					"ClusterCSIDriver KMS key must match the configured initialKMSKeyARN")
			}).WithContext(tc.Context).WithTimeout(5 * time.Minute).WithPolling(10 * time.Second).Should(Succeed())

			By("verifying the default StorageClass carries the KMS key")
			sc := &storagev1.StorageClass{}
			Expect(hostedClusterClient.Get(tc.Context, crclient.ObjectKey{Name: storageKMSStorageClassName}, sc)).To(Succeed())
			Expect(sc.Parameters).To(HaveKeyWithValue("encrypted", "true"),
				"StorageClass must request encryption")
			Expect(sc.Parameters).To(HaveKeyWithValue("kmsKeyId", expectedKMSKeyARN),
				"StorageClass kmsKeyId must match the configured initialKMSKeyARN")

			By("provisioning a PVC and pod using the default StorageClass")
			pvc := &corev1.PersistentVolumeClaim{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "e2e-storage-kms-pvc",
					Namespace: "default",
				},
				Spec: corev1.PersistentVolumeClaimSpec{
					AccessModes:      []corev1.PersistentVolumeAccessMode{corev1.ReadWriteOnce},
					StorageClassName: ptr.To(storageKMSStorageClassName),
					Resources: corev1.VolumeResourceRequirements{
						Requests: corev1.ResourceList{
							corev1.ResourceStorage: resource.MustParse("1Gi"),
						},
					},
				},
			}
			Expect(hostedClusterClient.Create(tc.Context, pvc)).To(Succeed(), "failed to create PVC")
			DeferCleanup(func() {
				if err := hostedClusterClient.Delete(tc.Context, pvc); err != nil && !apierrors.IsNotFound(err) {
					GinkgoWriter.Printf("WARNING: failed to cleanup PVC: %v\n", err)
				}
			})

			pod := &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "e2e-storage-kms-pod",
					Namespace: "default",
				},
				Spec: corev1.PodSpec{
					Containers: []corev1.Container{
						{
							Name:    "test",
							Image:   "registry.access.redhat.com/ubi9/ubi-minimal:latest",
							Command: []string{"sleep", "3600"},
							VolumeMounts: []corev1.VolumeMount{
								{Name: "vol", MountPath: "/data"},
							},
						},
					},
					Volumes: []corev1.Volume{
						{
							Name: "vol",
							VolumeSource: corev1.VolumeSource{
								PersistentVolumeClaim: &corev1.PersistentVolumeClaimVolumeSource{
									ClaimName: pvc.Name,
								},
							},
						},
					},
				},
			}
			Expect(hostedClusterClient.Create(tc.Context, pod)).To(Succeed(), "failed to create pod")
			DeferCleanup(func() {
				if err := hostedClusterClient.Delete(tc.Context, pod); err != nil && !apierrors.IsNotFound(err) {
					GinkgoWriter.Printf("WARNING: failed to cleanup pod: %v\n", err)
				}
			})

			By("waiting for the PVC to bind")
			var volumeHandle string
			Eventually(func(g Gomega) {
				bound := &corev1.PersistentVolumeClaim{}
				g.Expect(hostedClusterClient.Get(tc.Context, crclient.ObjectKeyFromObject(pvc), bound)).To(Succeed())
				g.Expect(bound.Status.Phase).To(Equal(corev1.ClaimBound), "PVC should bind")
				g.Expect(bound.Spec.VolumeName).NotTo(BeEmpty(), "PVC should reference a PV")

				pv := &corev1.PersistentVolume{}
				g.Expect(hostedClusterClient.Get(tc.Context, crclient.ObjectKey{Name: bound.Spec.VolumeName}, pv)).To(Succeed())
				g.Expect(pv.Spec.CSI).NotTo(BeNil(), "PV should be a CSI volume")
				volumeHandle = pv.Spec.CSI.VolumeHandle
				g.Expect(volumeHandle).NotTo(BeEmpty(), "PV should have an EBS volume handle")
			}).WithContext(tc.Context).WithTimeout(5 * time.Minute).WithPolling(10 * time.Second).Should(Succeed())

			By("verifying the backing EBS volume is encrypted with the configured KMS key")
			region := hc.Spec.Platform.AWS.Region

			awsSession := awsutil.NewSession(tc.Context, "e2e-storage-kms", awsCredsFile, "", "", region)
			awsConfig := awsutil.NewConfig()
			ec2Client := ec2.NewFromConfig(*awsSession, func(o *ec2.Options) {
				o.Retryer = awsConfig()
			})

			output, err := ec2Client.DescribeVolumes(tc.Context, &ec2.DescribeVolumesInput{
				VolumeIds: []string{volumeHandle},
			})
			Expect(err).NotTo(HaveOccurred(), "failed to describe EBS volume %s", volumeHandle)
			Expect(output.Volumes).NotTo(BeEmpty(), "DescribeVolumes returned no volumes for %s", volumeHandle)

			volume := output.Volumes[0]
			Expect(volume.Encrypted).To(HaveValue(BeTrue()),
				"EBS volume %s must be encrypted", volumeHandle)
			Expect(volume.KmsKeyId).NotTo(BeNil(), "EBS volume %s must reference a KMS key", volumeHandle)

			// The configured initialKMSKeyARN may be an alias ARN, while
			// DescribeVolumes always returns the resolved key ARN. Resolve the
			// configured value through DescribeKey (which canonicalizes both alias
			// ARNs and key ARNs to the key ARN) and compare, so the volume is
			// proven to use exactly the configured key rather than any key.
			kmsClient := kms.NewFromConfig(*awsSession, func(o *kms.Options) {
				o.Retryer = awsConfig()
			})
			keyOut, err := kmsClient.DescribeKey(tc.Context, &kms.DescribeKeyInput{
				KeyId: aws.String(expectedKMSKeyARN),
			})
			Expect(err).NotTo(HaveOccurred(), "failed to resolve configured KMS key %s", expectedKMSKeyARN)
			Expect(keyOut.KeyMetadata).NotTo(BeNil(), "DescribeKey returned no metadata for %s", expectedKMSKeyARN)
			expectedKeyARN := aws.ToString(keyOut.KeyMetadata.Arn)
			Expect(expectedKeyARN).NotTo(BeEmpty(), "resolved key ARN for %s must be non-empty", expectedKMSKeyARN)

			Expect(aws.ToString(volume.KmsKeyId)).To(Equal(expectedKeyARN),
				"EBS volume %s must be encrypted with the configured KMS key", volumeHandle)
		})
	})
}

// RegisterHostedClusterStorageKMSTests registers all storage KMS tests.
func RegisterHostedClusterStorageKMSTests(getTestCtx internal.TestContextGetter) {
	StorageKMSPropagationTest(getTestCtx)
}

var _ = Describe("[sig-hypershift][Jira:Hypershift][Feature:StorageKMS] Hosted Cluster Storage KMS", Label("storage-kms"), func() {
	var testCtx *internal.TestContext

	BeforeEach(func() {
		testCtx = internal.GetTestContext()
		Expect(testCtx).NotTo(BeNil(), "test context should be set up in BeforeSuite")
	})

	RegisterHostedClusterStorageKMSTests(func() *internal.TestContext { return testCtx })
})
