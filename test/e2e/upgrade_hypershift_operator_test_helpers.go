//go:build e2e

package e2e

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	"github.com/openshift/hypershift/support/k8sutil"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/types"

	capiv1beta1 "sigs.k8s.io/cluster-api/api/core/v1beta1"
	capiv1beta2 "sigs.k8s.io/cluster-api/api/core/v1beta2"
	crclient "sigs.k8s.io/controller-runtime/pkg/client"
)

const (
	tokenSecretAnnotation          = "hypershift.openshift.io/ignition-config"
	tokenSecretTokenGenerationTime = "hypershift.openshift.io/last-token-generation-time"
	nodePoolAnnotation             = "hypershift.openshift.io/nodePool"
)

// bootstrapChainState captures the complete bootstrap credential chain for a NodePool,
// including the CAPI workload's bootstrap Secret reference and the referenced token/userdata Secrets.
type bootstrapChainState struct {
	NodePoolName string
	NodePoolUID  types.UID

	// Bootstrap Secret reference from CAPI workload (MachineDeployment or MachineSet)
	BootstrapSecretName string

	// Token Secret (contains credentials, referenced indirectly via userdata)
	TokenSecretName string
	TokenSecretUID  types.UID
	TokenID         string
	GenerationTime  time.Time

	// Userdata Secret (referenced by CAPI workload bootstrap)
	UserdataSecretName string
	UserdataSecretUID  types.UID
}

// captureBootstrapChain captures the complete bootstrap chain for all NodePools,
// following the actual CAPI workload references instead of listing all Secrets.
func captureBootstrapChain(ctx context.Context, client crclient.Client, namespace string, nodePools map[string]*hyperv1.NodePool, useCAPIv1Beta1 bool) (map[string]*bootstrapChainState, error) {
	result := make(map[string]*bootstrapChainState)

	for npName, nodePool := range nodePools {
		state := &bootstrapChainState{
			NodePoolName: npName,
			NodePoolUID:  nodePool.UID,
		}

		// Get bootstrap Secret reference from CAPI workload
		var bootstrapSecretName *string
		if nodePool.Spec.Management.UpgradeType == hyperv1.UpgradeTypeReplace {
			if useCAPIv1Beta1 {
				md := &capiv1beta1.MachineDeployment{}
				if err := client.Get(ctx, crclient.ObjectKey{Namespace: namespace, Name: npName}, md); err != nil {
					if apierrors.IsNotFound(err) {
						continue // MachineDeployment not yet created
					}
					return nil, fmt.Errorf("failed to get MachineDeployment %s/%s: %w", namespace, npName, err)
				}
				bootstrapSecretName = md.Spec.Template.Spec.Bootstrap.DataSecretName
			} else {
				md := &capiv1beta2.MachineDeployment{}
				if err := client.Get(ctx, crclient.ObjectKey{Namespace: namespace, Name: npName}, md); err != nil {
					if apierrors.IsNotFound(err) {
						continue
					}
					return nil, fmt.Errorf("failed to get MachineDeployment %s/%s: %w", namespace, npName, err)
				}
				bootstrapSecretName = md.Spec.Template.Spec.Bootstrap.DataSecretName
			}
		} else {
			// InPlace uses MachineSet (always v1beta2)
			ms := &capiv1beta2.MachineSet{}
			if err := client.Get(ctx, crclient.ObjectKey{Namespace: namespace, Name: npName}, ms); err != nil {
				if apierrors.IsNotFound(err) {
					continue
				}
				return nil, fmt.Errorf("failed to get MachineSet %s/%s: %w", namespace, npName, err)
			}
			bootstrapSecretName = ms.Spec.Template.Spec.Bootstrap.DataSecretName
		}

		if bootstrapSecretName == nil || *bootstrapSecretName == "" {
			return nil, fmt.Errorf("NodePool %s has no bootstrap Secret reference", npName)
		}

		state.BootstrapSecretName = *bootstrapSecretName

		// The bootstrap Secret is the userdata Secret
		userdataSecret := &corev1.Secret{}
		if err := client.Get(ctx, crclient.ObjectKey{Namespace: namespace, Name: *bootstrapSecretName}, userdataSecret); err != nil {
			return nil, fmt.Errorf("failed to get userdata Secret %s/%s: %w", namespace, *bootstrapSecretName, err)
		}

		state.UserdataSecretName = userdataSecret.Name
		state.UserdataSecretUID = userdataSecret.UID

		// Extract hash from userdata Secret name to find the corresponding token Secret
		// Userdata Secret format: user-data-{nodepool}-{hash}
		// Token Secret format: token-{nodepool}-{hash}
		hash := extractHashFromSecretName(state.UserdataSecretName)
		if hash == "" {
			return nil, fmt.Errorf("failed to extract hash from userdata Secret name: %s", state.UserdataSecretName)
		}

		tokenSecretName := fmt.Sprintf("token-%s-%s", npName, hash)
		tokenSecret := &corev1.Secret{}
		if err := client.Get(ctx, crclient.ObjectKey{Namespace: namespace, Name: tokenSecretName}, tokenSecret); err != nil {
			return nil, fmt.Errorf("failed to get token Secret %s/%s: %w", namespace, tokenSecretName, err)
		}

		// Verify this is a token Secret
		if tokenSecret.Annotations[tokenSecretAnnotation] != "true" {
			return nil, fmt.Errorf("secret %s is not a token Secret (missing annotation)", tokenSecretName)
		}

		state.TokenSecretName = tokenSecret.Name
		state.TokenSecretUID = tokenSecret.UID
		state.TokenID = string(tokenSecret.Data["token"])

		// Parse generation time
		genTimeStr := tokenSecret.Annotations[tokenSecretTokenGenerationTime]
		if genTimeStr == "" {
			return nil, fmt.Errorf("token Secret %s missing generation time annotation", tokenSecretName)
		}
		genTime, err := time.Parse(time.RFC3339Nano, genTimeStr)
		if err != nil {
			return nil, fmt.Errorf("failed to parse generation time for token Secret %s: %w", tokenSecretName, err)
		}
		state.GenerationTime = genTime

		result[npName] = state
	}

	if len(result) == 0 {
		return nil, fmt.Errorf("no bootstrap chains captured (expected %d NodePools)", len(nodePools))
	}

	return result, nil
}

