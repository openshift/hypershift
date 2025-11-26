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
	DefaultSubnetCIDR = "10.0.0.0/24"
)

// CreateInfraOptions contains options for creating GCP infrastructure
type CreateInfraOptions struct {
	// Required flags
	ProjectID string
	Region    string
	InfraID   string

	// Optional flags
	VPCCidr    string
	OutputFile string
}

// CreateInfraOutput contains the output from infrastructure creation
type CreateInfraOutput struct {
	Region          string `json:"region"`
	ProjectID       string `json:"projectId"`
	InfraID         string `json:"infraId"`
	NetworkName     string `json:"networkName"`
	NetworkSelfLink string `json:"networkSelfLink"`
	SubnetName      string `json:"subnetName"`
	SubnetSelfLink  string `json:"subnetSelfLink"`
	SubnetCIDR      string `json:"subnetCidr"`
	RouterName      string `json:"routerName"`
	NATName         string `json:"natName"`
}

// NewCreateCommand creates a new cobra command for creating GCP infrastructure
func NewCreateCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:          "gcp",
		Short:        "Creates GCP infrastructure resources for a cluster",
		SilenceUsage: true,
	}

	opts := CreateInfraOptions{
		VPCCidr: DefaultSubnetCIDR,
	}

	cmd.Flags().StringVar(&opts.ProjectID, "project-id", opts.ProjectID, "GCP Project ID (required)")
	cmd.Flags().StringVar(&opts.Region, "region", opts.Region, "GCP region where infrastructure will be created")
	cmd.Flags().StringVar(&opts.InfraID, "infra-id", opts.InfraID, "Infrastructure ID for resource naming (required)")
	cmd.Flags().StringVar(&opts.VPCCidr, "vpc-cidr", opts.VPCCidr, "CIDR block for the subnet")
	cmd.Flags().StringVar(&opts.OutputFile, "output-file", opts.OutputFile, "Path to file that will contain output information from infra resources (optional)")

	_ = cmd.MarkFlagRequired("project-id")
	_ = cmd.MarkFlagRequired("region")
	_ = cmd.MarkFlagRequired("infra-id")

	logger := log.Log
	cmd.PreRunE = func(cmd *cobra.Command, args []string) error {
		return opts.Validate()
	}
	cmd.RunE = func(cmd *cobra.Command, args []string) error {
		if err := opts.Run(cmd.Context(), logger); err != nil {
			logger.Error(err, "Failed to create GCP infrastructure")
			return err
		}
		logger.Info("Successfully created GCP infrastructure")
		return nil
	}

	return cmd
}

// Validate validates the create infrastructure options
func (o *CreateInfraOptions) Validate() error {
	if o.ProjectID == "" {
		return fmt.Errorf("--project-id is required")
	}
	if o.InfraID == "" {
		return fmt.Errorf("--infra-id is required")
	}
	if o.Region == "" {
		return fmt.Errorf("--region is required")
	}
	return nil
}

// Run executes the infrastructure creation
func (o *CreateInfraOptions) Run(ctx context.Context, logger logr.Logger) error {
	result, err := o.CreateInfra(ctx, logger)
	if err != nil {
		return err
	}
	return o.Output(result)
}

// Output writes the infrastructure output to stdout or a file
func (o *CreateInfraOptions) Output(result *CreateInfraOutput) error {
	out := os.Stdout
	if len(o.OutputFile) > 0 {
		var err error
		out, err = os.Create(o.OutputFile)
		if err != nil {
			return fmt.Errorf("cannot create output file: %w", err)
		}
		defer func(out *os.File) {
			_ = out.Close()
		}(out)
	}
	outputBytes, err := json.MarshalIndent(result, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to serialize result: %w", err)
	}
	_, err = out.Write(outputBytes)
	if err != nil {
		return fmt.Errorf("failed to write result: %w", err)
	}
	return nil
}

// CreateInfra creates the GCP infrastructure resources
func (o *CreateInfraOptions) CreateInfra(ctx context.Context, logger logr.Logger) (*CreateInfraOutput, error) {
	logger.Info("Creating GCP infrastructure", "projectID", o.ProjectID, "region", o.Region, "infraID", o.InfraID)

	// Initialize network manager
	networkManager, err := NewNetworkManager(ctx, o.ProjectID, o.InfraID, o.Region, logger)
	if err != nil {
		return nil, fmt.Errorf("failed to initialize network manager: %w", err)
	}

	result := &CreateInfraOutput{
		Region:    o.Region,
		ProjectID: o.ProjectID,
		InfraID:   o.InfraID,
	}

	// Create VPC network
	network, err := networkManager.CreateNetwork(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to create VPC network: %w", err)
	}
	result.NetworkName = network.Name
	result.NetworkSelfLink = network.SelfLink

	// Create subnet
	subnet, err := networkManager.CreateSubnet(ctx, network.SelfLink, o.VPCCidr)
	if err != nil {
		return nil, fmt.Errorf("failed to create subnet: %w", err)
	}
	result.SubnetName = subnet.Name
	result.SubnetSelfLink = subnet.SelfLink
	result.SubnetCIDR = subnet.IpCidrRange

	// Create Cloud Router
	router, err := networkManager.CreateRouter(ctx, network.SelfLink)
	if err != nil {
		return nil, fmt.Errorf("failed to create Cloud Router: %w", err)
	}
	result.RouterName = router.Name

	// Create Cloud NAT
	natName, err := networkManager.CreateNAT(ctx, router.Name, subnet.SelfLink)
	if err != nil {
		return nil, fmt.Errorf("failed to create Cloud NAT: %w", err)
	}
	result.NATName = natName

	return result, nil
}
