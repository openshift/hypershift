package ignitionpayload

import (
	"context"
	"errors"

	ignserver "github.com/openshift/hypershift/ignition-server/controllers"
	payloadstore "github.com/openshift/hypershift/support/ignitionpayload"
)

// genInputs are the arguments assembled for the MCO pipeline's GetPayload call.
type genInputs struct {
	releaseImage        string
	customConfig        string
	pullSecretHash      string
	trustBundleHash     string
	hcConfigurationHash string
	osImageStream       string
	cloudConfigHash     string
}

// payloadGenerator renders ignition payloads via the MCO pipeline and stores them in the
// PayloadStore keyed by rollout identity (owner + rolloutGeneration), idempotently across leader
// failover.
type payloadGenerator struct {
	store    payloadstore.PayloadStore
	provider ignserver.IgnitionProvider
}

// ensurePayload renders and stores the payload for (owner, rolloutGeneration) only when the store
// does not already hold bytes recorded with configHash. It covers both first generation (no entry
// yet) and the Policy A refresh (same rolloutGeneration, new configHash → overwrite in place). On a
// hit it skips the release-image pull, so steady state is one pull per config version regardless of
// replica count (a failover before Put costs at most one extra pull).
func (g *payloadGenerator) ensurePayload(ctx context.Context, owner payloadstore.OwnerRef, rolloutGeneration int64, configHash string, in genInputs) (generated bool, err error) {
	_, existingHash, err := g.store.Get(ctx, owner, rolloutGeneration)
	if err == nil && existingHash == configHash {
		return false, nil
	}
	if err != nil && !errors.Is(err, payloadstore.ErrNotFound) {
		return false, err
	}

	payload, err := g.render(ctx, in)
	if err != nil {
		return false, err
	}
	if err := g.store.Put(ctx, owner, rolloutGeneration, configHash, payload); err != nil {
		return false, err
	}
	return true, nil
}

func (g *payloadGenerator) render(ctx context.Context, in genInputs) ([]byte, error) {
	return g.provider.GetPayload(ctx, in.releaseImage, in.customConfig, in.pullSecretHash,
		in.trustBundleHash, in.hcConfigurationHash, in.osImageStream, in.cloudConfigHash)
}