// extractHashFromSecretName extracts the hash suffix from a Secret name.
// Format: {prefix}-{nodepool-name}-{hash}
func extractHashFromSecretName(name string) string {
	parts := strings.Split(name, "-")
	if len(parts) < 3 {
		return ""
	}
	return parts[len(parts)-1]
}

// makeTokenRotationDue patches a token Secret's generation timestamp to make rotation due.
// Sets the timestamp to 6 hours in the past, ensuring it exceeds the 5.5-hour rotation threshold (11h TTL / 2).
func makeTokenRotationDue(ctx context.Context, client crclient.Client, namespace string, state *bootstrapChainState) error {
	tokenSecret := &corev1.Secret{}
	tokenSecret.Namespace = namespace
	tokenSecret.Name = state.TokenSecretName

	return k8sutil.UpdateObject(ctx, client, tokenSecret, func() error {
		// Verify UID matches (Secret wasn't replaced)
		if tokenSecret.UID != state.TokenSecretUID {
			return fmt.Errorf("token Secret %s UID changed (expected %s, got %s)", state.TokenSecretName, state.TokenSecretUID, tokenSecret.UID)
		}

		// Set generation time to 6 hours ago
		sixHoursAgo := time.Now().Add(-6 * time.Hour)
		if tokenSecret.Annotations == nil {
			tokenSecret.Annotations = make(map[string]string)
		}
		tokenSecret.Annotations[tokenSecretTokenGenerationTime] = sixHoursAgo.Format(time.RFC3339Nano)

		return nil
	})
}

