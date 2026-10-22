//go:build e2ev2

package tests

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"maps"
	"strings"
	"time"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/util/retry"

	crclient "sigs.k8s.io/controller-runtime/pkg/client"
)

const nativeRecoveryAnnotation = "hypershift-e2e-native-recovery"
const nativeRecoverySelector = "ocpbugs-77268-test"
const nativeRecoveryAdditionalSelector = `{"ocpbugs-77268-test":"true"}`

func nativeRecoveryFixture(cluster *hyperv1.HostedCluster, pools []hyperv1.NodePool, variant string) error {
	if cluster.Annotations[nativeRecoveryAnnotation] != variant {
		return fmt.Errorf("native recovery requires its dedicated %s fixture", variant)
	}
	if cluster.Spec.ControllerAvailabilityPolicy != hyperv1.HighlyAvailable {
		return fmt.Errorf("native recovery requires a HighlyAvailable control plane")
	}
	for _, pool := range pools {
		if pool.Spec.ClusterName == cluster.Name && (pool.Spec.AutoScaling != nil || pool.Spec.Replicas == nil || *pool.Spec.Replicas != 0) {
			return fmt.Errorf("native recovery requires explicitly zero workers, found NodePool %s", pool.Name)
		}
	}
	return nil
}

func nativeEtcdConverged(sts *appsv1.StatefulSet) error {
	if sts.Spec.Replicas == nil || *sts.Spec.Replicas != 3 || sts.Status.ReadyReplicas != *sts.Spec.Replicas {
		return fmt.Errorf("expected HA etcd with three ready replicas, got %d", sts.Status.ReadyReplicas)
	}
	return nil
}

func nativeEtcdReplaced(before, after []corev1.Pod) error {
	if len(before) != 3 || len(after) != len(before) {
		return fmt.Errorf("expected all three etcd members to be present")
	}
	for _, original := range before {
		found := false
		for _, replacement := range after {
			if original.Name == replacement.Name && original.Namespace == replacement.Namespace {
				found = replacement.UID != "" && replacement.UID != original.UID
			}
		}
		if !found {
			return fmt.Errorf("etcd member %s has not been replaced", original.Name)
		}
	}
	return nil
}

func nativeMarkerSurvived(expected, actual *corev1.ConfigMap) error {
	if !maps.Equal(expected.Data, actual.Data) {
		return fmt.Errorf("marker ConfigMap data changed during etcd disruption")
	}
	return nil
}

func nativeEtcdCommandResult(output string, err error, forbidden string) error {
	if err != nil {
		return fmt.Errorf("executing etcd disruption: %w", err)
	}
	if strings.Contains(output, forbidden) {
		return fmt.Errorf("etcd command output contained %q", forbidden)
	}
	return nil
}

func nativeEtcdMemberID(output string) (string, error) {
	memberID := strings.TrimSpace(output)
	if memberID == "" {
		return "", fmt.Errorf("etcd member discovery returned an empty ID")
	}
	for _, digit := range memberID {
		if !strings.ContainsRune("0123456789abcdefABCDEF", digit) {
			return "", fmt.Errorf("etcd member discovery returned a malformed ID")
		}
	}
	return memberID, nil
}

type nativePullSecretSnapshot struct {
	clusterKey      crclient.ObjectKey
	clusterUID      types.UID
	secretKey       crclient.ObjectKey
	secretUID       types.UID
	dockerConfig    []byte
	hadDockerConfig bool
	selector        string
	hadSelector     bool
	annotation      string
	hadAnnotation   bool
}

func nativeCapturePullSecret(cluster *hyperv1.HostedCluster, secret *corev1.Secret, clusters []hyperv1.HostedCluster) (nativePullSecretSnapshot, error) {
	if secret.Name == "" || secret.Name != cluster.Spec.PullSecret.Name || secret.Namespace != cluster.Namespace {
		return nativePullSecretSnapshot{}, fmt.Errorf("pull Secret must be the actual HostedCluster reference")
	}
	for _, other := range clusters {
		if other.Namespace == cluster.Namespace && other.Name != cluster.Name && other.Spec.PullSecret.Name == secret.Name {
			return nativePullSecretSnapshot{}, fmt.Errorf("refusing to corrupt a pull Secret shared with another HostedCluster")
		}
	}
	dockerConfig, hadDockerConfig := secret.Data[corev1.DockerConfigJsonKey]
	selector, hadSelector := cluster.Spec.NodeSelector[nativeRecoverySelector]
	annotation, hadAnnotation := cluster.Annotations[hyperv1.RequestServingNodeAdditionalSelectorAnnotation]
	return nativePullSecretSnapshot{
		clusterKey: crclient.ObjectKeyFromObject(cluster), clusterUID: cluster.UID,
		secretKey: crclient.ObjectKeyFromObject(secret), secretUID: secret.UID,
		dockerConfig: bytes.Clone(dockerConfig), hadDockerConfig: hadDockerConfig,
		selector: selector, hadSelector: hadSelector, annotation: annotation, hadAnnotation: hadAnnotation,
	}, nil
}

