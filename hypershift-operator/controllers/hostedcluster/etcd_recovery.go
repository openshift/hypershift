package hostedcluster

import (
	"context"
	"errors"
	"fmt"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/utils/ptr"
	ctrl "sigs.k8s.io/controller-runtime"
	crclient "sigs.k8s.io/controller-runtime/pkg/client"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	"github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/common"
	cpomanifests "github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/manifests"
	"github.com/openshift/hypershift/hypershift-operator/controllers/manifests"
	etcdrecoverymanifests "github.com/openshift/hypershift/hypershift-operator/controllers/manifests/etcdrecovery"
	"github.com/openshift/hypershift/support/upsert"
	hyperutil "github.com/openshift/hypershift/support/util"
)

type etcdJobStatus struct {
	exists     bool
	finished   bool
	successful bool
}

func (r *HostedClusterReconciler) reconcileETCDMemberRecovery(ctx context.Context, hcluster *hyperv1.HostedCluster, createOrUpdate upsert.CreateOrUpdateFN) (*time.Duration, error) {
	log := ctrl.LoggerFrom(ctx)
	hcpNS := manifests.HostedControlPlaneNamespace(hcluster.Namespace, hcluster.Name)

	// Check the recovery job
	recoveryJob := etcdrecoverymanifests.EtcdRecoveryJob(hcpNS)
	jobStatus, err := r.etcdRecoveryJobStatus(ctx, recoveryJob)
	if err != nil {
		return nil, err
	}

	etcdRecoveryActiveCondition := metav1.Condition{
		Type:               string(hyperv1.EtcdRecoveryActive),
		ObservedGeneration: hcluster.Generation,
	}

	if jobStatus.exists {
		if !jobStatus.finished {
			log.Info("waiting for etcd recovery job to complete")
			return nil, nil
		}

		if !jobStatus.successful {
			etcdRecoveryActiveCondition.Status = metav1.ConditionFalse
			etcdRecoveryActiveCondition.Reason = hyperv1.EtcdRecoveryJobFailedReason
			etcdRecoveryActiveCondition.Message = "Error in Etcd Recovery job: the Etcd cluster requires manual intervention."
			etcdRecoveryActiveCondition.LastTransitionTime = r.now()

			oldCondition := meta.FindStatusCondition(hcluster.Status.Conditions, string(hyperv1.EtcdRecoveryActive))

			if oldCondition == nil || oldCondition.Status != etcdRecoveryActiveCondition.Status {
				meta.SetStatusCondition(&hcluster.Status.Conditions, etcdRecoveryActiveCondition)
				if err := r.Client.Status().Update(ctx, hcluster); err != nil {
					return nil, fmt.Errorf("failed to update etcd recovery job condition: %w", err)
				}
			}

			// There is no benefit in requeuing, since the cluster needs manual intervention
			log.Error(errors.New("etcd recovery failed"), "failed recovery job exists", "job", crclient.ObjectKeyFromObject(recoveryJob).String())
			return nil, nil
		}

		// Cleanup ETCD Recovery objects
		if err := r.cleanupEtcdRecoveryObjects(ctx, hcluster); err != nil {
			return nil, fmt.Errorf("failed to cleanup etcd recovery job: %w", err)
		}

		etcdRecoveryActiveCondition.Status = metav1.ConditionFalse
		etcdRecoveryActiveCondition.Reason = hyperv1.AsExpectedReason
		etcdRecoveryActiveCondition.Message = "ETCD Recovery job succeeded."
		etcdRecoveryActiveCondition.LastTransitionTime = r.now()

		oldCondition := meta.FindStatusCondition(hcluster.Status.Conditions, string(hyperv1.EtcdRecoveryActive))

		if oldCondition == nil || oldCondition.Status != etcdRecoveryActiveCondition.Status {
			meta.SetStatusCondition(&hcluster.Status.Conditions, etcdRecoveryActiveCondition)
			if err := r.Client.Status().Update(ctx, hcluster); err != nil {
				return nil, fmt.Errorf("failed to update etcd recovery job condition: %w", err)
			}
		}
	}

	etcdStatefulSet := etcdrecoverymanifests.EtcdStatefulSet(hcpNS)
	if err := r.Get(ctx, crclient.ObjectKeyFromObject(etcdStatefulSet), etcdStatefulSet); err != nil {
		if apierrors.IsNotFound(err) {
			log.Info("etcd statefulset does not yet exist")
			return nil, nil
		}
	}
	fullyAvailable := etcdStatefulSet.Status.ReadyReplicas == 3 && etcdStatefulSet.Status.AvailableReplicas == 3
	if !fullyAvailable {
		log.Info("etcd is not reporting fully available, need to watch")
	}
	requeueAfter := etcdCheckRequeueInterval

	etcdPodList := &corev1.PodList{}
	if err := r.List(ctx, etcdPodList, crclient.InNamespace(hcpNS), crclient.MatchingLabels{
		"app": "etcd",
	}); err != nil {
		return nil, fmt.Errorf("failed to list etcd pods: %w", err)
	}

	if len(etcdPodList.Items) < 3 {
		// Cannot initiate recovery without all etcd pods, let's requeue
		return &requeueAfter, nil
	}

	var failingEtcdPod *corev1.Pod
	for _, pod := range etcdPodList.Items {
		for _, containerStatus := range pod.Status.ContainerStatuses {
			if containerStatus.State.Waiting != nil && containerStatus.RestartCount > 0 && containerStatus.Name == "etcd" {
				failingEtcdPod = &pod
				log.Info("detected etcd failing pod", "name", pod.Name, "namespace", pod.Namespace)
				break
			}
		}
	}

	if failingEtcdPod == nil {
		// No failing etcd pods detected
		// However, if the statefulset is not reporting fully available, check later
		if !fullyAvailable {
			return &requeueAfter, nil
		}
		return nil, nil
	}

	log.Info("there are symptoms of etcd cluster degradation, triggering recovery job")

	recoveryRole := etcdrecoverymanifests.EtcdRecoveryRole(hcpNS)
	if _, err := createOrUpdate(ctx, r.Client, recoveryRole, func() error {
		r.reconcileEtcdRecoveryRole(recoveryRole)
		return nil
	}); err != nil {
		return nil, fmt.Errorf("failed to reconcile etcd recovery role: %w", err)
	}

	recoverySA := etcdrecoverymanifests.EtcdRecoveryServiceAccount(hcpNS)
	if _, err := createOrUpdate(ctx, r.Client, recoverySA, func() error {
		hyperutil.EnsurePullSecret(recoverySA, common.PullSecret("").Name)
		return nil
	}); err != nil {
		return nil, fmt.Errorf("failed to reconcile etcd-recovery job service account: %w", err)
	}

	recoveryRoleBinding := etcdrecoverymanifests.EtcdRecoveryRoleBinding(hcpNS)
	if _, err := createOrUpdate(ctx, r.Client, recoveryRoleBinding, func() error {
		r.reconcileEtcdRecoveryRoleBinding(recoveryRoleBinding, recoveryRole, recoverySA)
		return nil
	}); err != nil {
		return nil, fmt.Errorf("failed to reconcile etcd recovery role binding: %w", err)
	}

	if _, err := createOrUpdate(ctx, r.Client, recoveryJob, func() error {
		return r.reconcileEtcdRecoveryJob(recoveryJob, hcluster)
	}); err != nil {
		return nil, fmt.Errorf("failed to reconcile etcd recovery job: %w", err)
	}

	// Creating the condition for the first time or in the case of the ETCD fails intermitently
	etcdRecoveryActiveCondition.Status = metav1.ConditionTrue
	etcdRecoveryActiveCondition.Reason = hyperv1.AsExpectedReason
	etcdRecoveryActiveCondition.Message = "ETCD Recovery job in progress."
	etcdRecoveryActiveCondition.LastTransitionTime = r.now()

	// If the ETCD keeps failing and recovering, we can see the hcluster.Generation increasing indefinitely.
	oldCondition := meta.FindStatusCondition(hcluster.Status.Conditions, string(hyperv1.EtcdRecoveryActive))

	if oldCondition == nil || oldCondition.Status != etcdRecoveryActiveCondition.Status {
		meta.SetStatusCondition(&hcluster.Status.Conditions, etcdRecoveryActiveCondition)
		if err := r.Client.Status().Update(ctx, hcluster); err != nil {
			return nil, fmt.Errorf("failed to update etcd recovery job condition: %w", err)
		}
	}

	return nil, nil
}

