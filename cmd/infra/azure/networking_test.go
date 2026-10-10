package azure

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	. "github.com/onsi/gomega"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
	"github.com/Azure/azure-sdk-for-go/sdk/azidentity"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/network/armnetwork/v5"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/privatedns/armprivatedns"

	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/utils/ptr"
)

func TestNewVirtualNetwork(t *testing.T) {
	tests := map[string]struct {
		location       string
		vnetAddrPrefix string
	}{
		"When location is eastus it should create virtual network with correct configuration": {
			location:       "eastus",
			vnetAddrPrefix: "10.0.0.0/16",
		},
		"When location is westus2 it should create virtual network with correct configuration": {
			location:       "westus2",
			vnetAddrPrefix: "192.168.0.0/16",
		},
	}

	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			g := NewGomegaWithT(t)

			vnet := NewVirtualNetwork(test.location, test.vnetAddrPrefix)

			g.Expect(vnet.Location).ToNot(BeNil())
			g.Expect(*vnet.Location).To(Equal(test.location))
			g.Expect(vnet.Properties).ToNot(BeNil())
			g.Expect(vnet.Properties.AddressSpace).ToNot(BeNil())
			g.Expect(vnet.Properties.AddressSpace.AddressPrefixes).To(HaveLen(1))
			g.Expect(*vnet.Properties.AddressSpace.AddressPrefixes[0]).To(Equal(test.vnetAddrPrefix))
			g.Expect(vnet.Properties.Subnets).ToNot(BeNil())
			g.Expect(vnet.Properties.Subnets).To(BeEmpty())
		})
	}
}

func TestNewVirtualNetworkLink(t *testing.T) {
	tests := map[string]struct {
		location            string
		vnetID              string
		registrationEnabled bool
	}{
		"When registration is enabled it should create link with registration enabled": {
			location:            "global",
			vnetID:              "/subscriptions/sub123/resourceGroups/rg/providers/Microsoft.Network/virtualNetworks/vnet1",
			registrationEnabled: true,
		},
		"When registration is disabled it should create link with registration disabled": {
			location:            "global",
			vnetID:              "/subscriptions/sub456/resourceGroups/rg2/providers/Microsoft.Network/virtualNetworks/vnet2",
			registrationEnabled: false,
		},
	}

	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			g := NewGomegaWithT(t)

			link := NewVirtualNetworkLink(test.location, test.vnetID, test.registrationEnabled)

			g.Expect(link.Location).ToNot(BeNil())
			g.Expect(*link.Location).To(Equal(test.location))
			g.Expect(link.Properties).ToNot(BeNil())
			g.Expect(link.Properties.VirtualNetwork).ToNot(BeNil())
			g.Expect(*link.Properties.VirtualNetwork.ID).To(Equal(test.vnetID))
			g.Expect(link.Properties.RegistrationEnabled).ToNot(BeNil())
			g.Expect(*link.Properties.RegistrationEnabled).To(Equal(test.registrationEnabled))
		})
	}
}

type testSanitizedCauseError struct {
	marker string
}

func (e *testSanitizedCauseError) Error() string {
	return e.marker
}

func TestSanitizeVirtualNetworkLinkCause(t *testing.T) {
	tests := []struct {
		name             string
		cause            error
		isTarget         error
		expectedCategory string
		asTarget         func() any
	}{
		{
			name:             "When the cause is a deadline, it should use a fixed deadline category",
			cause:            fmt.Errorf("wrapped: %w", context.DeadlineExceeded),
			isTarget:         context.DeadlineExceeded,
			expectedCategory: "provider/deadline-exceeded",
		},
		{
			name:             "When the cause is cancellation, it should use a fixed cancellation category",
			cause:            fmt.Errorf("wrapped: %w", context.Canceled),
			isTarget:         context.Canceled,
			expectedCategory: "provider/canceled",
		},
		{
			name:             "When the cause is an authentication failure, it should use a fixed authentication category",
			cause:            fmt.Errorf("wrapped: %w", &azidentity.AuthenticationFailedError{}),
			expectedCategory: "provider/authentication",
			asTarget: func() any {
				var target *azidentity.AuthenticationFailedError
				return &target
			},
		},
		{
			name: "When the cause is a transport failure, it should use a fixed transport category",
			cause: fmt.Errorf("wrapped: %w", &net.OpError{
				Op:  "dial",
				Net: "tcp",
				Err: &testSanitizedCauseError{marker: "SENSITIVE_TRANSPORT_DETAIL"},
			}),
			expectedCategory: "provider/transport",
			asTarget: func() any {
				var target *net.OpError
				return &target
			},
		},
		{
			name:             "When the cause is unrecognized, it should use a fixed unknown category",
			cause:            fmt.Errorf("wrapped: %w", &testSanitizedCauseError{marker: "SENSITIVE_UNKNOWN_DETAIL"}),
			expectedCategory: "provider/unknown",
			asTarget: func() any {
				var target *testSanitizedCauseError
				return &target
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			g := NewGomegaWithT(t)
			err := sanitizeVirtualNetworkLinkCause("poll", "provider", test.cause)

			g.Expect(err).To(HaveOccurred())
			g.Expect(err.Error()).To(Equal(fmt.Sprintf("virtual network link provider error phase=poll category=%s", test.expectedCategory)))
			g.Expect(errors.Is(err, test.cause)).To(BeTrue())
			if test.isTarget != nil {
				g.Expect(errors.Is(err, test.isTarget)).To(BeTrue())
			}
			g.Expect(err.Error()).ToNot(ContainSubstring("SENSITIVE_"))
			if test.asTarget != nil {
				g.Expect(errors.As(err, test.asTarget())).To(BeTrue())
			}
		})
	}
}

func TestNewPublicIPAddress(t *testing.T) {
	tests := map[string]struct {
		name     string
		location string
	}{
		"When location is eastus it should create public IP with standard configuration": {
			name:     "test-infra-id",
			location: "eastus",
		},
		"When location is northeurope it should create public IP with correct region": {
			name:     "my-cluster-ip",
			location: "northeurope",
		},
	}

	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			g := NewGomegaWithT(t)

			ip := NewPublicIPAddress(test.name, test.location)

			g.Expect(ip.Name).ToNot(BeNil())
			g.Expect(*ip.Name).To(Equal(test.name))
			g.Expect(ip.Location).ToNot(BeNil())
			g.Expect(*ip.Location).To(Equal(test.location))
			g.Expect(ip.Properties).ToNot(BeNil())
			g.Expect(*ip.Properties.PublicIPAddressVersion).To(Equal(armnetwork.IPVersionIPv4))
			g.Expect(*ip.Properties.PublicIPAllocationMethod).To(Equal(armnetwork.IPAllocationMethodStatic))
			g.Expect(*ip.Properties.IdleTimeoutInMinutes).To(Equal(int32(4)))
			g.Expect(ip.SKU).ToNot(BeNil())
			g.Expect(*ip.SKU.Name).To(Equal(armnetwork.PublicIPAddressSKUNameStandard))
		})
	}
}

func TestNewLoadBalancer(t *testing.T) {
	tests := map[string]struct {
		location         string
		infraID          string
		idPrefix         string
		loadBalancerName string
	}{
		"When standard configuration is provided it should create load balancer with correct settings": {
			location:         "eastus",
			infraID:          "test-infra-id",
			idPrefix:         "subscriptions/sub123/resourceGroups/test-rg/providers/Microsoft.Network/loadBalancers",
			loadBalancerName: "test-infra-id",
		},
	}

	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			g := NewGomegaWithT(t)

			publicIP := &armnetwork.PublicIPAddress{
				ID: stringPtr("/subscriptions/sub123/resourceGroups/test-rg/providers/Microsoft.Network/publicIPAddresses/test-ip"),
			}

			lb := NewLoadBalancer(test.location, test.infraID, test.idPrefix, test.loadBalancerName, publicIP)

			g.Expect(lb.Location).ToNot(BeNil())
			g.Expect(*lb.Location).To(Equal(test.location))
			g.Expect(lb.SKU).ToNot(BeNil())
			g.Expect(*lb.SKU.Name).To(Equal(armnetwork.LoadBalancerSKUNameStandard))
			g.Expect(lb.Properties).ToNot(BeNil())

			// Check frontend IP configuration
			g.Expect(lb.Properties.FrontendIPConfigurations).To(HaveLen(1))
			g.Expect(*lb.Properties.FrontendIPConfigurations[0].Name).To(Equal(test.infraID))

			// Check backend address pool
			g.Expect(lb.Properties.BackendAddressPools).To(HaveLen(1))
			g.Expect(*lb.Properties.BackendAddressPools[0].Name).To(Equal(test.infraID))

			// Check health probe
			g.Expect(lb.Properties.Probes).To(HaveLen(1))
			g.Expect(*lb.Properties.Probes[0].Name).To(Equal(test.infraID))
			g.Expect(*lb.Properties.Probes[0].Properties.Port).To(Equal(int32(30595)))
			g.Expect(*lb.Properties.Probes[0].Properties.RequestPath).To(Equal("/healthz"))

			// Check outbound rule
			g.Expect(lb.Properties.OutboundRules).To(HaveLen(1))
			g.Expect(*lb.Properties.OutboundRules[0].Name).To(Equal(test.infraID))
		})
	}
}

func stringPtr(s string) *string {
	return &s
}

func TestIsAzureConflictError(t *testing.T) {
	tests := map[string]struct {
		err      error
		expected bool
	}{
		"When the error is a 409 ConflictingConcurrentWriteNotAllowed, it should be retryable": {
			err: &azcore.ResponseError{
				StatusCode: http.StatusConflict,
				ErrorCode:  "ConflictingConcurrentWriteNotAllowed",
			},
			expected: true,
		},
		"When the error is a 409 with different error code, it should be retryable": {
			err: &azcore.ResponseError{
				StatusCode: http.StatusConflict,
				ErrorCode:  "AnotherConflict",
			},
			expected: true,
		},
		"When the error is a 429 too many requests, it should not be retryable": {
			err: &azcore.ResponseError{
				StatusCode: http.StatusTooManyRequests,
			},
			expected: false,
		},
		"When the error is a 500 internal server error, it should not be retryable": {
			err: &azcore.ResponseError{
				StatusCode: http.StatusInternalServerError,
			},
			expected: false,
		},
		"When the error is a 400 bad request, it should not be retryable": {
			err: &azcore.ResponseError{
				StatusCode: http.StatusBadRequest,
			},
			expected: false,
		},
		"When the error is not an Azure ResponseError, it should not be retryable": {
			err:      fmt.Errorf("some random error"),
			expected: false,
		},
		"When the error wraps a 409 Azure ResponseError, it should be retryable": {
			err: fmt.Errorf("wrapped: %w", &azcore.ResponseError{
				StatusCode: http.StatusConflict,
				ErrorCode:  "ConflictingConcurrentWriteNotAllowed",
			}),
			expected: true,
		},
		"When the error is nil, it should not be retryable": {
			err:      nil,
			expected: false,
		},
	}

	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			g := NewGomegaWithT(t)
			g.Expect(isAzureConflictError(test.err)).To(Equal(test.expected))
		})
	}
}

