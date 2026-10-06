package azure

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	"github.com/openshift/hypershift/pkg/manifests"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/compute/armcompute/v5"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"

	capiazure "sigs.k8s.io/cluster-api-provider-azure/api/v1beta1"
	clusterv1 "sigs.k8s.io/cluster-api/api/core/v1beta1"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	"github.com/go-logr/logr"
)

func TestDumpMachineDiagnostics(t *testing.T) {
	err := DumpMachineDiagnostics(context.Background(), nil, nil, "", t.TempDir(), logr.Discard())
	if err == nil || !strings.Contains(err.Error(), "credentials file is not configured") {
		t.Fatalf("expected missing credentials error, got %v", err)
	}
}

func TestListAzureMachines(t *testing.T) {
	scheme := runtime.NewScheme()
	if err := capiazure.AddToScheme(scheme); err != nil {
		t.Fatal(err)
	}
	controlPlaneNamespace := manifests.HostedControlPlaneNamespace("clusters", "hc")
	infraID := "hc-infra"
	wanted := &capiazure.AzureMachine{ObjectMeta: metav1.ObjectMeta{
		Name: "worker-0", Namespace: controlPlaneNamespace, Labels: map[string]string{clusterv1.ClusterNameLabel: infraID},
	}}
	unrelated := &capiazure.AzureMachine{ObjectMeta: metav1.ObjectMeta{
		Name: "other-0", Namespace: controlPlaneNamespace, Labels: map[string]string{clusterv1.ClusterNameLabel: "other"},
	}}
	wrongNamespace := &capiazure.AzureMachine{ObjectMeta: metav1.ObjectMeta{
		Name: "wrong-namespace", Namespace: "clusters", Labels: map[string]string{clusterv1.ClusterNameLabel: infraID},
	}}
	wrongClusterName := &capiazure.AzureMachine{ObjectMeta: metav1.ObjectMeta{
		Name: "wrong-cluster-name", Namespace: controlPlaneNamespace, Labels: map[string]string{clusterv1.ClusterNameLabel: "hc"},
	}}
	kubeClient := fake.NewClientBuilder().WithScheme(scheme).WithObjects(wanted, unrelated, wrongNamespace, wrongClusterName).Build()

	machines, err := listAzureMachines(context.Background(), kubeClient, &hyperv1.HostedCluster{
		ObjectMeta: metav1.ObjectMeta{Name: "hc", Namespace: "clusters"},
		Spec:       hyperv1.HostedClusterSpec{InfraID: infraID},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(machines) != 1 || machines[0].Name != wanted.Name {
		t.Fatalf("got AzureMachines %v, want only %q", machineNames(machines), wanted.Name)
	}
}

func TestCollectMachineDiagnostics(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("sig") != "sensitive-sas-token" {
			t.Errorf("serial log request did not include the SAS token")
		}
		_, _ = w.Write([]byte("boot console output"))
	}))
	defer server.Close()

	validProviderID := "azure:///subscriptions/sub-123/resourceGroups/worker-rg/providers/Microsoft.Compute/virtualMachines/worker-0"
	tests := []struct {
		name                string
		machine             capiazure.AzureMachine
		diagnosticsDisabled bool
		serialLogURI        string
		serialLogErr        error
		journal             string
		journalErr          error
		wantConsoleLog      string
		wantJournal         string
		wantError           string
		wantSerialLogCalls  int
		wantJournalCalls    int
		wantClientCreation  bool
	}{
		{
			name:               "When Azure boot diagnostics and Run Command succeed, it should write both artifacts",
			machine:            azureMachineWithProviderID("worker-0", validProviderID),
			serialLogURI:       server.URL + "/serial.log?sig=sensitive-sas-token",
			journal:            "worker journal output",
			wantConsoleLog:     "boot console output",
			wantJournal:        "worker journal output",
			wantSerialLogCalls: 1,
			wantJournalCalls:   1,
			wantClientCreation: true,
		},
		{
			name:                "When boot diagnostics are disabled, it should skip the serial log and still collect journals",
			machine:             azureMachineWithProviderID("worker-0", validProviderID),
			diagnosticsDisabled: true,
			serialLogURI:        "https://example.invalid/serial.log?sig=secret",
			journal:             "worker journal output",
			wantJournal:         "worker journal output",
			wantJournalCalls:    1,
			wantClientCreation:  true,
		},
		{
			name:               "When Azure returns no serial log URI, it should skip the console artifact",
			machine:            azureMachineWithProviderID("worker-0", validProviderID),
			journal:            "worker journal output",
			wantJournal:        "worker journal output",
			wantSerialLogCalls: 1,
			wantJournalCalls:   1,
			wantClientCreation: true,
		},
		{
			name:               "When boot diagnostics retrieval fails, it should report the error and still collect journals",
			machine:            azureMachineWithProviderID("worker-0", validProviderID),
			serialLogErr:       errors.New("boot diagnostics unavailable"),
			journal:            "worker journal output",
			wantJournal:        "worker journal output",
			wantError:          "failed to retrieve Azure boot diagnostics",
			wantSerialLogCalls: 1,
			wantJournalCalls:   1,
			wantClientCreation: true,
		},
		{
			name:               "When Run Command fails, it should report the journal error without blocking other diagnostics",
			machine:            azureMachineWithProviderID("worker-0", validProviderID),
			serialLogURI:       server.URL + "/serial.log?sig=sensitive-sas-token",
			journalErr:         errors.New("run command unavailable"),
			wantConsoleLog:     "boot console output",
			wantError:          "failed to retrieve worker journal",
			wantSerialLogCalls: 1,
			wantJournalCalls:   1,
			wantClientCreation: true,
		},
		{
			name:               "When an AzureMachine has no provider ID, it should skip that machine",
			machine:            capiazure.AzureMachine{ObjectMeta: metav1.ObjectMeta{Name: "worker-0"}},
			wantClientCreation: false,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if test.diagnosticsDisabled {
				test.machine.Spec.Diagnostics = &capiazure.Diagnostics{Boot: &capiazure.BootDiagnostics{StorageAccountType: capiazure.DisabledDiagnosticsStorage}}
			}
			vmClient := &fakeComputeClient{
				serialLogURI: test.serialLogURI,
				serialLogErr: test.serialLogErr,
				journal:      test.journal,
				journalErr:   test.journalErr,
			}
			factoryCalled := false
			artifactDir := t.TempDir()
			err := collectMachineDiagnostics(context.Background(), []capiazure.AzureMachine{test.machine}, artifactDir, func(subscriptionID string) (computeClient, error) {
				factoryCalled = true
				if subscriptionID != "sub-123" {
					t.Fatalf("got subscription %q, want sub-123", subscriptionID)
				}
				return vmClient, nil
			}, logr.Discard())
			if test.wantError == "" {
				if err != nil {
					t.Fatalf("unexpected error: %v", err)
				}
			} else if err == nil || !strings.Contains(err.Error(), test.wantError) {
				t.Fatalf("expected error containing %q, got %v", test.wantError, err)
			}
			if factoryCalled != test.wantClientCreation {
				t.Fatalf("client factory called = %v, want %v", factoryCalled, test.wantClientCreation)
			}
			if vmClient.serialLogCalls != test.wantSerialLogCalls || vmClient.journalCalls != test.wantJournalCalls {
				t.Fatalf("got serial calls=%d and journal calls=%d, want %d and %d", vmClient.serialLogCalls, vmClient.journalCalls, test.wantSerialLogCalls, test.wantJournalCalls)
			}
			assertOptionalFileContent(t, filepath.Join(artifactDir, consoleLogsDirectory, "worker-0.log"), test.wantConsoleLog)
			assertOptionalFileContent(t, filepath.Join(artifactDir, journalsDirectory, "worker-0.log"), test.wantJournal)
		})
	}
}

