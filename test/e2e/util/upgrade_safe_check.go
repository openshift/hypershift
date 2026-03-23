package util

import (
	"context"
	"fmt"
	"testing"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	"github.com/openshift/hypershift/hypershift-operator/controllers/manifests"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"

	crclient "sigs.k8s.io/controller-runtime/pkg/client"
)

// UpgradeSafeFieldCheck returns a Predicate that tolerates a status field
// not being available during an upgrade. Once the upgrade is confirmed complete,
// it latches and enforces the provided predicate on every subsequent poll.
//
// The upgrade detection is independent of the field being tested:
//
//   - TargetReleaseImage set: skips until hc.Spec.Release.Image matches the target
//     AND the control-plane-operator CPC reports RolloutComplete=True.
//   - IsHOUpgrade set: skips until the CRD schema includes the field
//     (via HasFieldInCRDSchema).
//
// TargetReleaseImage and IsHOUpgrade are mutually exclusive.
func UpgradeSafeFieldCheck(
	t *testing.T,
	ctx context.Context,
	client crclient.Client,
	fieldPath string,
	uc *UpgradeContext,
	enforce Predicate[*hyperv1.HostedCluster],
) Predicate[*hyperv1.HostedCluster] {
	const crdName = "hostedclusters.hypershift.openshift.io"
	if uc != nil && uc.IsHOUpgrade && uc.TargetReleaseImage != "" {
		t.Fatalf("UpgradeSafeFieldCheck: IsHOUpgrade and TargetReleaseImage are mutually exclusive")
	}

	latched := false
	schemaHasField := false

	return func(hc *hyperv1.HostedCluster) (done bool, reasons string, err error) {
		if latched {
			return enforce(hc)
		}

		if uc == nil {
			latched = true
			return enforce(hc)
		}

		// Gate 1: For HO upgrades, wait for the CRD schema to include the field.
		if uc.IsHOUpgrade && !schemaHasField {
			found, checkErr := HasFieldInCRDSchema(ctx, client, crdName, fieldPath)
			if checkErr != nil {
				return false, fmt.Sprintf("failed to check CRD schema for %s: %v", fieldPath, checkErr), nil
			}
			if !found {
				return true, fmt.Sprintf("CRD schema does not yet include %s (pre-HO-upgrade), skipping", fieldPath), nil
			}
			schemaHasField = true
			t.Logf("UpgradeSafeFieldCheck: %s detected in CRD schema for %s", fieldPath, crdName)
		}

		// Gate 2: For control plane upgrades, wait for the release image to match
		// the target and the CPO to complete its rollout.
		if uc.TargetReleaseImage != "" {
			if hc.Spec.Release.Image != uc.TargetReleaseImage {
				return true, fmt.Sprintf("release image %s != target %s (pre-upgrade), skipping %s",
					hc.Spec.Release.Image, uc.TargetReleaseImage, fieldPath), nil
			}

			rolled, rollErr := isCPORolloutComplete(ctx, client, hc)
			if rollErr != nil {
				return false, fmt.Sprintf("failed to check CPO rollout status for %s: %v", fieldPath, rollErr), nil
			}
			if !rolled {
				return true, fmt.Sprintf("control-plane-operator not yet RolloutComplete, skipping %s", fieldPath), nil
			}
		}

		latched = true
		t.Logf("UpgradeSafeFieldCheck: latch engaged for %s", fieldPath)
		return enforce(hc)
	}
}

// isCPORolloutComplete checks whether the control-plane-operator ControlPlaneComponent
// has RolloutComplete=True in the HCP namespace.
func isCPORolloutComplete(ctx context.Context, client crclient.Client, hc *hyperv1.HostedCluster) (bool, error) {
	hcpNs := manifests.HostedControlPlaneNamespace(hc.Namespace, hc.Name)
	cpc := &hyperv1.ControlPlaneComponent{}
	if err := client.Get(ctx, types.NamespacedName{
		Namespace: hcpNs,
		Name:      "control-plane-operator",
	}, cpc); err != nil {
		if apierrors.IsNotFound(err) {
			return false, nil
		}
		return false, fmt.Errorf("getting control-plane-operator CPC: %w", err)
	}

	for _, c := range cpc.Status.Conditions {
		if c.Type == string(hyperv1.ControlPlaneComponentRolloutComplete) {
			return c.Status == metav1.ConditionTrue, nil
		}
	}
	return false, nil
}
