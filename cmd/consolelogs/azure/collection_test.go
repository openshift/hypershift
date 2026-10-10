package azure

import (
	"context"
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"

	capiazure "sigs.k8s.io/cluster-api-provider-azure/api/v1beta1"

	"github.com/go-logr/logr"
)

const testProviderID = "azure:///subscriptions/sub-123/resourceGroups/worker-rg/providers/Microsoft.Compute/virtualMachines/worker-0"

type fakeComputeClient struct {
	uri         string
	serialErr   error
	state       bootDiagnosticsState
	stateErr    error
	stateCalls  atomic.Int32
	serialCalls atomic.Int32
}

func (c *fakeComputeClient) RetrieveSerialConsoleLogURI(context.Context, string, string) (string, error) {
	c.serialCalls.Add(1)
	return c.uri, c.serialErr
}
func (c *fakeComputeClient) BootDiagnosticsState(context.Context, string, string) (bootDiagnosticsState, error) {
	c.stateCalls.Add(1)
	return c.state, c.stateErr
}

func TestBootDiagnosticsSetting(t *testing.T) {
	for _, tt := range []struct {
		name    string
		setting capiazure.BootDiagnosticsStorageAccountType
		want    bootDiagnosticsState
	}{
		{"When diagnostics are absent, it should report unknown", "", bootUnknown},
		{"When diagnostics are managed, it should report enabled", capiazure.ManagedDiagnosticsStorage, bootEnabled},
		{"When diagnostics are user managed, it should report enabled", capiazure.UserManagedDiagnosticsStorage, bootEnabled},
		{"When diagnostics are disabled, it should report disabled", capiazure.DisabledDiagnosticsStorage, bootDisabled},
	} {
		t.Run(tt.name, func(t *testing.T) {
			machine := capiazure.AzureMachine{}
			if tt.setting != "" {
				machine.Spec.Diagnostics = &capiazure.Diagnostics{Boot: &capiazure.BootDiagnostics{StorageAccountType: tt.setting}}
			}
			if got := bootDiagnosticsSetting(machine); got != tt.want {
				t.Fatalf("got %s, want %s", got, tt.want)
			}
		})
	}
}

func TestCollectSerialConsoleLog(t *testing.T) {
	for _, tt := range []struct {
		name                                    string
		state                                   bootDiagnosticsState
		stateErr, serialErr                     error
		content                                 string
		noURI, noID, configuredDisabled, cancel bool
		want                                    string
	}{
		{name: "When VM diagnostics are enabled, it should collect a serial artifact without a Node", state: bootEnabled, content: "boot console output", want: StatusCollected},
		{name: "When unknown diagnostics resolve to disabled, it should report the disabled VM", state: bootDisabled, want: StatusSkipped},
		{name: "When diagnostics remain unknown, it should report unavailable diagnostics", state: bootUnknown, want: StatusSkipped},
		{name: "When diagnostics lookup fails, it should report failure", stateErr: errors.New("lookup failed"), want: StatusFailed},
		{name: "When serial retrieval fails, it should report failure", state: bootEnabled, serialErr: errors.New("secret SAS URI"), want: StatusFailed},
		{name: "When the URI is missing, it should report unavailable output", state: bootEnabled, noURI: true, want: StatusSkipped},
		{name: "When output is empty, it should not create an artifact", state: bootEnabled, want: StatusEmpty},
		{name: "When provider ID is missing, it should visibly skip the machine", noID: true, want: StatusSkipped},
		{name: "When configured diagnostics are disabled, it should not call Azure", configuredDisabled: true, want: StatusSkipped},
		{name: "When collection is canceled, it should record the cancellation", cancel: true, want: StatusTimedOut},
	} {
		t.Run(tt.name, func(t *testing.T) {
			vm := &fakeComputeClient{uri: "https://storage.example/serial.log?sig=secret", state: tt.state, stateErr: tt.stateErr, serialErr: tt.serialErr}
			if tt.noURI {
				vm.uri = ""
			}
			machine := azureMachineWithProviderID("worker-0", testProviderID)
			if tt.noID {
				machine.Spec.ProviderID = nil
			}
			if tt.configuredDisabled {
				machine.Spec.Diagnostics = &capiazure.Diagnostics{Boot: &capiazure.BootDiagnostics{StorageAccountType: capiazure.DisabledDiagnosticsStorage}}
			}
			dir := t.TempDir()
			if err := os.MkdirAll(filepath.Join(dir, consoleLogsDirectory), 0755); err != nil {
				t.Fatal(err)
			}
			// A previous file must never mask unsuccessful collection.
			path := filepath.Join(dir, consoleLogsDirectory, machine.Name+".log")
			if err := os.WriteFile(path, []byte("stale output"), 0644); err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			if tt.cancel {
				cancel()
			}
			_, result := collectSerialConsoleLog(ctx, func(string) (computeClient, error) { return vm, nil }, machine, dir, serialHTTPClient(tt.content, 200))
			if result.Status != tt.want {
				t.Fatalf("got %+v, want %s", result, tt.want)
			}
			if strings.Contains(result.Reason, "secret") {
				t.Fatalf("secret exposed: %+v", result)
			}
			if tt.want == StatusCollected {
				assertOptionalFileContent(t, path, tt.content)
			} else if _, err := os.Stat(path); !os.IsNotExist(err) {
				t.Fatalf("unsuccessful collection left an artifact: %v", err)
			}
			if tt.configuredDisabled && (vm.stateCalls.Load() != 0 || vm.serialCalls.Load() != 0) {
				t.Fatal("disabled diagnostics called Azure")
			}
		})
	}
}