// etcdRecoveryJobStatus checks the status of the ETCD recovery job and returns a status
func (r *HostedClusterReconciler) etcdRecoveryJobStatus(ctx context.Context, job *batchv1.Job) (*etcdJobStatus, error) {
	log := ctrl.LoggerFrom(ctx)
	result := &etcdJobStatus{}

	// Check the job's status
	if err := r.Get(ctx, crclient.ObjectKeyFromObject(job), job); err != nil {
		if apierrors.IsNotFound(err) {
			return result, nil
		}
		return nil, fmt.Errorf("failed to get etcd recovery job: %w", err)
	}

	result.exists = true
	for _, cond := range job.Status.Conditions {
		switch cond.Type {
		case batchv1.JobComplete:
			if cond.Status == corev1.ConditionTrue {
				result.finished = true
				result.successful = true
			}
		case batchv1.JobFailed:
			if cond.Status == corev1.ConditionTrue {
				result.finished = true
				result.successful = false
			}
		}
	}
	if result.finished {
		if result.successful {
			log.Info("etcd recovery job completed successfully")
		} else {
			log.Info("etcd recovery job failed")
		}
	}
	return result, nil
}

func (r *HostedClusterReconciler) cleanupEtcdRecoveryObjects(ctx context.Context, hcluster *hyperv1.HostedCluster) error {
	hcpNS := manifests.HostedControlPlaneNamespace(hcluster.Namespace, hcluster.Name)

	recoveryJob := etcdrecoverymanifests.EtcdRecoveryJob(hcpNS)
	if _, err := hyperutil.DeleteIfNeededWithOptions(ctx, r.Client, recoveryJob, crclient.PropagationPolicy(metav1.DeletePropagationBackground)); err != nil {
		return fmt.Errorf("failed to cleanup etcd recovery job: %w", err)
	}

	recoverySA := etcdrecoverymanifests.EtcdRecoveryServiceAccount(hcpNS)
	if _, err := hyperutil.DeleteIfNeeded(ctx, r.Client, recoverySA); err != nil {
		return fmt.Errorf("failed to cleanup etcd-recovery job service account: %w", err)
	}

	recoveryRoleBinding := etcdrecoverymanifests.EtcdRecoveryRoleBinding(hcpNS)
	if _, err := hyperutil.DeleteIfNeeded(ctx, r.Client, recoveryRoleBinding); err != nil {
		return fmt.Errorf("failed to cleanup etcd-recovery role binding: %w", err)
	}

	recoveryRole := etcdrecoverymanifests.EtcdRecoveryRole(hcpNS)
	if _, err := hyperutil.DeleteIfNeeded(ctx, r.Client, recoveryRole); err != nil {
		return fmt.Errorf("failed to cleanup etcd-recovery role: %w", err)
	}

	return nil
}

