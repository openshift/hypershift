package resources

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	awsutil "github.com/openshift/hypershift/support/awsutil"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/util/retry"

	"sigs.k8s.io/controller-runtime/pkg/client"
)

func (r *reconciler) retireCompletedAWSLoadBalancerCleanupProgress(ctx context.Context, state *awsLoadBalancerCleanupState, result awsutil.LoadBalancerCleanupResult, serviceCleanupComplete map[string]bool) error {
	if !result.OrphanTargetGroupScanSuccess {
		return nil
	}
	for uid := range state.progress.Services {
		if _, present := state.currentServices[uid]; !present && serviceCleanupComplete[uid] {
			delete(state.progress.Services, uid)
			state.progressChanged = true
		}
	}
	if !state.progressChanged {
		return nil
	}
	if len(state.progress.Services) == 0 {
		if state.progressExists {
			if err := r.cpClient.Delete(ctx, state.progressConfigMap); err != nil && !apierrors.IsNotFound(err) {
				return newSafeLoadBalancerCleanupError("failed to retire completed AWS load balancer cleanup progress", err)
			}
		}
		return nil
	}
	if err := r.saveAWSLoadBalancerCleanupProgress(ctx, state.progressConfigMap, state.progressExists, state.progress); err != nil {
		return newSafeLoadBalancerCleanupError("failed to update AWS load balancer cleanup progress", err)
	}
	return nil
}

func (r *reconciler) loadAWSLoadBalancerCleanupProgress(ctx context.Context, hcp *hyperv1.HostedControlPlane) (*awsLoadBalancerCleanupProgress, *corev1.ConfigMap, bool, error) {
	if hcp.UID == "" {
		return nil, nil, false, newSafeLoadBalancerCleanupError("HostedControlPlane UID is required to load AWS load balancer cleanup progress", nil)
	}
	if r.cpClient == nil {
		return nil, nil, false, newSafeLoadBalancerCleanupError("management-cluster client is unavailable for AWS load balancer cleanup progress", nil)
	}
	if r.cpAPIReader == nil {
		return nil, nil, false, newSafeLoadBalancerCleanupError("uncached management-cluster reader is unavailable for AWS load balancer cleanup progress", nil)
	}
	configMap := &corev1.ConfigMap{ObjectMeta: metav1.ObjectMeta{Namespace: hcp.Namespace, Name: awsLoadBalancerProgressConfigMapName(hcp.UID)}}
	if err := r.cpAPIReader.Get(ctx, client.ObjectKeyFromObject(configMap), configMap); err != nil {
		if apierrors.IsNotFound(err) {
			region := ""
			if hcp.Spec.Platform.AWS != nil {
				region = hcp.Spec.Platform.AWS.Region
			}
			return &awsLoadBalancerCleanupProgress{HCPUID: hcp.UID, InfraID: hcp.Spec.InfraID, Region: region, Services: map[string]awsLoadBalancerServiceProof{}}, configMap, false, nil
		}
		return nil, nil, false, newSafeLoadBalancerCleanupError("failed to read AWS load balancer cleanup progress from management cluster", err)
	}
	serialized := configMap.Data[awsLoadBalancerProgressDataKey]
	progress := &awsLoadBalancerCleanupProgress{}
	if serialized == "" {
		return nil, nil, false, newSafeLoadBalancerCleanupError(fmt.Sprintf("progress ConfigMap has no data for key %q", awsLoadBalancerProgressDataKey), nil)
	}
	if err := json.Unmarshal([]byte(serialized), progress); err != nil {
		return nil, nil, false, newSafeLoadBalancerCleanupError("AWS load balancer cleanup progress is invalid", fmt.Errorf("failed to unmarshal progress ConfigMap data: %w", err))
	}
	region := ""
	if hcp.Spec.Platform.AWS != nil {
		region = hcp.Spec.Platform.AWS.Region
	}
	if progress.HCPUID != hcp.UID {
		return nil, nil, false, newSafeLoadBalancerCleanupError("AWS load balancer cleanup progress UID does not match the HostedControlPlane", nil)
	}
	if progress.InfraID != hcp.Spec.InfraID {
		return nil, nil, false, newSafeLoadBalancerCleanupError("AWS load balancer cleanup progress InfraID does not match the HostedControlPlane", nil)
	}
	if progress.Region != "" && progress.Region != region {
		return nil, nil, false, newSafeLoadBalancerCleanupError("AWS load balancer cleanup progress AWS region does not match the HostedControlPlane", nil)
	}
	if progress.VPCID != "" && hcp.Spec.Platform.AWS != nil && hcp.Spec.Platform.AWS.CloudProviderConfig != nil && progress.VPCID != hcp.Spec.Platform.AWS.CloudProviderConfig.VPC {
		return nil, nil, false, newSafeLoadBalancerCleanupError("AWS load balancer cleanup progress VPC ID does not match the HostedControlPlane", nil)
	}
	if progress.Services == nil {
		progress.Services = map[string]awsLoadBalancerServiceProof{}
	}
	return progress, configMap, true, nil
}