func TestParseAzureVMResourceID(t *testing.T) {
	tests := []struct {
		name           string
		providerID     string
		subscriptionID string
		resourceGroup  string
		vmName         string
		wantError      bool
	}{
		{
			name:           "When provider ID is a CAPZ VM resource ID, it should parse subscription, resource group, and VM name",
			providerID:     "azure:///subscriptions/sub-123/resourceGroups/worker-rg/providers/Microsoft.Compute/virtualMachines/worker-0",
			subscriptionID: "sub-123",
			resourceGroup:  "worker-rg",
			vmName:         "worker-0",
		},
		{
			name:           "When resource group contains escaped characters, it should decode the resource group",
			providerID:     "azure:///subscriptions/sub-123/resourceGroups/worker%20rg/providers/Microsoft.Compute/virtualMachines/worker-0",
			subscriptionID: "sub-123",
			resourceGroup:  "worker rg",
			vmName:         "worker-0",
		},
		{
			name:       "When provider ID has a non-Azure scheme, it should return an error",
			providerID: "aws:///subscriptions/sub-123/resourceGroups/worker-rg/providers/Microsoft.Compute/virtualMachines/worker-0",
			wantError:  true,
		},
		{
			name:       "When provider ID targets a scale set VM, it should reject the resource ID",
			providerID: "azure:///subscriptions/sub-123/resourceGroups/worker-rg/providers/Microsoft.Compute/virtualMachineScaleSets/workers/virtualMachines/0",
			wantError:  true,
		},
		{
			name:       "When provider ID omits the VM name, it should return an error",
			providerID: "azure:///subscriptions/sub-123/resourceGroups/worker-rg/providers/Microsoft.Compute/virtualMachines",
			wantError:  true,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			subscriptionID, resourceGroup, vmName, err := parseAzureVMResourceID(test.providerID)
			if (err != nil) != test.wantError {
				t.Fatalf("expected error=%v, got %v", test.wantError, err)
			}
			if err != nil {
				return
			}
			if subscriptionID != test.subscriptionID || resourceGroup != test.resourceGroup || vmName != test.vmName {
				t.Fatalf("got (%q, %q, %q), want (%q, %q, %q)", subscriptionID, resourceGroup, vmName, test.subscriptionID, test.resourceGroup, test.vmName)
			}
		})
	}
}

