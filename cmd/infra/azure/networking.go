package azure

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"regexp"
	"strings"
	"time"

	"github.com/openshift/hypershift/support/azureutil"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"
	azruntime "github.com/Azure/azure-sdk-for-go/sdk/azcore/runtime"
	"github.com/Azure/azure-sdk-for-go/sdk/azidentity"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/dns/armdns"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/network/armnetwork/v5"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/privatedns/armprivatedns"

	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/client-go/util/retry"
	"k8s.io/utils/ptr"

	ctrl "sigs.k8s.io/controller-runtime"
)

// NetworkManager handles Azure networking operations
type NetworkManager struct {
	subscriptionID string
	creds          azcore.TokenCredential
	cloud          string
}

// NewNetworkManager creates a new NetworkManager
func NewNetworkManager(subscriptionID string, creds azcore.TokenCredential, cloud string) *NetworkManager {
	return &NetworkManager{
		subscriptionID: subscriptionID,
		creds:          creds,
		cloud:          cloud,
	}
}

// GetBaseDomainID gets the resource group ID for the resource group containing the base domain
func (n *NetworkManager) GetBaseDomainID(ctx context.Context, baseDomain string) (string, error) {
	cloudConfig, err := azureutil.GetAzureCloudConfiguration(n.cloud)
	if err != nil {
		return "", fmt.Errorf("failed to get Azure cloud configuration: %w", err)
	}
	zonesClient, err := armdns.NewZonesClient(n.subscriptionID, n.creds, &arm.ClientOptions{ClientOptions: azcore.ClientOptions{Cloud: cloudConfig}})
	if err != nil {
		return "", fmt.Errorf("failed to create dns zone %s: %w", baseDomain, err)
	}

	pager := zonesClient.NewListPager(nil)
	if pager.More() {
		pagerResults, err := pager.NextPage(ctx)
		if err != nil {
			return "", fmt.Errorf("failed to retrieve list of DNS zones: %w", err)
		}

		for _, result := range pagerResults.Value {
			if *result.Name == baseDomain {
				return *result.ID, nil
			}
		}
	}
	return "", fmt.Errorf("could not find DNS zone '%s' in subscription; ensure a public DNS zone for the base domain exists in the subscription", baseDomain)
}

// CreateSecurityGroup creates the security group the virtual network will use
func (n *NetworkManager) CreateSecurityGroup(ctx context.Context, resourceGroupName string, name string, infraID string, location string) (string, error) {
	cloudConfig, err := azureutil.GetAzureCloudConfiguration(n.cloud)
	if err != nil {
		return "", fmt.Errorf("failed to get Azure cloud configuration: %w", err)
	}
	securityGroupClient, err := armnetwork.NewSecurityGroupsClient(n.subscriptionID, n.creds, &arm.ClientOptions{ClientOptions: azcore.ClientOptions{Cloud: cloudConfig}})
	if err != nil {
		return "", fmt.Errorf("failed to create security group client: %w", err)
	}

	securityGroupName := name + "-" + infraID + "-nsg"
	securityGroupFuture, err := securityGroupClient.BeginCreateOrUpdate(ctx, resourceGroupName, securityGroupName, armnetwork.SecurityGroup{Location: &location}, nil)
	if err != nil {
		return "", fmt.Errorf("failed to create network security group: %w", err)
	}
	securityGroup, err := securityGroupFuture.PollUntilDone(ctx, nil)
	if err != nil {
		return "", fmt.Errorf("failed to get network security group creation result: %w", err)
	}

	return *securityGroup.ID, nil
}

// NewVirtualNetwork creates a VirtualNetwork struct with the given address prefix.
// It initializes an empty virtual network with the specified location and address space,
// ready to have subnets added to it.
//
// Parameters:
//   - location: Azure region where the virtual network will be created (e.g., "eastus")
//   - vnetAddrPrefix: CIDR notation for the virtual network address space (e.g., "10.0.0.0/16")
//
// Returns an armnetwork.VirtualNetwork with an empty Subnets slice that can be populated later.
func NewVirtualNetwork(location string, vnetAddrPrefix string) armnetwork.VirtualNetwork {
	return armnetwork.VirtualNetwork{
		Location: &location,
		Properties: &armnetwork.VirtualNetworkPropertiesFormat{
			AddressSpace: &armnetwork.AddressSpace{
				AddressPrefixes: []*string{
					ptr.To(vnetAddrPrefix),
				},
			},
			Subnets: []*armnetwork.Subnet{},
		},
	}
}

