package azure

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	cmdutil "github.com/openshift/hypershift/cmd/util"
	"github.com/openshift/hypershift/pkg/manifests"
	"github.com/openshift/hypershift/support/azureutil"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
	"github.com/Azure/azure-sdk-for-go/sdk/azidentity"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/compute/armcompute/v5"

	utilerrors "k8s.io/apimachinery/pkg/util/errors"

	capiazure "sigs.k8s.io/cluster-api-provider-azure/api/v1beta1"
	clusterv1 "sigs.k8s.io/cluster-api/api/core/v1beta1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/go-logr/logr"
)

const (
	consoleLogsDirectory = "machine-console-logs"
	journalsDirectory    = "machine-journals"

	maxConcurrentMachines = 4
	maxSerialLogSize      = 32 << 20
	consoleLogTimeout     = 2 * time.Minute
	runCommandTimeout     = 2 * time.Minute
)

const journalCommand = "journalctl --no-pager --boot=0 --lines=1000 2>&1 | tail -c 3500"

type computeClient interface {
	RetrieveSerialConsoleLogURI(ctx context.Context, resourceGroup, vmName string) (string, error)
	RunJournalCommand(ctx context.Context, resourceGroup, vmName string) (string, error)
}

type computeClientFactory func(subscriptionID string) (computeClient, error)

// DumpMachineDiagnostics collects Azure VM serial console logs and best-effort
// worker journals for the HostedCluster. Azure credentials are read from the
// same credentials file used to create Azure HostedClusters.
func DumpMachineDiagnostics(ctx context.Context, kubeClient client.Client, hostedCluster *hyperv1.HostedCluster, credentialsFile, artifactDir string, logger logr.Logger) error {
	if credentialsFile == "" {
		return errors.New("azure credentials file is not configured")
	}

	machines, err := listAzureMachines(ctx, kubeClient, hostedCluster)
	if err != nil {
		return fmt.Errorf("failed to list AzureMachines: %w", err)
	}
	if len(machines) == 0 {
		logger.Info("No AzureMachines found for HostedCluster; skipping Azure machine diagnostics", "namespace", hostedCluster.Namespace, "name", hostedCluster.Name)
		return nil
	}

	credentials, err := cmdutil.ReadCredentials(credentialsFile)
	if err != nil {
		return fmt.Errorf("failed to read Azure credentials: %w", err)
	}
	if credentials.ClientID == "" || credentials.TenantID == "" || credentials.ClientSecret == "" {
		return errors.New("azure credentials file must contain clientId, tenantId, and clientSecret")
	}
	if hostedCluster.Spec.Platform.Azure == nil {
		return errors.New("hostedcluster Azure platform configuration is missing")
	}
	cloudConfig, err := azureutil.GetAzureCloudConfiguration(hostedCluster.Spec.Platform.Azure.Cloud)
	if err != nil {
		return fmt.Errorf("failed to resolve Azure cloud environment: %w", err)
	}

	credential, err := azidentity.NewClientSecretCredential(credentials.TenantID, credentials.ClientID, credentials.ClientSecret, &azidentity.ClientSecretCredentialOptions{
		ClientOptions: azcore.ClientOptions{Cloud: cloudConfig},
	})
	if err != nil {
		return fmt.Errorf("failed to create Azure credentials: %w", err)
	}
	factory := func(subscriptionID string) (computeClient, error) {
		vmClient, err := armcompute.NewVirtualMachinesClient(subscriptionID, credential, &arm.ClientOptions{
			ClientOptions: policy.ClientOptions{Cloud: cloudConfig},
		})
		if err != nil {
			return nil, fmt.Errorf("failed to create Azure compute client: %w", err)
		}
		return sdkComputeClient{virtualMachines: vmClient}, nil
	}

	return collectMachineDiagnostics(ctx, machines, artifactDir, factory, logger)
}

func listAzureMachines(ctx context.Context, kubeClient client.Client, hostedCluster *hyperv1.HostedCluster) ([]capiazure.AzureMachine, error) {
	machineList := &capiazure.AzureMachineList{}
	if err := kubeClient.List(ctx, machineList,
		client.InNamespace(manifests.HostedControlPlaneNamespace(hostedCluster.Namespace, hostedCluster.Name)),
		client.MatchingLabels{clusterv1.ClusterNameLabel: hostedCluster.Spec.InfraID},
	); err != nil {
		return nil, err
	}
	return machineList.Items, nil
}

