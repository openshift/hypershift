package etcd

import (
	_ "embed"
	"fmt"
	"strings"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	"github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/manifests"
	component "github.com/openshift/hypershift/support/controlplane-component"
	"github.com/openshift/hypershift/support/netutil"
	"github.com/openshift/hypershift/support/podspec"

	appsv1 "k8s.io/api/apps/v1"
	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/apimachinery/pkg/api/resource"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/utils/ptr"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

func adaptStatefulSet(cpContext component.WorkloadContext, sts *appsv1.StatefulSet) error {
	hcp := cpContext.HCP
	managedEtcdSpec := hcp.Spec.Etcd.Managed

	ipv4, err := netutil.IsIPv4CIDR(hcp.Spec.Networking.ClusterNetwork[0].CIDR.String())
	if err != nil {
		return fmt.Errorf("error checking the ClusterNetworkCIDR: %v", err)
	}

	podspec.UpdateContainer(ComponentName, sts.Spec.Template.Spec.Containers, func(c *corev1.Container) {
		replicas := component.DefaultReplicas(hcp, &etcd{}, ComponentName)
		var members []string
		for i := range replicas {
			name := fmt.Sprintf("etcd-%d", i)
			members = append(members, fmt.Sprintf("%s=https://%s.etcd-discovery.%s.svc:2380", name, name, hcp.Namespace))
		}
		c.Env = append(c.Env,
			corev1.EnvVar{
				Name:  "ETCD_INITIAL_CLUSTER",
				Value: strings.Join(members, ","),
			},
		)

		if !ipv4 {
			podspec.UpsertEnvVar(c, corev1.EnvVar{
				Name:  "ETCD_LISTEN_PEER_URLS",
				Value: "https://[$(POD_IP)]:2380",
			})
			podspec.UpsertEnvVar(c, corev1.EnvVar{
				Name:  "ETCD_LISTEN_CLIENT_URLS",
				Value: "https://[$(POD_IP)]:2379,https://localhost:2379",
			})
			podspec.UpsertEnvVar(c, corev1.EnvVar{
				Name:  "ETCD_LISTEN_METRICS_URLS",
				Value: "https://[::]:2382",
			})
		}
	})

	podspec.UpdateContainer("etcd-metrics", sts.Spec.Template.Spec.Containers, func(c *corev1.Container) {
		var loInterface, allInterfaces string
		if ipv4 {
			loInterface = "127.0.0.1"
			allInterfaces = "0.0.0.0"
		} else {
			loInterface = "[::1]"
			allInterfaces = "[::]"
		}
		c.Args = append(c.Args,
			fmt.Sprintf("--listen-addr=%s:2383", loInterface),
			fmt.Sprintf("--metrics-addr=https://%s:2381", allInterfaces),
			"--advertise-client-url=",
		)
	})

	if defragControllerPredicate(cpContext) {
		sts.Spec.Template.Spec.Containers = append(sts.Spec.Template.Spec.Containers, buildEtcdDefragControllerContainer(hcp.Namespace))
		sts.Spec.Template.Spec.ServiceAccountName = manifests.EtcdDefragControllerServiceAccount("").Name
	}

	snapshotRestored := meta.IsStatusConditionTrue(hcp.Status.Conditions, string(hyperv1.EtcdSnapshotRestored))
	if managedEtcdSpec != nil && len(managedEtcdSpec.Storage.RestoreSnapshotURL) > 0 && !snapshotRestored {
		sts.Spec.Template.Spec.InitContainers = append(sts.Spec.Template.Spec.InitContainers,
			buildEtcdInitContainer(managedEtcdSpec.Storage.RestoreSnapshotURL[0]), // RestoreSnapshotURL can only have 1 entry
		)
	}

	// Automated restore: inject init container if restore Job completed with a snapshot
	if hcp.Spec.Etcd.Managed != nil &&
		hcp.Spec.Etcd.Managed.AutomatedBackup != nil &&
		!snapshotRestored {
		shouldRestore, err := shouldInjectRestore(cpContext)
		if err != nil {
			return err
		}
		if shouldRestore {
			sts.Spec.Template.Spec.InitContainers = append(sts.Spec.Template.Spec.InitContainers,
				buildRestoreInitContainer(),
			)
			sts.Spec.Template.Spec.Volumes = append(sts.Spec.Template.Spec.Volumes,
				corev1.Volume{
					Name: "restore-snapshot",
					VolumeSource: corev1.VolumeSource{
						PersistentVolumeClaim: &corev1.PersistentVolumeClaimVolumeSource{
							ClaimName: "etcd-restore-snapshot",
						},
					},
				},
			)
			podspec.UpdateContainer("reset-member", sts.Spec.Template.Spec.InitContainers, func(c *corev1.Container) {
				podspec.UpsertEnvVar(c, corev1.EnvVar{
					Name:  "SNAPSHOT_RESTORE",
					Value: "true",
				})
			})
			sts.Spec.Replicas = ptr.To(int32(1))
		}
	}

	// adapt PersistentVolume
	if managedEtcdSpec != nil && managedEtcdSpec.Storage.Type == hyperv1.PersistentVolumeEtcdStorage {
		if pv := managedEtcdSpec.Storage.PersistentVolume; pv != nil {
			sts.Spec.VolumeClaimTemplates[0].Spec.StorageClassName = pv.StorageClassName
			if pv.Size != nil {
				sts.Spec.VolumeClaimTemplates[0].Spec.Resources = corev1.VolumeResourceRequirements{
					Requests: corev1.ResourceList{
						corev1.ResourceStorage: *pv.Size,
					},
				}
			}
		}
	}

	return nil
}

//go:embed etcd-init.sh
var etcdInitScript string

func buildEtcdInitContainer(restoreUrl string) corev1.Container {
	c := corev1.Container{
		Name: "etcd-init",
	}
	c.Env = []corev1.EnvVar{
		{
			Name:  "RESTORE_URL_ETCD",
			Value: restoreUrl,
		},
	}
	c.Image = "etcd"
	c.ImagePullPolicy = corev1.PullIfNotPresent
	c.Command = []string{"/bin/sh", "-ce", etcdInitScript}
	c.VolumeMounts = []corev1.VolumeMount{
		{
			Name:      "data",
			MountPath: "/var/lib",
		},
	}
	return c
}

func buildEtcdDefragControllerContainer(namespace string) corev1.Container {
	c := corev1.Container{
		Name: "etcd-defrag",
	}
	c.Image = "controlplane-operator"
	c.ImagePullPolicy = corev1.PullIfNotPresent
	c.Command = []string{"control-plane-operator"}
	c.Args = []string{
		"etcd-defrag-controller",
		"--namespace",
		namespace,
	}
	c.VolumeMounts = []corev1.VolumeMount{
		{
			Name:      "client-tls",
			MountPath: "/etc/etcd/tls/client",
		},
		{
			Name:      "etcd-ca",
			MountPath: "/etc/etcd/tls/etcd-ca",
		},
	}
	c.Resources = corev1.ResourceRequirements{
		Requests: corev1.ResourceList{
			corev1.ResourceCPU:    resource.MustParse("10m"),
			corev1.ResourceMemory: resource.MustParse("50Mi"),
		},
	}
	return c
}

//go:embed etcd-restore.sh
var etcdRestoreScript string

func shouldInjectRestore(cpContext component.WorkloadContext) (bool, error) {
	job := &batchv1.Job{}
	jobKey := client.ObjectKey{
		Namespace: cpContext.HCP.Namespace,
		Name:      manifests.EtcdRestoreJob("").Name,
	}
	if err := cpContext.Client.Get(cpContext, jobKey, job); err != nil {
		return false, fmt.Errorf("waiting for GCS restore job: %w", err)
	}

	for _, cond := range job.Status.Conditions {
		if cond.Type == batchv1.JobComplete && cond.Status == corev1.ConditionTrue {
			msg := getJobTerminationMessage(cpContext, job)
			if strings.HasPrefix(msg, "no-snapshot") || strings.HasPrefix(msg, "no-secrets") {
				return false, nil
			}
			return true, nil
		}
		if cond.Type == batchv1.JobFailed && cond.Status == corev1.ConditionTrue {
			return false, fmt.Errorf("GCS restore job failed: %s", cond.Message)
		}
	}

	return false, fmt.Errorf("GCS restore job not yet complete")
}

func getJobTerminationMessage(cpContext component.WorkloadContext, job *batchv1.Job) string {
	podList := &corev1.PodList{}
	if err := cpContext.Client.List(cpContext, podList, &client.ListOptions{
		Namespace: job.Namespace,
		LabelSelector: labels.SelectorFromValidatedSet(labels.Set{
			"job-name": job.Name,
		}),
	}); err != nil {
		return ""
	}
	for _, pod := range podList.Items {
		for _, cs := range pod.Status.ContainerStatuses {
			if cs.State.Terminated != nil && cs.State.Terminated.Message != "" {
				return cs.State.Terminated.Message
			}
		}
	}
	return ""
}

func buildRestoreInitContainer() corev1.Container {
	return corev1.Container{
		Name:            "etcd-restore",
		Image:           "etcd",
		ImagePullPolicy: corev1.PullIfNotPresent,
		Command:         []string{"/bin/sh", "-ce", etcdRestoreScript},
		Env: []corev1.EnvVar{
			{
				Name: "NAMESPACE",
				ValueFrom: &corev1.EnvVarSource{
					FieldRef: &corev1.ObjectFieldSelector{
						FieldPath: "metadata.namespace",
					},
				},
			},
		},
		VolumeMounts: []corev1.VolumeMount{
			{
				Name:      "data",
				MountPath: "/var/lib",
			},
			{
				Name:      "restore-snapshot",
				MountPath: "/snapshot",
			},
		},
	}
}