// CreateVirtualNetwork creates the virtual network
func (n *NetworkManager) CreateVirtualNetwork(ctx context.Context, resourceGroupName string, name string, infraID string, location string, subnetID string, securityGroupID string) (armnetwork.VirtualNetworksClientCreateOrUpdateResponse, error) {
	l := ctrl.LoggerFrom(ctx)

	cloudConfig, err := azureutil.GetAzureCloudConfiguration(n.cloud)
	if err != nil {
		return armnetwork.VirtualNetworksClientCreateOrUpdateResponse{}, fmt.Errorf("failed to get Azure cloud configuration: %w", err)
	}
	networksClient, err := armnetwork.NewVirtualNetworksClient(n.subscriptionID, n.creds, &arm.ClientOptions{ClientOptions: azcore.ClientOptions{Cloud: cloudConfig}})
	if err != nil {
		return armnetwork.VirtualNetworksClientCreateOrUpdateResponse{}, fmt.Errorf("failed to create new virtual networks client: %w", err)
	}

	vnetToCreate := NewVirtualNetwork(location, VirtualNetworkAddressPrefix)

	if len(subnetID) > 0 {
		vnetToCreate.Properties.Subnets = append(vnetToCreate.Properties.Subnets, &armnetwork.Subnet{ID: ptr.To(subnetID)})
		l.Info("Using existing subnet in vnet creation", "ID", subnetID)
	} else {
		vnetToCreate.Properties.Subnets = append(vnetToCreate.Properties.Subnets, &armnetwork.Subnet{
			Name: ptr.To("default"),
			Properties: &armnetwork.SubnetPropertiesFormat{
				AddressPrefix: ptr.To(VirtualNetworkSubnetAddressPrefix),
				NetworkSecurityGroup: &armnetwork.SecurityGroup{
					ID: ptr.To(securityGroupID),
				},
			},
		})
		l.Info("Creating new subnet for vnet creation")
	}

	vnetName := name + "-" + infraID
	vnetFuture, err := networksClient.BeginCreateOrUpdate(ctx, resourceGroupName, vnetName, vnetToCreate, nil)
	if err != nil {
		return armnetwork.VirtualNetworksClientCreateOrUpdateResponse{}, fmt.Errorf("failed to create vnet: %w", err)
	}
	vnet, err := vnetFuture.PollUntilDone(ctx, nil)
	if err != nil {
		return armnetwork.VirtualNetworksClientCreateOrUpdateResponse{}, fmt.Errorf("failed to wait for vnet creation: %w", err)
	}

	if vnet.ID == nil || vnet.Name == nil {
		return armnetwork.VirtualNetworksClientCreateOrUpdateResponse{}, fmt.Errorf("created vnet has no ID or name")
	}

	if len(vnet.Properties.Subnets) < 1 {
		return armnetwork.VirtualNetworksClientCreateOrUpdateResponse{}, fmt.Errorf("created vnet has no subnets: %+v", vnet)
	}

	if vnet.Properties.Subnets[0].ID == nil || vnet.Properties.Subnets[0].Name == nil {
		return armnetwork.VirtualNetworksClientCreateOrUpdateResponse{}, fmt.Errorf("created vnet has no subnet ID or name")
	}

	return vnet, nil
}

// CreatePrivateDNSZone creates the private DNS zone
func (n *NetworkManager) CreatePrivateDNSZone(ctx context.Context, resourceGroupName string, name string, baseDomain string) (string, string, error) {
	cloudConfig, err := azureutil.GetAzureCloudConfiguration(n.cloud)
	if err != nil {
		return "", "", fmt.Errorf("failed to get Azure cloud configuration: %w", err)
	}
	privateZoneClient, err := armprivatedns.NewPrivateZonesClient(n.subscriptionID, n.creds, &arm.ClientOptions{ClientOptions: azcore.ClientOptions{Cloud: cloudConfig}})
	if err != nil {
		return "", "", fmt.Errorf("failed to create new private zones client: %w", err)
	}
	privateZoneParams := armprivatedns.PrivateZone{
		Location: ptr.To("global"),
	}
	privateDNSZonePromise, err := privateZoneClient.BeginCreateOrUpdate(ctx, resourceGroupName, name+"."+baseDomain, privateZoneParams, nil)
	if err != nil {
		return "", "", fmt.Errorf("failed to create private DNS zone: %w", err)
	}
	privateDNSZone, err := privateDNSZonePromise.PollUntilDone(ctx, nil)
	if err != nil {
		return "", "", fmt.Errorf("failed waiting for private DNS zone completion: %w", err)
	}

	return *privateDNSZone.ID, *privateDNSZone.Name, nil
}

// NewVirtualNetworkLink creates a VirtualNetworkLink struct for linking a VNet to a Private DNS Zone.
// This allows resources in the virtual network to resolve DNS records from the private DNS zone.
//
// Parameters:
//   - location: Azure region, typically "global" for private DNS zone links
//   - vnetID: Full resource ID of the virtual network to link (e.g., "/subscriptions/.../virtualNetworks/...")
//   - registrationEnabled: If true, enables automatic DNS record registration for VMs in the VNet
//
// Returns an armprivatedns.VirtualNetworkLink ready to be created via the Azure API.
func NewVirtualNetworkLink(location string, vnetID string, registrationEnabled bool) armprivatedns.VirtualNetworkLink {
	return armprivatedns.VirtualNetworkLink{
		Location: ptr.To(location),
		Properties: &armprivatedns.VirtualNetworkLinkProperties{
			VirtualNetwork:      &armprivatedns.SubResource{ID: ptr.To(vnetID)},
			RegistrationEnabled: ptr.To(registrationEnabled),
		},
	}
}

const privateDNSZoneLinkConvergenceTimeout = 2 * time.Minute

