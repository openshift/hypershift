package azure

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/openshift/hypershift/cmd/cluster/core"
	azureinfra "github.com/openshift/hypershift/cmd/infra/azure"
	"github.com/openshift/hypershift/cmd/log"
	"github.com/openshift/hypershift/cmd/util"

	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/resources/armresources"
	"github.com/spf13/cobra"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/errors"
	crclient "sigs.k8s.io/controller-runtime/pkg/client"
)

func NewDestroyCommand(opts *core.DestroyOptions) *cobra.Command {
	cmd := &cobra.Command{
		Use:          "azure",
		Short:        "Destroys a HostedCluster and its associated infrastructure on Azure",
		SilenceUsage: true,
	}

	opts.AzurePlatform.Location = "eastus"
	cmd.Flags().StringVar(&opts.AzurePlatform.CredentialsFile, "azure-creds", opts.AzurePlatform.CredentialsFile, "Path to an Azure credentials file (required)")
	cmd.Flags().StringVar(&opts.AzurePlatform.Location, "location", opts.AzurePlatform.Location, "Location for the cluster")
	cmd.Flags().StringVar(&opts.AzurePlatform.ResourceGroupName, "resource-group-name", opts.AzurePlatform.ResourceGroupName, "The name of the resource group containing the HostedCluster infrastructure resources that need to be destroyed.")

	_ = cmd.MarkFlagRequired("azure-creds")

	logger := log.Log
	cmd.Run = func(cmd *cobra.Command, args []string) {
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()

		sigs := make(chan os.Signal, 1)
		signal.Notify(sigs, syscall.SIGINT)
		go func() {
			<-sigs
			cancel()
		}()

		if err := DestroyCluster(ctx, opts); err != nil {
			logger.Error(err, "Failed to destroy cluster")
			os.Exit(1)
		}
	}

	return cmd
}
func DestroyCluster(ctx context.Context, o *core.DestroyOptions) error {
	hostedCluster, err := core.GetCluster(ctx, o)
	if err != nil {
		return err
	}

	if hostedCluster != nil {
		o.InfraID = hostedCluster.Spec.InfraID
		o.AzurePlatform.Location = hostedCluster.Spec.Platform.Azure.Location
	}

	var inputErrors []error
	if o.InfraID == "" {
		inputErrors = append(inputErrors, fmt.Errorf("infrastructure ID is required"))
	}
	if o.AzurePlatform.Location == "" {
		inputErrors = append(inputErrors, fmt.Errorf("location is required"))
	}
	if err := errors.NewAggregate(inputErrors); err != nil {
		return fmt.Errorf("required inputs are missing: %w", err)
	}

	// Verify a user provided resource group name is correct by trying to retrieve it before carrying on with deleting things
	if o.AzurePlatform.ResourceGroupName != "" {
		// Setup subscription ID and Azure credential information
		subscriptionID, azureCreds, err := util.SetupAzureCredentials(o.Log, nil, o.AzurePlatform.CredentialsFile)
		if err != nil {
			return fmt.Errorf("failed to setup Azure credentials: %w", err)
		}

		// Setup Azure resource group client
		resourceGroupClient, err := armresources.NewResourceGroupsClient(subscriptionID, azureCreds, nil)
		if err != nil {
			return fmt.Errorf("failed to create new resource groups client: %w", err)
		}

		if _, err = resourceGroupClient.Get(ctx, o.AzurePlatform.ResourceGroupName, nil); err != nil {
			return fmt.Errorf("failed to get resource group name, '%s': %w", o.AzurePlatform.ResourceGroupName, err)
		}
	} else {
		o.AzurePlatform.ResourceGroupName = o.Name + "-" + o.InfraID
	}

	client, err := util.GetClient()
	if err != nil {
		return fmt.Errorf("failed to get client: %w", err)
	}

	techPreviewCM := &corev1.ConfigMap{ObjectMeta: metav1.ObjectMeta{Namespace: "hypershift", Name: "feature-gate"}}
	if err := client.Get(context.Background(), crclient.ObjectKeyFromObject(techPreviewCM), techPreviewCM); err != nil && !apierrors.IsNotFound(err) {
		return fmt.Errorf("failed to retrieve feature-gate ConfigMap: %w", err)
	}

	if techPreviewCM.Data["TechPreviewEnabled"] == "true" {
		o.TechPreviewEnabled = true
	}

	if hostedCluster != nil && o.TechPreviewEnabled {
		o.AzurePlatform.ControlPlaneMIs = hostedCluster.Spec.Platform.Azure.ManagedIdentities
	}

	return core.DestroyCluster(ctx, hostedCluster, o, destroyPlatformSpecifics)
}

func destroyPlatformSpecifics(ctx context.Context, o *core.DestroyOptions) error {
	destroyInfraOptions := &azureinfra.DestroyInfraOptions{
		Name:              o.Name,
		Location:          o.AzurePlatform.Location,
		InfraID:           o.InfraID,
		CredentialsFile:   o.AzurePlatform.CredentialsFile,
		ResourceGroupName: o.AzurePlatform.ResourceGroupName,
	}

	if o.TechPreviewEnabled {
		destroyInfraOptions.TechPreviewEnabled = o.TechPreviewEnabled
		destroyInfraOptions.ControlPlaneMIs = o.AzurePlatform.ControlPlaneMIs
	}
	return destroyInfraOptions.Run(ctx, o.Log)
}
