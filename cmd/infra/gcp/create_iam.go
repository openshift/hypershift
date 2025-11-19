package gcp

import (
	"context"
	"encoding/json"
	"fmt"
	"os"

	"github.com/openshift/hypershift/cmd/log"

	"github.com/go-logr/logr"
	"github.com/spf13/cobra"
)

const (
	infraIDFlag         = "infra-id"
	projectIDFlag       = "project-id"
	clusterOIDCJWKSFlag = "oidc-jwks-file"
	outputFileFlag      = "output-file"
	oidcIssuerURLFlag   = "oidc-issuer-url"
)

type CreateIAMOptions struct {
	ProjectID           string
	InfraID             string
	ClusterOIDCJWKSFile string
	OutputFile          string
	OIDCIssuerURL       string
}

type CreateIAMOutput struct {
	ProjectID            string                 `json:"projectId"`
	ProjectNumber        string                 `json:"projectNumber"`
	InfraID              string                 `json:"infraId"`
	WorkloadIdentityPool WorkloadIdentityConfig `json:"workloadIdentityPool"`
	ServiceAccounts      map[string]string      `json:"serviceAccounts"`
}

type WorkloadIdentityConfig struct {
	PoolID     string `json:"poolId"`
	ProviderID string `json:"providerId"`
	Audience   string `json:"audience"`
}

func NewCreateIAMCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:          "gcp",
		Short:        "Creates GCP IAM infrastructure for HyperShift cluster",
		SilenceUsage: true,
	}

	opts := bindOptions(cmd)

	logger := log.Log
	cmd.PreRunE = func(cmd *cobra.Command, args []string) error {
		return opts.ValidateInputs()
	}
	cmd.RunE = func(cmd *cobra.Command, args []string) error {
		if err := opts.Run(cmd.Context(), logger); err != nil {
			logger.Error(err, "Failed to create GCP IAM infrastructure")
			return err
		}
		return nil
	}

	return cmd
}

func bindOptions(cmd *cobra.Command) *CreateIAMOptions {
	o := &CreateIAMOptions{}
	cmd.Flags().StringVar(&o.InfraID, infraIDFlag, o.InfraID, "Infrastructure ID to use for GCP resources.")
	cmd.Flags().StringVar(&o.ProjectID, projectIDFlag, o.ProjectID, "GCP Project ID where resources will be created")
	cmd.Flags().StringVar(&o.ClusterOIDCJWKSFile, clusterOIDCJWKSFlag, o.ClusterOIDCJWKSFile, "Path to a local JSON file containing OIDC provider's public key in JWKS format")
	cmd.Flags().StringVar(&o.OutputFile, outputFileFlag, o.OutputFile, "Path to output JSON file with GSA details (optional)")
	cmd.Flags().StringVar(&o.OIDCIssuerURL, oidcIssuerURLFlag, o.OIDCIssuerURL, "Custom OIDC issuer URL (optional, defaults to https://hypershift-<infra-id>-oidc)")

	_ = cmd.MarkFlagRequired(infraIDFlag)
	_ = cmd.MarkFlagRequired(projectIDFlag)
	_ = cmd.MarkFlagRequired(clusterOIDCJWKSFlag)
	return o
}

func (o *CreateIAMOptions) ValidateInputs() error {
	if o.InfraID == "" {
		return fmt.Errorf("infra-id is required")
	}
	if o.ProjectID == "" {
		return fmt.Errorf("project-id is required")
	}
	if o.ClusterOIDCJWKSFile == "" {
		return fmt.Errorf("oidc-jwks-file is required")
	}

	if err := o.ValidateJWKSFile(); err != nil {
		return fmt.Errorf("invalid JWKS file: %w", err)
	}

	return nil
}

func (o *CreateIAMOptions) Run(ctx context.Context, logger logr.Logger) error {
	results, err := o.CreateIAM(ctx, logger)
	if err != nil {
		return err
	}
	return o.Output(results, logger)
}

func (o *CreateIAMOptions) Output(results *CreateIAMOutput, logger logr.Logger) error {
	out := os.Stdout
	if len(o.OutputFile) > 0 {
		var err error
		out, err = os.Create(o.OutputFile)
		if err != nil {
			return fmt.Errorf("cannot create output file: %w", err)
		}
		defer func(out *os.File) {
			if err := out.Close(); err != nil {
				logger.Error(err, "Failed to close output file", "file", o.OutputFile)
			}
		}(out)
	}
	outputBytes, err := json.MarshalIndent(results, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to serialize result: %w", err)
	}
	_, err = out.Write(outputBytes)
	if err != nil {
		return fmt.Errorf("failed to write result: %w", err)
	}
	return nil
}

func (o *CreateIAMOptions) CreateIAM(ctx context.Context, logger logr.Logger) (*CreateIAMOutput, error) {

	// Use IAMManager for all GCP API interactions
	iamManager, err := NewIAMManager(ctx, o.ProjectID, o.InfraID, o.ClusterOIDCJWKSFile, logger)
	if err != nil {
		return nil, fmt.Errorf("failed to initialize GCP clients: %w", err)
	}

	// Set custom OIDC issuer URL if provided
	if o.OIDCIssuerURL != "" {
		iamManager.SetOIDCIssuerURL(o.OIDCIssuerURL)
	}

	projectNumber, err := iamManager.GetProjectNumber(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to validate project: %w", err)
	}

	results := &CreateIAMOutput{
		ProjectID:            o.ProjectID,
		ProjectNumber:        projectNumber,
		InfraID:              o.InfraID,
		WorkloadIdentityPool: WorkloadIdentityConfig{},
	}

	poolID, err := iamManager.CreateWorkloadIdentityPool(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to create Workload Identity Pool: %w", err)
	}

	providerID, providerAudience, err := iamManager.CreateOIDCProvider(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to create OIDC Provider: %w", err)
	}

	// Create service accounts, assign roles, and setup WIF bindings
	serviceAccountEmails, err := iamManager.CreateServiceAccounts(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to create service accounts: %w", err)
	}

	results.WorkloadIdentityPool = WorkloadIdentityConfig{
		PoolID:     poolID,
		ProviderID: providerID,
		Audience:   providerAudience,
	}
	results.ServiceAccounts = serviceAccountEmails

	logger.Info("Created GCP IAM infrastructure", "infraID", o.InfraID, "projectID", o.ProjectID, "serviceAccountsCreated", len(serviceAccountEmails))

	return results, nil
}

func (o *CreateIAMOptions) ValidateJWKSFile() error {
	// Read and validate the JWKS file format
	jwksData, err := os.ReadFile(o.ClusterOIDCJWKSFile)
	if err != nil {
		if os.IsNotExist(err) {
			return fmt.Errorf("cluster OIDC JWKS file does not exist: %s", o.ClusterOIDCJWKSFile)
		}
		return fmt.Errorf("failed to read JWKS file: %w", err)
	}

	// Basic JSON validation
	var jwks map[string]interface{}
	if err := json.Unmarshal(jwksData, &jwks); err != nil {
		return fmt.Errorf("JWKS file contains invalid JSON: %w", err)
	}

	// Validate that it contains the expected JWKS structure
	if _, exists := jwks["keys"]; !exists {
		return fmt.Errorf("JWKS file must contain a 'keys' field")
	}

	return nil
}