var (
	// Match only the observed adjacent operation-group clause, plus the literal ellipsis
	// used by the sanitized Jira evidence. The pipe-delimited fields are bounded path
	// segments, not an arbitrary prose bridge.
	pendingVirtualNetworkLinkUpsertPattern = regexp.MustCompile(`(?i)^another operation is pending for (?:the )?requested object\. (?:\.\.\.|operation group '/operations/groups/id/\|virtualnetworklinks\|[^|']+\|[^|']+\|[^|']+\|[^|']+') already has 1 operations like '/operations/type/upsertvirtualnetworklink/id/[^']+' queued\.$`)
	safeAzureErrorCodePattern              = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9._-]{0,63}$`)
)

type virtualNetworkLinkPoller interface {
	PollUntilDone(context.Context) (armprivatedns.VirtualNetworkLink, error)
}

type virtualNetworkLinkClient interface {
	Get(context.Context, string, string, string) (armprivatedns.VirtualNetworkLink, error)
	BeginCreateOrUpdate(context.Context, string, string, string, armprivatedns.VirtualNetworkLink, *armprivatedns.VirtualNetworkLinksClientBeginCreateOrUpdateOptions) (virtualNetworkLinkPoller, error)
}

type azureVirtualNetworkLinkClient struct {
	client *armprivatedns.VirtualNetworkLinksClient
}

func (c *azureVirtualNetworkLinkClient) Get(ctx context.Context, resourceGroupName, privateDNSZoneName, linkName string) (armprivatedns.VirtualNetworkLink, error) {
	response, err := c.client.Get(ctx, resourceGroupName, privateDNSZoneName, linkName, nil)
	return response.VirtualNetworkLink, err
}

func (c *azureVirtualNetworkLinkClient) BeginCreateOrUpdate(ctx context.Context, resourceGroupName, privateDNSZoneName, linkName string, link armprivatedns.VirtualNetworkLink, options *armprivatedns.VirtualNetworkLinksClientBeginCreateOrUpdateOptions) (virtualNetworkLinkPoller, error) {
	poller, err := c.client.BeginCreateOrUpdate(ctx, resourceGroupName, privateDNSZoneName, linkName, link, options)
	if err != nil {
		return nil, err
	}
	return &azureVirtualNetworkLinkPoller{poller: poller}, nil
}

type azureVirtualNetworkLinkPoller struct {
	poller *azruntime.Poller[armprivatedns.VirtualNetworkLinksClientCreateOrUpdateResponse]
}

func (p *azureVirtualNetworkLinkPoller) PollUntilDone(ctx context.Context) (armprivatedns.VirtualNetworkLink, error) {
	response, err := p.poller.PollUntilDone(ctx, nil)
	return response.VirtualNetworkLink, err
}

type privateDNSZoneLinkWaitConfig struct {
	timeout time.Duration
	backoff wait.Backoff
}

func defaultPrivateDNSZoneLinkWaitConfig() privateDNSZoneLinkWaitConfig {
	return privateDNSZoneLinkWaitConfig{
		timeout: privateDNSZoneLinkConvergenceTimeout,
		backoff: wait.Backoff{
			Duration: 2 * time.Second,
			Factor:   2,
			Jitter:   0.1,
			Steps:    7,
			Cap:      30 * time.Second,
		},
	}
}

type virtualNetworkLinkCompletionAuthority int

const (
	virtualNetworkLinkRecoveryRead virtualNetworkLinkCompletionAuthority = iota
	virtualNetworkLinkSuccessfulPoller
)

type virtualNetworkLinkPropertyState string

const (
	virtualNetworkLinkPropertiesDesired      virtualNetworkLinkPropertyState = "desired"
	virtualNetworkLinkPropertiesMissing      virtualNetworkLinkPropertyState = "missing"
	virtualNetworkLinkPropertiesIncompatible virtualNetworkLinkPropertyState = "incompatible"
)

type virtualNetworkLinkStateStatus string

const (
	virtualNetworkLinkStatesAbsent  virtualNetworkLinkStateStatus = "absent"
	virtualNetworkLinkStatesSuccess virtualNetworkLinkStateStatus = "success"
	virtualNetworkLinkStatesPending virtualNetworkLinkStateStatus = "pending"
	virtualNetworkLinkStatesFailure virtualNetworkLinkStateStatus = "failure"
	virtualNetworkLinkStatesUnknown virtualNetworkLinkStateStatus = "unknown"
)

type virtualNetworkLinkAssessment struct {
	properties virtualNetworkLinkPropertyState
	states     virtualNetworkLinkStateStatus
	reason     string
}

type sanitizedVirtualNetworkLinkCause struct {
	phase      string
	category   string
	statusCode int
	errorCode  string
	cause      error
}

func (e *sanitizedVirtualNetworkLinkCause) Error() string {
	if e.statusCode == 0 {
		return fmt.Sprintf("virtual network link provider error phase=%s category=%s", e.phase, e.category)
	}
	return fmt.Sprintf("virtual network link provider error phase=%s category=%s status=%d code=%s", e.phase, e.category, e.statusCode, e.errorCode)
}

func (e *sanitizedVirtualNetworkLinkCause) Unwrap() error {
	return e.cause
}

func sanitizeVirtualNetworkLinkCause(phase, category string, cause error) error {
	if cause == nil {
		return nil
	}

	sanitized := &sanitizedVirtualNetworkLinkCause{
		phase:    phase,
		category: category,
		cause:    cause,
	}
	var responseError *azcore.ResponseError
	if errors.As(cause, &responseError) {
		sanitized.statusCode = responseError.StatusCode
		sanitized.errorCode = "unavailable"
		if safeAzureErrorCodePattern.MatchString(responseError.ErrorCode) {
			sanitized.errorCode = responseError.ErrorCode
		}
		return sanitized
	}

	var authenticationError *azidentity.AuthenticationFailedError
	var transportError net.Error
	switch {
	case errors.Is(cause, context.DeadlineExceeded):
		sanitized.category += "/deadline-exceeded"
	case errors.Is(cause, context.Canceled):
		sanitized.category += "/canceled"
	case errors.As(cause, &authenticationError):
		sanitized.category += "/authentication"
	case errors.As(cause, &transportError):
		sanitized.category += "/transport"
	default:
		sanitized.category += "/unknown"
	}
	return sanitized
}

func assessVirtualNetworkLink(link armprivatedns.VirtualNetworkLink, desiredVNetID string) virtualNetworkLinkAssessment {
	assessment := virtualNetworkLinkAssessment{properties: virtualNetworkLinkPropertiesDesired}
	if link.Properties == nil {
		assessment.properties = virtualNetworkLinkPropertiesMissing
		assessment.reason = "desired properties are missing"
	} else {
		vnetIDMissing := link.Properties.VirtualNetwork == nil || link.Properties.VirtualNetwork.ID == nil
		registrationMissing := link.Properties.RegistrationEnabled == nil
		switch {
		case !vnetIDMissing && !strings.EqualFold(*link.Properties.VirtualNetwork.ID, desiredVNetID):
			assessment.properties = virtualNetworkLinkPropertiesIncompatible
			assessment.reason = "virtual network does not match"
		case !registrationMissing && *link.Properties.RegistrationEnabled:
			assessment.properties = virtualNetworkLinkPropertiesIncompatible
			assessment.reason = "registration is enabled"
		case vnetIDMissing || registrationMissing:
			assessment.properties = virtualNetworkLinkPropertiesMissing
			assessment.reason = "desired properties are missing"
		}
	}

	assessment.states = assessVirtualNetworkLinkStates(link.Properties)
	return assessment
}

func assessVirtualNetworkLinkStates(properties *armprivatedns.VirtualNetworkLinkProperties) virtualNetworkLinkStateStatus {
	if properties == nil {
		return virtualNetworkLinkStatesAbsent
	}

	if properties.ProvisioningState != nil {
		switch *properties.ProvisioningState {
		case armprivatedns.ProvisioningStateFailed, armprivatedns.ProvisioningStateCanceled, armprivatedns.ProvisioningStateDeleting:
			return virtualNetworkLinkStatesFailure
		case armprivatedns.ProvisioningStateCreating, armprivatedns.ProvisioningStateUpdating, armprivatedns.ProvisioningStateSucceeded:
		default:
			return virtualNetworkLinkStatesUnknown
		}
	}

	if properties.VirtualNetworkLinkState != nil {
		switch *properties.VirtualNetworkLinkState {
		case armprivatedns.VirtualNetworkLinkStateInProgress, armprivatedns.VirtualNetworkLinkStateCompleted:
		default:
			return virtualNetworkLinkStatesUnknown
		}
	}

	if properties.ProvisioningState != nil && (*properties.ProvisioningState == armprivatedns.ProvisioningStateCreating || *properties.ProvisioningState == armprivatedns.ProvisioningStateUpdating) {
		return virtualNetworkLinkStatesPending
	}
	if properties.VirtualNetworkLinkState != nil && *properties.VirtualNetworkLinkState == armprivatedns.VirtualNetworkLinkStateInProgress {
		return virtualNetworkLinkStatesPending
	}
	if properties.ProvisioningState != nil && *properties.ProvisioningState == armprivatedns.ProvisioningStateSucceeded {
		return virtualNetworkLinkStatesSuccess
	}
	if properties.VirtualNetworkLinkState != nil && *properties.VirtualNetworkLinkState == armprivatedns.VirtualNetworkLinkStateCompleted {
		return virtualNetworkLinkStatesSuccess
	}
	return virtualNetworkLinkStatesAbsent
}

func evaluateVirtualNetworkLink(assessment virtualNetworkLinkAssessment, authority virtualNetworkLinkCompletionAuthority) (complete bool, retry bool, err error) {
	if assessment.properties == virtualNetworkLinkPropertiesIncompatible {
		return false, false, fmt.Errorf("virtual network link has incompatible desired properties: %s", assessment.reason)
	}
	if assessment.states == virtualNetworkLinkStatesFailure {
		return false, false, fmt.Errorf("virtual network link reports a failure state")
	}
	if assessment.states == virtualNetworkLinkStatesUnknown {
		return false, false, fmt.Errorf("virtual network link reports an unknown state")
	}
	if assessment.properties == virtualNetworkLinkPropertiesMissing || assessment.states == virtualNetworkLinkStatesPending {
		return false, true, nil
	}
	if authority == virtualNetworkLinkSuccessfulPoller {
		// A nil PollUntilDone error is the completion authority. Azure models both
		// state fields as optional, so their absence does not contradict the poller.
		return true, false, nil
	}
	if assessment.states == virtualNetworkLinkStatesSuccess {
		// This is a conservative HyperShift recovery policy for operations without
		// a poller, not an Azure guarantee: require at least one explicit success.
		return true, false, nil
	}
	return false, true, nil
}

func isResponseStatus(err error, statusCode int) bool {
	var responseError *azcore.ResponseError
	return errors.As(err, &responseError) && responseError.StatusCode == statusCode
}

func isPendingVirtualNetworkLinkUpsert(err error) bool {
	var responseError *azcore.ResponseError
	if !errors.As(err, &responseError) || responseError.StatusCode != http.StatusConflict || !strings.EqualFold(responseError.ErrorCode, "Conflict") || responseError.RawResponse == nil {
		return false
	}

	payload, payloadErr := azruntime.Payload(responseError.RawResponse)
	if payloadErr != nil || len(payload) == 0 {
		return false
	}
	var body struct {
		Error struct {
			Code    string `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}
	if json.Unmarshal(payload, &body) != nil || !strings.EqualFold(body.Error.Code, "Conflict") || body.Error.Message == "" {
		return false
	}

	if strings.ContainsAny(body.Error.Message, "\r\n") {
		return false
	}
	normalizedMessage := strings.Join(strings.Fields(body.Error.Message), " ")
	return pendingVirtualNetworkLinkUpsertPattern.MatchString(normalizedMessage)
}