// Verify that errors.As works with our test helper errors
func TestErrorsAsAzureResponseError(t *testing.T) {
	g := NewGomegaWithT(t)
	wrappedErr := fmt.Errorf("operation failed: %w", &azcore.ResponseError{
		StatusCode: http.StatusConflict,
		ErrorCode:  "ConflictingConcurrentWriteNotAllowed",
	})

	var respErr *azcore.ResponseError
	g.Expect(errors.As(wrappedErr, &respErr)).To(BeTrue())
	g.Expect(respErr.StatusCode).To(Equal(http.StatusConflict))
}

type virtualNetworkLinkGetAction func(context.Context) (armprivatedns.VirtualNetworkLink, error)

type virtualNetworkLinkBeginAction func(context.Context, *armprivatedns.VirtualNetworkLinksClientBeginCreateOrUpdateOptions) (virtualNetworkLinkPoller, error)

type testTokenCredential struct{}

func (testTokenCredential) GetToken(context.Context, policy.TokenRequestOptions) (azcore.AccessToken, error) {
	return azcore.AccessToken{Token: "test-token", ExpiresOn: time.Now().Add(time.Hour)}, nil
}

type testHTTPTransport func(*http.Request) (*http.Response, error)

func (f testHTTPTransport) Do(request *http.Request) (*http.Response, error) {
	return f(request)
}

type fakeVirtualNetworkLinkClient struct {
	mu sync.Mutex

	getActions   []virtualNetworkLinkGetAction
	beginActions []virtualNetworkLinkBeginAction

	getContexts   []context.Context
	beginContexts []context.Context
	beginOptions  []*armprivatedns.VirtualNetworkLinksClientBeginCreateOrUpdateOptions
}

func (f *fakeVirtualNetworkLinkClient) Get(ctx context.Context, _, _, _ string) (armprivatedns.VirtualNetworkLink, error) {
	f.mu.Lock()
	call := len(f.getContexts)
	f.getContexts = append(f.getContexts, ctx)
	if call >= len(f.getActions) {
		f.mu.Unlock()
		return armprivatedns.VirtualNetworkLink{}, fmt.Errorf("unexpected Get call %d", call+1)
	}
	action := f.getActions[call]
	f.mu.Unlock()
	return action(ctx)
}

func (f *fakeVirtualNetworkLinkClient) BeginCreateOrUpdate(ctx context.Context, _, _, _ string, _ armprivatedns.VirtualNetworkLink, options *armprivatedns.VirtualNetworkLinksClientBeginCreateOrUpdateOptions) (virtualNetworkLinkPoller, error) {
	f.mu.Lock()
	call := len(f.beginContexts)
	f.beginContexts = append(f.beginContexts, ctx)
	var optionsCopy *armprivatedns.VirtualNetworkLinksClientBeginCreateOrUpdateOptions
	if options != nil {
		copied := *options
		optionsCopy = &copied
	}
	f.beginOptions = append(f.beginOptions, optionsCopy)
	if call >= len(f.beginActions) {
		f.mu.Unlock()
		return nil, fmt.Errorf("unexpected BeginCreateOrUpdate call %d", call+1)
	}
	action := f.beginActions[call]
	f.mu.Unlock()
	return action(ctx, options)
}

func (f *fakeVirtualNetworkLinkClient) snapshot() (getContexts, beginContexts []context.Context, beginOptions []*armprivatedns.VirtualNetworkLinksClientBeginCreateOrUpdateOptions) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]context.Context(nil), f.getContexts...), append([]context.Context(nil), f.beginContexts...), append([]*armprivatedns.VirtualNetworkLinksClientBeginCreateOrUpdateOptions(nil), f.beginOptions...)
}

type fakeVirtualNetworkLinkPoller struct {
	mu       sync.Mutex
	result   armprivatedns.VirtualNetworkLink
	err      error
	onPoll   func(context.Context)
	contexts []context.Context
}

type manualErrorContext struct {
	context.Context
	done chan struct{}
	once sync.Once
	mu   sync.RWMutex
	err  error
}

func newManualErrorContext() *manualErrorContext {
	return &manualErrorContext{Context: context.Background(), done: make(chan struct{})}
}

func (c *manualErrorContext) Done() <-chan struct{} {
	return c.done
}

func (c *manualErrorContext) Err() error {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.err
}

func (c *manualErrorContext) cancel(err error) {
	c.once.Do(func() {
		c.mu.Lock()
		c.err = err
		c.mu.Unlock()
		close(c.done)
	})
}

func (f *fakeVirtualNetworkLinkPoller) PollUntilDone(ctx context.Context) (armprivatedns.VirtualNetworkLink, error) {
	f.mu.Lock()
	f.contexts = append(f.contexts, ctx)
	onPoll := f.onPoll
	result := f.result
	err := f.err
	f.mu.Unlock()
	if onPoll != nil {
		onPoll(ctx)
	}
	return result, err
}

func (f *fakeVirtualNetworkLinkPoller) pollContexts() []context.Context {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]context.Context(nil), f.contexts...)
}

func testPrivateDNSZoneLinkConfig(steps int) privateDNSZoneLinkWaitConfig {
	return privateDNSZoneLinkWaitConfig{
		timeout: time.Second,
		backoff: wait.Backoff{Steps: steps},
	}
}

func TestDefaultPrivateDNSZoneLinkWaitConfig(t *testing.T) {
	g := NewGomegaWithT(t)
	config := defaultPrivateDNSZoneLinkWaitConfig()
	g.Expect(config.timeout).To(Equal(2 * time.Minute))
	g.Expect(config.backoff.Duration).To(Equal(2 * time.Second))
	g.Expect(config.backoff.Factor).To(Equal(2.0))
	g.Expect(config.backoff.Jitter).To(Equal(0.1))
	g.Expect(config.backoff.Steps).To(Equal(7))
	g.Expect(config.backoff.Cap).To(Equal(30 * time.Second))
}

func TestCappedExponentialBackoffWithContext(t *testing.T) {
	g := NewGomegaWithT(t)
	attempts := 0
	err := cappedExponentialBackoffWithContext(context.Background(), wait.Backoff{
		Duration: time.Nanosecond,
		Factor:   2,
		Steps:    7,
		Cap:      time.Nanosecond,
	}, func(context.Context) (bool, error) {
		attempts++
		return false, nil
	})
	g.Expect(wait.Interrupted(err)).To(BeTrue())
	g.Expect(attempts).To(Equal(7))
}

func getVirtualNetworkLink(link armprivatedns.VirtualNetworkLink) virtualNetworkLinkGetAction {
	return func(context.Context) (armprivatedns.VirtualNetworkLink, error) {
		return link, nil
	}
}

func getVirtualNetworkLinkError(err error) virtualNetworkLinkGetAction {
	return func(context.Context) (armprivatedns.VirtualNetworkLink, error) {
		return armprivatedns.VirtualNetworkLink{}, err
	}
}

func beginVirtualNetworkLink(poller virtualNetworkLinkPoller, err error) virtualNetworkLinkBeginAction {
	return func(context.Context, *armprivatedns.VirtualNetworkLinksClientBeginCreateOrUpdateOptions) (virtualNetworkLinkPoller, error) {
		return poller, err
	}
}

func testVirtualNetworkLink(vnetID string, registrationEnabled *bool, provisioningState *armprivatedns.ProvisioningState, linkState *armprivatedns.VirtualNetworkLinkState) armprivatedns.VirtualNetworkLink {
	return armprivatedns.VirtualNetworkLink{
		Properties: &armprivatedns.VirtualNetworkLinkProperties{
			VirtualNetwork:          &armprivatedns.SubResource{ID: ptr.To(vnetID)},
			RegistrationEnabled:     registrationEnabled,
			ProvisioningState:       provisioningState,
			VirtualNetworkLinkState: linkState,
		},
	}
}

func testResponseError(statusCode int, topLevelCode, body string) *azcore.ResponseError {
	return &azcore.ResponseError{
		StatusCode: statusCode,
		ErrorCode:  topLevelCode,
		RawResponse: &http.Response{
			StatusCode: statusCode,
			Status:     http.StatusText(statusCode),
			Header:     http.Header{},
			Body:       io.NopCloser(strings.NewReader(body)),
		},
	}
}

func testSensitiveResponseError(t *testing.T, statusCode int, topLevelCode, marker string) *azcore.ResponseError {
	t.Helper()
	body := fmt.Sprintf(`{"error":{"code":"%s","message":"raw-body-%s contains /operations/type/UpsertVirtualNetworkLink/id/operation-%s"}}`, topLevelCode, marker, marker)
	responseError := testResponseError(statusCode, topLevelCode, body)
	request, err := http.NewRequestWithContext(t.Context(), http.MethodGet, "https://management.azure.com/subscriptions/"+marker+"/resourceGroups/resource-"+marker, nil)
	if err != nil {
		t.Fatalf("failed to construct sensitive response request: %v", err)
	}
	responseError.RawResponse.Request = request
	return responseError
}

const pendingVirtualNetworkLinkUpsertBody = `{"error":{"code":"Conflict","message":"Another operation is pending for requested object. ... already has 1 operations like '/operations/type/UpsertVirtualNetworkLink/id/<REDACTED>' queued."}}`

const pendingVirtualNetworkLinkUpsertStructuredBody = `{"error":{"code":"Conflict","message":"Another operation is pending for requested object. Operation group '/operations/groups/id/|virtualNetworkLinks|<SUBSCRIPTION>|<RESOURCE_GROUP>|<DNS_ZONE>|<LINK_NAME>' already has 1 operations like '/operations/type/UpsertVirtualNetworkLink/id/<OPERATION_ID>_<SUBSCRIPTION>' queued."}}`