// verifyTokenRotation verifies that a token Secret has rotated while maintaining the same UID.
func verifyTokenRotation(ctx context.Context, client crclient.Client, namespace string, originalState *bootstrapChainState) error {
	tokenSecret := &corev1.Secret{}
	if err := client.Get(ctx, crclient.ObjectKey{Namespace: namespace, Name: originalState.TokenSecretName}, tokenSecret); err != nil {
		return fmt.Errorf("token Secret %s not found after rotation: %w", originalState.TokenSecretName, err)
	}

	// Verify same UID (not replaced)
	if tokenSecret.UID != originalState.TokenSecretUID {
		return fmt.Errorf("token Secret %s was replaced (UID changed from %s to %s)", originalState.TokenSecretName, originalState.TokenSecretUID, tokenSecret.UID)
	}

	// Verify token ID changed
	newTokenID := string(tokenSecret.Data["token"])
	if newTokenID == originalState.TokenID {
		return fmt.Errorf("token ID did not change for Secret %s", originalState.TokenSecretName)
	}

	// Verify generation time was updated
	genTimeStr := tokenSecret.Annotations[tokenSecretTokenGenerationTime]
	if genTimeStr == "" {
		return fmt.Errorf("token Secret %s missing generation time after rotation", originalState.TokenSecretName)
	}
	newGenTime, err := time.Parse(time.RFC3339Nano, genTimeStr)
	if err != nil {
		return fmt.Errorf("failed to parse generation time after rotation: %w", err)
	}
	if !newGenTime.After(originalState.GenerationTime) {
		return fmt.Errorf("generation time not updated (was %v, now %v)", originalState.GenerationTime, newGenTime)
	}

	return nil
}

// verifyBootstrapChainIntact verifies that the bootstrap chain references remain unchanged.
func verifyBootstrapChainIntact(ctx context.Context, client crclient.Client, namespace string, originalState *bootstrapChainState, useCAPIv1Beta1 bool, nodePool *hyperv1.NodePool) error {
	// Verify CAPI workload still references the same bootstrap Secret
	var currentBootstrapRef *string
	if nodePool.Spec.Management.UpgradeType == hyperv1.UpgradeTypeReplace {
		if useCAPIv1Beta1 {
			md := &capiv1beta1.MachineDeployment{}
			if err := client.Get(ctx, crclient.ObjectKey{Namespace: namespace, Name: nodePool.Name}, md); err != nil {
				return fmt.Errorf("failed to get MachineDeployment: %w", err)
			}
			currentBootstrapRef = md.Spec.Template.Spec.Bootstrap.DataSecretName
		} else {
			md := &capiv1beta2.MachineDeployment{}
			if err := client.Get(ctx, crclient.ObjectKey{Namespace: namespace, Name: nodePool.Name}, md); err != nil {
				return fmt.Errorf("failed to get MachineDeployment: %w", err)
			}
			currentBootstrapRef = md.Spec.Template.Spec.Bootstrap.DataSecretName
		}
	} else {
		ms := &capiv1beta2.MachineSet{}
		if err := client.Get(ctx, crclient.ObjectKey{Namespace: namespace, Name: nodePool.Name}, ms); err != nil {
			return fmt.Errorf("failed to get MachineSet: %w", err)
		}
		currentBootstrapRef = ms.Spec.Template.Spec.Bootstrap.DataSecretName
	}

	if currentBootstrapRef == nil || *currentBootstrapRef != originalState.BootstrapSecretName {
		return fmt.Errorf("bootstrap Secret reference changed from %s to %v", originalState.BootstrapSecretName, currentBootstrapRef)
	}

	// Verify userdata Secret still exists with same UID
	userdataSecret := &corev1.Secret{}
	if err := client.Get(ctx, crclient.ObjectKey{Namespace: namespace, Name: originalState.UserdataSecretName}, userdataSecret); err != nil {
		return fmt.Errorf("userdata Secret %s not found: %w", originalState.UserdataSecretName, err)
	}
	if userdataSecret.UID != originalState.UserdataSecretUID {
		return fmt.Errorf("userdata Secret %s was replaced (UID changed)", originalState.UserdataSecretName)
	}

	return nil
}