func cappedExponentialBackoffWithContext(ctx context.Context, backoff wait.Backoff, condition wait.ConditionWithContextFunc) error {
	delay := backoff.Duration
	for step := 0; step < backoff.Steps; step++ {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		complete, err := condition(ctx)
		if err != nil || complete {
			return err
		}
		if step == backoff.Steps-1 {
			break
		}

		waitFor := delay
		if backoff.Jitter > 0 {
			waitFor = wait.Jitter(waitFor, backoff.Jitter)
		}
		timer := time.NewTimer(waitFor)
		select {
		case <-ctx.Done():
			if !timer.Stop() {
				select {
				case <-timer.C:
				default:
				}
			}
			return ctx.Err()
		case <-timer.C:
		}

		if backoff.Factor != 0 {
			delay = time.Duration(float64(delay) * backoff.Factor)
		}
		if backoff.Cap > 0 && delay > backoff.Cap {
			delay = backoff.Cap
		}
	}
	return wait.ErrorInterrupted(nil)
}

func createPrivateDNSZoneLink(ctx context.Context, client virtualNetworkLinkClient, resourceGroupName, privateDNSZoneName, linkName, vnetID string, config privateDNSZoneLinkWaitConfig) error {
	desiredLink := NewVirtualNetworkLink(VirtualNetworkLinkLocation, vnetID, false)
	poller, complete, err := convergePrivateDNSZoneLink(ctx, client, resourceGroupName, privateDNSZoneName, linkName, vnetID, desiredLink, config)
	if err != nil {
		return fmt.Errorf("failed to set up network link for private DNS zone: %w", err)
	}
	if complete {
		return nil
	}

	pollResult, err := poller.PollUntilDone(ctx)
	if err != nil {
		if contextErr := ctx.Err(); contextErr != nil {
			sanitizedPollErr := sanitizeVirtualNetworkLinkCause("poll", "provider", err)
			return fmt.Errorf("failed waiting for network link for private DNS zone: %w", errors.Join(sanitizedPollErr, contextErr))
		}
		return recoverPrivateDNSZoneLinkAfterPollError(ctx, client, resourceGroupName, privateDNSZoneName, linkName, vnetID, err, config.timeout)
	}

	assessment := assessVirtualNetworkLink(pollResult, vnetID)
	complete, retry, assessmentErr := evaluateVirtualNetworkLink(assessment, virtualNetworkLinkSuccessfulPoller)
	if assessmentErr != nil {
		return fmt.Errorf("failed validating completed network link for private DNS zone: %w", assessmentErr)
	}
	if complete {
		return nil
	}
	if !retry {
		return fmt.Errorf("completed network link for private DNS zone is not acceptable")
	}
	return waitForExistingPrivateDNSZoneLink(ctx, client, resourceGroupName, privateDNSZoneName, linkName, vnetID, virtualNetworkLinkSuccessfulPoller, config)
}