func TestEvaluateVirtualNetworkLink(t *testing.T) {
	desiredVNetID := "/subscriptions/example/resourceGroups/example/providers/Microsoft.Network/virtualNetworks/desired"
	succeeded := armprivatedns.ProvisioningStateSucceeded
	creating := armprivatedns.ProvisioningStateCreating
	failed := armprivatedns.ProvisioningStateFailed
	canceled := armprivatedns.ProvisioningStateCanceled
	deleting := armprivatedns.ProvisioningStateDeleting
	completed := armprivatedns.VirtualNetworkLinkStateCompleted
	inProgress := armprivatedns.VirtualNetworkLinkStateInProgress
	unknown := armprivatedns.ProvisioningState("FutureState")
	unknownLinkState := armprivatedns.VirtualNetworkLinkState("FutureState")
	missingVNetID := testVirtualNetworkLink(desiredVNetID, ptr.To(false), &succeeded, nil)
	missingVNetID.Properties.VirtualNetwork.ID = nil
	missingVNetIDWithEnabledRegistration := testVirtualNetworkLink(desiredVNetID, ptr.To(true), &succeeded, nil)
	missingVNetIDWithEnabledRegistration.Properties.VirtualNetwork.ID = nil

	tests := map[string]struct {
		link      armprivatedns.VirtualNetworkLink
		authority virtualNetworkLinkCompletionAuthority
		complete  bool
		retry     bool
		wantError bool
	}{
		"When a successful poller returns desired properties with both optional states absent, it should succeed": {
			link: testVirtualNetworkLink(desiredVNetID, ptr.To(false), nil, nil), authority: virtualNetworkLinkSuccessfulPoller, complete: true,
		},
		"When a recovery read has both optional states absent, it should remain unproven": {
			link: testVirtualNetworkLink(desiredVNetID, ptr.To(false), nil, nil), authority: virtualNetworkLinkRecoveryRead, retry: true,
		},
		"When a recovery read has Succeeded and the other state absent, it should succeed": {
			link: testVirtualNetworkLink(desiredVNetID, ptr.To(false), &succeeded, nil), authority: virtualNetworkLinkRecoveryRead, complete: true,
		},
		"When a recovery read has Completed and the other state absent, it should succeed": {
			link: testVirtualNetworkLink(desiredVNetID, ptr.To(false), nil, &completed), authority: virtualNetworkLinkRecoveryRead, complete: true,
		},
		"When both explicit states are successful, it should succeed": {
			link: testVirtualNetworkLink(desiredVNetID, ptr.To(false), &succeeded, &completed), authority: virtualNetworkLinkRecoveryRead, complete: true,
		},
		"When success is mixed with pending, it should remain pending": {
			link: testVirtualNetworkLink(desiredVNetID, ptr.To(false), &succeeded, &inProgress), authority: virtualNetworkLinkRecoveryRead, retry: true,
		},
		"When a provisioning state is pending, it should remain pending even with Completed": {
			link: testVirtualNetworkLink(desiredVNetID, ptr.To(false), &creating, &completed), authority: virtualNetworkLinkRecoveryRead, retry: true,
		},
		"When a failure state is present, it should fail": {
			link: testVirtualNetworkLink(desiredVNetID, ptr.To(false), &failed, &completed), authority: virtualNetworkLinkRecoveryRead, wantError: true,
		},
		"When a canceled state is present, it should fail": {
			link: testVirtualNetworkLink(desiredVNetID, ptr.To(false), &canceled, &completed), authority: virtualNetworkLinkRecoveryRead, wantError: true,
		},
		"When a deleting state is present, it should fail": {
			link: testVirtualNetworkLink(desiredVNetID, ptr.To(false), &deleting, &completed), authority: virtualNetworkLinkRecoveryRead, wantError: true,
		},
		"When an unknown state is present, it should fail closed": {
			link: testVirtualNetworkLink(desiredVNetID, ptr.To(false), &unknown, &completed), authority: virtualNetworkLinkRecoveryRead, wantError: true,
		},
		"When an unknown link state is present, it should fail closed": {
			link: testVirtualNetworkLink(desiredVNetID, ptr.To(false), &succeeded, &unknownLinkState), authority: virtualNetworkLinkRecoveryRead, wantError: true,
		},
		"When registration is missing, it should remain unproven": {
			link: testVirtualNetworkLink(desiredVNetID, nil, &succeeded, nil), authority: virtualNetworkLinkRecoveryRead, retry: true,
		},
		"When the VNet ID is missing, it should remain unproven": {
			link: missingVNetID, authority: virtualNetworkLinkRecoveryRead, retry: true,
		},
		"When registration is missing but the VNet differs, it should fail immediately": {
			link: testVirtualNetworkLink("/subscriptions/example/resourceGroups/example/providers/Microsoft.Network/virtualNetworks/other", nil, &succeeded, nil), authority: virtualNetworkLinkRecoveryRead, wantError: true,
		},
		"When the VNet ID is missing but registration is enabled, it should fail immediately": {
			link: missingVNetIDWithEnabledRegistration, authority: virtualNetworkLinkRecoveryRead, wantError: true,
		},
		"When registration is enabled, it should fail without overwrite": {
			link: testVirtualNetworkLink(desiredVNetID, ptr.To(true), &succeeded, &completed), authority: virtualNetworkLinkRecoveryRead, wantError: true,
		},
		"When the VNet differs, it should fail without overwrite": {
			link: testVirtualNetworkLink("/subscriptions/example/resourceGroups/example/providers/Microsoft.Network/virtualNetworks/other", ptr.To(false), &succeeded, &completed), authority: virtualNetworkLinkRecoveryRead, wantError: true,
		},
	}

	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			g := NewGomegaWithT(t)
			complete, retry, err := evaluateVirtualNetworkLink(assessVirtualNetworkLink(test.link, desiredVNetID), test.authority)
			g.Expect(complete).To(Equal(test.complete))
			g.Expect(retry).To(Equal(test.retry))
			if test.wantError {
				g.Expect(err).To(HaveOccurred())
			} else {
				g.Expect(err).ToNot(HaveOccurred())
			}
		})
	}
}

func TestCreatePrivateDNSZoneLink(t *testing.T) {
	t.Run("When an existing link is observed, it should enforce the recovery policy", testCreatePrivateDNSZoneLinkExistingLinkPolicy)
	t.Run("When an absent link is created, it should preserve context and create-only semantics", testCreatePrivateDNSZoneLinkUsesConditionalCreateAndOriginalPollContext)
	t.Run("When a successful poll result is incomplete, it should verify the result", testCreatePrivateDNSZoneLinkValidatesIncompleteSuccessfulPollResult)
	t.Run("When the incident conflict is returned, it should observe a visible link without overwriting", testCreatePrivateDNSZoneLinkRecoversPendingUpsertWithoutOverwritingVisibleLink)
	t.Run("When the incident conflict leaves the target absent, it should conditionally resubmit", testCreatePrivateDNSZoneLinkResubmitsOnlyAfterTargetRemainsAbsent)
	t.Run("When the incident conflict never converges, it should fail at the configured bound", testCreatePrivateDNSZoneLinkPendingConflictExhaustion)
	t.Run("When a conditional create loses a race, it should verify without overwriting", testCreatePrivateDNSZoneLinkHandlesConditionalCreateRace)
	t.Run("When a conditional race remains pending, it should observe without overwriting", testCreatePrivateDNSZoneLinkObservesPendingConditionalRaceWithoutOverwrite)
	t.Run("When a pending conditional race never converges, it should fail at the configured bound preserving the 412 cause", testCreatePrivateDNSZoneLinkConditionalRaceExhaustion)
	t.Run("When Begin returns an unrelated error, it should fail closed", testCreatePrivateDNSZoneLinkPreservesUnrelatedBeginError)
	t.Run("When polling ends after cancellation, it should skip recovery GET", testCreatePrivateDNSZoneLinkPollErrorCancellationSkipsRecoveryGet)
	t.Run("When polling ends after a deadline, it should skip recovery GET", testCreatePrivateDNSZoneLinkPollErrorDeadlineSkipsRecoveryGet)
	t.Run("When polling fails without context cancellation, it should use one conservative recovery GET", testCreatePrivateDNSZoneLinkPollErrorUsesOneConservativeRecoveryGet)
	t.Run("When an in-flight provider call is canceled, it should stop subsequent calls", testCreatePrivateDNSZoneLinkInFlightCancellation)
	t.Run("When cancellation occurs during backoff, it should stop subsequent calls", testCreatePrivateDNSZoneLinkCancellationDuringBackoffStopsFurtherCalls)
	t.Run("When the convergence child deadline expires, it should preserve the deadline and stop calls", testCreatePrivateDNSZoneLinkInternalDeadlineStopsFurtherCalls)
	t.Run("When provider failures are returned, it should sanitize text and preserve causes", testCreatePrivateDNSZoneLinkSanitizesProviderErrors)
}

