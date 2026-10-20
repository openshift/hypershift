package azure

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
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
)

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
			content, err := downloadSerialConsoleLog(context.Background(), serialHTTPClient("boot console output", test.statusCode), "https://storage.example"+test.uri)
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
	t.Run("When a download blocks until cancellation, it should preserve timeout status and redact the SAS URL", func(t *testing.T) {
		ctx, cancel := context.WithTimeout(t.Context(), 20*time.Millisecond)
		defer cancel()
		httpClient := &http.Client{Transport: serialRoundTripper(func(r *http.Request) (*http.Response, error) {
			<-r.Context().Done()
			return nil, fmt.Errorf("download %s: %w", r.URL, r.Context().Err())
		})}
		_, err := downloadSerialConsoleLog(ctx, httpClient, "https://storage.example/log?sig=sensitive-token")
		if !errors.Is(err, context.DeadlineExceeded) || strings.Contains(err.Error(), "sensitive-token") {
			t.Fatalf("unexpected or unsafe error: %v", err)
		}
	})
	t.Run("When the downloaded body is oversized, it should reject the content", func(t *testing.T) {
		_, err := downloadSerialConsoleLog(t.Context(), serialHTTPClient(strings.Repeat("x", maxSerialLogSize+1), 200), "https://storage.example/log?sig=sensitive-token")
		if err == nil || strings.Contains(err.Error(), "sensitive-token") {
			t.Fatalf("unexpected or unsafe error: %v", err)
		}
	})
}

func TestSafeAzureError(t *testing.T) {
	for _, tt := range []struct {
		name string
		err  error
		want string
	}{
		{"When Azure reports a deleted VM, it should identify the missing VM", &azcore.ResponseError{StatusCode: 404, ErrorCode: "secret"}, "Azure VM was not found (HTTP 404)"},
		{"When Azure denies access, it should preserve the HTTP status", &azcore.ResponseError{StatusCode: 403, ErrorCode: "secret"}, "Azure API returned HTTP 403"},
		{"When a wrapped timeout includes a credential URL, it should retain only the timeout", fmt.Errorf("secret URL: %w", context.DeadlineExceeded), context.DeadlineExceeded.Error()},
		{"When a transport error includes credentials, it should redact them", errors.New("secret transport error"), "azure API request failed"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			// The SDK and caller both sanitize errors; status details must survive.
			got := safeAzureError(safeAzureError(tt.err))
			if got.Error() != tt.want || strings.Contains(got.Error(), "secret") {
				t.Fatalf("got %v, want %s", got, tt.want)
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

type serialRoundTripper func(*http.Request) (*http.Response, error)

func (f serialRoundTripper) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }
func serialHTTPClient(content string, status int) *http.Client {
	return &http.Client{Transport: serialRoundTripper(func(r *http.Request) (*http.Response, error) { return azureResponse(r, status, content), nil })}
}
