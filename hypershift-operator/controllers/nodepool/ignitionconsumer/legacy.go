package ignitionconsumer

import (
	"context"
	"fmt"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	"github.com/openshift/hypershift/support/api"
	payloadstore "github.com/openshift/hypershift/support/ignitionpayload"
	supportutil "github.com/openshift/hypershift/support/util"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
)

// legacyTokenSecretPrefix is the name prefix of the token-{machineSet}-{configVersion} Secret the
// HCCO InPlaceUpgrader reads. It matches the NodePool controller's TokenSecretPrefix.
const legacyTokenSecretPrefix = "token"

// reconcileLegacyInPlaceSecret keeps the unmodified HCCO InPlaceUpgrader working during
// coexistence. For InPlace NodePools it writes a token-{machineSet}-{configHash} Secret in the HCP
// namespace carrying the rendered payload under "payload" (compressed+encoded) and the release
// version under "release-version" — both required by the InPlaceUpgrader. The configHash in the
// name is status.current.configHash, the payload-identity hash the HCCO matches against the
// MachineSet's targetConfigVersion annotation. The Secret is CR-owned so it cascade-deletes with
// the CR; owner references do not affect the HCCO read. It is a no-op for Replace NodePools, whose
// rollout is driven by the generator-authored userData Secret.
//
// In the new model the generator authors the node-facing userData Secret, and the bearer token
// lives only there and in the store — never in status — so the consumer no longer authors userdata.
// This legacy write is the NodePool controller's remaining Secret-authoring responsibility, and it
// resolves payload BYTES (not the token) via PayloadStore.Get(owner, rolloutGeneration), needing the
// HCP-namespace Secret access the consumer already has (CR-only readers do not), so it never
// re-introduces the token into a readable place.
func reconcileLegacyInPlaceSecret(ctx context.Context, c client.Client, store payloadstore.PayloadStore,
	machineSetName string, upgradeType hyperv1.UpgradeType, releaseVersion string,
	current hyperv1.PayloadReference, crOwner *hyperv1.IgnitionPayload) error {
	if upgradeType != hyperv1.UpgradeTypeInPlace {
		return nil
	}

	owner := payloadstore.OwnerRef{Namespace: crOwner.Namespace, Name: crOwner.Name}
	payload, storedConfigHash, err := store.Get(ctx, owner, current.RolloutGeneration)
	if err != nil {
		return fmt.Errorf("failed to read payload for in-place rollout generation %d: %w", current.RolloutGeneration, err)
	}
	// The Secret name carries status.current.configHash, so its bytes must be that payload. A
	// same-generation refresh writes the store before it commits the new configHash to status, so the
	// store can briefly be ahead of the status this reconcile read. Fail rather than write payload b
	// under hash a; the retry reads the committed status and writes the matching Secret.
	if storedConfigHash != current.ConfigHash {
		return fmt.Errorf("stored payload for in-place rollout generation %d has config hash %q, status has %q; waiting for the generator to commit it",
			current.RolloutGeneration, storedConfigHash, current.ConfigHash)
	}
	compressed, err := supportutil.CompressAndEncode(payload)
	if err != nil {
		return fmt.Errorf("failed to compress in-place payload: %w", err)
	}

	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      fmt.Sprintf("%s-%s-%s", legacyTokenSecretPrefix, machineSetName, current.ConfigHash),
			Namespace: crOwner.Namespace,
		},
	}
	if _, err := controllerutil.CreateOrUpdate(ctx, c, secret, func() error {
		if err := controllerutil.SetControllerReference(crOwner, secret, api.Scheme); err != nil {
			return err
		}
		if secret.Data == nil {
			secret.Data = map[string][]byte{}
		}
		secret.Data["payload"] = compressed.Bytes()
		secret.Data["release-version"] = []byte(releaseVersion)
		return nil
	}); err != nil {
		return fmt.Errorf("failed to reconcile legacy in-place token Secret %s/%s: %w", crOwner.Namespace, secret.Name, err)
	}
	return nil
}