func TestBootDiagnosticsDisabled(t *testing.T) {
	tests := []struct {
		name     string
		machine  *capiazure.AzureMachine
		disabled bool
	}{
		{name: "When diagnostics are absent, it should treat boot diagnostics as enabled", machine: &capiazure.AzureMachine{}},
		{name: "When boot diagnostics are absent, it should treat boot diagnostics as enabled", machine: &capiazure.AzureMachine{Spec: capiazure.AzureMachineSpec{Diagnostics: &capiazure.Diagnostics{}}}},
		{
			name: "When boot diagnostics are explicitly disabled, it should skip serial log collection",
			machine: &capiazure.AzureMachine{Spec: capiazure.AzureMachineSpec{Diagnostics: &capiazure.Diagnostics{
				Boot: &capiazure.BootDiagnostics{StorageAccountType: capiazure.DisabledDiagnosticsStorage},
			}}},
			disabled: true,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := bootDiagnosticsDisabled(test.machine); got != test.disabled {
				t.Fatalf("bootDiagnosticsDisabled() = %v, want %v", got, test.disabled)
			}
		})
	}
}

func TestDownloadSerialConsoleLog(t *testing.T) {
	tests := []struct {
		name          string
		statusCode    int
		uri           string
		wantContent   string
		wantError     string
		wantErrorOmit string
	}{
		{
			name:        "When the SAS URL returns a successful response, it should return the log body",
			statusCode:  http.StatusOK,
			uri:         "/serial.log?sig=sensitive-sas-token",
			wantContent: "boot console output",
		},
		{
			name:          "When the SAS URL is rejected, it should return an error without exposing the token",
			statusCode:    http.StatusForbidden,
			uri:           "/serial.log?sig=sensitive-sas-token",
			wantError:     "HTTP status 403",
			wantErrorOmit: "sensitive-sas-token",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(test.statusCode)
				_, _ = w.Write([]byte("boot console output"))
			}))
			defer server.Close()

			content, err := downloadSerialConsoleLog(context.Background(), server.Client(), server.URL+test.uri)
			if test.wantError == "" {
				if err != nil {
					t.Fatalf("unexpected error: %v", err)
				}
				if string(content) != test.wantContent {
					t.Fatalf("got content %q, want %q", content, test.wantContent)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), test.wantError) {
				t.Fatalf("expected error containing %q, got %v", test.wantError, err)
			}
			if strings.Contains(err.Error(), test.wantErrorOmit) || strings.Contains(err.Error(), "sig=") {
				t.Fatalf("download error exposed the SAS URL: %v", err)
			}
		})
	}
}

func TestSDKComputeClientRetrieveSerialConsoleLogURI(t *testing.T) {
	serialLogURI := "https://storage.example/serial.log?sig=secret"
	transport := azureTransportFunc(func(request *http.Request) (*http.Response, error) {
		if request.Method != http.MethodPost || !strings.HasSuffix(request.URL.Path, "/retrieveBootDiagnosticsData") {
			t.Fatalf("got request %s %s, want POST retrieveBootDiagnosticsData", request.Method, request.URL.Path)
		}
		return azureResponse(request, http.StatusOK, `{"serialConsoleLogBlobUri":"`+serialLogURI+`"}`), nil
	})
	vmClient := newSDKComputeClient(t, transport)

	got, err := (sdkComputeClient{virtualMachines: vmClient}).RetrieveSerialConsoleLogURI(context.Background(), "worker-rg", "worker-0")
	if err != nil {
		t.Fatal(err)
	}
	if got != serialLogURI {
		t.Fatalf("RetrieveSerialConsoleLogURI() = %q, want %q", got, serialLogURI)
	}
}