func ensureAWSLoadBalancerProgressOwnerReference(configMap *corev1.ConfigMap, hcp *hyperv1.HostedControlPlane) (bool, error) {
	if len(configMap.OwnerReferences) > 1 || (len(configMap.OwnerReferences) == 1 && configMap.OwnerReferences[0].UID != hcp.UID) {
		return false, newSafeLoadBalancerCleanupError("AWS load balancer cleanup progress ConfigMap has an unexpected owner", nil)
	}

	controller := true
	blockOwnerDeletion := false
	expected := metav1.OwnerReference{
		APIVersion:         hyperv1.GroupVersion.String(),
		Kind:               "HostedControlPlane",
		Name:               hcp.Name,
		UID:                hcp.UID,
		Controller:         &controller,
		BlockOwnerDeletion: &blockOwnerDeletion,
	}
	if len(configMap.OwnerReferences) == 1 {
		current := configMap.OwnerReferences[0]
		if current.APIVersion == expected.APIVersion && current.Kind == expected.Kind && current.Name == expected.Name && current.UID == expected.UID && current.Controller != nil && *current.Controller && current.BlockOwnerDeletion != nil && !*current.BlockOwnerDeletion {
			return false, nil
		}
	}
	configMap.OwnerReferences = []metav1.OwnerReference{expected}
	return true, nil
}

func (r *reconciler) saveAWSLoadBalancerCleanupProgress(ctx context.Context, configMap *corev1.ConfigMap, exists bool, progress *awsLoadBalancerCleanupProgress) error {
	if r.cpClient == nil {
		return newSafeLoadBalancerCleanupError("management-cluster client is unavailable for AWS load balancer cleanup progress", nil)
	}
	serialized, err := json.Marshal(progress)
	if err != nil {
		return fmt.Errorf("failed to serialize AWS load balancer cleanup progress: %w", err)
	}
	if !exists {
		if configMap.Data == nil {
			configMap.Data = map[string]string{}
		}
		configMap.Data[awsLoadBalancerProgressDataKey] = string(serialized)
		return r.cpClient.Create(ctx, configMap)
	}

	if r.cpAPIReader == nil {
		return newSafeLoadBalancerCleanupError("uncached management-cluster reader is unavailable for AWS load balancer cleanup progress updates", nil)
	}
	progressKey := client.ObjectKeyFromObject(configMap)
	return retry.RetryOnConflict(retry.DefaultRetry, func() error {
		latest := &corev1.ConfigMap{}
		if err := r.cpAPIReader.Get(ctx, progressKey, latest); err != nil {
			return err
		}
		// A retry must not overwrite progress that changed identity or ownership
		// after the initial validation.
		latestProgress := &awsLoadBalancerCleanupProgress{}
		if err := json.Unmarshal([]byte(latest.Data[awsLoadBalancerProgressDataKey]), latestProgress); err != nil {
			return newSafeLoadBalancerCleanupError("AWS load balancer cleanup progress is invalid", err)
		}
		if latestProgress.HCPUID != progress.HCPUID || latestProgress.InfraID != progress.InfraID ||
			(latestProgress.Region != "" && latestProgress.Region != progress.Region) ||
			(latestProgress.VPCID != "" && latestProgress.VPCID != progress.VPCID) {
			return newSafeLoadBalancerCleanupError("AWS load balancer cleanup progress identity changed before update", nil)
		}
		if len(latest.OwnerReferences) > 1 || (len(latest.OwnerReferences) == 1 && latest.OwnerReferences[0].UID != progress.HCPUID) {
			return newSafeLoadBalancerCleanupError("AWS load balancer cleanup progress ConfigMap has an unexpected owner", nil)
		}
		latest.Data[awsLoadBalancerProgressDataKey] = string(serialized)
		latest.OwnerReferences = configMap.OwnerReferences
		return r.cpClient.Update(ctx, latest)
	})
}

func awsLoadBalancerProgressConfigMapName(uid types.UID) string {
	digest := sha256.Sum256([]byte(uid))
	return "aws-lb-cleanup-" + hex.EncodeToString(digest[:12])
}
