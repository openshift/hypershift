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
	"sort"
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
	"k8s.io/client-go/rest"

	capiazure "sigs.k8s.io/cluster-api-provider-azure/api/v1beta1"
	clusterv1 "sigs.k8s.io/cluster-api/api/core/v1beta1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/go-logr/logr"
)

const (
	consoleLogsDirectory  = "machine-console-logs"
	journalsDirectory     = "machine-journals"
	maxConcurrentMachines = 4
	maxSerialLogSize      = 32 << 20
	consoleLogTimeout     = 2 * time.Minute
	collectionTimeout     = 10 * time.Minute
)

type bootDiagnosticsState string

const (
	bootEnabled  bootDiagnosticsState = "enabled"
	bootDisabled bootDiagnosticsState = "disabled"
	bootUnknown  bootDiagnosticsState = "unknown"
)

type computeClient interface {
	RetrieveSerialConsoleLogURI(context.Context, string, string) (string, error)
	BootDiagnosticsState(context.Context, string, string) (bootDiagnosticsState, error)
}
type computeClientFactory func(string) (computeClient, error)

// DiagnosticsOptions configures independent serial and journal collection.
// SSH is attempted only after unsuccessful API journal collection and only when configured.
type DiagnosticsOptions struct {
	CredentialsFile  string
	ArtifactDir      string
	ManagementConfig *rest.Config
	SSH              *SSHOptions
	// SerialOnly avoids guest access when validating workers that cannot register.
	SerialOnly       bool
	serialHTTPClient *http.Client
}

// DumpMachineDiagnostics collects diagnostics best-effort, returning aggregate
// collection errors. Every discovered machine's outcome is saved in the summary.
func DumpMachineDiagnostics(ctx context.Context, kubeClient client.Client, hc *hyperv1.HostedCluster, credentialsFile, artifactDir string, logger logr.Logger) error {
	_, err := CollectMachineDiagnostics(ctx, kubeClient, hc, DiagnosticsOptions{CredentialsFile: credentialsFile, ArtifactDir: artifactDir}, logger)
	return err
}

// CollectMachineDiagnostics collects serial logs before attempting journals.
// Missing Azure credentials do not prevent API or configured SSH journal access.
// The report is also written to machine-diagnostics.json, including empty discovery.
func CollectMachineDiagnostics(ctx context.Context, kubeClient client.Client, hc *hyperv1.HostedCluster, opts DiagnosticsOptions, logger logr.Logger) (CollectionReport, error) {
	ctx, cancel := context.WithTimeout(ctx, collectionTimeout)
	defer cancel()
	if hc == nil || hc.Spec.Platform.Type != hyperv1.AzurePlatform {
		return CollectionReport{}, errors.New("an Azure HostedCluster is required")
	}
	if opts.ArtifactDir == "" {
		return CollectionReport{}, errors.New("an artifact directory is required")
	}
	machines, err := listAzureMachines(ctx, kubeClient, hc)
	if err != nil {
		report := CollectionReport{Error: "failed to list AzureMachines"}
		return report, utilerrors.NewAggregate([]error{errors.New(report.Error), writeReport(opts.ArtifactDir, report)})
	}
	factory, factoryErr := newComputeClientFactory(hc, opts.CredentialsFile)
	if factoryErr != nil {
		factory = func(string) (computeClient, error) { return nil, factoryErr }
	}
	lifetimeCtx := ctx
	apiFactory := func(ctx context.Context) (JournalCollector, func(), error) {
		return newAPIJournalCollector(ctx, lifetimeCtx, kubeClient, hc, opts.ManagementConfig)
	}
	return collectMachineDiagnostics(ctx, machines, opts, factory, apiFactory, logger)
}

func newComputeClientFactory(hc *hyperv1.HostedCluster, credentialsFile string) (computeClientFactory, error) {
	if credentialsFile == "" {
		return nil, errors.New("azure credentials file is not configured")
	}
	credentials, err := cmdutil.ReadCredentials(credentialsFile)
	if err != nil {
		return nil, errors.New("failed to read Azure credentials file")
	}
	if credentials.ClientID == "" || credentials.TenantID == "" || credentials.ClientSecret == "" {
		return nil, errors.New("azure credentials file must contain clientId, tenantId, and clientSecret")
	}
	if hc.Spec.Platform.Azure == nil {
		return nil, errors.New("HostedCluster Azure configuration is missing")
	}
	cloudConfig, err := azureutil.GetAzureCloudConfiguration(hc.Spec.Platform.Azure.Cloud)
	if err != nil {
		return nil, errors.New("failed to resolve Azure cloud environment")
	}
	credential, err := azidentity.NewClientSecretCredential(credentials.TenantID, credentials.ClientID, credentials.ClientSecret, &azidentity.ClientSecretCredentialOptions{ClientOptions: azcore.ClientOptions{Cloud: cloudConfig}})
	if err != nil {
		return nil, errors.New("failed to create Azure credentials")
	}
	return func(subscriptionID string) (computeClient, error) {
		vmClient, err := armcompute.NewVirtualMachinesClient(subscriptionID, credential, &arm.ClientOptions{ClientOptions: policy.ClientOptions{Cloud: cloudConfig}})
		if err != nil {
			return nil, errors.New("failed to create Azure compute client")
		}
		return sdkComputeClient{virtualMachines: vmClient}, nil
	}, nil
}

