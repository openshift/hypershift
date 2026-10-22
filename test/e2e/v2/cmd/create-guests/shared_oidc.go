//go:build e2ev2

package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path"
	"strings"
	"time"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	"github.com/openshift/hypershift/support/certs"
	"github.com/openshift/hypershift/test/e2e/v2/lifecycle"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
)

func prepareAutomaticSharedOIDC(ctx context.Context, cfg envConfig, specs []lifecycle.ClusterSpec, setup func(context.Context, string, lifecycle.SharedOIDCOptions) (lifecycle.SharedOIDCInputs, error)) (envConfig, []lifecycle.ClusterSpec, error) {
	if cfg.platform.Name() != "aws" || cfg.sharedOIDCIssuer != "" || cfg.sharedOIDCKeyFile != "" {
		return cfg, specs, nil
	}
	options := cfg.sharedOIDCOptions
	if len(specs) > 0 {
		for index, argument := range specs[0].CreateArgs {
			if strings.HasPrefix(argument, "--additional-tags=") {
				options.AdditionalTags = append(options.AdditionalTags, strings.TrimPrefix(argument, "--additional-tags="))
			} else if argument == "--additional-tags" && index+1 < len(specs[0].CreateArgs) {
				options.AdditionalTags = append(options.AdditionalTags, specs[0].CreateArgs[index+1])
			}
		}
	}
	inputs, err := setup(ctx, cfg.sharedDir, options)
	if err != nil {
		return cfg, nil, fmt.Errorf("preparing automatic shared AWS OIDC: %w", err)
	}
	cfg.sharedOIDCIssuer, cfg.sharedOIDCKeyFile = inputs.IssuerURL, inputs.SigningKeyFile
	configured, err := configureSharedOIDC(cfg, specs)
	if err != nil {
		cleanupCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Minute)
		defer cancel()
		return cfg, nil, errors.Join(err, lifecycle.CleanupSharedOIDC(cleanupCtx, cfg.sharedDir))
	}
	return cfg, configured, err
}

func prepareScenarioInputs(cfg envConfig, specs []lifecycle.ClusterSpec) ([]lifecycle.ClusterSpec, error) {
	if err := validateScenarioInputs(cfg, specs); err != nil {
		return nil, err
	}
	return configureSharedOIDC(cfg, specs)
}

func configureSharedOIDC(cfg envConfig, specs []lifecycle.ClusterSpec) ([]lifecycle.ClusterSpec, error) {
	if cfg.sharedOIDCIssuer == "" && cfg.sharedOIDCKeyFile == "" {
		return specs, nil
	}
	if cfg.platform.Name() != "aws" || cfg.sharedOIDCIssuer == "" || cfg.sharedOIDCKeyFile == "" {
		return nil, fmt.Errorf("shared AWS OIDC requires both E2E_AWS_SHARED_OIDC_ISSUER_URL and E2E_AWS_SHARED_OIDC_SIGNING_KEY_FILE on the AWS platform")
	}
	issuer, err := url.Parse(cfg.sharedOIDCIssuer)
	if err != nil || issuer.Scheme != "https" || issuer.Hostname() == "" || issuer.User != nil || issuer.RawQuery != "" || issuer.ForceQuery || strings.Contains(cfg.sharedOIDCIssuer, "#") {
		return nil, fmt.Errorf("shared AWS OIDC issuer must be an absolute HTTPS URL without credentials, query or fragment")
	}
	data, err := os.ReadFile(cfg.sharedOIDCKeyFile)
	if err != nil {
		return nil, fmt.Errorf("reading shared AWS OIDC signing key: %w", err)
	}
	privateKey, err := certs.PemToPrivateKey(data)
	if err != nil {
		return nil, fmt.Errorf("parsing shared AWS OIDC signing key: %w", err)
	}
	if err := privateKey.Validate(); err != nil {
		return nil, fmt.Errorf("validating shared AWS OIDC signing key: %w", err)
	}
	configured := append([]lifecycle.ClusterSpec(nil), specs...)
	for index := range configured {
		if path.Base(issuer.Path) == lifecycle.DeriveClusterName(cfg.prowJobID, configured[index].Variant) {
			return nil, fmt.Errorf("shared AWS OIDC provider identity must differ from each HostedCluster infrastructure identity")
		}
		for _, argument := range configured[index].CreateArgs {
			if argument == "--oidc-issuer-url" || strings.HasPrefix(argument, "--oidc-issuer-url=") || argument == "--sa-token-issuer-private-key-path" || strings.HasPrefix(argument, "--sa-token-issuer-private-key-path=") {
				return nil, fmt.Errorf("AWS fixture %q already configures its OIDC issuer or signing key", configured[index].Variant)
			}
		}
		configured[index].CreateArgs = append(append([]string{}, configured[index].CreateArgs...), "--oidc-issuer-url="+cfg.sharedOIDCIssuer, "--sa-token-issuer-private-key-path="+cfg.sharedOIDCKeyFile)
	}
	return configured, nil
}

func validateSharedOIDCFixture(cfg envConfig, objects []*unstructured.Unstructured) error {
	if cfg.sharedOIDCIssuer == "" {
		return nil
	}
	key, err := os.ReadFile(cfg.sharedOIDCKeyFile)
	if err != nil {
		return fmt.Errorf("reading rendered shared OIDC signing-key input: %w", err)
	}
	clusters := 0
	for _, object := range objects {
		if object.GetKind() != "HostedCluster" {
			continue
		}
		clusters++
		cluster := &hyperv1.HostedCluster{}
		if err := runtime.DefaultUnstructuredConverter.FromUnstructured(object.Object, cluster); err != nil {
			return fmt.Errorf("decoding shared OIDC HostedCluster: %w", err)
		}
		if cluster.Spec.IssuerURL != cfg.sharedOIDCIssuer || cluster.Spec.ServiceAccountSigningKey == nil || cluster.Spec.ServiceAccountSigningKey.Name == "" {
			return fmt.Errorf("rendered HostedCluster does not preserve the configured shared OIDC issuer and signing-key reference")
		}
		matches := 0
		for _, candidate := range objects {
			if candidate.GetKind() != "Secret" || candidate.GetNamespace() != cluster.Namespace || candidate.GetName() != cluster.Spec.ServiceAccountSigningKey.Name {
				continue
			}
			secret := &corev1.Secret{}
			if err := runtime.DefaultUnstructuredConverter.FromUnstructured(candidate.Object, secret); err != nil {
				return fmt.Errorf("decoding rendered OIDC signing-key Secret: %w", err)
			}
			if !bytes.Equal(secret.Data[hyperv1.ServiceAccountSigningKeySecretKey], key) {
				return fmt.Errorf("rendered OIDC signing-key Secret does not match the configured key file")
			}
			matches++
		}
		if matches != 1 {
			return fmt.Errorf("rendered HostedCluster requires exactly one matching OIDC signing-key Secret, got %d", matches)
		}
	}
	if clusters != 1 {
		return fmt.Errorf("shared OIDC fixture requires exactly one HostedCluster, got %d", clusters)
	}
	return nil
}