func convergePrivateDNSZoneLink(ctx context.Context, client virtualNetworkLinkClient, resourceGroupName, privateDNSZoneName, linkName, vnetID string, desiredLink armprivatedns.VirtualNetworkLink, config privateDNSZoneLinkWaitConfig) (virtualNetworkLinkPoller, bool, error) {
	convergenceCtx, cancel := context.WithTimeout(ctx, config.timeout)
	defer cancel()

	var poller virtualNetworkLinkPoller
	completed := false
	lastObservation := "target not yet observed"
	var lastRetryableProviderErr error
	var lastConditionalRaceErr error
	attempts := 0
	err := cappedExponentialBackoffWithContext(convergenceCtx, config.backoff, func(callCtx context.Context) (bool, error) {
		attempts++
		link, getErr := client.Get(callCtx, resourceGroupName, privateDNSZoneName, linkName)
		if getErr == nil {
			assessment := assessVirtualNetworkLink(link, vnetID)
			lastObservation = fmt.Sprintf("properties=%s states=%s", assessment.properties, assessment.states)
			complete, retry, assessmentErr := evaluateVirtualNetworkLink(assessment, virtualNetworkLinkRecoveryRead)
			if assessmentErr != nil {
				return false, assessmentErr
			}
			if complete {
				completed = true
				return true, nil
			}
			return !retry, nil
		}
		if !isResponseStatus(getErr, http.StatusNotFound) {
			return false, sanitizeVirtualNetworkLinkCause("convergence-get", "provider", getErr)
		}

		poller, getErr = client.BeginCreateOrUpdate(callCtx, resourceGroupName, privateDNSZoneName, linkName, desiredLink, &armprivatedns.VirtualNetworkLinksClientBeginCreateOrUpdateOptions{IfNoneMatch: ptr.To("*")})
		if getErr == nil {
			if poller == nil {
				return false, fmt.Errorf("conditional network link create returned no poller")
			}
			return true, nil
		}
		if isPendingVirtualNetworkLinkUpsert(getErr) {
			lastObservation = "pending virtual network link upsert"
			lastRetryableProviderErr = getErr
			return false, nil
		}
		if !isResponseStatus(getErr, http.StatusPreconditionFailed) {
			return false, sanitizeVirtualNetworkLinkCause("conditional-create", "provider", getErr)
		}

		conditionalErr := sanitizeVirtualNetworkLinkCause("conditional-create", "precondition-failed", getErr)
		link, getErr = client.Get(callCtx, resourceGroupName, privateDNSZoneName, linkName)
		if getErr != nil {
			if isResponseStatus(getErr, http.StatusNotFound) {
				return false, fmt.Errorf("conditional create failed and network link remained absent: %w", conditionalErr)
			}
			verificationErr := sanitizeVirtualNetworkLinkCause("conditional-verification-get", "provider", getErr)
			return false, fmt.Errorf("conditional create and verification failed: %w", errors.Join(conditionalErr, verificationErr))
		}
		assessment := assessVirtualNetworkLink(link, vnetID)
		lastObservation = fmt.Sprintf("conditional race properties=%s states=%s", assessment.properties, assessment.states)
		complete, retry, assessmentErr := evaluateVirtualNetworkLink(assessment, virtualNetworkLinkRecoveryRead)
		if assessmentErr != nil {
			return false, fmt.Errorf("conditional create found an incompatible network link: %w", errors.Join(conditionalErr, assessmentErr))
		}
		if complete {
			completed = true
			return true, nil
		}
		if retry {
			// The conditional create lost a race to a compatible but still-pending
			// link. Retain the sanitized 412 cause so that, if continued observation
			// later exhausts the retry budget or hits the deadline, the final error
			// still preserves the original precondition failure for errors.Is/errors.As.
			lastConditionalRaceErr = conditionalErr
		}
		return !retry, nil
	})
	if err != nil {
		if lastRetryableProviderErr != nil {
			err = errors.Join(err, sanitizeVirtualNetworkLinkCause("conditional-create", "pending-upsert", lastRetryableProviderErr))
		}
		if lastConditionalRaceErr != nil {
			err = errors.Join(err, lastConditionalRaceErr)
		}
		return nil, false, fmt.Errorf("network link did not converge (attempts=%d category=%s): %w", attempts, lastObservation, err)
	}
	return poller, completed, nil
}