func listAzureMachines(ctx context.Context, kubeClient client.Client, hc *hyperv1.HostedCluster) ([]capiazure.AzureMachine, error) {
	list := &capiazure.AzureMachineList{}
	err := kubeClient.List(ctx, list, client.InNamespace(manifests.HostedControlPlaneNamespace(hc.Namespace, hc.Name)), client.MatchingLabels{clusterv1.ClusterNameLabel: hc.Spec.InfraID})
	sort.Slice(list.Items, func(i, j int) bool { return list.Items[i].Name < list.Items[j].Name })
	return list.Items, err
}

type apiJournalFactory func(context.Context) (JournalCollector, func(), error)

func collectMachineDiagnostics(ctx context.Context, machines []capiazure.AzureMachine, opts DiagnosticsOptions, factory computeClientFactory, apiFactory apiJournalFactory, logger logr.Logger) (CollectionReport, error) {
	artifactDir := opts.ArtifactDir
	report := CollectionReport{Machines: make([]MachineResult, len(machines))}
	if err := os.MkdirAll(filepath.Join(artifactDir, consoleLogsDirectory), 0755); err != nil {
		return report, err
	}
	if err := os.MkdirAll(filepath.Join(artifactDir, journalsDirectory), 0755); err != nil {
		return report, err
	}
	for i := range machines {
		report.Machines[i] = MachineResult{Name: machines[i].Name, BootDiagnostics: string(bootUnknown), Journals: map[string]ArtifactResult{}}
	}
	// Journals cannot consume the serial collection budget for later machines.
	parallelMachines(ctx, len(machines), func(i int) {
		state, result := collectSerialConsoleLog(ctx, factory, machines[i], artifactDir, opts.serialHTTPClient)
		report.Machines[i].BootDiagnostics = string(state)
		report.Machines[i].Serial = result
	})
	var api JournalCollector
	var apiErr error
	if len(machines) > 0 && !opts.SerialOnly {
		setupCtx, cancel := context.WithTimeout(ctx, journalTimeout)
		var cleanup func()
		api, cleanup, apiErr = initializeAPIJournalCollector(setupCtx, apiFactory)
		cancel()
		if cleanup != nil {
			defer cleanup()
		}
	}
	parallelMachines(ctx, len(machines), func(i int) {
		if opts.SerialOnly {
			for _, kind := range journalKinds {
				report.Machines[i].Journals[kind] = ArtifactResult{Status: StatusSkipped, Reason: "serial-only collection requested"}
			}
		} else {
			report.Machines[i].Journals = collectJournals(ctx, machines[i], artifactDir, api, apiErr, opts.SSH)
		}
	})
	var errs []error
	for _, machine := range report.Machines {
		logger.Info("Azure machine diagnostics", "machine", machine.Name, "serial", machine.Serial.Status, "reason", machine.Serial.Reason, "bootDiagnostics", machine.BootDiagnostics)
		for kind, result := range machine.Journals {
			logger.Info("Azure worker journal", "machine", machine.Name, "kind", kind, "status", result.Status, "reason", result.Reason)
			if result.Status == StatusFailed || result.Status == StatusTimedOut || result.Status == StatusEmpty {
				errs = append(errs, fmt.Errorf("AzureMachine %s %s: %s", machine.Name, kind, result.Reason))
			}
		}
		if machine.Serial.Status == StatusFailed || machine.Serial.Status == StatusTimedOut || machine.Serial.Status == StatusEmpty {
			errs = append(errs, fmt.Errorf("AzureMachine %s serial: %s", machine.Name, machine.Serial.Reason))
		}
	}
	errs = append(errs, writeReport(artifactDir, report))
	return report, utilerrors.NewAggregate(errs)
}

func parallelMachines(ctx context.Context, count int, collect func(int)) {
	var wg sync.WaitGroup
	semaphore := make(chan struct{}, maxConcurrentMachines)
	for i := 0; i < count; i++ {
		// Always record cancellation, even for work that never starts.
		if ctx.Err() != nil {
			collect(i)
			continue
		}
		select {
		case semaphore <- struct{}{}:
		case <-ctx.Done():
			collect(i)
			continue
		}
		wg.Add(1)
		go func(i int) { defer wg.Done(); defer func() { <-semaphore }(); collect(i) }(i)
	}
	wg.Wait()
}