func TestCollectMachineDiagnostics(t *testing.T) {
	t.Run("When serial-only collection is requested, it should avoid API setup and journal transport", func(t *testing.T) {
		vm := &fakeComputeClient{uri: "https://storage.example/serial.log", state: bootEnabled}
		dir := t.TempDir()
		report, err := collectMachineDiagnostics(t.Context(), []capiazure.AzureMachine{azureMachineWithProviderID("worker-0", testProviderID)}, DiagnosticsOptions{ArtifactDir: dir, SerialOnly: true, serialHTTPClient: serialHTTPClient("serial output", 200)}, func(string) (computeClient, error) { return vm, nil }, func(context.Context) (JournalCollector, func(), error) {
			t.Error("serial-only collection initialized guest access")
			return nil, nil, errors.New("unexpected API setup")
		}, logr.Discard())
		if err != nil {
			t.Fatal(err)
		}
		if err := VerifyArtifacts(dir, report, []string{"worker-0"}, false); err != nil {
			t.Fatal(err)
		}
		for _, journal := range report.Machines[0].Journals {
			if journal.Status != StatusSkipped {
				t.Fatalf("unexpected journal outcome: %+v", journal)
			}
		}
	})
	t.Run("When discovery is empty, it should write a summary without a vacuous validation pass", func(t *testing.T) {
		dir := t.TempDir()
		report, err := collectMachineDiagnostics(t.Context(), nil, DiagnosticsOptions{ArtifactDir: dir}, nil, nil, logr.Discard())
		if err != nil {
			t.Fatal(err)
		}
		if _, err := os.Stat(filepath.Join(dir, "machine-diagnostics.json")); err != nil {
			t.Fatal(err)
		}
		if VerifyArtifacts(dir, report, nil, true) == nil {
			t.Fatal("empty machine discovery passed validation")
		}
	})
	t.Run("When serial collection finishes, it should independently attempt journals and save a summary", func(t *testing.T) {
		vm := &fakeComputeClient{uri: "https://storage.example/serial.log", state: bootEnabled}
		machines := []capiazure.AzureMachine{azureMachineWithProviderID("worker-0", testProviderID), azureMachineWithProviderID("worker-1", testProviderID)}
		apiFactory := func(context.Context) (JournalCollector, func(), error) {
			if vm.serialCalls.Load() != 2 {
				t.Errorf("journals started before all serial requests")
			}
			return func(_ context.Context, _ capiazure.AzureMachine, _ string, out io.Writer) error {
				_, err := io.WriteString(out, "worker journal output")
				return err
			}, func() {}, nil
		}
		dir := t.TempDir()
		report, err := collectMachineDiagnostics(t.Context(), machines, DiagnosticsOptions{ArtifactDir: dir, serialHTTPClient: serialHTTPClient("serial boot output", 200)}, func(string) (computeClient, error) { return vm, nil }, apiFactory, logr.Discard())
		if err != nil {
			t.Fatal(err)
		}
		if err := VerifyArtifacts(dir, report, []string{"worker-0", "worker-1"}, true); err != nil {
			t.Fatal(err)
		}
		if _, err := os.Stat(filepath.Join(dir, "machine-diagnostics.json")); err != nil {
			t.Fatal(err)
		}
	})
	t.Run("When Azure credentials are unavailable, it should still collect journals", func(t *testing.T) {
		apiFactory := func(context.Context) (JournalCollector, func(), error) {
			return func(_ context.Context, _ capiazure.AzureMachine, _ string, out io.Writer) error {
				_, err := io.WriteString(out, "journal output")
				return err
			}, nil, nil
		}
		report, err := collectMachineDiagnostics(t.Context(), []capiazure.AzureMachine{azureMachineWithProviderID("worker-0", testProviderID)}, DiagnosticsOptions{ArtifactDir: t.TempDir()}, func(string) (computeClient, error) {
			return nil, errors.New("Azure credentials file is not configured")
		}, apiFactory, logr.Discard())
		if err == nil || report.Machines[0].Serial.Status != StatusFailed {
			t.Fatal("missing serial credentials were hidden")
		}
		for _, result := range report.Machines[0].Journals {
			if result.Status != StatusCollected {
				t.Fatalf("journals were suppressed: %+v", result)
			}
		}
	})
	t.Run("When journal access fails, it should preserve serial artifacts", func(t *testing.T) {
		vm := &fakeComputeClient{uri: "https://storage.example/serial.log", state: bootEnabled}
		dir := t.TempDir()
		report, err := collectMachineDiagnostics(t.Context(), []capiazure.AzureMachine{azureMachineWithProviderID("worker-0", testProviderID)}, DiagnosticsOptions{ArtifactDir: dir, serialHTTPClient: serialHTTPClient("serial output", 200)}, func(string) (computeClient, error) { return vm, nil }, func(context.Context) (JournalCollector, func(), error) {
			return nil, nil, errors.New("hosted API unavailable")
		}, logr.Discard())
		if err == nil {
			t.Fatal("expected journal access error")
		}
		if err := VerifyArtifacts(dir, report, []string{"worker-0"}, false); err != nil {
			t.Fatal(err)
		}
	})
}