func collectMachineDiagnostics(ctx context.Context, machines []capiazure.AzureMachine, artifactDir string, newClient computeClientFactory, logger logr.Logger) error {
	if err := os.MkdirAll(filepath.Join(artifactDir, consoleLogsDirectory), 0755); err != nil {
		return fmt.Errorf("failed to create Azure console log directory: %w", err)
	}
	if err := os.MkdirAll(filepath.Join(artifactDir, journalsDirectory), 0755); err != nil {
		return fmt.Errorf("failed to create Azure journal directory: %w", err)
	}

	var wg sync.WaitGroup
	semaphore := make(chan struct{}, maxConcurrentMachines)
	machineErrors := make([]error, len(machines))
	for i := range machines {
		select {
		case semaphore <- struct{}{}:
		case <-ctx.Done():
			machineErrors[i] = ctx.Err()
			continue
		}
		wg.Add(1)
		go func(index int, machine capiazure.AzureMachine) {
			defer wg.Done()
			defer func() { <-semaphore }()
			machineErrors[index] = collectOneMachine(ctx, machine, artifactDir, newClient, logger)
		}(i, machines[i])
	}
	wg.Wait()

	var errs []error
	for i, err := range machineErrors {
		if err != nil {
			errs = append(errs, fmt.Errorf("AzureMachine %s: %w", machines[i].Name, err))
		}
	}
	return utilerrors.NewAggregate(errs)
}

func collectOneMachine(ctx context.Context, machine capiazure.AzureMachine, artifactDir string, newClient computeClientFactory, logger logr.Logger) error {
	if machine.Spec.ProviderID == nil || strings.TrimSpace(*machine.Spec.ProviderID) == "" {
		logger.Info("Skipping AzureMachine without a provider ID", "machine", machine.Name)
		return nil
	}
	subscriptionID, resourceGroup, vmName, err := parseAzureVMResourceID(*machine.Spec.ProviderID)
	if err != nil {
		return fmt.Errorf("cannot identify Azure VM: %w", err)
	}
	vmClient, err := newClient(subscriptionID)
	if err != nil {
		return fmt.Errorf("failed to create Azure VM client: %w", err)
	}

	var errs []error
	if !bootDiagnosticsDisabled(&machine) {
		if err := collectSerialConsoleLog(ctx, vmClient, machine.Name, resourceGroup, vmName, artifactDir); err != nil {
			errs = append(errs, err)
		}
	} else {
		logger.Info("Skipping disabled Azure boot diagnostics", "machine", machine.Name)
	}

	if err := collectJournal(ctx, vmClient, machine.Name, resourceGroup, vmName, artifactDir); err != nil {
		errs = append(errs, err)
	}
	return utilerrors.NewAggregate(errs)
}

func collectSerialConsoleLog(ctx context.Context, vmClient computeClient, machineName, resourceGroup, vmName, artifactDir string) error {
	requestCtx, cancel := context.WithTimeout(ctx, consoleLogTimeout)
	serialLogURI, err := vmClient.RetrieveSerialConsoleLogURI(requestCtx, resourceGroup, vmName)
	cancel()
	if err != nil {
		return fmt.Errorf("failed to retrieve Azure boot diagnostics: %w", err)
	}
	if serialLogURI == "" {
		return nil
	}

	content, err := downloadSerialConsoleLog(ctx, &http.Client{Timeout: consoleLogTimeout}, serialLogURI)
	if err != nil {
		return err
	}
	logFile := filepath.Join(artifactDir, consoleLogsDirectory, machineName+".log")
	if err := os.WriteFile(logFile, content, 0644); err != nil {
		return fmt.Errorf("failed to write Azure serial console log: %w", err)
	}
	return nil
}

func collectJournal(ctx context.Context, vmClient computeClient, machineName, resourceGroup, vmName, artifactDir string) error {
	requestCtx, cancel := context.WithTimeout(ctx, runCommandTimeout)
	journal, err := vmClient.RunJournalCommand(requestCtx, resourceGroup, vmName)
	cancel()
	if err != nil {
		return fmt.Errorf("failed to retrieve worker journal: %w", err)
	}
	if journal == "" {
		return nil
	}
	logFile := filepath.Join(artifactDir, journalsDirectory, machineName+".log")
	if err := os.WriteFile(logFile, []byte(journal), 0644); err != nil {
		return fmt.Errorf("failed to write Azure worker journal: %w", err)
	}
	return nil
}