func (r *HostedClusterReconciler) reconcileEtcdRecoveryRole(role *rbacv1.Role) {
	role.Rules = []rbacv1.PolicyRule{
		{
			APIGroups: []string{""},
			Resources: []string{
				"pods",
				"persistentvolumeclaims",
			},
			Verbs: []string{"get", "list", "delete"},
		},
		{
			APIGroups: []string{appsv1.SchemeGroupVersion.Group},
			Resources: []string{
				"statefulsets",
			},
			Verbs: []string{"get", "list"},
		},
	}
}

func (r *HostedClusterReconciler) reconcileEtcdRecoveryRoleBinding(roleBinding *rbacv1.RoleBinding, role *rbacv1.Role, sa *corev1.ServiceAccount) {
	roleBinding.RoleRef = rbacv1.RoleRef{
		APIGroup: rbacv1.GroupName,
		Kind:     "Role",
		Name:     role.Name,
	}
	roleBinding.Subjects = []rbacv1.Subject{
		{
			Kind:      "ServiceAccount",
			Name:      sa.Name,
			Namespace: sa.Namespace,
		},
	}
}

func (r *HostedClusterReconciler) reconcileEtcdRecoveryJob(job *batchv1.Job, hc *hyperv1.HostedCluster) error {
	if job.Labels == nil {
		job.Labels = map[string]string{}
	}
	job.Labels[jobHostedClusterNameLabel] = hc.Name
	job.Labels[jobHostedClusterNamespaceLabel] = hc.Namespace
	job.Spec = batchv1.JobSpec{
		Completions:  ptr.To[int32](1),
		BackoffLimit: ptr.To[int32](10),
		Template: corev1.PodTemplateSpec{
			ObjectMeta: metav1.ObjectMeta{
				Labels: map[string]string{
					NeedManagementKASAccessLabel: "true",
				},
			},
			Spec: corev1.PodSpec{
				Containers: []corev1.Container{
					{
						Name:            "etcd-recovery",
						Image:           r.HypershiftOperatorImage,
						ImagePullPolicy: corev1.PullIfNotPresent,
						Command: []string{
							"/usr/bin/hypershift-operator",
							"recover-etcd",
						},
						Args: []string{
							"run",
							"--etcd-ca-cert",
							"/etc/etcd/tls/etcd-ca/ca.crt",
							"--etcd-client-cert",
							"/etc/etcd/tls/client/etcd-client.crt",
							"--etcd-client-key",
							"/etc/etcd/tls/client/etcd-client.key",
							"--namespace",
							"$(NAMESPACE)",
						},
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
								MountPath: "/etc/etcd/tls/client",
								Name:      "client-tls",
							},
							{
								MountPath: "/etc/etcd/tls/etcd-ca",
								Name:      "etcd-ca",
							},
						},
					},
				},
				RestartPolicy:      corev1.RestartPolicyNever,
				ServiceAccountName: etcdrecoverymanifests.EtcdRecoveryServiceAccount("").Name,
				Volumes: []corev1.Volume{
					{
						Name: "client-tls",
						VolumeSource: corev1.VolumeSource{
							Secret: &corev1.SecretVolumeSource{
								SecretName:  cpomanifests.EtcdClientSecret("").Name,
								DefaultMode: ptr.To[int32](420),
							},
						},
					},
					{
						Name: "etcd-ca",
						VolumeSource: corev1.VolumeSource{
							ConfigMap: &corev1.ConfigMapVolumeSource{
								LocalObjectReference: corev1.LocalObjectReference{
									Name: cpomanifests.EtcdSignerCAConfigMap("").Name,
								},
								DefaultMode: ptr.To[int32](420),
							},
						},
					},
				},
			},
		},
	}

	return nil
}