func TestAzureVirtualNetworkLinkClientBeginCreateOrUpdate(t *testing.T) {
	const (
		subscriptionID      = "test-subscription"
		resourceGroupName   = "test-resource-group"
		privateDNSZoneName  = "test.private.example.com"
		linkName            = "test-link"
		desiredVNetID       = "/subscriptions/test-subscription/resourceGroups/test-vnet-resource-group/providers/Microsoft.Network/virtualNetworks/test-vnet"
		expectedRequestPath = "/subscriptions/test-subscription/resourceGroups/test-resource-group/providers/Microsoft.Network/privateDnsZones/test.private.example.com/virtualNetworkLinks/test-link"
	)

	newClient := func(t *testing.T, transport testHTTPTransport) *azureVirtualNetworkLinkClient {
		t.Helper()
		sdkClient, err := armprivatedns.NewVirtualNetworkLinksClient(subscriptionID, testTokenCredential{}, &arm.ClientOptions{
			ClientOptions: policy.ClientOptions{Transport: transport},
		})
		NewGomegaWithT(t).Expect(err).ToNot(HaveOccurred())
		return &azureVirtualNetworkLinkClient{client: sdkClient}
	}

	t.Run("When the adapter creates a link, it should forward the target and desired request", func(t *testing.T) {
		g := NewGomegaWithT(t)
		transport := testHTTPTransport(func(request *http.Request) (*http.Response, error) {
			g.Expect(request.Method).To(Equal(http.MethodPut))
			g.Expect(request.URL.Path).To(Equal(expectedRequestPath))
			g.Expect(request.URL.Query().Get("api-version")).To(Equal("2024-06-01"))
			g.Expect(request.Header.Get("If-None-Match")).To(Equal("*"))

			var requestedLink armprivatedns.VirtualNetworkLink
			g.Expect(json.NewDecoder(request.Body).Decode(&requestedLink)).To(Succeed())
			g.Expect(requestedLink.Properties).ToNot(BeNil())
			g.Expect(requestedLink.Properties.VirtualNetwork).ToNot(BeNil())
			g.Expect(requestedLink.Properties.VirtualNetwork.ID).ToNot(BeNil())
			g.Expect(*requestedLink.Properties.VirtualNetwork.ID).To(Equal(desiredVNetID))
			g.Expect(requestedLink.Properties.RegistrationEnabled).ToNot(BeNil())
			g.Expect(*requestedLink.Properties.RegistrationEnabled).To(BeFalse())

			return &http.Response{
				StatusCode: http.StatusOK,
				Status:     http.StatusText(http.StatusOK),
				Header:     http.Header{"Content-Type": []string{"application/json"}},
				Body: io.NopCloser(strings.NewReader(fmt.Sprintf(
					`{"location":"global","properties":{"provisioningState":"Succeeded","virtualNetwork":{"id":%q},"registrationEnabled":false}}`,
					desiredVNetID,
				))),
				Request: request,
			}, nil
		})
		client := newClient(t, transport)

		poller, err := client.BeginCreateOrUpdate(
			t.Context(),
			resourceGroupName,
			privateDNSZoneName,
			linkName,
			NewVirtualNetworkLink(VirtualNetworkLinkLocation, desiredVNetID, false),
			&armprivatedns.VirtualNetworkLinksClientBeginCreateOrUpdateOptions{IfNoneMatch: ptr.To("*")},
		)

		g.Expect(err).ToNot(HaveOccurred())
		g.Expect(poller).ToNot(BeNil())
	})

	t.Run("When the SDK returns the recognized pending conflict, it should construct a classifiable response error", func(t *testing.T) {
		g := NewGomegaWithT(t)
		transport := testHTTPTransport(func(request *http.Request) (*http.Response, error) {
			return &http.Response{
				StatusCode: http.StatusConflict,
				Status:     http.StatusText(http.StatusConflict),
				Header:     http.Header{"Content-Type": []string{"application/json"}},
				Body:       io.NopCloser(strings.NewReader(pendingVirtualNetworkLinkUpsertStructuredBody)),
				Request:    request,
			}, nil
		})
		client := newClient(t, transport)

		poller, err := client.BeginCreateOrUpdate(
			t.Context(),
			resourceGroupName,
			privateDNSZoneName,
			linkName,
			NewVirtualNetworkLink(VirtualNetworkLinkLocation, desiredVNetID, false),
			&armprivatedns.VirtualNetworkLinksClientBeginCreateOrUpdateOptions{IfNoneMatch: ptr.To("*")},
		)

		g.Expect(poller).To(BeNil())
		g.Expect(err).To(HaveOccurred())
		var responseError *azcore.ResponseError
		g.Expect(errors.As(err, &responseError)).To(BeTrue())
		g.Expect(responseError.StatusCode).To(Equal(http.StatusConflict))
		g.Expect(responseError.ErrorCode).To(Equal("Conflict"))
		g.Expect(isPendingVirtualNetworkLinkUpsert(err)).To(BeTrue())
	})
}

func testCreatePrivateDNSZoneLinkExistingLinkPolicy(t *testing.T) {
	desiredVNetID := "/subscriptions/example/resourceGroups/example/providers/Microsoft.Network/virtualNetworks/desired"
	succeeded := armprivatedns.ProvisioningStateSucceeded
	creating := armprivatedns.ProvisioningStateCreating
	missingVNetIDWithEnabledRegistration := testVirtualNetworkLink(desiredVNetID, ptr.To(true), &succeeded, nil)
	missingVNetIDWithEnabledRegistration.Properties.VirtualNetwork.ID = nil
	tests := map[string]struct {
		links     []armprivatedns.VirtualNetworkLink
		wantError bool
	}{
		"When an existing desired link has one explicit success state, it should succeed without a PUT": {
			links: []armprivatedns.VirtualNetworkLink{testVirtualNetworkLink(desiredVNetID, ptr.To(false), &succeeded, nil)},
		},
		"When an existing desired link progresses to explicit success, it should use GET only": {
			links: []armprivatedns.VirtualNetworkLink{
				testVirtualNetworkLink(desiredVNetID, ptr.To(false), &creating, nil),
				testVirtualNetworkLink(desiredVNetID, ptr.To(false), &succeeded, nil),
			},
		},
		"When an existing desired link has both optional states absent, it should fail bounded": {
			links: []armprivatedns.VirtualNetworkLink{
				testVirtualNetworkLink(desiredVNetID, ptr.To(false), nil, nil),
				testVirtualNetworkLink(desiredVNetID, ptr.To(false), nil, nil),
			}, wantError: true,
		},
		"When an existing link is incompatible, it should fail without a PUT": {
			links: []armprivatedns.VirtualNetworkLink{testVirtualNetworkLink("/subscriptions/example/resourceGroups/example/providers/Microsoft.Network/virtualNetworks/other", ptr.To(false), &succeeded, nil)}, wantError: true,
		},
		"When registration is missing but the VNet differs, it should fail after one GET without a PUT": {
			links: []armprivatedns.VirtualNetworkLink{testVirtualNetworkLink("/subscriptions/example/resourceGroups/example/providers/Microsoft.Network/virtualNetworks/other", nil, &succeeded, nil)}, wantError: true,
		},
		"When the VNet ID is missing but registration is enabled, it should fail after one GET without a PUT": {
			links: []armprivatedns.VirtualNetworkLink{missingVNetIDWithEnabledRegistration}, wantError: true,
		},
	}

	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			g := NewGomegaWithT(t)
			client := &fakeVirtualNetworkLinkClient{}
			for _, link := range test.links {
				client.getActions = append(client.getActions, getVirtualNetworkLink(link))
			}

			err := createPrivateDNSZoneLink(context.Background(), client, "resource-group", "private-zone", "link", desiredVNetID, testPrivateDNSZoneLinkConfig(len(test.links)))
			if test.wantError {
				g.Expect(err).To(HaveOccurred())
			} else {
				g.Expect(err).ToNot(HaveOccurred())
			}
			getContexts, beginContexts, _ := client.snapshot()
			g.Expect(getContexts).To(HaveLen(len(test.links)))
			g.Expect(beginContexts).To(BeEmpty())
		})
	}
}

func testCreatePrivateDNSZoneLinkUsesConditionalCreateAndOriginalPollContext(t *testing.T) {
	g := NewGomegaWithT(t)
	desiredVNetID := "/subscriptions/example/resourceGroups/example/providers/Microsoft.Network/virtualNetworks/desired"
	notFound := testResponseError(http.StatusNotFound, "NotFound", ``)
	poller := &fakeVirtualNetworkLinkPoller{result: testVirtualNetworkLink(desiredVNetID, ptr.To(false), nil, nil)}
	client := &fakeVirtualNetworkLinkClient{
		getActions:   []virtualNetworkLinkGetAction{getVirtualNetworkLinkError(notFound)},
		beginActions: []virtualNetworkLinkBeginAction{beginVirtualNetworkLink(poller, nil)},
	}
	type contextKey string
	ctx := context.WithValue(context.Background(), contextKey("key"), "value")

	err := createPrivateDNSZoneLink(ctx, client, "resource-group", "private-zone", "link", desiredVNetID, testPrivateDNSZoneLinkConfig(2))
	g.Expect(err).ToNot(HaveOccurred())

	getContexts, beginContexts, beginOptions := client.snapshot()
	g.Expect(getContexts).To(HaveLen(1))
	g.Expect(beginContexts).To(HaveLen(1))
	g.Expect(beginOptions).To(HaveLen(1))
	g.Expect(beginOptions[0]).ToNot(BeNil())
	g.Expect(beginOptions[0].IfNoneMatch).ToNot(BeNil())
	g.Expect(*beginOptions[0].IfNoneMatch).To(Equal("*"))
	_, hasGetDeadline := getContexts[0].Deadline()
	_, hasBeginDeadline := beginContexts[0].Deadline()
	g.Expect(hasGetDeadline).To(BeTrue())
	g.Expect(hasBeginDeadline).To(BeTrue())
	g.Expect(getContexts[0].Value(contextKey("key"))).To(Equal("value"))
	g.Expect(beginContexts[0].Value(contextKey("key"))).To(Equal("value"))
	g.Expect(getContexts[0] == ctx).To(BeFalse())
	g.Expect(beginContexts[0] == ctx).To(BeFalse())
	pollContexts := poller.pollContexts()
	g.Expect(pollContexts).To(HaveLen(1))
	g.Expect(pollContexts[0] == ctx).To(BeTrue())
}

func testCreatePrivateDNSZoneLinkValidatesIncompleteSuccessfulPollResult(t *testing.T) {
	desiredVNetID := "/subscriptions/example/resourceGroups/example/providers/Microsoft.Network/virtualNetworks/desired"
	creating := armprivatedns.ProvisioningStateCreating
	inProgress := armprivatedns.VirtualNetworkLinkStateInProgress
	tests := map[string]struct {
		pollResult      armprivatedns.VirtualNetworkLink
		validationLinks []armprivatedns.VirtualNetworkLink
		wantError       bool
	}{
		"When a successful poll result omits desired properties, it should verify them using bounded GETs": {
			pollResult:      armprivatedns.VirtualNetworkLink{},
			validationLinks: []armprivatedns.VirtualNetworkLink{testVirtualNetworkLink(desiredVNetID, ptr.To(false), nil, nil)},
		},
		"When a successful poll result still reports pending, it should verify until pending clears": {
			pollResult:      testVirtualNetworkLink(desiredVNetID, ptr.To(false), &creating, &inProgress),
			validationLinks: []armprivatedns.VirtualNetworkLink{testVirtualNetworkLink(desiredVNetID, ptr.To(false), nil, nil)},
		},
		"When pending never clears after successful polling, it should fail bounded": {
			pollResult: testVirtualNetworkLink(desiredVNetID, ptr.To(false), &creating, &inProgress),
			validationLinks: []armprivatedns.VirtualNetworkLink{
				testVirtualNetworkLink(desiredVNetID, ptr.To(false), &creating, &inProgress),
				testVirtualNetworkLink(desiredVNetID, ptr.To(false), &creating, &inProgress),
			},
			wantError: true,
		},
	}

	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			g := NewGomegaWithT(t)
			notFound := testResponseError(http.StatusNotFound, "NotFound", ``)
			poller := &fakeVirtualNetworkLinkPoller{result: test.pollResult}
			client := &fakeVirtualNetworkLinkClient{
				getActions:   []virtualNetworkLinkGetAction{getVirtualNetworkLinkError(notFound)},
				beginActions: []virtualNetworkLinkBeginAction{beginVirtualNetworkLink(poller, nil)},
			}
			for _, link := range test.validationLinks {
				client.getActions = append(client.getActions, getVirtualNetworkLink(link))
			}

			err := createPrivateDNSZoneLink(context.Background(), client, "resource-group", "private-zone", "link", desiredVNetID, testPrivateDNSZoneLinkConfig(len(test.validationLinks)))
			if test.wantError {
				g.Expect(err).To(HaveOccurred())
			} else {
				g.Expect(err).ToNot(HaveOccurred())
			}
			getContexts, beginContexts, _ := client.snapshot()
			g.Expect(getContexts).To(HaveLen(1 + len(test.validationLinks)))
			g.Expect(beginContexts).To(HaveLen(1))
		})
	}
}