func bootDiagnosticsDisabled(machine *capiazure.AzureMachine) bool {
	if machine.Spec.Diagnostics == nil || machine.Spec.Diagnostics.Boot == nil {
		return false
	}
	return machine.Spec.Diagnostics.Boot.StorageAccountType == capiazure.DisabledDiagnosticsStorage
}

func parseAzureVMResourceID(providerID string) (subscriptionID, resourceGroup, vmName string, err error) {
	parsed, err := url.Parse(strings.TrimSpace(providerID))
	if err != nil || !strings.EqualFold(parsed.Scheme, "azure") || parsed.Host != "" {
		return "", "", "", errors.New("provider ID is not a valid Azure resource ID")
	}
	parts := strings.Split(strings.Trim(parsed.EscapedPath(), "/"), "/")
	decoded := make([]string, len(parts))
	for i, part := range parts {
		decoded[i], err = url.PathUnescape(part)
		if err != nil || strings.Contains(decoded[i], "/") {
			return "", "", "", errors.New("provider ID contains an invalid resource ID segment")
		}
	}

	for i := 0; i+7 < len(decoded); i++ {
		if strings.EqualFold(decoded[i], "subscriptions") &&
			strings.EqualFold(decoded[i+2], "resourceGroups") &&
			strings.EqualFold(decoded[i+4], "providers") &&
			strings.EqualFold(decoded[i+5], "Microsoft.Compute") &&
			strings.EqualFold(decoded[i+6], "virtualMachines") {
			if decoded[i+1] == "" || decoded[i+3] == "" || decoded[i+7] == "" {
				break
			}
			return decoded[i+1], decoded[i+3], decoded[i+7], nil
		}
	}
	return "", "", "", errors.New("provider ID does not identify an Azure virtual machine")
}

func downloadSerialConsoleLog(ctx context.Context, httpClient *http.Client, serialLogURI string) ([]byte, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, serialLogURI, nil)
	if err != nil {
		// The URL contains a SAS token. Do not wrap errors that may include it.
		return nil, errors.New("azure returned an invalid serial console log URL")
	}
	response, err := httpClient.Do(request)
	if err != nil {
		// net/http errors include the request URL, including its SAS query string.
		return nil, errors.New("failed to download Azure serial console log")
	}
	defer response.Body.Close()
	if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
		return nil, fmt.Errorf("azure serial console log download returned HTTP status %d", response.StatusCode)
	}

	content, err := io.ReadAll(io.LimitReader(response.Body, maxSerialLogSize+1))
	if err != nil {
		return nil, errors.New("failed to read Azure serial console log response")
	}
	if len(content) > maxSerialLogSize {
		return nil, fmt.Errorf("azure serial console log exceeds the %d byte size limit", maxSerialLogSize)
	}
	return content, nil
}

type sdkComputeClient struct {
	virtualMachines *armcompute.VirtualMachinesClient
}

func (c sdkComputeClient) RetrieveSerialConsoleLogURI(ctx context.Context, resourceGroup, vmName string) (string, error) {
	response, err := c.virtualMachines.RetrieveBootDiagnosticsData(ctx, resourceGroup, vmName, nil)
	if err != nil {
		return "", err
	}
	if response.SerialConsoleLogBlobURI == nil {
		return "", nil
	}
	return *response.SerialConsoleLogBlobURI, nil
}

func (c sdkComputeClient) RunJournalCommand(ctx context.Context, resourceGroup, vmName string) (string, error) {
	commandID := "RunShellScript"
	script := journalCommand
	poller, err := c.virtualMachines.BeginRunCommand(ctx, resourceGroup, vmName, armcompute.RunCommandInput{
		CommandID: &commandID,
		Script:    []*string{&script},
	}, nil)
	if err != nil {
		return "", err
	}
	response, err := poller.PollUntilDone(ctx, nil)
	if err != nil {
		return "", err
	}

	var output []string
	for _, status := range response.Value {
		if status == nil || status.Code == nil || status.Message == nil {
			continue
		}
		if strings.Contains(*status.Code, "StdOut") || strings.Contains(*status.Code, "StdErr") {
			output = append(output, *status.Message)
		}
	}
	return strings.Join(output, "\n"), nil
}