func waitForExistingPrivateDNSZoneLink(ctx context.Context, client virtualNetworkLinkClient, resourceGroupName, privateDNSZoneName, linkName, vnetID string, authority virtualNetworkLinkCompletionAuthority, config privateDNSZoneLinkWaitConfig) error {
	verificationCtx, cancel := context.WithTimeout(ctx, config.timeout)
	defer cancel()

	lastObservation := "target not yet observed"
	attempts := 0
	err := cappedExponentialBackoffWithContext(verificationCtx, config.backoff, func(callCtx context.Context) (bool, error) {
		attempts++
		link, getErr := client.Get(callCtx, resourceGroupName, privateDNSZoneName, linkName)
		if getErr != nil {
			if isResponseStatus(getErr, http.StatusNotFound) {
				lastObservation = "target absent"
				return false, nil
			}
			return false, sanitizeVirtualNetworkLinkCause("post-poll-verification-get", "provider", getErr)
		}
		assessment := assessVirtualNetworkLink(link, vnetID)
		lastObservation = fmt.Sprintf("properties=%s states=%s", assessment.properties, assessment.states)
		complete, retry, assessmentErr := evaluateVirtualNetworkLink(assessment, authority)
		if assessmentErr != nil {
			return false, assessmentErr
		}
		return complete || !retry, nil
	})
	if err != nil {
		return fmt.Errorf("network link verification did not converge (attempts=%d category=%s): %w", attempts, lastObservation, err)
	}
	return nil
}

func recoverPrivateDNSZoneLinkAfterPollError(ctx context.Context, client virtualNetworkLinkClient, resourceGroupName, privateDNSZoneName, linkName, vnetID string, pollErr error, timeout time.Duration) error {
	verificationCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	link, getErr := client.Get(verificationCtx, resourceGroupName, privateDNSZoneName, linkName)
	sanitizedPollErr := sanitizeVirtualNetworkLinkCause("poll", "provider", pollErr)
	if getErr != nil {
		sanitizedGetErr := sanitizeVirtualNetworkLinkCause("poll-recovery-get", "provider", getErr)
		return fmt.Errorf("failed waiting for network link and final verification failed: %w", errors.Join(sanitizedPollErr, sanitizedGetErr))
	}
	assessment := assessVirtualNetworkLink(link, vnetID)
	complete, _, assessmentErr := evaluateVirtualNetworkLink(assessment, virtualNetworkLinkRecoveryRead)
	if complete {
		return nil
	}
	if assessmentErr != nil {
		return fmt.Errorf("failed waiting for network link; final verification rejected properties=%s states=%s: %w", assessment.properties, assessment.states, errors.Join(sanitizedPollErr, assessmentErr))
	}
	return fmt.Errorf("failed waiting for network link; final verification was inconclusive properties=%s states=%s: %w", assessment.properties, assessment.states, sanitizedPollErr)
}

// CreatePrivateDNSZoneLink creates the private DNS Zone network link.
// It returns successfully for an existing compatible link only when completion is
// established by the applicable poller or conservative recovery-state policy.
func (n *NetworkManager) CreatePrivateDNSZoneLink(ctx context.Context, resourceGroupName string, name string, infraID string, vnetID string, privateDNSZoneName string) error {
	cloudConfig, err := azureutil.GetAzureCloudConfiguration(n.cloud)
	if err != nil {
		return fmt.Errorf("failed to get Azure cloud configuration: %w", err)
	}
	privateZoneLinkClient, err := armprivatedns.NewVirtualNetworkLinksClient(n.subscriptionID, n.creds, &arm.ClientOptions{ClientOptions: azcore.ClientOptions{Cloud: cloudConfig}})
	if err != nil {
		return fmt.Errorf("failed to create new virtual network links client: %w", err)
	}

	linkName := name + "-" + infraID
	return createPrivateDNSZoneLink(ctx, &azureVirtualNetworkLinkClient{client: privateZoneLinkClient}, resourceGroupName, privateDNSZoneName, linkName, vnetID, defaultPrivateDNSZoneLinkWaitConfig())
}

