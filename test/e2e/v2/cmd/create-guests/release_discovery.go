//go:build e2ev2

package main

import (
	"context"
	"errors"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"

	controlplaneoperatoroverrides "github.com/openshift/hypershift/hypershift-operator/controlplaneoperator-overrides"
	"github.com/openshift/hypershift/support/supportedversion"
	"github.com/openshift/hypershift/test/e2e/v2/lifecycle"

	crclient "sigs.k8s.io/controller-runtime/pkg/client"
)

func resolveReleaseImages(ctx context.Context, cfg envConfig, newClient func() (crclient.WithWatch, error)) (envConfig, error) {
	if cfg.releaseImage == "" {
		client, err := newClient()
		if err != nil {
			return cfg, fmt.Errorf("creating management cluster client for default release lookup: %w", err)
		}
		version, err := supportedversion.LookupDefaultOCPVersion(ctx, "", client)
		if err != nil {
			return cfg, fmt.Errorf("looking up default release image: %w", err)
		}
		if strings.TrimSpace(version.PullSpec) == "" {
			return cfg, fmt.Errorf("default release lookup returned an empty pull spec")
		}
		cfg.releaseImage = version.PullSpec
	}
	if cfg.n1Image == "" {
		cfg.n1Image = cfg.releaseImage
	}
	return cfg, nil
}

func prepareCreatePlan(ctx context.Context, cfg envConfig, newClient func() (crclient.WithWatch, error)) (envConfig, []lifecycle.ClusterSpec, bool, error) {
	plan, planErr := lifecycle.ResolveTestPlan(cfg.testPlanPath, cfg.platform)
	if planErr == nil {
		var err error
		cfg, err = defaultMappedCPOReleaseImages(cfg, plan)
		if err != nil {
			return cfg, nil, false, err
		}
	}
	cfg, err := resolveReleaseImages(ctx, cfg, newClient)
	if err != nil {
		return cfg, nil, false, err
	}
	if planErr != nil {
		return cfg, nil, false, fmt.Errorf("resolving test plan: %w", planErr)
	}
	log.Printf("Using test plan %q", plan.Name)
	allSpecs := cfg.platform.ClusterSpecs(cfg.releaseImage, cfg.n1Image)
	if err := plan.Validate(allSpecs); err != nil {
		return cfg, nil, false, err
	}
	specs := plan.FilterClusterSpecs(allSpecs)
	cpoOverrides, err := validateCPOOverrideSelection(plan, cfg.platform.Name(), specs, os.Getenv("TEST_CPO_OVERRIDE") == "1")
	if err != nil {
		return cfg, nil, false, err
	}
	specs, err = prepareScenarioInputs(cfg, specs)
	if err != nil {
		return cfg, nil, false, err
	}
	return cfg, specs, cpoOverrides, nil
}

func defaultMappedCPOReleaseImages(cfg envConfig, plan *lifecycle.TestPlan) (envConfig, error) {
	selected := false
	for _, variant := range plan.TestMatrix.Variants() {
		selected = selected || variant == "cpo-override-latest" || variant == "cpo-override-previous"
	}
	if !selected {
		return cfg, nil
	}
	allSpecs := cfg.platform.ClusterSpecs(cfg.releaseImage, cfg.n1Image)
	if err := plan.Validate(allSpecs); err != nil {
		return cfg, err
	}
	if _, err := validateCPOOverrideSelection(plan, cfg.platform.Name(), plan.FilterClusterSpecs(allSpecs), os.Getenv("TEST_CPO_OVERRIDE") == "1"); err != nil {
		return cfg, err
	}
	if cfg.releaseImage == "" {
		latest, previous := controlplaneoperatoroverrides.LatestOverrideTestReleases(cfg.platform.Name())
		cfg.releaseImage = latest
		if cfg.n1Image == "" {
			cfg.n1Image = previous
		}
	}
	return cfg, nil
}

func validateCreateState(cfg envConfig) error {
	if cfg.sharedOIDCIssuer != "" || cfg.sharedOIDCKeyFile != "" {
		if cfg.platform.Name() != "aws" || cfg.sharedOIDCIssuer == "" || cfg.sharedOIDCKeyFile == "" {
			return fmt.Errorf("shared AWS OIDC requires both E2E_AWS_SHARED_OIDC_ISSUER_URL and E2E_AWS_SHARED_OIDC_SIGNING_KEY_FILE on the AWS platform")
		}
	}
	if cfg.platform.Name() == "aws" {
		if _, err := os.Stat(filepath.Join(cfg.sharedDir, lifecycle.SharedOIDCStateFileName)); err == nil {
			return fmt.Errorf("shared OIDC ownership state already exists; finish destroy-guests before reusing SHARED_DIR")
		} else if !errors.Is(err, os.ErrNotExist) {
			return err
		}
	}
	return nil
}
