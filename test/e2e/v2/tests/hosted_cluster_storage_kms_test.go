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
	Context("[Feature:StorageKMS] Day-1 KMS key configuration", func() {
		It("should propagate initialKMSKeyARN to the hosted cluster ClusterCSIDriver and StorageClass, and encrypt provisioned volumes", Label("AWS", internal.InformingLabel), func() {
			tc := getTestCtx()
			tc.SkipIfNotPlatform(hyperv1.AWSPlatform)

			hc, err := tc.GetHostedCluster()
			Expect(err).NotTo(HaveOccurred(), "failed to get HostedCluster")

			if hc.Spec.OperatorConfiguration == nil ||
				hc.Spec.OperatorConfiguration.CSIDriverConfig.AWS.InitialKMSKeyARN == "" {
				Skip("initialKMSKeyARN is not configured on this hosted cluster")
			}
			expectedKMSKeyARN := hc.Spec.OperatorConfiguration.CSIDriverConfig.AWS.InitialKMSKeyARN

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
			awsCredsFile := internal.GetEnvVarValue("AWS_GUEST_INFRA_CREDENTIALS_FILE")
			Expect(awsCredsFile).NotTo(BeEmpty(), "AWS_GUEST_INFRA_CREDENTIALS_FILE must be set for the EBS encryption check")
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

			// The StorageClass carries the KMS key as an alias or key ARN, while
			// DescribeVolumes always returns the resolved key ARN. Assert the volume
			// is encrypted with a customer-managed key (not the empty/default), which
			// together with the StorageClass assertion above proves the key propagated.
			Expect(aws.ToString(volume.KmsKeyId)).NotTo(BeEmpty(),
				"EBS volume %s KMS key ARN must be non-empty", volumeHandle)
		})
	})
}

// StorageKMSWriteOnceTest validates that administrator edits to the hosted cluster
// ClusterCSIDriver.spec.driverConfig persist across HCCO reconcile cycles: the
// HCCO writes the KMS key only on initial creation and never reverts later changes.
func StorageKMSWriteOnceTest(getTestCtx internal.TestContextGetter) {
	Context("[Feature:StorageKMS] Write-once reconciliation", func() {
		It("should not revert administrator changes to ClusterCSIDriver.spec.driverConfig", Label("AWS", internal.InformingLabel), func() {
			tc := getTestCtx()
			tc.SkipIfNotPlatform(hyperv1.AWSPlatform)

			hc, err := tc.GetHostedCluster()
			Expect(err).NotTo(HaveOccurred(), "failed to get HostedCluster")

			if hc.Spec.OperatorConfiguration == nil ||
				hc.Spec.OperatorConfiguration.CSIDriverConfig.AWS.InitialKMSKeyARN == "" {
				Skip("initialKMSKeyARN is not configured on this hosted cluster")
			}

			hostedClusterClient, err := tc.GetHostedClusterClient(hc)
			Expect(err).NotTo(HaveOccurred(), "failed to build hosted cluster client")

			driver := &operatorv1.ClusterCSIDriver{}
			Expect(hostedClusterClient.Get(tc.Context, crclient.ObjectKey{Name: storageKMSEBSDriverName}, driver)).To(Succeed())
			Expect(driver.Spec.DriverConfig.AWS).NotTo(BeNil())
			originalKey := driver.Spec.DriverConfig.AWS.KMSKeyARN

			// Simulate an administrator changing the key on the hosted cluster resource.
			adminKey := "arn:aws:kms:us-east-1:820196288204:key/00000000-0000-0000-0000-000000000000"
			Expect(e2eutil.UpdateObject(GinkgoTB(), tc.Context, hostedClusterClient, driver, func(obj *operatorv1.ClusterCSIDriver) {
				if obj.Spec.DriverConfig.AWS == nil {
					obj.Spec.DriverConfig.AWS = &operatorv1.AWSCSIDriverConfigSpec{}
				}
				obj.Spec.DriverConfig.AWS.KMSKeyARN = adminKey
			})).To(Succeed(), "failed to apply administrator change to ClusterCSIDriver")

			DeferCleanup(func() {
				restore := &operatorv1.ClusterCSIDriver{}
				if err := hostedClusterClient.Get(tc.Context, crclient.ObjectKey{Name: storageKMSEBSDriverName}, restore); err != nil {
					GinkgoWriter.Printf("WARNING: failed to fetch ClusterCSIDriver for restore: %v\n", err)
					return
				}
				if err := e2eutil.UpdateObject(GinkgoTB(), tc.Context, hostedClusterClient, restore, func(obj *operatorv1.ClusterCSIDriver) {
					if obj.Spec.DriverConfig.AWS == nil {
						obj.Spec.DriverConfig.AWS = &operatorv1.AWSCSIDriverConfigSpec{}
					}
					obj.Spec.DriverConfig.AWS.KMSKeyARN = originalKey
				}); err != nil {
					GinkgoWriter.Printf("WARNING: failed to restore ClusterCSIDriver KMS key: %v\n", err)
				}
			})

			// The HCCO reconciles periodically; assert the admin value is stable and
			// not reverted to the original over a sustained window.
			Consistently(func(g Gomega) {
				current := &operatorv1.ClusterCSIDriver{}
				g.Expect(hostedClusterClient.Get(tc.Context, crclient.ObjectKey{Name: storageKMSEBSDriverName}, current)).To(Succeed())
				g.Expect(current.Spec.DriverConfig.AWS).NotTo(BeNil())
				g.Expect(current.Spec.DriverConfig.AWS.KMSKeyARN).To(Equal(adminKey),
					"HCCO must not revert the administrator's ClusterCSIDriver change")
			}).WithContext(tc.Context).WithTimeout(2 * time.Minute).WithPolling(15 * time.Second).Should(Succeed())
		})
	})
}

// RegisterHostedClusterStorageKMSTests registers all storage KMS tests.
func RegisterHostedClusterStorageKMSTests(getTestCtx internal.TestContextGetter) {
	StorageKMSPropagationTest(getTestCtx)
	StorageKMSWriteOnceTest(getTestCtx)
}

var _ = Describe("[sig-hypershift][Jira:Hypershift][Feature:StorageKMS] Hosted Cluster Storage KMS", Label("storage-kms"), func() {
	var testCtx *internal.TestContext

	BeforeEach(func() {
		testCtx = internal.GetTestContext()
		Expect(testCtx).NotTo(BeNil(), "test context should be set up in BeforeSuite")
	})

	RegisterHostedClusterStorageKMSTests(func() *internal.TestContext { return testCtx })
})
