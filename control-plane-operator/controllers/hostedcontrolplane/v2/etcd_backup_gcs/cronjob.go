package etcdbackupgcs

import (
	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	component "github.com/openshift/hypershift/support/controlplane-component"

	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	"k8s.io/utils/ptr"
)

func adaptCronJob(cpContext component.WorkloadContext, cronJob *batchv1.CronJob) error {
	hcp := cpContext.HCP
	if hcp.Spec.Etcd.Managed == nil || hcp.Spec.Etcd.Managed.AutomatedBackup == nil {
		return nil
	}
	backupConfig := hcp.Spec.Etcd.Managed.AutomatedBackup
	infraID := hcp.Spec.InfraID

	cronJob.Spec.Schedule = backupConfig.Schedule
	cronJob.Spec.ConcurrencyPolicy = batchv1.ForbidConcurrent
	cronJob.Spec.SuccessfulJobsHistoryLimit = ptr.To(int32(3))
	cronJob.Spec.FailedJobsHistoryLimit = ptr.To(int32(3))

	cronJob.Spec.JobTemplate.Spec.Template.Spec.InitContainers = []corev1.Container{
		{
			Name:            "snapshot",
			Image:           "etcd",
			ImagePullPolicy: corev1.PullIfNotPresent,
			Command:         []string{"etcdctl"},
			Args: []string{
				"snapshot", "save", "/tmp/etcd-backup/snapshot.db",
				"--endpoints=https://etcd-client:2379",
				"--cacert=/etc/etcd/tls/etcd-ca/ca.crt",
				"--cert=/etc/etcd/tls/client/etcd-client.crt",
				"--key=/etc/etcd/tls/client/etcd-client.key",
			},
			Env: []corev1.EnvVar{
				{Name: "ETCDCTL_API", Value: "3"},
			},
			VolumeMounts: []corev1.VolumeMount{
				{Name: "client-tls", MountPath: "/etc/etcd/tls/client"},
				{Name: "etcd-ca", MountPath: "/etc/etcd/tls/etcd-ca"},
				{Name: "etcd-snapshot", MountPath: "/tmp/etcd-backup"},
			},
			Resources: corev1.ResourceRequirements{
				Requests: corev1.ResourceList{
					corev1.ResourceCPU:    resource.MustParse("10m"),
					corev1.ResourceMemory: resource.MustParse("80Mi"),
				},
			},
		},
	}

	cronJob.Spec.JobTemplate.Spec.Template.Spec.Containers = []corev1.Container{
		{
			Name:            "upload",
			Image:           "controlplane-operator",
			ImagePullPolicy: corev1.PullIfNotPresent,
			Command:         []string{"control-plane-operator"},
			Args: []string{
				"etcd-upload",
				"--storage-type", "GCS",
				"--gcs-bucket", backupConfig.Storage.GCS.Bucket,
				"--key-prefix", infraID,
				"--snapshot-path", "/tmp/etcd-backup/snapshot.db",
				"--secrets-dir", "/tmp/etcd-backup/secrets",
			},
			VolumeMounts: []corev1.VolumeMount{
				{Name: "etcd-snapshot", MountPath: "/tmp/etcd-backup"},
				{Name: "root-ca", MountPath: "/tmp/etcd-backup/secrets/root-ca"},
				{Name: "etcd-signer", MountPath: "/tmp/etcd-backup/secrets/etcd-signer"},
				{Name: "sa-signing-key", MountPath: "/tmp/etcd-backup/secrets/sa-signing-key"},
			},
			Resources: corev1.ResourceRequirements{
				Requests: corev1.ResourceList{
					corev1.ResourceCPU:    resource.MustParse("10m"),
					corev1.ResourceMemory: resource.MustParse("80Mi"),
				},
			},
		},
	}

	cronJob.Spec.JobTemplate.Spec.Template.Spec.Volumes = []corev1.Volume{
		{
			Name: "client-tls",
			VolumeSource: corev1.VolumeSource{
				Secret: &corev1.SecretVolumeSource{
					SecretName:  "etcd-client-tls",
					DefaultMode: ptr.To(int32(0640)),
				},
			},
		},
		{
			Name: "etcd-ca",
			VolumeSource: corev1.VolumeSource{
				ConfigMap: &corev1.ConfigMapVolumeSource{
					LocalObjectReference: corev1.LocalObjectReference{
						Name: "etcd-ca",
					},
				},
			},
		},
		{
			Name: "etcd-snapshot",
			VolumeSource: corev1.VolumeSource{
				EmptyDir: &corev1.EmptyDirVolumeSource{},
			},
		},
		{
			Name: "root-ca",
			VolumeSource: corev1.VolumeSource{
				Secret: &corev1.SecretVolumeSource{
					SecretName:  "root-ca",
					DefaultMode: ptr.To(int32(0640)),
				},
			},
		},
		{
			Name: "etcd-signer",
			VolumeSource: corev1.VolumeSource{
				Secret: &corev1.SecretVolumeSource{
					SecretName:  "etcd-signer",
					DefaultMode: ptr.To(int32(0640)),
				},
			},
		},
		{
			Name: "sa-signing-key",
			VolumeSource: corev1.VolumeSource{
				Secret: &corev1.SecretVolumeSource{
					SecretName:  "sa-signing-key",
					DefaultMode: ptr.To(int32(0640)),
				},
			},
		},
	}

	cronJob.Spec.JobTemplate.Spec.Template.Spec.RestartPolicy = corev1.RestartPolicyNever
	cronJob.Spec.JobTemplate.Spec.Template.Spec.ServiceAccountName = ComponentName

	if hcp.Spec.SecretEncryption != nil && hcp.Spec.SecretEncryption.Type == hyperv1.AESCBC && hcp.Spec.SecretEncryption.AESCBC != nil {
		activeKeyName := hcp.Spec.SecretEncryption.AESCBC.ActiveKey.Name
		cronJob.Spec.JobTemplate.Spec.Template.Spec.Volumes = append(cronJob.Spec.JobTemplate.Spec.Template.Spec.Volumes,
			corev1.Volume{
				Name: "aescbc-active-key",
				VolumeSource: corev1.VolumeSource{
					Secret: &corev1.SecretVolumeSource{
						SecretName:  activeKeyName,
						DefaultMode: ptr.To(int32(0640)),
					},
				},
			},
		)
		cronJob.Spec.JobTemplate.Spec.Template.Spec.Containers[0].VolumeMounts = append(
			cronJob.Spec.JobTemplate.Spec.Template.Spec.Containers[0].VolumeMounts,
			corev1.VolumeMount{Name: "aescbc-active-key", MountPath: "/tmp/etcd-backup/secrets/" + activeKeyName},
		)

		if hcp.Spec.SecretEncryption.AESCBC.BackupKey != nil {
			backupKeyName := hcp.Spec.SecretEncryption.AESCBC.BackupKey.Name
			cronJob.Spec.JobTemplate.Spec.Template.Spec.Volumes = append(cronJob.Spec.JobTemplate.Spec.Template.Spec.Volumes,
				corev1.Volume{
					Name: "aescbc-backup-key",
					VolumeSource: corev1.VolumeSource{
						Secret: &corev1.SecretVolumeSource{
							SecretName:  backupKeyName,
							DefaultMode: ptr.To(int32(0640)),
						},
					},
				},
			)
			cronJob.Spec.JobTemplate.Spec.Template.Spec.Containers[0].VolumeMounts = append(
				cronJob.Spec.JobTemplate.Spec.Template.Spec.Containers[0].VolumeMounts,
				corev1.VolumeMount{Name: "aescbc-backup-key", MountPath: "/tmp/etcd-backup/secrets/" + backupKeyName},
			)
		}
	}

	return nil
}