func testCreatePrivateDNSZoneLinkRecoversPendingUpsertWithoutOverwritingVisibleLink(t *testing.T) {
	g := NewGomegaWithT(t)
	desiredVNetID := "/subscriptions/example/resourceGroups/example/providers/Microsoft.Network/virtualNetworks/desired"
	notFound := testResponseError(http.StatusNotFound, "NotFound", ``)
	pendingConflict := testResponseError(http.StatusConflict, "Conflict", pendingVirtualNetworkLinkUpsertBody)
	creating := armprivatedns.ProvisioningStateCreating
	inProgress := armprivatedns.VirtualNetworkLinkStateInProgress
	succeeded := armprivatedns.ProvisioningStateSucceeded
	client := &fakeVirtualNetworkLinkClient{
		getActions: []virtualNetworkLinkGetAction{
			getVirtualNetworkLinkError(notFound),
			getVirtualNetworkLink(testVirtualNetworkLink(desiredVNetID, ptr.To(false), &creating, &inProgress)),
			getVirtualNetworkLink(testVirtualNetworkLink(desiredVNetID, ptr.To(false), &succeeded, nil)),
		},
		beginActions: []virtualNetworkLinkBeginAction{beginVirtualNetworkLink(nil, pendingConflict)},
	}

	err := createPrivateDNSZoneLink(context.Background(), client, "resource-group", "private-zone", "link", desiredVNetID, testPrivateDNSZoneLinkConfig(4))
	g.Expect(err).ToNot(HaveOccurred())
	getContexts, beginContexts, _ := client.snapshot()
	g.Expect(getContexts).To(HaveLen(3))
	g.Expect(beginContexts).To(HaveLen(1))
}

func testCreatePrivateDNSZoneLinkResubmitsOnlyAfterTargetRemainsAbsent(t *testing.T) {
	g := NewGomegaWithT(t)
	desiredVNetID := "/subscriptions/example/resourceGroups/example/providers/Microsoft.Network/virtualNetworks/desired"
	notFoundOne := testResponseError(http.StatusNotFound, "NotFound", ``)
	notFoundTwo := testResponseError(http.StatusNotFound, "NotFound", ``)
	pendingConflict := testResponseError(http.StatusConflict, "Conflict", pendingVirtualNetworkLinkUpsertBody)
	poller := &fakeVirtualNetworkLinkPoller{result: testVirtualNetworkLink(desiredVNetID, ptr.To(false), nil, nil)}
	client := &fakeVirtualNetworkLinkClient{
		getActions: []virtualNetworkLinkGetAction{
			getVirtualNetworkLinkError(notFoundOne),
			getVirtualNetworkLinkError(notFoundTwo),
		},
		beginActions: []virtualNetworkLinkBeginAction{
			beginVirtualNetworkLink(nil, pendingConflict),
			beginVirtualNetworkLink(poller, nil),
		},
	}

	err := createPrivateDNSZoneLink(context.Background(), client, "resource-group", "private-zone", "link", desiredVNetID, testPrivateDNSZoneLinkConfig(3))
	g.Expect(err).ToNot(HaveOccurred())
	_, beginContexts, options := client.snapshot()
	g.Expect(beginContexts).To(HaveLen(2))
	g.Expect(options).To(HaveLen(2))
	for _, option := range options {
		g.Expect(option).ToNot(BeNil())
		g.Expect(option.IfNoneMatch).ToNot(BeNil())
		g.Expect(*option.IfNoneMatch).To(Equal("*"))
	}
}

func testCreatePrivateDNSZoneLinkPendingConflictExhaustion(t *testing.T) {
	g := NewGomegaWithT(t)
	desiredVNetID := "/subscriptions/example/resourceGroups/example/providers/Microsoft.Network/virtualNetworks/desired"
	client := &fakeVirtualNetworkLinkClient{}
	var lastPendingConflict *azcore.ResponseError
	for attempt := range 3 {
		pendingBody := fmt.Sprintf(`{"error":{"code":"Conflict","message":"Another operation is pending for requested object. Operation group '/operations/groups/id/|virtualNetworkLinks|sensitive-subscription-%d|sensitive-resource-group-%d|sensitive-zone-%d|sensitive-link-%d' already has 1 operations like '/operations/type/UpsertVirtualNetworkLink/id/sensitive-operation-%d' queued."}}`, attempt, attempt, attempt, attempt, attempt)
		lastPendingConflict = testResponseError(http.StatusConflict, "Conflict", pendingBody)
		client.getActions = append(client.getActions, getVirtualNetworkLinkError(testResponseError(http.StatusNotFound, "NotFound", ``)))
		client.beginActions = append(client.beginActions, beginVirtualNetworkLink(nil, lastPendingConflict))
	}

	err := createPrivateDNSZoneLink(context.Background(), client, "resource-group", "private-zone", "link", desiredVNetID, testPrivateDNSZoneLinkConfig(3))
	g.Expect(err).To(HaveOccurred())
	g.Expect(wait.Interrupted(err)).To(BeTrue())
	var responseError *azcore.ResponseError
	g.Expect(errors.As(err, &responseError)).To(BeTrue())
	g.Expect(responseError).To(BeIdenticalTo(lastPendingConflict))
	g.Expect(err.Error()).To(ContainSubstring("attempts=3"))
	g.Expect(err.Error()).To(ContainSubstring("category=pending virtual network link upsert"))
	g.Expect(err.Error()).To(ContainSubstring("category=pending-upsert status=409 code=Conflict"))
	g.Expect(err.Error()).ToNot(ContainSubstring("/operations/type/"))
	g.Expect(err.Error()).ToNot(ContainSubstring("sensitive-subscription"))
	g.Expect(err.Error()).ToNot(ContainSubstring("sensitive-resource-group"))
	g.Expect(err.Error()).ToNot(ContainSubstring("sensitive-zone"))
	g.Expect(err.Error()).ToNot(ContainSubstring("sensitive-link"))
	g.Expect(err.Error()).ToNot(ContainSubstring("sensitive-operation"))
	getContexts, beginContexts, _ := client.snapshot()
	g.Expect(getContexts).To(HaveLen(3))
	g.Expect(beginContexts).To(HaveLen(3))
}

func testCreatePrivateDNSZoneLinkHandlesConditionalCreateRace(t *testing.T) {
	desiredVNetID := "/subscriptions/example/resourceGroups/example/providers/Microsoft.Network/virtualNetworks/desired"
	succeeded := armprivatedns.ProvisioningStateSucceeded
	tests := map[string]struct {
		raceLink      armprivatedns.VirtualNetworkLink
		raceGetErr    error
		wantError     bool
		wantOriginal  bool
		wantSecondary bool
		precondition  *azcore.ResponseError
	}{
		"When a compatible link wins the conditional race, it should recover": {
			raceLink:     testVirtualNetworkLink(desiredVNetID, ptr.To(false), &succeeded, nil),
			precondition: testResponseError(http.StatusPreconditionFailed, "PreconditionFailed", ``),
		},
		"When an incompatible VNet wins the conditional race, it should not overwrite it": {
			raceLink:     testVirtualNetworkLink("/subscriptions/example/resourceGroups/example/providers/Microsoft.Network/virtualNetworks/other", ptr.To(false), &succeeded, nil),
			wantError:    true,
			wantOriginal: true,
			precondition: testResponseError(http.StatusPreconditionFailed, "PreconditionFailed", ``),
		},
		"When registration enabled wins the conditional race, it should not overwrite it": {
			raceLink:     testVirtualNetworkLink(desiredVNetID, ptr.To(true), &succeeded, nil),
			wantError:    true,
			wantOriginal: true,
			precondition: testResponseError(http.StatusPreconditionFailed, "PreconditionFailed", ``),
		},
		"When the target remains absent after the conditional response, it should preserve the original error": {
			raceGetErr:   testResponseError(http.StatusNotFound, "NotFound", ``),
			wantError:    true,
			wantOriginal: true,
			precondition: testResponseError(http.StatusPreconditionFailed, "PreconditionFailed", ``),
		},
		"When verification fails after the conditional response, it should preserve the original error": {
			raceGetErr:    testResponseError(http.StatusInternalServerError, "InternalServerError", ``),
			wantError:     true,
			wantOriginal:  true,
			wantSecondary: true,
			precondition:  testResponseError(http.StatusPreconditionFailed, "PreconditionFailed", ``),
		},
	}

	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			g := NewGomegaWithT(t)
			initialNotFound := testResponseError(http.StatusNotFound, "NotFound", ``)
			client := &fakeVirtualNetworkLinkClient{
				getActions: []virtualNetworkLinkGetAction{
					getVirtualNetworkLinkError(initialNotFound),
				},
				beginActions: []virtualNetworkLinkBeginAction{beginVirtualNetworkLink(nil, test.precondition)},
			}
			if test.raceGetErr != nil {
				client.getActions = append(client.getActions, getVirtualNetworkLinkError(test.raceGetErr))
			} else {
				client.getActions = append(client.getActions, getVirtualNetworkLink(test.raceLink))
			}

			err := createPrivateDNSZoneLink(context.Background(), client, "resource-group", "private-zone", "link", desiredVNetID, testPrivateDNSZoneLinkConfig(2))
			if test.wantError {
				g.Expect(err).To(HaveOccurred())
			} else {
				g.Expect(err).ToNot(HaveOccurred())
			}
			if test.wantOriginal {
				g.Expect(errors.Is(err, test.precondition)).To(BeTrue())
			}
			if test.wantSecondary {
				g.Expect(errors.Is(err, test.raceGetErr)).To(BeTrue())
			}
			getContexts, beginContexts, _ := client.snapshot()
			g.Expect(getContexts).To(HaveLen(2))
			g.Expect(beginContexts).To(HaveLen(1))
		})
	}
}

