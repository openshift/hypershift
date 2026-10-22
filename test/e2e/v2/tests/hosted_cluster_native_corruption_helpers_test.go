//go:build e2ev2

package tests

import (
	"context"
	"fmt"
	"time"

	etcdrecoverymanifests "github.com/openshift/hypershift/pkg/manifests/etcdrecovery"

	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/types"

	crclient "sigs.k8s.io/controller-runtime/pkg/client"
)

func nativeCorruptionMemberStatus(ctx context.Context, reader crclient.Reader, selected corev1.Pod) (*corev1.ContainerStatus, error) {
	pods := &corev1.PodList{}
	if err := reader.List(ctx, pods, crclient.InNamespace(selected.Namespace), crclient.MatchingLabels{"app": "etcd"}); err != nil {
		return nil, fmt.Errorf("list corruption members: %w", err)
	}
	if len(pods.Items) != 3 {
		return nil, fmt.Errorf("corruption attribution requires three etcd members, got %d", len(pods.Items))
	}
	var chosen *corev1.ContainerStatus
	for _, pod := range pods.Items {
		var status *corev1.ContainerStatus
		for index := range pod.Status.ContainerStatuses {
			if pod.Status.ContainerStatuses[index].Name == "etcd" {
				status = &pod.Status.ContainerStatuses[index]
				break
			}
		}
		if status == nil {
			return nil, fmt.Errorf("member %s lacks etcd status", pod.Name)
		}
		if pod.Name == selected.Name {
			if pod.UID != selected.UID {
				return nil, fmt.Errorf("selected member %s UID changed", pod.Name)
			}
			chosen = status
		} else if status.State.Waiting != nil && status.RestartCount > 0 {
			return nil, fmt.Errorf("other member %s is eligible for recovery", pod.Name)
		}
	}
	if chosen == nil {
		return nil, fmt.Errorf("selected member %s is absent", selected.Name)
	}
	return chosen, nil

}

func nativeCorruptionReadJob(ctx context.Context, reader crclient.Reader, namespace string) (*batchv1.Job, error) {
	job := etcdrecoverymanifests.EtcdRecoveryJob(namespace)
	if err := reader.Get(ctx, crclient.ObjectKeyFromObject(job), job); err != nil {
		if apierrors.IsNotFound(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("get corruption recovery Job: %w", err)
	}
	return job, nil

}

func nativeCorruptionBaselineJob(previous *batchv1.Job) (types.UID, error) {
	var previousUID types.UID
	if previous != nil {
		finished := false
		for _, condition := range previous.Status.Conditions {
			if (condition.Type == batchv1.JobComplete || condition.Type == batchv1.JobFailed) && condition.Status == corev1.ConditionTrue {
				finished = true
			}
		}
		if previous.Status.Active != 0 || !finished || previous.UID == "" {
			return "", fmt.Errorf("recovery Job already exists without an inactive terminal identity")
		}
		previousUID = previous.UID
	}
	return previousUID, nil
}

func nativeCorruptEtcdMember(ctx context.Context, reader crclient.Reader, cluster crclient.ObjectKey, selected corev1.Pod, exec func(context.Context, []string) (string, error), interval time.Duration) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if selected.UID == "" || selected.Name == "" || selected.Namespace == "" || cluster.Name == "" || cluster.Namespace == "" || interval <= 0 {
		return fmt.Errorf("corruption requires selected member identity, hosted cluster identity and a positive polling interval")
	}
	baseline, err := nativeCorruptionMemberStatus(ctx, reader, selected)
	if err != nil {
		return err
	}
	if baseline.State.Running == nil {
		return fmt.Errorf("selected member %s must be Running before corruption", selected.Name)
	}
	previous, err := nativeCorruptionReadJob(ctx, reader, selected.Namespace)
	if err != nil {
		return err
	}
	previousUID, err := nativeCorruptionBaselineJob(previous)
	if err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	execCtx, cancelExec := context.WithTimeout(ctx, 5*time.Minute)
	output, execErr := exec(execCtx, []string{"/bin/sh", "-c", "rm -rf /var/lib/data/member"})
	if execErr == nil {
		execErr = execCtx.Err()
	}
	cancelExec()
	if err := nativeEtcdCommandResult(output, execErr, "No such file or directory"); err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	return nativeObserveCorruption(ctx, reader, cluster, selected, baseline.RestartCount, previousUID, interval)
}

func nativeCorruptionActiveJob(job *batchv1.Job, previousUID types.UID, cluster crclient.ObjectKey, witnessed bool) (bool, error) {
	if job == nil {
		return false, nil
	}
	if job.UID == "" {
		return false, fmt.Errorf("recovery Job lacks a UID")
	}
	if job.UID == previousUID {
		if job.Status.Active != 0 {
			return false, fmt.Errorf("previous recovery Job became active again")
		}
		return false, nil
	}
	if !witnessed {
		return false, fmt.Errorf("new recovery Job preceded selected member failure witness")
	}
	if job.Labels["hypershift.openshift.io/cluster-name"] != cluster.Name || job.Labels["hypershift.openshift.io/cluster-namespace"] != cluster.Namespace {
		return false, fmt.Errorf("new recovery Job lacks the expected hosted cluster ownership labels")
	}
	return job.Status.Active == 1 && job.DeletionTimestamp == nil, nil
}

func nativeObserveCorruption(ctx context.Context, reader crclient.Reader, cluster crclient.ObjectKey, selected corev1.Pod, baselineRestarts int32, previousUID types.UID, interval time.Duration) error {
	observeCtx, cancelObserve := context.WithTimeout(ctx, 10*time.Minute)
	defer cancelObserve()
	witnessed := false
	lastObservation := "selected member has not entered Waiting with an increased restart count"
	for {
		if err := observeCtx.Err(); err != nil {
			return fmt.Errorf("corruption recovery: %s: %w", lastObservation, err)
		}
		status, err := nativeCorruptionMemberStatus(observeCtx, reader, selected)
		if err != nil {
			return err
		}
		if status.State.Waiting != nil && status.RestartCount > baselineRestarts && status.RestartCount > 0 {
			witnessed = true
			lastObservation = "selected member failure witnessed, waiting for a new active recovery Job"
		}
		job, err := nativeCorruptionReadJob(observeCtx, reader, selected.Namespace)
		if err != nil {
			return err
		}
		active, err := nativeCorruptionActiveJob(job, previousUID, cluster, witnessed)
		if err != nil {
			return err
		}
		if active {
			return observeCtx.Err()
		}
		timer := time.NewTimer(interval)
		select {
		case <-observeCtx.Done():
			timer.Stop()
			return fmt.Errorf("corruption recovery: %s: %w", lastObservation, observeCtx.Err())
		case <-timer.C:
		}
	}
}