func TestSDKComputeClientBootDiagnosticsState(t *testing.T) {
	for _, tt := range []struct {
		name, body string
		want       bootDiagnosticsState
	}{
		{"When the VM enables diagnostics, it should report enabled", `{"properties":{"diagnosticsProfile":{"bootDiagnostics":{"enabled":true}}}}`, bootEnabled},
		{"When the VM disables diagnostics, it should report disabled", `{"properties":{"diagnosticsProfile":{"bootDiagnostics":{"enabled":false}}}}`, bootDisabled},
		{"When the VM omits diagnostics, it should report unknown", `{"properties":{}}`, bootUnknown},
	} {
		t.Run(tt.name, func(t *testing.T) {
			vm := newSDKComputeClient(t, azureTransportFunc(func(r *http.Request) (*http.Response, error) {
				if r.Method != http.MethodGet {
					t.Errorf("expected VM read")
				}
				return azureResponse(r, 200, tt.body), nil
			}))
			state, err := (sdkComputeClient{virtualMachines: vm}).BootDiagnosticsState(t.Context(), "worker-rg", "worker-0")
			if err != nil || state != tt.want {
				t.Fatalf("got %s %v", state, err)
			}
		})
	}
}

func TestNewComputeClientFactory(t *testing.T) {
	t.Run("When credentials are not configured, it should return an explicit error", func(t *testing.T) {
		_, err := newComputeClientFactory(&hyperv1.HostedCluster{}, "")
		if err == nil {
			t.Fatal("expected credentials configuration error")
		}
	})
}

func TestParallelMachines(t *testing.T) {
	t.Run("When many machines need collection, it should limit concurrent work to four", func(t *testing.T) {
		var active, peak atomic.Int32
		var started sync.WaitGroup
		started.Add(maxConcurrentMachines)
		release := make(chan struct{})
		done := make(chan struct{})
		go func() {
			parallelMachines(t.Context(), 12, func(i int) {
				count := active.Add(1)
				for previous := peak.Load(); count > previous; previous = peak.Load() {
					if peak.CompareAndSwap(previous, count) {
						break
					}
				}
				if i < maxConcurrentMachines {
					started.Done()
				}
				<-release
				active.Add(-1)
			})
			close(done)
		}()
		started.Wait()
		close(release)
		<-done
		if peak.Load() != maxConcurrentMachines {
			t.Fatalf("got %d concurrent machines, want %d", peak.Load(), maxConcurrentMachines)
		}
	})
}
