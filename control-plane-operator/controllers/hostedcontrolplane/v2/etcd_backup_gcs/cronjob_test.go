package etcdbackupgcs

import (
	"testing"

	. "github.com/onsi/gomega"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	component "github.com/openshift/hypershift/support/controlplane-component"

	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func TestPredicate(t *testing.T) {
	t.Run("When etcd managed is nil it should return false", func(t *testing.T) {
		g := NewGomegaWithT(t)
		cpContext := component.WorkloadContext{
			HCP: &hyperv1.HostedControlPlane{
				Spec: hyperv1.HostedControlPlaneSpec{
					Etcd: hyperv1.EtcdSpec{
						ManagementType: hyperv1.Managed,
					},
				},
			},
		}
		result, err := predicate(cpContext)
		g.Expect(err).ToNot(HaveOccurred())
		g.Expect(result).To(BeFalse())
	})

	t.Run("When automatedBackup is nil it should return false", func(t *testing.T) {
		g := NewGomegaWithT(t)
		cpContext := component.WorkloadContext{
			HCP: &hyperv1.HostedControlPlane{
				Spec: hyperv1.HostedControlPlaneSpec{
					Etcd: hyperv1.EtcdSpec{
						ManagementType: hyperv1.Managed,
						Managed:        &hyperv1.ManagedEtcdSpec{},
					},
				},
			},
		}
		result, err := predicate(cpContext)
		g.Expect(err).ToNot(HaveOccurred())
		g.Expect(result).To(BeFalse())
	})

	t.Run("When automatedBackup is configured but etcd is not available it should return false", func(t *testing.T) {
		g := NewGomegaWithT(t)
		cpContext := component.WorkloadContext{
			HCP: &hyperv1.HostedControlPlane{
				Spec: hyperv1.HostedControlPlaneSpec{
					Etcd: hyperv1.EtcdSpec{
						ManagementType: hyperv1.Managed,
						Managed: &hyperv1.ManagedEtcdSpec{
							AutomatedBackup: &hyperv1.AutomatedEtcdBackupConfig{
								Schedule: "0 */4 * * *",
								Storage: hyperv1.AutomatedEtcdBackupStorage{
									Type: hyperv1.AutomatedEtcdBackupStorageTypeGCS,
									GCS: &hyperv1.AutomatedEtcdBackupGCS{
										Bucket:            "my-bucket",
										GCPServiceAccount: "backup@proj.iam.gserviceaccount.com",
									},
								},
							},
						},
					},
				},
			},
		}
		result, err := predicate(cpContext)
		g.Expect(err).ToNot(HaveOccurred())
		g.Expect(result).To(BeFalse())
	})

	t.Run("When automatedBackup is configured and etcd is available it should return true", func(t *testing.T) {
		g := NewGomegaWithT(t)
		cpContext := component.WorkloadContext{
			HCP: &hyperv1.HostedControlPlane{
				Spec: hyperv1.HostedControlPlaneSpec{
					Etcd: hyperv1.EtcdSpec{
						ManagementType: hyperv1.Managed,
						Managed: &hyperv1.ManagedEtcdSpec{
							AutomatedBackup: &hyperv1.AutomatedEtcdBackupConfig{
								Schedule: "0 */4 * * *",
								Storage: hyperv1.AutomatedEtcdBackupStorage{
									Type: hyperv1.AutomatedEtcdBackupStorageTypeGCS,
									GCS: &hyperv1.AutomatedEtcdBackupGCS{
										Bucket:            "my-bucket",
										GCPServiceAccount: "backup@proj.iam.gserviceaccount.com",
									},
								},
							},
						},
					},
				},
				Status: hyperv1.HostedControlPlaneStatus{
					Conditions: []metav1.Condition{
						{
							Type:   string(hyperv1.EtcdAvailable),
							Status: metav1.ConditionTrue,
						},
					},
				},
			},
		}
		result, err := predicate(cpContext)
		g.Expect(err).ToNot(HaveOccurred())
		g.Expect(result).To(BeTrue())
	})
}

func TestAdaptCronJob(t *testing.T) {
	t.Run("When adapting CronJob it should set schedule and containers correctly", func(t *testing.T) {
		g := NewGomegaWithT(t)
		cpContext := component.WorkloadContext{
			HCP: &hyperv1.HostedControlPlane{
				Spec: hyperv1.HostedControlPlaneSpec{
					InfraID: "test-infra-123",
					Etcd: hyperv1.EtcdSpec{
						ManagementType: hyperv1.Managed,
						Managed: &hyperv1.ManagedEtcdSpec{
							AutomatedBackup: &hyperv1.AutomatedEtcdBackupConfig{
								Schedule: "0 */6 * * *",
								Storage: hyperv1.AutomatedEtcdBackupStorage{
									Type: hyperv1.AutomatedEtcdBackupStorageTypeGCS,
									GCS: &hyperv1.AutomatedEtcdBackupGCS{
										Bucket:            "my-backup-bucket",
										GCPServiceAccount: "backup@proj.iam.gserviceaccount.com",
									},
								},
							},
						},
					},
				},
			},
		}
		cronJob := &batchv1.CronJob{}

		err := adaptCronJob(cpContext, cronJob)
		g.Expect(err).ToNot(HaveOccurred())
		g.Expect(cronJob.Spec.Schedule).To(Equal("0 */6 * * *"))
		g.Expect(cronJob.Spec.ConcurrencyPolicy).To(Equal(batchv1.ForbidConcurrent))

		g.Expect(cronJob.Spec.JobTemplate.Spec.Template.Spec.InitContainers).To(HaveLen(1))
		g.Expect(cronJob.Spec.JobTemplate.Spec.Template.Spec.InitContainers[0].Name).To(Equal("snapshot"))

		g.Expect(cronJob.Spec.JobTemplate.Spec.Template.Spec.Containers).To(HaveLen(1))
		uploadContainer := cronJob.Spec.JobTemplate.Spec.Template.Spec.Containers[0]
		g.Expect(uploadContainer.Name).To(Equal("upload"))
		g.Expect(uploadContainer.Args).To(ContainElements("--gcs-bucket", "my-backup-bucket"))
		g.Expect(uploadContainer.Args).To(ContainElements("--key-prefix", "test-infra-123"))
		g.Expect(uploadContainer.Args).To(ContainElements("--secrets-dir", "/tmp/etcd-backup/secrets"))

		volumeNames := make([]string, 0, len(cronJob.Spec.JobTemplate.Spec.Template.Spec.Volumes))
		for _, v := range cronJob.Spec.JobTemplate.Spec.Template.Spec.Volumes {
			volumeNames = append(volumeNames, v.Name)
		}
		g.Expect(volumeNames).To(ContainElements("root-ca", "etcd-signer", "sa-signing-key"))
		g.Expect(volumeNames).ToNot(ContainElement("aescbc-active-key"))

		mountNames := make([]string, 0, len(uploadContainer.VolumeMounts))
		for _, m := range uploadContainer.VolumeMounts {
			mountNames = append(mountNames, m.Name)
		}
		g.Expect(mountNames).To(ContainElements("root-ca", "etcd-signer", "sa-signing-key"))
		g.Expect(mountNames).ToNot(ContainElement("aescbc-active-key"))
	})

	t.Run("When AESCBC encryption is configured it should include active key volume and mount", func(t *testing.T) {
		g := NewGomegaWithT(t)
		cpContext := component.WorkloadContext{
			HCP: &hyperv1.HostedControlPlane{
				Spec: hyperv1.HostedControlPlaneSpec{
					InfraID: "test-infra-123",
					SecretEncryption: &hyperv1.SecretEncryptionSpec{
						Type: hyperv1.AESCBC,
						AESCBC: &hyperv1.AESCBCSpec{
							ActiveKey: corev1.LocalObjectReference{Name: "my-encryption-key"},
						},
					},
					Etcd: hyperv1.EtcdSpec{
						ManagementType: hyperv1.Managed,
						Managed: &hyperv1.ManagedEtcdSpec{
							AutomatedBackup: &hyperv1.AutomatedEtcdBackupConfig{
								Schedule: "0 */4 * * *",
								Storage: hyperv1.AutomatedEtcdBackupStorage{
									Type: hyperv1.AutomatedEtcdBackupStorageTypeGCS,
									GCS: &hyperv1.AutomatedEtcdBackupGCS{
										Bucket:            "my-bucket",
										GCPServiceAccount: "backup@proj.iam.gserviceaccount.com",
									},
								},
							},
						},
					},
				},
			},
		}
		cronJob := &batchv1.CronJob{}

		err := adaptCronJob(cpContext, cronJob)
		g.Expect(err).ToNot(HaveOccurred())

		volumeNames := make([]string, 0, len(cronJob.Spec.JobTemplate.Spec.Template.Spec.Volumes))
		volumeSecretNames := make(map[string]string)
		for _, v := range cronJob.Spec.JobTemplate.Spec.Template.Spec.Volumes {
			volumeNames = append(volumeNames, v.Name)
			if v.Secret != nil {
				volumeSecretNames[v.Name] = v.Secret.SecretName
			}
		}
		g.Expect(volumeNames).To(ContainElements("root-ca", "etcd-signer", "sa-signing-key", "aescbc-active-key"))
		g.Expect(volumeSecretNames["aescbc-active-key"]).To(Equal("my-encryption-key"))
		g.Expect(volumeNames).ToNot(ContainElement("aescbc-backup-key"))

		uploadContainer := cronJob.Spec.JobTemplate.Spec.Template.Spec.Containers[0]
		mountPaths := make(map[string]string)
		for _, m := range uploadContainer.VolumeMounts {
			mountPaths[m.Name] = m.MountPath
		}
		g.Expect(mountPaths).To(HaveKey("aescbc-active-key"))
		g.Expect(mountPaths["aescbc-active-key"]).To(Equal("/tmp/etcd-backup/secrets/my-encryption-key"))
	})

	t.Run("When AESCBC encryption is configured with backup key it should include both key volumes", func(t *testing.T) {
		g := NewGomegaWithT(t)
		cpContext := component.WorkloadContext{
			HCP: &hyperv1.HostedControlPlane{
				Spec: hyperv1.HostedControlPlaneSpec{
					InfraID: "test-infra-123",
					SecretEncryption: &hyperv1.SecretEncryptionSpec{
						Type: hyperv1.AESCBC,
						AESCBC: &hyperv1.AESCBCSpec{
							ActiveKey: corev1.LocalObjectReference{Name: "my-encryption-key"},
							BackupKey: &corev1.LocalObjectReference{Name: "my-backup-key"},
						},
					},
					Etcd: hyperv1.EtcdSpec{
						ManagementType: hyperv1.Managed,
						Managed: &hyperv1.ManagedEtcdSpec{
							AutomatedBackup: &hyperv1.AutomatedEtcdBackupConfig{
								Schedule: "0 */4 * * *",
								Storage: hyperv1.AutomatedEtcdBackupStorage{
									Type: hyperv1.AutomatedEtcdBackupStorageTypeGCS,
									GCS: &hyperv1.AutomatedEtcdBackupGCS{
										Bucket:            "my-bucket",
										GCPServiceAccount: "backup@proj.iam.gserviceaccount.com",
									},
								},
							},
						},
					},
				},
			},
		}
		cronJob := &batchv1.CronJob{}

		err := adaptCronJob(cpContext, cronJob)
		g.Expect(err).ToNot(HaveOccurred())

		volumeNames := make([]string, 0, len(cronJob.Spec.JobTemplate.Spec.Template.Spec.Volumes))
		volumeSecretNames := make(map[string]string)
		for _, v := range cronJob.Spec.JobTemplate.Spec.Template.Spec.Volumes {
			volumeNames = append(volumeNames, v.Name)
			if v.Secret != nil {
				volumeSecretNames[v.Name] = v.Secret.SecretName
			}
		}
		g.Expect(volumeNames).To(ContainElements("aescbc-active-key", "aescbc-backup-key"))
		g.Expect(volumeSecretNames["aescbc-active-key"]).To(Equal("my-encryption-key"))
		g.Expect(volumeSecretNames["aescbc-backup-key"]).To(Equal("my-backup-key"))

		uploadContainer := cronJob.Spec.JobTemplate.Spec.Template.Spec.Containers[0]
		mountPaths := make(map[string]string)
		for _, m := range uploadContainer.VolumeMounts {
			mountPaths[m.Name] = m.MountPath
		}
		g.Expect(mountPaths).To(HaveKey("aescbc-active-key"))
		g.Expect(mountPaths["aescbc-active-key"]).To(Equal("/tmp/etcd-backup/secrets/my-encryption-key"))
		g.Expect(mountPaths).To(HaveKey("aescbc-backup-key"))
		g.Expect(mountPaths["aescbc-backup-key"]).To(Equal("/tmp/etcd-backup/secrets/my-backup-key"))
	})
}

func TestAdaptServiceAccount(t *testing.T) {
	t.Run("When adapting ServiceAccount it should set GKE WI annotation", func(t *testing.T) {
		g := NewGomegaWithT(t)
		cpContext := component.WorkloadContext{
			HCP: &hyperv1.HostedControlPlane{
				Spec: hyperv1.HostedControlPlaneSpec{
					Etcd: hyperv1.EtcdSpec{
						ManagementType: hyperv1.Managed,
						Managed: &hyperv1.ManagedEtcdSpec{
							AutomatedBackup: &hyperv1.AutomatedEtcdBackupConfig{
								Storage: hyperv1.AutomatedEtcdBackupStorage{
									Type: hyperv1.AutomatedEtcdBackupStorageTypeGCS,
									GCS: &hyperv1.AutomatedEtcdBackupGCS{
										Bucket:            "my-bucket",
										GCPServiceAccount: "backup@proj.iam.gserviceaccount.com",
									},
								},
							},
						},
					},
				},
			},
		}
		sa := &corev1.ServiceAccount{}

		err := adaptServiceAccount(cpContext, sa)
		g.Expect(err).ToNot(HaveOccurred())
		g.Expect(sa.Annotations["iam.gke.io/gcp-service-account"]).To(Equal("backup@proj.iam.gserviceaccount.com"))
	})
}