// verifyUserdataCredentialUpdated verifies that the userdata Secret contains the current token credential.
// This is critical: rotation updates the token Secret, but new workers fail if the userdata Secret
// referenced by CAPI is not updated with the new credential.
func verifyUserdataCredentialUpdated(ctx context.Context, client crclient.Client, namespace string, state *bootstrapChainState) error {
	// Get the current token Secret to extract the current token ID
	tokenSecret := &corev1.Secret{}
	if err := client.Get(ctx, crclient.ObjectKey{Namespace: namespace, Name: state.TokenSecretName}, tokenSecret); err != nil {
		return fmt.Errorf("failed to get token Secret %s: %w", state.TokenSecretName, err)
	}

	currentTokenID := string(tokenSecret.Data["token"])
	if currentTokenID == "" {
		return fmt.Errorf("token Secret %s has empty token", state.TokenSecretName)
	}

	// Get the userdata Secret
	userdataSecret := &corev1.Secret{}
	if err := client.Get(ctx, crclient.ObjectKey{Namespace: namespace, Name: state.UserdataSecretName}, userdataSecret); err != nil {
		return fmt.Errorf("failed to get userdata Secret %s: %w", state.UserdataSecretName, err)
	}

	// Parse the ignition config from userdata Secret
	ignitionConfigJSON, ok := userdataSecret.Data["value"]
	if !ok {
		return fmt.Errorf("userdata Secret %s missing 'value' key", state.UserdataSecretName)
	}

	// Parse the ignition config JSON
	var ignConfig map[string]interface{}
	if err := json.Unmarshal(ignitionConfigJSON, &ignConfig); err != nil {
		return fmt.Errorf("failed to parse ignition config: %w", err)
	}

	// Extract the token from the ignition config
	// Structure: ignition.config.merge[0].httpHeaders[...{name: "Authorization", value: "Bearer <base64-token>"}]
	ignition, ok := ignConfig["ignition"].(map[string]interface{})
	if !ok {
		return fmt.Errorf("ignition config missing 'ignition' section")
	}

	config, ok := ignition["config"].(map[string]interface{})
	if !ok {
		return fmt.Errorf("ignition config missing 'config' section")
	}

	merge, ok := config["merge"].([]interface{})
	if !ok || len(merge) == 0 {
		return fmt.Errorf("ignition config missing 'merge' array")
	}

	mergeFirst, ok := merge[0].(map[string]interface{})
	if !ok {
		return fmt.Errorf("ignition config merge[0] is not an object")
	}

	httpHeaders, ok := mergeFirst["httpHeaders"].([]interface{})
	if !ok {
		return fmt.Errorf("ignition config missing 'httpHeaders'")
	}

	// Find the Authorization header
	var encodedToken string
	for _, header := range httpHeaders {
		headerMap, ok := header.(map[string]interface{})
		if !ok {
			continue
		}
		name, ok := headerMap["name"].(string)
		if !ok || name != "Authorization" {
			continue
		}
		value, ok := headerMap["value"].(string)
		if !ok {
			continue
		}
		// Value format: "Bearer <base64-encoded-token>"
		parts := strings.Split(value, " ")
		if len(parts) == 2 && parts[0] == "Bearer" {
			encodedToken = parts[1]
			break
		}
	}

	if encodedToken == "" {
		return fmt.Errorf("authorization header not found in userdata ignition config")
	}

	// Decode the base64 token
	decodedToken, err := base64.StdEncoding.DecodeString(encodedToken)
	if err != nil {
		return fmt.Errorf("failed to decode token from userdata: %w", err)
	}

	embeddedTokenID := string(decodedToken)

	// Verify the embedded token matches the current token Secret
	if embeddedTokenID != currentTokenID {
		// Don't include credential material in error messages
		return fmt.Errorf("userdata Secret %s has stale credential (does not match current token Secret %s)",
			state.UserdataSecretName, state.TokenSecretName)
	}

	return nil
}

// verifyCredentialPropagationAndIdentity verifies that the userdata Secret contains
// the current credential AND that both token and userdata Secret UIDs match the original state.
// This catches both stale credentials and Secret replacement.
func verifyCredentialPropagationAndIdentity(ctx context.Context, client crclient.Client, namespace string, originalState *bootstrapChainState) error {
	// First verify credential propagation
	if err := verifyUserdataCredentialUpdated(ctx, client, namespace, originalState); err != nil {
		return err
	}

	// Then verify Secret identities preserved
	tokenSecret := &corev1.Secret{}
	if err := client.Get(ctx, crclient.ObjectKey{Namespace: namespace, Name: originalState.TokenSecretName}, tokenSecret); err != nil {
		return fmt.Errorf("token Secret %s not found: %w", originalState.TokenSecretName, err)
	}
	if tokenSecret.UID != originalState.TokenSecretUID {
		return fmt.Errorf("token Secret %s was replaced after rotation (UID changed from %s to %s)",
			originalState.TokenSecretName, originalState.TokenSecretUID, tokenSecret.UID)
	}

	userdataSecret := &corev1.Secret{}
	if err := client.Get(ctx, crclient.ObjectKey{Namespace: namespace, Name: originalState.UserdataSecretName}, userdataSecret); err != nil {
		return fmt.Errorf("userdata Secret %s not found: %w", originalState.UserdataSecretName, err)
	}
	if userdataSecret.UID != originalState.UserdataSecretUID {
		return fmt.Errorf("userdata Secret %s was replaced after rotation (UID changed from %s to %s)",
			originalState.UserdataSecretName, originalState.UserdataSecretUID, userdataSecret.UID)
	}

	return nil
}