func nativeRemovePullSecretKey(ctx context.Context, client crclient.Client, snapshot nativePullSecretSnapshot) error {
	return retry.RetryOnConflict(retry.DefaultRetry, func() error {
		secret := &corev1.Secret{}
		if err := client.Get(ctx, snapshot.secretKey, secret); err != nil {
			return err
		}
		if secret.UID != snapshot.secretUID {
			return fmt.Errorf("pull Secret was replaced before mutation")
		}
		delete(secret.Data, corev1.DockerConfigJsonKey)
		return client.Update(ctx, secret)
	})
}

func nativeMutateRecoverySelectors(ctx context.Context, client crclient.Client, snapshot nativePullSecretSnapshot) error {
	return retry.RetryOnConflict(retry.DefaultRetry, func() error {
		cluster := &hyperv1.HostedCluster{}
		if err := client.Get(ctx, snapshot.clusterKey, cluster); err != nil {
			return err
		}
		if cluster.UID != snapshot.clusterUID {
			return fmt.Errorf("HostedCluster was replaced before mutation")
		}
		if cluster.Spec.NodeSelector == nil {
			cluster.Spec.NodeSelector = map[string]string{}
		}
		if cluster.Annotations == nil {
			cluster.Annotations = map[string]string{}
		}
		cluster.Spec.NodeSelector[nativeRecoverySelector] = "true"
		cluster.Annotations[hyperv1.RequestServingNodeAdditionalSelectorAnnotation] = nativeRecoveryAdditionalSelector
		return client.Update(ctx, cluster)
	})
}

func nativeRecoverySelectorsPropagated(hcp *hyperv1.HostedControlPlane) error {
	if hcp.Spec.NodeSelector[nativeRecoverySelector] != "true" {
		return fmt.Errorf("NodeSelector not yet propagated to HCP")
	}
	if hcp.Annotations[hyperv1.RequestServingNodeAdditionalSelectorAnnotation] != nativeRecoveryAdditionalSelector {
		return fmt.Errorf("RequestServingNodeAdditionalSelector not yet propagated to HCP")
	}
	return nil
}

func nativeRestorePullSecretBounded(ctx context.Context, client crclient.Client, snapshot nativePullSecretSnapshot) error {
	cleanupCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 2*time.Minute)
	defer cancel()
	return nativeRestorePullSecret(cleanupCtx, client, snapshot)
}

func nativeRestorePullSecret(ctx context.Context, client crclient.Client, snapshot nativePullSecretSnapshot) error {
	secretErr := retry.RetryOnConflict(retry.DefaultRetry, func() error {
		secret := &corev1.Secret{}
		if err := client.Get(ctx, snapshot.secretKey, secret); err != nil {
			return err
		}
		if secret.UID != snapshot.secretUID {
			return fmt.Errorf("refusing to restore a replacement pull Secret")
		}
		if snapshot.hadDockerConfig {
			if secret.Data == nil {
				secret.Data = map[string][]byte{}
			}
			secret.Data[corev1.DockerConfigJsonKey] = bytes.Clone(snapshot.dockerConfig)
		} else {
			delete(secret.Data, corev1.DockerConfigJsonKey)
		}
		return client.Update(ctx, secret)
	})
	clusterErr := retry.RetryOnConflict(retry.DefaultRetry, func() error {
		cluster := &hyperv1.HostedCluster{}
		if err := client.Get(ctx, snapshot.clusterKey, cluster); err != nil {
			return err
		}
		if cluster.UID != snapshot.clusterUID {
			return fmt.Errorf("refusing to restore a replacement HostedCluster")
		}
		if snapshot.hadSelector {
			if cluster.Spec.NodeSelector == nil {
				cluster.Spec.NodeSelector = map[string]string{}
			}
			cluster.Spec.NodeSelector[nativeRecoverySelector] = snapshot.selector
		} else {
			delete(cluster.Spec.NodeSelector, nativeRecoverySelector)
		}
		if snapshot.hadAnnotation {
			if cluster.Annotations == nil {
				cluster.Annotations = map[string]string{}
			}
			cluster.Annotations[hyperv1.RequestServingNodeAdditionalSelectorAnnotation] = snapshot.annotation
		} else {
			delete(cluster.Annotations, hyperv1.RequestServingNodeAdditionalSelectorAnnotation)
		}
		return client.Update(ctx, cluster)
	})
	return errors.Join(secretErr, clusterErr)
}