// NewPublicIPAddress creates a PublicIPAddress struct configured for use with a load balancer.
// The IP address is configured as a static IPv4 address with the Standard SKU, suitable for
// production load balancers that require consistent, non-changing IP addresses.
//
// Parameters:
//   - name: Name for the public IP address resource
//   - location: Azure region where the IP address will be allocated (e.g., "eastus")
//
// Returns an armnetwork.PublicIPAddress with:
//   - Static allocation method (IP doesn't change)
//   - IPv4 address version
//   - Standard SKU (required for Standard Load Balancers)
//   - 4-minute idle timeout
func NewPublicIPAddress(name string, location string) armnetwork.PublicIPAddress {
	return armnetwork.PublicIPAddress{
		Name:     ptr.To(name),
		Location: ptr.To(location),
		Properties: &armnetwork.PublicIPAddressPropertiesFormat{
			PublicIPAddressVersion:   ptr.To(armnetwork.IPVersionIPv4),
			PublicIPAllocationMethod: ptr.To(armnetwork.IPAllocationMethodStatic),
			IdleTimeoutInMinutes:     ptr.To[int32](4),
		},
		SKU: &armnetwork.PublicIPAddressSKU{
			Name: ptr.To(armnetwork.PublicIPAddressSKUNameStandard),
		},
	}
}

// isAzureConflictError returns true for Azure 409 Conflict errors that the SDK's
// default retry policy does not cover (the SDK already retries 408/429/500/502/503/504).
func isAzureConflictError(err error) bool {
	if err == nil {
		return false
	}
	var respErr *azcore.ResponseError
	if !errors.As(err, &respErr) {
		return false
	}
	return respErr.StatusCode == http.StatusConflict
}