func testCreatePrivateDNSZoneLinkObservesPendingConditionalRaceWithoutOverwrite(t *testing.T) {
	g := NewGomegaWithT(t)
	desiredVNetID := "/subscriptions/example/resourceGroups/example/providers/Microsoft.Network/virtualNetworks/desired"
	notFound := testResponseError(http.StatusNotFound, "NotFound", ``)
	precondition := testResponseError(http.StatusPreconditionFailed, "PreconditionFailed", ``)
	creating := armprivatedns.ProvisioningStateCreating
	succeeded := armprivatedns.ProvisioningStateSucceeded
	client := &fakeVirtualNetworkLinkClient{
		getActions: []virtualNetworkLinkGetAction{
			getVirtualNetworkLinkError(notFound),
			getVirtualNetworkLink(testVirtualNetworkLink(desiredVNetID, ptr.To(false), &creating, nil)),
			getVirtualNetworkLink(testVirtualNetworkLink(desiredVNetID, ptr.To(false), &succeeded, nil)),
		},
		beginActions: []virtualNetworkLinkBeginAction{beginVirtualNetworkLink(nil, precondition)},
	}

	err := createPrivateDNSZoneLink(context.Background(), client, "resource-group", "private-zone", "link", desiredVNetID, testPrivateDNSZoneLinkConfig(3))
	g.Expect(err).ToNot(HaveOccurred())
	getContexts, beginContexts, _ := client.snapshot()
	g.Expect(getContexts).To(HaveLen(3))
	g.Expect(beginContexts).To(HaveLen(1))
}

func testCreatePrivateDNSZoneLinkConditionalRaceExhaustion(t *testing.T) {
	g := NewGomegaWithT(t)
	desiredVNetID := "/subscriptions/example/resourceGroups/example/providers/Microsoft.Network/virtualNetworks/desired"
	creating := armprivatedns.ProvisioningStateCreating
	client := &fakeVirtualNetworkLinkClient{}
	var lastPrecondition *azcore.ResponseError
	markers := make([]string, 0, 3)
	for attempt := range 3 {
		marker := fmt.Sprintf("CONDITIONAL_RACE_EXHAUSTION_MARKER_%d", attempt)
		markers = append(markers, marker)
		lastPrecondition = testSensitiveResponseError(t, http.StatusPreconditionFailed, "PreconditionFailed", marker)
		// Each attempt loses the conditional create to a compatible but still-pending
		// link, so observation continues until the retry budget is exhausted.
		client.getActions = append(client.getActions,
			getVirtualNetworkLinkError(testResponseError(http.StatusNotFound, "NotFound", ``)),
			getVirtualNetworkLink(testVirtualNetworkLink(desiredVNetID, ptr.To(false), &creating, nil)),
		)
		client.beginActions = append(client.beginActions, beginVirtualNetworkLink(nil, lastPrecondition))
	}

	err := createPrivateDNSZoneLink(context.Background(), client, "resource-group", "private-zone", "link", desiredVNetID, testPrivateDNSZoneLinkConfig(3))
	g.Expect(err).To(HaveOccurred())
	g.Expect(wait.Interrupted(err)).To(BeTrue())
	var responseError *azcore.ResponseError
	g.Expect(errors.As(err, &responseError)).To(BeTrue())
	g.Expect(responseError).To(BeIdenticalTo(lastPrecondition))
	g.Expect(err.Error()).To(ContainSubstring("attempts=3"))
	g.Expect(err.Error()).To(ContainSubstring("category=conditional race properties=desired states=pending"))
	g.Expect(err.Error()).To(ContainSubstring("category=precondition-failed status=412 code=PreconditionFailed"))
	g.Expect(err.Error()).ToNot(ContainSubstring("/subscriptions/"))
	g.Expect(err.Error()).ToNot(ContainSubstring("/operations/type/"))
	for _, marker := range markers {
		g.Expect(err.Error()).ToNot(ContainSubstring(marker))
		g.Expect(err.Error()).ToNot(ContainSubstring("raw-body-" + marker))
		g.Expect(err.Error()).ToNot(ContainSubstring("operation-" + marker))
	}
	getContexts, beginContexts, _ := client.snapshot()
	g.Expect(getContexts).To(HaveLen(6))
	g.Expect(beginContexts).To(HaveLen(3))
}

func testCreatePrivateDNSZoneLinkPreservesUnrelatedBeginError(t *testing.T) {
	g := NewGomegaWithT(t)
	desiredVNetID := "/subscriptions/example/resourceGroups/example/providers/Microsoft.Network/virtualNetworks/desired"
	notFound := testResponseError(http.StatusNotFound, "NotFound", ``)
	unrelatedConflict := testResponseError(http.StatusConflict, "Conflict", `{"error":{"code":"Conflict","message":"Another operation of type 'DeleteVirtualNetworkLink' is pending for the requested object."}}`)
	client := &fakeVirtualNetworkLinkClient{
		getActions:   []virtualNetworkLinkGetAction{getVirtualNetworkLinkError(notFound)},
		beginActions: []virtualNetworkLinkBeginAction{beginVirtualNetworkLink(nil, unrelatedConflict)},
	}

	err := createPrivateDNSZoneLink(context.Background(), client, "resource-group", "private-zone", "link", desiredVNetID, testPrivateDNSZoneLinkConfig(3))
	g.Expect(err).To(HaveOccurred())
	g.Expect(errors.Is(err, unrelatedConflict)).To(BeTrue())
	getContexts, beginContexts, _ := client.snapshot()
	g.Expect(getContexts).To(HaveLen(1))
	g.Expect(beginContexts).To(HaveLen(1))
}

func TestIsPendingVirtualNetworkLinkUpsert(t *testing.T) {
	tests := map[string]struct {
		err      func() error
		expected bool
	}{
		"When the structured conflict contains the exact sanitized incident response, it should match": {
			err: func() error {
				return fmt.Errorf("wrapped: %w", testResponseError(http.StatusConflict, "Conflict", pendingVirtualNetworkLinkUpsertBody))
			}, expected: true,
		},
		"When the structured conflict contains the observed operation-group grammar, it should match": {
			err: func() error {
				return testResponseError(http.StatusConflict, "Conflict", pendingVirtualNetworkLinkUpsertStructuredBody)
			}, expected: true,
		},
		"When whitespace and case vary within the associated clauses, it should match": {
			err: func() error {
				return testResponseError(http.StatusConflict, "conflict", `{"error":{"code":"CONFLICT","message":"ANOTHER   OPERATION IS PENDING FOR THE REQUESTED OBJECT. OPERATION GROUP '/OPERATIONS/GROUPS/ID/|VIRTUALNETWORKLINKS|subscription|resource-group|private-zone|link' ALREADY HAS 1 OPERATIONS LIKE '/operations/type/upsertvirtualnetworklink/id/redacted' QUEUED."}}`)
			}, expected: true,
		},
		"When a historical qualifier precedes the pending sentence, it should fail closed": {
			err: func() error {
				return testResponseError(http.StatusConflict, "Conflict", `{"error":{"code":"Conflict","message":"Historical example says another operation is pending for requested object. Scope already has 1 operations like '/operations/type/UpsertVirtualNetworkLink/id/example' queued."}}`)
			},
		},
		"When newline-delimited intervening prose precedes a historical queued Upsert, it should fail closed": {
			err: func() error {
				return testResponseError(http.StatusConflict, "Conflict", `{"error":{"code":"Conflict","message":"Another operation is pending for requested object.\nThis request queues DeleteVirtualNetworkLink\nHistorical example an object already has 1 operations like '/operations/type/UpsertVirtualNetworkLink/id/example' queued."}}`)
			},
		},
		"When the adjacent queued clause has a delimiter-free historical qualifier, it should fail closed": {
			err: func() error {
				return testResponseError(http.StatusConflict, "Conflict", `{"error":{"code":"Conflict","message":"Another operation is pending for requested object. Historical scope already has 1 operations like '/operations/type/UpsertVirtualNetworkLink/id/example' queued."}}`)
			},
		},
		"When the adjacent queued clause has a delimiter-free explanatory qualifier, it should fail closed": {
			err: func() error {
				return testResponseError(http.StatusConflict, "Conflict", `{"error":{"code":"Conflict","message":"Another operation is pending for requested object. For example scope already has 1 operations like '/operations/type/UpsertVirtualNetworkLink/id/example' queued."}}`)
			},
		},
		"When the response body is missing, it should fail closed": {
			err: func() error { return &azcore.ResponseError{StatusCode: http.StatusConflict, ErrorCode: "Conflict"} },
		},
		"When the raw response has no body, it should fail closed": {
			err: func() error {
				return &azcore.ResponseError{StatusCode: http.StatusConflict, ErrorCode: "Conflict", RawResponse: &http.Response{StatusCode: http.StatusConflict}}
			},
		},
		"When the response body is malformed, it should fail closed": {
			err: func() error { return testResponseError(http.StatusConflict, "Conflict", `{`) },
		},
		"When the nested error object is missing, it should fail closed": {
			err: func() error { return testResponseError(http.StatusConflict, "Conflict", `{}`) },
		},
		"When the nested message is missing, it should fail closed": {
			err: func() error {
				return testResponseError(http.StatusConflict, "Conflict", `{"error":{"code":"Conflict"}}`)
			},
		},
		"When the body code does not match, it should fail closed": {
			err: func() error {
				return testResponseError(http.StatusConflict, "Conflict", `{"error":{"code":"OtherConflict","message":"Another operation is pending for requested object. Scope already has 1 operations like '/operations/type/UpsertVirtualNetworkLink/id/redacted' queued."}}`)
			},
		},
		"When another operation type is queued, it should fail closed": {
			err: func() error {
				return testResponseError(http.StatusConflict, "Conflict", `{"error":{"code":"Conflict","message":"Another operation is pending for requested object. Scope already has 1 operations like '/operations/type/DeleteVirtualNetworkLink/id/redacted' queued."}}`)
			},
		},
		"When the Upsert marker is in a historical clause, it should fail closed": {
			err: func() error {
				return testResponseError(http.StatusConflict, "Conflict", `{"error":{"code":"Conflict","message":"Another operation is pending for requested object. A previous response mentioned '/operations/type/UpsertVirtualNetworkLink/id/historical' as completed. Scope already has 1 operations like '/operations/type/DeleteVirtualNetworkLink/id/redacted' queued."}}`)
			},
		},
		"When an unrelated clause precedes a historical queued Upsert clause, it should fail closed": {
			err: func() error {
				return testResponseError(http.StatusConflict, "Conflict", `{"error":{"code":"Conflict","message":"Another operation is pending for requested object. This request queues DeleteVirtualNetworkLink. Historical example: an object already has 1 operations like '/operations/type/UpsertVirtualNetworkLink/id/example' queued during a previous retry."}}`)
			},
		},
		"When the neighboring queued Upsert grammar is labeled as historical, it should fail closed": {
			err: func() error {
				return testResponseError(http.StatusConflict, "Conflict", `{"error":{"code":"Conflict","message":"Another operation is pending for requested object. Historical example: an object already has 1 operations like '/operations/type/UpsertVirtualNetworkLink/id/example' queued during a previous retry."}}`)
			},
		},
		"When neighboring queued Upsert grammar describes a previous retry, it should fail closed": {
			err: func() error {
				return testResponseError(http.StatusConflict, "Conflict", `{"error":{"code":"Conflict","message":"Another operation is pending for requested object. Scope already has 1 operations like '/operations/type/UpsertVirtualNetworkLink/id/example' queued during a previous retry."}}`)
			},
		},
		"When a semicolon separates an explanatory clause from queued Upsert grammar, it should fail closed": {
			err: func() error {
				return testResponseError(http.StatusConflict, "Conflict", `{"error":{"code":"Conflict","message":"Another operation is pending for requested object. Explanatory note; an object already has 1 operations like '/operations/type/UpsertVirtualNetworkLink/id/example' queued during a previous retry."}}`)
			},
		},
		"When the queued clause precedes the pending-object clause, it should fail closed": {
			err: func() error {
				return testResponseError(http.StatusConflict, "Conflict", `{"error":{"code":"Conflict","message":"Scope already has 1 operations like '/operations/type/UpsertVirtualNetworkLink/id/redacted' queued. Another operation is pending for requested object."}}`)
			},
		},
		"When the operation path is not described as queued, it should fail closed": {
			err: func() error {
				return testResponseError(http.StatusConflict, "Conflict", `{"error":{"code":"Conflict","message":"Another operation is pending for requested object. Scope already has 1 operations like '/operations/type/UpsertVirtualNetworkLink/id/redacted'."}}`)
			},
		},
		"When marker words are unassociated, it should fail closed": {
			err: func() error {
				return testResponseError(http.StatusConflict, "Conflict", `{"error":{"code":"Conflict","message":"Another operation is pending for requested object. UpsertVirtualNetworkLink is documented here. Different operations are queued."}}`)
			},
		},
		"When the HTTP status differs, it should fail closed": {
			err: func() error {
				return testResponseError(http.StatusPreconditionFailed, "Conflict", pendingVirtualNetworkLinkUpsertBody)
			},
		},
		"When the top level code differs, it should fail closed": {
			err: func() error {
				return testResponseError(http.StatusConflict, "OtherConflict", pendingVirtualNetworkLinkUpsertBody)
			},
		},
	}

	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			g := NewGomegaWithT(t)
			g.Expect(isPendingVirtualNetworkLinkUpsert(test.err())).To(Equal(test.expected))
		})
	}
}