func TestSDKComputeClientRunJournalCommand(t *testing.T) {
	transport := azureTransportFunc(func(request *http.Request) (*http.Response, error) {
		if request.Method != http.MethodPost || !strings.HasSuffix(request.URL.Path, "/runCommand") {
			t.Fatalf("got request %s %s, want POST runCommand", request.Method, request.URL.Path)
		}
		var input armcompute.RunCommandInput
		if err := json.NewDecoder(request.Body).Decode(&input); err != nil {
			t.Fatalf("failed to decode Run Command input: %v", err)
		}
		if input.CommandID == nil || *input.CommandID != "RunShellScript" {
			t.Fatalf("Run Command ID = %v, want RunShellScript", input.CommandID)
		}
		if len(input.Script) != 1 || input.Script[0] == nil || *input.Script[0] != journalCommand {
			t.Fatalf("Run Command script = %v, want %q", input.Script, journalCommand)
		}
		return azureResponse(request, http.StatusOK, `{"value":[
			{"code":"ComponentStatus/StdOut/succeeded","message":"worker journal output"},
			{"code":"ComponentStatus/StdErr/succeeded","message":"journal warning"},
			{"code":"ProvisioningState/succeeded","message":"ignore this status"}
		]}`), nil
	})
	vmClient := newSDKComputeClient(t, transport)

	got, err := (sdkComputeClient{virtualMachines: vmClient}).RunJournalCommand(context.Background(), "worker-rg", "worker-0")
	if err != nil {
		t.Fatal(err)
	}
	if want := "worker journal output\njournal warning"; got != want {
		t.Fatalf("RunJournalCommand() = %q, want %q", got, want)
	}
}

type azureTransportFunc func(*http.Request) (*http.Response, error)

func (f azureTransportFunc) Do(request *http.Request) (*http.Response, error) {
	return f(request)
}

type staticAzureCredential struct{}

func (staticAzureCredential) GetToken(context.Context, policy.TokenRequestOptions) (azcore.AccessToken, error) {
	return azcore.AccessToken{Token: "test-token", ExpiresOn: time.Now().Add(time.Hour)}, nil
}

func newSDKComputeClient(t *testing.T, transport azureTransportFunc) *armcompute.VirtualMachinesClient {
	t.Helper()
	client, err := armcompute.NewVirtualMachinesClient("sub-123", staticAzureCredential{}, &arm.ClientOptions{
		ClientOptions: policy.ClientOptions{Transport: transport},
	})
	if err != nil {
		t.Fatal(err)
	}
	return client
}

func azureResponse(request *http.Request, statusCode int, body string) *http.Response {
	return &http.Response{
		StatusCode: statusCode,
		Header:     http.Header{"Content-Type": []string{"application/json"}},
		Body:       io.NopCloser(strings.NewReader(body)),
		Request:    request,
	}
}

type fakeComputeClient struct {
	serialLogURI   string
	serialLogErr   error
	journal        string
	journalErr     error
	serialLogCalls int
	journalCalls   int
}

func (c *fakeComputeClient) RetrieveSerialConsoleLogURI(context.Context, string, string) (string, error) {
	c.serialLogCalls++
	return c.serialLogURI, c.serialLogErr
}

func (c *fakeComputeClient) RunJournalCommand(context.Context, string, string) (string, error) {
	c.journalCalls++
	return c.journal, c.journalErr
}

func azureMachineWithProviderID(name, providerID string) capiazure.AzureMachine {
	return capiazure.AzureMachine{
		ObjectMeta: metav1.ObjectMeta{Name: name},
		Spec:       capiazure.AzureMachineSpec{ProviderID: &providerID},
	}
}

func assertOptionalFileContent(t *testing.T, filename, expected string) {
	t.Helper()
	content, err := os.ReadFile(filename)
	if expected == "" {
		if !os.IsNotExist(err) {
			t.Fatalf("expected %s not to exist, read error: %v", filename, err)
		}
		return
	}
	if err != nil {
		t.Fatal(err)
	}
	if string(content) != expected {
		t.Fatalf("%s contained %q, want %q", filename, string(content), expected)
	}
}

func machineNames(machines []capiazure.AzureMachine) []string {
	names := make([]string, 0, len(machines))
	for _, machine := range machines {
		names = append(names, machine.Name)
	}
	return names
}