// verifyCompleteRolloutInvariants verifies that neither NodePools nor their CAPI workloads
// have initiated a rollout. This includes generations, config annotations, and updating conditions.
func verifyCompleteRolloutInvariants(ctx context.Context, client crclient.Client, namespace string, nodePoolsMap map[string]*hyperv1.NodePool, machineDeploymentMap map[string]int64, useCAPIv1Beta1 bool) error {
	// Verify all expected NodePools still exist and haven't changed
	for npName, originalNodePool := range nodePoolsMap {
		currentNodePool := &hyperv1.NodePool{}
		if err := client.Get(ctx, crclient.ObjectKey{Namespace: originalNodePool.Namespace, Name: npName}, currentNodePool); err != nil {
			return fmt.Errorf("NodePool %s not found: %w", npName, err)
		}

		if currentNodePool.Generation != originalNodePool.Generation {
			return fmt.Errorf("NodePool %s generation changed from %d to %d",
				npName, originalNodePool.Generation, currentNodePool.Generation)
		}

		const (
			nodePoolAnnotationCurrentConfig        = "hypershift.openshift.io/nodePoolCurrentConfig"
			nodePoolAnnotationCurrentConfigVersion = "hypershift.openshift.io/nodePoolCurrentConfigVersion"
		)

		if currentNodePool.Annotations[nodePoolAnnotationCurrentConfig] != originalNodePool.Annotations[nodePoolAnnotationCurrentConfig] {
			return fmt.Errorf("NodePool %s current config annotation changed", npName)
		}

		if currentNodePool.Annotations[nodePoolAnnotationCurrentConfigVersion] != originalNodePool.Annotations[nodePoolAnnotationCurrentConfigVersion] {
			return fmt.Errorf("NodePool %s current config version annotation changed", npName)
		}

		// Check updating conditions
		for _, condition := range currentNodePool.Status.Conditions {
			if condition.Type == hyperv1.NodePoolUpdatingVersionConditionType ||
				condition.Type == hyperv1.NodePoolUpdatingConfigConditionType ||
				condition.Type == hyperv1.NodePoolUpdatingPlatformMachineTemplateConditionType {
				if condition.Status != "False" {
					return fmt.Errorf("NodePool %s has updating condition %s=%s (expected False)",
						npName, condition.Type, condition.Status)
				}
			}
		}
	}

	// Verify all expected MachineDeployments/MachineSets still exist and haven't changed
	currentWorkloads, err := listMachineDeploymentGenerations(ctx, client, namespace, useCAPIv1Beta1)
	if err != nil {
		return fmt.Errorf("failed to list current workload generations: %w", err)
	}

	if len(currentWorkloads) != len(machineDeploymentMap) {
		return fmt.Errorf("workload count changed from %d to %d", len(machineDeploymentMap), len(currentWorkloads))
	}

	for name, originalGeneration := range machineDeploymentMap {
		currentGeneration, exists := currentWorkloads[name]
		if !exists {
			return fmt.Errorf("workload %s no longer exists", name)
		}
		if currentGeneration != originalGeneration {
			return fmt.Errorf("workload %s generation changed from %d to %d",
				name, originalGeneration, currentGeneration)
		}
	}

	return nil
}

// listMachineDeploymentGenerations is a helper that lists MachineDeployment or MachineSet generations.
func listMachineDeploymentGenerations(ctx context.Context, client crclient.Client, namespace string, useV1Beta1 bool) (map[string]int64, error) {
	generations := make(map[string]int64)
	if useV1Beta1 {
		list := &capiv1beta1.MachineDeploymentList{}
		if err := client.List(ctx, list, crclient.InNamespace(namespace)); err != nil {
			return nil, err
		}
		for i := range list.Items {
			generations[list.Items[i].Name] = list.Items[i].Generation
		}
	} else {
		list := &capiv1beta2.MachineDeploymentList{}
		if err := client.List(ctx, list, crclient.InNamespace(namespace)); err != nil {
			return nil, err
		}
		for i := range list.Items {
			generations[list.Items[i].Name] = list.Items[i].Generation
		}
	}
	return generations, nil
}