func testCreatePrivateDNSZoneLinkPollErrorCancellationSkipsRecoveryGet(t *testing.T) {
	g := NewGomegaWithT(t)
	desiredVNetID := "/subscriptions/example/resourceGroups/example/providers/Microsoft.Network/virtualNetworks/desired"
	notFound := testResponseError(http.StatusNotFound, "NotFound", ``)
	pollErr := errors.New("poll failed")
	ctx, cancel := context.WithCancel(context.Background())
	poller := &fakeVirtualNetworkLinkPoller{
		err: pollErr,
		onPoll: func(context.Context) {
			cancel()
		},
	}
	client := &fakeVirtualNetworkLinkClient{
		getActions:   []virtualNetworkLinkGetAction{getVirtualNetworkLinkError(notFound)},
		beginActions: []virtualNetworkLinkBeginAction{beginVirtualNetworkLink(poller, nil)},
	}

	err := createPrivateDNSZoneLink(ctx, client, "resource-group", "private-zone", "link", desiredVNetID, testPrivateDNSZoneLinkConfig(2))
	g.Expect(err).To(HaveOccurred())
	g.Expect(errors.Is(err, context.Canceled)).To(BeTrue())
	g.Expect(errors.Is(err, pollErr)).To(BeTrue())
	getContexts, _, _ := client.snapshot()
	g.Expect(getContexts).To(HaveLen(1))
}

func testCreatePrivateDNSZoneLinkPollErrorDeadlineSkipsRecoveryGet(t *testing.T) {
	g := NewGomegaWithT(t)
	desiredVNetID := "/subscriptions/example/resourceGroups/example/providers/Microsoft.Network/virtualNetworks/desired"
	notFound := testResponseError(http.StatusNotFound, "NotFound", ``)
	pollErr := errors.New("poll failed")
	ctx := newManualErrorContext()
	poller := &fakeVirtualNetworkLinkPoller{
		err: pollErr,
		onPoll: func(context.Context) {
			ctx.cancel(context.DeadlineExceeded)
		},
	}
	client := &fakeVirtualNetworkLinkClient{
		getActions:   []virtualNetworkLinkGetAction{getVirtualNetworkLinkError(notFound)},
		beginActions: []virtualNetworkLinkBeginAction{beginVirtualNetworkLink(poller, nil)},
	}

	err := createPrivateDNSZoneLink(ctx, client, "resource-group", "private-zone", "link", desiredVNetID, testPrivateDNSZoneLinkConfig(2))
	g.Expect(err).To(HaveOccurred())
	g.Expect(errors.Is(err, context.DeadlineExceeded)).To(BeTrue())
	g.Expect(errors.Is(err, pollErr)).To(BeTrue())
	getContexts, _, _ := client.snapshot()
	g.Expect(getContexts).To(HaveLen(1))
}

func testCreatePrivateDNSZoneLinkPollErrorUsesOneConservativeRecoveryGet(t *testing.T) {
	desiredVNetID := "/subscriptions/example/resourceGroups/example/providers/Microsoft.Network/virtualNetworks/desired"
	succeeded := armprivatedns.ProvisioningStateSucceeded
	creating := armprivatedns.ProvisioningStateCreating
	failed := armprivatedns.ProvisioningStateFailed
	unknown := armprivatedns.ProvisioningState("FutureState")
	pollErr := errors.New("poll failed with sensitive-operation-marker")
	recoveryNotFound := testResponseError(http.StatusNotFound, "NotFound", ``)
	recoveryProviderErr := testResponseError(http.StatusInternalServerError, "InternalServerError", ``)
	tests := map[string]struct {
		recoveryAction virtualNetworkLinkGetAction
		recoveryCause  error
		wantError      bool
	}{
		"When recovery has one explicit success state, it should recover": {
			recoveryAction: getVirtualNetworkLink(testVirtualNetworkLink(desiredVNetID, ptr.To(false), &succeeded, nil)),
		},
		"When recovery has both states absent, it should preserve the poll error": {
			recoveryAction: getVirtualNetworkLink(testVirtualNetworkLink(desiredVNetID, ptr.To(false), nil, nil)), wantError: true,
		},
		"When recovery remains pending, it should preserve the poll error": {
			recoveryAction: getVirtualNetworkLink(testVirtualNetworkLink(desiredVNetID, ptr.To(false), &creating, nil)), wantError: true,
		},
		"When recovery reports failure, it should preserve the poll error": {
			recoveryAction: getVirtualNetworkLink(testVirtualNetworkLink(desiredVNetID, ptr.To(false), &failed, nil)), wantError: true,
		},
		"When recovery reports an unknown state, it should preserve the poll error": {
			recoveryAction: getVirtualNetworkLink(testVirtualNetworkLink(desiredVNetID, ptr.To(false), &unknown, nil)), wantError: true,
		},
		"When recovery finds incompatible properties, it should preserve the poll error": {
			recoveryAction: getVirtualNetworkLink(testVirtualNetworkLink("/subscriptions/example/resourceGroups/example/providers/Microsoft.Network/virtualNetworks/other", ptr.To(false), &succeeded, nil)), wantError: true,
		},
		"When recovery finds the target absent, it should preserve both causes": {
			recoveryAction: getVirtualNetworkLinkError(recoveryNotFound), recoveryCause: recoveryNotFound, wantError: true,
		},
		"When recovery GET fails, it should preserve both causes": {
			recoveryAction: getVirtualNetworkLinkError(recoveryProviderErr), recoveryCause: recoveryProviderErr, wantError: true,
		},
	}

	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			g := NewGomegaWithT(t)
			notFound := testResponseError(http.StatusNotFound, "NotFound", ``)
			poller := &fakeVirtualNetworkLinkPoller{err: pollErr}
			client := &fakeVirtualNetworkLinkClient{
				getActions: []virtualNetworkLinkGetAction{
					getVirtualNetworkLinkError(notFound),
					test.recoveryAction,
				},
				beginActions: []virtualNetworkLinkBeginAction{beginVirtualNetworkLink(poller, nil)},
			}

			err := createPrivateDNSZoneLink(context.Background(), client, "resource-group", "private-zone", "link", desiredVNetID, testPrivateDNSZoneLinkConfig(2))
			if test.wantError {
				g.Expect(err).To(HaveOccurred())
				g.Expect(errors.Is(err, pollErr)).To(BeTrue())
				g.Expect(err.Error()).ToNot(ContainSubstring("sensitive-operation-marker"))
				if test.recoveryCause != nil {
					g.Expect(errors.Is(err, test.recoveryCause)).To(BeTrue())
				}
			} else {
				g.Expect(err).ToNot(HaveOccurred())
			}
			getContexts, beginContexts, _ := client.snapshot()
			g.Expect(getContexts).To(HaveLen(2))
			g.Expect(beginContexts).To(HaveLen(1))
			g.Expect(poller.pollContexts()).To(HaveLen(1))
		})
	}
}

func testCreatePrivateDNSZoneLinkInFlightCancellation(t *testing.T) {
	desiredVNetID := "/subscriptions/example/resourceGroups/example/providers/Microsoft.Network/virtualNetworks/desired"
	tests := map[string]struct {
		client  func(chan struct{}) *fakeVirtualNetworkLinkClient
		getCall int
		begins  int
	}{
		"When an in-flight Get is canceled, it should stop without a Begin call": {
			client: func(started chan struct{}) *fakeVirtualNetworkLinkClient {
				return &fakeVirtualNetworkLinkClient{getActions: []virtualNetworkLinkGetAction{func(ctx context.Context) (armprivatedns.VirtualNetworkLink, error) {
					close(started)
					<-ctx.Done()
					return armprivatedns.VirtualNetworkLink{}, ctx.Err()
				}}}
			}, getCall: 1,
		},
		"When an in-flight Begin is canceled, it should stop without polling": {
			client: func(started chan struct{}) *fakeVirtualNetworkLinkClient {
				return &fakeVirtualNetworkLinkClient{
					getActions: []virtualNetworkLinkGetAction{getVirtualNetworkLinkError(testResponseError(http.StatusNotFound, "NotFound", ``))},
					beginActions: []virtualNetworkLinkBeginAction{func(ctx context.Context, _ *armprivatedns.VirtualNetworkLinksClientBeginCreateOrUpdateOptions) (virtualNetworkLinkPoller, error) {
						close(started)
						<-ctx.Done()
						return nil, ctx.Err()
					}},
				}
			}, getCall: 1, begins: 1,
		},
	}

	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			g := NewGomegaWithT(t)
			started := make(chan struct{})
			client := test.client(started)
			ctx, cancel := context.WithCancel(context.Background())
			result := make(chan error, 1)
			go func() {
				result <- createPrivateDNSZoneLink(ctx, client, "resource-group", "private-zone", "link", desiredVNetID, testPrivateDNSZoneLinkConfig(2))
			}()
			<-started
			cancel()
			err := <-result
			g.Expect(errors.Is(err, context.Canceled)).To(BeTrue())
			getContexts, beginContexts, _ := client.snapshot()
			g.Expect(getContexts).To(HaveLen(test.getCall))
			g.Expect(beginContexts).To(HaveLen(test.begins))
		})
	}
}