func collectSerialConsoleLog(ctx context.Context, factory computeClientFactory, machine capiazure.AzureMachine, artifactDir string, httpClient *http.Client) (bootDiagnosticsState, ArtifactResult) {
	state := bootDiagnosticsSetting(machine)
	result := ArtifactResult{Status: StatusSkipped}
	path := filepath.Join(consoleLogsDirectory, machine.Name+".log")
	_ = os.Remove(filepath.Join(artifactDir, path))
	if ctx.Err() != nil {
		return state, failedResult(ctx.Err(), "serial collection canceled")
	}
	if machine.Spec.ProviderID == nil || strings.TrimSpace(*machine.Spec.ProviderID) == "" {
		result.Reason = "provider ID is missing"
		return state, result
	}
	sub, rg, vm, err := parseAzureVMResourceID(*machine.Spec.ProviderID)
	if err != nil {
		return state, failedResult(err, "provider ID does not identify an Azure VM")
	}
	if state == bootDisabled {
		result.Reason = "boot diagnostics are disabled"
		return state, result
	}
	vmClient, err := factory(sub)
	if err != nil {
		return state, failedResult(err, err.Error())
	}
	requestCtx, cancel := context.WithTimeout(ctx, consoleLogTimeout)
	defer cancel()
	if state == bootUnknown {
		state, err = vmClient.BootDiagnosticsState(requestCtx, rg, vm)
		if err != nil {
			return bootUnknown, failedResult(err, "failed to query VM boot diagnostics: "+safeAzureError(err).Error())
		}
		if state != bootEnabled {
			result.Reason = "VM boot diagnostics are " + string(state)
			return state, result
		}
	}
	uri, err := vmClient.RetrieveSerialConsoleLogURI(requestCtx, rg, vm)
	if err != nil {
		return state, failedResult(err, "failed to retrieve Azure boot diagnostics: "+safeAzureError(err).Error())
	}
	if uri == "" {
		result.Reason = "serial log URI is unavailable"
		return state, result
	}
	if httpClient == nil {
		httpClient = &http.Client{Timeout: consoleLogTimeout}
	}
	content, err := downloadSerialConsoleLog(requestCtx, httpClient, uri)
	if err != nil {
		return state, failedResult(err, err.Error())
	}
	if len(strings.TrimSpace(string(content))) == 0 {
		return state, ArtifactResult{Status: StatusEmpty, Reason: "serial log is empty"}
	}
	if err := os.WriteFile(filepath.Join(artifactDir, path), content, 0644); err != nil {
		return state, failedResult(err, "failed to write serial log")
	}
	return state, ArtifactResult{Status: StatusCollected, Path: path, Bytes: int64(len(content))}
}

func bootDiagnosticsSetting(machine capiazure.AzureMachine) bootDiagnosticsState {
	if machine.Spec.Diagnostics == nil || machine.Spec.Diagnostics.Boot == nil {
		return bootUnknown
	}
	switch machine.Spec.Diagnostics.Boot.StorageAccountType {
	case capiazure.DisabledDiagnosticsStorage:
		return bootDisabled
	case capiazure.ManagedDiagnosticsStorage, capiazure.UserManagedDiagnosticsStorage:
		return bootEnabled
	default:
		return bootUnknown
	}
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
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		if errors.Is(err, context.DeadlineExceeded) {
			return nil, context.DeadlineExceeded
		}
		return nil, errors.New("failed to download Azure serial console log")
	}
	defer response.Body.Close()
	if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
		return nil, fmt.Errorf("azure serial console log download returned HTTP status %d", response.StatusCode)
	}

	content, err := io.ReadAll(io.LimitReader(response.Body, maxSerialLogSize+1))
	if err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
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

func (c sdkComputeClient) RetrieveSerialConsoleLogURI(ctx context.Context, rg, vm string) (string, error) {
	response, err := c.virtualMachines.RetrieveBootDiagnosticsData(ctx, rg, vm, nil)
	if err != nil {
		return "", safeAzureError(err)
	}
	if response.SerialConsoleLogBlobURI == nil {
		return "", nil
	}
	return *response.SerialConsoleLogBlobURI, nil
}

func (c sdkComputeClient) BootDiagnosticsState(ctx context.Context, rg, vm string) (bootDiagnosticsState, error) {
	response, err := c.virtualMachines.Get(ctx, rg, vm, nil)
	if err != nil {
		return bootUnknown, safeAzureError(err)
	}
	if response.Properties == nil || response.Properties.DiagnosticsProfile == nil || response.Properties.DiagnosticsProfile.BootDiagnostics == nil || response.Properties.DiagnosticsProfile.BootDiagnostics.Enabled == nil {
		return bootUnknown, nil
	}
	if *response.Properties.DiagnosticsProfile.BootDiagnostics.Enabled {
		return bootEnabled, nil
	}
	return bootDisabled, nil
}

func safeAzureError(err error) error {
	if errors.Is(err, context.Canceled) {
		return context.Canceled
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return context.DeadlineExceeded
	}
	var safe *azureAPIError
	if errors.As(err, &safe) {
		return safe
	}
	var response *azcore.ResponseError
	if errors.As(err, &response) {
		return &azureAPIError{status: response.StatusCode}
	}
	return errors.New("azure API request failed")
}

type azureAPIError struct{ status int }

func (e *azureAPIError) Error() string {
	if e.status == http.StatusNotFound {
		return "Azure VM was not found (HTTP 404)"
	}
	return fmt.Sprintf("Azure API returned HTTP %d", e.status)
}