// CreatePublicIPAddressForLB creates a public IP address to use for the outbound rule in the load balancer
func (n *NetworkManager) CreatePublicIPAddressForLB(ctx context.Context, resourceGroupName string, infraID string, location string) (*armnetwork.PublicIPAddress, error) {
	log := ctrl.LoggerFrom(ctx)
	cloudConfig, err := azureutil.GetAzureCloudConfiguration(n.cloud)
	if err != nil {
		return nil, fmt.Errorf("failed to get Azure cloud configuration: %w", err)
	}
	publicIPAddressClient, err := armnetwork.NewPublicIPAddressesClient(n.subscriptionID, n.creds, &arm.ClientOptions{ClientOptions: azcore.ClientOptions{Cloud: cloudConfig}})
	if err != nil {
		return nil, fmt.Errorf("failed to create public IP address client, %w", err)
	}

	publicIPAddress := NewPublicIPAddress(infraID, location)

	// Max total retry wait ~155s (5+10+20+40+80), suitable for infra provisioning.
	backoff := wait.Backoff{
		Steps:    5,
		Duration: 5 * time.Second,
		Factor:   2.0,
		Jitter:   0.1,
	}

	var result *armnetwork.PublicIPAddress
	// BeginCreateOrUpdate is idempotent (CreateOrUpdate), so retries that re-submit
	// the creation request after a transient failure on PollUntilDone are safe.
	err = retry.OnError(backoff, isAzureConflictError, func() error {
		pollerResp, err := publicIPAddressClient.BeginCreateOrUpdate(
			ctx,
			resourceGroupName,
			infraID,
			publicIPAddress,
			nil,
		)
		if err != nil {
			if isAzureConflictError(err) {
				log.Info("Transient error creating public IP address, will retry", "error", err)
			}
			return err
		}

		resp, err := pollerResp.PollUntilDone(ctx, nil)
		if err != nil {
			if isAzureConflictError(err) {
				log.Info("Transient error waiting for public IP address creation, will retry", "error", err)
			}
			return err
		}
		result = &resp.PublicIPAddress
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("failed to create public IP address: %w", err)
	}
	return result, nil
}

// newFrontendIPConfiguration creates a frontend IP configuration for a load balancer.
// The frontend configuration defines the public-facing IP address that clients connect to.
// It uses dynamic private IP allocation and associates with the provided public IP address.
//
// Parameters:
//   - name: Name for the frontend IP configuration
//   - publicIPAddress: The public IP address to associate with this frontend
//
// Returns a frontend IP configuration suitable for a Standard Load Balancer.
func newFrontendIPConfiguration(name string, publicIPAddress *armnetwork.PublicIPAddress) *armnetwork.FrontendIPConfiguration {
	return &armnetwork.FrontendIPConfiguration{
		Name: ptr.To(name),
		Properties: &armnetwork.FrontendIPConfigurationPropertiesFormat{
			PrivateIPAllocationMethod: ptr.To(armnetwork.IPAllocationMethodDynamic),
			PublicIPAddress:           publicIPAddress,
		},
	}
}

// newBackendAddressPool creates a backend address pool for a load balancer.
// The backend pool contains the network interfaces of VMs that will receive traffic
// distributed by the load balancer. VMs must be added to this pool to receive load-balanced traffic.
//
// Parameters:
//   - name: Name for the backend address pool
//
// Returns a backend address pool configuration.
func newBackendAddressPool(name string) *armnetwork.BackendAddressPool {
	return &armnetwork.BackendAddressPool{
		Name: ptr.To(name),
	}
}

// newHealthProbe creates a health probe for a load balancer.
// Health probes monitor the health of backend pool members by periodically sending HTTP requests.
// The load balancer only sends traffic to instances that pass the health check.
//
// Parameters:
//   - name: Name for the health probe
//   - port: TCP port to probe (e.g., 30595 for Kubernetes node health)
//   - requestPath: HTTP path to request (e.g., "/healthz")
//
// Returns a health probe configured with:
//   - HTTP protocol
//   - 5-second probe interval
//   - 2 consecutive failures required before marking unhealthy
func newHealthProbe(name string, port int32, requestPath string) *armnetwork.Probe {
	return &armnetwork.Probe{
		Name: ptr.To(name),
		Properties: &armnetwork.ProbePropertiesFormat{
			Protocol:          ptr.To(armnetwork.ProbeProtocolHTTP),
			Port:              ptr.To(port),
			IntervalInSeconds: ptr.To[int32](5),
			ProbeThreshold:    ptr.To[int32](2),
			RequestPath:       ptr.To(requestPath),
		},
	}
}

// newOutboundRule creates an outbound rule for a load balancer to enable egress connectivity.
// Outbound rules provide explicit control over SNAT (Source Network Address Translation) for backend
// pool members to reach the internet. This configuration follows Azure's recommended approach for
// managing outbound connectivity: https://learn.microsoft.com/en-us/azure/load-balancer/load-balancer-outbound-connections#outboundrules
//
// Parameters:
//   - name: Name for the outbound rule
//   - idPrefix: Azure resource ID prefix for constructing full resource references
//   - loadBalancerName: Name of the parent load balancer
//   - infraID: Infrastructure identifier used for resource naming
//
// Returns an outbound rule configured with:
//   - All protocols (TCP and UDP)
//   - 1024 allocated outbound ports per backend instance
//   - TCP reset enabled for idle connections
//   - 4-minute idle timeout
func newOutboundRule(name string, idPrefix string, loadBalancerName string, infraID string) *armnetwork.OutboundRule {
	return &armnetwork.OutboundRule{
		Name: ptr.To(name),
		Properties: &armnetwork.OutboundRulePropertiesFormat{
			BackendAddressPool: &armnetwork.SubResource{
				ID: ptr.To(fmt.Sprintf("/%s/%s/backendAddressPools/%s", idPrefix, loadBalancerName, infraID)),
			},
			FrontendIPConfigurations: []*armnetwork.SubResource{
				{
					ID: ptr.To(fmt.Sprintf("/%s/%s/frontendIPConfigurations/%s", idPrefix, loadBalancerName, infraID)),
				},
			},
			Protocol:               ptr.To(armnetwork.LoadBalancerOutboundRuleProtocolAll),
			AllocatedOutboundPorts: ptr.To[int32](1024),
			EnableTCPReset:         ptr.To(true),
			IdleTimeoutInMinutes:   ptr.To[int32](4),
		},
	}
}

// NewLoadBalancer creates a LoadBalancer struct configured for guest cluster egress traffic.
// This load balancer is used to provide outbound internet connectivity for nodes in the hosted cluster.
// The Azure cloud provider can later reuse this load balancer to add additional public IP addresses
// and load balancing rules for services of type LoadBalancer.
//
// The load balancer includes:
//   - Frontend IP configuration with a public IP address
//   - Backend address pool for guest cluster nodes
//   - Health probe for monitoring node health
//   - Outbound rule for explicit egress SNAT configuration
//
// Parameters:
//   - location: Azure region where the load balancer will be created
//   - infraID: Infrastructure identifier used for naming components
//   - idPrefix: Azure resource ID prefix for constructing component references
//   - loadBalancerName: Name for the load balancer resource
//   - publicIPAddress: Public IP address to use for the frontend configuration
//
// Returns a fully configured armnetwork.LoadBalancer with Standard SKU.
func NewLoadBalancer(location string, infraID string, idPrefix string, loadBalancerName string, publicIPAddress *armnetwork.PublicIPAddress) armnetwork.LoadBalancer {
	return armnetwork.LoadBalancer{
		Location: ptr.To(location),
		SKU: &armnetwork.LoadBalancerSKU{
			Name: ptr.To(armnetwork.LoadBalancerSKUNameStandard),
		},
		Properties: &armnetwork.LoadBalancerPropertiesFormat{
			FrontendIPConfigurations: []*armnetwork.FrontendIPConfiguration{
				newFrontendIPConfiguration(infraID, publicIPAddress),
			},
			BackendAddressPools: []*armnetwork.BackendAddressPool{
				newBackendAddressPool(infraID),
			},
			Probes: []*armnetwork.Probe{
				newHealthProbe(infraID, 30595, "/healthz"),
			},
			OutboundRules: []*armnetwork.OutboundRule{
				newOutboundRule(infraID, idPrefix, loadBalancerName, infraID),
			},
		},
	}
}

// CreateLoadBalancer creates a load balancer (LB) with an outbound rule for guest cluster egress; azure cloud provider will reuse this LB to add a public ip address and the load balancer rules
func (n *NetworkManager) CreateLoadBalancer(ctx context.Context, resourceGroupName string, infraID string, location string, publicIPAddress *armnetwork.PublicIPAddress) error {
	idPrefix := fmt.Sprintf("subscriptions/%s/resourceGroups/%s/providers/Microsoft.Network/loadBalancers", n.subscriptionID, resourceGroupName)
	loadBalancerName := infraID

	cloudConfig, err := azureutil.GetAzureCloudConfiguration(n.cloud)
	if err != nil {
		return fmt.Errorf("failed to get cloud configuration: %w", err)
	}
	loadBalancerClient, err := armnetwork.NewLoadBalancersClient(n.subscriptionID, n.creds, &arm.ClientOptions{ClientOptions: azcore.ClientOptions{Cloud: cloudConfig}})
	if err != nil {
		return fmt.Errorf("failed to create load balancer client, %w", err)
	}

	loadBalancer := NewLoadBalancer(location, infraID, idPrefix, loadBalancerName, publicIPAddress)
	pollerResp, err := loadBalancerClient.BeginCreateOrUpdate(ctx, resourceGroupName, loadBalancerName, loadBalancer, nil)

	if err != nil {
		return fmt.Errorf("failed to create guest cluster egress load balancer: %w", err)
	}

	_, err = pollerResp.PollUntilDone(ctx, nil)
	if err != nil {
		return fmt.Errorf("failed waiting to create guest cluster egress load balancer: %w", err)
	}
	return nil
}