func testCreatePrivateDNSZoneLinkCancellationDuringBackoffStopsFurtherCalls(t *testing.T) {
	g := NewGomegaWithT(t)
	desiredVNetID := "/subscriptions/example/resourceGroups/example/providers/Microsoft.Network/virtualNetworks/desired"
	started := make(chan struct{})
	client := &fakeVirtualNetworkLinkClient{
		getActions: []virtualNetworkLinkGetAction{
			func(context.Context) (armprivatedns.VirtualNetworkLink, error) {
				close(started)
				return testVirtualNetworkLink(desiredVNetID, ptr.To(false), nil, nil), nil
			},
			getVirtualNetworkLink(testVirtualNetworkLink(desiredVNetID, ptr.To(false), nil, nil)),
		},
	}
	config := privateDNSZoneLinkWaitConfig{
		timeout: time.Hour,
		backoff: wait.Backoff{Duration: time.Hour, Steps: 2},
	}
	ctx, cancel := context.WithCancel(context.Background())
	result := make(chan error, 1)
	go func() {
		result <- createPrivateDNSZoneLink(ctx, client, "resource-group", "private-zone", "link", desiredVNetID, config)
	}()
	<-started
	cancel()
	err := <-result
	g.Expect(errors.Is(err, context.Canceled)).To(BeTrue())
	getContexts, beginContexts, _ := client.snapshot()
	g.Expect(getContexts).To(HaveLen(1))
	g.Expect(beginContexts).To(BeEmpty())
}

func testCreatePrivateDNSZoneLinkInternalDeadlineStopsFurtherCalls(t *testing.T) {
	g := NewGomegaWithT(t)
	desiredVNetID := "/subscriptions/example/resourceGroups/example/providers/Microsoft.Network/virtualNetworks/desired"
	started := make(chan struct{})
	client := &fakeVirtualNetworkLinkClient{
		getActions: []virtualNetworkLinkGetAction{func(ctx context.Context) (armprivatedns.VirtualNetworkLink, error) {
			close(started)
			<-ctx.Done()
			return armprivatedns.VirtualNetworkLink{}, ctx.Err()
		}},
	}
	config := privateDNSZoneLinkWaitConfig{
		timeout: 50 * time.Millisecond,
		backoff: wait.Backoff{Duration: time.Hour, Steps: 2},
	}
	parent := context.Background()
	result := make(chan error, 1)
	go func() {
		result <- createPrivateDNSZoneLink(parent, client, "resource-group", "private-zone", "link", desiredVNetID, config)
	}()
	<-started
	err := <-result
	g.Expect(err).To(HaveOccurred())
	g.Expect(errors.Is(err, context.DeadlineExceeded)).To(BeTrue())
	g.Expect(parent.Err()).To(BeNil())
	g.Expect(err.Error()).To(ContainSubstring("attempts=1"))
	g.Expect(err.Error()).To(ContainSubstring("category=target not yet observed"))
	getContexts, beginContexts, _ := client.snapshot()
	g.Expect(getContexts).To(HaveLen(1))
	g.Expect(beginContexts).To(BeEmpty())
}

func testCreatePrivateDNSZoneLinkSanitizesProviderErrors(t *testing.T) {
	desiredVNetID := "/subscriptions/example/resourceGroups/example/providers/Microsoft.Network/virtualNetworks/desired"
	assertSanitized := func(t *testing.T, err error, causes []error, markers ...string) {
		t.Helper()
		g := NewGomegaWithT(t)
		g.Expect(err).To(HaveOccurred())
		for _, cause := range causes {
			g.Expect(errors.Is(err, cause)).To(BeTrue())
		}
		for _, marker := range markers {
			g.Expect(err.Error()).ToNot(ContainSubstring(marker))
		}
		g.Expect(err.Error()).ToNot(ContainSubstring("/subscriptions/"))
		g.Expect(err.Error()).ToNot(ContainSubstring("/operations/type/"))
		var responseError *azcore.ResponseError
		g.Expect(errors.As(err, &responseError)).To(BeTrue())
	}

	t.Run("When preflight GET fails, it should redact provider response details", func(t *testing.T) {
		marker := "PREFLIGHT_GET_SENSITIVE_MARKER"
		cause := testSensitiveResponseError(t, http.StatusInternalServerError, "InternalServerError", marker)
		client := &fakeVirtualNetworkLinkClient{getActions: []virtualNetworkLinkGetAction{getVirtualNetworkLinkError(cause)}}
		err := createPrivateDNSZoneLink(context.Background(), client, "resource-group", "private-zone", "link", desiredVNetID, testPrivateDNSZoneLinkConfig(1))
		assertSanitized(t, err, []error{cause}, marker, "raw-body-"+marker, "operation-"+marker)
	})

	t.Run("When conditional Begin fails, it should redact provider response details", func(t *testing.T) {
		marker := "BEGIN_SENSITIVE_MARKER"
		cause := testSensitiveResponseError(t, http.StatusConflict, "Conflict", marker)
		client := &fakeVirtualNetworkLinkClient{
			getActions:   []virtualNetworkLinkGetAction{getVirtualNetworkLinkError(testResponseError(http.StatusNotFound, "NotFound", ``))},
			beginActions: []virtualNetworkLinkBeginAction{beginVirtualNetworkLink(nil, cause)},
		}
		err := createPrivateDNSZoneLink(context.Background(), client, "resource-group", "private-zone", "link", desiredVNetID, testPrivateDNSZoneLinkConfig(1))
		assertSanitized(t, err, []error{cause}, marker, "raw-body-"+marker, "operation-"+marker)
	})

	t.Run("When verification after a 412 fails, it should redact both provider responses and preserve both causes", func(t *testing.T) {
		conditionalMarker := "CONDITIONAL_SENSITIVE_MARKER"
		verificationMarker := "VERIFICATION_SENSITIVE_MARKER"
		conditionalCause := testSensitiveResponseError(t, http.StatusPreconditionFailed, "PreconditionFailed", conditionalMarker)
		verificationCause := testSensitiveResponseError(t, http.StatusInternalServerError, "InternalServerError", verificationMarker)
		client := &fakeVirtualNetworkLinkClient{
			getActions: []virtualNetworkLinkGetAction{
				getVirtualNetworkLinkError(testResponseError(http.StatusNotFound, "NotFound", ``)),
				getVirtualNetworkLinkError(verificationCause),
			},
			beginActions: []virtualNetworkLinkBeginAction{beginVirtualNetworkLink(nil, conditionalCause)},
		}
		err := createPrivateDNSZoneLink(context.Background(), client, "resource-group", "private-zone", "link", desiredVNetID, testPrivateDNSZoneLinkConfig(1))
		assertSanitized(t, err, []error{conditionalCause, verificationCause}, conditionalMarker, verificationMarker)
	})

	t.Run("When polling fails, it should redact the poll response and preserve its cause", func(t *testing.T) {
		marker := "POLL_SENSITIVE_MARKER"
		pollCause := testSensitiveResponseError(t, http.StatusInternalServerError, "InternalServerError", marker)
		poller := &fakeVirtualNetworkLinkPoller{err: pollCause}
		client := &fakeVirtualNetworkLinkClient{
			getActions: []virtualNetworkLinkGetAction{
				getVirtualNetworkLinkError(testResponseError(http.StatusNotFound, "NotFound", ``)),
				getVirtualNetworkLink(testVirtualNetworkLink(desiredVNetID, ptr.To(false), nil, nil)),
			},
			beginActions: []virtualNetworkLinkBeginAction{beginVirtualNetworkLink(poller, nil)},
		}
		err := createPrivateDNSZoneLink(context.Background(), client, "resource-group", "private-zone", "link", desiredVNetID, testPrivateDNSZoneLinkConfig(1))
		assertSanitized(t, err, []error{pollCause}, marker, "raw-body-"+marker, "operation-"+marker)
	})

	t.Run("When poll recovery GET fails, it should redact both failures and preserve both causes", func(t *testing.T) {
		marker := "POLL_RECOVERY_SENSITIVE_MARKER"
		pollCause := errors.New("poll-" + marker)
		recoveryCause := testSensitiveResponseError(t, http.StatusInternalServerError, "InternalServerError", marker)
		poller := &fakeVirtualNetworkLinkPoller{err: pollCause}
		client := &fakeVirtualNetworkLinkClient{
			getActions: []virtualNetworkLinkGetAction{
				getVirtualNetworkLinkError(testResponseError(http.StatusNotFound, "NotFound", ``)),
				getVirtualNetworkLinkError(recoveryCause),
			},
			beginActions: []virtualNetworkLinkBeginAction{beginVirtualNetworkLink(poller, nil)},
		}
		err := createPrivateDNSZoneLink(context.Background(), client, "resource-group", "private-zone", "link", desiredVNetID, testPrivateDNSZoneLinkConfig(1))
		assertSanitized(t, err, []error{pollCause, recoveryCause}, marker, "raw-body-"+marker, "operation-"+marker)
	})

	t.Run("When post-poll verification GET fails, it should redact provider response details", func(t *testing.T) {
		marker := "POST_POLL_SENSITIVE_MARKER"
		cause := testSensitiveResponseError(t, http.StatusInternalServerError, "InternalServerError", marker)
		poller := &fakeVirtualNetworkLinkPoller{result: armprivatedns.VirtualNetworkLink{}}
		client := &fakeVirtualNetworkLinkClient{
			getActions: []virtualNetworkLinkGetAction{
				getVirtualNetworkLinkError(testResponseError(http.StatusNotFound, "NotFound", ``)),
				getVirtualNetworkLinkError(cause),
			},
			beginActions: []virtualNetworkLinkBeginAction{beginVirtualNetworkLink(poller, nil)},
		}
		err := createPrivateDNSZoneLink(context.Background(), client, "resource-group", "private-zone", "link", desiredVNetID, testPrivateDNSZoneLinkConfig(1))
		assertSanitized(t, err, []error{cause}, marker, "raw-body-"+marker, "operation-"+marker)
	})
}
